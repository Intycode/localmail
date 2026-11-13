package mime

import (
	"strings"
	"testing"
)

const raw = `Subject: Ciao mondo
From: Anna Bianchi <anna@acme.dev>
To: luca@example.com, Marco <marco@x.com>
Date: Mon, 25 Aug 2026 09:30:00 +0200

Questo e il corpo della mail.`

func TestParseHeadersAndBody(t *testing.T) {
	m, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Subject != "Ciao mondo" {
		t.Errorf("subject = %q", m.Subject)
	}
	if m.From.Name != "Anna Bianchi" || m.From.Email != "anna@acme.dev" {
		t.Errorf("from = %+v", m.From)
	}
	if len(m.To) != 2 {
		t.Errorf("to len = %d", len(m.To))
	}
	if !strings.Contains(m.Body, "Questo e il corpo") {
		t.Errorf("body = %q", m.Body)
	}
	if m.Date.IsZero() {
		t.Errorf("date not parsed")
	}
}

func TestContentType(t *testing.T) {
	if got := ContentType("text/plain; charset=utf-8"); got != "text/plain" {
		t.Errorf("ct = %q", got)
	}
}

// An unknown charset must not drop the message, nor the parts after it.
func TestParseUnknownCharsetKeepsMessageAndParts(t *testing.T) {
	raw := "Subject: Fattura\r\nFrom: a@b.c\r\nContent-Type: multipart/mixed; boundary=X\r\n\r\n" +
		"--X\r\nContent-Type: text/plain; charset=unknown-8bit\r\n\r\nciao\r\n" +
		"--X\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=f.pdf\r\n\r\n%PDF\r\n" +
		"--X--\r\n"
	m, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Subject != "Fattura" || len(m.Attachments) != 1 || !strings.Contains(m.BodyText, "ciao") {
		t.Fatalf("subject=%q atts=%d text=%q", m.Subject, len(m.Attachments), m.BodyText)
	}
	single := "Subject: Solo\r\nContent-Type: text/plain; charset=unknown-8bit\r\n\r\ntesto"
	if _, err := Parse([]byte(single)); err != nil {
		t.Fatalf("single-part unknown charset dropped: %v", err)
	}
}

func TestParseInlinesCIDImages(t *testing.T) {
	raw := "From: a@x.dev\r\nTo: b@x.dev\r\nSubject: logo\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/related; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" +
		`<p>Ciao</p><img src="cid:logo@x.dev" width="200"><img src="cid:missing@x.dev">` + "\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-Id: <logo@x.dev>\r\nContent-Transfer-Encoding: base64\r\n" +
		"Content-Disposition: inline; filename=logo.png\r\n\r\niVBORw0KGgo=\r\n--B--\r\n"
	m, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Body, "data:image/png;base64,iVBORw0KGgo=") {
		t.Fatalf("cid image not inlined: %s", m.Body)
	}
	if strings.Contains(m.Body, "cid:logo") {
		t.Fatalf("cid reference left: %s", m.Body)
	}
	if m.RemoteImages != 0 {
		t.Fatalf("inline image counted as remote: %d", m.RemoteImages)
	}
}
