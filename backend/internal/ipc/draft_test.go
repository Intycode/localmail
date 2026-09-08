package ipc

// Draft flow: save (local-first, drafts folder auto-created), reopen by id,
// update the same draft, delete. Server-side APPEND is best effort and does
// not fail the local save when the secret is unavailable.

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDraftFlow(t *testing.T) {
	env := startEnv(t)
	host, portRaw, _ := net.SplitHostPort(env.addr)
	port, _ := strconv.Atoi(portRaw)

	ok, data, errMsg := env.call(t, "d0", "accounts.add", map[string]any{
		"name": "Luca", "email": "luca@x.dev",
		"imap":          map[string]any{"host": host, "port": port, "security": "plain", "username": "luca"},
		"secret":        secret,
		"allowInsecure": true,
	})
	if !ok {
		t.Fatalf("accounts.add: %s", errMsg)
	}
	var acc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &acc); err != nil {
		t.Fatal(err)
	}

	if ok, _, errMsg = env.call(t, "d1", "sync.start", map[string]any{"accountId": acc.ID, "secret": secret}); !ok {
		t.Fatalf("sync.start: %s", errMsg)
	}
	env.waitEvent(t, "sync.completed", 15*time.Second)

	// save a draft (no IMAP Drafts folder on the server: local-only path)
	ok, data, errMsg = env.call(t, "d2", "draft.save", map[string]any{
		"accountId": acc.ID,
		"message": map[string]any{
			"to":      []map[string]any{{"email": "anna@acme.dev"}},
			"subject": "Bozza uno",
			"body":    "testo della bozza",
		},
	})
	if !ok {
		t.Fatalf("draft.save: %s", errMsg)
	}
	var saved struct {
		DraftID string `json:"draftId"`
	}
	if err := json.Unmarshal(data, &saved); err != nil || saved.DraftID == "" {
		t.Fatalf("draft.save response: %s", data)
	}

	// the draft is retrievable and lives in a drafts folder
	ok, data, errMsg = env.call(t, "d3", "message.get", map[string]any{"messageId": saved.DraftID})
	if !ok {
		t.Fatalf("message.get: %s", errMsg)
	}
	var msg struct {
		FolderID string `json:"folderId"`
		Subject  string `json:"subject"`
		Body     string `json:"body"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.FolderID, "drafts") || msg.Subject != "Bozza uno" {
		t.Fatalf("draft message wrong: %s", data)
	}

	// saving with the same id replaces the row
	ok, data, errMsg = env.call(t, "d4", "draft.save", map[string]any{
		"accountId": acc.ID,
		"draftId":   saved.DraftID,
		"message": map[string]any{
			"to":      []map[string]any{{"email": "anna@acme.dev"}},
			"subject": "Bozza uno (rivista)",
			"body":    "aggiornata",
		},
	})
	if !ok {
		t.Fatalf("draft.save update: %s", errMsg)
	}
	var updated struct {
		DraftID string `json:"draftId"`
	}
	_ = json.Unmarshal(data, &updated)
	ok, data, errMsg = env.call(t, "d5", "message.get", map[string]any{"messageId": updated.DraftID})
	if !ok {
		t.Fatalf("message.get updated: %s", errMsg)
	}
	if !strings.Contains(string(data), "rivista") {
		t.Fatalf("draft not updated: %s", data)
	}

	// delete purges it
	ok, _, errMsg = env.call(t, "d6", "draft.delete", map[string]any{"draftId": saved.DraftID})
	if !ok {
		t.Fatalf("draft.delete: %s", errMsg)
	}
	if ok, _, _ = env.call(t, "d7", "message.get", map[string]any{"messageId": saved.DraftID}); ok {
		t.Fatal("draft still present after delete")
	}
}
