package ipc

// Unit tests for the threads.list account-level view (the unified inbox):
// {accountId:"all"} joins every account's inbox, {accountId:<id>} limits to
// one account, {folderId} keeps the per-folder behavior. Threads are seeded
// through the production path (message.Store.UpsertBatch) so the aggregate
// recompute — including the thread sender — is exercised end to end.

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

func newApp(t *testing.T) *App {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "unified.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &App{
		DB:       db,
		Threads:  thread.New(db),
		Messages: message.New(db),
		Accounts: account.New(db),
	}
}

// seedAccount inserts an account and returns its inbox + archive folder ids.
func seedAccount(t *testing.T, app *App, id, name, email string) (inbox, archive string) {
	t.Helper()
	if _, err := app.DB.Exec(
		`INSERT INTO accounts(id,name,email,provider,active,created_at)
		 VALUES(?,?,?,'generic-imap',0,'2026-01-01T00:00:00.000000000Z')`, id, name, email,
	); err != nil {
		t.Fatalf("seed account %s: %v", id, err)
	}
	inbox, archive = "f:"+id+":inbox", "f:"+id+":archive"
	for _, f := range []struct{ id, typ string }{{inbox, "inbox"}, {archive, "archive"}} {
		if _, err := app.DB.Exec(
			`INSERT INTO folders(id,account,name,type,total,unread) VALUES(?,?,?,?,0,0)`,
			f.id, id, f.id, f.typ,
		); err != nil {
			t.Fatalf("seed folder: %v", err)
		}
	}
	return inbox, archive
}

// seedThread persists one thread (one message) through UpsertBatch, the same
// path the sync pipeline uses: thread upsert, message insert, aggregate
// recompute that fills last_date/snippet and the thread sender.
func seedThread(t *testing.T, app *App, id, folder, acc, subject, fromName, fromEmail, date string) {
	t.Helper()
	d, err := time.Parse(time.RFC3339, date)
	if err != nil {
		t.Fatalf("bad date %q: %v", date, err)
	}
	err = app.Messages.UpsertBatch([]domain.Message{{
		ID: id + "-m1", ThreadID: id, FolderID: folder, AccountID: acc,
		Subject: subject, From: domain.Recipient{Name: fromName, Email: fromEmail},
		Body: subject, BodyText: subject, Date: d,
	}})
	if err != nil {
		t.Fatalf("seed thread %s: %v", id, err)
	}
}

func TestThreadsListUnifiedAllAccounts(t *testing.T) {
	app := newApp(t)
	inbox1, archive1 := seedAccount(t, app, "a1", "Luca", "luca@x.dev")
	_, _ = seedAccount(t, app, "a2", "Bo", "bo@y.dev")

	seedThread(t, app, "t1", inbox1, "a1", "Vecchio", "Anna", "anna@acme.dev", "2026-08-25T09:00:00Z")
	seedThread(t, app, "t2", inbox1, "a1", "Recente", "Bo", "bo@x.dev", "2026-08-26T10:00:00Z")
	seedThread(t, app, "t3", archive1, "a1", "Archiviato", "Zoe", "zoe@x.dev", "2026-08-27T10:00:00Z")
	seedThread(t, app, "t4", "f:a2:inbox", "a2", "Altra casella", "Max", "max@y.dev", "2026-08-28T10:00:00Z")

	data, err := opThreadsList(context.Background(), app, []byte(`{"accountId":"all"}`))
	if err != nil {
		t.Fatal(err)
	}
	threads := data.([]domain.Thread)
	if len(threads) != 3 {
		t.Fatalf("want 3 inbox threads (archive excluded), got %d: %+v", len(threads), threads)
	}
	// newest first, across accounts
	if threads[0].ID != "t4" || threads[1].ID != "t2" || threads[2].ID != "t1" {
		t.Errorf("unexpected order: %s, %s, %s", threads[0].ID, threads[1].ID, threads[2].ID)
	}
	// each thread carries its own account + folder + latest-message sender
	if threads[0].AccountID != "a2" || threads[0].From.Email != "max@y.dev" {
		t.Errorf("t4 payload wrong: %+v", threads[0])
	}
	if threads[1].From.Name != "Bo" {
		t.Errorf("t2 sender wrong: %+v", threads[1])
	}
}

func TestThreadsListSingleAccount(t *testing.T) {
	app := newApp(t)
	inbox1, _ := seedAccount(t, app, "a1", "Luca", "luca@x.dev")
	seedAccount(t, app, "a2", "Bo", "bo@y.dev")
	seedThread(t, app, "t1", inbox1, "a1", "Uno", "A", "a@x.dev", "2026-08-25T09:00:00Z")
	seedThread(t, app, "t2", "f:a2:inbox", "a2", "Due", "B", "b@y.dev", "2026-08-26T09:00:00Z")

	data, err := opThreadsList(context.Background(), app, []byte(`{"accountId":"a1"}`))
	if err != nil {
		t.Fatal(err)
	}
	threads := data.([]domain.Thread)
	if len(threads) != 1 || threads[0].ID != "t1" {
		t.Fatalf("want only a1 threads, got %+v", threads)
	}
}

func TestThreadsListByFolderStillWorks(t *testing.T) {
	app := newApp(t)
	inbox1, _ := seedAccount(t, app, "a1", "Luca", "luca@x.dev")
	seedAccount(t, app, "a2", "Bo", "bo@y.dev")
	seedThread(t, app, "t1", inbox1, "a1", "Uno", "A", "a@x.dev", "2026-08-25T09:00:00Z")
	seedThread(t, app, "t2", "f:a2:inbox", "a2", "Due", "B", "b@y.dev", "2026-08-26T09:00:00Z")

	data, err := opThreadsList(context.Background(), app, []byte(`{"folderId":"f:a2:inbox"}`))
	if err != nil {
		t.Fatal(err)
	}
	threads := data.([]domain.Thread)
	if len(threads) != 1 || threads[0].ID != "t2" {
		t.Fatalf("want only t2, got %+v", threads)
	}
}

func TestThreadsListRequiresParams(t *testing.T) {
	app := newApp(t)
	if _, err := opThreadsList(context.Background(), app, []byte(`{}`)); err == nil {
		t.Fatal("expected error without folderId/accountId")
	}
}
