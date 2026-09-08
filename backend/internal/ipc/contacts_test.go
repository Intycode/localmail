package ipc

// Tests for the compose autocomplete: contacts.suggest returns distinct
// recipients from the local mail history, most-seen first, matching on
// email or name, tolerating a trailing partial entry from the compose field.

import (
	"context"
	"path/filepath"
	"testing"

	"mail/backend/internal/database"
	"mail/backend/internal/domain"
	"mail/backend/internal/message"
	"mail/backend/internal/thread"
)

func seedContactsFixture(t *testing.T) (*App, func()) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "contacts.db"))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{DB: db, Accounts: nil, Threads: thread.New(db), Messages: message.New(db)}
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
	if err := app.Messages.UpsertBatch([]domain.Message{
		{
			ID: "m1", ThreadID: "t1", FolderID: "f1", AccountID: "a1",
			Subject: "uno", Date: dateUTC("2026-08-25T09:00:00Z"),
			To: []domain.Recipient{
				{Name: "Anna Verdi", Email: "anna@acme.dev"},
				{Name: "Bruno", Email: "bruno@acme.dev"},
			},
		},
		{
			ID: "m2", ThreadID: "t2", FolderID: "f1", AccountID: "a1",
			Subject: "due", Date: dateUTC("2026-08-25T10:00:00Z"),
			To: []domain.Recipient{{Name: "Anna Verdi", Email: "anna@acme.dev"}},
			CC: []domain.Recipient{{Email: "carla@example.org"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return app, func() { db.Close() }
}

func TestContactsSuggest(t *testing.T) {
	app, cleanup := seedContactsFixture(t)
	defer cleanup()

	// by email fragment: anna twice (most-seen first)
	res, err := opContactsSuggest(context.Background(), app, []byte(`{"query":"an"}`))
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	contacts := res.([]domain.Contact)
	if len(contacts) == 0 || contacts[0].Email != "anna@acme.dev" || contacts[0].Name != "Anna Verdi" {
		t.Fatalf("unexpected result: %+v", contacts)
	}

	// by name fragment
	res, err = opContactsSuggest(context.Background(), app, []byte(`{"query":"verdi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if contacts := res.([]domain.Contact); len(contacts) != 1 || contacts[0].Email != "anna@acme.dev" {
		t.Fatalf("unexpected name match: %+v", contacts)
	}

	// trailing partial entry from a comma-separated field
	res, err = opContactsSuggest(context.Background(), app, []byte(`{"query":"anna@acme.dev, bru"}`))
	if err != nil {
		t.Fatal(err)
	}
	if contacts := res.([]domain.Contact); len(contacts) != 1 || contacts[0].Email != "bruno@acme.dev" {
		t.Fatalf("unexpected partial match: %+v", contacts)
	}

	// empty query → empty list, no error
	res, err = opContactsSuggest(context.Background(), app, []byte(`{"query":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if contacts := res.([]domain.Contact); len(contacts) != 0 {
		t.Fatalf("expected no suggestions, got %+v", contacts)
	}

	// dedup by lowercase email: one entry per address
	res, err = opContactsSuggest(context.Background(), app, []byte(`{"query":"acme.dev"}`))
	if err != nil {
		t.Fatal(err)
	}
	if contacts := res.([]domain.Contact); len(contacts) != 2 {
		t.Fatalf("expected 2 distinct acme.dev contacts, got %+v", contacts)
	}
}
