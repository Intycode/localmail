package pdfdoc

// Tests build minimal but VALID PDFs in memory (computed xref offsets) so
// extraction runs without fixtures or external tools (a text PDF built with
// a Helvetica text object).

import (
	"fmt"
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
