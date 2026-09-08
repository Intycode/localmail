package ipc

// Attachment flow: a multipart message lands on the server, the sync stores
// its attachment metadata, and attachment.save re-downloads the raw message,
// extracts the part and writes it to a client-chosen path.

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	goimap "github.com/emersion/go-imap/v2"
)

const mailWithAttachment = "From: Anna <anna@acme.dev>\r\n" +
	"To: luca@x.dev\r\nSubject: Contratto\r\n" +
	"Date: Wed, 27 Aug 2026 10:00:00 +0200\r\nMessage-Id: <contratto@acme.dev>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=BOUND\r\n\r\n" +
	"--BOUND\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
	"In allegato il contratto.\r\n" +
	"--BOUND\r\nContent-Type: application/pdf; name=\"contratto.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"contratto.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\n" +
	"JVBERi0xLjQKJSBUZXN0IFBERg==\r\n" +
	"--BOUND--\r\n"

func TestAttachmentSave(t *testing.T) {
	env := startEnv(t)
	host, portRaw, _ := net.SplitHostPort(env.addr)
	port, _ := strconv.Atoi(portRaw)

	ok, data, errMsg := env.call(t, "a0", "accounts.add", map[string]any{
		"name": "Luca", "email": "luca@x.dev",
		"imap":          map[string]any{"host": host, "port": port, "security": "plain", "username": "luca"},
		"secret":        secret,
		"allowInsecure": true,
	})
	if !ok {
		t.Fatalf("accounts.add: %s", errMsg)
	}
	var acc struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(data, &acc)

	// deliver a message with an attachment on the server before syncing
	if _, err := env.usr.Append("INBOX", &testLiteral{r: strings.NewReader(mailWithAttachment)}, &goimap.AppendOptions{}); err != nil {
		t.Fatalf("append: %v", err)
	}

	ok, _, errMsg = env.call(t, "a1", "sync.start", map[string]any{"accountId": acc.ID, "secret": secret})
	if !ok {
		t.Fatalf("sync.start: %s", errMsg)
	}
	env.waitEvent(t, "sync.completed", 15*time.Second)

	// the synced message lists the attachment
	ok, data, errMsg = env.call(t, "a2a", "folders.list", map[string]any{"accountId": acc.ID})
	if !ok {
		t.Fatalf("folders.list: %s", errMsg)
	}
	var fl struct {
		Folders []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"folders"`
	}
	// folders.list answers with a bare array
	if err := json.Unmarshal(data, &fl.Folders); err != nil {
		t.Fatal(err)
	}
	inboxID := ""
	for _, f := range fl.Folders {
		if f.Type == "inbox" {
			inboxID = f.ID
		}
	}
	if inboxID == "" {
		t.Fatalf("cartella INBOX non trovata: %s", data)
	}

	ok, data, errMsg = env.call(t, "a2", "threads.list", map[string]any{"folderId": inboxID})
	if !ok {
		t.Fatalf("threads.list: %s", errMsg)
	}
	var res []struct {
		ID             string `json:"id"`
		HasAttachments bool   `json:"hasAttachments"`
		Subject        string `json:"subject"`
		MessageIDs     []string `json:"messageIds"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	var msgID string
	for _, th := range res {
		if strings.Contains(strings.ToLower(th.Subject), "contratto") {
			msgID = th.MessageIDs[0]
			break
		}
	}
	if msgID == "" {
		t.Fatalf("messaggio con allegato non trovato: %s", data)
	}
	ok, data, errMsg = env.call(t, "a3", "message.get", map[string]any{"messageId": msgID})
	if !ok {
		t.Fatalf("message.get: %s", errMsg)
	}
	if !strings.Contains(string(data), "contratto.pdf") {
		t.Fatalf("metadati allegato mancanti: %s", data)
	}

	// attachment.save writes the decoded bytes
	dest := filepath.Join(t.TempDir(), "contratto.pdf")
	ok, data, errMsg = env.call(t, "a4", "attachment.save", map[string]any{
		"messageId": msgID, "name": "contratto.pdf", "dest": dest, "secret": secret,
	})
	if !ok {
		t.Fatalf("attachment.save: %s", errMsg)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("file non scritto: %v", err)
	}
	want, _ := base64.StdEncoding.DecodeString("JVBERi0xLjQKJSBUZXN0IFBERg==")
	if string(got) != string(want) {
		t.Fatalf("contenuto allegato errato: %q", got)
	}
}
