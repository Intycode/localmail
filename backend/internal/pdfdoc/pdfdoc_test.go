package pdfdoc

// Tests build minimal but VALID PDFs in memory (computed xref offsets) so
// both extraction contracts run without fixtures or external tools:
//   - a text PDF (text object with Helvetica);
//   - a "scanned" PDF (image XObject only, no text layer) for the vision
//     rendering path.

import (
	"bytes"
	"compress/flate"
	"fmt"
	"image/color"
	"strings"
	"testing"
)

func buildPDFFromObjects(objects []string) []byte {
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

// buildPDF assembles a one-page PDF whose text object draws the given lines
// with Helvetica. Offsets for the xref table are computed while writing.
func buildPDF(t *testing.T, lines []string) []byte {
	t.Helper()
	var stream strings.Builder
	stream.WriteString("BT /F1 12 Tf 72 700 Td\n")
	for i, l := range lines {
		if i > 0 {
			stream.WriteString("T* 0 -16 Td\n")
		}
		stream.WriteString("(" + l + ") Tj\n")
	}
	stream.WriteString("ET")
	body := stream.String()

	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(body), body),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	return buildPDFFromObjects(objects)
}

// buildScannedPDF assembles a one-page PDF made of a single 8x8 RGB image
// XObject (flate-compressed raw pixels): a real "scanned" page — no text
// layer, just pixels. Used by the vision rendering tests.
func buildScannedPDF(t *testing.T) []byte {
	t.Helper()
	const w, h = 8, 8
	raw := make([]byte, 0, w*h*3)
	for y := range h {
		for x := range w {
			c := color.RGBA{R: uint8(x * 30), G: uint8(y * 30), B: 128, A: 255}
			raw = append(raw, c.R, c.G, c.B)
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
	return buildPDFFromObjects(objects)
}

func TestExtractReadsText(t *testing.T) {
	pdf := buildPDF(t, []string{"Ciao dal PDF di prova", "Seconda riga: preventivo 1.200 EUR"})
	text, truncated, err := Extract(pdf, 0)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("truncated su PDF piccolo")
	}
	for _, want := range []string{"Ciao dal PDF di prova", "preventivo 1.200 EUR"} {
		if !strings.Contains(text, want) {
			t.Fatalf("testo mancante %q in %q", want, text)
		}
	}
}

func TestExtractCapsAndReportsTruncation(t *testing.T) {
	pdf := buildPDF(t, []string{"abcdefghij", "KLMNOPQRST", "uvwxyz"})
	text, truncated, err := Extract(pdf, 12)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("cap non segnalato")
	}
	if len(text) > 12 {
		t.Fatalf("testo oltre il cap: %d", len(text))
	}
}

func TestExtractScanLikePDFIsEmpty(t *testing.T) {
	// a valid page with an empty content stream: no text layer at all
	pdf := buildPDF(t, nil)
	text, truncated, err := Extract(pdf, 0)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || text != "" {
		t.Fatalf("scan-like deve dare testo vuoto: %q truncated=%v", text, truncated)
	}
}

func TestExtractRejectsNonPDF(t *testing.T) {
	if _, _, err := Extract([]byte("questo non è un pdf"), 0); err == nil {
		t.Fatal("input non-PDF accettato")
	}
}

func TestRenderPagesScannedPDF(t *testing.T) {
	pdf := buildScannedPDF(t)

	// the same document yields NO text: it is genuinely "scanned"
	text, _, err := Extract(pdf, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(text) != "" {
		t.Fatalf("un PDF solo-immagine non deve dare testo: %q", text)
	}

	pages, err := RenderPages(pdf, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages = %d", len(pages))
	}
	if pages[0].Index != 0 || pages[0].MIME != "image/jpeg" {
		t.Fatalf("page meta: %+v", pages[0])
	}
	if !bytes.HasPrefix(pages[0].Data, []byte{0xFF, 0xD8}) {
		t.Fatalf("output non JPEG: %x", pages[0].Data[:4])
	}
}

func TestRenderPagesCapsAndRejects(t *testing.T) {
	pdf := buildScannedPDF(t)
	pages, err := RenderPages(pdf, RenderOptions{MaxPages: 1})
	if err != nil || len(pages) != 1 {
		t.Fatalf("pages = %d err=%v", len(pages), err)
	}
	if _, err := RenderPages([]byte("non è un pdf"), RenderOptions{}); err == nil {
		t.Fatal("input non-PDF accettato")
	}
	// a text PDF renders too (text layer or not, pages are pages)
	pages, err = RenderPages(buildPDF(t, []string{"hello"}), RenderOptions{})
	if err != nil || len(pages) != 1 {
		t.Fatalf("text pdf render: %d err=%v", len(pages), err)
	}
}
