package smtp

// Fake SMTP server (net/textproto) + tests for composition, auth and the
// plaintext gate. No real network beyond 127.0.0.1.

import (
	"bufio"
	"encoding/base64"
	"net"
	"strings"
	"testing"

	"mail/backend/internal/domain"
)

// fakeSMTP is a minimal single-purpose SMTP server capturing the message.
type fakeSMTP struct {
	ln       net.Listener
	mu       authMsg
	authMech string // mechanisms advertised in EHLO
	wantUser string
	wantPass string
}

type authMsg struct {
	msg      string
	user     string
	authUsed string
	quit     bool
}

func startFakeSMTP(t *testing.T, authMechs, user, pass string) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, authMech: authMechs, wantUser: user, wantPass: pass}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		f.serve(conn)
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeSMTP) Addr() string { return f.ln.Addr().String() }

func (f *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	st := &authMsg{}
	defer func() { f.mu = *st }()
	r := bufio.NewReader(conn)
	write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	write("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			write("250-fake")
			write("250-AUTH " + f.authMech)
			write("250 8BITMIME")
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			st.authUsed = "PLAIN"
			b64 := strings.TrimSpace(strings.TrimPrefix(line, "AUTH PLAIN "))
			raw, derr := base64.StdEncoding.DecodeString(b64)
			if derr != nil {
				write("535 bad base64")
				continue
			}
			parts := strings.SplitN(string(raw), "\x00", 3)
			if len(parts) != 3 || parts[1] != f.wantUser || parts[2] != f.wantPass {
				write("535 auth failed")
				continue
			}
			st.user = parts[1]
			write("235 ok")
		case strings.HasPrefix(upper, "AUTH LOGIN"):
			st.authUsed = "LOGIN"
			write("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			u, _ := r.ReadString('\n')
			ub, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(u))
			write("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			p, _ := r.ReadString('\n')
			pb, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(p))
			if string(ub) != f.wantUser || string(pb) != f.wantPass {
				write("535 auth failed")
				continue
			}
			st.user = string(ub)
			write("235 ok")
		case strings.HasPrefix(upper, "AUTH"):
			write("504 mechanism not supported")
		case strings.HasPrefix(upper, "MAIL FROM"):
			write("250 ok")
		case strings.HasPrefix(upper, "RCPT TO"):
			write("250 ok")
		case strings.HasPrefix(upper, "DATA"):
			write("354 go ahead")
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
				b.WriteString(dl + "\n")
			}
			st.msg = b.String()
			write("250 accepted")
		case strings.HasPrefix(upper, "QUIT"):
			st.quit = true
			write("221 bye")
			return
		case strings.HasPrefix(upper, "RSET"), strings.HasPrefix(upper, "NOOP"):
			write("250 ok")
		default:
			write("250 ok")
		}
	}
}

func TestSendMessagePlainCapturesMessage(t *testing.T) {
	f := startFakeSMTP(t, "PLAIN LOGIN", "luca@x.dev", "secret")
	host, portRaw, _ := net.SplitHostPort(f.Addr())
	port := 0
	for _, c := range portRaw {
		port = port*10 + int(c-'0')
	}
	cfg := Config{Host: host, Port: port, Security: SecurityPlain, Username: "luca@x.dev"}

	msg := domain.ComposeMessage{
		To:      []domain.Recipient{{Name: "Anna", Email: "anna@acme.dev"}},
		CC:      []domain.Recipient{{Email: "bo@acme.dev"}},
		BCC:     []domain.Recipient{{Email: "hidden@acme.dev"}},
		Subject: "Ciao è ültima",
		Body:    "prima riga\nseconda con àccento",
	}
	raw, err := SendMessage(cfg, "secret", true,
		domain.Recipient{Name: "Luca", Email: "luca@x.dev"}, msg)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	got := f.mu.msg
	if got == "" {
		t.Fatal("server did not capture a message")
	}
	if f.mu.authUsed != "PLAIN" || f.mu.user != "luca@x.dev" {
		t.Fatalf("auth: used=%s user=%s", f.mu.authUsed, f.mu.user)
	}
	if !f.mu.quit {
		t.Fatal("client did not QUIT")
	}
	for _, want := range []string{
		"From: \"Luca\" <luca@x.dev>",
		"To: \"Anna\" <anna@acme.dev>",
		"Cc: <bo@acme.dev>",
		"Message-ID: <",
		"@x.dev>",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=\"utf-8\"",
		"Content-Transfer-Encoding: quoted-printable",
		"Date: ",
	} {
		if !strings.Contains(got, want) && !strings.Contains(string(raw), want) {
			t.Errorf("message missing %q\n---\n%s", want, got)
		}
	}
	if strings.Contains(got, "Bcc:") || strings.Contains(got, "hidden@acme.dev") {
		t.Errorf("BCC leaked into headers:\n%s", got)
	}
	if !strings.Contains(got, "Subject: =?utf-8?q?") {
		t.Errorf("subject not RFC 2047 encoded:\n%s", got)
	}
	if !strings.Contains(got, "prima riga") || !strings.Contains(got, "=C3=A0ccento") {
		t.Errorf("body not quoted-printable encoded:\n%s", got)
	}
}

func TestSendMessageAuthLoginFallback(t *testing.T) {
	// server only offers LOGIN: the client must fall back to LOGIN
	f := startFakeSMTP(t, "LOGIN", "luca@x.dev", "secret")
	host, portRaw, _ := net.SplitHostPort(f.Addr())
	port := 0
	for _, c := range portRaw {
		port = port*10 + int(c-'0')
	}
	cfg := Config{Host: host, Port: port, Security: SecurityPlain, Username: "luca@x.dev"}
	_, err := SendMessage(cfg, "secret", true, domain.Recipient{Email: "luca@x.dev"},
		domain.ComposeMessage{To: []domain.Recipient{{Email: "a@b.dev"}}, Subject: "s", Body: "b"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if f.mu.authUsed != "LOGIN" {
		t.Fatalf("auth used = %s, want LOGIN", f.mu.authUsed)
	}
}

func TestSendMessageAuthMechanismUnsupported(t *testing.T) {
	f := startFakeSMTP(t, "CRAM-MD5", "luca@x.dev", "secret")
	host, portRaw, _ := net.SplitHostPort(f.Addr())
	port := 0
	for _, c := range portRaw {
		port = port*10 + int(c-'0')
	}
	cfg := Config{Host: host, Port: port, Security: SecurityPlain, Username: "luca@x.dev"}
	_, err := SendMessage(cfg, "secret", true, domain.Recipient{Email: "luca@x.dev"},
		domain.ComposeMessage{To: []domain.Recipient{{Email: "a@b.dev"}}})
	if err == nil || !strings.Contains(err.Error(), "meccanismo") {
		t.Fatalf("want unsupported mechanism error, got %v", err)
	}
}

func TestComposeThreadingHeaders(t *testing.T) {
	raw := string(Compose(domain.Recipient{Email: "luca@x.dev"}, domain.ComposeMessage{
		To:         []domain.Recipient{{Email: "anna@acme.dev"}},
		Subject:    "Re: Progetto",
		Body:       "ok",
		InReplyTo:  "<orig@acme.dev>",
		References: []string{"<first@acme.dev>", "<orig@acme.dev>"},
	}))
	if !strings.Contains(raw, "In-Reply-To: <orig@acme.dev>") {
		t.Errorf("missing In-Reply-To:\n%s", raw)
	}
	if !strings.Contains(raw, "References: <first@acme.dev> <orig@acme.dev>") {
		t.Errorf("missing References:\n%s", raw)
	}
}

func TestComposeMultipartAlternative(t *testing.T) {
	raw := string(Compose(domain.Recipient{Email: "luca@x.dev"}, domain.ComposeMessage{
		To:       []domain.Recipient{{Email: "anna@acme.dev"}},
		Subject:  "Re: Progetto",
		Body:     "testo con àccento\n> citazione",
		HTMLBody: `<p>testo con àccento</p><blockquote type="cite"><p>originale <b>html</b></p></blockquote>`,
	}))
	if !strings.Contains(raw, "Content-Type: multipart/alternative; boundary=\"=_mail_") {
		t.Errorf("missing multipart/alternative with generated boundary:\n%s", raw)
	}
	for _, want := range []string{
		"Content-Type: text/plain; charset=\"utf-8\"",
		"Content-Type: text/html; charset=\"utf-8\"",
		"testo con =C3=A0ccento",     // text part, qp-encoded
		"testo con =C3=A0ccento</p>", // html part, qp-encoded
		"--=_mail_",                  // boundary delimiters
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("multipart message missing %q\n---\n%s", want, raw)
		}
	}
	if strings.Count(raw, "--=_mail_") < 3 {
		t.Errorf("expected 3 boundary lines (open, second, close):\n%s", raw)
	}
	// the html part must carry the original markup verbatim (only qp-encoded)
	if !strings.Contains(raw, "blockquote type=3D\"cite\"") {
		t.Errorf("html part lost the blockquote markup:\n%s", raw)
	}
	// plain-only messages must stay text/plain (single part)
	plain := string(Compose(domain.Recipient{Email: "luca@x.dev"}, domain.ComposeMessage{
		To: []domain.Recipient{{Email: "a@b.dev"}}, Body: "ciao",
	}))
	if strings.Contains(plain, "multipart/") {
		t.Errorf("plain message must not be multipart:\n%s", plain)
	}
}

func TestValidateConfigPlaintextGate(t *testing.T) {
	cfg := Config{Host: "smtp.x.dev", Port: 465, Security: SecurityPlain, Username: "u"}
	if err := ValidateConfig(cfg, false); err == nil {
		t.Fatal("plaintext must be refused without opt-in")
	}
	if err := ValidateConfig(cfg, true); err != nil {
		t.Fatalf("plaintext with opt-in: %v", err)
	}
	cfg.Security = "bogus"
	if err := ValidateConfig(cfg, true); err == nil {
		t.Fatal("unknown security mode must be refused")
	}
}

func TestDefaultFor(t *testing.T) {
	d := DefaultFor("imap.posta.example", "luca@posta.example")
	if d.Host != "smtp.posta.example" || d.Port != 465 || d.Security != SecurityTLS {
		t.Fatalf("unexpected default: %+v", d)
	}
	if d.Username != "luca@posta.example" {
		t.Fatalf("username not carried over: %+v", d)
	}
	// a bare domain must not become smtp.<tld> (credentials to a stranger)
	if d := DefaultFor("posteo.de", "u"); d.Host != "posteo.de" {
		t.Fatalf("bare domain rewritten: %+v", d)
	}
}
