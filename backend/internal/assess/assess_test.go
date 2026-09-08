package assess

// Heuristic tests (M7): spam scoring stays high for classic spam and low for
// normal mail; phishing flags fire for masked links, IP/shortener/IDN
// destinations, brand impersonation, login bait and dangerous attachments.

import (
	"strings"
	"testing"

	"mail/backend/internal/domain"
)

func normalMail() *domain.Message {
	return &domain.Message{
		Subject:  "Progetto X: note della riunione",
		From:     domain.Recipient{Email: "anna@acme.dev"},
		BodyText: "Ciao Luca, in allegato le note. A domani.",
	}
}

func TestNormalMailScoresLow(t *testing.T) {
	r := Evaluate(normalMail())
	if r.IsSuspicious() {
		t.Fatalf("normal mail flagged: score=%v flags=%v", r.SpamScore, r.Phishing)
	}
}

func TestSpammyMailScoresHigh(t *testing.T) {
	m := &domain.Message{
		Subject:  "CONGRATULAZIONI!!! HAI VINTO UNA LOTTERIA",
		From:     domain.Recipient{Email: "winner@lucky.top"},
		BodyText: "Clicca qui per ritirare il premio istantaneo. Offerta limitata, ora o mai!",
	}
	r := Evaluate(m)
	if !r.IsSpam() {
		t.Fatalf("spam score too low: %v", r.SpamScore)
	}
}

func TestKeywordCapKeepsNormalishMailBelowSpam(t *testing.T) {
	// many weak signals, but a legitimate-ish newsletter: must not cross the
	// spam threshold from keywords alone
	m := &domain.Message{
		Subject:  "Newsletter: offerta limitata, solo oggi, affrettati, clicca qui",
		From:     domain.Recipient{Email: "news@shop.dev"},
		BodyText: "Clicca qui. Ultima possibilita. Offerta limitata. Solo oggi. Affrettati.",
	}
	r := Evaluate(m)
	if r.IsSpam() {
		t.Fatalf("keyword cap failed: %v", r.SpamScore)
	}
	if !r.IsSuspicious() {
		t.Fatalf("expected at least suspicious: %v", r.SpamScore)
	}
}

func TestMaskedLinkFlagged(t *testing.T) {
	m := normalMail()
	m.Body = "<a href=\"https://secure.login-example.ru/now\">https://www.poste.it</a>"
	r := Evaluate(m)
	found := false
	for _, f := range r.Phishing {
		if strings.Contains(f, "Link mascherato") {
			found = true
		}
	}
	if !found {
		t.Fatalf("masked link not flagged: %v", r.Phishing)
	}
}

func TestIPAndShortenerFlagged(t *testing.T) {
	m := normalMail()
	m.Body = "<a href=\"http://192.168.13.37/pay\">paga</a>" +
		"<a href=\"https://bit.ly/3xYz\">clicca</a>"
	r := Evaluate(m)
	joined := strings.Join(r.Phishing, " | ")
	if !strings.Contains(joined, "indirizzo IP") {
		t.Fatalf("IP link not flagged: %v", r.Phishing)
	}
	if !strings.Contains(joined, "accorciato") {
		t.Fatalf("shortener not flagged: %v", r.Phishing)
	}
}

func TestIDNHomoglyphFlagged(t *testing.T) {
	m := normalMail()
	m.Body = "<a href=\"https://xn--pypal-4ve.com/login\">paypal</a>"
	r := Evaluate(m)
	joined := strings.Join(r.Phishing, " | ")
	if !strings.Contains(joined, "IDN") {
		t.Fatalf("IDN not flagged: %v", r.Phishing)
	}
}

func TestBrandImpersonationFlagged(t *testing.T) {
	m := normalMail()
	m.Subject = "Il tuo conto paypal e stato limitato"
	m.Body = "<a href=\"https://secure-verify.ru/account\">Accedi al conto</a>"
	r := Evaluate(m)
	joined := strings.Join(r.Phishing, " | ")
	if !strings.Contains(joined, "paypal") {
		t.Fatalf("brand impersonation not flagged: %v", r.Phishing)
	}
}

func TestLoginBaitFlagged(t *testing.T) {
	m := normalMail()
	m.Body = "<p>Il tuo account bloccato da 24 ore. <a href=\"https://brand.example.com/unlock\">Sblocca</a></p>"
	r := Evaluate(m)
	joined := strings.Join(r.Phishing, " | ")
	if !strings.Contains(joined, "phishing") {
		t.Fatalf("login bait not flagged: %v", r.Phishing)
	}
}

func TestDangerousAttachmentFlagged(t *testing.T) {
	m := normalMail()
	m.Attachments = []domain.Attachment{{Name: "fattura.pdf.exe", Size: 10, ContentType: "application/x-msdownload"}}
	r := Evaluate(m)
	joined := strings.Join(r.Phishing, " | ")
	if !strings.Contains(joined, "fattura.pdf.exe") {
		t.Fatalf("dangerous attachment not flagged: %v", r.Phishing)
	}
}

func TestLegitBrandLinkNotFlagged(t *testing.T) {
	m := normalMail()
	m.Subject = "Spedizione dhl in arrivo"
	m.Body = "<a href=\"https://www.dhl.it/tracking\">Traccia la spedizione</a>"
	r := Evaluate(m)
	joined := strings.Join(r.Phishing, " | ")
	if strings.Contains(joined, "dhl") {
		t.Fatalf("legit brand link flagged: %v", r.Phishing)
	}
}

func TestApplyStoresOnMessage(t *testing.T) {
	m := &domain.Message{
		Subject: "HAI VINTO",
		From:    domain.Recipient{Email: "x@spam.top"},
		Body:    "<a href=\"http://8.8.8.8/x\">clicca qui</a>",
	}
	r := Apply(m)
	if m.SpamScore != r.SpamScore || len(m.Phishing) != len(r.Phishing) {
		t.Fatalf("apply mismatch: %+v vs %+v", m, r)
	}
}
