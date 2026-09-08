package search

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mail/backend/internal/database"
	"mail/backend/internal/domain"
)

func TestFtsSearch(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// seed account + folder first (messages reference them via FK)
	if _, err := db.Exec(`INSERT INTO accounts(id,name,email,provider,active,created_at)
		VALUES('a1','Luca','luca@example.com','mock',1,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO folders(id,account,name,type,total,unread)
		VALUES('f-inbox','a1','Inbox','inbox',0,0)`); err != nil {
		t.Fatalf("seed folder: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO threads(id,folder,account,subject,snippet,last_date,is_unread,is_starred,message_count,has_attach)
		VALUES('t1','f-inbox','a1','Report Q3','','2026-08-25T09:00:00Z',0,0,1,0)`); err != nil {
		t.Fatalf("seed thread: %v", err)
	}
	// seed a message; triggers populate mail_fts
	if _, err := db.Exec(`INSERT INTO messages(id,thread,folder,account,subject,from_name,from_email,body,body_text,date)
		VALUES('m1','t1','f-inbox','a1','Report Q3','Anna','anna@acme.dev','Il report e pronto','Il report e pronto','2026-08-25T09:00:00Z')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO recipients(message,kind,name,email)
		VALUES('m1','to','Luca','luca@example.com')`); err != nil {
		t.Fatalf("seed recipient: %v", err)
	}

	st := New(db)
	res, err := st.Query(domain.SearchQuery{Query: "report"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(res) != 1 || res[0].Subject != "Report Q3" {
		t.Errorf("unexpected results %+v", res)
	}
	if res[0].Matched != "subject" {
		t.Errorf("matched = %q", res[0].Matched)
	}
}

// TestSearchFilters verifies that the SearchQuery filters (folder, account,
// date range) and the recipient match are honored, not just the free text.
func TestSearchFilters(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "filters.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	seed := func(id, folder, acc, subject, body, fromEmail, toEmail, date string) {
		t.Helper()
		if _, err := db.Exec(
			`INSERT INTO messages(id,thread,folder,account,subject,from_name,from_email,body,body_text,date)
			 VALUES(?,?,?,?,?,?,?,?,?,?)`,
			id, "t-"+id, folder, acc, subject, "Nome", fromEmail, body, body, date,
		); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		if _, err := db.Exec(
			`INSERT INTO recipients(message,kind,name,email) VALUES(?,'to','Nome',?)`, id, toEmail,
		); err != nil {
			t.Fatalf("seed recipient %s: %v", id, err)
		}
	}

	for _, a := range []struct{ id, email string }{{"a1", "luca@x.dev"}, {"a2", "bo@y.dev"}} {
		if _, err := db.Exec(
			`INSERT INTO accounts(id,name,email,provider,active,created_at) VALUES(?,?,?,'mock',0,'2026-01-01T00:00:00Z')`,
			a.id, a.id, a.email,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO folders(id,account,name,type) VALUES('f-inbox','a1','Inbox','inbox')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO folders(id,account,name,type) VALUES('f-inbox-2','a2','Inbox','inbox')`); err != nil {
		t.Fatal(err)
	}
	// note: thread rows are needed for the threads join
	insThread := func(id, folder, acc string) {
		t.Helper()
		if _, err := db.Exec(
			`INSERT INTO threads(id,folder,account,subject,snippet,last_date,is_unread,is_starred,message_count,has_attach)
			 VALUES(?,?,?,?,?, '2026-08-25T09:00:00Z',0,0,1,0)`, "t-"+id, folder, acc, id, "",
		); err != nil {
			t.Fatalf("seed thread %s: %v", id, err)
		}
	}

	insThread("m1", "f-inbox", "a1")
	seed("m1", "f-inbox", "a1", "Report Q3", "Il report e pronto", "anna@acme.dev", "luca@x.dev", "2026-08-25T09:00:00.000000000Z")
	insThread("m2", "f-inbox-2", "a2")
	seed("m2", "f-inbox-2", "a2", "Report Q3", "Riepilogo mensile", "kim@other.dev", "bo@y.dev", "2026-08-26T09:00:00.000000000Z")
	insThread("m3", "f-inbox", "a1")
	seed("m3", "f-inbox", "a1", "Report Q3", "Aggiornamento", "sam@acme.dev", "luca@x.dev", "2026-08-30T09:00:00.000000000Z")

	st := New(db)

	// no filters: all three threads (deduped), newest thread date first is not
	// guaranteed by rank; just check the count
	res, err := st.Query(domain.SearchQuery{Query: "report"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("want 3 threads, got %d", len(res))
	}

	// folder filter
	res, err = st.Query(domain.SearchQuery{Query: "report", FolderID: "f-inbox"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("folder filter: want 2, got %+v", res)
	}

	// account filter
	res, err = st.Query(domain.SearchQuery{Query: "report", AccountID: "a2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ThreadID != "t-m2" {
		t.Fatalf("account filter: want only t-m2, got %+v", res)
	}

	// date range (After only)
	after, _ := time.Parse(time.RFC3339, "2026-08-28T00:00:00Z")
	res, err = st.Query(domain.SearchQuery{Query: "report", After: after})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ThreadID != "t-m3" {
		t.Fatalf("after filter: want only t-m3, got %+v", res)
	}

	// date range (Before only)
	before, _ := time.Parse(time.RFC3339, "2026-08-26T00:00:00Z")
	res, err = st.Query(domain.SearchQuery{Query: "report", Before: before})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ThreadID != "t-m1" {
		t.Fatalf("before filter: want only t-m1, got %+v", res)
	}

	// recipient match reports matchedOn "recipient"
	res, err = st.Query(domain.SearchQuery{Query: "bo@y.dev"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Matched != "recipient" {
		t.Fatalf("recipient match: want recipient, got %+v", res)
	}
}

// --- M10: prefix terms, quoted phrases, snippets ------------------------

func TestFtsPrefixAndPhrase(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "pfx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,email,provider,active,created_at)
		VALUES('a1','Luca','luca@example.com','mock',1,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO folders(id,account,name,type) VALUES('f','a1','Inbox','inbox')`); err != nil {
		t.Fatal(err)
	}
	ins := func(id, thread, subject, body string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO threads(id,folder,account,subject,snippet,last_date,is_unread,is_starred,message_count,has_attach)
			VALUES(?,?,?,?,?,'2026-08-25T09:00:00Z',0,0,1,0)`, thread, "f", "a1", subject, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO messages(id,thread,folder,account,subject,from_name,from_email,body,body_text,date)
			VALUES(?,?, 'f','a1',?, 'Anna','anna@acme.dev',?,?, '2026-08-25T09:00:00.000000000Z')`,
			id, thread, subject, body, body); err != nil {
			t.Fatal(err)
		}
	}
	ins("m1", "t1", "Rinnovo contratto", "Il rinnovo del contratto di affitto e pronto")
	ins("m2", "t2", "Ferie agosto", "Richiesta ferie per agosto")

	st := New(db)

	// prefix: "rinnov" matches rinnovo (word forms), the old exact-phrase
	// behavior would have returned nothing
	res, err := st.Query(domain.SearchQuery{Query: "rinnov"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ThreadID != "t1" {
		t.Fatalf("prefix search: %+v", res)
	}
	// snippet carries the highlight markers
	if !strings.Contains(res[0].Snippet, hlStart) || !strings.Contains(res[0].Snippet, hlEnd) {
		t.Fatalf("snippet senza marker: %q", res[0].Snippet)
	}

	// multi-word AND: both terms must match
	res, err = st.Query(domain.SearchQuery{Query: "rinnovo affitto"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("multi-word: %+v", res)
	}

	// AND semantics exclude partial matches
	res, err = st.Query(domain.SearchQuery{Query: "rinnovo ferie"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Fatalf("AND atteso vuoto: %+v", res)
	}

	// quoted phrase stays exact (no prefixing inside quotes)
	res, err = st.Query(domain.SearchQuery{Query: `"ferie agosto"`})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ThreadID != "t2" {
		t.Fatalf("phrase: %+v", res)
	}
	res, err = st.Query(domain.SearchQuery{Query: `"agosto ferie"`})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Fatalf("phrase order deve contare: %+v", res)
	}

	// email addresses tokenize: searching the local part hits the sender
	// (both seeded messages come from anna@acme.dev)
	res, err = st.Query(domain.SearchQuery{Query: "anna"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Matched != "sender" || res[1].Matched != "sender" {
		t.Fatalf("sender match: %+v", res)
	}

	// punctuation-only query never breaks the MATCH syntax
	if _, err := st.Query(domain.SearchQuery{Query: "!!! @@@"}); err != nil {
		t.Fatalf("punctuation query: %v", err)
	}
}

func TestFtsTermBuilding(t *testing.T) {
	if got := ftsTerm("rinnovo contratto"); got != `rinnovo* contratto*` {
		t.Fatalf("ftsTerm multi = %q", got)
	}
	if got := ftsTerm(`"ferie agosto"`); got != `"ferie agosto"` {
		t.Fatalf("ftsTerm phrase = %q", got)
	}
	if got := ftsTerm("anna@acme.dev"); got != `anna* acme* dev*` {
		t.Fatalf("ftsTerm email = %q", got)
	}
	if got := ftsTerm("!!!"); got != `"!!!"` {
		t.Fatalf("ftsTerm punctuation fallback = %q", got)
	}
	if got := ftsTerm("  "); got != "" {
		t.Fatalf("ftsTerm empty = %q", got)
	}
}
