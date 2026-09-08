package ipc

// Push test: a message appended on the server while the watcher idles on
// INBOX must surface as message.created within seconds (IDLE) and land in
// the local thread list.

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	goimap "github.com/emersion/go-imap/v2"
)

// mail3 carries a Message-Id so a later reply could thread to it.
const mail3 = "Subject: Progetto X\r\nFrom: Anna <anna@acme.dev>\r\nTo: luca@x.dev\r\n" +
	"Date: Wed, 27 Aug 2026 09:00:00 +0200\r\nMessage-Id: <orig@x.dev>\r\n\r\nStato del progetto."

func TestIdlePush(t *testing.T) {
	env := startEnv(t)
	host, portRaw, _ := net.SplitHostPort(env.addr)
	port, _ := strconv.Atoi(portRaw)

	ok, data, errMsg := env.call(t, "i0", "accounts.add", map[string]any{
		"name": "Luca", "email": "luca@x.dev",
		"imap":          map[string]any{"host": host, "port": port, "security": "plain", "username": "luca"},
		"secret":        secret,
		"allowInsecure": true,
	})
	if !ok {
		t.Fatalf("accounts.add: %s", errMsg)
	}
	var acc struct {
		ID      string `json:"id"`
		Folders []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"folders"`
	}
	if err := json.Unmarshal(data, &acc); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	var inboxID string
	for _, f := range acc.Folders {
		if strings.EqualFold(f.Name, "INBOX") {
			inboxID = f.ID
		}
	}

	ok, _, errMsg = env.call(t, "i1", "sync.start", map[string]any{"accountId": acc.ID, "secret": secret})
	if !ok {
		t.Fatalf("sync.start: %s", errMsg)
	}
	env.waitEvent(t, "sync.completed", 15*time.Second)

	// give the watcher a moment to dial its session and enter IDLE on INBOX
	time.Sleep(500 * time.Millisecond)

	// new mail arrives server-side while the watcher idles on INBOX
	r := strings.NewReader(mail3)
	if _, err := env.usr.Append("INBOX", &testLiteral{r: r}, &goimap.AppendOptions{}); err != nil {
		t.Fatalf("server append: %v", err)
	}

	// push: the watcher ends IDLE, syncs incrementally, emits message.created
	start := time.Now()
	env.waitEvent(t, "message.created", 10*time.Second)
	t.Logf("push delivered in %s", time.Since(start))

	ok, data, errMsg = env.call(t, "i2", "threads.list", map[string]any{"folderId": inboxID})
	if !ok {
		t.Fatalf("threads.list: %s", errMsg)
	}
	var threads []struct {
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal(data, &threads); err != nil {
		t.Fatalf("decode threads: %v", err)
	}
	if len(threads) != 3 {
		t.Fatalf("threads = %d, want 3 (%s)", len(threads), data)
	}
}
