package ipc

// End-to-end send flow: SMTP delivery (fake server), APPEND into the server
// Sent mailbox, local persistence with thread resolution.

import (
	"bufio"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSMTP captures delivered messages (plaintext, AUTH PLAIN only).
type fakeSMTP struct {
	ln         net.Listener
	msg        string
	deliveries atomic.Int32
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(t, conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeSMTP) serve(t *testing.T, conn net.Conn) {
	defer conn.Close()
	w := bufio.NewWriter(conn)
	r := bufio.NewReader(conn)
	say := func(s string) {
		_, _ = w.WriteString(s + "\r\n")
		_ = w.Flush()
		t.Logf("fakeSMTP S> %s", s)
	}
	say("220 fake ESMTP")
	for {
		line, rerr := r.ReadString('\n')
		if rerr != nil {
			t.Logf("fakeSMTP read err: %v", rerr)
			return
		}
		t.Logf("fakeSMTP C> %s", strings.TrimSpace(line))
		up := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			say("250-fake")
			say("250-AUTH PLAIN")
			say("250 8BITMIME")
		case strings.HasPrefix(up, "AUTH PLAIN"):
			say("235 ok")
		case strings.HasPrefix(up, "MAIL FROM"), strings.HasPrefix(up, "RCPT TO"):
			say("250 ok")
		case strings.HasPrefix(up, "DATA"):
			say("354 go ahead")
			var b strings.Builder
			for {
				dl, derr := r.ReadString('\n')
				if derr != nil {
					return
				}
				dl = strings.TrimRight(dl, "\r\n")
				if dl == "." {
					break
				}
				b.WriteString(dl + "\r\n")
			}
			f.msg += b.String()
			f.deliveries.Add(1)
			say("250 accepted")
		case strings.HasPrefix(up, "QUIT"):
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (f *fakeSMTP) hostPort() (string, int) {
	host, portRaw, _ := net.SplitHostPort(f.ln.Addr().String())
	port, _ := strconv.Atoi(portRaw)
	return host, port
}

func TestSendFlow(t *testing.T) {
	f := startFakeSMTP(t)
	smtpHost, smtpPort := f.hostPort()

	env := startEnv(t)
	host, portRaw, _ := net.SplitHostPort(env.addr)
	port, _ := strconv.Atoi(portRaw)

	ok, data, errMsg := env.call(t, "s0", "accounts.add", map[string]any{
		"name": "Luca", "email": "luca@x.dev",
		"imap":          map[string]any{"host": host, "port": port, "security": "plain", "username": "luca"},
		"smtp":          map[string]any{"host": smtpHost, "port": smtpPort, "security": "plain", "username": "luca@x.dev"},
		"secret":        secret,
		"allowInsecure": true,
	})
	if !ok {
		t.Fatalf("accounts.add: %s", errMsg)
	}
	var acc struct {
		ID   string `json:"id"`
		Smtp *struct {
			Host string `json:"host"`
		} `json:"smtp"`
		Folders []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"folders"`
	}
	if err := json.Unmarshal(data, &acc); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	if acc.Smtp == nil || acc.Smtp.Host != smtpHost {
		t.Fatalf("smtp config not persisted: %+v", acc.Smtp)
	}
	var sentID string
	for _, fl := range acc.Folders {
		if strings.EqualFold(fl.Name, "Sent") {
			sentID = fl.ID
		}
	}
	if sentID == "" {
		t.Fatalf("Sent folder missing: %+v", acc.Folders)
	}

	if ok, _, errMsg = env.call(t, "s1", "sync.start", map[string]any{"accountId": acc.ID, "secret": secret}); !ok {
		t.Fatalf("sync.start: %s", errMsg)
	}
	env.waitEvent(t, "sync.completed", 15*time.Second)

	// reply send: SMTP delivery + threading headers on the wire
	ok, data, errMsg = env.call(t, "s2", "send", map[string]any{
		"accountId": acc.ID,
		"secret":    secret,
		"message": map[string]any{
			"to":         []map[string]any{{"name": "Anna", "email": "anna@acme.dev"}},
			"subject":    "Re: Progetto X",
			"body":       "Ecco l'aggiornamento.",
			"inReplyTo":  "orig@x.dev",
			"references": []string{"orig@x.dev"},
		},
	})
	if !ok {
		t.Fatalf("send: %s", errMsg)
	}
	var sent struct {
		Sent      bool   `json:"sent"`
		MessageID string `json:"messageId"`
		ThreadID  string `json:"threadId"`
	}
	if err := json.Unmarshal(data, &sent); err != nil {
		t.Fatalf("decode send response: %v", err)
	}
	if !sent.Sent || sent.MessageID == "" {
		t.Fatalf("unexpected send response: %s", data)
	}
	if !strings.Contains(f.msg, "In-Reply-To: <orig@x.dev>") {
		t.Errorf("SMTP message missing In-Reply-To:\n%s", f.msg)
	}
	if !strings.Contains(f.msg, "References: <orig@x.dev>") {
		t.Errorf("SMTP message missing References:\n%s", f.msg)
	}
	if !strings.Contains(f.msg, "Subject: Re: Progetto X") {
		t.Errorf("SMTP message missing subject:\n%s", f.msg)
	}
	if strings.Contains(f.msg, secret) {
		t.Errorf("secret leaked in message:\n%s", f.msg)
	}

	// local persistence: the sent message lives in the Sent folder
	ok, data, errMsg = env.call(t, "s3", "message.get", map[string]any{"messageId": sent.MessageID})
	if !ok {
		t.Fatalf("message.get: %s", errMsg)
	}
	var got struct {
		FolderID string `json:"folderId"`
		Subject  string `json:"subject"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if got.FolderID != sentID {
		t.Errorf("sent message folder = %s, want %s", got.FolderID, sentID)
	}

	// fresh message (no threading): second delivery on the wire
	ok, data, errMsg = env.call(t, "s4", "send", map[string]any{
		"accountId": acc.ID,
		"secret":    secret,
		"message": map[string]any{
			"to":      []map[string]any{{"email": "bo@x.dev"}},
			"subject": "Nuovo filo",
			"body":    "Ciao",
		},
	})
	if !ok {
		t.Fatalf("second send: %s", errMsg)
	}
	if strings.Count(f.msg, "Message-ID:") != 2 {
		t.Errorf("SMTP server should hold two messages, got:\n%s", f.msg)
	}
}

// A delivered message must never come back as an error: the outbox would
// retry it and the recipient would get it twice.
func TestSendSucceedsWhenLocalPersistenceFails(t *testing.T) {
	f := startFakeSMTP(t)
	smtpHost, smtpPort := f.hostPort()

	env := startEnv(t)
	host, portRaw, _ := net.SplitHostPort(env.addr)
	port, _ := strconv.Atoi(portRaw)

	ok, data, errMsg := env.call(t, "p0", "accounts.add", map[string]any{
		"name": "Luca", "email": "luca@x.dev",
		"imap":          map[string]any{"host": host, "port": port, "security": "plain", "username": "luca"},
		"smtp":          map[string]any{"host": smtpHost, "port": smtpPort, "security": "plain", "username": "luca@x.dev"},
		"secret":        secret,
		"allowInsecure": true,
	})
	if !ok {
		t.Fatalf("accounts.add: %s", errMsg)
	}
	var acc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &acc); err != nil {
		t.Fatalf("decode account: %v", err)
	}

	// every insert into messages now fails
	if _, err := env.app.DB.Exec(`CREATE TRIGGER fail_insert BEFORE INSERT ON messages
		BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}

	ok, data, errMsg = env.call(t, "p1", "send", map[string]any{
		"accountId": acc.ID,
		"secret":    secret,
		"message": map[string]any{
			"to":      []map[string]any{{"email": "bo@x.dev"}},
			"subject": "Consegnata",
			"body":    "Ciao",
		},
	})
	if !ok {
		t.Fatalf("send must report success once delivered, got error: %s", errMsg)
	}
	var sent struct {
		Sent      bool   `json:"sent"`
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(data, &sent); err != nil {
		t.Fatal(err)
	}
	if !sent.Sent || sent.MessageID != "" {
		t.Fatalf("unexpected response: %s", data)
	}
	if !strings.Contains(f.msg, "Subject: Consegnata") {
		t.Errorf("message was not delivered over SMTP:\n%s", f.msg)
	}
}

// A retry of the same compose attempt (same clientId) must not deliver the
// mail twice, whether the first send is finished or not.
func TestSendWithSameClientIDDeliversOnce(t *testing.T) {
	f := startFakeSMTP(t)
	smtpHost, smtpPort := f.hostPort()

	env := startEnv(t)
	host, portRaw, _ := net.SplitHostPort(env.addr)
	port, _ := strconv.Atoi(portRaw)

	ok, data, errMsg := env.call(t, "d0", "accounts.add", map[string]any{
		"name": "Luca", "email": "luca@x.dev",
		"imap":          map[string]any{"host": host, "port": port, "security": "plain", "username": "luca"},
		"smtp":          map[string]any{"host": smtpHost, "port": smtpPort, "security": "plain", "username": "luca@x.dev"},
		"secret":        secret,
		"allowInsecure": true,
	})
	if !ok {
		t.Fatalf("accounts.add: %s", errMsg)
	}
	var acc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &acc); err != nil {
		t.Fatal(err)
	}

	send := func(id, clientID string) {
		ok, _, errMsg := env.call(t, id, "send", map[string]any{
			"accountId": acc.ID,
			"secret":    secret,
			"message": map[string]any{
				"to":       []map[string]any{{"email": "bo@x.dev"}},
				"subject":  "Una volta sola",
				"body":     "Ciao",
				"clientId": clientID,
			},
		})
		if !ok {
			t.Fatalf("send %s: %s", id, errMsg)
		}
	}
	send("d1", "attempt-1")
	send("d2", "attempt-1") // the user clicks Send again after a timeout
	if got := f.deliveries.Load(); got != 1 {
		t.Fatalf("deliveries = %d, want 1", got)
	}
	send("d3", "attempt-2") // a new compose attempt is a new mail
	if got := f.deliveries.Load(); got != 2 {
		t.Fatalf("deliveries = %d, want 2", got)
	}
}
