// provider.go — the provider.MailProvider implementation over JMAP.
//
// UID emulation: every Email id gets a stable per-folder numeric uid stored
// in jmap_map (account-wide lookups also skip re-downloading emails that
// merely moved between mailboxes). Folder reconciliation rides on the
// engine's existing difference() logic via AllUIDs.
package jmap

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"mail/backend/internal/domain"
	"mail/backend/internal/email/imap"
	"mail/backend/internal/email/smtp"
	"mail/backend/internal/provider"
)

// Provider implements provider.MailProvider over JMAP.
type Provider struct {
	c     *client
	db    *sql.DB
	token string
}

// New builds a JMAP provider. sessionURL is the JMAP session URL; token is
// the Bearer credential (keychain).
func New(db *sql.DB, sessionURL, username, token string) *Provider {
	return &Provider{
		c:  newClient(db, sessionURL, username, token),
		db: db,
	}
}

// Close is a no-op: JMAP keeps no persistent TCP session (HTTP only).
func (p *Provider) Close() {}

// Ping validates the credentials with a cheap session fetch.
func (p *Provider) Ping() error { return p.c.ensureSession() }

// accountID resolves (once) the JMAP account id via the session.
func (p *Provider) accountID() string {
	if err := p.c.ensureSession(); err != nil {
		return ""
	}
	return p.c.accountID
}

// Folders returns the server mailboxes mapped to canonical folder names and
// RFC 6154 special-use flags, so opAccountsAdd can reuse mapFolders. The
// folder name stored locally doubles as the resolution key (role first,
// then display name).
func (p *Provider) Folders() ([]imap.FolderInfo, error) {
	boxes, err := p.c.mailboxes()
	if err != nil {
		return nil, err
	}
	var out []imap.FolderInfo
	for _, b := range boxes {
		switch b.role() {
		case "inbox":
			out = append(out, imap.FolderInfo{Name: "INBOX", SpecialUse: "\\Inbox"})
		case "sent":
			out = append(out, imap.FolderInfo{Name: "Sent", SpecialUse: "\\Sent"})
		case "drafts":
			out = append(out, imap.FolderInfo{Name: "Drafts", SpecialUse: "\\Drafts"})
		case "trash":
			out = append(out, imap.FolderInfo{Name: "Trash", SpecialUse: "\\Trash"})
		case "archive":
			out = append(out, imap.FolderInfo{Name: "Archive", SpecialUse: "\\Archive"})
		default:
			if b.Name == "" {
				continue
			}
			out = append(out, imap.FolderInfo{Name: b.Name})
		}
	}
	return out, nil
}

// --- mapping helpers ---

// loadMap returns the folder's email_id->uid rows and the account-wide set
// of known email ids (used to skip moved emails).
func (p *Provider) loadMap(accountID, folderID string) (map[string]uint32, map[string]struct{}, error) {
	rows, err := p.db.Query(
		"SELECT folder_id, email_id, uid FROM jmap_map WHERE account_id=?",
		accountID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	folder := map[string]uint32{}
	known := map[string]struct{}{}
	for rows.Next() {
		var fid, emailID string
		var uid uint32
		if err := rows.Scan(&fid, &emailID, &uid); err != nil {
			return nil, nil, err
		}
		known[emailID] = struct{}{}
		if fid == folderID {
			folder[emailID] = uid
		}
	}
	return folder, known, rows.Err()
}

func (p *Provider) saveMapRows(accountID, folderID string, rows map[string]uint32) error {
	tx, err := p.db.Begin()
	if err != nil {
		return err
	}
	for emailID, uid := range rows {
		if _, err := tx.Exec(
			"INSERT OR REPLACE INTO jmap_map(account_id, folder_id, email_id, uid) VALUES(?,?,?,?)",
			accountID, folderID, emailID, uid,
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// resolveUIDs maps uids back to email ids for flag/move/delete ops. uids
// missing from the folder map are silently ignored (already gone remotely).
func (p *Provider) resolveUIDs(accountID, folderID string, uids []uint32) (map[uint32]string, error) {
	rows, err := p.db.Query(
		"SELECT email_id, uid FROM jmap_map WHERE account_id=? AND folder_id=?",
		accountID, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byUID := map[uint32]string{}
	for rows.Next() {
		var emailID string
		var uid uint32
		if err := rows.Scan(&emailID, &uid); err != nil {
			return nil, err
		}
		byUID[uid] = emailID
	}
	out := make(map[uint32]string, len(uids))
	for _, uid := range uids {
		if id, ok := byUID[uid]; ok {
			out[uid] = id
		}
	}
	return out, rows.Err()
}

func keywordsToFlags(kw map[string]bool) []string {
	var flags []string
	if kw["$seen"] {
		flags = append(flags, "\\Seen")
	}
	if kw["$flagged"] {
		flags = append(flags, "\\Flagged")
	}
	if kw["$draft"] {
		flags = append(flags, "\\Draft")
	}
	if kw["$answered"] {
		flags = append(flags, "\\Answered")
	}
	return flags
}

// --- provider.MailProvider ---

// SyncFolder pulls one folder's state from the server.
func (p *Provider) SyncFolder(ref provider.FolderRef) (provider.FolderSync, error) {
	accountID := p.accountID()
	if accountID == "" {
		return provider.FolderSync{}, fmt.Errorf("account JMAP non risolto")
	}
	mailboxID, err := p.c.resolveMailbox(ref.MailboxName)
	if err != nil {
		// unknown mailbox (custom folder without a server counterpart):
		// report the current mapping so the engine's reconciliation is a no-op
		folder, _, lerr := p.loadMap(accountID, ref.MailboxName)
		if lerr != nil {
			return provider.FolderSync{}, lerr
		}
		all := make([]uint32, 0, len(folder))
		for _, uid := range folder {
			all = append(all, uid)
		}
		return provider.FolderSync{UIDValidity: 1, AllUIDs: all}, nil
	}

	folder, known, err := p.loadMap(accountID, ref.MailboxName)
	if err != nil {
		return provider.FolderSync{}, err
	}

	ids, err := p.c.queryEmailIDs(mailboxID)
	if err != nil {
		return provider.FolderSync{}, err
	}
	serverSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		serverSet[id] = struct{}{}
	}

	// new emails: ids never seen account-wide (moved emails keep their row
	// under the source folder and are skipped here — no re-download)
	var newIDs []string
	for _, id := range ids {
		if _, ok := known[id]; !ok {
			newIDs = append(newIDs, id)
		}
	}

	var msgs []provider.RawMessage
	next := ref.LastUID
	for _, uid := range folder {
		if uid > next {
			next = uid
		}
	}
	if len(newIDs) > 0 {
		props, err := p.c.getEmailProps(newIDs, []string{"blobId", "keywords"})
		if err != nil {
			return provider.FolderSync{}, err
		}
		for _, id := range newIDs {
			e, ok := props[id]
			if !ok || e.BlobID == "" {
				continue
			}
			raw, derr := p.c.downloadBlob(e.BlobID)
			if derr != nil {
				continue // skip undownloadable messages; never abort the sync
			}
			next++
			folder[id] = next
			msgs = append(msgs, provider.RawMessage{UID: next, Flags: keywordsToFlags(e.Keywords), Data: raw})
		}
	}

	// persist the mapping; prune rows for emails gone from THIS mailbox
	for id := range folder {
		if _, ok := serverSet[id]; !ok {
			delete(folder, id)
		}
	}
	if err := p.saveMapRows(accountID, ref.MailboxName, folder); err != nil {
		return provider.FolderSync{}, err
	}

	all := make([]uint32, 0, len(ids))
	for _, id := range ids {
		if uid, ok := folder[id]; ok {
			all = append(all, uid)
		}
	}
	return provider.FolderSync{UIDValidity: 1, Messages: msgs, AllUIDs: all}, nil
}

// setEmailKeywords fetches the current keywords of the emails and applies a
// patch (Email keywords are a wholesale-set property).
func (p *Provider) setEmailKeywords(mailbox string, uids []uint32, patch map[string]*bool) error {
	accountID := p.accountID()
	byUID, err := p.resolveUIDs(accountID, mailbox, uids)
	if err != nil {
		return err
	}
	if len(byUID) == 0 {
		return nil
	}
	ids := make([]string, 0, len(byUID))
	for _, id := range byUID {
		ids = append(ids, id)
	}
	props, err := p.c.getEmailProps(ids, []string{"keywords"})
	if err != nil {
		return err
	}
	updates := map[string]any{}
	for _, id := range ids {
		kw := map[string]bool{}
		if e, ok := props[id]; ok {
			for k, v := range e.Keywords {
				kw[k] = v
			}
		}
		for k, v := range patch {
			if v == nil {
				delete(kw, k)
			} else {
				kw[k] = *v
			}
		}
		updates[id] = map[string]any{"keywords": kw}
	}
	_, err = p.c.call("Email/set", map[string]any{
		"accountId": accountID,
		"update":    updates,
	}, "u")
	return err
}

// MarkRead marks messages read on the provider.
func (p *Provider) MarkRead(mailbox string, uids []uint32) error {
	yes := true
	return p.setEmailKeywords(mailbox, uids, map[string]*bool{"$seen": &yes})
}

// MarkUnread clears the read flag on the provider.
func (p *Provider) MarkUnread(mailbox string, uids []uint32) error {
	return p.setEmailKeywords(mailbox, uids, map[string]*bool{"$seen": nil})
}

// Move moves messages to another mailbox via Email/set JSON-pointer patch.
func (p *Provider) Move(mailbox string, uids []uint32, target string) error {
	accountID := p.accountID()
	src, err := p.c.resolveMailbox(mailbox)
	if err != nil {
		return err
	}
	dst, err := p.c.resolveMailbox(target)
	if err != nil {
		return err
	}
	byUID, err := p.resolveUIDs(accountID, mailbox, uids)
	if err != nil {
		return err
	}
	if len(byUID) == 0 {
		return nil
	}
	updates := map[string]any{}
	for _, id := range byUID {
		updates[id] = map[string]any{
			"mailboxIds/" + src: nil,
			"mailboxIds/" + dst: true,
		}
	}
	_, err = p.c.call("Email/set", map[string]any{
		"accountId": accountID,
		"update":    updates,
	}, "u")
	return err
}

// Delete removes messages on the provider.
func (p *Provider) Delete(mailbox string, uids []uint32) error {
	accountID := p.accountID()
	byUID, err := p.resolveUIDs(accountID, mailbox, uids)
	if err != nil {
		return err
	}
	if len(byUID) == 0 {
		return nil
	}
	ids := make([]string, 0, len(byUID))
	for _, id := range byUID {
		ids = append(ids, id)
	}
	_, err = p.c.call("Email/set", map[string]any{
		"accountId": accountID,
		"destroy":   ids,
	}, "d")
	return err
}

// Send delivers a composed message via JMAP EmailSubmission: upload blob ->
// Email/import (draft) -> EmailSubmission/set with onSuccessUpdateEmail
// moving it to Sent. Returns the locally composed raw bytes (the engine
// persists them into the Sent folder, mirroring the IMAP path).
func (p *Provider) Send(from domain.Recipient, msg domain.ComposeMessage) ([]byte, error) {
	accountID := p.accountID()
	recipients := len(msg.To) + len(msg.CC) + len(msg.BCC)
	if recipients == 0 {
		return nil, fmt.Errorf("nessun destinatario valido")
	}
	raw := smtp.Compose(from, msg)

	blobID, err := p.c.uploadBlob(raw)
	if err != nil {
		return nil, err
	}
	draftsID, err := p.c.resolveMailbox("drafts")
	if err != nil {
		return nil, err
	}
	sentID, err := p.c.resolveMailbox("sent")
	if err != nil {
		return nil, err
	}

	ident, err := p.c.call("Identity/get", map[string]any{"accountId": accountID}, "i0")
	if err != nil {
		return nil, err
	}
	var ir struct {
		List []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"list"`
	}
	if err := json.Unmarshal(ident, &ir); err != nil {
		return nil, err
	}
	if len(ir.List) == 0 {
		return nil, fmt.Errorf("nessuna identita di invio sul server JMAP")
	}
	identityID := ir.List[0].ID
	for _, i := range ir.List {
		if strings.EqualFold(i.Email, from.Email) {
			identityID = i.ID
			break
		}
	}

	if _, err := p.c.call("Email/import", map[string]any{
		"accountId": accountID,
		"emails": map[string]any{
			"#draft": map[string]any{
				"blobId":     blobID,
				"keywords":   map[string]bool{"$seen": true, "$draft": true},
				"mailboxIds": map[string]bool{draftsID: true},
			},
		},
	}, "i1"); err != nil {
		return nil, err
	}
	if _, err := p.c.call("EmailSubmission/set", map[string]any{
		"accountId": accountID,
		"create": map[string]any{
			"s0": map[string]any{
				"identityId": identityID,
				"emailId":    "#draft",
				"undoStatus": "final",
				"onSuccessUpdateEmail": map[string]any{
					"#s0": map[string]any{
						"mailboxIds": map[string]bool{sentID: true, draftsID: false},
						"keywords":   map[string]bool{"$draft": false, "$seen": true},
					},
				},
			},
		},
	}, "i2"); err != nil {
		return nil, err
	}
	return raw, nil
}

// Append delivers raw bytes into a mailbox (drafts/sent APPEND parity).
// JMAP path: upload blob -> Email/import. JMAP has no server UIDs: returns 0.
func (p *Provider) Append(mailbox string, raw []byte, flags []string, date time.Time) (uint32, error) {
	accountID := p.accountID()
	blobID, err := p.c.uploadBlob(raw)
	if err != nil {
		return 0, err
	}
	mailboxID, err := p.c.resolveMailbox(mailbox)
	if err != nil {
		return 0, err
	}
	kw := map[string]bool{}
	for _, f := range flags {
		switch f {
		case "\\Seen":
			kw["$seen"] = true
		case "\\Flagged":
			kw["$flagged"] = true
		case "\\Draft":
			kw["$draft"] = true
		}
	}
	_, err = p.c.call("Email/import", map[string]any{
		"accountId": accountID,
		"emails": map[string]any{
			"new": map[string]any{
				"blobId":     blobID,
				"keywords":   kw,
				"mailboxIds": map[string]bool{mailboxID: true},
			},
		},
	}, "i")
	if err != nil {
		return 0, err
	}
	return 0, nil
}

// FetchRaw downloads one message's raw bytes (attachments on demand).
func (p *Provider) FetchRaw(mailbox string, uid uint32) ([]byte, error) {
	accountID := p.accountID()
	byUID, err := p.resolveUIDs(accountID, mailbox, []uint32{uid})
	if err != nil {
		return nil, err
	}
	emailID, ok := byUID[uid]
	if !ok {
		return nil, fmt.Errorf("messaggio non trovato nel mapping JMAP")
	}
	props, err := p.c.getEmailProps([]string{emailID}, []string{"blobId"})
	if err != nil {
		return nil, err
	}
	e, ok := props[emailID]
	if !ok || e.BlobID == "" {
		return nil, fmt.Errorf("messaggio %s senza blob", emailID)
	}
	return p.c.downloadBlob(e.BlobID)
}

// Idle enters push mode via the JMAP EventSource endpoint.
func (p *Provider) Idle(mailbox string, notify chan<- struct{}) (stop func(), err error) {
	if err := p.c.ensureSession(); err != nil {
		return nil, err
	}
	url := p.c.session.EventSourceURL
	url = strings.ReplaceAll(url, "{types}", "Email")
	url = strings.ReplaceAll(url, "{closeafter}", "no")
	url = strings.ReplaceAll(url, "{ping}", "30")
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	p.c.auth(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, imap.ErrIdleUnsupported
	}
	done := make(chan struct{})
	var once sync.Once
	stop = func() { once.Do(func() { close(done); resp.Body.Close() }) }
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			select {
			case <-done:
				return
			default:
			}
			line := sc.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(line[5:])
			if payload == "" || payload == "ping" {
				continue
			}
			if strings.Contains(payload, "Email") {
				select {
				case notify <- struct{}{}:
				default: // coalesce bursts
				}
			}
		}
	}()
	return stop, nil
}

var _ provider.MailProvider = (*Provider)(nil)
