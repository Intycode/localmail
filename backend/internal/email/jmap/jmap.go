// Package jmap implements a JMAP (RFC 8620/8621) mail transport.
//
// It adapts JMAP's id-based model to the sync engine's UID contract:
//   - a persistent SQLite map (jmap_map, migration 0009) assigns each Email
//     id a stable 32-bit uid per folder; emails that merely move between
//     mailboxes are never re-downloaded (account-wide id lookup);
//   - message payloads come from the server blob store (the raw RFC 5322
//     source), so parsing/persistence is byte-identical to IMAP;
//   - push uses the JMAP EventSource endpoint.
//
// The connection config reuses imap.Config: Host carries the JMAP *session
// URL* (https://…/jmap/session), Username the account identity. The secret
// is the Bearer token (or app password) from the keychain.
package jmap

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// JMAP capability URIs.
const (
	capCore = "urn:ietf:params:jmap:core"
	capMail = "urn:ietf:params:jmap:mail"
)

// Session is the JMAP session object (RFC 8620 §2).
type Session struct {
	Accounts        map[string]Account `json:"accounts"`
	PrimaryAccounts map[string]string  `json:"primaryAccounts"`
	APIUrl          string             `json:"apiUrl"`
	DownloadURL     string             `json:"downloadUrl"`
	UploadURL       string             `json:"uploadUrl"`
	EventSourceURL  string             `json:"eventSourceUrl"`
}

// Account is one server account with its capabilities.
type Account struct {
	Name         string                      `json:"name"`
	Capabilities map[string]json.RawMessage `json:"capabilities"`
}

// Mailbox is a JMAP Mailbox object (subset).
type Mailbox struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	Role *string `json:"role"`
}

func (m Mailbox) role() string {
	if m.Role == nil {
		return ""
	}
	return strings.ToLower(*m.Role)
}

// Email headers/parts fetched on demand (subset).
type Email struct {
	ID       string            `json:"id"`
	BlobID   string            `json:"blobId"`
	Keywords map[string]bool   `json:"keywords"`
}

// methodResponses entries are positional arrays: [name, args, callID].
type apiResponse struct {
	MethodResponses [][]json.RawMessage `json:"methodResponses"`
}

type client struct {
	db     *sql.DB
	cfg    SessionConfig
	token  string
	http   *http.Client
	mu     sync.Mutex
	session *Session
	boxes   []Mailbox
	accountID string
}

// SessionConfig carries the connection settings (alias-free subset of
// imap.Config to keep the two providers decoupled).
type SessionConfig struct {
	SessionURL string
	Username   string
}

func newClient(db *sql.DB, sessionURL, username, token string) *client {
	return &client{
		db:     db,
		cfg:    SessionConfig{SessionURL: sessionURL, Username: username},
		token:  token,
		http:   &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *client) auth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
}

// ensureSession fetches (once per provider instance) the JMAP session and
// resolves the mail-capable account id.
func (c *client) ensureSession() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		return nil
	}
	req, err := http.NewRequest(http.MethodGet, c.cfg.SessionURL, nil)
	if err != nil {
		return err
	}
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sessione JMAP non raggiungibile: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("token JMAP non valido o scaduto (401)")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("sessione JMAP: status %d: %s", resp.StatusCode, body)
	}
	var s Session
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return fmt.Errorf("sessione JMAP non valida: %w", err)
	}
	accID := s.PrimaryAccounts[capCore]
	if accID == "" {
		for id := range s.Accounts {
			if _, ok := s.Accounts[id].Capabilities[capMail]; ok {
				accID = id
				break
			}
		}
	}
	if accID == "" {
		return fmt.Errorf("nessun account con capability mail sul server JMAP")
	}
	c.session = &s
	c.accountID = accID
	return nil
}

// call runs one JMAP invocation and returns the args of the response with
// the same call id (or an error from the server's method-level error).
func (c *client) call(name string, args map[string]any, callID string) (json.RawMessage, error) {
	if err := c.ensureSession(); err != nil {
		return nil, err
	}
	body, err := json.Marshal([][]any{{name, args, callID}})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.session.APIUrl, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.auth(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chiamata %s non riuscita: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("token JMAP non valido o scaduto (401)")
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%s: status %d: %s", name, resp.StatusCode, b)
	}
	var ar apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return nil, err
	}
	for _, mr := range ar.MethodResponses {
		if len(mr) < 2 {
			continue
		}
		var rname, rcall string
		_ = json.Unmarshal(mr[0], &rname)
		if len(mr) > 2 {
			_ = json.Unmarshal(mr[2], &rcall)
		}
		if rcall != callID {
			continue
		}
		if rname == "error" {
			return nil, fmt.Errorf("%s: errore server: %s", name, string(mr[1]))
		}
		return mr[1], nil
	}
	return nil, fmt.Errorf("%s: risposta senza callID %s", name, callID)
}

// mailboxes lists every mailbox of the account (cached per instance).
func (c *client) mailboxes() ([]Mailbox, error) {
	c.mu.Lock()
	if c.boxes != nil {
		b := c.boxes
		c.mu.Unlock()
		return b, nil
	}
	c.mu.Unlock()
	q, err := c.call("Mailbox/query", map[string]any{
		"accountId":       c.accountID,
		"sort":            []map[string]any{{"property": "sortOrder"}},
		"calculateTotal":  true,
	}, "q")
	if err != nil {
		return nil, err
	}
	var qr struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(q, &qr); err != nil {
		return nil, err
	}
	g, err := c.call("Mailbox/get", map[string]any{
		"accountId": c.accountID,
		"ids":       qr.IDs,
	}, "g")
	if err != nil {
		return nil, err
	}
	var gr struct {
		List []Mailbox `json:"list"`
	}
	if err := json.Unmarshal(g, &gr); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.boxes == nil {
		c.boxes = gr.List
	}
	b := c.boxes
	c.mu.Unlock()
	return b, nil
}

// resolveMailbox maps a folder role or display name to the JMAP mailbox id.
func (c *client) resolveMailbox(roleOrName string) (string, error) {
	boxes, err := c.mailboxes()
	if err != nil {
		return "", err
	}
	role := normalizeRole(roleOrName)
	for _, b := range boxes {
		if b.role() == role && role != "" {
			return b.ID, nil
		}
	}
	for _, b := range boxes {
		if strings.EqualFold(b.Name, roleOrName) {
			return b.ID, nil
		}
	}
	return "", fmt.Errorf("mailbox %q non trovata sul server", roleOrName)
}

// normalizeRole maps folder names/types to JMAP mailbox roles.
func normalizeRole(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "inbox":
		return "inbox"
	case "sent":
		return "sent"
	case "drafts", "draft":
		return "drafts"
	case "trash":
		return "trash"
	case "archive":
		return "archive"
	}
	return ""
}

// queryEmailIDs returns every Email id currently in the mailbox, sorted by
// receivedAt ascending (uid order follows time order).
func (c *client) queryEmailIDs(mailboxID string) ([]string, error) {
	var ids []string
	for position := 0; ; position += 500 {
		q, err := c.call("Email/query", map[string]any{
			"accountId":       c.accountID,
			"filter":          map[string]any{"inMailbox": mailboxID},
			"sort":            []map[string]any{{"property": "receivedAt"}},
			"position":        position,
			"limit":           500,
			"calculateTotal":  true,
		}, "q")
		if err != nil {
			return nil, err
		}
		var qr struct {
			IDs   []string `json:"ids"`
			Total int      `json:"total"`
		}
		if err := json.Unmarshal(q, &qr); err != nil {
			return nil, err
		}
		ids = append(ids, qr.IDs...)
		if position+len(qr.IDs) >= qr.Total || len(qr.IDs) == 0 {
			return ids, nil
		}
	}
}

// getEmailProps fetches the given properties for a batch of Email ids.
func (c *client) getEmailProps(ids []string, props []string) (map[string]Email, error) {
	out := map[string]Email{}
	for start := 0; start < len(ids); start += 200 {
		batch := ids[start:]
		if len(batch) > 200 {
			batch = batch[:200]
		}
		g, err := c.call("Email/get", map[string]any{
			"accountId":  c.accountID,
			"ids":        batch,
			"properties": props,
		}, "g")
		if err != nil {
			return nil, err
		}
		var gr struct {
			List    []Email `json:"list"`
			NotFound []string `json:"notFound"`
		}
		if err := json.Unmarshal(g, &gr); err != nil {
			return nil, err
		}
		for _, e := range gr.List {
			out[e.ID] = e
		}
	}
	return out, nil
}

// downloadBlob fetches raw bytes from the server blob store.
func (c *client) downloadBlob(blobID string) ([]byte, error) {
	if err := c.ensureSession(); err != nil {
		return nil, err
	}
	url := c.session.DownloadURL
	url = strings.ReplaceAll(url, "{accountId}", c.accountID)
	url = strings.ReplaceAll(url, "{blobId}", blobID)
	url = strings.ReplaceAll(url, "{name}", "message.eml")
	url = strings.ReplaceAll(url, "{type}", "message/rfc822")
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("download blob: status %d: %s", resp.StatusCode, b)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// uploadBlob pushes raw bytes into the blob store, returning the blobId.
func (c *client) uploadBlob(raw []byte) (string, error) {
	if err := c.ensureSession(); err != nil {
		return "", err
	}
	url := c.session.UploadURL
	url = strings.ReplaceAll(url, "{accountId}", c.accountID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	c.auth(req)
	req.Header.Set("Content-Type", "message/rfc822")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", fmt.Errorf("upload blob: status %d: %s", resp.StatusCode, b)
	}
	var ur struct {
		BlobID string `json:"blobId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ur); err != nil {
		return "", err
	}
	if ur.BlobID == "" {
		return "", fmt.Errorf("upload blob: blobId mancante")
	}
	return ur.BlobID, nil
}
