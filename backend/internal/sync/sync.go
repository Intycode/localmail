// Package sync is the Sync Engine.
//
// Pipeline:
//
//	provider -> sync engine -> MIME parsing -> thread resolution -> SQLite
//	-> events -> frontend
//
// Performance notes: messages are persisted in one transaction per folder,
// FETCHes happen in batches (see imap.FetchBatch), and only UIDs greater than
// the last synced UID are pulled after the initial window.
package sync

import (
	"fmt"
	"log"
	"strconv"
	"sync"

	"database/sql"

	"mail/backend/internal/account"
	"mail/backend/internal/assess"
	"mail/backend/internal/domain"
	"mail/backend/internal/rule"
	"mail/backend/internal/email/mime"
	"mail/backend/internal/events"
	"mail/backend/internal/message"
	"mail/backend/internal/provider"
	"mail/backend/internal/thread"
)

// Engine runs a provider's data through the pipeline and persists it locally.
type Engine struct {
	Provider provider.MailProvider
	Bus      *events.Bus

	// mu guards Provider swaps: a user-triggered sync and a push watcher
	// must not race on the shared field.
	mu sync.Mutex

	db       *sql.DB
	messages *message.Store
	resolver *thread.Resolver
	accounts *account.Store
}

func New(db *sql.DB, p provider.MailProvider, bus *events.Bus) *Engine {
	return &Engine{
		Provider: p,
		Bus:      bus,
		db:       db,
		messages: message.New(db),
		resolver: thread.NewResolver(db),
		accounts: account.New(db),
	}
}

// SyncAccountWith runs a full one-shot sync with a specific provider
// (per-account factory). The IPC layer uses it to inject the account's own
// transport + runtime secret. The provider is CLOSED at the end (one-shot).
func (e *Engine) SyncAccountWith(accountID string, p provider.MailProvider) error {
	e.mu.Lock()
	orig := e.Provider
	e.Provider = p
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.Provider = orig
		e.mu.Unlock()
		if c, ok := p.(provider.Closer); ok {
			c.Close()
		}
	}()
	return e.syncAll(accountID, p, false)
}

// SyncPush runs one incremental pass reusing the watcher's provider session.
// It emits message.created for every new message instead of the full
// sync.started/completed cycle (which would flap the UI indicator).
func (e *Engine) SyncPush(accountID string, p provider.MailProvider) error {
	return e.syncAll(accountID, p, true)
}

// syncPushQuiet runs a watcher pass: message.created events flow, but a
// failure is only logged (the watcher stays alive for the next notify/poll).
func (e *Engine) syncPushQuiet(accountID string, p provider.MailProvider) {
	if err := e.syncAll(accountID, p, true); err != nil {
		log.Printf("watch sync %s: %v", accountID, err)
	}
}

// SyncAccount performs a sync of every syncable folder of the account using
// the engine's own provider (mock/smoke path).
func (e *Engine) SyncAccount(accountID string) error {
	e.mu.Lock()
	p := e.Provider
	e.mu.Unlock()
	if err := e.syncAll(accountID, p, false); err != nil {
		return err
	}
	// one-shot semantics: release the engine-owned provider
	if c, ok := p.(provider.Closer); ok {
		c.Close()
	}
	return nil
}

// syncAll syncs every syncable folder with the given provider. push passes
// skip the sync.started/progress/completed lifecycle events.
func (e *Engine) syncAll(accountID string, p provider.MailProvider, push bool) error {
	if !push {
		e.Bus.SyncStarted(accountID)
		defer func() { e.Bus.SyncCompleted(accountID) }()
	}

	refs, err := e.accounts.SyncRefs(accountID)
	if err != nil {
		if !push {
			e.Bus.SyncError(accountID, err.Error())
		}
		return fmt.Errorf("sync refs %s: %w", accountID, err)
	}

	if !push {
		e.Bus.SyncProgress(accountID, 0, len(refs))
	}
	for i, ref := range refs {
		if err := e.syncFolder(accountID, ref, p); err != nil {
			if !push {
				e.Bus.SyncError(accountID, err.Error())
			}
			return fmt.Errorf("sync %s/%s: %w", accountID, ref.FolderID, err)
		}
		if !push {
			e.Bus.SyncProgress(accountID, i+1, len(refs))
		}
	}
	return nil
}

func (e *Engine) syncFolder(accountID string, ref account.SyncRef, p provider.MailProvider) error {
	fs, err := p.SyncFolder(provider.FolderRef{
		FolderID:    ref.FolderID,
		MailboxName: ref.MailboxName,
		UIDValidity: ref.UIDValidity,
		LastUID:     ref.LastUID,
	})
	if err != nil {
		return err
	}

	// UIDVALIDITY changed on the server: local UIDs are meaningless.
	if ref.UIDValidity != 0 && fs.UIDValidity != ref.UIDValidity {
		if err := e.messages.WipeFolder(ref.FolderID); err != nil {
			return fmt.Errorf("wipe folder: %w", err)
		}
	}

	incremental := ref.LastUID != 0
	if len(fs.Messages) > 0 {
		// M8: load the enabled rules once per folder sync
		rules, ruleErr := rule.New(e.db).ForAccount(accountID)
		if ruleErr != nil {
			log.Printf("load rules for %s: %v", accountID, ruleErr)
		}
		var pendingReads []uint32

		batch := make([]domain.Message, 0, len(fs.Messages))
		for _, raw := range fs.Messages {
			msg, err := mime.Parse(raw.Data)
			if err != nil {
				// skip unparseable messages; never abort the whole sync
				continue
			}
			msg.ID = messageID(accountID, ref.FolderID, raw.UID)
			msg.ThreadID, err = e.resolver.Resolve(accountID, ref.FolderID, &msg)
			if err != nil {
				return fmt.Errorf("resolve thread: %w", err)
			}
			msg.FolderID = ref.FolderID
			msg.AccountID = accountID
			msg.ProviderMessageID = strconv.FormatUint(uint64(raw.UID), 10)
			msg.IsUnread = !hasFlag(raw.Flags, "\\Seen")
			// M7: local spam/phishing assessment at ingest (no network, no
			// auto-move: flags and score surface as UI warnings)
			assess.Apply(&msg)

			// M8: rules in position order; all matches apply. A move happens
			// SERVER-SIDE before persisting: the message then arrives through
			// its target folder on the next folder sync.
			dropped := false
			for _, r := range rule.Matching(rules, &msg) {
				switch r.Action {
				case "mark_read":
					msg.IsUnread = false
					pendingReads = append(pendingReads, raw.UID)
				case "star":
					msg.IsStarred = true
				case "mark_spam":
					msg.SpamScore = 100
				case "move":
					if dropped {
						continue // a move already matched: first one wins
					}
					if name := e.folderNameByID(accountID, r.Target); name != "" {
						if err := p.Move(ref.MailboxName, []uint32{raw.UID}, name); err != nil {
							log.Printf("rule move uid %d -> %s: %v", raw.UID, name, err)
							// move failed: persist normally (never lose mail)
						} else {
							dropped = true
						}
					}
				}
			}
			if dropped {
				continue // arrives via its target folder instead
			}
			batch = append(batch, msg)
		}
		if err := e.messages.UpsertBatch(batch); err != nil {
			return err
		}
		if incremental {
			// push pass: announce every new message so the UI refreshes live
			for i := range batch {
				e.Bus.MessageCreated(batch[i])
			}
		}
		// best-effort provider-side flag changes for mark_read rules
		if len(pendingReads) > 0 {
			if err := p.MarkRead(ref.MailboxName, pendingReads); err != nil {
				log.Printf("rule mark read %s: %v", ref.MailboxName, err)
			}
		}
	}

	// Reconcile server-side deletions: local UIDs missing from the server.
	if !fs.Partial {
		local, err := e.messages.LocalUIDs(ref.FolderID)
		if err != nil {
			return err
		}
		if missing := difference(local, fs.AllUIDs); len(missing) > 0 {
			if err := e.messages.DeleteUIDs(ref.FolderID, missing); err != nil {
				return err
			}
		}
	}

	if len(fs.Flags) > 0 {
		unread := make(map[string]bool, len(fs.Flags))
		for uid, flags := range fs.Flags {
			unread[messageID(accountID, ref.FolderID, uid)] = !hasFlag(flags, "\\Seen")
		}
		if err := e.messages.ApplyServerRead(ref.FolderID, unread); err != nil {
			return err
		}
	}

	highest := ref.LastUID
	if ref.UIDValidity != 0 && fs.UIDValidity != ref.UIDValidity {
		highest = 0 // folder was wiped above: the old high-water mark is meaningless
	}
	if n := len(fs.AllUIDs); n > 0 && fs.AllUIDs[n-1] > highest {
		highest = fs.AllUIDs[n-1]
	}
	return e.accounts.UpdateFolderSyncState(ref.FolderID, fs.UIDValidity, highest)
}

func messageID(accountID, folderID string, uid uint32) string {
	return fmt.Sprintf("m:%s:%s:%d", accountID, folderID, uid)
}

func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

// folderNameByID returns the remote mailbox name of a local folder (used as
// the provider.Move target for move rules). Empty when the folder does not
// belong to the account.
func (e *Engine) folderNameByID(accountID, folderID string) string {
	var name string
	_ = e.db.QueryRow(
		`SELECT COALESCE(imap_name,'') FROM folders WHERE id=? AND account=?`,
		folderID, accountID).Scan(&name)
	return name
}

// difference returns a - b.
func difference(a, b []uint32) []uint32 {
	set := make(map[uint32]struct{}, len(b))
	for _, v := range b {
		set[v] = struct{}{}
	}
	var out []uint32
	for _, v := range a {
		if _, ok := set[v]; !ok {
			out = append(out, v)
		}
	}
	return out
}