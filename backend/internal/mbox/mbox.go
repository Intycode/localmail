// Package mbox implements the classic mboxrd mailbox format (M11): several
// RFC5322 messages concatenated, each introduced by a "From " separator line,
// with body "From " lines escaped as ">From " on write and unescaped on read.
// Used by mailbox export and import so other mail clients can read what we
// export, and we can read what they export.
package mbox

import (
	"bufio"
	"bytes"
	"strings"
)

// separatorPrefix marks an mbox message separator line.
const separatorPrefix = "From "

// Append writes one message into the mbox stream: a "From " separator (with
// the envelope sender and current date when not known) followed by the raw
// message with mboxrd escaping applied.
func Append(buf *bytes.Buffer, fromLine string, raw []byte) {
	if fromLine == "" {
		fromLine = "From MAILER-DAEMON Thu Jan 01 00:00:00 1970"
	}
	buf.WriteString(fromLine)
	buf.WriteString("\n")
	esc := mboxrdEscape(raw)
	buf.Write(esc)
	if !bytes.HasSuffix(esc, []byte("\n")) {
		buf.WriteString("\n")
	}
	buf.WriteString("\n")
}

// mboxrdEscape prefixes every line matching ^>*"From " with one more ">"
// (mboxrd: ">From ", ">>From ", ... round-trip safely).
func mboxrdEscape(raw []byte) []byte {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	out := &bytes.Buffer{}
	for sc.Scan() {
		line := sc.Bytes()
		i := 0
		for i < len(line) && line[i] == '>' {
			i++
		}
		if bytes.HasPrefix(line[i:], []byte("From ")) {
			out.WriteByte('>')
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// mboxrdUnescape reverses mboxrdEscape: strips exactly one leading ">" from
// lines matching ^>+"From " (one or more '>' followed by From).
func mboxrdUnescape(line string) string {
	i := 0
	for i < len(line) && line[i] == '>' {
		i++
	}
	if i > 0 && strings.HasPrefix(line[i:], "From ") {
		return line[1:]
	}
	return line
}

// Split breaks an mbox file into raw messages (bytes.Reader friendly).
// The "From " separators are removed and mboxrd escapes undone. Lenient on
// malformed input: text before the first separator is ignored.
func Split(data []byte) [][]byte {
	var out [][]byte
	var cur *bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "From ") && (cur == nil || isSeparator(line)) {
			// naive rule: any "From " line that starts a message. Real mbox
			// relies on the writer escaping; mboxrd guarantees ">From " for
			// body lines, so an unescaped "From " IS a separator.
			if cur != nil {
				out = append(out, cur.Bytes())
			}
			cur = &bytes.Buffer{}
			continue
		}
		if cur == nil {
			continue // preamble junk
		}
		cur.WriteString(mboxrdUnescape(line))
		cur.WriteString("\n")
	}
	if cur != nil {
		out = append(out, cur.Bytes())
	}
	return out
}

// isSeparator distinguishes a genuine "From " separator from a body line the
// writer forgot to escape: separators look like "From <addr> <asctime>".
func isSeparator(line string) bool {
	// count spaces: "From addr weekday mon dd hh:mm:ss yyyy" has >= 6 fields
	fields := strings.Fields(line)
	return len(fields) >= 3
}
