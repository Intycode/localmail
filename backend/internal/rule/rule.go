// Package rule implements the local rules engine (M8): CRUD for rules and
// the matcher the sync engine runs at ingest. One condition -> one action per
// rule; rules are evaluated in position order and all matches apply.
package rule

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"mail/backend/internal/domain"
)

// Store persists rules in SQLite.
type Store struct {
	db *sql.DB
}

// New builds a rule store.
func New(db *sql.DB) *Store { return &Store{db: db} }

// scanRule reads one row (column order = listColumns).
const listColumns = "id,account_id,name,enabled,position,field,op,value,action,target"

func scanRule(row interface{ Scan(...any) error }) (domain.Rule, error) {
	var r domain.Rule
	var enabled int
	if err := row.Scan(&r.ID, &r.Account, &r.Name, &enabled, &r.Position,
		&r.Field, &r.Op, &r.Value, &r.Action, &r.Target); err != nil {
		return r, err
	}
	r.Enabled = enabled == 1
	return r, nil
}

// ForAccount returns the enabled rules that apply to an account: the ones
// bound to it plus the global ones (account_id = ''), in position order.
func (s *Store) ForAccount(accountID string) ([]domain.Rule, error) {
	rows, err := s.db.Query(
		"SELECT "+listColumns+" FROM rules WHERE enabled=1 AND (account_id='' OR account_id=?) ORDER BY position, id",
		accountID)
	if err != nil {
		return nil, fmt.Errorf("rules for %s: %w", accountID, err)
	}
	defer rows.Close()
	out := []domain.Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// All returns every rule (enabled or not), position order. Used by the UI.
func (s *Store) All() ([]domain.Rule, error) {
	rows, err := s.db.Query("SELECT " + listColumns + " FROM rules ORDER BY position, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Save inserts or updates a rule (keyed by ID; empty ID = new id).
func (s *Store) Save(r *domain.Rule) error {
	if r.ID == "" {
		r.ID = newRuleID()
	}
	if r.Position == 0 {
		// append at the end by default
		var max sql.NullInt64
		_ = s.db.QueryRow("SELECT MAX(position) FROM rules").Scan(&max)
		r.Position = int(max.Int64) + 1
	}
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO rules(id,account_id,name,enabled,position,field,op,value,action,target) VALUES(?,?,?,?,?,?,?,?,?,?)",
		r.ID, r.Account, r.Name, boolInt(r.Enabled), r.Position, r.Field, r.Op, r.Value, r.Action, r.Target)
	if err != nil {
		return fmt.Errorf("save rule: %w", err)
	}
	return nil
}

// Delete removes a rule by id.
func (s *Store) Delete(id string) error {
	_, err := s.db.Exec("DELETE FROM rules WHERE id=?", id)
	if err != nil {
		return fmt.Errorf("delete rule %s: %w", id, err)
	}
	return nil
}

// Get loads one rule; a missing rule surfaces as an error.
func (s *Store) Get(id string) (domain.Rule, error) {
	row := s.db.QueryRow("SELECT "+listColumns+" FROM rules WHERE id=?", id)
	r, err := scanRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("regola %s non trovata", id)
	}
	return r, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// newRuleID returns a short random id. Not time-based: Windows clock ticks are
// coarse, so back-to-back saves got equal ids and INSERT OR REPLACE overwrote.
func newRuleID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "r-" + hex.EncodeToString(b)
}

// Matching returns the enabled rules whose condition matches the message, in
// order. The engine decides what each matched action does.
func Matching(rules []domain.Rule, m *domain.Message) []domain.Rule {
	out := []domain.Rule{}
	for _, r := range rules {
		if ruleMatches(r, m) {
			out = append(out, r)
		}
	}
	return out
}

func ruleMatches(r domain.Rule, m *domain.Message) bool {
	value := strings.ToLower(r.Value)
	var hay string
	switch r.Field {
	case "from":
		hay = strings.ToLower(m.From.Email + " " + m.From.Name)
	case "to":
		var sb strings.Builder
		for _, rc := range append(append([]domain.Recipient{}, m.To...), m.CC...) {
			sb.WriteString(strings.ToLower(rc.Email + " " + rc.Name + " "))
		}
		hay = sb.String()
	case "subject":
		hay = strings.ToLower(m.Subject)
	case "body":
		hay = strings.ToLower(m.BodyText + " " + m.Body)
	case "spam_score":
		threshold, err := strconv.ParseFloat(r.Value, 64)
		if err != nil {
			return false
		}
		return m.SpamScore >= threshold
	case "has_attachment":
		return len(m.Attachments) > 0
	default:
		return false
	}
	switch r.Op {
	case "contains":
		return value != "" && strings.Contains(hay, value)
	case "equals":
		return value != "" && strings.TrimSpace(hay) == value
	case "starts_with":
		return value != "" && strings.HasPrefix(strings.TrimSpace(hay), value)
	case "ends_with":
		return value != "" && strings.HasSuffix(strings.TrimSpace(hay), value)
	case "any":
		return strings.TrimSpace(hay) != ""
	}
	return false
}
