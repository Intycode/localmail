// Package pgp implements OpenPGP signing/encryption on device (M9) with
// github.com/ProtonMail/go-crypto.
//
// Design rules:
//   - Private keys NEVER reach the webview: the engine generates/holds them
//     in the runtime vault under the pgp/<accountID> key and Rust persists
//     them in the OS keychain (same channel as account passwords).
//   - Contact public keys are not secrets: they live in the encrypted local
//     DB (pgp_pubkeys), keyed by lowercase email.
//   - M9 uses PGP/Inline (clearsign + armored encryption) on the text part:
//     simple, widely readable, no MIME surgery. When encrypting, the message
//     is sent as text-only so no plaintext HTML leaks.
package pgp

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"

	"mail/backend/internal/auth"
)

// VaultKey returns the runtime-vault key that holds the armored private
// keyring of an account.
func VaultKey(accountID string) string { return "pgp/" + accountID }

// Service bundles keyring access and the contact public-key store.
type Service struct {
	secrets *auth.RuntimeSecrets
	db      *sql.DB
}

// New builds a PGP service.
func New(secrets *auth.RuntimeSecrets, db *sql.DB) *Service {
	return &Service{secrets: secrets, db: db}
}

// --- private keyring (per account, runtime vault) -----------------------

// Generate creates a 3072-bit RSA key pair for name/email, stores the
// private keyring in the runtime vault and returns the armored public key.
// The generated key has NO passphrase: it is born on device, lives in the OS
// keychain and never leaves it.
func (s *Service) Generate(accountID, name, email string) (string, error) {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(email) == "" {
		return "", fmt.Errorf("nome e email sono richiesti")
	}
	entity, err := openpgp.NewEntity(name, "", email, nil)
	if err != nil {
		return "", fmt.Errorf("generazione chiave: %w", err)
	}
	es := openpgp.EntityList{entity}
	if err := s.storePrivate(accountID, es); err != nil {
		return "", err
	}
	return ArmorPublic(es)
}

// ImportPrivate validates an armored private keyring and stores it in the
// runtime vault for the account.
func (s *Service) ImportPrivate(accountID, armored string) error {
	es, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	if err != nil {
		return fmt.Errorf("chiave privata non valida: %w", err)
	}
	hasPrivate := false
	for _, e := range es {
		if e.PrivateKey != nil {
			hasPrivate = true
			break
		}
	}
	if !hasPrivate {
		return fmt.Errorf("il testo non contiene chiavi private")
	}
	return s.storePrivate(accountID, es)
}

func (s *Service) storePrivate(accountID string, es openpgp.EntityList) error {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return fmt.Errorf("armor encode: %w", err)
	}
	for _, e := range es {
		if err := e.SerializePrivateWithoutSigning(w, nil); err != nil {
			return fmt.Errorf("serialize private: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	s.secrets.Put(VaultKey(accountID), auth.Secret(buf.String()))
	return nil
}

// HasKey reports whether the account owns a private keyring in the vault.
func (s *Service) HasKey(accountID string) bool {
	_, ok := s.secrets.Get(VaultKey(accountID))
	return ok
}

// Fingerprint returns the primary key fingerprint of the account keyring.
func (s *Service) Fingerprint(accountID string) string {
	es := s.private(accountID)
	if len(es) == 0 || es[0].PrimaryKey == nil {
		return ""
	}
	return strings.ToUpper(fmt.Sprintf("%x", es[0].PrimaryKey.Fingerprint))
}

func (s *Service) private(accountID string) openpgp.EntityList {
	sec, ok := s.secrets.Get(VaultKey(accountID))
	if !ok {
		return nil
	}
	es, err := openpgp.ReadArmoredKeyRing(strings.NewReader(sec.Expose()))
	if err != nil {
		return nil
	}
	return es
}

// Forget wipes the private keyring from the runtime vault.
func (s *Service) Forget(accountID string) { s.secrets.Forget(VaultKey(accountID)) }

// ArmorPublic serializes the public halves of a keyring as ASCII armor.
func ArmorPublic(es openpgp.EntityList) (string, error) {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return "", fmt.Errorf("armor encode: %w", err)
	}
	for _, e := range es {
		if err := e.Serialize(w); err != nil {
			return "", fmt.Errorf("serialize public: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ExportPublic returns the armored public key of the account keyring.
func (s *Service) ExportPublic(accountID string) (string, error) {
	es := s.private(accountID)
	if len(es) == 0 {
		return "", fmt.Errorf("nessuna chiave PGP per questo account: generane una o importane una")
	}
	return ArmorPublic(es)
}

// --- contact public keys (encrypted local DB) ---------------------------

// SetPubKey stores/updates the armored public key for a contact email.
func (s *Service) SetPubKey(email, armored string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	es, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	if err != nil {
		return "", fmt.Errorf("chiave pubblica non valida: %w", err)
	}
	if len(es) == 0 || es[0].PrimaryKey == nil {
		return "", fmt.Errorf("nessuna chiave trovata nel testo")
	}
	fp := strings.ToUpper(fmt.Sprintf("%x", es[0].PrimaryKey.Fingerprint))
	_, err = s.db.Exec(
		"INSERT OR REPLACE INTO pgp_pubkeys(email,armored,fingerprint) VALUES(?,?,?)",
		email, strings.TrimSpace(armored), fp)
	if err != nil {
		return "", fmt.Errorf("salva chiave pubblica: %w", err)
	}
	return fp, nil
}

// DeletePubKey removes a contact key.
func (s *Service) DeletePubKey(email string) error {
	_, err := s.db.Exec("DELETE FROM pgp_pubkeys WHERE email=?", strings.ToLower(strings.TrimSpace(email)))
	return err
}

// PubKeyRow is one contact key (UI listing).
type PubKeyRow struct {
	Email       string `json:"email"`
	Fingerprint string `json:"fingerprint"`
}

// ListPubKeys lists all contact keys.
func (s *Service) ListPubKeys() ([]PubKeyRow, error) {
	rows, err := s.db.Query("SELECT email,fingerprint FROM pgp_pubkeys ORDER BY email")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PubKeyRow{}
	for rows.Next() {
		var r PubKeyRow
		if err := rows.Scan(&r.Email, &r.Fingerprint); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) contactKeys(emails []string) openpgp.EntityList {
	es := openpgp.EntityList{}
	for _, e := range emails {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		var armored string
		if err := s.db.QueryRow("SELECT armored FROM pgp_pubkeys WHERE email=?", e).Scan(&armored); err != nil {
			continue
		}
		if list, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored)); err == nil {
			es = append(es, list...)
		}
	}
	return es
}

// --- sign / encrypt / decrypt / verify -----------------------------------

// Sign clearsigns the text with the account private key (PGP/Inline).
func (s *Service) Sign(accountID, text string) (string, error) {
	es := s.private(accountID)
	if len(es) == 0 || es[0].PrivateKey == nil {
		return "", fmt.Errorf("nessuna chiave privata PGP per questo account")
	}
	var buf bytes.Buffer
	w, err := clearsign.Encode(&buf, es[0].PrivateKey, nil)
	if err != nil {
		return "", fmt.Errorf("firma: %w", err)
	}
	if _, err := w.Write([]byte(text)); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Encrypt armors the text for the given recipients plus the account itself
// (encrypt-to-self keeps Sent readable). Returns the recipients without a
// stored key so the caller can warn the user.
func (s *Service) Encrypt(accountID, text string, recipients []string) (string, []string, error) {
	es := s.contactKeys(recipients)
	if self := s.private(accountID); len(self) > 0 {
		es = append(es, self...)
	}
	var missing []string
	for _, r := range recipients {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" {
			continue
		}
		var one int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM pgp_pubkeys WHERE email=?", r).Scan(&one); err != nil || one == 0 {
			missing = append(missing, r)
		}
	}
	if len(es) == 0 {
		return "", missing, fmt.Errorf("nessuna chiave pubblica disponibile per cifrare")
	}
	var buf bytes.Buffer
	armW, err := armor.Encode(&buf, "PGP MESSAGE", nil)
	if err != nil {
		return "", missing, fmt.Errorf("armor encode: %w", err)
	}
	pl, err := openpgp.Encrypt(armW, es, nil, nil, nil)
	if err != nil {
		return "", missing, fmt.Errorf("encrypt: %w", err)
	}
	if _, err := io.WriteString(pl, text); err != nil {
		return "", missing, err
	}
	if err := pl.Close(); err != nil {
		return "", missing, err
	}
	if err := armW.Close(); err != nil {
		return "", missing, err
	}
	return buf.String(), missing, nil
}

// Decrypt decrypts an armored PGP message with the account private key.
// Returns the plaintext and, when the message is also signed, the signer
// fingerprint (empty when not signed or unchecked).
func (s *Service) Decrypt(accountID, armored string) (string, string, error) {
	es := s.private(accountID)
	if len(es) == 0 {
		return "", "", fmt.Errorf("nessuna chiave privata PGP per questo account")
	}
	armBlock, err := armor.Decode(strings.NewReader(armored))
	if err != nil {
		return "", "", fmt.Errorf("armatura PGP non valida: %w", err)
	}
	md, err := openpgp.ReadMessage(armBlock.Body, es, nil, nil)
	if err != nil {
		return "", "", fmt.Errorf("decifratura non riuscita: %w", err)
	}
	plain, err := io.ReadAll(md.UnverifiedBody)
	if err != nil {
		return "", "", fmt.Errorf("lettura testo: %w", err)
	}
	signedBy := ""
	if md.SignedBy != nil && md.SignedBy.PublicKey != nil {
		signedBy = strings.ToUpper(fmt.Sprintf("%x", md.SignedBy.PublicKey.Fingerprint))
	}
	return string(plain), signedBy, nil
}

// Verify checks a clearsigned block against the account keys and every
// stored contact key. Returns valid, invalid or unknown (signature present
// but no matching public key locally).
func (s *Service) Verify(accountID, clearsigned string) string {
	es := s.private(accountID)
	es = append(es, s.allContactEntities()...)
	block, _ := clearsign.Decode([]byte(clearsigned))
	if block == nil {
		return "invalid"
	}
	signer, err := block.VerifySignature(es, nil)
	if err != nil || signer == nil {
		return classifySigError(err)
	}
	return "valid"
}

func classifySigError(err error) string {
	if err == nil {
		return "valid"
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no keys") || strings.Contains(msg, "cannot verify") || strings.Contains(msg, "unknown") {
		return "unknown"
	}
	return "invalid"
}

func (s *Service) allContactEntities() openpgp.EntityList {
	rows, err := s.db.Query("SELECT armored FROM pgp_pubkeys")
	if err != nil {
		return nil
	}
	defer rows.Close()
	es := openpgp.EntityList{}
	for rows.Next() {
		var armored string
		if err := rows.Scan(&armored); err != nil {
			continue
		}
		if list, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored)); err == nil {
			es = append(es, list...)
		}
	}
	return es
}

// openpgpNewEntity wraps NewEntity so the default packet config lives in
// one place (library modern defaults, 3072-bit RSA).
func openpgpNewEntity(name, email string) (*openpgp.Entity, error) {
	return openpgp.NewEntity(name, "", email, nil)
}