package sync

// Performance benchmarks for the sync pipeline. They do not run during
// `go test ./...`: invoke them explicitly, e.g.
//
//	go test ./internal/sync -bench . -benchtime 1x

import (
	"fmt"
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
	"mail/backend/internal/thread"
)

func mkRaw(i int) []byte {
	return []byte(fmt.Sprintf("Subject: Email numero %d\r\nFrom: Mittente %d <m%d@x.dev>\r\nTo: luca@x.dev\r\nDate: Mon, 25 Aug 2026 09:%02d:00 +0200\r\nMessage-Id: <p%d@x.dev>\r\n\r\nQuesto e il corpo del messaggio di prova numero %d con un testo abbastanza lungo per il FTS.", i, i, i, i%60, i, i))
}

type benchProvider struct{ msgs []provider.RawMessage; uids []uint32 }

func (p *benchProvider) SyncFolder(ref provider.FolderRef) (provider.FolderSync, error) {
	if ref.LastUID == 0 {
		return provider.FolderSync{UIDValidity: 1, Messages: p.msgs, AllUIDs: p.uids}, nil
	}
	var inc []provider.RawMessage
	for _, m := range p.msgs {
		if m.UID > ref.LastUID {
			inc = append(inc, m)
		}
	}
	return provider.FolderSync{UIDValidity: 1, Messages: inc, AllUIDs: p.uids}, nil
}
func (p *benchProvider) MarkRead(string, []uint32) error     { return nil }
func (p *benchProvider) MarkUnread(string, []uint32) error   { return nil }
func (p *benchProvider) Move(string, []uint32, string) error { return nil }
func (p *benchProvider) Delete(string, []uint32) error       { return nil }
func (p *benchProvider) Send(domain.Recipient, domain.ComposeMessage) ([]byte, error) { return nil, nil }
func (p *benchProvider) Append(string, []byte, []string, time.Time) (uint32, error)   { return 0, nil }
func (p *benchProvider) FetchRaw(string, uint32) ([]byte, error)             { return nil, nil }
func (p *benchProvider) Idle(string, chan<- struct{}) (func(), error)                 { return nil, imap.ErrIdleUnsupported }

// benchEnv builds a fresh engine with one IMAP-like account.
func benchEnv(b *testing.B) (*Engine, string) {
	b.Helper()
	db, err := database.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	accs := account.New(db)
	if err := accs.Create(domain.Account{ID: "a1", Name: "P", Email: "p@x.dev", Provider: "generic-imap", Active: true,
		Folders: []domain.Folder{{ID: "f-inbox", Account: "a1", Name: "INBOX", Type: "inbox"}}}); err != nil {
		b.Fatal(err)
	}
	return New(db, provider.NewMock(), events.New()), "a1"
}

func benchMsgs(n int) *benchProvider {
	p := &benchProvider{uids: make([]uint32, 0, n)}
	for i := 1; i <= n; i++ {
		p.msgs = append(p.msgs, provider.RawMessage{UID: uint32(i), Data: mkRaw(i)})
		p.uids = append(p.uids, uint32(i))
	}
	return p
}

func BenchmarkInitialSync200(b *testing.B) {
	engine, accID := benchEnv(b)
	p := benchMsgs(200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := engine.SyncAccountWith(accID, p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkIncrementalSync10(b *testing.B) {
	engine, accID := benchEnv(b)
	p := benchMsgs(200)
	if err := engine.SyncAccountWith(accID, p); err != nil {
		b.Fatal(err)
	}
	for i := 201; i <= 210; i++ {
		p.msgs = append(p.msgs, provider.RawMessage{UID: uint32(i), Data: mkRaw(i)})
		p.uids = append(p.uids, uint32(i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := engine.SyncAccountWith(accID, p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBulk10k(b *testing.B) {
	engine, accID := benchEnv(b)
	p := benchMsgs(10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := engine.SyncAccountWith(accID, p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkThreadList(b *testing.B) {
	engine, accID := benchEnv(b)
	if err := engine.SyncAccountWith(accID, benchMsgs(10000)); err != nil {
		b.Fatal(err)
	}
	db := engine.db
	threads := thread.New(db)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		list, err := threads.ListByFolder("f-inbox")
		if err != nil {
			b.Fatal(err)
		}
		for j := range list {
			ids, err := threads.MessageIDs(list[j].ID)
			if err != nil {
				b.Fatal(err)
			}
			list[j].MessageIDs = ids
		}
	}
}

func BenchmarkFTSQuery(b *testing.B) {
	engine, accID := benchEnv(b)
	if err := engine.SyncAccountWith(accID, benchMsgs(10000)); err != nil {
		b.Fatal(err)
	}
	s := search.New(engine.db)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Query(domain.SearchQuery{Query: "numero 9999"}); err != nil {
			b.Fatal(err)
		}
	}
}