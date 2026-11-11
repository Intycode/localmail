// Package provider defines the mail-provider contract and ships a mock.
//
// Providers: Gmail, Microsoft, Generic IMAP. The contract is what real
// transports implement without touching the sync engine or the UI.
package provider

import (
	"fmt"
	"time"

	"mail/backend/internal/domain"
	"mail/backend/internal/email/imap"
)

// FolderRef identifies the folder to sync, with the local incremental state.
type FolderRef struct {
	FolderID    string // local folder id (folders.id)
	MailboxName string // remote IMAP mailbox name (folders.imap_name)
	UIDValidity uint32 // last known UIDVALIDITY (0 = never synced)
	LastUID     uint32 // last synced UID (0 = never synced)
	// OnBatch, when set, receives the new messages in batches while the
	// provider fetches (UIDVALIDITY of the folder + raw messages) and they
	// are NOT repeated in FolderSync.Messages: memory stays bounded by one
	// batch. Providers that cannot stream ignore it and return everything in
	// FolderSync.Messages. An error aborts the folder sync.
	OnBatch func(uidValidity uint32, msgs []RawMessage) error
}

// FolderSync is the result of syncing one folder.
type FolderSync struct {
	UIDValidity uint32
	// Messages are the new raw messages (UID > ref.LastUID, or the initial
	// window on first sync / UIDVALIDITY change).
	Messages []RawMessage
	// AllUIDs is the complete UID list of the folder, used to reconcile
	// server-side deletions locally.
	AllUIDs []uint32
	// Partial: AllUIDs covers only the fetched window (Graph), so it must
	// not be used to reconcile deletions.
	Partial bool
	// Flags holds the current server flags of messages synced earlier
	// (incremental IMAP passes): read on another device shows up here.
	Flags map[uint32][]string
}

// RawMessage is an unparsed message plus its server UID and flags.
type RawMessage struct {
	UID   uint32
	Flags []string
	Data  []byte
}

// MailProvider is the transport abstraction the sync engine consumes.
type MailProvider interface {
	// SyncFolder pulls one folder's new messages from the remote server.
	SyncFolder(ref FolderRef) (FolderSync, error)
	// MarkRead marks messages read on the provider.
	MarkRead(mailbox string, uids []uint32) error
	// MarkUnread clears the read flag on the provider (removes \Seen).
	MarkUnread(mailbox string, uids []uint32) error
	// Move moves messages to another mailbox on the provider.
	Move(mailbox string, uids []uint32, target string) error
	// Delete removes messages on the provider.
	Delete(mailbox string, uids []uint32) error
	// Send delivers a composed message via the account's SMTP transport and
	// returns the raw RFC 5322 bytes delivered (used for the APPEND into
	// Sent). from is the account identity to send as.
	Send(from domain.Recipient, msg domain.ComposeMessage) ([]byte, error)
	// Append delivers raw bytes into a mailbox (IMAP APPEND), returning the
	// server-assigned UID when the server reports APPENDUID (0 otherwise).
	Append(mailbox string, raw []byte, flags []string, date time.Time) (uint32, error)
	// Idle enters push mode on the mailbox, sending struct{} on notify on
	// server activity. Returns ErrIdleUnsupported when unavailable.
	Idle(mailbox string, notify chan<- struct{}) (stop func(), err error)
	// FetchRaw downloads one message's raw bytes from the mailbox. Used for
	// on-demand attachment extraction: raw bodies are never persisted.
	FetchRaw(mailbox string, uid uint32) ([]byte, error)
}

// Closer is implemented by providers that hold network resources (IMAP
// sessions): the engine closes them at the end of an account sync.
type Closer interface {
	Close()
}

// SMTPConfigurer is implemented by providers that accept an explicit
// outgoing SMTP configuration (the IMAP transport; JMAP submits through the
// server instead). The result type is the concrete provider to keep the
// fluent chaining the IMAP client already exposes.
type SMTPConfigurer interface {
	WithSMTP(domain.SmtpConfig) *ImapProvider
}

// Pinger is implemented by providers that can validate credentials with a
// cheap dial+login (no full sync).
type Pinger interface {
	Ping() error
}

// Ping validates credentials when the provider supports it.
func Ping(p MailProvider) error {
	if pi, ok := p.(Pinger); ok {
		return pi.Ping()
	}
	return nil
}

// MockProvider implements MailProvider with no network: it reports no
// messages, so the engine's sync stays a no-op until a real provider lands.
type MockProvider struct{}

// FetchRaw has no meaning without a network backend.
func (m MockProvider) FetchRaw(string, uint32) ([]byte, error) {
	return nil, fmt.Errorf("fetch raw: non disponibile con il provider mock")
}

func NewMock() MailProvider { return MockProvider{} }

func (MockProvider) SyncFolder(FolderRef) (FolderSync, error)  { return FolderSync{}, nil }
func (MockProvider) MarkRead(string, []uint32) error           { return nil }
func (MockProvider) MarkUnread(string, []uint32) error         { return nil }
func (MockProvider) Move(string, []uint32, string) error       { return nil }
func (MockProvider) Delete(string, []uint32) error             { return nil }
func (MockProvider) Send(domain.Recipient, domain.ComposeMessage) ([]byte, error) { return nil, nil }
func (MockProvider) Append(string, []byte, []string, time.Time) (uint32, error)    { return 0, nil }
func (MockProvider) Idle(string, chan<- struct{}) (func(), error)                  { return nil, imap.ErrIdleUnsupported }