package mbox

// mbox roundtrip tests (M11): escaping of body "From " lines survives an
// export/import cycle, separators are detected on read, plain messages pass
// through byte-faithful.

import (
	"bytes"
	"strings"
	"testing"
)

func TestAppendSplitRoundTrip(t *testing.T) {
	raw1 := []byte("Subject: uno\nFrom: a@b.c\n\nCorpo normale.\n")
	raw2 := []byte("Subject: due\nFrom: d@e.f\n\nRiga From che inizia cosi nel corpo:\nFrom someone@example si maschererebbe.\nDopo.\n")

	buf := &bytes.Buffer{}
	Append(buf, "From a@b.c Mon Aug 25 09:30:00 2026", raw1)
	Append(buf, "From d@e.f Tue Aug 26 10:00:00 2026", raw2)

	got := Split(buf.Bytes())
	if len(got) != 2 {
		t.Fatalf("Split = %d messaggi, attesi 2", len(got))
	}
	if !strings.Contains(string(got[0]), "Subject: uno") || !strings.Contains(string(got[0]), "Corpo normale.") {
		t.Fatalf("msg1 corrotto: %s", got[0])
	}
	// mboxrd escaping round-trip: the body "From " line must survive unescaped
	if !strings.Contains(string(got[1]), "From someone@example") {
		t.Fatalf("From line non ricomposta: %s", got[1])
	}
	if strings.Contains(string(got[1]), ">From someone@example") {
		t.Fatalf("escape non rimosso: %s", got[1])
	}
}

func TestEscapeAddsOneGtPerLevel(t *testing.T) {
	raw := []byte("From primario\n>>From doppio annidato\n")
	out := mboxrdEscape(raw)
	if !bytes.Contains(out, []byte(">From primario\n")) {
		t.Fatalf("From primario non escapato: %s", out)
	}
	if !bytes.Contains(out, []byte(">>>From doppio annidato\n")) {
		t.Fatalf(">>From non escapato: %s", out)
	}
}

func TestUnescapeStripsOneLevel(t *testing.T) {
	if got := mboxrdUnescape(">From singolo"); got != "From singolo" {
		t.Fatalf("unescape = %q", got)
	}
	if got := mboxrdUnescape(">>>From annidato"); got != ">>From annidato" {
		t.Fatalf("unescape annidato = %q", got)
	}
	if got := mboxrdUnescape("From senza escape"); got != "From senza escape" {
		t.Fatalf("linea normale modificata: %q", got)
	}
}

func TestSplitIgnoresPreamble(t *testing.T) {
	data := "testo spazzatura prima del primo separatore\nFrom a@b.c Mon Aug 25 09:30:00 2026\nSubject: x\n\ncorpo\n"
	got := Split([]byte(data))
	if len(got) != 1 {
		t.Fatalf("Split = %d messaggi", len(got))
	}
	if !strings.Contains(string(got[0]), "Subject: x") {
		t.Fatalf("messaggio mancante: %s", got[0])
	}
	if strings.Contains(string(got[0]), "spazzatura") {
		t.Fatal("preamble incluso per errore")
	}
}
