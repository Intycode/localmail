// Package graph implements the Microsoft Graph mail transport (Outlook):
// it maps the Graph REST API onto the provider.MailProvider contract, which
// is UID-centric. The mapping is documented and deliberately simple:
//
//   - a message's UID is the epoch seconds of its receivedDateTime, so
//     incremental sync becomes "$filter=receivedDateTime gt <lastUID>".
//   - folders.imap_name stores Graph well-known folder names (inbox,
//     sentitems, drafts, deleteditems, archive, junkemail), which the API
//     accepts in the URL path: no folder discovery needed.
//
// Known v1 limitations (by design, documented in docs/roadmap.md): push
// (IDLE) is unavailable → the watcher falls back to its 5 minute poll;
// deletion reconciliation only covers the synced window; drafts stay
// local-only (Graph draft creation would need a different endpoint).
package graph

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mail/backend/internal/domain"
	"mail/backend/internal/email/imap"
	"mail/backend/internal/email/smtp"
	"mail/backend/internal/provider"
)

const defaultBase = "https://graph.microsoft.com"

// initialWindow matches the IMAP provider: how many messages a first sync
// pulls per folder.
const initialWindow = 50

// WellKnownFolders are the local folders an Outlook account gets at add
// time; their names are the Graph well-known identifiers used in URLs.
var WellKnownFolders = []imap.FolderInfo{
	{Name: "inbox", SpecialUse: "\\Inbox"},
	{Name: "sentitems", SpecialUse: "\\Sent"},
	{Name: "drafts", SpecialUse: "\\Drafts"},
	{Name: "deleteditems", SpecialUse: "\\Trash"},
	{Name: "archive", SpecialUse: "\\Archive"},
	{Name: "junkemail", SpecialUse: "\\Junk"},
}

// graphMessage is the slim projection used for listings.
type graphMessage struct {
	ID               string `json:"id"`
	Subject          string `json:"subject"`
	ReceivedDateTime string `json:"receivedDateTime"`
	IsRead           bool   `json:"isRead"`
}

// Provider is one Graph session bound to an access token (delivered fresh
// by the Rust bridge: the refresh token never leaves the keychain).
type Provider struct {
	base     string
	username string
	token    string
	httpc    *http.Client
}

// New builds a Graph provider. base may be empty (default endpoint).
func New(base, username, accessToken string) *Provider {
	if base == "" {
		base = defaultBase
	}
	return &Provider{
		base:     strings.TrimRight(base, "/"),
		username: username,
		token:    accessToken,
		httpc:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Close implements provider.Closer: no persistent resources.
func (p *Provider) Close() {}

// Ping validates the credential with a cheap profile call.
func (p *Provider) Ping() error {
	var out map[string]any
	if err := p.getJSON("/v1.0/me?$select=id", &out); err != nil {
		return fmt.Errorf("graph: credenziali non valide: %w", err)
	}
	return nil
}

// api performs an authenticated request and decodes a JSON body.
func (p *Provider) api(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, p.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("graph %s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (p *Provider) getJSON(path string, out any) error { return p.api("GET", path, nil, out) }

// listMessages returns the messages of a well-known folder, newest first.
func (p *Provider) listMessages(folder, filter, top string) ([]graphMessage, error) {
	path := folderPath(folder) +
		"?$select=id,receivedDateTime,isRead&$orderby=receivedDateTime desc&$top=" + top
	if filter != "" {
		path += "&$filter=" + url.QueryEscape(filter)
	}
	var out struct {
		Value []graphMessage `json:"value"`
	}
	if err := p.getJSON(path, &out); err != nil {
		return nil, err
	}
	return out.Value, nil
}

// fetchRawByID downloads one full MIME message by Graph id.
func (p *Provider) fetchRawByID(folder, id string) ([]byte, error) {
	req, err := http.NewRequest("GET",
		p.base+"/v1.0/me/mailFolders/"+url.PathEscape(folder)+"/messages/"+url.PathEscape(id)+"/$value", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("graph fetch raw: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

// epochOf converts a receivedDateTime (RFC 3339) into the UID we expose.
func epochOf(received string) (uint32, error) {
	t, err := time.Parse(time.RFC3339, received)
	if err != nil {
		return 0, err
	}
	return uint32(t.UTC().Unix()), nil
}

// folderPath builds the messages URL for a well-known folder name.
func folderPath(folder string) string {
	return "/v1.0/me/mailFolders/" + url.PathEscape(folder) + "/messages"
}

// uidToID maps engine UIDs (epoch seconds) back to Graph message ids by
// listing the folder's recent window. Messages older than the window cannot
// be addressed: operations on them are skipped silently (same policy as
// the server-side reconciliation window).
func (p *Provider) uidToID(folder string, uids []uint32) (map[uint32]string, error) {
	msgs, err := p.listMessages(folder, "", "500")
	if err != nil {
		return nil, err
	}
	out := make(map[uint32]string, len(msgs))
	for _, m := range msgs {
		uid, err := epochOf(m.ReceivedDateTime)
		if err != nil {
			continue
		}
		out[uid] = m.ID
	}
	return out, nil
}

// --- provider.MailProvider ---

// SyncFolder pulls one folder's new messages (or the newest initialWindow
// on the first sync). AllUIDs covers the fetched window only: deletions
// outside it reconcile on the next full resync.
func (p *Provider) SyncFolder(ref provider.FolderRef) (provider.FolderSync, error) {
	folder := ref.MailboxName
	var msgs []graphMessage
	var err error
	if ref.LastUID == 0 {
		// first sync: newest window, newest first, then reverse for UIDs
		msgs, err = p.listMessages(folder, "", fmt.Sprint(initialWindow))
	} else {
		since := time.Unix(int64(ref.LastUID), 0).UTC().Format("2006-01-02T15:04:05Z")
		msgs, err = p.listMessages(folder, "receivedDateTime gt "+since, fmt.Sprint(initialWindow))
	}
	if err != nil {
		return provider.FolderSync{}, err
	}

	out := provider.FolderSync{UIDValidity: 1}
	for i := len(msgs) - 1; i >= 0; i-- { // ascending UID order
		m := msgs[i]
		uid, uerr := epochOf(m.ReceivedDateTime)
		if uerr != nil {
			continue
		}
		raw, rerr := p.fetchRawByID(folder, m.ID)
		if rerr != nil {
			return provider.FolderSync{}, rerr
		}
		flags := []string(nil)
		if m.IsRead {
			flags = append(flags, "\\Seen")
		}
		out.Messages = append(out.Messages, provider.RawMessage{UID: uid, Flags: flags, Data: raw})
		out.AllUIDs = append(out.AllUIDs, uid)
	}
	return out, nil
}

func (p *Provider) MarkRead(mailbox string, uids []uint32) error {
	return p.patchRead(mailbox, uids, true)
}

func (p *Provider) MarkUnread(mailbox string, uids []uint32) error {
	return p.patchRead(mailbox, uids, false)
}

func (p *Provider) patchRead(mailbox string, uids []uint32, isRead bool) error {
	ids, err := p.uidToID(mailbox, uids)
	if err != nil {
		return err
	}
	for _, uid := range uids {
		id, ok := ids[uid]
		if !ok {
			continue
		}
		if err := p.api("PATCH", "/v1.0/me/mailFolders/"+url.PathEscape(mailbox)+"/messages/"+url.PathEscape(id),
			map[string]bool{"isRead": isRead}, nil); err != nil {
			return err
		}
	}
	return nil
}

// Move moves messages to another folder (target is the destination's
// well-known name or Graph id, exactly as stored in folders.imap_name).
func (p *Provider) Move(mailbox string, uids []uint32, target string) error {
	ids, err := p.uidToID(mailbox, uids)
	if err != nil {
		return err
	}
	for _, uid := range uids {
		id, ok := ids[uid]
		if !ok {
			continue
		}
		if err := p.api("POST", "/v1.0/me/messages/"+url.PathEscape(id)+"/move",
			map[string]string{"destinationId": target}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) Delete(mailbox string, uids []uint32) error {
	ids, err := p.uidToID(mailbox, uids)
	if err != nil {
		return err
	}
	for _, uid := range uids {
		id, ok := ids[uid]
		if !ok {
			continue
		}
		if err := p.api("DELETE", "/v1.0/me/mailFolders/"+url.PathEscape(mailbox)+"/messages/"+url.PathEscape(id), nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// Send delivers via Graph sendMail: text/HTML body, PGP already handled
// upstream, attachments embedded as base64 fileAttachments (their bytes
// were loaded by smtp.LoadFiles before provider dispatch). Graph saves the
// delivered message into Sent Items itself; the locally composed raw bytes
// (RFC 5322) are returned for the local Sent copy.
func (p *Provider) Send(from domain.Recipient, msg domain.ComposeMessage) ([]byte, error) {
	recipients := func(rs []domain.Recipient) []map[string]any {
		out := make([]map[string]any, 0, len(rs))
		for _, r := range rs {
			if strings.TrimSpace(r.Email) == "" {
				continue
			}
			entry := map[string]any{"emailAddress": map[string]string{"address": r.Email}}
			if r.Name != "" {
				entry["emailAddress"].(map[string]string)["name"] = r.Name
			}
			out = append(out, entry)
		}
		return out
	}
	message := map[string]any{
		"subject": msg.Subject,
		"body": map[string]string{
			"contentType": "Text",
			"content":     msg.Body,
		},
	}
	if to := recipients(msg.To); len(to) > 0 {
		message["toRecipients"] = to
	}
	if cc := recipients(msg.CC); len(cc) > 0 {
		message["ccRecipients"] = cc
	}
	if bcc := recipients(msg.BCC); len(bcc) > 0 {
		message["bccRecipients"] = bcc
	}
	if len(msg.Files) > 0 {
		atts := make([]map[string]any, 0, len(msg.Files))
		for _, f := range msg.Files {
			atts = append(atts, map[string]any{
				"@odata.type":  "#microsoft.graph.fileAttachment",
				"name":         f.Name,
				"contentType":  f.ContentType,
				"contentBytes": base64.StdEncoding.EncodeToString(f.Data),
			})
		}
		message["attachments"] = atts
	}
	if err := p.api("POST", "/v1.0/me/sendMail",
		map[string]any{"message": message, "saveToSentItems": true}, nil); err != nil {
		return nil, err
	}
	// local Sent copy: compose the RFC 5322 representation (the engine
	// parses + stores it, and dedupes on the next Sent sync)
	return smtp.Compose(from, msg), nil
}

// Append is a no-op on Graph: sent messages are saved server-side by
// sendMail, drafts stay local (v1 limitation). Returning 0 keeps the
// engine's best-effort semantics.
func (p *Provider) Append(string, []byte, []string, time.Time) (uint32, error) { return 0, nil }

// Idle is unsupported on Graph v1: the watcher falls back to its poll.
func (p *Provider) Idle(string, chan<- struct{}) (func(), error) {
	return nil, imap.ErrIdleUnsupported
}

// FetchRaw downloads one message's raw MIME (on-demand attachments).
func (p *Provider) FetchRaw(mailbox string, uid uint32) ([]byte, error) {
	ids, err := p.uidToID(mailbox, []uint32{uid})
	if err != nil {
		return nil, err
	}
	id, ok := ids[uid]
	if !ok {
		return nil, fmt.Errorf("graph: messaggio %d non trovato in %s", uid, mailbox)
	}
	return p.fetchRawByID(mailbox, id)
}

// Folders returns the well-known Outlook folder set (used at add time).
func (p *Provider) Folders() ([]imap.FolderInfo, error) {
	return WellKnownFolders, nil
}
