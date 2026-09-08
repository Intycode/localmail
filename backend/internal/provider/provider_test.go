package provider

import (
	"testing"
	"time"

	"mail/backend/internal/domain"
)

func TestMockProviderReturnsEmptySync(t *testing.T) {
	p := NewMock()
	fs, err := p.SyncFolder(FolderRef{})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(fs.Messages) != 0 || len(fs.AllUIDs) != 0 {
		t.Errorf("expected no messages, got %+v", fs)
	}
	if _, err := p.Send(domain.Recipient{Email: "a@b.dev"}, domain.ComposeMessage{}); err != nil {
		t.Errorf("send (mock): %v", err)
	}
	if _, err := p.Append("INBOX", []byte("x"), nil, time.Now()); err != nil {
		t.Errorf("append (mock): %v", err)
	}
	if err := p.Delete("INBOX", []uint32{1}); err != nil {
		t.Errorf("delete: %v", err)
	}
	if err := p.MarkRead("INBOX", []uint32{1}); err != nil {
		t.Errorf("markRead: %v", err)
	}
}