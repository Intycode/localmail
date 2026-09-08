// Package pdfdoc extracts plain text from PDF attachments (M12) so the
// local AI can read what the mail bodies alone do not say. Pure-Go parsing
// (ledongthuc/pdf, a maintained rsc.io/pdf fork): no OCR — a scanned PDF
// without a text layer yields an empty result and the caller notes it.
package pdfdoc

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/ledongthuc/pdf"
)

// MaxChars caps the extraction per PDF so one huge document cannot blow the
// IPC line budget; the caller sees truncated=true and may fetch more later.
const MaxChars = 100_000

var (
	spaceRun   = regexp.MustCompile(`[ \t]+`)
	blankLines = regexp.MustCompile(`\n{3,}`)
)

// Extract returns the normalized plain text of a PDF, capped at maxChars
// (<=0 means MaxChars). Per-page failures (rare fonts, broken content
// streams) are tolerated: whatever is readable is returned and truncated
// reports that content was dropped (cap hit or a page failed).
func Extract(data []byte, maxChars int) (text string, truncated bool, err error) {
	if maxChars <= 0 {
		maxChars = MaxChars
	}
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("%PDF-")) {
		return "", false, fmt.Errorf("il file non è un PDF valido")
	}
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", false, fmt.Errorf("pdf non leggibile: %w", err)
	}

	var b strings.Builder
	fonts := make(map[string]*pdf.Font)
	pages := reader.NumPage()
	for i := 1; i <= pages; i++ {
		page := reader.Page(i)
		for _, name := range page.Fonts() {
			if _, ok := fonts[name]; !ok {
				f := page.Font(name)
				fonts[name] = &f
			}
		}
		pageText, perr := page.GetPlainText(fonts)
		if perr != nil {
			truncated = true // unreadable page: keep what we can
			continue
		}
		if b.Len() >= maxChars {
			truncated = true
			break
		}
		b.WriteString(pageText)
		b.WriteString("\n")
		if b.Len() > maxChars {
			truncated = true
		}
	}
	out := normalize(b.String())
	if maxChars > 0 && len(out) > maxChars {
		out = out[:maxChars]
		truncated = true
	}
	return out, truncated, nil
}

// PageCount returns the number of pages in a PDF (0 when unreadable).
func PageCount(data []byte) int {
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("%PDF-")) {
		return 0
	}
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0
	}
	return reader.NumPage()
}

// normalize cleans the raw extraction for the prompt: unified newlines,
// collapsed space runs, at most one blank line between paragraphs.
func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = spaceRun.ReplaceAllString(s, " ")
	s = blankLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
