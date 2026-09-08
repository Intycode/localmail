package ipc

// Unit tests for the thread-level flag ops: thread.markRead marks every
// message of a thread read (and the aggregate), thread.markUnread restores
// the unread state. Accounts are seeded with the mock provider so the
// server-side round-trip is a no-op and only the local effects are asserted.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"mail/backend/internal/database"
	"mail/backend/internal/domain"
	"mail/backend/internal/message"
	"mail/backend/internal/thread"
)

func dateUTC(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func TestThreadMarkReadAndUnread(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "flags.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app := &App{DB: db, Threads: thread.New(db), Messages: message.New(db)}

	if _, err := db.Exec(
		`INSERT INTO accounts(id,name,email,provider,active,created_at)
		 VALUES('a1','Luca','luca@x.dev','mock',0,'2026-01-01T00:00:00.000000000Z')`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO folders(id,account,name,type,total,unread) VALUES('f1','a1','INBOX','inbox',0,0)`,
	); err != nil {
		t.Fatal(err)
	}
	err = app.Messages.UpsertBatch([]domain.Message{
		{ID: "m1", ThreadID: "t1", FolderID: "f1", AccountID: "a1", Subject: "uno", IsUnread: true, Date: dateUTC("2026-08-25T09:00:00Z")},
		{ID: "m2", ThreadID: "t1", FolderID: "f1", AccountID: "a1", Subject: "due", IsUnread: true, Date: dateUTC("2026-08-25T10:00:00Z")},
	})
	if err != nil {
		t.Fatal(err)
	}

	// mark the whole thread read
	if _, err := opThreadMarkRead(context.Background(), app, []byte(`{"threadId":"t1"}`)); err != nil {
		t.Fatalf("markRead: %v", err)
	}
	assertFlagState(t, app, "t1", map[string]bool{"m1": false, "m2": false}, false)
	assertFolderBadge(t, app, "f1", 1, 0) // total=1, unread=0 after read

	// mark it unread again
	if _, err := opThreadMarkUnread(context.Background(), app, []byte(`{"threadId":"t1"}`)); err != nil {
		t.Fatalf("markUnread: %v", err)
	}
	assertFlagState(t, app, "t1", map[string]bool{"m1": true, "m2": true}, true)
	assertFolderBadge(t, app, "f1", 1, 1) // badge back to 1 unread

	// missing param errors
	if _, err := opThreadMarkRead(context.Background(), app, []byte(`{}`)); err == nil {
		t.Fatal("expected error without threadId")
	}
	if _, err := opThreadMarkUnread(context.Background(), app, []byte(`{}`)); err == nil {
		t.Fatal("expected error without threadId")
	}
}

func assertFolderBadge(t *testing.T, app *App, folderID string, wantTotal, wantUnread int) {
	t.Helper()
	var total, unread int
	if err := app.DB.QueryRow(
		`SELECT total, unread FROM folders WHERE id=?`, folderID,
	).Scan(&total, &unread); err != nil {
		t.Fatalf("folder %s: %v", folderID, err)
	}
	if total != wantTotal || unread != wantUnread {
		t.Errorf("folder badge total=%d unread=%d, want total=%d unread=%d", total, unread, wantTotal, wantUnread)
	}
}

func assertFlagState(t *testing.T, app *App, threadID string, wantMessages map[string]bool, wantThreadUnread bool) {
	t.Helper()
	for id, want := range wantMessages {
		var unread int
		if err := app.DB.QueryRow(`SELECT is_unread FROM messages WHERE id=?`, id).Scan(&unread); err != nil {
			t.Fatalf("message %s: %v", id, err)
		}
		if (unread != 0) != want {
			t.Errorf("message %s is_unread = %d, want %v", id, unread, want)
		}
	}
	var unread int
	if err := app.DB.QueryRow(`SELECT is_unread FROM threads WHERE id=?`, threadID).Scan(&unread); err != nil {
		t.Fatalf("thread %s: %v", threadID, err)
	}
	if (unread != 0) != wantThreadUnread {
		t.Errorf("thread is_unread = %d, want %v", unread, wantThreadUnread)
	}
}
