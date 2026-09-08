package message

// Store round-trip tests: upsert -> get (with recipients + attachments),
// upsert of the same id (replace), DeleteFull, LocalUIDs. A folder row must
// exist first: threads.folder references folders(id).

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mail/backend/internal/account"
	"mail/backend/internal/database"
	"mail/backend/internal/domain"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedStore creates the account + folders f1/f2 that message rows reference.
func seedStore(t *testing.T) *Store {
	t.Helper()
	db := testDB(t)
	accs := account.New(db)
	acc := domain.Account{
		ID: "a1", Name: "Luca", Email: "luca@x.dev", Provider: "generic-imap", Active: true,
		Imap: &domain.ImapConfig{Host: "h", Port: 993, Security: "tls", Username: "luca@x.dev"},
		Folders: []domain.Folder{
			{ID: "f1", Account: "a1", Name: "INBOX", Type: "inbox"},
			{ID: "f2", Account: "a1", Name: "Bozze", Type: "drafts"},
		},
	}
	if err := accs.Create(acc); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return New(db)
}

func sampleMsg(id, folderID string, atts []domain.Attachment) domain.Message {
	return domain.Message{
		ID:        id,
		ThreadID:  "t1",
		FolderID:  folderID,
		AccountID: "a1",
		Subject:   "Oggetto",
		From:      domain.Recipient{Name: "Anna", Email: "anna@acme.dev"},
		To:        []domain.Recipient{{Email: "luca@x.dev"}},
		CC:        []domain.Recipient{{Email: "bo@acme.dev"}},
		Body:      "<p>ciao</p>",
		BodyText:  "ciao",
		Date:      time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC),
		IsUnread:  true,
		Attachments: atts,
	}
}

func TestUpsertGetRoundTrip(t *testing.T) {
	s := seedStore(t)
	in := sampleMsg("m1", "f1", []domain.Attachment{
		{Name: "report.pdf", Size: 1234, ContentType: "application/pdf"},
	})
	in.RemoteImages = 3
	in.Phishing = []string{"Link accorciato (bit.ly): destinazione nascosta"}
	if err := s.UpsertBatch([]domain.Message{in}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	out, err := s.Get("m1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if out.Subject != "Oggetto" || out.From.Email != "anna@acme.dev" {
		t.Fatalf("header mismatch: %+v", out)
	}
	if len(out.To) != 1 || len(out.CC) != 1 {
		t.Fatalf("recipients mismatch: %+v", out)
	}
	if len(out.Attachments) != 1 || out.Attachments[0].Name != "report.pdf" || out.Attachments[0].Size != 1234 {
		t.Fatalf("attachments mismatch: %+v", out.Attachments)
	}
	if !out.IsUnread {
		t.Fatal("is_unread non salvato")
	}
	if out.RemoteImages != 3 {
		t.Fatalf("remote_images non salvato: %d", out.RemoteImages)
	}
	if len(out.Phishing) != 1 || !strings.Contains(out.Phishing[0], "bit.ly") {
		t.Fatalf("phishing non salvato: %v", out.Phishing)
	}
}

func TestUpsertReplacesSameID(t *testing.T) {
	s := seedStore(t)
	if err := s.UpsertBatch([]domain.Message{sampleMsg("m1", "f1", nil)}); err != nil {
		t.Fatal(err)
	}
	updated := sampleMsg("m1", "f1", nil)
	updated.Subject = "Rivisto"
	updated.IsUnread = false
	if err := s.UpsertBatch([]domain.Message{updated}); err != nil {
		t.Fatal(err)
	}
	out, err := s.Get("m1")
	if err != nil {
		t.Fatal(err)
	}
	if out.Subject != "Rivisto" || out.IsUnread {
		t.Fatalf("replace non riuscito: %+v", out)
	}
}

func TestDeleteFullAndUIDs(t *testing.T) {
	s := seedStore(t)
	m := sampleMsg("m1", "f1", nil)
	m.ProviderMessageID = "7"
	if err := s.UpsertBatch([]domain.Message{m}); err != nil {
		t.Fatal(err)
	}
	uids, err := s.LocalUIDs("f1")
	if err != nil {
		t.Fatal(err)
	}
	if len(uids) != 1 || uids[0] != 7 {
		t.Fatalf("uid attesi [7], ottenuti %v", uids)
	}
	if err := s.DeleteFull("m1"); err != nil {
		t.Fatalf("delete full: %v", err)
	}
	if _, err := s.Get("m1"); err == nil {
		t.Fatal("messaggio ancora presente dopo DeleteFull")
	}
	uids, _ = s.LocalUIDs("f1")
	if len(uids) != 0 {
		t.Fatalf("uid residui dopo delete: %v", uids)
	}
}
