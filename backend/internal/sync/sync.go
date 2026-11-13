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
	"errors"
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
	incremental := ref.LastUID != 0

	// UIDVALIDITY changed on the server: local UIDs are meaningless. Checked
	// before the first batch is stored (providers stream batches while they
	// fetch) and once more after the provider returns.
	wiped := false
	wipeIfStale := func(uidValidity uint32) error {
		if wiped || ref.UIDValidity == 0 || uidValidity == ref.UIDValidity {
			return nil
		}
		if err := e.messages.WipeFolder(ref.FolderID); err != nil {
			return fmt.Errorf("wipe folder: %w", err)
		}
		wiped = true
		return nil
	}

	ingest := e.ingester(accountID, ref, p, incremental, true)
	var minSeen uint32 // oldest UID fetched this pass (backfill cursor on a first sync)
	seen := func(raws []provider.RawMessage) {
		for _, r := range raws {
			if minSeen == 0 || r.UID < minSeen {
				minSeen = r.UID
			}
		}
	}

	fs, err := p.SyncFolder(provider.FolderRef{
		FolderID:    ref.FolderID,
		MailboxName: ref.MailboxName,
		UIDValidity: ref.UIDValidity,
		LastUID:     ref.LastUID,
		OnBatch: func(uidValidity uint32, raws []provider.RawMessage) error {
			if err := wipeIfStale(uidValidity); err != nil {
				return err
			}
			seen(raws)
			return ingest(raws)
		},
	})
	if err != nil {
		return err
	}
	if err := wipeIfStale(fs.UIDValidity); err != nil {
		return err
	}
	// providers that do not stream (JMAP, Graph) return everything here
	seen(fs.Messages)
	if err := ingest(fs.Messages); err != nil {
		return err
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

	highest, lowest := ref.LastUID, ref.LowestUID
	if ref.UIDValidity != 0 && fs.UIDValidity != ref.UIDValidity {
		// folder was wiped above: the old marks are meaningless
		highest, lowest = 0, 0
	}
	if n := len(fs.AllUIDs); n > 0 && fs.AllUIDs[n-1] > highest {
		highest = fs.AllUIDs[n-1]
	}
	if lowest == 0 {
		lowest = minSeen // first sync: history below the window is backfilled
	}
	return e.accounts.UpdateFolderSyncState(ref.FolderID, fs.UIDValidity, highest, lowest)
}

// ingester returns a function that parses, threads, rules-matches and stores
// one batch of raw messages in its own transaction (a big sync holds one
// batch in memory, not the whole window). announce emits message.created per
// stored message (live push); applyRules runs the account's rules, which may
// act on the server (move, mark read).
func (e *Engine) ingester(accountID string, ref account.SyncRef, p provider.MailProvider, announce, applyRules bool) func([]provider.RawMessage) error {
	var rules []domain.Rule
	rulesLoaded := !applyRules
	return func(raws []provider.RawMessage) error {
		if len(raws) == 0 {
			return nil
		}
		if !rulesLoaded {
			// M8: load the enabled rules once per folder sync
			var ruleErr error
			if rules, ruleErr = rule.New(e.db).ForAccount(accountID); ruleErr != nil {
				log.Printf("load rules for %s: %v", accountID, ruleErr)
			}
			rulesLoaded = true
		}
		var pendingReads []uint32

		batch := make([]domain.Message, 0, len(raws))
		for _, raw := range raws {
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
		if announce {
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
		return nil
	}
}

// errStaleFolder stops a backfill whose folder changed UIDVALIDITY: the next
// regular sync wipes and restarts it.
var errStaleFolder = errors.New("uidvalidity changed")

// BackfillStep downloads up to limit older messages of the first folder that
// still has history on the server (skipping folders in done, which it fills
// as they complete) and moves its cursor down after every stored batch, so an
// interruption resumes where it stopped. Rules are not applied: they act on
// the server and are meant for incoming mail, not years of archive. It
// returns false once every folder is complete or the provider cannot
// backfill.
func (e *Engine) BackfillStep(accountID string, p provider.MailProvider, limit int, done map[string]bool) (bool, error) {
	bf, ok := p.(provider.Backfiller)
	if !ok {
		return false, nil
	}
	refs, err := e.accounts.SyncRefs(accountID)
	if err != nil {
		return false, err
	}
	for _, ref := range refs {
		// lowest 0: never synced (or empty); 1: nothing can be older
		if done[ref.FolderID] || ref.UIDValidity == 0 || ref.LowestUID <= 1 {
			continue
		}
		ingest := e.ingester(accountID, ref, p, false, false)
		remaining, err := bf.FetchOlder(ref.MailboxName, ref.LowestUID, limit,
			func(uidValidity, lowest uint32, raws []provider.RawMessage) error {
				if uidValidity != ref.UIDValidity {
					return errStaleFolder
				}
				if err := ingest(raws); err != nil {
					return err
				}
				return e.accounts.UpdateFolderLowestUID(ref.FolderID, uidValidity, lowest)
			})
		if errors.Is(err, errStaleFolder) {
			done[ref.FolderID] = true
			continue
		}
		if err != nil {
			return true, fmt.Errorf("backfill %s/%s: %w", accountID, ref.FolderID, err)
		}
		if remaining == 0 {
			done[ref.FolderID] = true
			// cursor 1 = complete: later watchers skip the folder without a round trip
			if err := e.accounts.UpdateFolderLowestUID(ref.FolderID, ref.UIDValidity, 1); err != nil {
				return true, err
			}
		}
		e.Bus.Publish("sync.backfill", map[string]any{
			"accountId": accountID, "folderId": ref.FolderID, "remaining": remaining,
		})
		return true, nil
	}
	return false, nil
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