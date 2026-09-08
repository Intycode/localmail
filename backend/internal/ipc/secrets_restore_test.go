package ipc

// Unit test for the engine-internal secrets.restore op: re-delivered
// credentials land in the runtime vault, so withProvider finds them and
// server round-trip ops work right after an engine restart.

import (
	"context"
	"path/filepath"
	"testing"

	"mail/backend/internal/account"
	"mail/backend/internal/auth"
	"mail/backend/internal/database"
	"mail/backend/internal/message"
	"mail/backend/internal/thread"
)

func TestSecretsRestorePopulatesRuntimeVault(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "restore.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app := &App{DB: db, Threads: thread.New(db), Messages: message.New(db), Accounts: account.New(db), Secrets: auth.NewRuntimeSecrets()}

	if _, err := db.Exec(
		`INSERT INTO accounts(id,name,email,provider,active,created_at)
		 VALUES('a1','Luca','luca@x.dev','generic-imap',0,'2026-01-01T00:00:00.000000000Z')`,
	); err != nil {
		t.Fatal(err)
	}

	// before restore: the vault is empty (the post-restart bug this fixes)
	if _, ok := app.Secrets.Get("a1"); ok {
		t.Fatal("vault should start empty")
	}

	data, err := opSecretsRestore(context.Background(), app, []byte(
		`{"secrets":[{"accountId":"a1","secret":"s3cret"},{"accountId":"ghost","secret":""}]}`))
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if data.(map[string]int)["restored"] != 1 {
		t.Fatalf("restored = %v, want 1 (empty secret skipped)", data)
	}

	got, ok := app.Secrets.Get("a1")
	if !ok || got.Expose() != "s3cret" {
		t.Fatalf("secret not restored: ok=%v", ok)
	}
	if _, err := opSecretsRestore(context.Background(), app, []byte(`{}`)); err != nil {
		t.Fatalf("empty restore should be a no-op, got %v", err)
	}
}
