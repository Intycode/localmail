package ipc

// attachment.text (M12): the local AI reads PDF attachments. E2E over the
// fake IMAP server — a multipart message with a REAL minimal PDF lands on
// the server, the sync stores the metadata, and attachment.text re-downloads
// the raw, extracts the part and returns its plain text. Non-PDF parts and
// unknown names are rejected.

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	goimap "github.com/emersion/go-imap/v2"
)

// base64 of a valid minimal one-page PDF reading two lines (generated once,
// stable content asserted below).
const minimalPDFB64 = `JVBERi0xLjQKMSAwIG9iago8PCAvVHlwZSAvQ2F0YWxvZyAvUGFnZXMgMiAwIFIgPj4KZW5kb2Jq
CjIgMCBvYmoKPDwgL1R5cGUgL1BhZ2VzIC9LaWRzIFszIDAgUl0gL0NvdW50IDEgPj4KZW5kb2Jq
CjMgMCBvYmoKPDwgL1R5cGUgL1BhZ2UgL1BhcmVudCAyIDAgUiAvTWVkaWFCb3ggWzAgMCA2MTIg
NzkyXSAvQ29udGVudHMgNCAwIFIgL1Jlc291cmNlcyA8PCAvRm9udCA8PCAvRjEgNSAwIFIgPj4g
Pj4gPj4KZW5kb2JqCjQgMCBvYmoKPDwgL0xlbmd0aCAxMDIgPj4Kc3RyZWFtCkJUIC9GMSAxMiBU
ZiA3MiA3MDAgVGQKKFByZXZlbnRpdm8gbi4gNDIpIFRqClQqIDAgLTE2IFRkCihUb3RhbGU6IDEu
MjAwIEVVUiwgdmFsaWRpdGEgMzAgZ2lvcm5pKSBUagpFVAplbmRzdHJlYW0KZW5kb2JqCjUgMCBv
YmoKPDwgL1R5cGUgL0ZvbnQgL1N1YnR5cGUgL1R5cGUxIC9CYXNlRm9udCAvSGVsdmV0aWNhID4+
CmVuZG9iagp4cmVmCjAgNgowMDAwMDAwMDAwIDY1NTM1IGYgCjAwMDAwMDAwMDkgMDAwMDAgbiAK
MDAwMDAwMDA1OCAwMDAwMCBuIAowMDAwMDAwMTE1IDAwMDAwIG4gCjAwMDAwMDAyNDEgMDAwMDAg
biAKMDAwMDAwMDM5NCAwMDAwMCBuIAp0cmFpbGVyCjw8IC9TaXplIDYgL1Jvb3QgMSAwIFIgPj4K
c3RhcnR4cmVmCjQ2NAolJUVPRgo=`

func pdfAttachmentMail() string {
	return "From: Anna <anna@acme.dev>\r\n" +
		"To: luca@x.dev\r\nSubject: Preventivo allegato\r\n" +
		"Date: Wed, 27 Aug 2026 10:00:00 +0200\r\nMessage-Id: <preventivo@acme.dev>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=BOUND\r\n\r\n" +
		"--BOUND\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		"In allegato il preventivo.\r\n" +
		"--BOUND\r\nContent-Type: application/pdf; name=\"preventivo.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"preventivo.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		minimalPDFB64 + "\r\n" +
		"--BOUND--\r\n"
}

// syncOneAttachment adds the account, appends the multipart message and syncs;
// it returns the message id carrying the attachment.
func syncOneAttachment(t *testing.T, env *testEnv, mail, subject, attachmentName string) string {
	t.Helper()
	host, portRaw, _ := net.SplitHostPort(env.addr)
	port, _ := strconv.Atoi(portRaw)
	ok, data, errMsg := env.call(t, "s0", "accounts.add", map[string]any{
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

	if _, err := env.usr.Append("INBOX", &testLiteral{r: strings.NewReader(mail)}, &goimap.AppendOptions{}); err != nil {
		t.Fatalf("append: %v", err)
	}
	ok, _, errMsg = env.call(t, "s1", "sync.start", map[string]any{"accountId": acc.ID, "secret": secret})
	if !ok {
		t.Fatalf("sync.start: %s", errMsg)
	}
	env.waitEvent(t, "sync.completed", 15*time.Second)

	// find the message via the inbox folder listing
	ok, data, errMsg = env.call(t, "s3", "folders.list", map[string]any{"accountId": acc.ID})
	if !ok {
		t.Fatalf("folders.list: %s", errMsg)
	}
	var folders []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &folders); err != nil {
		t.Fatal(err)
	}
	inbox := ""
	for _, f := range folders {
		if f.Type == "inbox" {
			inbox = f.ID
		}
	}
	if inbox == "" {
		t.Fatalf("INBOX non trovata: %s", data)
	}
	ok, data, errMsg = env.call(t, "s4", "threads.list", map[string]any{"folderId": inbox})
	if !ok {
		t.Fatalf("threads.list: %s", errMsg)
	}
	var threads []struct {
		Subject    string   `json:"subject"`
		MessageIDs []string `json:"messageIds"`
	}
	if err := json.Unmarshal(data, &threads); err != nil {
		t.Fatal(err)
	}
	for _, th := range threads {
		if strings.Contains(strings.ToLower(th.Subject), strings.ToLower(subject)) &&
			len(th.MessageIDs) > 0 {
			return th.MessageIDs[0]
		}
	}
	t.Fatalf("messaggio con allegato %q non trovato: %s", attachmentName, data)
	return ""
}

func TestAttachmentTextExtractsPDF(t *testing.T) {
	env := startEnv(t)
	msgID := syncOneAttachment(t, env, pdfAttachmentMail(), "preventivo allegato", "preventivo.pdf")

	ok, data, errMsg := env.call(t, "t1", "attachment.text", map[string]any{
		"messageId": msgID, "name": "preventivo.pdf", "secret": secret,
	})
	if !ok {
		t.Fatalf("attachment.text: %s", errMsg)
	}
	var res struct {
		Name      string `json:"name"`
		Text      string `json:"text"`
		Chars     int    `json:"chars"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if res.Truncated || res.Chars == 0 {
		t.Fatalf("estrazione degradata: %+v", res)
	}
	if !strings.Contains(res.Text, "Preventivo n. 42") || !strings.Contains(res.Text, "1.200 EUR") {
		t.Fatalf("testo PDF mancante: %q", res.Text)
	}
}

func TestAttachmentTextRejectsNonPDF(t *testing.T) {
	env := startEnv(t)
	msgID := syncOneAttachment(t, env, mailWithAttachment, "contratto", "contratto.pdf")
	// the fake .pdf in the shared fixture is not a real PDF: extraction fails
	// with a clear error instead of garbage
	ok, _, errMsg := env.call(t, "t2", "attachment.text", map[string]any{
		"messageId": msgID, "name": "contratto.pdf", "secret": secret,
	})
	if ok || !strings.Contains(errMsg, "PDF") {
		t.Fatalf("PDF finto accettato: ok=%v err=%s", ok, errMsg)
	}
}

func TestAttachmentTextRequiresKnownAttachment(t *testing.T) {
	env := startEnv(t)
	msgID := syncOneAttachment(t, env, pdfAttachmentMail(), "preventivo allegato", "preventivo.pdf")
	ok, _, errMsg := env.call(t, "t3", "attachment.text", map[string]any{
		"messageId": msgID, "name": "inesistente.pdf", "secret": secret,
	})
	if ok || !strings.Contains(errMsg, "non presente") {
		t.Fatalf("allegato sconosciuto accettato: ok=%v err=%s", ok, errMsg)
	}
}
