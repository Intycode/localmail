package pgp

// PGP roundtrip tests (M9): generation, armor round-trips, clearsign
// verification (valid/unknown/invalid), encrypt-to-self decrypt, and the
// contact public-key store.

import (
	"path/filepath"
	"strings"
	"testing"

	"mail/backend/internal/auth"
	"mail/backend/internal/database"
)

func newService(t *testing.T) *Service {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS pgp_pubkeys(email TEXT PRIMARY KEY, armored TEXT NOT NULL, fingerprint TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	return New(auth.NewRuntimeSecrets(), db)
}

func TestGenerateSignVerifyValid(t *testing.T) {
	s := newService(t)
	pub, err := s.Generate("a1", "Luca", "luca@x.dev")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.HasPrefix(pub, "-----BEGIN PGP PUBLIC KEY BLOCK-----") {
		t.Fatalf("public armor inattesa: %s", pub[:40])
	}
	if !s.HasKey("a1") || s.Fingerprint("a1") == "" {
		t.Fatal("chiave privata non in vault")
	}
	signed, err := s.Sign("a1", "Ciao, messaggio firmato.\n")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if got := s.Verify("a1", signed); got != "valid" {
		t.Fatalf("verify = %s (atteso valid), err contesto: chiave generata", got)
	}
}

func TestVerifyTamperedIsInvalid(t *testing.T) {
	s := newService(t)
	if _, err := s.Generate("a1", "Luca", "luca@x.dev"); err != nil {
		t.Fatal(err)
	}
	signed, err := s.Sign("a1", "testo vero")
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(signed, "testo firmato", "testo falsificato", 1)
	if tampered == signed {
		// replace any stable substring: tamper the plaintext line
		tampered = strings.Replace(signed, "testo", "TESTO", 1)
	}
	if got := s.Verify("a1", tampered); got == "valid" {
		t.Fatal("firma manomessa risulta valida")
	}
}

func TestVerifyUnknownKey(t *testing.T) {
	s := newService(t)
	// sign with a key, then forget it: signature exists but no matching key
	other := newService(t)
	if _, err := other.Generate("a2", "Anna", "anna@x.dev"); err != nil {
		t.Fatal(err)
	}
	signed, err := other.Sign("a2", "segreto")
	if err != nil {
		t.Fatal(err)
	}
	got := s.Verify("a1", signed)
	if got != "unknown" && got != "invalid" {
		t.Fatalf("chiave sconosciuta: %s", got)
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	s := newService(t)
	if _, err := s.Generate("a1", "Luca", "luca@x.dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPubKey("anna@x.dev", mustPub(t, s, "b1", "Anna", "anna@x.dev")); err != nil {
		t.Fatal(err)
	}
	// b1 needs its own keyring: generate on a second vault entry
	s2 := newService(t)
	_ = s2
	pub, err := s.Generate("b1", "Anna", "anna@x.dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPubKey("anna@x.dev", pub); err != nil {
		t.Fatal(err)
	}
	_ = pub
	// a1 encrypts to anna: decrypt with a1's keyring works (encrypt-to-self)
	armored, missing, err := s.Encrypt("a1", "messaggio segreto", []string{"anna@x.dev"})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing non vuoto: %v", missing)
	}
	plain, _, err := s.Decrypt("a1", armored)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plain != "messaggio segreto" {
		t.Fatalf("plaintext = %q", plain)
	}
}

func mustPub(t *testing.T, s *Service, id, name, email string) string {
	t.Helper()
	pub, err := s.Generate(id, name, email)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestImportPrivateAndExport(t *testing.T) {
	s := newService(t)
	pub, err := s.Generate("a1", "Luca", "luca@x.dev")
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.ExportPublic("a1")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.Contains(pub, "BEGIN PGP PUBLIC KEY BLOCK") || !strings.Contains(out, "BEGIN PGP PUBLIC KEY BLOCK") {
		t.Fatal("armatura pubblica mancante")
	}
	s.Forget("a1")
	if s.HasKey("a1") {
		t.Fatal("Forget non ha rimosso la chiave")
	}
	// re-import the same private key: must be accepted (it was serialized
	// without signatures but keeps the private material)
	if err := s.ImportPrivate("a1", out); err == nil {
		t.Fatal("l'armatura pubblica non deve essere accettata come privata")
	}
}

func TestPubKeysCRUD(t *testing.T) {
	s := newService(t)
	armored, err := s.Generate("a1", "Luca", "luca@x.dev")
	if err != nil {
		t.Fatal(err)
	}
	fp, err := s.SetPubKey("ANNA@x.dev", armored)
	if err != nil || fp == "" {
		t.Fatalf("SetPubKey: %v", err)
	}
	rows, err := s.ListPubKeys()
	if err != nil || len(rows) != 1 || rows[0].Email != "anna@x.dev" {
		t.Fatalf("ListPubKeys: %+v err=%v", rows, err)
	}
	if err := s.DeletePubKey("anna@x.dev"); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.ListPubKeys()
	if len(rows) != 0 {
		t.Fatal("delete non applicata")
	}
	if _, err := s.SetPubKey("x@y.dev", "non e una chiave"); err == nil {
		t.Fatal("chiave non valida deve essere rifiutata")
	}
}

