// Package mime parses raw RFC5322/MIME messages into domain.Message.
// It is the first stage of the sync pipeline: raw bytes -> structured message.
//
// Implementation uses github.com/emersion/go-message (mail reader): multipart,
// quoted-printable/base64 transfer encodings and non-UTF-8 charsets are
// decoded correctly (charset decoders registered via blank import).
package mime

import (
	"bytes"
	"fmt"
	"io"
	stdmime "mime"
	"strings"
	"time"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // registers charset decoders
	"github.com/emersion/go-message/mail"

	"mail/backend/internal/domain"
)

// maxHeaderLen caps pathological headers (anti-DoS from hostile servers).
const maxHeaderLen = 1 << 20 // 1 MiB

// Parse converts raw RFC5322/MIME bytes into a Message.
func Parse(raw []byte) (domain.Message, error) {
	if len(raw) > 32<<20 {
		return domain.Message{}, io.ErrUnexpectedEOF // oversized: rejected upstream too
	}
	reader, err := mail.CreateReader(bytes.NewReader(raw))
	if fatal(err) {
		return domain.Message{}, err
	}

	m := domain.Message{}

	if subj, err := reader.Header.Subject(); err == nil {
		m.Subject = subj
	}
	if d, err := reader.Header.Date(); err == nil {
		m.Date = d
	} else {
		m.Date = time.Now().UTC()
	}
	if from := parseAddressList(&reader.Header, "From"); len(from) > 0 {
		m.From = from[0]
	}
	m.To = parseAddressList(&reader.Header, "To")
	m.CC = parseAddressList(&reader.Header, "Cc")
	m.BCC = parseAddressList(&reader.Header, "Bcc")
	if id, err := reader.Header.MessageID(); err == nil {
		m.MessageID = id
	}
	if refs := reader.Header.Get("References"); refs != "" {
		m.References = splitMsgIDs(refs)
	}
	if irt := reader.Header.Get("In-Reply-To"); irt != "" {
		m.InReplyTo = splitMsgIDs(irt)
	}

	// Walk the part tree: first text/plain becomes BodyText, first text/html
	// becomes Body. Attachments are recorded as metadata (name, size, type);
	// binaries are re-downloaded on demand by ExtractAttachment.
	var text, html string
	var atts []domain.Attachment
	var pgpEncPayload string
	var pgpSignedMIME bool
	// PGP/MIME (multipart/encrypted): the go-message reader transparently
	// unpacks subparts, so detect it from the TOP-LEVEL content type and pull
	// the armor straight from the raw bytes (parse stays pure).
	topCT := reader.Header.Get("Content-Type")
	if strings.HasPrefix(topCT, "multipart/encrypted") {
		if blk := extractBlock(string(raw), armorBeginMessage, armorEndMessage); blk != "" {
			m.IsEncrypted = true
			m.Body = blk
			m.BodyText = ""
			if m.Date.IsZero() {
				m.Date = time.Now().UTC()
			}
			return m, nil
		}
	}
	if strings.HasPrefix(topCT, "multipart/signed") {
		// flagged, signature verification deferred (M9 verifies PGP/Inline
		// clearsigns only; MIME canonicalization needs the exact bytes)
		pgpSignedMIME = true
	}
	walkParts(reader, &text, &html, &atts, &pgpEncPayload, &pgpSignedMIME, 0)
	m.Attachments = atts
	switch {
	case pgpEncPayload != "":
		// PGP/MIME (multipart/encrypted): the armor is the content; the DB
		// keeps ciphertext at rest and the IPC layer decrypts on view.
		m.IsEncrypted = true
		m.Body = pgpEncPayload
		m.BodyText = ""
	case strings.Contains(text, "-----BEGIN PGP MESSAGE-----"):
		// PGP/Inline: the armored block replaces the readable body
		info := DetectPGP(text)
		if info.IsEncrypted {
			m.IsEncrypted = true
			m.Body = info.EncryptedPayload
			m.BodyText = ""
		}
	case strings.Contains(text, "-----BEGIN PGP SIGNED MESSAGE-----"):
		// clearsigned: Body keeps the original block (for verification),
		// BodyText carries the readable plaintext
		if info := DetectPGP(text); info.IsSigned {
			m.IsSigned = true
			m.Body = info.Clearsigned
			m.BodyText = info.Plaintext
		}
	default:
		m.Body = html
	}
	if pgpEncPayload == "" && pgpSignedMIME {
		// PGP/MIME multipart/signed: flagged, signature verification deferred
		m.IsSigned = true
		if m.SigStatus == "" {
			m.SigStatus = "unknown"
		}
	}
	if m.Body != "" && m.Body == html {
		// anti-tracking (M6): strip pixels + link trackers at ingest; count
		// the remote images the UI will gate behind consent. Only the HTML
		// part: escaping a clearsigned block would break its signature.
		clean, remote := SanitizeHTML(m.Body)
		m.Body = clean
		m.RemoteImages = remote
	}
	if m.Body == "" {
		m.Body = text // no HTML part: Body carries the plain text
	}
	if m.BodyText == "" || m.IsEncrypted {
		// PGP/Inline encrypted: BodyText stays empty until decryption; for
		// clearsigned text the extracted plaintext is already set
		if !m.IsEncrypted {
			m.BodyText = text
		}
	}
	if m.BodyText == "" && !m.IsEncrypted && m.Body != "" {
		m.BodyText = stripHTML(m.Body)
	}
	if m.Date.IsZero() {
		m.Date = time.Now().UTC()
	}
	return m, nil
}

func walkParts(r *mail.Reader, text, html *string, atts *[]domain.Attachment, encPayload *string, signedMIME *bool, depth int) {
	if depth > 10 { // nested-multipart cap
		return
	}
	for {
		p, err := r.NextPart()
		if fatal(err) {
			return
		}
		if ah, ok := p.Header.(*mail.AttachmentHeader); ok {
			name, _ := ah.Filename()
			if name == "" {
				name = "allegato"
			}
			n, _ := io.Copy(io.Discard, io.LimitReader(p.Body, 32<<20))
			ct := p.Header.Get("Content-Type")
			if i := strings.Index(ct, ";"); i >= 0 {
				ct = ct[:i]
			}
			*atts = append(*atts, domain.Attachment{
				Name:        name,
				Size:        n,
				ContentType: strings.TrimSpace(ct),
			})
			continue
		}
		rawCT := p.Header.Get("Content-Type")
		if rawCT == "" {
			rawCT = "text/plain" // RFC 2045 default when Content-Type is absent
		}
		ct, _, _ := stdmime.ParseMediaType(rawCT)
		switch {
		case ct == "multipart/encrypted":
			// PGP/MIME: second subpart carries the armored PGP MESSAGE; the
			// armor lives inside the raw multipart text — capture it whole
			b, _ := io.ReadAll(io.LimitReader(p.Body, 4<<20))
			if blk := extractBlock(string(b), armorBeginMessage, armorEndMessage); blk != "" {
				*encPayload = extractBlock(string(b), armorBeginMessage, armorEndMessage)
			}
		case ct == "multipart/signed":
			// PGP/MIME signed: flagged as signed; canonical-byte verification
			// is deferred (M9 verifies PGP/Inline clearsigns only)
			*signedMIME = true
			sub, err := mail.CreateReader(p.Body)
			if fatal(err) {
				return
			}
			walkParts(sub, text, html, atts, encPayload, signedMIME, depth+1)
		case strings.HasPrefix(ct, "multipart/"):
			// nested multipart: recurse with a sub-reader
			sub, err := mail.CreateReader(p.Body)
			if fatal(err) {
				return
			}
			walkParts(sub, text, html, atts, encPayload, signedMIME, depth+1)
		case ct == "text/plain":
			if *text == "" {
				b, _ := io.ReadAll(io.LimitReader(p.Body, 2<<20))
				*text = string(b)
			} else {
				io.Copy(io.Discard, io.LimitReader(p.Body, 1<<20))
			}
		case ct == "text/html":
			if *html == "" {
				b, _ := io.ReadAll(io.LimitReader(p.Body, 2<<20))
				*html = string(b)
			} else {
				io.Copy(io.Discard, io.LimitReader(p.Body, 1<<20))
			}
		default:
			io.Copy(io.Discard, io.LimitReader(p.Body, 1<<20))
		}
	}
}

func parseAddressList(h *mail.Header, key string) []domain.Recipient {
	list, err := h.AddressList(key)
	if err != nil {
		return nil
	}
	out := make([]domain.Recipient, 0, len(list))
	for _, a := range list {
		if a.Address == "" {
			continue
		}
		out = append(out, domain.Recipient{Name: a.Name, Email: a.Address})
	}
	return out
}

// splitMsgIDs splits a References/In-Reply-To header into message ids.
func splitMsgIDs(s string) []string {
	var out []string
	for _, part := range strings.Fields(s) {
		part = strings.Trim(part, "<>")
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// stripHTML converts an HTML body to plain text (fallback when no text/plain
// ExtractAttachment pulls one named attachment's bytes out of a raw RFC 5322
// message (first match wins). Used for on-demand downloads so raw bodies
// never have to be persisted locally.
func ExtractAttachment(raw []byte, name string) ([]byte, error) {
	reader, err := mail.CreateReader(bytes.NewReader(raw))
	if fatal(err) {
		return nil, fmt.Errorf("messaggio illeggibile: %w", err)
	}
	var found []byte
	var walk func(r *mail.Reader, depth int)
	walk = func(r *mail.Reader, depth int) {
		if found != nil || depth > 10 {
			return
		}
		for {
			p, err := r.NextPart()
			if fatal(err) {
				return
			}
			if ah, ok := p.Header.(*mail.AttachmentHeader); ok {
				fn, _ := ah.Filename()
				if fn == name {
					b, err := io.ReadAll(io.LimitReader(p.Body, 64<<20))
					if err != nil {
						return
					}
					found = b
					return
				}
				io.Copy(io.Discard, io.LimitReader(p.Body, 64<<20))
				continue
			}
			ct := p.Header.Get("Content-Type")
			if strings.HasPrefix(ct, "multipart/") {
				sub, err := mail.CreateReader(p.Body)
				if fatal(err) {
					return
				}
				walk(sub, depth+1)
				continue
			}
			io.Copy(io.Discard, io.LimitReader(p.Body, 2<<20))
		}
	}
	walk(reader, 0)
	if found == nil {
		return nil, fmt.Errorf("allegato %q non trovato", name)
	}
	return found, nil
}

// part exists). Tags are removed; the UI sanitizes HTML rendering separately.
func stripHTML(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// ContentType returns the declared content-type of a header value.
func ContentType(h string) string {
	ct, _, err := stdmime.ParseMediaType(h)
	if err != nil {
		return ""
	}
	return ct
}
// fatal reports whether a go-message error stops parsing. An unknown charset
// or transfer encoding comes with a usable reader/part (raw bytes): dropping
// it would lose the whole message, or every part after it.
func fatal(err error) bool {
	return err != nil && !message.IsUnknownCharset(err)
}
