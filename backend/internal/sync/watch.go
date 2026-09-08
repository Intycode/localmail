package sync

// Push watcher: after the initial sync, keep accounts fresh with IMAP IDLE
// and a periodic fallback. Each watched folder gets its own goroutine and
// its own IMAP session (IDLE occupies the session's selected mailbox, so
// multi-folder push needs one session per folder): notify -> end IDLE ->
// incremental sync -> re-enter IDLE. Incremental syncs are serialized by a
// shared mutex (single SQLite writer). Servers without IDLE fall back to a
// 5 minute poll per folder.

import (
	"errors"
	"sync"
	"time"

	"mail/backend/internal/email/imap"
	"mail/backend/internal/provider"
)

const (
	// pollInterval is the fallback refresh cadence (no IDLE support).
	pollInterval = 5 * time.Minute
	// maxWatchedFolders caps the IMAP sessions per account: INBOX first,
	// then the SPECIAL-USE folders. Extra custom folders rely on the next
	// full sync (or the poll fallback).
	maxWatchedFolders = 8
)

// Supervisor owns one Watcher per account.
type Supervisor struct {
	engine *Engine

	mu sync.Mutex
	ws map[string]*Watcher
}

func NewSupervisor(e *Engine) *Supervisor {
	return &Supervisor{engine: e, ws: make(map[string]*Watcher)}
}

// Start (re)launches the watcher for an account, stopping any previous one.
// The primary provider watches mailboxes[0]; extra mailboxes each get their
// own provider via newProvider (a failed dial just degrades to fewer
// watched folders). The supervisor owns every provider it receives or
// creates and closes them all when the watcher stops.
func (s *Supervisor) Start(
	accountID string,
	primary provider.MailProvider,
	mailboxes []string,
	newProvider func() (provider.MailProvider, error),
) {
	s.Stop(accountID)
	w := &Watcher{stopCh: make(chan struct{}), done: make(chan struct{})}
	s.mu.Lock()
	s.ws[accountID] = w
	s.mu.Unlock()
	go w.run(s.engine, accountID, primary, mailboxes, newProvider)
}

// Stop ends the watcher for an account (no-op when none is running).
func (s *Supervisor) Stop(accountID string) {
	s.mu.Lock()
	w := s.ws[accountID]
	delete(s.ws, accountID)
	s.mu.Unlock()
	if w != nil {
		w.Stop()
	}
}

// StopAll ends every watcher (engine shutdown / tests).
func (s *Supervisor) StopAll() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.ws))
	for id := range s.ws {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.Stop(id)
	}
}

// Watcher supervises one account: one IDLE session per folder plus a shared
// sync mutex.
type Watcher struct {
	stopCh chan struct{}
	done   chan struct{}
	once   sync.Once
}

// Stop ends the watcher and waits for its goroutines to exit.
func (w *Watcher) Stop() {
	w.once.Do(func() { close(w.stopCh) })
	<-w.done
}

func (w *Watcher) run(
	e *Engine,
	accountID string,
	primary provider.MailProvider,
	mailboxes []string,
	newProvider func() (provider.MailProvider, error),
) {
	defer close(w.done)
	// the watcher owns the primary provider lifetime
	defer func() {
		if c, ok := primary.(provider.Closer); ok {
			c.Close()
		}
	}()
	if len(mailboxes) == 0 {
		return // nothing to watch
	}

	// incremental syncs from any folder go through one mutex: the engine
	// pipeline writes to SQLite (single writer) and coalesces events
	var syncMu sync.Mutex
	syncOnce := func(p provider.MailProvider) {
		syncMu.Lock()
		defer syncMu.Unlock()
		e.syncPushQuiet(accountID, p)
	}

	var wg sync.WaitGroup
	for i, mailbox := range mailboxes {
		p := primary
		if i > 0 {
			np, err := newProvider()
			if err != nil {
				continue // degrade: watch fewer folders, never crash
			}
			p = np
			defer func(pv provider.MailProvider) {
				if c, ok := pv.(provider.Closer); ok {
					c.Close()
				}
			}(p)
		}
		wg.Add(1)
		go func(pv provider.MailProvider, mb string, isPrimary bool) {
			defer wg.Done()
			w.watchOne(e, accountID, pv, mb, isPrimary, syncOnce)
		}(p, mailbox, i == 0)
	}
	wg.Wait()
}

// watchOne runs the IDLE loop for one mailbox. Real failures on the primary
// mailbox surface as sync.error (as before); failures on secondary folders
// just end that folder's loop — the primary keeps pushing, and the next
// full sync (app start / manual) heals the rest.
func (w *Watcher) watchOne(
	e *Engine,
	accountID string,
	p provider.MailProvider,
	mailbox string,
	isPrimary bool,
	syncOnce func(provider.MailProvider),
) {
	notify := make(chan struct{}, 8)
	idleStop, idleErr := p.Idle(mailbox, notify)
	idleOK := idleErr == nil
	if !idleOK {
		if isPrimary && !errors.Is(idleErr, imap.ErrIdleUnsupported) {
			// a real failure (network/auth): surface it like a sync error
			e.Bus.SyncError(accountID, "watch: "+idleErr.Error())
			return
		}
		if !isPrimary && !errors.Is(idleErr, imap.ErrIdleUnsupported) {
			return // secondary folder: degrade silently
		}
	}

	var pollC <-chan time.Time
	if !idleOK {
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		pollC = t.C
	}

	for {
		select {
		case <-w.stopCh:
			if idleOK {
				idleStop()
			}
			return

		case <-notify:
			// end IDLE to run commands, then incremental sync + re-idle
			if idleOK {
				idleStop()
			}
			if w.stopped() {
				return
			}
			syncOnce(p)
			if w.stopped() {
				return
			}
			if idleOK {
				var err error
				idleStop, err = p.Idle(mailbox, notify)
				if err != nil {
					// session dropped (network change): fall back to polling
					idleOK = false
					t := time.NewTicker(pollInterval)
					defer t.Stop()
					pollC = t.C
				}
			}

		case <-pollC:
			syncOnce(p)
		}
	}
}

func (w *Watcher) stopped() bool {
	select {
	case <-w.stopCh:
		return true
	default:
		return false
	}
}
