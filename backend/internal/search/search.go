// Package search wraps SQLite FTS5 so the engine can query subject, sender,
// recipient and body.
//
// M10 keyword search: multi-word queries become prefix terms (rinnovo ->
// "rinnovo*", matching rinnovo/rinnovi/...), quoted spans stay exact phrases
// and results are bm25-ranked with native FTS5 snippets carrying [[ ]]
// highlight markers the webview renders bold.
package search

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"

	"mail/backend/internal/domain"
)

type Store struct{ db *sql.DB }

// timeFmt mirrors the message store's date serialization (UTC RFC 3339 with
// nanoseconds) so date-range filters compare lexicographically.
const timeFmt = "2006-01-02T15:04:05.000000000Z07:00"

// Highlight markers embedded in snippets; the webview turns them into <mark>.
const (
	hlStart = "[["
	hlEnd   = "]]"
)

func New(db *sql.DB) *Store { return &Store{db: db} }

// Query runs a FTS5 search and returns matching threads (deduplicated by
// thread_id). The free-text query matches subject, sender, recipient and body.
// The optional filters of domain.SearchQuery (folder, account, date range) are
// applied via joins with messages/threads — mail_fts is content-linked to
// messages, so the joins keep rank ordering intact.
func (s *Store) Query(q domain.SearchQuery) ([]domain.SearchResult, error) {
	term := ftsTerm(q.Query)
	if term == "" {
		return nil, nil
	}
	where := "WHERE mail_fts MATCH ?"
	args := []any{term}
	if q.FolderID != "" {
		where += " AND t.folder = ?"
		args = append(args, q.FolderID)
	}
	if q.AccountID != "" {
		where += " AND t.account = ?"
		args = append(args, q.AccountID)
	}
	if !q.After.IsZero() {
		where += " AND m.date >= ?"
		args = append(args, q.After.UTC().Format(timeFmt))
	}
	if !q.Before.IsZero() {
		where += " AND m.date <= ?"
		args = append(args, q.Before.UTC().Format(timeFmt))
	}
	sql := "SELECT f.subject, f.body, COALESCE(f.recipient, ''), f.thread_id, " +
		"snippet(mail_fts, -1, '" + hlStart + "', '" + hlEnd + "', '…', 14) " +
		"FROM mail_fts f " +
		"JOIN messages m ON m.id = f.message_id " +
		"JOIN threads t  ON t.id = f.thread_id " +
		where + " ORDER BY rank LIMIT 50"
	rows, err := s.db.Query(sql, args...)
	if err != nil {
		return nil, fmt.Errorf("fts query: %w", err)
	}

	// scan everything first, then close rows before issuing follow-up queries
	// (SQLite is a single connection; nested queries while rows are open deadlock)
	var hits []domain.SearchResult
	seen := map[string]bool{}
	for rows.Next() {
		var subject, body, recipient, threadID, snip string
		if err := rows.Scan(&subject, &body, &recipient, &threadID, &snip); err != nil {
			rows.Close()
			return nil, err
		}
		if seen[threadID] {
			continue
		}
		seen[threadID] = true
		hits = append(hits, domain.SearchResult{
			ThreadID: threadID,
			Subject:  subject,
			Snippet:  snip,
			Matched:  matchedField(ftsTokens(q.Query), subject, body, recipient),
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	for i := range hits {
		hits[i].Date = s.threadDate(hits[i].ThreadID)
	}
	return hits, nil
}

// threadDate returns the latest message date of a thread.
func (s *Store) threadDate(threadID string) time.Time {
	var last string
	err := s.db.QueryRow("SELECT MAX(date) FROM messages WHERE thread=?", threadID).Scan(&last)
	if err != nil || last == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ftsTerm builds the FTS5 MATCH expression: unquoted words become AND-combined
// prefix terms (contratt -> contratto/contratti/...), "quoted spans" stay exact
// phrases. Falls back to the whole input as one phrase when nothing tokenizes
// (pure punctuation) so special characters can never break the MATCH syntax.
func ftsTerm(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	terms := []string{}
	for _, part := range splitQuoted(q) {
		if part.quoted {
			inner := strings.ReplaceAll(part.text, `"`, "")
			if inner != "" {
				terms = append(terms, `"`+inner+`"`)
			}
			continue
		}
		for _, tok := range tokenize(part.text) {
			terms = append(terms, `"`+tok+`"*`) // quoted: AND/OR/NOT stay plain words
		}
	}
	if len(terms) == 0 {
		return `"` + strings.ReplaceAll(q, `"`, `""`) + `"`
	}
	return strings.Join(terms, " ")
}

// ftsTokens returns the plain words of the query (no prefixes, no quotes) for
// field classification.
func ftsTokens(q string) []string {
	var out []string
	for _, part := range splitQuoted(q) {
		if part.quoted {
			out = append(out, strings.ToLower(part.text))
			continue
		}
		for _, tok := range tokenize(part.text) {
			out = append(out, strings.ToLower(tok))
		}
	}
	return out
}

// tokenize keeps letters and digits only: FTS5's unicode61 tokenizer also
// splits on punctuation, so "anna@acme.dev" indexes as anna/acme/dev — the
// prefix terms match the same units.
func tokenize(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

type qpart struct {
	text   string
	quoted bool
}

// splitQuoted splits on whitespace but keeps "double quoted" spans intact.
func splitQuoted(q string) []qpart {
	var parts []qpart
	var cur strings.Builder
	inQuote := false
	flush := func(quoted bool) {
		if strings.TrimSpace(cur.String()) != "" {
			parts = append(parts, qpart{text: cur.String(), quoted: quoted})
		}
		cur.Reset()
	}
	for _, r := range q {
		switch {
		case r == '"':
			flush(inQuote)
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			flush(false)
		default:
			cur.WriteRune(r)
		}
	}
	flush(inQuote)
	return parts
}

// matchedField classifies which field the query words match best.
func matchedField(tokens []string, subject, body, recipient string) string {
	fields := map[string]string{
		"subject":   strings.ToLower(subject),
		"body":      strings.ToLower(body),
		"recipient": strings.ToLower(recipient),
	}
	best := "sender"
	for _, name := range []string{"subject", "body", "recipient"} {
		text := fields[name]
		all := true
		for _, tok := range tokens {
			if !strings.Contains(text, tok) {
				all = false
				break
			}
		}
		if all && len(tokens) > 0 {
			return name
		}
		if strings.Contains(text, strings.Join(tokens, " ")) {
			best = name
		}
	}
	return best
}
