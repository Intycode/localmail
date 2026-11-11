package ipc

import (
	"context"
	"encoding/json"
	"testing"

	"mail/backend/internal/auth"
)

// seedServerThread makes an account with an IMAP host and one synced thread
// (message UID 7 in INBOX), so user actions produce server commands.
func seedServerThread(t *testing.T) (*App, string) {
	t.Helper()
	app := newQueueApp(t)
	app.Secrets = auth.NewRuntimeSecrets() // empty vault: every server call fails
	inbox, archive := seedAccount(t, app, "acc1", "Test", "t@example.com")
	for _, q := range []string{
		`UPDATE accounts SET imap_host='imap.example.com' WHERE id='acc1'`,
		`UPDATE folders SET imap_name='INBOX' WHERE id='` + inbox + `'`,
		`UPDATE folders SET imap_name='Archive' WHERE id='` + archive + `'`,
	} {
		if _, err := app.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	seedThread(t, app, "th1", inbox, "acc1", "Oggetto", "Mario", "m@example.com", "2026-02-01T10:00:00Z")
	if _, err := app.DB.Exec(`UPDATE messages SET provider_msg_id='7', is_unread=1 WHERE id='th1-m1'`); err != nil {
		t.Fatal(err)
	}
	return app, archive
}

func pendingCmds(t *testing.T, app *App) []serverCmd {
	t.Helper()
	items, err := app.Queue.List(50)
	if err != nil {
		t.Fatal(err)
	}
	var out []serverCmd
	for _, it := range items {
		var c serverCmd
		if err := json.Unmarshal([]byte(it.Payload), &c); err != nil {
			t.Fatal(err)
		}
		if it.Kind == cmdMove {
			c.Unread = false
		}
		out = append(out, c)
	}
	return out
}

// The action must finish locally and queue the server change, even though
// the server is unreachable (no credential in the vault here).
func TestMarkReadIsLocalFirst(t *testing.T) {
	app, _ := seedServerThread(t)

	if _, err := opThreadMarkRead(context.Background(), app, []byte(`{"threadId":"th1"}`)); err != nil {
		t.Fatalf("markRead must not wait for the server: %v", err)
	}
	var unread int
	_ = app.DB.QueryRow(`SELECT is_unread FROM messages WHERE id='th1-m1'`).Scan(&unread)
	if unread != 0 {
		t.Fatal("local flag not updated")
	}
	cmds := pendingCmds(t, app)
	if len(cmds) != 1 || cmds[0].Mailbox != "INBOX" || len(cmds[0].UIDs) != 1 || cmds[0].UIDs[0] != 7 || cmds[0].Unread {
		t.Fatalf("queued commands = %+v, want one read flag for INBOX uid 7", cmds)
	}
}

func TestArchiveQueuesMoveWithOriginalUIDs(t *testing.T) {
	app, _ := seedServerThread(t)

	if _, err := opThreadArchive(context.Background(), app, []byte(`{"threadId":"th1"}`)); err != nil {
		t.Fatalf("archive: %v", err)
	}
	cmds := pendingCmds(t, app)
	if len(cmds) != 1 || cmds[0].Mailbox != "INBOX" || cmds[0].Target != "Archive" || cmds[0].UIDs[0] != 7 {
		t.Fatalf("queued commands = %+v, want INBOX→Archive uid 7", cmds)
	}
	// the local copy already lives in the archive
	var folder string
	_ = app.DB.QueryRow(`SELECT folder FROM threads WHERE id='th1'`).Scan(&folder)
	if folder != "f:acc1:archive" {
		t.Fatalf("thread folder = %s, want the archive", folder)
	}
}

// A failing command blocks the later ones of the same account (order matters
// for flags); a command that keeps failing for a non-network reason is
// dropped after maxServerCmdAttempts so it cannot clog the queue forever.
func TestServerCmdOrderingAndGiveUp(t *testing.T) {
	app, _ := seedServerThread(t)
	for _, u := range []string{`{"threadId":"th1"}`} {
		if _, err := opThreadMarkRead(context.Background(), app, []byte(u)); err != nil {
			t.Fatal(err)
		}
		if _, err := opThreadMarkUnread(context.Background(), app, []byte(u)); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := app.Queue.Count(); n != 2 {
		t.Fatalf("queued = %d, want 2", n)
	}

	// no credential in the vault: the first command fails, the second waits
	processed, _ := drainPass(app)
	if processed != 1 {
		t.Fatalf("processed = %d, want 1 (second blocked behind the first)", processed)
	}

	// make the failing head command exhaust its attempts, due again
	if _, err := app.DB.Exec(`UPDATE pending_ops SET attempts=?, next_attempt_at='2000-01-01T00:00:00Z' WHERE attempts>0`, maxServerCmdAttempts-1); err != nil {
		t.Fatal(err)
	}
	drainPass(app)
	if n, _ := app.Queue.Count(); n != 1 {
		t.Fatalf("queued after give-up = %d, want 1 (head dropped, tail kept)", n)
	}
}
