package sync

import (
	"path/filepath"
	"testing"

	"mail/backend/internal/database"
	"mail/backend/internal/events"
	"mail/backend/internal/provider"
	"mail/backend/internal/thread"
)

func newEngine(t *testing.T) *Engine {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db, provider.NewMock(), events.New())
}

func TestEngineSyncPublishesEvents(t *testing.T) {
	engine := newEngine(t)
	ch, stop := engine.Bus.Subscribe()
	defer stop()

	// account a1 does not exist: SyncRefs returns no folders, sync completes.
	if err := engine.SyncAccount("a1"); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var kinds []string
	for e := range ch {
		kinds = append(kinds, e.Kind)
		if e.Kind == "sync.completed" {
			break
		}
	}
	if len(kinds) < 2 {
		t.Errorf("expected started+completed events, got %v", kinds)
	}
	if kinds[0] != "sync.started" {
		t.Errorf("first event = %q", kinds[0])
	}
}

func TestNormalizeSubjectGroupsReplies(t *testing.T) {
	if a := thread.NormalizeSubject("Re: Ciao"); a != "Ciao" {
		t.Errorf("normalize Re: got %q", a)
	}
	if a := thread.NormalizeSubject("[p1] Progetto"); a != "Progetto" {
		t.Errorf("normalize [tag] got %q", a)
	}
	if a := thread.NormalizeSubject("Report"); a != "Report" {
		t.Errorf("normalize plain got %q", a)
	}
	if a := thread.NormalizeSubject("Fw: News"); a != "News" {
		t.Errorf("normalize Fw: got %q", a)
	}
}

func TestDifference(t *testing.T) {
	got := difference([]uint32{1, 2, 3}, []uint32{2})
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("difference = %v", got)
	}
}
