package ipc

// Tests for the undo-send / scheduled-send path: a future sendAt parks the
// message in the outbox queue (next_attempt_at = sendAt) and returns
// {queued, scheduled, opId}; the payload stored in the queue has the
// schedule stripped so the worker's replay is a normal immediate send.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mail/backend/internal/database"
	"mail/backend/internal/message"
	"mail/backend/internal/queue"
	"mail/backend/internal/thread"
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func TestSendScheduledParksInQueue(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "sched.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app := &App{
		DB: db,
		Threads: thread.New(db),
		Messages: message.New(db),
		Queue: queue.New(db),
	}
	if _, err := db.Exec(
		`INSERT INTO accounts(id,name,email,provider,active,created_at)
		 VALUES('a1','Luca','luca@x.dev','mock',1,'2026-01-01T00:00:00.000000000Z')`,
	); err != nil {
		t.Fatal(err)
	}

	sendAt := time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)
	params := map[string]any{
		"accountId": "a1",
		"message": map[string]any{
			"to":      []map[string]string{{"email": "anna@acme.dev"}},
			"subject": "programmato",
			"body":    "in arrivo",
			"sendAt":  sendAt,
		},
	}
	raw, err := jsonMarshal(params)
	if err != nil {
		t.Fatal(err)
	}
	res, err := opSend(context.Background(), app, raw)
	if err != nil {
		t.Fatalf("scheduled send: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok || m["queued"] != true || m["scheduled"] != true {
		t.Fatalf("unexpected response: %#v", res)
	}
	opID, _ := m["opId"].(string)
	if opID == "" {
		t.Fatal("missing opId")
	}

	// the op is queued but NOT due yet
	if n, _ := app.Queue.Count(); n != 1 {
		t.Fatalf("queue count = %d, want 1", n)
	}
	due, err := app.Queue.Due(time.Now().UTC(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("scheduled op is already due: %d", len(due))
	}
	due, err = app.Queue.Due(time.Now().UTC().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("scheduled op not due after sendAt: %d", len(due))
	}
	// the stored payload must NOT contain the schedule (replay = normal send)
	if strings.Contains(due[0].Payload, `"sendAt"`) {
		t.Errorf("payload still carries sendAt: %s", due[0].Payload)
	}
	// nothing was sent/persisted locally yet
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("scheduled message leaked into the store: %d", n)
	}

	// the user can cancel while pending
	if err := app.Queue.Cancel(opID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if n, _ := app.Queue.Count(); n != 0 {
		t.Fatalf("cancel did not remove the op: %d", n)
	}
}

func TestSendScheduledRejectsBadTimestamp(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "bad.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app := &App{DB: db, Threads: thread.New(db), Messages: message.New(db), Queue: queue.New(db)}
	if _, err := db.Exec(
		`INSERT INTO accounts(id,name,email,provider,active,created_at)
		 VALUES('a1','Luca','luca@x.dev','mock',1,'2026-01-01T00:00:00.000000000Z')`,
	); err != nil {
		t.Fatal(err)
	}
	params := map[string]any{
		"message": map[string]any{
			"to":      []map[string]string{{"email": "anna@acme.dev"}},
			"subject": "x",
			"body":    "y",
			"sendAt":  "non Una data",
		},
	}
	raw, _ := jsonMarshal(params)
	if _, err := opSend(context.Background(), app, raw); err == nil {
		t.Fatal("expected error for invalid sendAt")
	}
}

func TestEnqueueAtRespectsRunAt(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(
		`INSERT INTO accounts(id,name,email,provider,active,created_at)
		 VALUES('a1','Luca','luca@x.dev','mock',1,'2026-01-01T00:00:00.000000000Z')`,
	); err != nil {
		t.Fatal(err)
	}
	s := queue.New(db)
	runAt := time.Now().UTC().Add(5 * time.Minute)
	if _, err := s.EnqueueAt("a1", "send", []byte(`{}`), runAt); err != nil {
		t.Fatal(err)
	}
	if due, _ := s.Due(time.Now().UTC(), 10); len(due) != 0 {
		t.Fatal("op due before its run time")
	}
	if due, _ := s.Due(runAt.Add(time.Minute), 10); len(due) != 1 {
		t.Fatal("op not due at its run time")
	}
}
