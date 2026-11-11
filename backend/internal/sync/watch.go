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
	"log"
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

// Reconnect backoff: first retry after reconnectBase, doubling up to
// reconnectMax. Vars (not consts) so tests can shrink them.
var (
	reconnectBase = 5 * time.Second
	reconnectMax  = 5 * time.Minute
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
	syncOnce := func(p provider.MailProvider) error {
		syncMu.Lock()
		defer syncMu.Unlock()
		return e.SyncPush(accountID, p)
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
		}
		wg.Add(1)
		go func(pv provider.MailProvider, mb string, isPrimary bool) {
			defer wg.Done()
			w.watchOne(e, accountID, pv, mb, isPrimary, syncOnce, newProvider)
		}(p, mailbox, i == 0)
	}
	wg.Wait()
}

// watchOne keeps one mailbox watched for the lifetime of the watcher. When
// the session breaks (network change, standby, server restart) it closes the
// provider, waits with a growing delay, redials through newProvider and runs
// a catch-up sync before idling again, so mail that arrived while offline
// shows up. watchOne owns every provider it holds and closes it on exit.
func (w *Watcher) watchOne(
	e *Engine,
	accountID string,
	p provider.MailProvider,
	mailbox string,
	isPrimary bool,
	syncOnce func(provider.MailProvider) error,
	newProvider func() (provider.MailProvider, error),
) {
	cur := p
	defer func() { closeProvider(cur) }()
	for attempt, catchUp := 0, false; ; {
		start := time.Now()
		err := w.runSession(cur, mailbox, catchUp, syncOnce)
		if err == nil {
			return // watcher stopped
		}
		if time.Since(start) > time.Minute {
			attempt = 0 // the session was healthy for a while: restart the backoff
		}
		log.Printf("watch %s/%s: %v (riconnessione)", accountID, mailbox, err)
		if attempt == 0 && isPrimary {
			// surface the first failure like a sync error; retries stay quiet
			e.Bus.SyncError(accountID, "watch: "+err.Error())
		}
		closeProvider(cur)
		if !w.sleep(backoff(attempt)) {
			return
		}
		attempt++
		if np, nerr := newProvider(); nerr == nil {
			cur = np
		} else {
			log.Printf("watch %s/%s: nuova connessione: %v", accountID, mailbox, nerr)
		}
		catchUp = true
	}
}

// runSession idles (or polls, when the server has no IDLE) on one mailbox.
// It returns nil when the watcher is stopped and an error when the session
// broke and must be redialed. catchUp runs one sync first (after a redial).
func (w *Watcher) runSession(
	p provider.MailProvider,
	mailbox string,
	catchUp bool,
	syncOnce func(provider.MailProvider) error,
) error {
	if catchUp {
		if err := syncOnce(p); err != nil {
			return err
		}
	}
	notify := make(chan struct{}, 8)
	idleStop, err := p.Idle(mailbox, notify)
	idleOK := err == nil
	if err != nil && !errors.Is(err, imap.ErrIdleUnsupported) {
		return err
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
			return nil

		case <-notify:
			// new mail, or the session closed: end IDLE to run commands,
			// then incremental sync + re-idle
			if idleOK {
				idleStop()
			}
			if w.stopped() {
				return nil
			}
			if err := syncOnce(p); err != nil {
				return err
			}
			if w.stopped() {
				return nil
			}
			if idleOK {
				if idleStop, err = p.Idle(mailbox, notify); err != nil {
					return err
				}
			}

		case <-pollC:
			if err := syncOnce(p); err != nil {
				return err
			}
		}
	}
}

// backoff is the wait before reconnect attempt n (0-based): base, 2x, 4x… capped.
func backoff(n int) time.Duration {
	d := reconnectBase
	for i := 0; i < n && d < reconnectMax; i++ {
		d *= 2
	}
	if d > reconnectMax {
		d = reconnectMax
	}
	return d
}

// sleep waits d or until the watcher stops; false means stopped.
func (w *Watcher) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-w.stopCh:
		return false
	case <-t.C:
		return true
	}
}

func closeProvider(p provider.MailProvider) {
	if c, ok := p.(provider.Closer); ok {
		c.Close()
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
