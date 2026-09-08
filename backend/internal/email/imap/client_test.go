package imap

import (
	"bytes"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// startTestServer runs an in-process IMAP server (no TLS: tests only) and
// returns its address plus the in-memory user for fixture setup.
func startTestServer(t *testing.T) (string, *imapmemserver.User) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("luca", "test-secret")
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), &imapserver.GreetingData{}, nil
		},
		InsecureAuth: true, // tests only: production always uses TLS
	})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String(), user
}

func testConfig(addr string) (Config, string) {
	host, portRaw, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portRaw)
	return Config{Host: host, Port: port, Security: SecurityPlain, Username: "luca"}, "test-secret"
}

type literal struct {
	r *bytes.Reader
}

func (l *literal) Read(p []byte) (int, error) { return l.r.Read(p) }
func (l *literal) Size() int64                { return int64(l.r.Len()) }

const msg1 = "Subject: Ciao mondo\r\nFrom: Anna <anna@acme.dev>\r\nTo: luca@x.dev\r\nDate: Mon, 25 Aug 2026 09:30:00 +0200\r\n\r\nPrimo corpo."
const msg2 = "Subject: Re: Ciao mondo\r\nFrom: Luca <luca@x.dev>\r\nTo: anna@acme.dev\r\nDate: Tue, 26 Aug 2026 10:00:00 +0200\r\n\r\nRisposta."

func TestClientSessionFlow(t *testing.T) {
	addr, user := startTestServer(t)
	cfg, password := testConfig(addr)

	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("create inbox: %v", err)
	}
	for _, m := range []string{msg1, msg2} {
		if _, err := user.Append("INBOX", &literal{r: bytes.NewReader([]byte(m))}, &imap.AppendOptions{}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	s, err := Dial(cfg, password)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer s.Close()

	folders, err := s.Folders()
	if err != nil {
		t.Fatalf("folders: %v", err)
	}
	found := false
	for _, f := range folders {
		if strings.EqualFold(f.Name, "INBOX") {
			found = true
		}
	}
	if !found {
		t.Errorf("INBOX not in folders: %+v", folders)
	}

	state, err := s.Select("INBOX")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if state.Exists != 2 {
		t.Errorf("exists = %d, want 2", state.Exists)
	}
	if state.UIDValidity == 0 {
		t.Errorf("uidvalidity = 0")
	}

	uids, err := s.UIDs()
	if err != nil {
		t.Fatalf("uids: %v", err)
	}
	if len(uids) != 2 {
		t.Fatalf("uids = %v", uids)
	}

	raws, err := s.Fetch(uids)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(raws) != 2 || !strings.Contains(string(raws[0].Data), "Primo corpo") {
		t.Errorf("fetch = %d msgs, first = %q", len(raws), string(raws[0].Data))
	}

	// MarkSeen must not error; Delete removes one message
	if err := s.MarkSeen([]uint32{uids[0]}); err != nil {
		t.Errorf("markSeen: %v", err)
	}
	if err := s.Delete([]uint32{uids[1]}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	after, err := s.UIDs()
	if err != nil {
		t.Fatalf("uids after delete: %v", err)
	}
	if len(after) != 1 {
		t.Errorf("uids after delete = %v, want 1", after)
	}
}

func TestClientRejectsBadAuth(t *testing.T) {
	addr, _ := startTestServer(t)
	cfg, _ := testConfig(addr)
	if _, err := Dial(cfg, "wrong-password"); err == nil {
		t.Errorf("expected auth failure")
	}
}