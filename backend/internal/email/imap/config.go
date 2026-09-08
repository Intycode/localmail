// Package imap holds the IMAP transport contract and the real client.
// Security defaults: verified TLS (implicit or STARTTLS); plaintext requires
// an explicit opt-in and is strongly discouraged. Certificates are always
// verified against the system roots (InsecureSkipVerify is never set).
package imap

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"mail/backend/internal/domain"
)

// Security describes the transport security mode (values of
// domain.ImapConfig.Security).
const (
	SecurityTLS      = "tls"      // implicit TLS (default, port 993)
	SecurityStartTLS = "starttls" // plaintext dial + STARTTLS upgrade
	SecurityPlain    = "plain"    // NO encryption: explicit opt-in only
)

// Config is the non-secret IMAP connection configuration: an alias of the
// domain type so the wire format has a single source of truth. The password
// is NOT part of Config and is never persisted.
type Config = domain.ImapConfig

// ValidateConfig checks the configuration; plaintext is refused unless
// explicitly allowed by the user (allowInsecure = the UI showed a warning).
func ValidateConfig(c Config, allowInsecure bool) error {
	if c.Host == "" {
		return fmt.Errorf("host IMAP mancante")
	}
	if len(c.Host) > 253 {
		return fmt.Errorf("host IMAP troppo lungo")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("porta IMAP non valida (1-65535)")
	}
	switch c.Security {
	case SecurityTLS, SecurityStartTLS:
	case SecurityPlain:
		if !allowInsecure {
			return fmt.Errorf("connessione non cifrata rifiutata: scegli TLS o STARTTLS")
		}
	case "":
		return fmt.Errorf("modalità di sicurezza mancante (tls/starttls/plain)")
	default:
		return fmt.Errorf("modalità di sicurezza sconosciuta: %q", c.Security)
	}
	if c.Username == "" {
		return fmt.Errorf("username mancante")
	}
	return nil
}

// Address returns "host:port" for dialing.
func Address(c Config) string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// TLSConfig returns a strict TLS configuration: system roots, certificate
// verification ON, TLS 1.2+ only, SNI set to the host.
func TLSConfig(c Config) *tls.Config {
	return &tls.Config{
		ServerName: c.Host,
		MinVersion: tls.VersionTLS12,
		// InsecureSkipVerify deliberately never set: verification stays ON.
	}
}

// Timing and batching constants (security & performance).
const (
	DialTimeout   = 15 * time.Second // connect timeout (hostile server: fail fast)
	InitialWindow = 200              // initial sync: newest N messages per folder
	FetchBatch    = 50               // messages per UID FETCH batch
)

// dialer returns the net.Dialer with the configured connect timeout.
func dialer() *net.Dialer {
	return &net.Dialer{Timeout: DialTimeout}
}
