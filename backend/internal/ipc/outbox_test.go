package ipc

// Outbox tests: store round-trip, backoff on failure, and the replay path —
// a queued thread.markRead executed through the regular handler (mock
// provider → withProvider no-ops, the local flag path still runs).

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mail/backend/internal/account"
	"mail/backend/internal/database"
	"mail/backend/internal/message"
	"mail/backend/internal/queue"
	"mail/backend/internal/thread"
)

func newQueueApp(t *testing.T) *App {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "outbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &App{
		DB:       db,
		Threads:  thread.New(db),
		Messages: message.New(db),
		Accounts: account.New(db),
		Queue:    queue.New(db),
	}
}

func TestQueueStoreAndBackoff(t *testing.T) {
	app := newQueueApp(t)
	seedAccount(t, app, "acc1", "Test", "t@example.com")

	id, err := app.Queue.Enqueue("acc1", "send", []byte(`{"message":{"subject":"x"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := app.Queue.Count(); n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
	items, err := app.Queue.List(10)
	if err != nil || len(items) != 1 || items[0].Kind != "send" || items[0].Attempts != 0 {
		t.Fatalf("list = %+v err=%v", items, err)
	}

	// exponential backoff: 30s doubling, capped at 15m
	if got := queue.Backoff(0); got != 30*time.Second {
		t.Fatalf("backoff(0) = %v, want 30s", got)
	}
	if got := queue.Backoff(2); got != 2*time.Minute {
		t.Fatalf("backoff(2) = %v, want 2m", got)
	}
	if got := queue.Backoff(9); got != 15*time.Minute {
		t.Fatalf("backoff(9) = %v, want 15m cap", got)
	}

	if err := app.Queue.Done(id); err != nil {
		t.Fatal(err)
	}
	if n, _ := app.Queue.Count(); n != 0 {
		t.Fatalf("count after done = %d, want 0", n)
	}
}

func TestOutboxFailedAttemptParksWithBackoff(t *testing.T) {
	app := newQueueApp(t)
	seedAccount(t, app, "acc1", "Test", "t@example.com")

	// send without an active account fails with a NON-network error:
	// the op must stay queued with attempts++ and no offline signal
	if _, err := app.Queue.Enqueue("acc1", "send", []byte(`{"message":{"subject":"x"}}`)); err != nil {
		t.Fatal(err)
	}
	processed, offlineSeen := drainPass(app)
	if processed != 1 || offlineSeen {
		t.Fatalf("processed=%d offline=%v, want 1/false", processed, offlineSeen)
	}
	items, _ := app.Queue.List(10)
	if len(items) != 1 || items[0].Attempts != 1 || items[0].LastError == "" {
		t.Fatalf("after fail: %+v", items)
	}
	// backoff pushes next_attempt_at into the future: not due anymore
	due, _ := app.Queue.Due(time.Now(), 10)
	if len(due) != 0 {
		t.Fatalf("op should not be due yet: %+v", due)
	}
}

func TestOutboxReplayMarksThreadRead(t *testing.T) {
	app := newQueueApp(t)
	app.Events = nil // drainPass must tolerate a nil bus
	inbox, _ := seedAccount(t, app, "acc1", "Test", "t@example.com")
	// mock provider: withProvider no-ops, the local flag path still runs
	if _, err := app.DB.Exec(`UPDATE accounts SET provider='mock' WHERE id='acc1'`); err != nil {
		t.Fatal(err)
	}
	seedThread(t, app, "th1", inbox, "acc1", "Oggetto", "Mario", "m@example.com", "2026-02-01T10:00:00Z")

	if _, err := app.Queue.Enqueue("acc1", "thread.markRead", []byte(`{"threadId":"th1"}`)); err != nil {
		t.Fatal(err)
	}

	s := &Server{app: app}
	s.drainOutbox()

	var isUnread int
	if err := app.DB.QueryRow(`SELECT is_unread FROM threads WHERE id='th1'`).Scan(&isUnread); err != nil {
		t.Fatal(err)
	}
	if isUnread != 0 {
		t.Fatal("queued thread.markRead did not apply")
	}
	if n, _ := app.Queue.Count(); n != 0 {
		t.Fatalf("pending = %d, want 0", n)
	}
}

func TestIsNetworkError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("dial tcp 1.2.3.4:993: connect: connection refused"), true},
		{fmt.Errorf("imap: lookup imap.example.it: no such host"), true},
		{context.DeadlineExceeded, true},
		{fmt.Errorf("credenziale non disponibile: riapri l'app"), false},
		{fmt.Errorf("messaggio vuoto"), false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := isNetworkError(tc.err); got != tc.want {
			t.Errorf("isNetworkError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestStripSecretNeverPersistsCredentials(t *testing.T) {
	out := stripSecret(json.RawMessage(`{"accountId":"a1","secret":"hunter2","message":{"to":["x@y"]}}`))
	if strings.Contains(string(out), "hunter2") || !strings.Contains(string(out), `"accountId":"a1"`) {
		t.Fatalf("stripSecret = %s", out)
	}
	if got := string(stripSecret(nil)); got != "{}" {
		t.Fatalf("empty params = %s", got)
	}
}
