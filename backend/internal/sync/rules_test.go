package sync

// M8 integration: rules run at ingest. A move rule relocates the message
// server-side and skips the local persist (it arrives via its target folder);
// mark_read flips the local flag and issues the provider call.

import (
	"testing"

	"mail/backend/internal/domain"
	"mail/backend/internal/provider"
	"mail/backend/internal/rule"
)

func TestRulesApplyAtIngest(t *testing.T) {
	engine, _ := setupEngine(t)

	rs := rule.New(engine.db)
	moveRule := domain.Rule{
		Name: "anna in archivio", Enabled: true,
		Field: "from", Op: "contains", Value: "anna@acme.dev",
		Action: "move", Target: "f-archive",
	}
	if err := rs.Save(&moveRule); err != nil {
		t.Fatalf("save move rule: %v", err)
	}
	readRule := domain.Rule{
		Name: "altro letto", Enabled: true,
		Field: "subject", Op: "contains", Value: "ALTRO",
		Action: "mark_read",
	}
	if err := rs.Save(&readRule); err != nil {
		t.Fatalf("save read rule: %v", err)
	}

	fake := &fakeProvider{folders: map[string]provider.FolderSync{
		"f-inbox": {
			UIDValidity: 42,
			AllUIDs:     []uint32{1, 2, 3},
			Messages: []provider.RawMessage{
				{UID: 1, Data: []byte(rawMsg1)}, // from anna@acme.dev -> move
				{UID: 2, Data: []byte(rawMsg2)}, // no match -> unread
				{UID: 3, Data: []byte(rawMsg3)}, // subject "Altro thread" -> mark_read
			},
		},
		"f-archive": {UIDValidity: 7, AllUIDs: nil},
	}}

	if err := engine.SyncAccountWith("a1", fake); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// moved message: skipped locally (it will arrive via f-archive)
	if _, err := engine.messages.Get("m:a1:f-inbox:1"); err == nil {
		t.Fatal("il messaggio spostato non deve persistere nella cartella sorgente")
	}
	// unmatched message stays unread
	m2, err := engine.messages.Get("m:a1:f-inbox:2")
	if err != nil {
		t.Fatalf("get m2: %v", err)
	}
	if !m2.IsUnread {
		t.Fatal("il messaggio senza match deve restare non letto")
	}
	// mark_read rule applied
	m3, err := engine.messages.Get("m:a1:f-inbox:3")
	if err != nil {
		t.Fatalf("get m3: %v", err)
	}
	if m3.IsUnread {
		t.Fatal("la regola mark_read non applicata")
	}
}

func TestDisabledRulesDoNotApply(t *testing.T) {
	engine, _ := setupEngine(t)

	rs := rule.New(engine.db)
	spenta := domain.Rule{
		Name: "spenta", Enabled: false,
		Field: "from", Op: "contains", Value: "anna@acme.dev", Action: "mark_read",
	}
	if err := rs.Save(&spenta); err != nil {
		t.Fatalf("save: %v", err)
	}

	fake := &fakeProvider{folders: map[string]provider.FolderSync{
		"f-inbox": {
			UIDValidity: 42,
			AllUIDs:     []uint32{1},
			Messages:    []provider.RawMessage{{UID: 1, Data: []byte(rawMsg1)}},
		},
	}}
	if err := engine.SyncAccountWith("a1", fake); err != nil {
		t.Fatalf("sync: %v", err)
	}
	m, err := engine.messages.Get("m:a1:f-inbox:1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !m.IsUnread {
		t.Fatal("una regola disabilitata non deve applicarsi")
	}
}
