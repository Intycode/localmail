package smtp

import (
	"bytes"
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mail/backend/internal/domain"
	enginemime "mail/backend/internal/email/mime"
)

// writeTemp creates a temp file with the given content and returns its path.
func writeTemp(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return path
}

func TestLoadFilesFillsDataAndDefaults(t *testing.T) {
	pdf := writeTemp(t, "report.pdf", []byte("%PDF-1.4 fake"))
	txt := writeTemp(t, "note.txt", []byte("ciao"))

	msg := domain.ComposeMessage{
		Files: []domain.OutgoingFile{
			{Path: pdf},
			{Path: txt, Name: "appunto.txt", ContentType: "text/plain"},
		},
	}
	if err := LoadFiles(&msg); err != nil {
		t.Fatalf("LoadFiles: %v", err)
	}
	f0, f1 := msg.Files[0], msg.Files[1]
	if string(f0.Data) != "%PDF-1.4 fake" {
		t.Errorf("data not loaded: %q", f0.Data)
	}
	if f0.Name != "report.pdf" {
		t.Errorf("name default: %q", f0.Name)
	}
	if f0.ContentType != "application/pdf" {
		t.Errorf("content type inference: %q", f0.ContentType)
	}
	if f1.Name != "appunto.txt" || f1.ContentType != "text/plain" {
		t.Errorf("explicit fields overwritten: %+v", f1)
	}
	if string(f1.Data) != "ciao" {
		t.Errorf("second data not loaded: %q", f1.Data)
	}
}

func TestLoadFilesErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		msg := domain.ComposeMessage{Files: []domain.OutgoingFile{{Path: filepath.Join(t.TempDir(), "nope.bin")}}}
		err := LoadFiles(&msg)
		if err == nil || !strings.Contains(err.Error(), "nope.bin") {
			t.Fatalf("want readable error, got %v", err)
		}
	})
	t.Run("directory", func(t *testing.T) {
		msg := domain.ComposeMessage{Files: []domain.OutgoingFile{{Path: t.TempDir()}}}
		if err := LoadFiles(&msg); err == nil {
			t.Fatal("want error for directory")
		}
	})
	t.Run("oversized", func(t *testing.T) {
		big := writeTemp(t, "big.bin", bytes.Repeat([]byte{0}, maxFileBytes+1))
		msg := domain.ComposeMessage{Files: []domain.OutgoingFile{{Path: big}}}
		err := LoadFiles(&msg)
		if err == nil || !strings.Contains(err.Error(), "troppo grande") {
			t.Fatalf("want size error, got %v", err)
		}
	})
	t.Run("empty path", func(t *testing.T) {
		msg := domain.ComposeMessage{Files: []domain.OutgoingFile{{Path: "  "}}}
		if err := LoadFiles(&msg); err == nil {
			t.Fatal("want error for empty path")
		}
	})
	t.Run("no files is a no-op", func(t *testing.T) {
		msg := domain.ComposeMessage{}
		if err := LoadFiles(&msg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestComposeMultipartMixed(t *testing.T) {
	content := bytes.Repeat([]byte{0xCA, 0xFE, 0x00, 0x01}, 300) // 1200 bytes, binary
	msg := domain.ComposeMessage{
		To:      []domain.Recipient{{Email: "anna@acme.dev"}},
		Subject: "Con allegato",
		Body:    "testo del messaggio",
		Files: []domain.OutgoingFile{{
			Path:        "/tmp/report.bin",
			Name:        "report.bin",
			ContentType: "application/octet-stream",
			Data:        content,
		}},
	}
	raw := Compose(domain.Recipient{Email: "luca@x.dev"}, msg)
	s := string(raw)

	if !strings.Contains(s, `Content-Type: multipart/mixed; boundary="`) {
		t.Fatalf("missing multipart/mixed header:\n%s", s)
	}
	if !strings.Contains(s, "Content-Disposition: attachment; filename=\"report.bin\"") {
		t.Errorf("missing attachment disposition")
	}
	if !strings.Contains(s, `Content-Type: application/octet-stream; name="report.bin"`) {
		t.Errorf("missing content type header with name")
	}
	if !strings.Contains(s, "Content-Transfer-Encoding: base64") {
		t.Errorf("missing base64 CTE")
	}
	if !strings.Contains(s, "testo del messaggio") {
		t.Errorf("body part missing")
	}
	if strings.Contains(s, "Content-Type: multipart/alternative") {
		t.Errorf("unexpected alternative part without HTMLBody")
	}

	// the base64 payload must decode back to the original bytes
	wantB64 := base64.StdEncoding.EncodeToString(content)
	if !strings.Contains(s, chunkLines(wantB64)) {
		t.Errorf("base64 payload not found in message")
	}
	// header order: mixed must come after MIME-Version, body before first file
	mi := strings.Index(s, "multipart/mixed")
	bi := strings.Index(s, "testo del messaggio")
	ai := strings.Index(s, "Content-Disposition: attachment")
	if mi < 0 || bi < mi || ai < bi {
		t.Errorf("bad part order: mixed=%d body=%d attach=%d", mi, bi, ai)
	}
}

func TestComposeMixedWithAlternative(t *testing.T) {
	msg := domain.ComposeMessage{
		To:       []domain.Recipient{{Email: "anna@acme.dev"}},
		Subject:  "HTML con allegato",
		Body:     "testo",
		HTMLBody: "<p>testo</p>",
		Files: []domain.OutgoingFile{{
			Path:        "/tmp/a.txt",
			Name:        "a.txt",
			ContentType: "text/plain",
			Data:        []byte("contenuto"),
		}},
	}
	raw := Compose(domain.Recipient{Email: "luca@x.dev"}, msg)
	s := string(raw)
	if !strings.Contains(s, "multipart/mixed") || !strings.Contains(s, "multipart/alternative") {
		t.Fatalf("want nested alternative inside mixed:\n%s", s)
	}
	if !strings.Contains(s, base64.StdEncoding.EncodeToString([]byte("contenuto"))) {
		// attachment bytes go out base64-encoded: the literal text cannot appear
		t.Errorf("attachment content missing")
	}
	if !strings.Contains(s, "<p>testo</p>") {
		t.Errorf("html alternative part missing")
	}
}

// chunkLines re-chunks a base64 string into 76-char lines joined by CRLF,
// mirroring writeBase64, so tests can locate the payload in the raw message.
func chunkLines(b64 string) string {
	var b strings.Builder
	for len(b64) > 76 {
		b.WriteString(b64[:76])
		b.WriteString("\r\n")
		b64 = b64[76:]
	}
	b.WriteString(b64)
	return b.String()
}

func TestSendMessageWithAttachmentEndToEnd(t *testing.T) {
	f := startFakeSMTP(t, "PLAIN", "luca@x.dev", "secret")
	host, portRaw, _ := net.SplitHostPort(f.Addr())
	port := 0
	for _, c := range portRaw {
		port = port*10 + int(c-'0')
	}
	cfg := Config{Host: host, Port: port, Security: SecurityPlain, Username: "luca@x.dev"}

	path := writeTemp(t, "fattura.pdf", []byte("%PDF-fattura-di-prova"))
	msg := domain.ComposeMessage{
		To:      []domain.Recipient{{Email: "anna@acme.dev"}},
		Subject: "Allegato e2e",
		Body:    "vedi allegato",
		Files:   []domain.OutgoingFile{{Path: path}},
	}
	if err := LoadFiles(&msg); err != nil {
		t.Fatalf("LoadFiles: %v", err)
	}
	raw, err := SendMessage(cfg, "secret", true,
		domain.Recipient{Email: "luca@x.dev"}, msg)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	got := f.mu.msg
	if !strings.Contains(got, "multipart/mixed") {
		t.Fatalf("server did not receive a multipart message")
	}
	// the engine parser must see the attachment (used by the local Sent copy)
	parsed, err := enginemime.Parse(raw)
	if err != nil {
		t.Fatalf("parse sent raw: %v", err)
	}
	if len(parsed.Attachments) != 1 || parsed.Attachments[0].Name != "fattura.pdf" {
		t.Fatalf("parser lost the attachment: %+v", parsed.Attachments)
	}
}

func TestLoadFilesContentTypeCharsetStripped(t *testing.T) {
	txt := writeTemp(t, "doc.md", []byte("# titolo"))
	msg := domain.ComposeMessage{Files: []domain.OutgoingFile{{Path: txt}}}
	if err := LoadFiles(&msg); err != nil {
		t.Fatalf("LoadFiles: %v", err)
	}
	if strings.Contains(msg.Files[0].ContentType, ";") {
		t.Errorf("content type carries parameters: %q", msg.Files[0].ContentType)
	}
}

func TestComposeInlineImagesBecomeCid(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 1, 2, 3}
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	msg := domain.ComposeMessage{
		To:       []domain.Recipient{{Email: "anna@acme.dev"}},
		Subject:  "Firma",
		Body:     "ciao",
		HTMLBody: `<p>ciao</p><img src="` + uri + `" alt=""><img src="` + uri + `" alt="">`,
	}
	raw := Compose(domain.Recipient{Email: "luca@x.dev"}, msg)
	s := string(raw)
	if strings.Contains(s, "data:image") {
		t.Errorf("data: URI left in the message")
	}
	if !strings.Contains(s, `Content-Type: multipart/related; boundary="`) {
		t.Fatalf("missing multipart/related:\n%s", s)
	}
	if n := strings.Count(s, "Content-ID: <img1."); n != 1 {
		t.Errorf("identical images must share one part, got %d", n)
	}
	if !strings.Contains(s, "Content-Disposition: inline; filename=\"image1.png\"") {
		t.Errorf("missing inline disposition")
	}
	if !strings.Contains(s, base64.StdEncoding.EncodeToString(png)) {
		t.Errorf("image payload missing")
	}
	// the result must still parse, with the html pointing at the cid part
	parsed, err := enginemime.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(parsed.Body, `src="cid:img1.`) {
		t.Errorf("html does not reference the cid: %q", parsed.Body)
	}
	if len(parsed.Attachments) != 0 {
		t.Errorf("inline image listed as attachment: %+v", parsed.Attachments)
	}
}
