package imap

// Regression test for the mark-unread round-trip: MarkUnseen clears \Seen
// (STORE -FLAGS (\Seen)) without erroring, and the flag change is visible
// on a subsequent fetch.

import (
	"bytes"
	"slices"
	"testing"

	"github.com/emersion/go-imap/v2"
)

func TestClientSessionMarkUnseen(t *testing.T) {
	addr, user := startTestServer(t)
	cfg, password := testConfig(addr)

	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("create inbox: %v", err)
	}
	if _, err := user.Append("INBOX", &literal{r: bytes.NewReader([]byte(msg1))}, &imap.AppendOptions{}); err != nil {
		t.Fatalf("append: %v", err)
	}

	s, err := Dial(cfg, password)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer s.Close()

	if _, err := s.Select("INBOX"); err != nil {
		t.Fatalf("select: %v", err)
	}
	uids, err := s.UIDs()
	if err != nil {
		t.Fatalf("uids: %v", err)
	}
	if len(uids) != 1 {
		t.Fatalf("uids = %v, want 1", uids)
	}

	fetchFlags := func() []string {
		raws, err := s.Fetch([]uint32{uids[0]})
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		return raws[0].Flags
	}

	// add \Seen ...
	if err := s.MarkSeen([]uint32{uids[0]}); err != nil {
		t.Fatalf("markSeen: %v", err)
	}
	if !slices.Contains(fetchFlags(), "\\Seen") {
		t.Fatalf("flag \\Seen missing after markSeen")
	}

	// ... then clear it again (the flow behind "segna come non letto")
	if err := s.MarkUnseen([]uint32{uids[0]}); err != nil {
		t.Fatalf("markUnseen: %v", err)
	}
	if slices.Contains(fetchFlags(), "\\Seen") {
		t.Errorf("flags after markUnseen still contain \\Seen")
	}
}
