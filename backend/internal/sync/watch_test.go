package sync

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"mail/backend/internal/events"
	"mail/backend/internal/provider"
)

// fakeProv is a provider whose only live method is Idle: the watcher loop
// touches nothing else once syncOnce is injected.
type fakeProv struct {
	provider.MailProvider
	name   string
	onIdle func(notify chan<- struct{})
	closed atomic.Int32
	idles  atomic.Int32
}

func (f *fakeProv) Idle(_ string, notify chan<- struct{}) (func(), error) {
	f.idles.Add(1)
	if f.onIdle != nil {
		f.onIdle(notify)
	}
	return func() {}, nil
}

func (f *fakeProv) Close() { f.closed.Add(1) }

func TestWatchOneReconnectsAfterDrop(t *testing.T) {
	oldBase := reconnectBase
	reconnectBase = time.Millisecond
	t.Cleanup(func() { reconnectBase = oldBase })

	p1 := &fakeProv{name: "p1"}
	p1.onIdle = func(n chan<- struct{}) { n <- struct{}{} } // connection drops right away
	p2 := &fakeProv{name: "p2"}

	synced := make(chan string, 8)
	syncOnce := func(p provider.MailProvider) error {
		name := p.(*fakeProv).name
		synced <- name
		if name == "p1" {
			return errors.New("connection closed")
		}
		return nil
	}
	redials := 0
	newProvider := func() (provider.MailProvider, error) {
		redials++
		return p2, nil
	}

	w := &Watcher{stopCh: make(chan struct{}), done: make(chan struct{})}
	e := &Engine{Bus: events.New()}
	go func() {
		defer close(w.done)
		w.watchOne(e, "a1", p1, "INBOX", true, syncOnce, newProvider)
	}()

	// p1 fails its sync, then the watcher redials and runs a catch-up sync on p2
	for _, want := range []string{"p1", "p2"} {
		select {
		case got := <-synced:
			if got != want {
				t.Fatalf("sync on %s, want %s", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no sync on %s", want)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for p2.idles.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if p2.idles.Load() == 0 {
		t.Fatal("watcher never re-entered IDLE on the new provider")
	}

	w.Stop() // closes stopCh, waits for watchOne to return
	if p1.closed.Load() == 0 {
		t.Error("broken provider was not closed")
	}
	if p2.closed.Load() == 0 {
		t.Error("current provider was not closed on stop")
	}
	if redials != 1 {
		t.Errorf("redials = %d, want 1", redials)
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	if backoff(0) != reconnectBase || backoff(1) != 2*reconnectBase {
		t.Fatalf("backoff does not double: %v %v", backoff(0), backoff(1))
	}
	if backoff(50) != reconnectMax {
		t.Fatalf("backoff(50) = %v, want cap %v", backoff(50), reconnectMax)
	}
}
