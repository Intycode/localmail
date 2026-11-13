package sync

import (
	"errors"
	"fmt"
	"testing"

	"mail/backend/internal/provider"
)

// windowProvider behaves like the IMAP provider: a first sync returns only
// the newest `window` messages (AllUIDs is complete), FetchOlder serves the
// history below a cursor in chunks of `chunk`, newest chunk first.
type windowProvider struct {
	benchProvider
	window, chunk int
	validity      uint32
	failAfter     int // FetchOlder batches before an injected error (0 = never)
	fetched       map[string]int // mailbox/uid -> downloads
}

func (p *windowProvider) SyncFolder(ref provider.FolderRef) (provider.FolderSync, error) {
	fs, _ := p.benchProvider.SyncFolder(ref)
	fs.UIDValidity = p.validity
	if ref.LastUID == 0 || ref.UIDValidity != p.validity {
		fs.Messages = p.msgs[max(0, len(p.msgs)-p.window):]
	}
	for _, m := range fs.Messages {
		p.fetched[fmt.Sprintf("%s/%d", ref.MailboxName, m.UID)]++
	}
	return fs, nil
}

func (p *windowProvider) FetchOlder(mailbox string, below uint32, limit int, onBatch provider.BackfillBatch) (int, error) {
	n := 0
	for n < len(p.msgs) && p.msgs[n].UID < below {
		n++
	}
	older := p.msgs[max(0, n-limit):n]
	for end := len(older); end > 0; end -= p.chunk {
		if p.failAfter > 0 {
			if p.failAfter--; p.failAfter == 0 {
				return 0, errors.New("connection reset")
			}
		}
		chunk := older[max(0, end-p.chunk):end]
		for _, m := range chunk {
			p.fetched[fmt.Sprintf("%s/%d", mailbox, m.UID)]++
		}
		if err := onBatch(p.validity, chunk[0].UID, chunk); err != nil {
			return 0, err
		}
	}
	return n - len(older), nil
}

func newWindowProvider(n int) *windowProvider {
	return &windowProvider{benchProvider: *benchMsgs(n), window: 200, chunk: 50, validity: 7, fetched: map[string]int{}}
}

func countMessages(t *testing.T, e *Engine) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE folder='f-inbox'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func lowestUID(t *testing.T, e *Engine) uint32 {
	t.Helper()
	var u uint32
	if err := e.db.QueryRow(`SELECT lowest_uid FROM folders WHERE id='f-inbox'`).Scan(&u); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestBackfillDownloadsWholeHistoryOnce(t *testing.T) {
	engine, _ := setupEngine(t)
	p := newWindowProvider(730)

	if err := engine.SyncAccountWith("a1", p); err != nil {
		t.Fatal(err)
	}
	if got := countMessages(t, engine); got != 200 {
		t.Fatalf("first sync stored %d, want the 200-message window", got)
	}
	if got := lowestUID(t, engine); got != 531 {
		t.Fatalf("cursor after first sync = %d, want 531", got)
	}

	// one step = 100 messages; a fault mid-step keeps the batches already stored
	p.failAfter = 2
	done := map[string]bool{}
	if _, err := engine.BackfillStep("a1", p, 100, done); err == nil {
		t.Fatal("want the injected error")
	}
	if got := lowestUID(t, engine); got != 481 {
		t.Fatalf("cursor after interrupted step = %d, want 481 (one batch stored)", got)
	}

	steps := 0
	for {
		more, err := engine.BackfillStep("a1", p, 100, done)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
		if steps++; steps > 20 {
			t.Fatal("backfill does not terminate")
		}
	}
	if got := countMessages(t, engine); got != 730 {
		t.Fatalf("stored %d messages, want the whole history (730)", got)
	}
	if len(p.fetched) != 2*730 { // INBOX + Archive share the fake mailbox
		t.Fatalf("downloaded %d distinct messages, want %d", len(p.fetched), 2*730)
	}
	for key, n := range p.fetched {
		if n != 1 {
			t.Errorf("%s downloaded %d times", key, n)
		}
	}
	if got := lowestUID(t, engine); got != 1 {
		t.Fatalf("complete folder cursor = %d, want 1", got)
	}

	// a later watcher skips the complete folder without asking the server
	if more, err := engine.BackfillStep("a1", p, 100, map[string]bool{}); err != nil || more {
		t.Fatalf("complete folder: more=%v err=%v", more, err)
	}

	// an incremental sync keeps the cursor
	if err := engine.SyncAccountWith("a1", p); err != nil {
		t.Fatal(err)
	}
	if got := lowestUID(t, engine); got != 1 {
		t.Fatalf("incremental sync moved the cursor to %d", got)
	}
}

func TestBackfillResetsOnUIDValidityChange(t *testing.T) {
	engine, _ := setupEngine(t)
	p := newWindowProvider(300)
	if err := engine.SyncAccountWith("a1", p); err != nil {
		t.Fatal(err)
	}
	p.validity = 8 // server renumbered the mailbox

	// the backfill refuses to mix numberings and stops for the folder
	if more, err := engine.BackfillStep("a1", p, 100, map[string]bool{}); err != nil || more {
		t.Fatalf("stale folder: more=%v err=%v", more, err)
	}
	if got := countMessages(t, engine); got != 200 {
		t.Fatalf("stale backfill stored messages: %d", got)
	}
	// the next sync wipes and restarts: window again, cursor from the new window
	if err := engine.SyncAccountWith("a1", p); err != nil {
		t.Fatal(err)
	}
	if got := lowestUID(t, engine); got != 101 {
		t.Fatalf("cursor after reset = %d, want 101", got)
	}
}

func TestBackfillCursorForFoldersSyncedBeforeIt(t *testing.T) {
	engine, accs := setupEngine(t)
	p := newWindowProvider(300)
	if err := engine.SyncAccountWith("a1", p); err != nil {
		t.Fatal(err)
	}
	// simulate an install from before the backfill: no cursor stored
	if _, err := engine.db.Exec(`UPDATE folders SET lowest_uid=0`); err != nil {
		t.Fatal(err)
	}
	refs, err := accs.SyncRefs("a1")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range refs {
		if r.FolderID == "f-inbox" && r.LowestUID != 101 {
			t.Fatalf("derived cursor = %d, want 101 (oldest stored UID)", r.LowestUID)
		}
	}
}
