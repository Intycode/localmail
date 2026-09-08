package imap

// XOAUTH2 SASL correctness (M4): the initial response must be exactly
// "user=<u>\x01auth=Bearer <t>\x01\x01" with mechanism name XOAUTH2, and the
// client must reject any server challenge.

import (
	"strings"
	"testing"
)

func TestXoauth2ClientStart(t *testing.T) {
	c := xoauth2Client{user: "luca@gmail.com", token: "ya29.abc"}
	mech, resp, err := c.Start()
	if err != nil {
		t.Fatal(err)
	}
	if mech != "XOAUTH2" {
		t.Fatalf("mechanism = %q, want XOAUTH2", mech)
	}
	want := "user=luca@gmail.com\x01auth=Bearer ya29.abc\x01\x01"
	if string(resp) != want {
		t.Fatalf("response = %q, want %q", strings.ReplaceAll(string(resp), "\x01", "^A"), strings.ReplaceAll(want, "\x01", "^A"))
	}
}

func TestXoauth2ClientRejectsChallenge(t *testing.T) {
	c := xoauth2Client{user: "u", token: "t"}
	if _, err := c.Next([]byte("")); err == nil {
		t.Fatal("server challenge should be rejected")
	}
}
