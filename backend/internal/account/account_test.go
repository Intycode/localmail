package account

// Store round-trip tests: create with SMTP config, list, set active, drafts
// folder ensure (idempotent).

import (
	"database/sql"
	"path/filepath"
	"testing"

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

func TestCreateListWithSMTP(t *testing.T) {
	s := New(testDB(t))
	acc := domain.Account{
		ID: "a1", Name: "Luca", Email: "luca@x.dev", Provider: "generic-imap", Active: true,
		Imap: &domain.ImapConfig{Host: "imap.x.dev", Port: 993, Security: "tls", Username: "luca@x.dev"},
		Smtp: &domain.SmtpConfig{Host: "smtp.x.dev", Port: 465, Security: "tls", Username: "luca@x.dev"},
	}
	if err := s.Create(acc); err != nil {
		t.Fatalf("create: %v", err)
	}
	accounts, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].ID != "a1" {
		t.Fatalf("list: %+v", accounts)
	}
	a := accounts[0]
	if a.Smtp == nil || a.Smtp.Host != "smtp.x.dev" || a.Smtp.Port != 465 {
		t.Fatalf("smtp config persa: %+v", a.Smtp)
	}
	if a.Imap == nil || a.Imap.Host != "imap.x.dev" {
		t.Fatalf("imap config persa: %+v", a.Imap)
	}
}

func TestSetActiveExclusive(t *testing.T) {
	s := New(testDB(t))
	for i, id := range []string{"a1", "a2"} {
		acc := domain.Account{
			ID: id, Name: id, Email: id + "@x.dev", Provider: "generic-imap", Active: i == 0,
			Imap: &domain.ImapConfig{Host: "h", Port: 993, Security: "tls", Username: id},
		}
		if err := s.Create(acc); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetActive("a2"); err != nil {
		t.Fatal(err)
	}
	accounts, _ := s.List()
	for _, a := range accounts {
		if a.Active && a.ID != "a2" {
			t.Fatalf("account %s attivo in parallelo", a.ID)
		}
	}
}

func TestEnsureDraftsFolderIdempotent(t *testing.T) {
	s := New(testDB(t))
	acc := domain.Account{
		ID: "a1", Name: "A", Email: "a@x.dev", Provider: "generic-imap",
		Imap: &domain.ImapConfig{Host: "h", Port: 993, Security: "tls", Username: "a"},
	}
	if err := s.Create(acc); err != nil {
		t.Fatal(err)
	}
	f1, mb1, err := s.EnsureDraftsFolder("a1")
	if err != nil {
		t.Fatal(err)
	}
	f2, mb2, err := s.EnsureDraftsFolder("a1")
	if err != nil {
		t.Fatal(err)
	}
	if f1 != f2 || mb1 != mb2 || mb1 != "" {
		t.Fatalf("bozze non stabili: %q/%q vs %q/%q", f1, mb1, f2, mb2)
	}
}
