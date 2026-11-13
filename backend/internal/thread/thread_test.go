package thread

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"mail/backend/internal/database"
	"mail/backend/internal/domain"
)

func open(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

func TestThreadLifecycle(t *testing.T) {
	st := open(t)

	// folders must exist (threads reference folders via FK)
	if _, err := st.db.Exec(`INSERT INTO accounts(id,name,email,provider,active,created_at)
		VALUES('a1','Luca','luca@example.com','mock',1,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("account: %v", err)
	}
	for _, f := range []string{"f-inbox", "f-archive"} {
		if _, err := st.db.Exec(`INSERT INTO folders(id,account,name,type,total,unread)
			VALUES(?, 'a1', ?, ?, 0, 0)`, f, f, "folder"); err != nil {
			t.Fatalf("folder %s: %v", f, err)
		}
	}

	t1 := domain.Thread{
		ID: "t1", FolderID: "f-inbox", AccountID: "a1", Subject: "Ciao",
		Date: time.Now().UTC(), MessageCount: 1, MessageIDs: []string{"m1"},
	}
	if err := st.CreateThread(t1); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := st.Get("t1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Subject != "Ciao" {
		t.Errorf("subject = %q", got.Subject)
	}

	// mark read
	if err := st.MarkRead("m1"); err != nil {
		t.Fatal(err)
	}

	// move to archive
	if err := st.MoveToFolder("t1", "f-archive"); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListByFolder("f-archive", Page{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "t1" {
		t.Errorf("move failed: %+v", list)
	}
}

func TestListByFolderPagesWithoutGapsOrDuplicates(t *testing.T) {
	st := open(t)
	if _, err := st.db.Exec(`INSERT INTO accounts(id,name,email,provider,active,created_at)
		VALUES('a1','Luca','luca@example.com','mock',1,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO folders(id,account,name,type,total,unread) VALUES('f','a1','f','inbox',0,0)`); err != nil {
		t.Fatal(err)
	}
	// 25 threads, several sharing the same date: the id breaks ties
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 25; i++ {
		th := domain.Thread{ID: fmt.Sprintf("t%02d", i), FolderID: "f", AccountID: "a1", Date: base.Add(time.Duration(i/3) * time.Hour)}
		if err := st.CreateThread(th); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	var prev domain.Thread
	for pages := 0; ; pages++ {
		page := Page{Limit: 7}
		if prev.ID != "" {
			page.BeforeID, page.BeforeDate = prev.ID, prev.Date
		}
		list, err := st.ListByFolder("f", page)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) == 0 {
			break
		}
		for _, th := range list {
			if seen[th.ID] {
				t.Fatalf("thread %s on two pages", th.ID)
			}
			if prev.ID != "" && th.Date.After(prev.Date) {
				t.Fatalf("thread %s out of order", th.ID)
			}
			seen[th.ID] = true
			prev = th
		}
		if pages > 10 {
			t.Fatal("pagination does not end")
		}
	}
	if len(seen) != 25 {
		t.Fatalf("paged %d threads, want 25", len(seen))
	}
}
