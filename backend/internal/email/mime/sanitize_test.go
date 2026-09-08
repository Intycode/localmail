package mime

// Anti-tracking tests (M6): pixels removed, remote images counted, link
// trackers stripped while functional parameters survive.

import (
	"strings"
	"testing"
)

func TestSanitizeRemovesTinyPixels(t *testing.T) {
	in := "<p>Ciao</p>" +
		"<img src=\"https://t.example.com/pixel?id=1\" width=\"1\" height=\"1\">" +
		"<img src=\"https://t.example.com/zero.gif\" width=\"0\" height=\"0\">" +
		"<img src=\"https://t.example.com/hidden.png\" style=\"display:none\">"
	out, remote := SanitizeHTML(in)
	if strings.Contains(out, "pixel?id=1") || strings.Contains(out, "zero.gif") || strings.Contains(out, "hidden.png") {
		t.Fatalf("pixels survived: %s", out)
	}
	if remote != 0 {
		t.Fatalf("remote = %d, want 0", remote)
	}
	if !strings.Contains(out, "Ciao") {
		t.Fatalf("content lost: %s", out)
	}
}

func TestSanitizeCountsRealRemoteImages(t *testing.T) {
	in := "<img src=\"https://cdn.example.com/hero.jpg\" width=\"600\">" +
		"<img src=\"cid:inline-logo@example.com\">" +
		"<img src=\"data:image/png;base64,iVBOR\">"
	out, remote := SanitizeHTML(in)
	if remote != 1 {
		t.Fatalf("remote = %d, want 1", remote)
	}
	if !strings.Contains(out, "hero.jpg") {
		t.Fatalf("content image removed: %s", out)
	}
	if !strings.Contains(out, "cid:inline-logo@example.com") {
		t.Fatalf("inline image removed: %s", out)
	}
}

func TestSanitizeDropsTrackerHosts(t *testing.T) {
	in := "<img src=\"https://u5233726.ct.sendgrid.net/wf/open?upn=abc\" width=\"600\">"
	out, remote := SanitizeHTML(in)
	if remote != 0 || strings.Contains(out, "sendgrid") {
		t.Fatalf("tracker host survived: remote=%d out=%s", remote, out)
	}
}

func TestSanitizeStripsLinkTrackers(t *testing.T) {
	in := "<a href=\"https://shop.example.com/prod?id=42&utm_source=news&utm_campaign=spring&fbclid=AbC123\">Offerta</a>"
	out, _ := SanitizeHTML(in)
	if strings.Contains(out, "utm_") || strings.Contains(out, "fbclid") {
		t.Fatalf("tracking params survived: %s", out)
	}
	if !strings.Contains(out, "id=42") {
		t.Fatalf("functional param lost: %s", out)
	}
	if !strings.Contains(out, "Offerta") {
		t.Fatalf("text lost: %s", out)
	}
}

func TestSanitizeKeepsUnsubscribeToken(t *testing.T) {
	in := "<a href=\"https://news.example.com/unsub?token=SECRET&src=email\">Annulla iscrizione</a>"
	out, _ := SanitizeHTML(in)
	if !strings.Contains(out, "token=SECRET") {
		t.Fatalf("unsubscribe token lost: %s", out)
	}
}

func TestSanitizeRemovesScriptAndBase(t *testing.T) {
	in := "<script>alert(1)</script><base href=\"https://evil.example.com/\"><p>x</p>"
	out, _ := SanitizeHTML(in)
	if strings.Contains(strings.ToLower(out), "script") || strings.Contains(strings.ToLower(out), "base href") {
		t.Fatalf("dangerous tag survived: %s", out)
	}
}

func TestSanitizeRelativeHrefUntouched(t *testing.T) {
	in := "<a href=\"/promo?utm_campaign=x\">link</a>"
	out, _ := SanitizeHTML(in)
	// relative URLs bypass the query rewrite (only http(s) is rewritten) but
	// must survive intact
	if !strings.Contains(out, "/promo?utm_campaign=x") {
		t.Fatalf("relative href changed: %s", out)
	}
}

func TestParseIntegratesSanitizer(t *testing.T) {
	raw := []byte("From: Newsletter <news@shop.example.com>\r\n" +
		"To: Luca <luca@example.com>\r\n" +
		"Subject: Promo\r\n" +
		"Date: Mon, 23 Jun 2025 10:00:00 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<p>Ecco le offerte</p>" +
		"<img src=\"https://t.shop.example.com/pixel\" width=\"1\" height=\"1\">" +
		"<img src=\"https://cdn.shop.example.com/banner.jpg\" width=\"600\">" +
		"<a href=\"https://shop.example.com/sale?utm_medium=email&id=7\">Vedi</a>\r\n")
	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.RemoteImages != 1 {
		t.Fatalf("RemoteImages = %d, want 1", m.RemoteImages)
	}
	if strings.Contains(m.Body, "width=\"1\"") {
		t.Fatalf("pixel survived in stored body: %s", m.Body)
	}
	if strings.Contains(m.Body, "utm_medium") {
		t.Fatalf("link tracker survived in stored body: %s", m.Body)
	}
	if !strings.Contains(m.Body, "banner.jpg") || !strings.Contains(m.Body, "id=7") {
		t.Fatalf("legit content lost: %s", m.Body)
	}
}
