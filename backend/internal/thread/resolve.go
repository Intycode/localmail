// Thread resolution: RFC 5322 based (Message-ID / In-Reply-To / References)
// with a normalized-subject fallback. Thread ids are deterministic so the
// same message set always resolves to the same threads.
package thread

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"

	"mail/backend/internal/domain"
)

type Resolver struct{ db *sql.DB }

func NewResolver(db *sql.DB) *Resolver { return &Resolver{db: db} }

// Resolve returns the thread id for a message being persisted.
//
//  1. If the message references (or is referenced by) a known Message-ID in
//     the same folder, join that thread. Threads are folder-scoped: joining a
//     Sent thread would hide an Inbox reply from the Inbox list.
//  2. Otherwise group by normalized subject within the folder.
func (r *Resolver) Resolve(accountID, folderID string, m *domain.Message) (string, error) {
	ids := make([]string, 0, 1+len(m.InReplyTo)+len(m.References))
	if m.MessageID != "" {
		ids = append(ids, m.MessageID)
	}
	ids = append(ids, m.InReplyTo...)
	ids = append(ids, m.References...)

	if len(ids) > 0 {
		q := `SELECT DISTINCT thread FROM messages WHERE account=? AND folder=? AND thread<>'' AND message_id IN (` + placeholders(len(ids)) + `)`
		args := make([]any, 0, len(ids)+2)
		args = append(args, accountID, folderID)
		for _, id := range ids {
			args = append(args, id)
		}
		rows, err := r.db.Query(q, args...)
		if err != nil {
			return "", err
		}
		defer rows.Close()
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err == nil && t != "" {
				return t, rows.Err()
			}
		}
		if err := rows.Err(); err != nil {
			return "", err
		}
	}

	// Subject fallback: deterministic, folder-scoped thread id. The key is
	// hashed (not the raw subject) so ids stay URL-safe: subjects may contain
	// spaces, slashes and any Unicode.
	sum := sha256.Sum256([]byte(NormalizeSubject(m.Subject)))
	return "t:" + folderID + ":" + hex.EncodeToString(sum[:8]), nil
}

// NormalizeSubject strips reply/forward prefixes and [tag] blocks so replies
// join the same thread. Returns the canonical subject key.
func NormalizeSubject(s string) string {
	return trimTags(trimRe(strings.TrimSpace(s)))
}

func trimRe(s string) string {
	for {
		t := s
		low := strings.ToLower(t)
		if strings.HasPrefix(low, "re:") || strings.HasPrefix(low, "fwd:") || strings.HasPrefix(low, "fw:") {
			t = t[strings.Index(low, ":")+1:]
			s = strings.TrimSpace(t)
			continue
		}
		break
	}
	return s
}

func trimTags(s string) string {
	for {
		t := s
		if strings.HasPrefix(t, "[") {
			if i := strings.Index(t, "]"); i >= 0 {
				s = strings.TrimSpace(t[i+1:])
				continue
			}
		}
		break
	}
	return s
}

func placeholders(n int) string {
	out := make([]string, n)
	for i := range out {
		out[i] = "?"
	}
	return strings.Join(out, ",")
}