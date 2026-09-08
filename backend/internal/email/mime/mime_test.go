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
