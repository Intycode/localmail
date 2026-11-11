// Package ipc implements the stdio JSON-lines protocol between Tauri and the
// Go engine (see docs/ipc.md).
//
// Requests  (UI -> engine): {"id":"req-1","op":"accounts.list","params":{...}}
// Responses (engine -> UI): {"id":"req-1","ok":true,"data":{...}}
//
//	or  {"id":"req-1","ok":false,"error":"..."}
//
// Events    (engine -> UI): {"kind":"sync.started","data":{...}}
//
// Security:
//   - only allowlisted ops are accepted; unknown ops get an error response
//   - per-op timeouts: a slow handler cannot wedge the protocol loop
//   - max line size caps request size
//   - secrets never appear in responses or events: handlers never echo params
//     and auth.Secret redacts itself in String()/JSON
package ipc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"mail/backend/internal/rule"
	"mail/backend/internal/pgp"
	"sync"
	"sync/atomic"
	"time"

	"mail/backend/internal/account"
	"mail/backend/internal/assess"
	"mail/backend/internal/auth"
	"mail/backend/internal/domain"
	"mail/backend/internal/email/graph"
	"mail/backend/internal/email/imap"
	"mail/backend/internal/email/jmap"
	"mail/backend/internal/email/mime"
	"mail/backend/internal/email/smtp"
	"mail/backend/internal/events"
	"mail/backend/internal/localai"
	"mail/backend/internal/mbox"
	"mail/backend/internal/message"
	"mail/backend/internal/pdfdoc"
	"mail/backend/internal/provider"
	"mail/backend/internal/queue"
	"mail/backend/internal/search"
	"mail/backend/internal/semantic"
	mailsync "mail/backend/internal/sync"
	"mail/backend/internal/thread"
)

// maxLine caps the request line size (anti-DoS). The vision path ships
// base64 JPEG pages inside ai.chat: 8 MB keeps ~10 rendered pages headroom
// while still bounding any single request.
const maxLine = 8 << 20

// Op timeouts: reads are fast, a full sync may take minutes. A local LLM
// generating a summary can take a while too (120 s cap).
const (
	defaultTimeout = 30 * time.Second
	addTimeout     = 60 * time.Second
	sendTimeout    = 90 * time.Second  // SMTP session cap is 60s + overhead
	syncTimeout    = 10 * time.Minute
	aiChatTimeout  = 120 * time.Second // local model inference, non-streaming
)

// App bundles the engine dependencies the IPC layer needs.
type App struct {
	DB       *sql.DB
	Accounts *account.Store
	Messages *message.Store
	Threads  *thread.Store
	Search   *search.Store
	Events   *events.Bus
	Engine   *mailsync.Engine
	Secrets  *auth.RuntimeSecrets
	Watchers *mailsync.Supervisor // push watchers, one per account
	Queue    *queue.Store         // offline-first pending operations (may be nil)
	PGP      *pgp.Service         // OpenPGP: keyring vault + contact public keys (M9)
}

// Server serves the JSON-lines protocol over arbitrary reader/writer (stdio).
type Server struct {
	app     *App
	outMu   sync.Mutex
	out     io.Writer
	offline atomic.Bool // last known server reachability (connection.state transitions)
}

// Serve blocks reading requests from in and writing responses/events to out.
func Serve(in io.Reader, out io.Writer, app *App) error {
	s := &Server{app: app, out: out}
	go s.forwardEvents()

	// offline-first: replay queued operations until the engine stops
	workerStop := make(chan struct{})
	defer close(workerStop)
	s.startOutboxWorker(workerStop)
	// snooze loop: threads whose snooze moment has passed return to their
	// folder (they emit account.updated via the folder-counter refresh)
	s.startSnoozeLoop(workerStop)

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), maxLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		s.handleLine(line)
	}
	return sc.Err()
}

func (s *Server) forwardEvents() {
	ch, stop := s.app.Events.Subscribe()
	defer stop()
	for e := range ch {
		s.writeLine(e)
	}
}

// snoozeInterval is how often the loop looks for due snoozed threads.
const snoozeInterval = 30 * time.Second

// startSnoozeLoop restores due snoozed threads every snoozeInterval (and
// once at startup, so snoozes scheduled before a restart still fire).
func (s *Server) startSnoozeLoop(stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(snoozeInterval)
		defer ticker.Stop()
		s.restoreDueSnoozes()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.restoreDueSnoozes()
			}
		}
	}()
}

// restoreDueSnoozes moves every due snoozed thread back to its folder.
func (s *Server) restoreDueSnoozes() {
	due, err := s.app.Threads.DueSnoozes(time.Now())
	if err != nil || len(due) == 0 {
		return
	}
	for _, d := range due {
		if err := moveThreadToFolder(s.app, d.ThreadID, d.FolderID); err != nil {
			continue // next pass retries (snooze_* columns stay set)
		}
		_ = s.app.Threads.Unsnooze(d.ThreadID)
	}
}

func (s *Server) writeLine(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("ipc: marshal: %v", err)
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	_, _ = s.out.Write(append(b, '\n'))
}

type request struct {
	ID     string          `json:"id"`
	Op     string          `json:"op"`
	Params json.RawMessage `json:"params"`
}

type response struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func (s *Server) handleLine(line []byte) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		s.writeLine(response{Error: "richiesta non valida: " + err.Error()})
		return
	}
	handler, ok := ops[req.Op]
	if !ok {
		s.writeLine(response{ID: req.ID, Error: "op sconosciuto: " + req.Op})
		return
	}
	if slowOps[req.Op] {
		// long network/AI ops must not hold up the stdin loop: every click
		// would queue behind a 2-minute ai.chat. Fast ops stay in order.
		go s.execute(req, handler)
		return
	}
	s.execute(req, handler)
}

// slowOps run concurrently with other requests. Flag/move ops are not here:
// their order matters (markRead then markUnread).
var slowOps = map[string]bool{
	"ai.chat": true, "send": true, "accounts.add": true, "sync.start": true,
	"attachment.save": true, "attachment.text": true, "attachment.preview": true,
	"export.message": true, "export.mailbox": true, "import.mails": true,
}

func (s *Server) execute(req request, handler handler) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout(req.Op))
	defer cancel()

	type result struct {
		data any
		err  error
	}
	done := make(chan result, 1)
	go func() {
		d, e := handler(ctx, s.app, req.Params)
		done <- result{d, e}
	}()

	select {
	case r := <-done:
		resp := response{ID: req.ID, OK: r.err == nil}
		if r.err != nil {
			// op failures only travel the IPC response (easy to miss in the
			// UI): also leave a trace in the rotating engine log so support
			// can diagnose what the user's server actually replied.
			log.Printf("op %s: %v", req.Op, r.err)
			// Offline-first: a queueable op that failed at the transport level
			// is parked in pending_ops and reported as queued, not failed.
			if queueableOps[req.Op] && isNetworkError(r.err) && s.enqueueFailedOp(req.Op, req.Params) {
				resp.OK = true
				resp.Data = map[string]any{"queued": true}
			} else {
				resp.Error = r.err.Error()
			}
		} else {
			resp.Data = r.data
		}
		s.writeLine(resp)
	case <-ctx.Done():
		s.writeLine(response{ID: req.ID, Error: "timeout operazione " + req.Op})
	}
}

func opTimeout(op string) time.Duration {
	switch op {
	case "accounts.add":
		return addTimeout
	case "send":
		return sendTimeout
	case "sync.start":
		return syncTimeout
	case "ai.chat":
		return aiChatTimeout
	default:
		return defaultTimeout
	}
}

// handler processes one op. ctx is honoured where possible; engine internals
// have their own network timeouts.
type handler func(ctx context.Context, app *App, params json.RawMessage) (any, error)

// ops is the static allowlist of protocol operations.
var ops = map[string]handler{
	"accounts.list":      opAccountsList,
	"accounts.add":       opAccountsAdd,
	"accounts.remove":    opAccountsRemove,
	"accounts.setActive": opAccountsSetActive,
	"folders.list":       opFoldersList,
	"threads.list":       opThreadsList,
	"thread.get":         opThreadGet,
	"message.get":        opMessageGet,
	"message.markRead":   opMessageMarkRead,
	"message.delete":     opMessageDelete,
	"thread.markRead":    opThreadMarkRead,
	"thread.markUnread":  opThreadMarkUnread,
	"thread.archive":     opThreadArchive,
	"thread.move":        opThreadMove,
	"thread.snooze":      opThreadSnooze,
	"thread.unsnooze":    opThreadUnsnooze,
	"secrets.restore":    opSecretsRestore,
	"search.query":       opSearchQuery,
	"search.status":      opSearchStatus,
	"search.reindex":     opSearchReindex,
	"contacts.suggest":   opContactsSuggest,
	"ai.status":          opAIStatus,
	"ai.models":          opAIModels,
	"ai.config":          opAIConfig,
	"ai.chat":            opAIChat,
	"sync.start":         opSyncStart,
	"send":               opSend,
	"attachment.save":    opAttachmentSave,
	"attachment.text":    opAttachmentText,
	"attachment.preview": opAttachmentPreview,
	"draft.save":         opDraftSave,
	"draft.delete":       opDraftDelete,

	"outbox.list":   opOutboxList,
	"rules.list":        opRulesList,
	"pgp.status":        opPGPStatus,
	"pgp.generate":      opPGPGenerate,
	"pgp.importPrivate": opPGPImportPrivate,
	"pgp.exportPublic":  opPGPExportPublic,
	"pgp.forget":        opPGPForget,
	"pgp.pubImport":     opPGPPubSave,
	"pgp.pubList":       opPGPPubList,
	"pgp.pubDelete":     opPGPPubDelete,
	"export.message":    opExportMessage,
	"export.mailbox":    opExportMailbox,
	"import.mails":      opImportMails,
	"rules.save":    opRulesSave,
	"rules.delete":  opRulesDelete,
	"outbox.cancel": opOutboxCancel,
	"outbox.drain":  opOutboxDrain,
}

func decode(params json.RawMessage, v any) error {
	if len(params) == 0 {
		return nil
	}
	return json.Unmarshal(params, v)
}

// --- accounts ---

func opAccountsList(_ context.Context, app *App, _ json.RawMessage) (any, error) {
	return app.Accounts.List()
}

type addParams struct {
	Name          string             `json:"name"`
	Email         string             `json:"email"`
	Imap          imap.Config        `json:"imap"`
	Smtp          *domain.SmtpConfig `json:"smtp,omitempty"`
	Secret        string             `json:"secret"`
	AllowInsecure bool               `json:"allowInsecure"`
	Provider      string             `json:"provider,omitempty"`
}

func opAccountsAdd(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p addParams
	if err := decode(params, &p); err != nil {
		return nil, fmt.Errorf("parametri non validi: %w", err)
	}
	if p.Name == "" || p.Email == "" {
		return nil, fmt.Errorf("nome ed email sono obbligatori")
	}
	providerKind := p.Provider
	if providerKind == "" {
		providerKind = "generic-imap"
	}
	var smtpCfg *domain.SmtpConfig
	switch {
	case providerKind == "jmap":
		// JMAP: Imap.Host carries the session URL; sending goes through
		// EmailSubmission, so no SMTP config is needed or accepted
		if !strings.HasPrefix(p.Imap.Host, "https://") {
			return nil, fmt.Errorf("l'URL di sessione JMAP deve iniziare con https://")
		}
		if p.Imap.Username == "" || p.Secret == "" {
			return nil, fmt.Errorf("username e token sono obbligatori per JMAP")
		}
	case providerKind == "microsoft":
		// Microsoft Graph: Imap.Host may carry a Graph base URL (empty =
		// default endpoint); the well-known folder set needs no discovery and
		// sending goes through sendMail, so no SMTP config is accepted
		if p.Imap.Host != "" && !strings.HasPrefix(p.Imap.Host, "https://") {
			return nil, fmt.Errorf("l'URL Graph deve iniziare con https://")
		}
		if p.Imap.Username == "" || p.Secret == "" {
			return nil, fmt.Errorf("username e token sono obbligatori per Microsoft Graph")
		}
		if p.Imap.Host == "" {
			p.Imap.Host = "https://graph.microsoft.com"
		}
	default:
		if err := imap.ValidateConfig(p.Imap, p.AllowInsecure); err != nil {
			return nil, err
		}
		smtpCfg = normalizeSmtp(p.Smtp)
		if smtpCfg != nil {
			// explicit outgoing server: same plaintext gate as IMAP
			if err := smtp.ValidateConfig(*smtpCfg, p.AllowInsecure); err != nil {
				return nil, err
			}
		}
		if p.Imap.Security != imap.SecurityPlain && p.Secret == "" {
			return nil, fmt.Errorf("password/app-password mancante")
		}
	}
	if exists, err := app.Accounts.Exists(p.Email, p.Imap.Host); err != nil {
		return nil, err
	} else if exists {
		return nil, fmt.Errorf("account già presente per %s su %s", p.Email, p.Imap.Host)
	}

	secret := auth.Secret(p.Secret)
	var folders []imap.FolderInfo
	switch {
	case providerKind == "jmap":
		jv := jmap.New(app.DB, p.Imap.Host, p.Imap.Username, p.Secret)
		f, err := jv.Folders()
		if err != nil {
			return nil, err
		}
		folders = f
	case providerKind == "microsoft":
		// well-known Outlook folders: no network discovery needed at add
		folders = graph.WellKnownFolders
	default:
		iv := provider.NewImap(p.Imap, p.Secret)
		f, err := iv.Folders()
		if err != nil {
			return nil, err
		}
		folders = f
		if smtpCfg != nil {
			// outgoing server checked now: a wrong SMTP setup must fail the
			// add dialog, not the first send
			if err := smtp.Verify(*smtpCfg, p.Secret, p.AllowInsecure); err != nil {
				return nil, err
			}
		}
	}

	id := newID("a")
	domainFolders, err := mapFolders(id, folders)
	if err != nil {
		return nil, err
	}
	acc := domain.Account{
		ID:       id,
		Name:     p.Name,
		Email:    p.Email,
		Provider: providerKind,
		Active:   true,
		Folders:  domainFolders,
		Imap:     &p.Imap,
		Smtp:     smtpCfg,
	}
	if err := app.Accounts.Create(acc); err != nil {
		return nil, err
	}
	_ = app.Accounts.SetActive(id)
	app.Secrets.Put(id, secret)
	return acc, nil
}

func opAccountsRemove(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		AccountID string `json:"accountId"`
	}
	if err := decode(params, &p); err != nil || p.AccountID == "" {
		return nil, fmt.Errorf("accountId mancante")
	}
	app.Watchers.Stop(p.AccountID) // end push before the local wipe
	app.Secrets.Forget(p.AccountID)
	if err := app.Accounts.Delete(p.AccountID); err != nil {
		return nil, err
	}
	return map[string]bool{"removed": true}, nil
}

func opAccountsSetActive(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		AccountID string `json:"accountId"`
	}
	if err := decode(params, &p); err != nil || p.AccountID == "" {
		return nil, fmt.Errorf("accountId mancante")
	}
	return nil, app.Accounts.SetActive(p.AccountID)
}

// mapFolders turns IMAP mailboxes into domain folders with stable local ids.
func mapFolders(accountID string, folders []imap.FolderInfo) ([]domain.Folder, error) {
	var out []domain.Folder
	n := 0
	for _, f := range folders {
		if f.NoSelect || f.Name == "" {
			continue
		}
		out = append(out, domain.Folder{
			ID:      fmt.Sprintf("f:%s:%d", accountID, n),
			Account: accountID,
			Name:    f.Name,
			Type:    folderType(f.Name, f.SpecialUse),
		})
		n++
	}
	sort.Slice(out, func(i, j int) bool {
		pi, pj := folderRank(out[i].Type, out[i].Name), folderRank(out[j].Type, out[j].Name)
		if pi != pj {
			return pi < pj
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func folderType(name, specialUse string) string {
	// RFC 6154 special-use values, already normalized to strings by the
	// client (see imap/client.go Folders).
	switch specialUse {
	case "\\Sent":
		return "sent"
	case "\\Drafts":
		return "drafts"
	case "\\Trash":
		return "trash"
	case "\\Archive":
		return "archive"
	case "\\Junk":
		return "junk"
	}
	low := strings.ToLower(name)
	switch {
	case low == "inbox":
		return "inbox"
	case strings.Contains(low, "sent"):
		return "sent"
	case strings.Contains(low, "draft"):
		return "drafts"
	case strings.Contains(low, "trash") || strings.Contains(low, "deleted"):
		return "trash"
	case strings.Contains(low, "archive"):
		return "archive"
	case low == "junk" || strings.Contains(low, "spam") || strings.Contains(low, "bulk"):
		return "junk"
	default:
		return "custom"
	}
}

func folderRank(t, name string) int {
	switch t {
	case "inbox":
		return 0
	case "sent":
		return 1
	case "drafts":
		return 2
	case "archive":
		return 3
	case "trash":
		return 4
	case "junk":
		return 5
	default:
		if strings.EqualFold(name, "junk") || strings.Contains(strings.ToLower(name), "spam") {
			return 5
		}
		return 6
	}
}

// --- folders / threads / messages ---

func opFoldersList(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		AccountID string `json:"accountId"`
	}
	if err := decode(params, &p); err != nil || p.AccountID == "" {
		return nil, fmt.Errorf("accountId mancante")
	}
	return app.Accounts.Folders(p.AccountID)
}

// opThreadsList serves both views:
//   - {folderId}                     → threads of one folder (existing behavior)
//   - {accountId: "all"|id, type?}   → account-level view across its folders
//     (the unified inbox: type defaults to "inbox"; every account contributes
//     its own folder of that type; newest first, hard cap 200 rows)
func opThreadsList(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		FolderID   string `json:"folderId"`
		AccountID  string `json:"accountId"`
		FolderType string `json:"type"`
	}
	if err := decode(params, &p); err != nil {
		return nil, fmt.Errorf("parametri non validi")
	}
	var threads []domain.Thread
	var err error
	switch {
	case p.FolderID != "":
		threads, err = app.Threads.ListByFolder(p.FolderID)
	case p.AccountID != "":
		accountID := p.AccountID
		if accountID == "all" {
			accountID = "" // ListUnified: every account
		}
		if p.FolderType == "" {
			p.FolderType = "inbox"
		}
		threads, err = app.Threads.ListUnified(accountID, p.FolderType, 200)
	default:
		return nil, fmt.Errorf("folderId o accountId mancante")
	}
	if err != nil {
		return nil, err
	}
	for i := range threads {
		ids, err := app.Threads.MessageIDs(threads[i].ID)
		if err != nil {
			return nil, err
		}
		threads[i].MessageIDs = ids
	}
	return threads, nil
}

func opThreadGet(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if err := decode(params, &p); err != nil || p.ThreadID == "" {
		return nil, fmt.Errorf("threadId mancante")
	}
	th, err := app.Threads.Get(p.ThreadID)
	if err != nil {
		return nil, err
	}
	return th, nil
}

func opMessageGet(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		MessageID string `json:"messageId"`
	}
	if err := decode(params, &p); err != nil || p.MessageID == "" {
		return nil, fmt.Errorf("messageId mancante")
	}
	m, err := app.Messages.Get(p.MessageID)
	if err != nil {
		return nil, err
	}
	applyPGPOnView(app, &m)
	return m, nil
}

// extractArmor returns the first complete PGP MESSAGE armor block found in s
// (the engine stores the armor as the message body for encrypted mail).
func extractArmor(s string) string {
	i := strings.Index(s, "-----BEGIN PGP MESSAGE-----")
	if i < 0 {
		return s
	}
	j := strings.Index(s[i:], "-----END PGP MESSAGE-----")
	if j < 0 {
		return ""
	}
	return s[i : i+j+len("-----END PGP MESSAGE-----")]
}

// applyPGPOnView decrypts an encrypted body in place (the DB keeps the
// ciphertext at rest) and verifies signed messages whose status is still
// pending. Best-effort: without the key the payload is shown as-is and the
// UI explains what to do.
func applyPGPOnView(app *App, m *domain.Message) {
	if app.PGP == nil {
		return
	}
	if m.IsEncrypted && strings.Contains(m.Body, "-----BEGIN PGP MESSAGE-----") {
		plain, signedBy, err := app.PGP.Decrypt(m.AccountID, m.Body)
		if err == nil {
			m.Body = plain
			m.BodyText = plain
			if signedBy != "" {
				m.IsSigned = true
				if m.SigStatus == "" {
					m.SigStatus = "valid"
				}
			}
		}
	}
	if m.IsSigned && m.SigStatus == "" {
		m.SigStatus = app.PGP.Verify(m.AccountID, m.Body)
		_, _ = app.DB.Exec("UPDATE messages SET sig_status=? WHERE id=?", m.SigStatus, m.ID)
	}
}

// --- actions with server round-trip ---

func opMessageMarkRead(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		MessageID string `json:"messageId"`
	}
	if err := decode(params, &p); err != nil || p.MessageID == "" {
		return nil, fmt.Errorf("messageId mancante")
	}
	accountID, _, mailbox, uid, err := app.Messages.ActionTarget(p.MessageID)
	if err != nil {
		return nil, err
	}
	if err := app.Messages.MarkRead(p.MessageID); err != nil {
		return nil, err
	}
	if uid != 0 { // local-only messages (drafts) have nothing on the server
		if err := pushServerCmd(app, cmdFlags, serverCmd{AccountID: accountID, Mailbox: mailbox, UIDs: []uint32{uid}}); err != nil {
			return nil, err
		}
	}
	return map[string]bool{"ok": true}, nil
}

// threadFlagOp propagates a thread-level read flag change: one provider
// round-trip per folder involved (a thread lives in a single folder today,
// but the grouping keeps the op correct if that ever changes), then the local
// flag update. Message-less and non-IMAP accounts skip the round-trip.
func threadFlagOp(app *App, threadID string, unread bool) error {
	targets, err := app.Messages.ThreadFlagTargets(threadID)
	if err != nil {
		return err
	}
	byFolder := map[string]struct {
		mailbox string
		uids    []uint32
	}{}
	for _, t := range targets {
		if t.UID == 0 || t.Mailbox == "" {
			continue // local-only message (draft) or unsynced UID
		}
		if unread == t.IsUnread {
			continue // flag already in the wanted state server-side
		}
		g := byFolder[t.FolderID]
		g.mailbox = t.Mailbox
		g.uids = append(g.uids, t.UID)
		byFolder[t.FolderID] = g
	}
	// deterministic order
	folderIDs := make([]string, 0, len(byFolder))
	for id := range byFolder {
		folderIDs = append(folderIDs, id)
	}
	sort.Strings(folderIDs)
	// local first: the UI sees the change at once; the server follows
	if unread {
		if err := app.Messages.MarkThreadUnread(threadID); err != nil {
			return err
		}
	} else if err := app.Messages.MarkThreadRead(threadID); err != nil {
		return err
	}
	accountID := threadAccount(app, threadID)
	for _, folderID := range folderIDs {
		g := byFolder[folderID]
		if err := pushServerCmd(app, cmdFlags, serverCmd{AccountID: accountID, Mailbox: g.mailbox, UIDs: g.uids, Unread: unread}); err != nil {
			return err
		}
	}
	// refresh the sidebar badges + notify the UI (account.updated triggers an
	// accounts+threads refresh in the frontend)
	allFolderIDs := make([]string, 0, 1)
	seen := map[string]bool{}
	for _, t := range targets {
		if !seen[t.FolderID] {
			seen[t.FolderID] = true
			allFolderIDs = append(allFolderIDs, t.FolderID)
		}
	}
	recomputeFolderCounters(app, threadAccount(app, threadID), allFolderIDs...)
	return nil
}

// threadAccount returns the account owning a thread ("" when unknown).
func threadAccount(app *App, threadID string) string {
	var accountID string
	_ = app.DB.QueryRow(`SELECT account FROM threads WHERE id=?`, threadID).Scan(&accountID)
	return accountID
}

// recomputeFolderCounters refreshes the total/unread badges of the given
// folders and notifies the UI via account.updated so the sidebar follows
// flag and move operations immediately. Best-effort: never fails the op.
func recomputeFolderCounters(app *App, accountID string, folderIDs ...string) {
	for _, f := range folderIDs {
		if f == "" {
			continue
		}
		if err := app.Messages.UpdateFolderCounters(f); err != nil {
			return
		}
	}
	if app.Events == nil || accountID == "" {
		return
	}
	accounts, err := app.Accounts.List()
	if err != nil {
		return
	}
	for _, a := range accounts {
		if a.ID == accountID {
			app.Events.AccountUpdated(a)
			break
		}
	}
}

func opThreadMarkRead(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if err := decode(params, &p); err != nil || p.ThreadID == "" {
		return nil, fmt.Errorf("threadId mancante")
	}
	if err := threadFlagOp(app, p.ThreadID, false); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func opThreadMarkUnread(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if err := decode(params, &p); err != nil || p.ThreadID == "" {
		return nil, fmt.Errorf("threadId mancante")
	}
	if err := threadFlagOp(app, p.ThreadID, true); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func opMessageDelete(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		MessageID string `json:"messageId"`
	}
	if err := decode(params, &p); err != nil || p.MessageID == "" {
		return nil, fmt.Errorf("messageId mancante")
	}
	accountID, _, mailbox, uid, err := app.Messages.ActionTarget(p.MessageID)
	if err != nil {
		return nil, err
	}
	if err := app.Messages.DeleteFull(p.MessageID); err != nil {
		return nil, err
	}
	if uid != 0 {
		if err := pushServerCmd(app, cmdDelete, serverCmd{AccountID: accountID, Mailbox: mailbox, UIDs: []uint32{uid}}); err != nil {
			return nil, err
		}
	}
	return map[string]bool{"ok": true}, nil
}

// moveThreadToFolder is the shared implementation behind thread.archive and
// thread.move: local folder switch and badge refresh first, then the
// server-side IMAP MOVE of every message UID as a queued command (skipped for
// local-only folders without an imap_name or already-moved threads).
func moveThreadToFolder(app *App, threadID, targetFolder string) error {
	var accountID string
	if err := app.DB.QueryRow(`SELECT account FROM threads WHERE id=?`, threadID).Scan(&accountID); err != nil {
		return fmt.Errorf("thread %s: %w", threadID, err)
	}
	var targetID, targetMailbox string
	if err := app.DB.QueryRow(
		`SELECT id, imap_name FROM folders WHERE id=? AND account=?`, targetFolder, accountID,
	).Scan(&targetID, &targetMailbox); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("cartella di destinazione non trovata per l'account")
		}
		return err
	}
	mailbox, uids, err := app.Messages.ThreadUIDs(threadID)
	if err != nil {
		return err
	}
	// source folders must be collected BEFORE the local move: afterwards the
	// messages no longer reference them and their badges would go stale
	var sourceFolders []string
	rows, err := app.DB.Query(`SELECT DISTINCT folder FROM messages WHERE thread=?`, threadID)
	if err == nil {
		for rows.Next() {
			var f string
			if err := rows.Scan(&f); err == nil {
				sourceFolders = append(sourceFolders, f)
			}
		}
		rows.Close()
	}
	if err := app.Threads.MoveToFolder(threadID, targetID); err != nil {
		return err
	}
	// the move changes both folders' badges: refresh them and notify the UI
	recomputeFolderCounters(app, accountID, append(sourceFolders, targetID)...)
	// server MOVE follows (skipped for local-only folders and no-op moves)
	if targetMailbox != "" && targetMailbox != mailbox {
		return pushServerCmd(app, cmdMove, serverCmd{AccountID: accountID, Mailbox: mailbox, UIDs: uids, Target: targetMailbox})
	}
	return nil
}

func opThreadArchive(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if err := decode(params, &p); err != nil || p.ThreadID == "" {
		return nil, fmt.Errorf("threadId mancante")
	}
	var accountID string
	if err := app.DB.QueryRow(`SELECT account FROM threads WHERE id=?`, p.ThreadID).Scan(&accountID); err != nil {
		return nil, fmt.Errorf("thread %s: %w", p.ThreadID, err)
	}
	var archiveFolder string
	if err := app.DB.QueryRow(
		`SELECT id FROM folders WHERE account=? AND type='archive' LIMIT 1`, accountID,
	).Scan(&archiveFolder); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("cartella Archivio non trovata per l'account")
		}
		return nil, err
	}
	if err := moveThreadToFolder(app, p.ThreadID, archiveFolder); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// opThreadMove moves a thread to a client-chosen folder (the generic path
// behind archive and the spam actions): target is a local folder id owned by
// the same account as the thread.
func opThreadMove(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		ThreadID string `json:"threadId"`
		Target   string `json:"target"`
	}
	if err := decode(params, &p); err != nil || p.ThreadID == "" || p.Target == "" {
		return nil, fmt.Errorf("threadId e target sono obbligatori")
	}
	if err := moveThreadToFolder(app, p.ThreadID, p.Target); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// opThreadSnooze defers a thread: the thread moves to the account's local
// snoozed folder and comes back to its current folder at `until` (RFC 3339).
// Purely local scheduling: the IMAP server is not involved.
func opThreadSnooze(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		ThreadID string `json:"threadId"`
		Until    string `json:"until"`
	}
	if err := decode(params, &p); err != nil || p.ThreadID == "" || p.Until == "" {
		return nil, fmt.Errorf("threadId e until sono obbligatori")
	}
	until, err := time.Parse(time.RFC3339, p.Until)
	if err != nil {
		return nil, fmt.Errorf("until non valido: %w", err)
	}
	var accountID, currentFolder string
	if err := app.DB.QueryRow(
		`SELECT account, folder FROM threads WHERE id=?`, p.ThreadID,
	).Scan(&accountID, &currentFolder); err != nil {
		return nil, fmt.Errorf("thread %s: %w", p.ThreadID, err)
	}
	snoozedID, err := app.Accounts.EnsureSnoozedFolder(accountID)
	if err != nil {
		return nil, err
	}
	if err := app.Threads.Snooze(p.ThreadID, currentFolder, until); err != nil {
		return nil, err
	}
	if err := moveThreadToFolder(app, p.ThreadID, snoozedID); err != nil {
		_ = app.Threads.Unsnooze(p.ThreadID) // failed move: leave no schedule
		return nil, err
	}
	return map[string]any{"ok": true, "until": until.UTC().Format(time.RFC3339)}, nil
}

// opThreadUnsnooze restores a snoozed thread immediately.
func opThreadUnsnooze(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if err := decode(params, &p); err != nil || p.ThreadID == "" {
		return nil, fmt.Errorf("threadId mancante")
	}
	var snoozeFolder string
	if err := app.DB.QueryRow(
		`SELECT snooze_folder FROM threads WHERE id=?`, p.ThreadID,
	).Scan(&snoozeFolder); err != nil {
		return nil, fmt.Errorf("thread %s: %w", p.ThreadID, err)
	}
	if snoozeFolder == "" {
		return nil, fmt.Errorf("il thread non è snoozed")
	}
	if err := moveThreadToFolder(app, p.ThreadID, snoozeFolder); err != nil {
		return nil, err
	}
	if err := app.Threads.Unsnooze(p.ThreadID); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// --- search / sync / send ---

// opContactsSuggest feeds the compose autocomplete: distinct recipients seen
// in the local mail history (name + email), best match first. Local-only:
// the queries never leave the engine, consistent with the privacy stance.
func opContactsSuggest(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := decode(params, &p); err != nil {
		return nil, fmt.Errorf("parametri non validi: %w", err)
	}
	q := strings.TrimSpace(p.Query)
	// strip a trailing partial entry from a comma-separated compose field:
	// "anna@acme.dev, lu" suggests for "lu"
	if i := strings.LastIndex(q, ","); i >= 0 {
		q = strings.TrimSpace(q[i+1:])
	}
	if q == "" {
		return []domain.Contact{}, nil
	}
	if p.Limit <= 0 || p.Limit > 20 {
		p.Limit = 8
	}
	like := "%" + strings.ToLower(q) + "%"
	rows, err := app.DB.Query(
		`SELECT email, MAX(name) AS name
		   FROM recipients
		  WHERE LOWER(email) LIKE ? OR LOWER(name) LIKE ?
		  GROUP BY LOWER(email)
		  ORDER BY COUNT(*) DESC, email ASC
		  LIMIT ?`, like, like, p.Limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contacts := []domain.Contact{}
	for rows.Next() {
		var email, name string
		if err := rows.Scan(&email, &name); err != nil {
			return nil, err
		}
		contacts = append(contacts, domain.Contact{Email: email, Name: name})
	}
	return contacts, rows.Err()
}

// --- search / sync / send ---

// aiClient resolves the user-configured local AI server (Ollama / LM Studio,
// M12): app_config keys ai.* with the legacy OLLAMA_URL env fallback. Every
// AI feature degrades gracefully when the server is off.
func aiClient(app *App) *localai.Client {
	return localai.New(localai.LoadConfig(app.DB))
}

func opSearchQuery(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var q domain.SearchQuery
	if err := decode(params, &q); err != nil {
		return nil, fmt.Errorf("query non valida: %w", err)
	}
	// semantic mode (M10, optional): embeddings via the local AI server when
	// it is up AND vectors exist; otherwise graceful keyword fallback.
	if q.Mode == "semantic" && strings.TrimSpace(q.Query) != "" {
		sem := semantic.New(app.DB)
		client := aiClient(app)
		if client.Available() {
			res, err := sem.Search(client, q)
			if err != nil {
				return nil, fmt.Errorf("ricerca semantica: %w", err)
			}
			if len(res) > 0 {
				return res, nil
			}
			// nothing indexed (yet): fall through to keyword search
		}
		q.Mode = ""
	}
	return app.Search.Query(q)
}

// opSearchStatus reports the optional semantic-search layer state: whether a
// local AI server is reachable, the configured embedding model and the
// backfill progress (indexed with the current model vs total messages).
func opSearchStatus(_ context.Context, app *App, _ json.RawMessage) (any, error) {
	sem := semantic.New(app.DB)
	client := aiClient(app)
	available := client.Available()
	indexed, total := 0, 0
	if available {
		var err error
		if indexed, total, err = sem.Counts(); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"ollama":   available,
		"model":     sem.Model(),
		"indexed":   indexed,
		"total":     total,
	}, nil
}

// opSearchReindex embeds up to limit messages missing a vector (newest
// first). The UI calls it repeatedly until remaining == 0: a large mailbox
// is indexed in user-visible chunks instead of one long blocking request.
func opSearchReindex(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		Limit int `json:"limit"`
	}
	if err := decode(params, &p); err != nil {
		return nil, fmt.Errorf("parametri non validi: %w", err)
	}
	if p.Limit <= 0 || p.Limit > 500 {
		p.Limit = 200
	}
	sem := semantic.New(app.DB)
	client := aiClient(app)
	if !client.Available() {
		return nil, fmt.Errorf("server AI non raggiungibile su %s", client.Base)
	}
	indexed, err := sem.Backfill(client, p.Limit)
	if err != nil {
		return nil, err
	}
	_, remaining, cerr := sem.Counts()
	if cerr != nil {
		return nil, cerr
	}
	return map[string]int{"indexed": indexed, "remaining": remaining}, nil
}

// opAIStatus reports the local AI server state: whether it is reachable and
// the configured endpoint/kind/models. Fast: no model list download.
func opAIStatus(_ context.Context, app *App, _ json.RawMessage) (any, error) {
	cfg := localai.LoadConfig(app.DB)
	client := localai.New(cfg)
	sem := semantic.New(app.DB)
	status := map[string]any{
		"available":   false,
		"url":         client.Base,
		"kind":        cfg.Kind,
		"chatModel":   cfg.ChatModel,
		"embedModel":  sem.Model(),
		"allowRemote": cfg.AllowRemote,
	}
	if !cfg.AllowRemote {
		// probing a blocked host would be a wasted round-trip
		if err := client.Validate(); err != nil {
			status["error"] = err.Error()
			return status, nil
		}
	}
	status["available"] = client.Available()
	return status, nil
}

// opAIModels lists the models served by the configured endpoint: used by the
// settings screen as a connection test and to populate the model pickers.
func opAIModels(ctx context.Context, app *App, _ json.RawMessage) (any, error) {
	client := aiClient(app)
	models, err := client.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"models": models}, nil
}

// opAIConfig persists the local AI settings. Empty strings leave a value
// unchanged; embedModel routes through semantic.SetModel so a model switch
// wipes stored vectors (different models are not comparable). The response
// is the effective status, so the UI re-renders in one round-trip.
func opAIConfig(ctx context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		URL         string `json:"url"`
		Kind        string `json:"kind"`
		ChatModel   string `json:"chatModel"`
		EmbedModel  string `json:"embedModel"`
		AllowRemote *bool  `json:"allowRemote"`
	}
	if err := decode(params, &p); err != nil {
		return nil, fmt.Errorf("parametri non validi: %w", err)
	}
	// validate the kind before touching the config
	if k := strings.TrimSpace(p.Kind); k != "" && k != string(localai.KindOllama) && k != string(localai.KindOpenAI) {
		return nil, fmt.Errorf("tipo di server non valido: %s", k)
	}
	if strings.TrimSpace(p.URL) != "" {
		// the privacy guard is enforced by the client; probe eagerly so a
		// blocked host is reported at save time, not on first use
		probe := localai.New(localai.Config{
			Base: p.URL, Kind: localai.Kind(strings.TrimSpace(p.Kind)),
			AllowRemote: p.AllowRemote != nil && *p.AllowRemote,
		})
		if err := probe.Validate(); err != nil {
			return nil, err
		}
	}
	if err := localai.SaveConfig(app.DB, p.URL, p.Kind, p.ChatModel, p.AllowRemote); err != nil {
		return nil, err
	}
	if m := strings.TrimSpace(p.EmbedModel); m != "" {
		if err := semantic.New(app.DB).SetModel(m); err != nil {
			return nil, err
		}
	}
	return opAIStatus(ctx, app, nil)
}

// opAIChat runs a non-streaming completion with the configured chat model.
// The UI builds the prompts (mail content is already in the webview); the
// engine only forwards them to the user's local server. Not queueable: an
// AI request without the local server is an error, not an offline op.
func opAIChat(ctx context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		Messages []localai.ChatMessage `json:"messages"`
	}
	if err := decode(params, &p); err != nil {
		return nil, fmt.Errorf("parametri non validi: %w", err)
	}
	if len(p.Messages) == 0 {
		return nil, fmt.Errorf("richiesta vuota")
	}
	// text and images are budgeted separately: text hits the prompt design
	// caps, images cover the rendered pages of scanned PDFs (base64).
	textSize, imageSize := 0, 0
	for _, m := range p.Messages {
		textSize += len(m.Content)
		for _, img := range m.Images {
			imageSize += len(img)
		}
	}
	if textSize > 64*1024 {
		return nil, fmt.Errorf("contenuto troppo lungo per il modello locale (max 64 KB)")
	}
	if imageSize > 6*1024*1024 {
		return nil, fmt.Errorf("troppe immagini allegate (max 6 MB)")
	}
	cfg := localai.LoadConfig(app.DB)
	content, err := localai.New(cfg).Chat(ctx, cfg.ChatModel, p.Messages)
	if err != nil {
		return nil, err
	}
	return map[string]string{"content": content}, nil
}

// opSecretsRestore re-delivers keychain secrets into the engine's runtime
// vault after an engine restart. It is engine-internal: only Tauri calls it
// (right after spawning the engine, reading the OS keychain); the webview
// never supplies or receives credential material here. Secrets are never
// echoed back in the response.
func opSecretsRestore(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		Secrets []struct {
			AccountID string `json:"accountId"`
			Secret    string `json:"secret"`
		} `json:"secrets"`
	}
	if err := decode(params, &p); err != nil {
		return nil, fmt.Errorf("parametri non validi: %w", err)
	}
	restored := 0
	for _, s := range p.Secrets {
		if s.AccountID == "" || s.Secret == "" {
			continue
		}
		app.Secrets.Put(s.AccountID, auth.Secret(s.Secret))
		restored++
	}
	return map[string]int{"restored": restored}, nil
}

func opSyncStart(ctx context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		AccountID string `json:"accountId"`
		Secret    string `json:"secret,omitempty"`
	}
	if err := decode(params, &p); err != nil || p.AccountID == "" {
		return nil, fmt.Errorf("accountId mancante")
	}
	if p.Secret != "" {
		app.Secrets.Put(p.AccountID, auth.Secret(p.Secret))
	}
	accounts, err := app.Accounts.List()
	if err != nil {
		return nil, err
	}
	var target *domain.Account
	for i := range accounts {
		if accounts[i].ID == p.AccountID {
			target = &accounts[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("account %s non trovato", p.AccountID)
	}

	secret, ok := app.Secrets.Get(p.AccountID)
	if !ok || secret.Empty() {
		return nil, fmt.Errorf("credenziale non disponibile: riapri l'app o riconnetti l'account")
	}
	pv, err := providerForAccount(app, target, secret.Expose())
	if err != nil {
		return nil, err
	}
	// pre-validate the credentials synchronously: auth errors surface in
	// this response instead of a later sync.error event
	if err := provider.Ping(pv); err != nil {
		closeProvider(pv)
		return nil, err
	}
	closeProvider(pv)

	// a new sync replaces any previous watcher for this account
	app.Watchers.Stop(p.AccountID)
	go func(accountID string, acc domain.Account, sec auth.Secret) {
		syncPv, perr := providerForAccount(app, &acc, sec.Expose())
		if perr != nil {
			app.Events.SyncError(accountID, perr.Error())
			return
		}
		if err := app.Engine.SyncAccountWith(accountID, syncPv); err != nil {
			return // SyncAccountWith already emitted sync.error
		}
		// account removed during the sync (accounts.remove forgets the
		// secret): do not open IDLE sessions for it
		if _, ok := app.Secrets.Get(accountID); !ok {
			return
		}
		// push watcher owns its own provider/session from here on
		watchPv, werr := providerForAccount(app, &acc, sec.Expose())
		if werr != nil {
			return
		}
		newWatchPv := func() (provider.MailProvider, error) {
			// one extra IMAP session per watched folder (IDLE occupies the
			// session's selected mailbox). Also used to redial a dropped
			// session: read the CURRENT secret, not the one captured at
			// sync.start (an OAuth access token expires after ~1 h and is
			// refreshed in the vault by secrets.restore).
			cur, ok := app.Secrets.Get(accountID)
			if !ok || cur.Empty() {
				return nil, fmt.Errorf("credenziale non disponibile")
			}
			return providerForAccount(app, &acc, cur.Expose())
		}
		app.Watchers.Start(accountID, watchPv, watchMailboxes(app, accountID), newWatchPv)
	}(p.AccountID, *target, secret)

	return map[string]any{"started": true}, nil
}

// watchMailboxes picks the mailboxes to idle on: INBOX first (new mail
// lands there), then the SPECIAL-USE folders with a known mailbox name —
// sent, drafts, archive, trash, junk — so push covers the folders users
// actually care about. Custom folders keep relying on the next full sync.
func watchMailboxes(app *App, accountID string) []string {
	// one IMAP session per watched folder: keep a sane ceiling
	const maxWatchedFolders = 8
	rows, err := app.DB.Query(
		`SELECT type, imap_name FROM folders
		 WHERE account=? AND imap_name<>'' AND type IN
		       ('inbox','sent','drafts','archive','trash','junk')
		 ORDER BY CASE type
			WHEN 'inbox' THEN 0 WHEN 'sent' THEN 1 WHEN 'drafts' THEN 2
			WHEN 'archive' THEN 3 WHEN 'trash' THEN 4 ELSE 5 END`,
		accountID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	seen := make(map[string]bool)
	var out []string
	for rows.Next() {
		var typ, name string
		if err := rows.Scan(&typ, &name); err != nil {
			continue
		}
		if name == "" || seen[name] || len(out) >= maxWatchedFolders {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

type sendParams struct {
	AccountID string                `json:"accountId,omitempty"`
	Secret    string                `json:"secret,omitempty"`
	Message   domain.ComposeMessage `json:"message"`
}

// opSend delivers a composed message: SMTP send, best-effort APPEND into
// Sent, local persistence with thread resolution, then events. The secret
// comes from the keychain via Rust (account_send) and lives in memory only.
func opSend(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p sendParams
	if err := decode(params, &p); err != nil {
		return nil, fmt.Errorf("parametri non validi: %w", err)
	}
	if strings.TrimSpace(p.Message.Subject) == "" && strings.TrimSpace(p.Message.Body) == "" {
		return nil, fmt.Errorf("messaggio vuoto")
	}
	// undo-send / scheduled send: a future sendAt parks the message in the
	// outbox queue (cancellable via outbox.cancel) and the worker delivers
	// it at that time through this same handler. The stored payload has the
	// schedule stripped so the replay is a normal immediate send.
	if strings.TrimSpace(p.Message.SendAt) != "" {
		runAt, err := time.Parse(time.RFC3339, p.Message.SendAt)
		if err != nil {
			return nil, fmt.Errorf("sendAt non valido: %w", err)
		}
		if runAt.After(time.Now().UTC()) {
			p.Message.SendAt = ""
			accountID := p.AccountID
			if accountID == "" {
				accounts, aerr := app.Accounts.List()
				if aerr != nil {
					return nil, aerr
				}
				for _, a := range accounts {
					if a.Active {
						accountID = a.ID
						break
					}
				}
			}
			if accountID == "" {
				return nil, fmt.Errorf("nessun account da cui inviare: aggiungi un account")
			}
			if app.Queue == nil {
				return nil, fmt.Errorf("coda di invio non disponibile")
			}
			p.Secret = "" // never persisted: the replay reads the runtime vault
			payload, merr := json.Marshal(p)
			if merr != nil {
				return nil, merr
			}
			opID, qerr := app.Queue.EnqueueAt(accountID, "send", payload, runAt)
			if qerr != nil {
				return nil, qerr
			}
			emitOutboxCount(app)
			return map[string]any{
				"queued":    true,
				"scheduled": true,
				"opId":      opID,
				"sendAt":    runAt.UTC().Format(time.RFC3339),
			}, nil
		}
		p.Message.SendAt = "" // in the past: send immediately
	}
	accounts, err := app.Accounts.List()
	if err != nil {
		return nil, err
	}
	var target *domain.Account
	for i := range accounts {
		if p.AccountID == "" && accounts[i].Active {
			target = &accounts[i]
			break
		}
		if p.AccountID != "" && accounts[i].ID == p.AccountID {
			target = &accounts[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("nessun account da cui inviare: aggiungi un account")
	}
	if target.Imap == nil {
		return nil, fmt.Errorf("invio non supportato per il provider %q", target.Provider)
	}
	if p.Secret != "" {
		app.Secrets.Put(target.ID, auth.Secret(p.Secret))
	}
	secret, ok := app.Secrets.Get(target.ID)
	if !ok || secret.Empty() {
		return nil, fmt.Errorf("credenziale non disponibile: riapri l'app o riconnetti l'account")
	}

	pv, perr := providerForAccount(app, target, secret.Expose())
	if perr != nil {
		return nil, perr
	}
	defer closeProvider(pv)
	if target.Smtp != nil {
		if sm, ok := pv.(provider.SMTPConfigurer); ok {
			sm.WithSMTP(*target.Smtp)
		}
	}

	from := domain.Recipient{Name: target.Name, Email: target.Email}

	// outgoing attachments (new send path): read from the client-chosen
	// paths once here, so every provider transport embeds the same bytes
	if err := smtp.LoadFiles(&p.Message); err != nil {
		return nil, err
	}

	// M9: OpenPGP on the text part. Encrypt on the plaintext first, then
	// sign the armored body when both toggles are active.
	if p.Message.Encrypt {
		if len(p.Message.Attachments) > 0 || len(p.Message.Files) > 0 {
			return nil, fmt.Errorf("la cifratura PGP copre solo il testo: rimuovi gli allegati per inviare cifrato")
		}
		rcpts := make([]string, 0, len(p.Message.To)+len(p.Message.CC))
		for _, r := range append(append([]domain.Recipient{}, p.Message.To...), p.Message.CC...) {
			rcpts = append(rcpts, r.Email)
		}
		armored, missing, err := app.PGP.Encrypt(target.ID, p.Message.Body, rcpts)
		if err != nil {
			return nil, err
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("nessuna chiave pubblica per: %s (importa le chiavi dei destinatari)", strings.Join(missing, ", "))
		}
		p.Message.Body = armored
		p.Message.HTMLBody = "" // text-only: no plaintext HTML may leak
	}
	if p.Message.Sign {
		signed, err := app.PGP.Sign(target.ID, p.Message.Body)
		if err != nil {
			return nil, err
		}
		p.Message.Body = signed
		p.Message.HTMLBody = "" // clearsign covers the text part only
	}

	raw, err := pv.Send(from, p.Message)
	if err != nil {
		return nil, err
	}

	// Best-effort copy in the server Sent mailbox: the message was already
	// delivered, a failing APPEND must not fail the send. When the server
	// reports APPENDUID the local copy keeps the real UID (so the next Sent
	// sync reconciles instead of duplicating).
	sentFolderID, sentMailbox := sentFolderOf(app, target.ID)
	sentUID := ""
	if sentMailbox != "" {
		if u, aerr := pv.Append(sentMailbox, raw, []string{"\\Seen"}, time.Now()); aerr == nil && u > 0 {
			sentUID = strconv.FormatUint(uint64(u), 10)
		}
	}

	// From here the message is delivered: nothing below may return an error,
	// or the outbox would retry and send it twice. Local-copy failures are
	// only logged; the Sent folder sync recovers the message.
	msg, err := mime.Parse(raw)
	if err != nil {
		log.Printf("send: messaggio inviato ma non memorizzabile: %v", err)
		return map[string]any{"sent": true}, nil
	}
	msg.FolderID = sentFolderID
	msg.AccountID = target.ID
	if sentFolderID != "" {
		msg.ID = messageIDFor(target.ID, sentFolderID, sentUID)
		msg.ProviderMessageID = sentUID
		resolver := thread.NewResolver(app.DB)
		msg.ThreadID, err = resolver.Resolve(target.ID, sentFolderID, &msg)
		if err == nil {
			err = app.Messages.UpsertBatch([]domain.Message{msg})
		}
		if err != nil {
			log.Printf("send: copia locale non salvata: %v", err)
			return map[string]any{"sent": true}, nil
		}
		_ = app.Accounts.RefreshFolderCounters(sentFolderID)
	}

	app.Events.MessageCreated(msg)
	if acc, err := app.Accounts.List(); err == nil {
		for i := range acc {
			if acc[i].ID == target.ID {
				app.Events.AccountUpdated(acc[i])
				break
			}
		}
	}
	return map[string]any{"sent": true, "messageId": msg.ID, "threadId": msg.ThreadID}, nil
}

type draftParams struct {
	AccountID string                `json:"accountId,omitempty"`
	DraftID   string                `json:"draftId,omitempty"`
	Message   domain.ComposeMessage `json:"message"`
}

// opDraftSave stores a draft local-first: upsert in the drafts folder and a
// best-effort APPEND into the server Drafts mailbox. Returns the draft id.
func opDraftSave(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p draftParams
	if err := decode(params, &p); err != nil {
		return nil, fmt.Errorf("parametri non validi: %w", err)
	}
	accounts, err := app.Accounts.List()
	if err != nil {
		return nil, err
	}
	var target *domain.Account
	for i := range accounts {
		if p.AccountID == "" && accounts[i].Active {
			target = &accounts[i]
			break
		}
		if p.AccountID != "" && accounts[i].ID == p.AccountID {
			target = &accounts[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("nessun account: aggiungi un account prima di salvare bozze")
	}
	draftsID, draftsMailbox, err := app.Accounts.EnsureDraftsFolder(target.ID)
	if err != nil {
		return nil, err
	}

	from := domain.Recipient{Name: target.Name, Email: target.Email}
	raw := smtp.Compose(from, p.Message)

	// server copy of the previous version: replaced by this save
	var oldUID uint64
	if p.DraftID != "" {
		if old, gerr := app.Messages.Get(p.DraftID); gerr == nil {
			oldUID, _ = strconv.ParseUint(old.ProviderMessageID, 10, 32)
		}
	}
	uid := ""
	if draftsMailbox != "" {
		secret, ok := app.Secrets.Get(target.ID)
		if ok && !secret.Empty() {
			if pv, perr := providerForAccount(app, target, secret.Expose()); perr == nil {
				if u, aerr := pv.Append(draftsMailbox, raw, []string{"\\Draft", "\\Seen"}, time.Now()); aerr == nil && u > 0 {
					uid = strconv.FormatUint(uint64(u), 10)
					if oldUID > 0 {
						// else the next Drafts sync brings the old version back
						_ = pv.Delete(draftsMailbox, []uint32{uint32(oldUID)})
					}
				}
				closeProvider(pv)
			}
		}
		// missing secret or failing APPEND never fails a local save
	}

	msg, err := mime.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("bozza non memorizzabile: %w", err)
	}
	msg.FolderID = draftsID
	msg.AccountID = target.ID
	if uid != "" {
		msg.ID = fmt.Sprintf("m:%s:%s:%s", target.ID, draftsID, uid)
		msg.ProviderMessageID = uid
	} else {
		msg.ID = fmt.Sprintf("m:%s:%s:draft-%d", target.ID, draftsID, time.Now().UnixNano())
	}
	resolver := thread.NewResolver(app.DB)
	msg.ThreadID, err = resolver.Resolve(target.ID, draftsID, &msg)
	if err != nil {
		return nil, fmt.Errorf("resolve thread: %w", err)
	}
	// re-saving: a new server copy gets its UID-based id (the old id would
	// point the next sync at the deleted UID); offline keeps the old id
	if p.DraftID != "" {
		if uid == "" {
			msg.ID = p.DraftID
		} else if p.DraftID != msg.ID {
			_ = app.Messages.DeleteFull(p.DraftID)
		}
	}
	if err := app.Messages.UpsertBatch([]domain.Message{msg}); err != nil {
		return nil, fmt.Errorf("persist draft: %w", err)
	}
	_ = app.Accounts.RefreshFolderCounters(draftsID)
	return map[string]any{"draftId": msg.ID, "threadId": msg.ThreadID}, nil
}

// fetchAttachmentPart re-downloads the raw message on demand (never
// persisted) and extracts the named part. Shared by attachment.save and
// attachment.text: the keychain secret arrives from the dedicated Rust
// commands, or from the runtime vault when already restored.
func fetchAttachmentPart(app *App, messageID, name, secretParam string) ([]byte, error) {
	if messageID == "" || name == "" {
		return nil, fmt.Errorf("messageId e name sono obbligatori")
	}
	msg, err := app.Messages.Get(messageID)
	if err != nil {
		return nil, fmt.Errorf("messaggio non trovato")
	}
	present := false
	for _, a := range msg.Attachments {
		if a.Name == name {
			present = true
			break
		}
	}
	if !present {
		return nil, fmt.Errorf("allegato %q non presente nel messaggio", name)
	}
	accountID, _, mailbox, uid, err := app.Messages.ActionTarget(messageID)
	if err != nil {
		return nil, err
	}
	if uid == 0 || accountID == "" || mailbox == "" {
		return nil, fmt.Errorf("allegato non scaricabile: messaggio non collegato al server")
	}
	var imapCfg *domain.ImapConfig
	accounts, err := app.Accounts.List()
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		if accounts[i].ID == accountID {
			imapCfg = accounts[i].Imap
			break
		}
	}
	if imapCfg == nil {
		return nil, fmt.Errorf("account non configurato")
	}
	secret := auth.Secret(secretParam)
	if secretParam == "" {
		if s, ok := app.Secrets.Get(accountID); ok {
			secret = s
		}
	}
	if secret == "" {
		return nil, fmt.Errorf("segreto non disponibile: riconnetti l'account")
	}
	var target domain.Account
	for i := range accounts {
		if accounts[i].ID == accountID {
			target = accounts[i]
			break
		}
	}
	pv, perr := providerForAccount(app, &target, secret.Expose())
	if perr != nil {
		return nil, perr
	}
	raw, err := pv.FetchRaw(mailbox, uid)
	closeProvider(pv)
	if err != nil {
		return nil, fmt.Errorf("download non riuscito: %w", err)
	}
	data, err := mime.ExtractAttachment(raw, name)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// opAttachmentSave downloads one attachment to a client-chosen path. The
// raw message is re-fetched on demand (never persisted), the part is
// extracted client-side and written with 0600 permissions.
func opAttachmentSave(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		MessageID string `json:"messageId"`
		Name      string `json:"name"`
		Dest      string `json:"dest"`
		Secret    string `json:"secret,omitempty"`
	}
	if err := decode(params, &p); err != nil || p.MessageID == "" || p.Name == "" || p.Dest == "" {
		return nil, fmt.Errorf("messageId, name e dest sono obbligatori")
	}
	data, err := fetchAttachmentPart(app, p.MessageID, p.Name, p.Secret)
	if err != nil {
		return nil, err
	}
	dest := filepath.Clean(p.Dest)
	if !filepath.IsAbs(dest) || strings.HasSuffix(dest, string(filepath.Separator)) {
		return nil, fmt.Errorf("percorso di destinazione non valido")
	}
	if err := os.WriteFile(dest, data, 0o600); err != nil {
		return nil, fmt.Errorf("scrittura file non riuscita: %w", err)
	}
	return map[string]any{"path": dest, "size": len(data)}, nil
}

// maxPreviewBytes caps the inline image preview: the bytes travel JSON-
// encoded through the IPC bridge, so keep them modest.
const maxPreviewBytes = 10 << 20 // 10 MB

// opAttachmentPreview returns one image attachment's bytes for the inline
// viewer (ThreadPage): re-downloads the raw message on demand, sniffs the
// content type, and refuses anything that is not an image or over the cap.
func opAttachmentPreview(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		MessageID string `json:"messageId"`
		Name      string `json:"name"`
		Secret    string `json:"secret,omitempty"`
	}
	if err := decode(params, &p); err != nil || p.MessageID == "" || p.Name == "" {
		return nil, fmt.Errorf("messageId e name sono obbligatori")
	}
	data, err := fetchAttachmentPart(app, p.MessageID, p.Name, p.Secret)
	if err != nil {
		return nil, err
	}
	if len(data) > maxPreviewBytes {
		return nil, fmt.Errorf("immagine troppo grande per l'anteprima (limite %d MB): usa Apri o Scarica", maxPreviewBytes/(1<<20))
	}
	ctype := http.DetectContentType(data)
	if !strings.HasPrefix(ctype, "image/") {
		return nil, fmt.Errorf("anteprima disponibile solo per le immagini")
	}
	// []byte marshals base64: the UI builds a data: URL from mime + data
	return map[string]any{"mime": ctype, "data": data, "size": len(data)}, nil
}

// maxAttachmentTextBytes refuses absurdly large PDFs before downloading:
// the raw fetch would dominate the request for no usable prompt gain.
const maxAttachmentTextBytes = 50 << 20 // 50 MB

// opAttachmentText extracts the plain text of a PDF attachment (M12) so the
// local AI can read it: fetch raw → extract part → pdftext. Non-PDF parts
// are rejected; scanned PDFs (no text layer) return an empty text the UI
// annotates. Runs through the dedicated attachment_text Rust command.
func opAttachmentText(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		MessageID string `json:"messageId"`
		Name      string `json:"name"`
		Secret    string `json:"secret,omitempty"`
	}
	if err := decode(params, &p); err != nil || p.MessageID == "" || p.Name == "" {
		return nil, fmt.Errorf("messageId e name sono obbligatori")
	}
	msg, err := app.Messages.Get(p.MessageID)
	if err != nil {
		return nil, fmt.Errorf("messaggio non trovato")
	}
	var att *domain.Attachment
	for i := range msg.Attachments {
		if msg.Attachments[i].Name == p.Name {
			att = &msg.Attachments[i]
			break
		}
	}
	if att == nil {
		return nil, fmt.Errorf("allegato %q non presente nel messaggio", p.Name)
	}
	lower := strings.ToLower(p.Name)
	if !strings.Contains(strings.ToLower(att.ContentType), "pdf") && !strings.HasSuffix(lower, ".pdf") {
		return nil, fmt.Errorf("estrazione testo disponibile solo per allegati PDF")
	}
	if att.Size > maxAttachmentTextBytes {
		return nil, fmt.Errorf("PDF troppo grande per l'estrazione del testo (max 50 MB)")
	}
	data, err := fetchAttachmentPart(app, p.MessageID, p.Name, p.Secret)
	if err != nil {
		return nil, err
	}
	text, truncated, err := pdfdoc.Extract(data, pdfdoc.MaxChars)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"name":      p.Name,
		"text":      text,
		"chars":     len([]rune(text)),
		"truncated": truncated,
		"pages":     pdfdoc.PageCount(data),
	}, nil
}

// opDraftDelete removes a draft: IMAP delete best-effort, then local purge.
func opDraftDelete(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		DraftID string `json:"draftId"`
	}
	if err := decode(params, &p); err != nil || p.DraftID == "" {
		return nil, fmt.Errorf("draftId mancante")
	}
	msg, err := app.Messages.Get(p.DraftID)
	if err != nil {
		return nil, fmt.Errorf("bozza non trovata")
	}
	folderID := msg.FolderID
	if msg.ProviderMessageID != "" {
		var mailbox string
		if err := app.DB.QueryRow(
			`SELECT COALESCE(imap_name,'') FROM folders WHERE id=?`, folderID,
		).Scan(&mailbox); err == nil && mailbox != "" && msg.AccountID != "" {
			secret, ok := app.Secrets.Get(msg.AccountID)
			if ok && !secret.Empty() {
				if accs, lerr := app.Accounts.List(); lerr == nil {
					for i := range accs {
						if accs[i].ID == msg.AccountID {
							if pv, perr := providerForAccount(app, &accs[i], secret.Expose()); perr == nil {
								uid, _ := strconv.ParseUint(msg.ProviderMessageID, 10, 32)
								_ = pv.Delete(mailbox, []uint32{uint32(uid)})
								closeProvider(pv)
							}
							break
						}
					}
				}
			}
		}
	}
	if err := app.Messages.DeleteFull(p.DraftID); err != nil {
		return nil, err
	}
	_ = app.Accounts.RefreshFolderCounters(folderID)
	return map[string]bool{"deleted": true}, nil
}

// sentFolderOf returns the local Sent folder (id + IMAP mailbox name).
func sentFolderOf(app *App, accountID string) (string, string) {
	var id, mailbox string
	err := app.DB.QueryRow(
		`SELECT id, imap_name FROM folders WHERE account=? AND type='sent' LIMIT 1`, accountID,
	).Scan(&id, &mailbox)
	if err != nil {
		return "", ""
	}
	return id, mailbox
}

// messageIDFor builds the local id for a sent message.
func messageIDFor(accountID, folderID, uid string) string {
	if uid == "" {
		return fmt.Sprintf("m:%s:%s:sent-%d", accountID, folderID, time.Now().UnixNano())
	}
	return fmt.Sprintf("m:%s:%s:%s", accountID, folderID, uid)
}

// normalizeSmtp fills zero values and drops an empty host (derive later).
func normalizeSmtp(c *domain.SmtpConfig) *domain.SmtpConfig {
	if c == nil || c.Host == "" {
		return nil
	}
	out := *c
	if out.Port == 0 {
		out.Port = 465
	}
	if out.Security == "" {
		out.Security = smtp.SecurityTLS
	}
	if out.Username == "" {
		out.Username = ""
	}
	return &out
}

// withProvider builds the account provider for a server round-trip.
// folderID is used to resolve the account when not supplied.
func withProvider(app *App, folderID string, fn func(provider.MailProvider) error, accountIDs ...string) error {
	accountID := ""
	if len(accountIDs) > 0 {
		accountID = accountIDs[0]
	}
	if accountID == "" {
		if err := app.DB.QueryRow(`SELECT account FROM folders WHERE id=?`, folderID).Scan(&accountID); err != nil {
			return fmt.Errorf("account per cartella %s: %w", folderID, err)
		}
	}
	accounts, err := app.Accounts.List()
	if err != nil {
		return err
	}
	var target *domain.Account
	for i := range accounts {
		if accounts[i].ID == accountID {
			target = &accounts[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("account %s non trovato", accountID)
	}
	if target.Imap == nil {
		return nil // mock accounts: nothing to propagate
	}
	secret, ok := app.Secrets.Get(accountID)
	if !ok || secret.Empty() {
		return fmt.Errorf("credenziale non disponibile per l'account")
	}
	pv, err := providerForAccount(app, target, secret.Expose())
	if err != nil {
		return err
	}
	defer closeProvider(pv)
	return fn(pv)
}

// closeProvider releases provider network resources when supported
// (IMAP sessions; JMAP keeps no persistent connection).
func closeProvider(p provider.MailProvider) {
	if c, ok := p.(provider.Closer); ok {
		c.Close()
	}
}

// providerForAccount builds the transport for a stored account with the
// given credential: an IMAP session for generic-imap, an HTTP JMAP client
// for jmap (Imap.Host carries the session URL).
func providerForAccount(app *App, a *domain.Account, secret string) (provider.MailProvider, error) {
	if a.Imap == nil {
		return nil, fmt.Errorf("configurazione di connessione mancante per %s", a.Email)
	}
	switch a.Provider {
	case "jmap":
		return jmap.New(app.DB, a.Imap.Host, a.Imap.Username, secret), nil
	case "microsoft":
		// the access token arrives fresh from the Rust keychain bridge
		return graph.New(a.Imap.Host, a.Imap.Username, secret), nil
	case "", "generic-imap", "gmail-oauth":
		// gmail-oauth rides the IMAP transport with XOAUTH2 (the Rust bridge
		// swaps the refresh token for a fresh access token)
		return provider.NewImap(*a.Imap, secret), nil
	default:
		return nil, fmt.Errorf("provider %q non supportato", a.Provider)
	}
}

// newID returns a short random id with the given prefix.
func newID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(b)
}

// --- rules (M8) -------------------------------------------------------

// validRulesField/Op/Action gate the CRUD endpoints so a malformed rule can
// never enter the engine loop.
var (
	rulesFields  = map[string]struct{}{"from": {}, "to": {}, "subject": {}, "body": {}, "spam_score": {}, "has_attachment": {}}
	rulesOps     = map[string]struct{}{"contains": {}, "equals": {}, "starts_with": {}, "ends_with": {}, "at_least": {}, "any": {}}
	rulesActions = map[string]struct{}{"move": {}, "mark_read": {}, "star": {}, "mark_spam": {}}
)

// opRulesList returns every rule (enabled or not) for the settings UI.
func opRulesList(_ context.Context, app *App, _ json.RawMessage) (any, error) {
	return rule.New(app.DB).All()
}

// opRulesSave creates or updates one rule. Params = the rule object itself.
func opRulesSave(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var r domain.Rule
	if err := decode(params, &r); err != nil {
		return nil, fmt.Errorf("parametri non validi")
	}
	if _, ok := rulesFields[r.Field]; !ok {
		return nil, fmt.Errorf("campo non valido: %s", r.Field)
	}
	if _, ok := rulesOps[r.Op]; !ok {
		return nil, fmt.Errorf("operatore non valido: %s", r.Op)
	}
	if _, ok := rulesActions[r.Action]; !ok {
		return nil, fmt.Errorf("azione non valida: %s", r.Action)
	}
	if r.Action == "move" && r.Target == "" {
		return nil, fmt.Errorf("cartella destinazione mancante")
	}
	if strings.TrimSpace(r.Name) == "" {
		r.Name = "Regola senza nome"
	}
	if err := rule.New(app.DB).Save(&r); err != nil {
		return nil, err
	}
	return r, nil
}

// opRulesDelete removes one rule by id.
func opRulesDelete(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := decode(params, &p); err != nil || p.ID == "" {
		return nil, fmt.Errorf("id mancante")
	}
	if err := rule.New(app.DB).Delete(p.ID); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// --- pgp (M9) ---------------------------------------------------------

// opPGPStatus reports whether the account has a private keyring in the vault
// (plus its fingerprint) and how many contact public keys are stored.
func opPGPStatus(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		AccountID string `json:"accountId"`
	}
	if err := decode(params, &p); err != nil || p.AccountID == "" {
		return nil, fmt.Errorf("accountId mancante")
	}
	has := app.PGP.HasKey(p.AccountID)
	out := map[string]any{"hasKey": has, "fingerprint": ""}
	if has {
		out["fingerprint"] = app.PGP.Fingerprint(p.AccountID)
	}
	keys, _ := app.PGP.ListPubKeys()
	out["pubKeys"] = keys
	return out, nil
}

// opPGPGenerate creates the account keypair in the vault and returns the
// armored PUBLIC key (the private half never reaches the webview; Rust
// persists it in the keychain and re-delivers on restart).
func opPGPGenerate(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		AccountID string `json:"accountId"`
		Name      string `json:"name"`
		Email     string `json:"email"`
	}
	if err := decode(params, &p); err != nil || p.AccountID == "" {
		return nil, fmt.Errorf("accountId mancante")
	}
	pub, err := app.PGP.Generate(p.AccountID, p.Name, p.Email)
	if err != nil {
		return nil, err
	}
	return map[string]string{"publicKey": pub, "fingerprint": app.PGP.Fingerprint(p.AccountID)}, nil
}

// opPGPImportPrivate validates and installs an armored private keyring for
// the account (Rust has already stored it in the keychain by the time the
// webview calls this; the vault holds a copy in memory).
func opPGPImportPrivate(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		AccountID string `json:"accountId"`
		Armored   string `json:"armored"`
	}
	if err := decode(params, &p); err != nil || p.AccountID == "" || p.Armored == "" {
		return nil, fmt.Errorf("accountId e armored sono obbligatori")
	}
	if err := app.PGP.ImportPrivate(p.AccountID, p.Armored); err != nil {
		return nil, err
	}
	return map[string]string{"fingerprint": app.PGP.Fingerprint(p.AccountID)}, nil
}

func opPGPExportPublic(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		AccountID string `json:"accountId"`
	}
	if err := decode(params, &p); err != nil || p.AccountID == "" {
		return nil, fmt.Errorf("accountId mancante")
	}
	return map[string]string{"publicKey": mustExport(app, p.AccountID)}, nil
}

func mustExport(app *App, accountID string) string {
	pub, err := app.PGP.ExportPublic(accountID)
	if err != nil {
		return ""
	}
	return pub
}

func opPGPForget(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		AccountID string `json:"accountId"`
	}
	if err := decode(params, &p); err != nil || p.AccountID == "" {
		return nil, fmt.Errorf("accountId mancante")
	}
	app.PGP.Forget(p.AccountID)
	return map[string]bool{"ok": true}, nil
}

// opPGPPubSave stores a contact public key: {email, armored}.
func opPGPPubSave(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		Email   string `json:"email"`
		Armored string `json:"armored"`
	}
	if err := decode(params, &p); err != nil || p.Email == "" || p.Armored == "" {
		return nil, fmt.Errorf("email e armored sono obbligatori")
	}
	fp, err := app.PGP.SetPubKey(p.Email, p.Armored)
	if err != nil {
		return nil, err
	}
	return map[string]string{"fingerprint": fp}, nil
}

func opPGPPubList(_ context.Context, app *App, _ json.RawMessage) (any, error) {
	return app.PGP.ListPubKeys()
}

func opPGPPubDelete(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		Email string `json:"email"`
	}
	if err := decode(params, &p); err != nil || p.Email == "" {
		return nil, fmt.Errorf("email mancante")
	}
	if err := app.PGP.DeletePubKey(p.Email); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// --- import / export (M11) --------------------------------------------

// rawFor returns the raw RFC5322 bytes of a message: from the import_raw
// store for imported mail, otherwise re-fetched from the provider (same
// path attachments already use).
func rawFor(app *App, m *domain.Message) ([]byte, error) {
	if raw, err := app.Messages.GetRaw(m.ID); err != nil {
		return nil, err
	} else if raw != nil {
		return raw, nil
	}
	var mailbox string
	var accountID string
	if err := app.DB.QueryRow(
		`SELECT COALESCE(imap_name,''), COALESCE(account,'') FROM folders WHERE id=?`,
		m.FolderID).Scan(&mailbox, &accountID); err != nil || mailbox == "" {
		return nil, fmt.Errorf("raw non disponibile per questo messaggio")
	}
	secret, ok := app.Secrets.Get(accountID)
	if !ok || secret.Empty() {
		return nil, fmt.Errorf("credenziale non disponibile: riconnetti l'account")
	}
	accounts, err := app.Accounts.List()
	if err != nil {
		return nil, err
	}
	var target *domain.Account
	for i := range accounts {
		if accounts[i].ID == accountID {
			target = &accounts[i]
			break
		}
	}
	if target == nil || target.Imap == nil {
		return nil, fmt.Errorf("raw non disponibile per il provider di questo account")
	}
	pv, perr := providerForAccount(app, target, secret.Expose())
	if perr != nil {
		return nil, perr
	}
	defer closeProvider(pv)
	uid, _ := strconv.ParseUint(m.ProviderMessageID, 10, 32)
	return pv.FetchRaw(mailbox, uint32(uid))
}

// opExportMessage writes one message as a .eml file to the client-chosen path.
func opExportMessage(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		MessageID string `json:"messageId"`
		Dest      string `json:"dest"`
	}
	if err := decode(params, &p); err != nil || p.MessageID == "" || p.Dest == "" {
		return nil, fmt.Errorf("messageId e dest sono obbligatori")
	}
	dest, err := safeDest(p.Dest)
	if err != nil {
		return nil, err
	}
	m, err := app.Messages.Get(p.MessageID)
	if err != nil {
		return nil, err
	}
	raw, err := rawFor(app, &m)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(dest, raw, 0o600); err != nil {
		return nil, fmt.Errorf("scrittura file non riuscita: %w", err)
	}
	return map[string]any{"path": dest, "size": len(raw)}, nil
}

// safeDest validates a client-provided absolute path (no traversal tricks).
func safeDest(dest string) (string, error) {
	clean := filepath.Clean(dest)
	if !filepath.IsAbs(clean) || strings.HasSuffix(clean, string(filepath.Separator)) {
		return "", fmt.Errorf("percorso di destinazione non valido")
	}
	return clean, nil
}

// opExportMailbox writes a whole folder as an mboxrd archive: every message
// is fetched raw (import store first, then provider) and appended with a
// "From " separator and mboxrd escaping.
func opExportMailbox(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		FolderID string `json:"folderId"`
		Dest     string `json:"dest"`
	}
	if err := decode(params, &p); err != nil || p.FolderID == "" || p.Dest == "" {
		return nil, fmt.Errorf("folderId e dest sono obbligatori")
	}
	dest, err := safeDest(p.Dest)
	if err != nil {
		return nil, err
	}
	msgs, err := app.Messages.ListByFolder(p.FolderID)
	if err != nil {
		return nil, err
	}
	buf := &bytes.Buffer{}
	exported, failed := 0, 0
	for i := range msgs {
		raw, rerr := rawFor(app, &msgs[i])
		if rerr != nil {
			failed++
			continue // skip a missing raw, never abort the archive
		}
		// classic mbox separator: From <sender> <asctime>
		sep := fmt.Sprintf("From %s %s", msgs[i].From.Email, msgs[i].Date.UTC().Format("Mon Jan 02 15:04:05 2006"))
		mbox.Append(buf, sep, raw)
		exported++
	}
	if exported == 0 && failed > 0 {
		return nil, fmt.Errorf("nessun messaggio esportabile: %d non scaricati", failed)
	}
	if err := os.WriteFile(dest, buf.Bytes(), 0o600); err != nil {
		return nil, fmt.Errorf("scrittura archivio non riuscita: %w", err)
	}
	return map[string]int{"exported": exported, "failed": failed}, nil
}

// opImportMails reads a .eml or .mbox file and appends every message to the
// account's local "Importate" folder. Import is LOCAL-ONLY: nothing is
// pushed to the server (the folder has no imap_name, so sync skips it) and
// the raw bytes are kept in the import_raw table for faithful re-export.
func opImportMails(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		AccountID string `json:"accountId"`
		Src       string `json:"src"`
	}
	if err := decode(params, &p); err != nil || p.AccountID == "" || p.Src == "" {
		return nil, fmt.Errorf("accountId e src sono obbligatori")
	}
	src := filepath.Clean(p.Src)
	if !filepath.IsAbs(src) {
		return nil, fmt.Errorf("percorso sorgente non valido")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("lettura file non riuscita: %w", err)
	}
	folderID, ferr := app.Accounts.EnsureImportFolder(p.AccountID)
	if ferr != nil {
		return nil, ferr
	}
	raws := mbox.Split(data)
	if len(raws) == 0 {
		return nil, fmt.Errorf("nessun messaggio riconosciuto nel file")
	}
	// continue after earlier imports: restarting at 1 would overwrite them
	var maxUID uint32
	if err := app.DB.QueryRow(`SELECT COALESCE(MAX(CAST(provider_msg_id AS INTEGER)),0) FROM messages WHERE folder=?`,
		folderID).Scan(&maxUID); err != nil {
		return nil, err
	}
	resolver := thread.NewResolver(app.DB)
	imported, skipped, nextUID := 0, 0, maxUID+1
	for _, raw := range raws {
		msg, perr := mime.Parse(raw)
		if perr != nil {
			skipped++
			continue
		}
		msg.ID = fmt.Sprintf("m:%s:%s:imp:%d", p.AccountID, folderID, nextUID)
		msg.ProviderMessageID = strconv.FormatUint(uint64(nextUID), 10)
		nextUID++
		msg.FolderID = folderID
		msg.AccountID = p.AccountID
		msg.IsUnread = true
		msg.ThreadID, perr = resolver.Resolve(p.AccountID, folderID, &msg)
		if perr != nil {
			skipped++
			continue
		}
		assess.Apply(&msg)
		if err := app.Messages.UpsertBatch([]domain.Message{msg}); err != nil {
			skipped++
			continue
		}
		if err := app.Messages.SaveRaw(msg.ID, raw); err != nil {
			skipped++
			continue
		}
		imported++
	}
	_ = app.Accounts.RefreshFolderCounters(folderID)
	app.Events.SyncCompleted(p.AccountID)
	return map[string]any{"imported": imported, "skipped": skipped, "folderId": folderID}, nil
}

// NOTE: database backup/restore is NOT an engine op: the adiantum encrypting
// VFS cannot VACUUM INTO arbitrary host paths, so the snapshot is taken at
// file level in Rust (backup_create/backup_restore) with the engine stopped.
