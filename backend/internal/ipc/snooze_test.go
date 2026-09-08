package ipc

// End-to-end test of the snooze flow: thread.snooze moves the thread into
// the account's local snoozed folder and schedules the return; the restore
// loop (restoreDueSnoozes) brings it back once the moment has passed, and
// thread.unsnooze restores immediately on demand.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"mail/backend/internal/account"
	"mail/backend/internal/database"
	"mail/backend/internal/domain"
	"mail/backend/internal/message"
	"mail/backend/internal/thread"
)

func seedSnoozeFixture(t *testing.T) (*App, func()) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "snooze.db"))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{DB: db, Accounts: account.New(db), Threads: thread.New(db), Messages: message.New(db)}
	mustExec := func(q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO accounts(id,name,email,provider,active,created_at)
		      VALUES('a1','Luca','luca@x.dev','mock',1,'2026-01-01T00:00:00.000000000Z')`)
	mustExec(`INSERT INTO folders(id,account,name,type,total,unread) VALUES('f-inbox','a1','INBOX','inbox',0,0)`)
	if err := app.Messages.UpsertBatch([]domain.Message{
		{ID: "m1", ThreadID: "t1", FolderID: "f-inbox", AccountID: "a1", Subject: "più tardi", IsUnread: true, Date: dateUTC("2026-08-25T09:00:00Z")},
	}); err != nil {
		t.Fatal(err)
	}
	return app, func() { db.Close() }
}

func threadFolder(t *testing.T, app *App, id string) string {
	t.Helper()
	var f string
	if err := app.DB.QueryRow(`SELECT folder FROM threads WHERE id=?`, id).Scan(&f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestThreadSnoozeAndRestore(t *testing.T) {
	app, cleanup := seedSnoozeFixture(t)
	defer cleanup()

	// snooze for one hour: the thread lands in the auto-created snoozed folder
	until := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	if _, err := opThreadSnooze(context.Background(), app, []byte(`{"threadId":"t1","until":"`+until+`"}`)); err != nil {
		t.Fatalf("snooze: %v", err)
	}
	sf := threadFolder(t, app, "t1")
	var typ string
	if err := app.DB.QueryRow(`SELECT type FROM folders WHERE id=?`, sf).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	if typ != "snoozed" {
		t.Fatalf("thread moved to %s (%s), want the snoozed folder", sf, typ)
	}
	var untilDB, folderDB string
	if err := app.DB.QueryRow(`SELECT snooze_until, snooze_folder FROM threads WHERE id='t1'`).Scan(&untilDB, &folderDB); err != nil {
		t.Fatal(err)
	}
	if untilDB == "" || folderDB != "f-inbox" {
		t.Fatalf("schedule not stored: until=%q folder=%q", untilDB, folderDB)
	}

	// not due yet: the restore loop must leave it alone
	s := &Server{app: app}
	s.restoreDueSnoozes()
	if got := threadFolder(t, app, "t1"); got != sf {
		t.Fatalf("restore fired too early: thread in %s", got)
	}

	// manual unsnooze: back to the inbox, schedule cleared
	if _, err := opThreadUnsnooze(context.Background(), app, []byte(`{"threadId":"t1"}`)); err != nil {
		t.Fatalf("unsnooze: %v", err)
	}
	if got := threadFolder(t, app, "t1"); got != "f-inbox" {
		t.Fatalf("unsnooze left the thread in %s", got)
	}
	if err := app.DB.QueryRow(`SELECT snooze_until FROM threads WHERE id='t1'`).Scan(&untilDB); err != nil {
		t.Fatal(err)
	}
	if untilDB != "" {
		t.Fatalf("unsnooze did not clear the schedule: %q", untilDB)
	}

	// unsnooze without a schedule errors
	if _, err := opThreadUnsnooze(context.Background(), app, []byte(`{"threadId":"t1"}`)); err == nil {
		t.Fatal("expected error when the thread is not snoozed")
	}
}

func TestSnoozeRestoreLoopBringsThreadBack(t *testing.T) {
	app, cleanup := seedSnoozeFixture(t)
	defer cleanup()

	// snooze in the past (a due moment): the loop restores it on the next pass
	until := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := opThreadSnooze(context.Background(), app, []byte(`{"threadId":"t1","until":"`+until+`"}`)); err != nil {
		t.Fatalf("snooze: %v", err)
	}
	sf := threadFolder(t, app, "t1")
	s := &Server{app: app}
	s.restoreDueSnoozes()
	if got := threadFolder(t, app, "t1"); got != "f-inbox" {
		t.Fatalf("loop did not restore: thread still in %s", got)
	}
	var untilDB string
	if err := app.DB.QueryRow(`SELECT snooze_until FROM threads WHERE id='t1'`).Scan(&untilDB); err != nil {
		t.Fatal(err)
	}
	if untilDB != "" {
		t.Fatalf("schedule not cleared after restore: %q", untilDB)
	}
	_ = sf
}

func TestThreadSnoozeValidatesUntil(t *testing.T) {
	app, cleanup := seedSnoozeFixture(t)
	defer cleanup()
	if _, err := opThreadSnooze(context.Background(), app, []byte(`{"threadId":"t1","until":"domani"}`)); err == nil {
		t.Fatal("expected error for invalid until")
	}
	if _, err := opThreadSnooze(context.Background(), app, []byte(`{"threadId":"t1"}`)); err == nil {
		t.Fatal("expected error without until")
	}
}
