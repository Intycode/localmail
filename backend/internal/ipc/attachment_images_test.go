package ipc

// attachment.images (M12 vision path): a scanned PDF (image XObject only,
// no text layer) is rasterized by the engine and returned as JPEG pages for
// the local multimodal model. Runs through the dedicated attachment_images
// Rust command.

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// buildScannedPDFBytes assembles a one-page PDF made of a single 8x8 RGB
// image XObject (flate-compressed raw pixels): pixels only, no text layer.
func buildScannedPDFBytes() []byte {
	const w, h = 8, 8
	raw := make([]byte, 0, w*h*3)
	for y := range h {
		for x := range w {
			raw = append(raw, uint8(x*30), uint8(y*30), 128)
		}
	}
	var flateBuf bytes.Buffer
	fw, _ := flate.NewWriter(&flateBuf, flate.DefaultCompression)
	_, _ = fw.Write(raw)
	_ = fw.Close()
	img := flateBuf.Bytes()

	content := "q 8 0 0 8 0 0 cm /Im0 Do Q"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 8 8] /Contents 4 0 R /Resources << /XObject << /Im0 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n", w, h, len(img)) + string(img) + "\nendstream",
	}

	var b strings.Builder
	offsets := make([]int, len(objects)+1)
	b.WriteString("%PDF-1.4\n")
	for i, obj := range objects {
		offsets[i+1] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n", len(objects)+1)
	b.WriteString("0000000000 65535 f \n")
	for _, off := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return []byte(b.String())
}

func scannedPDFMail() string {
	b64 := base64.StdEncoding.EncodeToString(buildScannedPDFBytes())
	return "From: Anna <anna@acme.dev>\r\n" +
		"To: luca@x.dev\r\nSubject: Scansione ricevuta\r\n" +
		"Date: Wed, 27 Aug 2026 11:00:00 +0200\r\nMessage-Id: <scansione@acme.dev>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=BOUND\r\n\r\n" +
		"--BOUND\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Ti giro la scansione.\r\n" +
		"--BOUND\r\nContent-Type: application/pdf; name=\"scansione.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"scansione.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		b64 + "\r\n" +
		"--BOUND--\r\n"
}

func TestAttachmentImagesRendersScannedPDF(t *testing.T) {
	env := startEnv(t)
	msgID := syncOneAttachment(t, env, scannedPDFMail(), "scansione ricevuta", "scansione.pdf")

	ok, data, errMsg := env.call(t, "v1", "attachment.images", map[string]any{
		"messageId": msgID, "name": "scansione.pdf", "secret": secret,
	})
	if !ok {
		t.Fatalf("attachment.images: %s", errMsg)
	}
	var res struct {
		Name      string `json:"name"`
		Rendered  int    `json:"rendered"`
		TotalPage int    `json:"totalPage"`
		Pages     []struct {
			Index int    `json:"index"`
			MIME  string `json:"mime"`
			Data  []byte `json:"data"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if res.Rendered != 1 || res.TotalPage != 1 || len(res.Pages) != 1 {
		t.Fatalf("pages = %+v", res)
	}
	if res.Pages[0].MIME != "image/jpeg" || !bytes.HasPrefix(res.Pages[0].Data, []byte{0xFF, 0xD8}) {
		t.Fatalf("output non JPEG: mime=%s head=%x", res.Pages[0].MIME, res.Pages[0].Data[:2])
	}
}

func TestAttachmentImagesRejectsNonPDF(t *testing.T) {
	env := startEnv(t)
	msgID := syncOneAttachment(t, env, mailWithAttachment, "contratto", "contratto.pdf")
	// the fixture's fake .pdf has the header but no valid body: the render
	// must refuse it with a clear error instead of returning garbage pages
	ok, _, errMsg := env.call(t, "v2", "attachment.images", map[string]any{
		"messageId": msgID, "name": "contratto.pdf", "secret": secret,
	})
	if ok || !(strings.Contains(errMsg, "PDF") || strings.Contains(errMsg, "pdf")) {
		t.Fatalf("non-PDF accettato: ok=%v err=%s", ok, errMsg)
	}
}
