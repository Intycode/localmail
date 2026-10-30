package mime

// PGP ingest tests (M9): detection of PGP/Inline encrypted and clearsigned
// text parts plus PGP/MIME multipart/encrypted, without touching parse
// purity (no decryption here).

import (
	"strings"
	"testing"
)

const clearsignedRaw = "Subject: Messaggio firmato\r\nFrom: Anna <anna@acme.dev>\r\n\r\n" +
	"-----BEGIN PGP SIGNED MESSAGE-----\r\nHash: SHA512\r\n\r\nContenuto in chiaro firmato. Tom & Jerry <bob@x.com>\r\n" +
	"-----BEGIN PGP SIGNATURE-----\r\n\r\nFAKEARMOR\r\n-----END PGP SIGNATURE-----\r\n"

const inlineEncryptedRaw = "Subject: Messaggio cifrato\r\nFrom: Anna <anna@acme.dev>\r\n\r\n" +
	"-----BEGIN PGP MESSAGE-----\r\n\r\nFAKEENCRYPTEDDATA\r\n-----END PGP MESSAGE-----\r\n"

const pgpMIMERaw = "Subject: PGP MIME\r\nFrom: Anna <anna@acme.dev>\r\n" +
	"Content-Type: multipart/encrypted; protocol=\"application/pgp-encrypted\"; boundary=BB\r\n\r\n" +
	"--BB\r\nContent-Type: application/pgp-encrypted\r\n\r\nVersion: 1\r\n\r\n" +
	"--BB\r\nContent-Type: application/octet-stream\r\n\r\n-----BEGIN PGP MESSAGE-----\r\n\r\nPGPMIMEDATA\r\n-----END PGP MESSAGE-----\r\n--BB--\r\n"

func TestParseClearsigned(t *testing.T) {
	m, err := Parse([]byte(clearsignedRaw))
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsSigned {
		t.Fatal("clearsigned non rilevato")
	}
	if !strings.Contains(m.BodyText, "Contenuto in chiaro firmato.") {
		t.Fatalf("plaintext = %q", m.BodyText)
	}
	if !strings.Contains(m.Body, "-----BEGIN PGP SIGNED MESSAGE-----") {
		t.Fatal("il blocco firmato deve restare in Body per la verifica")
	}
	// byte-exact: the HTML sanitizer must not escape the signed text
	if !strings.Contains(m.Body, "Tom & Jerry <bob@x.com>") {
		t.Fatalf("blocco firmato alterato: %q", m.Body)
	}
}

func TestParseInlineEncrypted(t *testing.T) {
	m, err := Parse([]byte(inlineEncryptedRaw))
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsEncrypted {
		t.Fatal("PGP/Inline cifrato non rilevato")
	}
	if !strings.Contains(m.Body, "-----BEGIN PGP MESSAGE-----") {
		t.Fatal("il body deve contenere l'armatura cifrata")
	}
	if strings.Contains(m.BodyText, "FAKEENCRYPTEDDATA") {
		t.Fatal("il testo cifrato non deve finire in BodyText")
	}
}

func TestParsePGPMIME(t *testing.T) {
	m, err := Parse([]byte(pgpMIMERaw))
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsEncrypted {
		t.Fatal("multipart/encrypted non rilevato")
	}
	if !strings.Contains(m.Body, "PGPMIMEDATA") {
		t.Fatalf("payload mancante: %s", m.Body)
	}
}

func TestParseNormalMailUnflagged(t *testing.T) {
	m, err := Parse([]byte("Subject: normale\r\nFrom: a@b.c\r\n\r\ntesto semplice con -----BEGIN PGP SIGNED MESSAGE----- dentro il testo ma senza chiusura\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m.IsSigned || m.IsEncrypted {
		t.Fatal("falso positivo su testo normale")
	}
}
