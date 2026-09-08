package smtp

// XOAUTH2 SMTP auth (M4): same wire format as IMAP XOAUTH2.

import (
	"testing"
)

func TestOauth2AuthStart(t *testing.T) {
	a := oauth2Auth{username: "luca@gmail.com", token: "ya29.abc"}
	mech, resp, err := a.Start(nil)
	if err != nil {
		t.Fatal(err)
	}
	if mech != "XOAUTH2" {
		t.Fatalf("mechanism = %q, want XOAUTH2", mech)
	}
	want := "user=luca@gmail.com\x01auth=Bearer ya29.abc\x01\x01"
	if string(resp) != want {
		t.Fatalf("response = %q, want %q", resp, want)
	}
}

func TestOauth2AuthRejectsChallenge(t *testing.T) {
	a := oauth2Auth{username: "u", token: "t"}
	if _, err := a.Next([]byte(""), true); err == nil {
		t.Fatal("server challenge should be rejected")
	}
}
