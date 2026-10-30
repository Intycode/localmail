package queue

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"mail/backend/internal/database"
)

func TestBackoffNeverOverflows(t *testing.T) {
	for _, n := range []int{0, 5, 29, 30, 64, 1000} {
		if d := Backoff(n); d <= 0 || d > maxDelay {
			t.Fatalf("Backoff(%d) = %v", n, d)
		}
	}
}

// Undo-send race: once the worker claimed an op, Cancel must refuse (the
// mail is going out); an op cancelled first must not be claimable.
func TestClaimAndCancelExcludeEachOther(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,email,provider,active,created_at)
		VALUES('a1','A','a@x.dev','mock',1,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	sent, _ := s.Enqueue("a1", "send", []byte(`{}`))
	undone, _ := s.Enqueue("a1", "send", []byte(`{}`))

	if ok, err := s.Claim(sent); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if err := s.Cancel(sent); !errors.Is(err, ErrRunning) {
		t.Fatalf("cancel of a running op = %v", err)
	}
	if err := s.Cancel(undone); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Claim(undone); ok {
		t.Fatal("cancelled op was claimed")
	}
	if due, _ := s.Due(time.Now(), 10); len(due) != 0 {
		t.Fatalf("claimed op still due: %d", len(due))
	}
	_ = s.ReleaseClaims(time.Now())
	if due, _ := s.Due(time.Now(), 10); len(due) != 1 {
		t.Fatalf("released claim not due again: %d", len(due))
	}
}
