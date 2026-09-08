package ipc

// End-to-end protocol test: a real in-process IMAP server behind the engine.
// Covers accounts.add (folder discovery + plaintext gate), sync.start
// (persistence), threads.list, wrong-secret handling, accounts.remove (purge)
// and the anti-leak guarantee (no secret ever on the wire).

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	goimap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"mail/backend/internal/account"
	"mail/backend/internal/auth"
	"mail/backend/internal/database"
	"mail/backend/internal/events"
	"mail/backend/internal/message"
	"mail/backend/internal/provider"
	"mail/backend/internal/search"
	mailsync "mail/backend/internal/sync"
	"mail/backend/internal/thread"
)

const secret = "e2e-app-password"

const mail1 = "Subject: Benvenuto\r\nFrom: Team <team@x.dev>\r\nTo: luca@x.dev\r\nDate: Mon, 25 Aug 2026 09:30:00 +0200\r\n\r\nPrimo messaggio di prova."
const mail2 = "Subject: Report mensile\r\nFrom: Bo <bo@x.dev>\r\nTo: luca@x.dev\r\nDate: Tue, 26 Aug 2026 10:00:00 +0200\r\n\r\nAllegato il report."

// lineWriter forwards every written line to a channel.
type lineWriter struct {
	mu    sync.Mutex
	lines chan string
}

func newLineWriter() *lineWriter { return &lineWriter{lines: make(chan string, 256)} }

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	sc := bufio.NewScanner(strings.NewReader(string(p)))
	for sc.Scan() {
		w.lines <- sc.Text()
	}
	return len(p), nil
}

type testEnv struct {
	app  *App
	out  *lineWriter
	in   *io.PipeWriter
	addr string
	usr  *imapmemserver.User
}

func startEnv(t *testing.T) *testEnv {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mem := imapmemserver.New()
	usr := imapmemserver.NewUser("luca", secret)
	mem.AddUser(usr)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), &imapserver.GreetingData{}, nil
		},
		InsecureAuth: true,
	})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { srv.Close() })

	for _, name := range []string{"INBOX", "Sent", "Archive", "Trash"} {
		if err := usr.Create(name, nil); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	for _, m := range []string{mail1, mail2} {
		r := strings.NewReader(m)
		if _, err := usr.Append("INBOX", &testLiteral{r: r}, &goimap.AppendOptions{}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	bus := events.New()
	engine := mailsync.New(db, provider.NewMock(), bus)
	app := &App{
		DB:       db,
		Accounts: account.New(db),
		Messages: message.New(db),
		Threads:  thread.New(db),
		Search:   search.New(db),
		Events:   bus,
		Engine:   engine,
		Secrets:  auth.NewRuntimeSecrets(),
		Watchers: mailsync.NewSupervisor(engine),
	}
	out := newLineWriter()
	pr, pw := io.Pipe()
	go func() { _ = Serve(pr, out, app) }()
	t.Cleanup(func() { _ = pw.CloseWithError(nil) })
	t.Cleanup(func() { app.Watchers.StopAll() })
	return &testEnv{app: app, out: out, in: pw, addr: ln.Addr().String(), usr: usr}
}

// waitEvent consumes lines until the given event kind arrives.
func (e *testEnv) waitEvent(t *testing.T, kind string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case line := <-e.out.lines:
			var ev struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				continue
			}
			if ev.Kind == kind {
				return
			}
		case <-deadline:
			t.Fatalf("timeout waiting event %s", kind)
		}
	}
}

type testLiteral struct{ r *strings.Reader }

func (l *testLiteral) Read(p []byte) (int, error) { return l.r.Read(p) }
func (l *testLiteral) Size() int64                { return int64(l.r.Len()) }

// call sends one request and waits for the matching response.
func (e *testEnv) call(t *testing.T, id, op string, params any) (bool, json.RawMessage, string) {
	t.Helper()
	req := map[string]any{"id": id, "op": op}
	if params != nil {
		req["params"] = params
	}
	b, _ := json.Marshal(req)
	if _, err := e.in.Write(append(b, '\n')); err != nil {
		t.Fatalf("write request: %v", err)
	}
	deadline := time.After(15 * time.Second)
	for {
		select {
		case line := <-e.out.lines:
			var resp struct {
				ID    string          `json:"id"`
				OK    bool            `json:"ok"`
				Data  json.RawMessage `json:"data"`
				Error string          `json:"error"`
			}
			if err := json.Unmarshal([]byte(line), &resp); err != nil {
				continue // event line, not a response
			}
			if resp.ID == id {
				return resp.OK, resp.Data, resp.Error
			}
		case <-deadline:
			t.Fatalf("timeout waiting response for %s", op)
		}
	}
}

func TestProtocolFlow(t *testing.T) {
	env := startEnv(t)

	host, portRaw, _ := net.SplitHostPort(env.addr)
	port, _ := strconv.Atoi(portRaw)

	// 1. unknown op -> error, never a panic
	ok, _, errMsg := env.call(t, "r0", "nonexistent.op", nil)
	if ok || !strings.Contains(errMsg, "op sconosciuto") {
		t.Fatalf("unknown op: ok=%v err=%q", ok, errMsg)
	}

	// 2. plaintext is refused without the explicit opt-in flag
	ok, _, errMsg = env.call(t, "r1", "accounts.add", map[string]any{
		"name": "Luca", "email": "luca@x.dev",
		"imap": map[string]any{"host": host, "port": port, "security": "plain", "username": "luca"},
		"secret": secret,
	})
	if ok {
		t.Fatalf("plaintext add should be refused without allowInsecure")
	}
	if !strings.Contains(errMsg, "non cifrata") {
		t.Fatalf("plaintext error = %q", errMsg)
	}

	// 3. accounts.add with explicit opt-in -> folders discovered
	ok, data, errMsg := env.call(t, "r2", "accounts.add", map[string]any{
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
			Type string `json:"type"`
		} `json:"folders"`
	}
	if err := json.Unmarshal(data, &acc); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	if acc.ID == "" || len(acc.Folders) == 0 {
		t.Fatalf("account incomplete: %s", data)
	}
	var inboxID string
	for _, f := range acc.Folders {
		if strings.EqualFold(f.Name, "INBOX") {
			inboxID = f.ID
		}
	}
	if inboxID == "" {
		t.Fatalf("INBOX folder missing: %s", data)
	}

	// 4. duplicate add is refused
	ok, _, errMsg = env.call(t, "r3", "accounts.add", map[string]any{
		"name": "Luca", "email": "luca@x.dev",
		"imap":          map[string]any{"host": host, "port": port, "security": "plain", "username": "luca"},
		"secret":        secret,
		"allowInsecure": true,
	})
	if ok || !strings.Contains(errMsg, "già presente") {
		t.Fatalf("duplicate add: ok=%v err=%q", ok, errMsg)
	}

	// 5. sync.start -> messages persisted (async: response is immediate)
	ok, data, errMsg = env.call(t, "r4", "sync.start", map[string]any{"accountId": acc.ID, "secret": secret})
	if !ok {
		t.Fatalf("sync.start: %s", errMsg)
	}
	var started struct {
		Started bool `json:"started"`
	}
	if err := json.Unmarshal(data, &started); err != nil || !started.Started {
		t.Fatalf("sync.start response: %s", data)
	}
	env.waitEvent(t, "sync.completed", 15*time.Second)

	ok, data, errMsg = env.call(t, "r5", "threads.list", map[string]any{"folderId": inboxID})
	if !ok {
		t.Fatalf("threads.list: %s", errMsg)
	}
	var threads []struct {
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal(data, &threads); err != nil {
		t.Fatalf("decode threads: %v", err)
	}
	if len(threads) != 2 {
		t.Fatalf("threads = %d, want 2 (%s)", len(threads), data)
	}

	// 6. wrong secret -> readable auth error
	ok, _, errMsg = env.call(t, "r6", "sync.start", map[string]any{"accountId": acc.ID, "secret": "wrong-secret"})
	if ok || !strings.Contains(errMsg, "autenticazione") {
		t.Fatalf("sync with wrong secret: ok=%v err=%q", ok, errMsg)
	}

	// 7. remove purges the account
	ok, _, errMsg = env.call(t, "r7", "accounts.remove", map[string]any{"accountId": acc.ID})
	if !ok {
		t.Fatalf("accounts.remove: %s", errMsg)
	}
	ok, data, _ = env.call(t, "r8", "accounts.list", nil)
	if !ok || strings.Contains(string(data), "luca@x.dev") {
		t.Fatalf("account still listed after remove: %s", data)
	}
	ok, data, _ = env.call(t, "r9", "threads.list", map[string]any{"folderId": inboxID})
	if !ok || strings.Contains(string(data), "Benvenuto") {
		t.Fatalf("threads not purged: %s", data)
	}
}

// TestProtocolNoSecretLeak runs a full flow and asserts the secret never
// appears on the protocol output.
func TestProtocolNoSecretLeak(t *testing.T) {
	env := startEnv(t)

	host, portRaw, _ := net.SplitHostPort(env.addr)
	port, _ := strconv.Atoi(portRaw)

	_, _, _ = env.call(t, "a1", "accounts.add", map[string]any{
		"name": "Luca", "email": "luca@x.dev",
		"imap":          map[string]any{"host": host, "port": port, "security": "plain", "username": "luca"},
		"secret":        secret,
		"allowInsecure": true,
	})
	_, _, _ = env.call(t, "a2", "sync.start", map[string]any{"accountId": "", "secret": secret})

	// drain any pending lines and scan everything
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case line := <-env.out.lines:
			if strings.Contains(line, secret) {
				t.Fatalf("secret leaked on the wire: %s", line)
			}
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}