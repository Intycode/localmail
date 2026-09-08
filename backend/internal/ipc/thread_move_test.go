package ipc

// Unit tests for thread.move (generic folder move behind archive and the
// spam actions) and for the \Junk folder-type mapping. Accounts are seeded
// with the mock provider (Imap == nil) so withProvider is a no-op and only
// the local effects are asserted.

import (
	"context"
	"path/filepath"
	"testing"

	"mail/backend/internal/database"
	"mail/backend/internal/domain"
	"mail/backend/internal/message"
	"mail/backend/internal/thread"
)

func seedMoveFixture(t *testing.T) (*App, func()) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "move.db"))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{DB: db, Threads: thread.New(db), Messages: message.New(db)}
	mustExec := func(q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO accounts(id,name,email,provider,active,created_at)
		      VALUES('a1','Luca','luca@x.dev','mock',0,'2026-01-01T00:00:00.000000000Z')`)
	mustExec(`INSERT INTO folders(id,account,name,type,total,unread) VALUES('f-inbox','a1','INBOX','inbox',0,0)`)
	mustExec(`INSERT INTO folders(id,account,name,type,total,unread) VALUES('f-junk','a1','Junk','junk',0,0)`)
	mustExec(`INSERT INTO folders(id,account,name,type,total,unread) VALUES('f-other','a1','Altro','custom',0,0)`)
	if err := app.Messages.UpsertBatch([]domain.Message{
		{ID: "m1", ThreadID: "t1", FolderID: "f-inbox", AccountID: "a1", Subject: "spam?", IsUnread: true, Date: dateUTC("2026-08-25T09:00:00Z")},
	}); err != nil {
		t.Fatal(err)
	}
	return app, func() { db.Close() }
}

func TestThreadMove(t *testing.T) {
	app, cleanup := seedMoveFixture(t)
	defer cleanup()

	// inbox → junk (the "segnala spam" path)
	if _, err := opThreadMove(context.Background(), app, []byte(`{"threadId":"t1","target":"f-junk"}`)); err != nil {
		t.Fatalf("move to junk: %v", err)
	}
	var folder string
	if err := app.DB.QueryRow(`SELECT folder FROM threads WHERE id='t1'`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if folder != "f-junk" {
		t.Fatalf("thread folder = %s, want f-junk", folder)
	}
	var msgFolder string
	if err := app.DB.QueryRow(`SELECT folder FROM messages WHERE id='m1'`).Scan(&msgFolder); err != nil {
		t.Fatal(err)
	}
	if msgFolder != "f-junk" {
		t.Fatalf("message folder = %s, want f-junk", msgFolder)
	}
	assertFolderBadge(t, app, "f-inbox", 0, 0)
	assertFolderBadge(t, app, "f-junk", 1, 1)

	// junk → inbox ("non è spam")
	if _, err := opThreadMove(context.Background(), app, []byte(`{"threadId":"t1","target":"f-inbox"}`)); err != nil {
		t.Fatalf("move back to inbox: %v", err)
	}
	assertFolderBadge(t, app, "f-inbox", 1, 1)
	assertFolderBadge(t, app, "f-junk", 0, 0)

	// unknown target folder errors
	if _, err := opThreadMove(context.Background(), app, []byte(`{"threadId":"t1","target":"f-nope"}`)); err == nil {
		t.Fatal("expected error for unknown target")
	}
	// missing params error
	if _, err := opThreadMove(context.Background(), app, []byte(`{"threadId":"t1"}`)); err == nil {
		t.Fatal("expected error without target")
	}
}

func TestThreadArchiveDelegatesToSharedMove(t *testing.T) {
	app, cleanup := seedMoveFixture(t)
	defer cleanup()
	mustExec := func(q string) {
		t.Helper()
		if _, err := app.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO folders(id,account,name,type,total,unread) VALUES('f-arch','a1','Archive','archive',0,0)`)

	if _, err := opThreadArchive(context.Background(), app, []byte(`{"threadId":"t1"}`)); err != nil {
		t.Fatalf("archive: %v", err)
	}
	var folder string
	if err := app.DB.QueryRow(`SELECT folder FROM threads WHERE id='t1'`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if folder != "f-arch" {
		t.Fatalf("thread folder = %s, want f-arch", folder)
	}
}

func TestFolderTypeJunkMapping(t *testing.T) {
	cases := []struct{ name, specialUse, want string }{
		{"Junk", "\\Junk", "junk"},
		{"Junk", "", "junk"},
		{"Spam", "", "junk"},
		{" Bulk Mail ", "", "junk"},
		{"Sent", "\\Sent", "sent"},
		{"INBOX", "", "inbox"},
		{"MailingList", "", "custom"},
	}
	for _, c := range cases {
		if got := folderType(c.name, c.specialUse); got != c.want {
			t.Errorf("folderType(%q, %q) = %q, want %q", c.name, c.specialUse, got, c.want)
		}
	}
}
