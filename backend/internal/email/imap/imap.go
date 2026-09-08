// Package imap defines the IMAP provider contract. Real Gmail/Microsoft/
// generic-IMAP transports plug in here later; for now only the mock is used.
package imap

import "mail/backend/internal/domain"

// Client is the low-level IMAP transport the sync engine talks to.
type Client interface {
	// FetchUIDs lists message UIDs in a folder (highest first).
	FetchUIDs(folderID string) ([]uint32, error)
	// FetchMessage returns the raw MIME bytes for a UID.
	FetchMessage(uid uint32) ([]byte, error)
	// MarkSeen marks a UID as read.
	MarkSeen(uid uint32) error
	// Move moves a UID to another folder.
	Move(uid uint32, targetFolder string) error
	// Delete removes a UID.
	Delete(uid uint32) error
}

// MockClient returns empty results: used until a real IMAP integration lands.
type MockClient struct{}

func (MockClient) FetchUIDs(string) ([]uint32, error)  { return nil, nil }
func (MockClient) FetchMessage(uint32) ([]byte, error) { return nil, nil }
func (MockClient) MarkSeen(uint32) error               { return nil }
func (MockClient) Move(uint32, string) error           { return nil }
func (MockClient) Delete(uint32) error                 { return nil }

var _ Client = MockClient{}

// NewMock returns a MockClient.
func NewMock() Client { return MockClient{} }

var _ = domain.Folder{} // keep import for future use
