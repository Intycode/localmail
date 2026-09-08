package imap

import "time"

// FolderInfo describes a mailbox discovered on the server.
type FolderInfo struct {
	Name       string // full IMAP mailbox name (e.g. "INBOX", "Archive/2026")
	SpecialUse string // RFC 6154 attribute when advertised ("\\Sent", "\\Trash", ...) or ""
	NoSelect   bool   // \\Noselect mailboxes cannot hold messages
}

// RawMessage is an unparsed message plus its server UID and flags.
type RawMessage struct {
	UID   uint32
	Flags []string
	Data  []byte
}

// SelectState is the state of a mailbox after SELECT.
type SelectState struct {
	UIDValidity uint32
	Exists      uint32
}

// Session is an authenticated IMAP session. Implementations must be safe to
// close once; all other calls happen before Close.
type Session interface {
	Close() error
	// Folders lists selectable mailboxes with special-use attributes.
	Folders() ([]FolderInfo, error)
	// Select makes mailbox the current folder.
	Select(mailbox string) (SelectState, error)
	// UIDs returns every message UID in the current folder, ascending.
	UIDs() ([]uint32, error)
	// Fetch returns raw MIME for the given UIDs (BODY.PEEK: never sets \Seen).
	Fetch(uids []uint32) ([]RawMessage, error)
	// MarkSeen adds the \Seen flag to the given UIDs.
	MarkSeen(uids []uint32) error
	// MarkUnseen removes the \Seen flag from the given UIDs (mark as unread).
	MarkUnseen(uids []uint32) error
	// MoveTo moves the given UIDs to another mailbox (MOVE when supported).
	MoveTo(uids []uint32, mailbox string) error
	// Delete removes the given UIDs (\Deleted + expunge).
	Delete(uids []uint32) error
	// Append delivers raw bytes into the mailbox (IMAP APPEND) with the given
	// flags and internal date, returning the server-assigned UID when the
	// server reports APPENDUID (0 otherwise).
	Append(mailbox string, raw []byte, flags []string, date time.Time) (uint32, error)
	// Idle blocks-free push: selects the mailbox and enters IDLE, sending
	// struct{} on notify whenever the server reports new activity. Returns a
	// stop function (sends DONE) and ErrIdleUnsupported when the server does
	// not advertise IDLE.
	Idle(mailbox string, notify chan<- struct{}) (stop func(), err error)
}

// Dialer creates authenticated sessions from a config and a runtime password.
type Dialer interface {
	Dial(cfg Config, password string) (Session, error)
}

// MockSession returns empty results; used by tests and as placeholder.
type MockSession struct{}

func (MockSession) Close() error                          { return nil }
func (MockSession) Folders() ([]FolderInfo, error)        { return nil, nil }
func (MockSession) Select(string) (SelectState, error)    { return SelectState{}, nil }
func (MockSession) UIDs() ([]uint32, error)               { return nil, nil }
func (MockSession) Fetch([]uint32) ([]RawMessage, error)  { return nil, nil }
func (MockSession) MarkSeen([]uint32) error               { return nil }
func (MockSession) MarkUnseen([]uint32) error             { return nil }
func (MockSession) MoveTo([]uint32, string) error         { return nil }
func (MockSession) Delete([]uint32) error                 { return nil }
func (MockSession) Append(string, []byte, []string, time.Time) (uint32, error) { return 0, nil }
func (MockSession) Idle(string, chan<- struct{}) (func(), error)            { return nil, ErrIdleUnsupported }

var _ Session = MockSession{}

// NewMockSession returns an empty Session (tests / placeholder).
func NewMockSession() Session { return MockSession{} }