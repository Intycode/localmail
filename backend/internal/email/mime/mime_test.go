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
