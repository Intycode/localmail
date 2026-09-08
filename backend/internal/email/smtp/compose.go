// Compose helpers: quoted-printable body encoding and CRLF normalization.
package smtp

import (
	"io"
	"mime/quotedprintable"
	"strings"
)

// quotedPrintableWriter returns a writer encoding the body as
// Content-Transfer-Encoding: quoted-printable (RFC 2045), writing into b.
func quotedPrintableWriter(b *strings.Builder) io.WriteCloser {
	return quotedprintable.NewWriter(&builderWriter{b: b})
}

// writeQuotedPrintable appends s to b as quoted-printable (RFC 2045) text.
func writeQuotedPrintable(b *strings.Builder, s string) {
	qp := quotedPrintableWriter(b)
	_, _ = qp.Write([]byte(normalizeCRLF(s)))
	_ = qp.Close()
}

// builderWriter adapts strings.Builder to io.Writer (Write must not fail).
type builderWriter struct{ b *strings.Builder }

func (w *builderWriter) Write(p []byte) (int, error) {
	w.b.Write(p)
	return len(p), nil
}

// normalizeCRLF converts bare LF (and lone CR) into CRLF as RFC 5322
// requires for the wire format.
func normalizeCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}
