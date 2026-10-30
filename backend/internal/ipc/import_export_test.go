package ipc

// M11 integration: import from an mbox file into the local-only "Importate"
// folder (raw preserved for re-export), export back to mbox round-trip, and
// a backup via VACUUM INTO.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mail/backend/internal/account"
	"mail/backend/internal/auth"
	"mail/backend/internal/database"
	"mail/backend/internal/events"
	"mail/backend/internal/message"
	"mail/backend/internal/thread"
)

func newIOApp(t *testing.T) *App {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "io.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &App{
		DB:       db,
		Threads:  thread.New(db),
		Messages: message.New(db),
		Accounts: account.New(db),
		Secrets:  auth.NewRuntimeSecrets(),
		Events:   events.New(),
	}
}

func TestImportMailsFromMbox(t *testing.T) {
	app := newIOApp(t)
	seedAccount(t, app, "acc1", "Test", "t@example.com")

	mboxData := "From a@b.c Mon Aug 25 09:30:00 2026\n" +
		"Subject: Prima importata\nFrom: Anna <anna@acme.dev>\n\nCorpo numero uno.\n\n" +
		"From d@e.f Tue Aug 26 10:00:00 2026\n" +
		"Subject: Seconda importata\nFrom: Luca <luca@x.dev>\n\n>From una riga mascherata.\n\n"
	src := filepath.Join(t.TempDir(), "in.mbox")
	if err := os.WriteFile(src, []byte(mboxData), 0o600); err != nil {
		t.Fatal(err)
	}

	params, _ := json.Marshal(map[string]string{"accountId": "acc1", "src": src})
	out, err := opImportMails(context.Background(), app, params)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	res := out.(map[string]any)
	if res["imported"].(int) != 2 || res["skipped"].(int) != 0 {
		t.Fatalf("import result: %+v", res)
	}
	folderID := res["folderId"].(string)
	if !strings.Contains(folderID, "acc1") {
		t.Fatalf("folder id inatteso: %s", folderID)
	}
	msgs, err := app.Messages.ListByFolder(folderID)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("messaggi importati: %d err=%v", len(msgs), err)
	}
	if msgs[0].Subject != "Prima importata" {
		t.Fatalf("ordine/oggetto: %s", msgs[0].Subject)
	}
	// raw preserved for re-export
	raw, err := app.Messages.GetRaw(msgs[0].ID)
	if err != nil || raw == nil || !strings.Contains(string(raw), "Corpo numero uno.") {
		t.Fatalf("raw non preservato: %v", err)
	}
	// mboxrd unescape on import
	raw2, _ := app.Messages.GetRaw(msgs[1].ID)
	if !strings.Contains(string(raw2), "From una riga mascherata.") {
		t.Fatalf("unescape fallito: %s", raw2)
	}
	// second import appends (idempotent-ish growth, no crash)
	out2, err := opImportMails(context.Background(), app, params)
	if err != nil {
		t.Fatal(err)
	}
	if out2.(map[string]any)["imported"].(int) != 2 {
		t.Fatal("secondo import fallito")
	}
	if all, _ := app.Messages.ListByFolder(folderID); len(all) != 4 {
		t.Fatalf("il secondo import ha sovrascritto il primo: %d messaggi", len(all))
	}
}

func TestExportMailboxRoundTrip(t *testing.T) {
	app := newIOApp(t)
	seedAccount(t, app, "acc1", "Test", "t@example.com")
	folder, _ := app.Accounts.EnsureImportFolder("acc1")

	// import two messages, then export the folder to mbox
	mboxData := "From a@b.c Mon Aug 25 09:30:00 2026\nSubject: M1\nFrom: a@b.c\n\nuno\n\n" +
		"From d@e.f Tue Aug 26 10:00:00 2026\nSubject: M2\nFrom: d@e.f\n\ndue\n\n"
	src := filepath.Join(t.TempDir(), "in.mbox")
	_ = os.WriteFile(src, []byte(mboxData), 0o600)
	params, _ := json.Marshal(map[string]string{"accountId": "acc1", "src": src})
	if _, err := opImportMails(context.Background(), app, params); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "out.mbox")
	expParams, _ := json.Marshal(map[string]string{"folderId": folder, "dest": dest})
	if _, err := opExportMailbox(context.Background(), app, expParams); err != nil {
		t.Fatalf("export: %v", err)
	}
	exported, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	for _, sep := range []string{"From a@b.c", "From d@e.f"} {
		if !strings.Contains(string(exported), sep) {
			t.Fatalf("separatore %q mancante nell'archivio", sep)
		}
	}
	if !strings.Contains(string(exported), "Subject: M1") || !strings.Contains(string(exported), "Subject: M2") {
		t.Fatal("messaggi mancanti nell'archivio")
	}
}

// Backup/restore is exercised in Rust (backup_create/backup_restore): the
// adiantum VFS cannot VACUUM INTO arbitrary host paths.
