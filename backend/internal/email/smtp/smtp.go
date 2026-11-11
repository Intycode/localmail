// Package smtp implements the outgoing mail transport: real SMTP with
// implicit TLS, STARTTLS or (explicit opt-in) plaintext, TLS 1.2+ only,
// verified certificates, AUTH PLAIN/LOGIN, and RFC 5322 message composition
// (text/plain, quoted-printable, generated Message-ID/Date, threading
// headers for replies). Passwords are provided in memory only.
package smtp

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	stdmime "mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"

	"mail/backend/internal/domain"
)

// Security modes (same vocabulary as the IMAP package).
const (
	SecurityTLS      = "tls"      // implicit TLS (default, port 465)
	SecurityStartTLS = "starttls" // plaintext dial + STARTTLS upgrade (port 587)
	SecurityPlain    = "plain"    // NO encryption: explicit opt-in only
)

// Timing constants (fail fast against hostile servers).
const (
	DialTimeout = 15 * time.Second
	IOTimeout   = 60 * time.Second
)

// Attachment size caps for the outgoing path: one file cannot exceed
// maxFileBytes and the message total cannot exceed maxFilesBytes (typical
// provider limits are 20–25 MB per message).
const (
	maxFileBytes  = 25 << 20 // 25 MB per file
	maxFilesBytes = 50 << 20 // 50 MB total
)

// Config is the non-secret SMTP connection configuration (domain alias so
// the wire format has a single source of truth). The password is NOT part of
// Config and is never persisted.
type Config = domain.SmtpConfig

// ValidateConfig checks the configuration; plaintext is refused unless the
// user explicitly opted in (allowInsecure = the UI showed a warning).
func ValidateConfig(c Config, allowInsecure bool) error {
	if c.Host == "" {
		return fmt.Errorf("host SMTP mancante")
	}
	if len(c.Host) > 253 {
		return fmt.Errorf("host SMTP troppo lungo")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("porta SMTP non valida (1-65535)")
	}
	switch c.Security {
	case SecurityTLS, SecurityStartTLS:
	case SecurityPlain:
		if !allowInsecure {
			return fmt.Errorf("connessione SMTP non cifrata rifiutata: scegli TLS o STARTTLS")
		}
	case "":
		return fmt.Errorf("modalità di sicurezza SMTP mancante (tls/starttls/plain)")
	default:
		return fmt.Errorf("modalità di sicurezza SMTP sconosciuta: %q", c.Security)
	}
	if c.Username == "" {
		return fmt.Errorf("username SMTP mancante")
	}
	return nil
}

// Address returns "host:port" for dialing.
func Address(c Config) string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// DefaultFor derives a sensible SMTP configuration from the IMAP one:
// smtp.<imap-host>:465 with implicit TLS and the same username.
func DefaultFor(imapHost, username string) Config {
	// strip the first label only when a domain remains: posteo.de -> smtp.de
	// would hand the password to an unrelated server, so keep the IMAP host
	host := imapHost
	if i := strings.Index(host, "."); i > 0 && strings.Contains(host[i+1:], ".") {
		host = "smtp." + host[i+1:]
	}
	return Config{Host: host, Port: 465, Security: SecurityTLS, Username: username}
}

// tlsConfig is strict: system roots, certificate verification ON, TLS 1.2+
// only, SNI set to the host (mirrors the IMAP package policy).
func tlsConfig(c Config) *tls.Config {
	return &tls.Config{
		ServerName: c.Host,
		MinVersion: tls.VersionTLS12,
		// InsecureSkipVerify deliberately never set: verification stays ON.
	}
}

// oauth2Auth implements SMTP AUTH XOAUTH2 (net/smtp only ships PLAIN/
// CRAM-MD5): initial response "user=<u>\x01auth=Bearer <t>\x01\x01", no
// server challenge on success.
type oauth2Auth struct {
	username string
	token    string
}

func (a oauth2Auth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	resp := "user=" + a.username + "\x01" + "auth=Bearer " + a.token + "\x01\x01"
	return "XOAUTH2", []byte(resp), nil
}

func (a oauth2Auth) Next(fromServer []byte, more bool) ([]byte, error) {
	return nil, fmt.Errorf("sfida XOAUTH2 inattesa dal server")
}

// loginAuth implements SMTP AUTH LOGIN (net/smtp only ships PLAIN/CRAM-MD5).
type loginAuth struct{ username, password string }

func (a loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte(a.username), nil
}

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(a.username), nil
	case "password:":
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("sfida LOGIN sconosciuta: %q", fromServer)
	}
}

// Compose builds the RFC 5322 message: generated Message-ID/Date,
// quoted-printable UTF-8 body, To/CC headers, In-Reply-To/References when
// replying. With an HTMLBody set the message is composed as multipart/
// alternative (text/plain first, text/html last per RFC 2046) so replies keep
// the quoted message in its original HTML form. BCC recipients are
// envelope-only (never in the headers).
func Compose(from domain.Recipient, msg domain.ComposeMessage) []byte {
	var b strings.Builder

	addrOf := func(r domain.Recipient) string {
		a := &mail.Address{Name: r.Name, Address: r.Email}
		return a.String()
	}
	list := func(rs []domain.Recipient) string {
		parts := make([]string, 0, len(rs))
		for _, r := range rs {
			if r.Email == "" {
				continue
			}
			parts = append(parts, addrOf(r))
		}
		return strings.Join(parts, ", ")
	}

	b.WriteString("From: " + addrOf(from) + "\r\n")
	if to := list(msg.To); to != "" {
		b.WriteString("To: " + to + "\r\n")
	}
	if cc := list(msg.CC); cc != "" {
		b.WriteString("Cc: " + cc + "\r\n")
	}
	subject := msg.Subject
	if subject == "" {
		subject = "(senza oggetto)"
	}
	b.WriteString("Subject: " + stdmime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: " + newMessageID(from.Email) + "\r\n")
	if msg.InReplyTo != "" {
		b.WriteString("In-Reply-To: <" + strings.Trim(msg.InReplyTo, "<>") + ">\r\n")
	}
	if len(msg.References) > 0 {
		refs := make([]string, 0, len(msg.References))
		for _, r := range msg.References {
			refs = append(refs, "<"+strings.Trim(r, "<>")+">")
		}
		b.WriteString("References: " + strings.Join(refs, " ") + "\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")

	// bodyPart writes the message body as a complete MIME part (headers +
	// content, no trailing CRLF): text/plain, or multipart/alternative when
	// an HTML variant is present (text first, html last per RFC 2046).
	html := strings.TrimSpace(msg.HTMLBody)
	writeBodyPart := func() {
		if html != "" {
			boundary := newBoundary()
			b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
			b.WriteString("--" + boundary + "\r\n")
			b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
			b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
			writeQuotedPrintable(&b, msg.Body)
			b.WriteString("\r\n--" + boundary + "\r\n")
			b.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n")
			b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
			writeQuotedPrintable(&b, html)
			b.WriteString("\r\n--" + boundary + "--\r\n")
			return
		}
		b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		writeQuotedPrintable(&b, msg.Body)
	}

	// loaded files (Data filled by LoadFiles): with attachments the message
	// becomes multipart/mixed — body part first, one base64 part per file.
	files := make([]domain.OutgoingFile, 0, len(msg.Files))
	for _, f := range msg.Files {
		if len(f.Data) > 0 {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		writeBodyPart()
		return []byte(b.String())
	}

	mixed := newBoundary()
	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + mixed + "\"\r\n\r\n")
	b.WriteString("--" + mixed + "\r\n")
	writeBodyPart()
	for _, f := range files {
		name := f.Name
		if name == "" {
			name = filepath.Base(f.Path)
		}
		encName := stdmime.QEncoding.Encode("utf-8", name)
		b.WriteString("\r\n--" + mixed + "\r\n")
		b.WriteString("Content-Type: " + f.ContentType + "; name=\"" + encName + "\"\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		b.WriteString("Content-Disposition: attachment; filename=\"" + encName + "\"\r\n\r\n")
		writeBase64(&b, f.Data)
	}
	b.WriteString("\r\n--" + mixed + "--\r\n")
	return []byte(b.String())
}

// LoadFiles reads every outgoing attachment from disk into memory: size
// caps are enforced before reading (per file and total), the content type
// is derived from the extension when absent, and Name defaults to the file
// base. It is called once in the send path before provider dispatch, so
// both the SMTP and the JMAP transports embed the same bytes.
func LoadFiles(msg *domain.ComposeMessage) error {
	if msg == nil || len(msg.Files) == 0 {
		return nil
	}
	var total int64
	for i := range msg.Files {
		f := &msg.Files[i]
		path := strings.TrimSpace(f.Path)
		if path == "" {
			return fmt.Errorf("percorso dell'allegato mancante")
		}
		base := filepath.Base(path)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("allegato %s non leggibile: %w", base, err)
		}
		if info.IsDir() {
			return fmt.Errorf("%s è una cartella: allega un file", base)
		}
		if info.Size() > maxFileBytes {
			return fmt.Errorf("allegato %s troppo grande (%.0f MB, limite %d MB)", base, float64(info.Size())/(1<<20), maxFileBytes/(1<<20))
		}
		total += info.Size()
		if total > maxFilesBytes {
			return fmt.Errorf("allegati troppo grandi in totale (limite %d MB)", maxFilesBytes/(1<<20))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("allegato %s non leggibile: %w", base, err)
		}
		f.Data = data
		f.Path = path
		if strings.TrimSpace(f.Name) == "" {
			f.Name = base
		}
		if strings.TrimSpace(f.ContentType) == "" {
			ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
			if i := strings.Index(ct, ";"); i >= 0 {
				ct = ct[:i]
			}
			if ct == "" {
				ct = "application/octet-stream"
			}
			f.ContentType = ct
		}
	}
	return nil
}

// writeBase64 emits data as base64 in 76-character lines (RFC 2045), each
// terminated by CRLF except the last: the next boundary delimiter supplies
// the final CRLF.
func writeBase64(b *strings.Builder, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		b.WriteString(enc[:76])
		b.WriteString("\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc)
}

// connect dials the server, upgrades to TLS when configured and
// authenticates. The caller owns (and closes) the returned client.
func connect(cfg Config, password, hello string) (*smtp.Client, error) {
	var conn net.Conn
	var err error
	switch cfg.Security {
	case SecurityTLS:
		d := &net.Dialer{Timeout: DialTimeout}
		conn, err = tls.DialWithDialer(d, "tcp", Address(cfg), tlsConfig(cfg))
	case SecurityStartTLS, SecurityPlain:
		conn, err = net.DialTimeout("tcp", Address(cfg), DialTimeout)
	default:
		return nil, fmt.Errorf("modalità di sicurezza SMTP sconosciuta: %q", cfg.Security)
	}
	if err != nil {
		return nil, fmt.Errorf("connessione SMTP a %s non riuscita: %v", Address(cfg), err)
	}
	// one overall deadline for the whole session: a wedged server cannot
	// hold the engine longer than IOTimeout
	_ = conn.SetDeadline(time.Now().Add(IOTimeout))

	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("handshake SMTP con %s non riuscito: %v", cfg.Host, err)
	}
	ok := false
	defer func() {
		if !ok {
			c.Close()
		}
	}()

	if err := c.Hello(hello); err != nil {
		return nil, fmt.Errorf("EHLO non riuscito: %v", err)
	}

	if cfg.Security == SecurityStartTLS {
		if has, _ := c.Extension("STARTTLS"); !has {
			return nil, fmt.Errorf("il server non supporta STARTTLS")
		}
		if err := c.StartTLS(tlsConfig(cfg)); err != nil {
			return nil, fmt.Errorf("STARTTLS con %s non riuscito: %v", cfg.Host, err)
		}
	}

	if err := authenticate(c, cfg, password); err != nil {
		return nil, err
	}
	ok = true
	return c, nil
}

// Verify logs into the outgoing server without sending anything, so a wrong
// host, port or password surfaces when the account is added, not at the
// first send.
func Verify(cfg Config, password string, allowInsecure bool) error {
	if err := ValidateConfig(cfg, allowInsecure); err != nil {
		return err
	}
	c, err := connect(cfg, password, "localhost")
	if err != nil {
		return err
	}
	if err := c.Quit(); err != nil {
		_ = c.Close() // login already succeeded: a failing QUIT is not an error
	}
	return nil
}

// SendMessage composes and delivers the message over SMTP. It returns the
// raw RFC 5322 bytes that were delivered (used for the APPEND into Sent).
func SendMessage(cfg Config, password string, allowInsecure bool, from domain.Recipient, msg domain.ComposeMessage) ([]byte, error) {
	if err := ValidateConfig(cfg, allowInsecure); err != nil {
		return nil, err
	}
	recipients := envelopeRecipients(msg)
	if len(recipients) == 0 {
		return nil, fmt.Errorf("nessun destinatario valido")
	}
	raw := Compose(from, msg)

	hello := "localhost"
	if i := strings.LastIndex(from.Email, "@"); i >= 0 && i < len(from.Email)-1 {
		hello = from.Email[i+1:]
	}
	c, err := connect(cfg, password, hello)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	if err := c.Mail(from.Email); err != nil {
		return nil, fmt.Errorf("MAIL FROM rifiutato: %v", err)
	}
	for _, r := range recipients {
		if err := c.Rcpt(r); err != nil {
			return nil, fmt.Errorf("destinatario %s rifiutato: %v", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return nil, fmt.Errorf("DATA non riuscito: %v", err)
	}
	if _, err := w.Write(raw); err != nil {
		return nil, fmt.Errorf("invio del messaggio non riuscito: %v", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("chiusura DATA non riuscita: %v", err)
	}
	if err := c.Quit(); err != nil {
		// the message was accepted; a failing QUIT must not fail the send
		_ = c.Close()
	}
	return raw, nil
}

// authenticate performs AUTH when the server advertises it: XOAUTH2 for
// OAuth accounts, PLAIN first, LOGIN as fallback. Plaintext connections only
// authenticate with LOGIN when the user explicitly opted out of encryption
// (allowInsecure at add time).
func authenticate(c *smtp.Client, cfg Config, password string) error {
	if password == "" {
		return fmt.Errorf("password SMTP mancante (credenziale non disponibile)")
	}
	if cfg.Auth == "oauth" {
		return c.Auth(oauth2Auth{username: cfg.Username, token: password})
	}
	supported, mechs := c.Extension("AUTH")
	if !supported {
		// server demands no auth (rare open relay): proceed unauthenticated
		return nil
	}
	switch {
	case strings.Contains(mechs, "PLAIN"):
		return c.Auth(smtp.PlainAuth("", cfg.Username, password, cfg.Host))
	case strings.Contains(mechs, "LOGIN"):
		return c.Auth(loginAuth{username: cfg.Username, password: password})
	default:
		return fmt.Errorf("nessun meccanismo di autenticazione supportato (server offre: %s)", mechs)
	}
}

// envelopeRecipients returns the unique envelope recipients (To+CC+BCC).
func envelopeRecipients(msg domain.ComposeMessage) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(rs []domain.Recipient) {
		for _, r := range rs {
			e := strings.TrimSpace(r.Email)
			if e == "" {
				continue
			}
			if _, dup := seen[e]; dup {
				continue
			}
			seen[e] = struct{}{}
			out = append(out, e)
		}
	}
	add(msg.To)
	add(msg.CC)
	add(msg.BCC)
	return out
}

// newBoundary generates a unique MIME boundary (crypto-random, cannot appear
// in normal body text).
func newBoundary() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("mail-boundary-%d", time.Now().UnixNano())
	}
	return "=_mail_" + hex.EncodeToString(b)
}

// newMessageID generates a RFC 5322 Message-ID from the sender domain.
func newMessageID(fromEmail string) string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("<%d@%s>", time.Now().UnixNano(), domainPart(fromEmail))
	}
	return "<" + hex.EncodeToString(b) + "@" + domainPart(fromEmail) + ">"
}

func domainPart(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 && i < len(email)-1 {
		return email[i+1:]
	}
	return "localhost"
}