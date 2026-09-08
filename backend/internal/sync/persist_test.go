package sync

import (
	"path/filepath"
	"testing"
	"time"

	"mail/backend/internal/account"
	"mail/backend/internal/database"
	"mail/backend/internal/domain"
	"mail/backend/internal/events"
	"mail/backend/internal/email/imap"
	"mail/backend/internal/provider"
	"mail/backend/internal/search"
)

const rawMsg1 = "Subject: Progetto X\r\nFrom: Anna <anna@acme.dev>\r\nTo: luca@x.dev\r\nDate: Mon, 25 Aug 2026 09:30:00 +0200\r\nMessage-Id: <m1@acme.dev>\r\n\r\nCorpo del messaggio numero uno."
const rawMsg2 = "Subject: Re: Progetto X\r\nFrom: Luca <luca@x.dev>\r\nTo: anna@acme.dev\r\nDate: Tue, 26 Aug 2026 10:00:00 +0200\r\nMessage-Id: <m2@x.dev>\r\nIn-Reply-To: <m1@acme.dev>\r\nReferences: <m1@acme.dev>\r\n\r\nRisposta al progetto."
const rawMsg3 = "Subject: Altro thread\r\nFrom: Bo <bo@x.dev>\r\nTo: luca@x.dev\r\nDate: Wed, 27 Aug 2026 11:00:00 +0200\r\n\r\nContenuto separato."

// fakeProvider returns canned folder syncs.
type fakeProvider struct {
	folders map[string]provider.FolderSync
}

func (f *fakeProvider) SyncFolder(ref provider.FolderRef) (provider.FolderSync, error) {
	return f.folders[ref.FolderID], nil
}
func (f *fakeProvider) MarkRead(string, []uint32) error      { return nil }
func (f *fakeProvider) MarkUnread(string, []uint32) error    { return nil }
func (f *fakeProvider) Move(string, []uint32, string) error  { return nil }
func (f *fakeProvider) Delete(string, []uint32) error        { return nil }
func (f *fakeProvider) Send(domain.Recipient, domain.ComposeMessage) ([]byte, error) { return nil, nil }
func (f *fakeProvider) Append(string, []byte, []string, time.Time) (uint32, error)   { return 0, nil }
func (f *fakeProvider) FetchRaw(string, uint32) ([]byte, error)             { return nil, nil }
func (f *fakeProvider) Idle(string, chan<- struct{}) (func(), error)                 { return nil, imap.ErrIdleUnsupported }

func setupEngine(t *testing.T) (*Engine, *account.Store) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	accs := account.New(db)
	acc := domain.Account{
		ID: "a1", Name: "Test", Email: "luca@x.dev", Provider: "generic-imap", Active: true,
		Folders: []domain.Folder{
			{ID: "f-inbox", Account: "a1", Name: "INBOX", Type: "inbox"},
			{ID: "f-archive", Account: "a1", Name: "Archive", Type: "archive"},
		},
	}
	if err := accs.Create(acc); err != nil {
		t.Fatalf("create account: %v", err)
	}
	engine := New(db, provider.NewMock(), events.New())
	return engine, accs
}

func TestSyncPersistsMessagesThreadsAndState(t *testing.T) {
	engine, accs := setupEngine(t)

	fake := &fakeProvider{folders: map[string]provider.FolderSync{
		"f-inbox": {
			UIDValidity: 42,
			AllUIDs:     []uint32{1, 2, 3},
			Messages: []provider.RawMessage{
				{UID: 1, Flags: []string{"\\Seen"}, Data: []byte(rawMsg1)},
				{UID: 2, Flags: []string{"\\Answered"}, Data: []byte(rawMsg2)},
				{UID: 3, Data: []byte(rawMsg3)},
			},
		},
		"f-archive": {UIDValidity: 7, AllUIDs: nil, Messages: nil},
	}}

	if err := engine.SyncAccountWith("a1", fake); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// messages persisted with the right flags
	m1, err := engine.messages.Get("m:a1:f-inbox:1")
	if err != nil {
		t.Fatalf("get m1: %v", err)
	}
	if m1.IsUnread {
		t.Errorf("\\Seen message should be read")
	}
	if m1.MessageID != "m1@acme.dev" {
		t.Errorf("messageId = %q", m1.MessageID)
	}
	m3, err := engine.messages.Get("m:a1:f-inbox:3")
	if err != nil {
		t.Fatalf("get m3: %v", err)
	}
	if !m3.IsUnread {
		t.Errorf("message without \\Seen should be unread")
	}

	// References-based threading: m1 and m2 share a thread, m3 is separate
	threads, err := engine.accounts.Folders("a1")
	if err != nil {
		t.Fatal(err)
	}
	var inbox domain.Folder
	for _, f := range threads {
		if f.ID == "f-inbox" {
			inbox = f
		}
	}
	if inbox.Total != 2 {
		t.Errorf("inbox threads = %d, want 2 (m1+m2 grouped)", inbox.Total)
	}
	if inbox.Unread != 2 {
		t.Errorf("inbox unread = %d, want 2 (thread1 has an unread reply, thread2 unread)", inbox.Unread)
	}

	// folder incremental state advanced
	refs, err := accs.SyncRefs("a1")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if ref.FolderID == "f-inbox" {
			if ref.LastUID != 3 || ref.UIDValidity != 42 {
				t.Errorf("inbox state = %+v, want lastUID 3 uidvalidity 42", ref)
			}
		}
	}

	// FTS5 finds the body text
	s := search.New(engine.db)
	results, err := s.Query(domain.SearchQuery{Query: "Corpo del messaggio"})
	if err != nil {
		t.Fatalf("fts query: %v", err)
	}
	if len(results) == 0 {
		t.Errorf("FTS found nothing for body text")
	}
}

func TestSyncReconcilesServerDeletions(t *testing.T) {
	engine, _ := setupEngine(t)

	full := &fakeProvider{folders: map[string]provider.FolderSync{
		"f-inbox": {
			UIDValidity: 42,
			AllUIDs:     []uint32{1, 2},
			Messages: []provider.RawMessage{
				{UID: 1, Data: []byte(rawMsg1)},
				{UID: 2, Data: []byte(rawMsg2)},
			},
		},
		"f-archive": {UIDValidity: 7},
	}}
	if err := engine.SyncAccountWith("a1", full); err != nil {
		t.Fatalf("sync 1: %v", err)
	}

	// server-side deletion of UID 2
	partial := &fakeProvider{folders: map[string]provider.FolderSync{
		"f-inbox": {UIDValidity: 42, AllUIDs: []uint32{1}},
		"f-archive": {UIDValidity: 7},
	}}
	if err := engine.SyncAccountWith("a1", partial); err != nil {
		t.Fatalf("sync 2: %v", err)
	}

	if _, err := engine.messages.Get("m:a1:f-inbox:2"); err == nil {
		t.Errorf("deleted message still present locally")
	}
	if _, err := engine.messages.Get("m:a1:f-inbox:1"); err != nil {
		t.Errorf("kept message missing: %v", err)
	}
}

func TestSyncHandlesUIDValidityChange(t *testing.T) {
	engine, _ := setupEngine(t)

	first := &fakeProvider{folders: map[string]provider.FolderSync{
		"f-inbox": {UIDValidity: 42, AllUIDs: []uint32{1}, Messages: []provider.RawMessage{
			{UID: 1, Data: []byte(rawMsg1)},
		}},
		"f-archive": {UIDValidity: 7},
	}}
	if err := engine.SyncAccountWith("a1", first); err != nil {
		t.Fatalf("sync 1: %v", err)
	}

	// UIDVALIDITY bumped: UIDs restart, old local data must be wiped
	reset := &fakeProvider{folders: map[string]provider.FolderSync{
		"f-inbox": {UIDValidity: 43, AllUIDs: []uint32{1}, Messages: []provider.RawMessage{
			{UID: 1, Data: []byte(rawMsg3)},
		}},
		"f-archive": {UIDValidity: 7},
	}}
	if err := engine.SyncAccountWith("a1", reset); err != nil {
		t.Fatalf("sync 2: %v", err)
	}

	m, err := engine.messages.Get("m:a1:f-inbox:1")
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Altro thread" {
		t.Errorf("after uidvalidity change, message = %q, want replaced content", m.Subject)
	}
}