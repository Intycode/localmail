package database

import (
	"os"
	"path/filepath"
	"testing"

	"mail/backend/internal/account"
	"mail/backend/internal/domain"
)

func TestOpenAppliesMigrations(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// schema present
	for _, table := range []string{
		"accounts", "folders", "threads", "messages", "recipients",
		"attachments", "labels", "message_labels", "sync_state", "mail_fts",
	} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if n == 0 {
			t.Errorf("table %s missing", table)
		}
	}

	// FTS5 works
	if _, err := db.Exec(`INSERT INTO mail_fts(subject, body, thread_id, message_id)
		VALUES('ciao', 'questo e il corpo', 't1', 'm1')`); err != nil {
		t.Fatalf("fts insert: %v", err)
	}
	var snip string
	if err := db.QueryRow(`SELECT subject FROM mail_fts WHERE mail_fts MATCH 'ciao'`).Scan(&snip); err != nil {
		t.Fatalf("fts match: %v", err)
	}
	if snip != "ciao" {
		t.Errorf("unexpected fts subject %q", snip)
	}
}

func TestAccountRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	st := account.New(db)
	a := domain.Account{
		ID: "a1", Name: "Luca", Email: "luca@example.com", Provider: "mock", Active: true,
		Folders: []domain.Folder{
			{ID: "f1", Account: "a1", Name: "Inbox", Type: "inbox"},
		},
	}
	if err := st.Create(a); err != nil {
		t.Fatalf("create: %v", err)
	}
	list, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Email != "luca@example.com" {
		t.Errorf("unexpected list %+v", list)
	}
	if len(list[0].Folders) != 1 || list[0].Folders[0].Type != "inbox" {
		t.Errorf("folders not stored")
	}
}

// --- encryption at rest (M3) ---

const testKey = "5f4dcc3b5aa765d61d8327deb882cf99a1b2c3d4e5f60718293a4b5c6d7e8f90"

func TestOpenEncryptedRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enc.db")

	db, err := Open(path, testKey)
	if err != nil {
		t.Fatalf("open keyed: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO accounts(id,name,email,provider,active,created_at)
		 VALUES('a1','Test','t@example.com','mock',1,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// the file must not be a plaintext SQLite database
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw[:16]) == "SQLite format 3\x00" {
		t.Fatal("database file is plaintext despite the key")
	}

	// reopening with the same key sees the data (and the applied migrations)
	db2, err := Open(path, testKey)
	if err != nil {
		t.Fatalf("reopen keyed: %v", err)
	}
	defer db2.Close()
	var n int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM accounts WHERE id='a1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestOpenWrongKeyFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enc.db")
	db, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	if _, err := Open(path, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("opening with a wrong key should fail")
	}
}

func TestOpenLegacyPlaintextArchived(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// simulate a pre-M3 plaintext database
	plain, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	plain.Close()
	if raw, _ := os.ReadFile(path); string(raw[:16]) != "SQLite format 3\x00" {
		t.Fatal("fixture should be a plaintext SQLite file")
	}

	db, err := Open(path, testKey) // must archive the plaintext file, not fail
	if err != nil {
		t.Fatalf("open over legacy plaintext: %v", err)
	}
	defer db.Close()

	if _, err := os.Stat(path + ".plain.bak"); err != nil {
		t.Fatalf("legacy database not archived: %v", err)
	}
}
