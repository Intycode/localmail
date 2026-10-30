// Package queue implements the offline-first pending-operations store: when
// the mail server is unreachable, user actions (send, flags, moves, deletes)
// are parked in SQLite and replayed by the outbox worker with exponential
// backoff. Secrets never live here — payloads reference the account, the
// credential is fetched from the runtime vault at replay time.
package queue

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// Op is one queued operation.
type Op struct {
	ID            string    `json:"id"`
	AccountID     string    `json:"accountId"`
	Kind          string    `json:"kind"`
	Payload       string    `json:"payload"`
	CreatedAt     time.Time `json:"createdAt"`
	Attempts      int       `json:"attempts"`
	NextAttemptAt time.Time `json:"nextAttemptAt"`
	LastError     string    `json:"lastError,omitempty"`
}

const (
	// baseDelay is the first retry delay; it doubles per attempt.
	baseDelay = 30 * time.Second
	// maxDelay caps the exponential backoff.
	maxDelay = 15 * time.Minute
)

// Backoff returns the delay before attempt n (1-based) and whether the
// attempt budget is exhausted (after maxAttempts the item parks for one hour
// per try instead of hammering the network forever).
func Backoff(attempts int) time.Duration {
	if attempts >= MaxAttempts {
		return maxDelay // also guards the shift below against int64 overflow
	}
	d := baseDelay << attempts // attempts is 0-based here: first retry 30s
	if d > maxDelay {
		d = maxDelay
	}
	return d
}

// MaxAttempts is the point where the backoff caps out (30s * 2^5 = 16m).
const MaxAttempts = 5

// Store is the pending-operations store over the local SQLite database.
type Store struct {
	db *sql.DB
}

// New builds a Store.
func New(db *sql.DB) *Store { return &Store{db: db} }

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Enqueue stores one operation for later replay. next_attempt_at is now: the
// worker tries immediately on the next tick (it may be back already).
func (s *Store) Enqueue(accountID, kind string, payload []byte) (string, error) {
	return s.EnqueueAt(accountID, kind, payload, time.Now().UTC())
}

// EnqueueAt stores one operation with an explicit first-run time: this is
// what makes undo-send and scheduled send possible (the outbox worker only
// replays items whose next_attempt_at has passed).
func (s *Store) EnqueueAt(accountID, kind string, payload []byte, runAt time.Time) (string, error) {
	now := time.Now().UTC()
	id := newID()
	_, err := s.db.Exec(
		`INSERT INTO pending_ops (id, account_id, kind, payload, created_at, attempts, next_attempt_at, last_error)
		  VALUES (?, ?, ?, ?, ?, 0, ?, '')`,
		id, accountID, kind, string(payload), now.Format(time.RFC3339Nano), runAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return "", fmt.Errorf("enqueue %s: %w", kind, err)
	}
	return id, nil
}

// Count returns the number of pending operations.
func (s *Store) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM pending_ops`).Scan(&n)
	return n, err
}

// List returns the newest pending operations (UI summary), limit capped.
func (s *Store) List(limit int) ([]Op, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, account_id, kind, payload, created_at, attempts, next_attempt_at, last_error
		 FROM pending_ops ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOps(rows)
}

// Due returns operations whose next_attempt_at has passed, oldest first.
func (s *Store) Due(now time.Time, limit int) ([]Op, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.Query(
		`SELECT id, account_id, kind, payload, created_at, attempts, next_attempt_at, last_error
		 FROM pending_ops WHERE next_attempt_at <= ? ORDER BY created_at ASC LIMIT ?`,
		// timestamps are stored UTC ("Z"): the comparison must be UTC too,
		// otherwise a +02:00 local param compares lexicographically wrong
		now.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOps(rows)
}

// Done removes a successfully replayed operation.
func (s *Store) Done(id string) error {
	_, err := s.db.Exec(`DELETE FROM pending_ops WHERE id = ?`, id)
	return err
}

// Failed records a failed attempt: attempts++, exponential backoff, error
// note. After MaxAttempts the retry settles at maxDelay (capped hammering).
func (s *Store) Failed(id string, opErr error, now time.Time) error {
	delay := baseDelay
	var attempts int
	err := s.db.QueryRow(`SELECT attempts FROM pending_ops WHERE id = ?`, id).Scan(&attempts)
	if err == nil {
		delay = Backoff(attempts)
	}
	next := now.Add(delay).UTC().Format(time.RFC3339Nano)
	msg := ""
	if opErr != nil {
		msg = opErr.Error()
		if len(msg) > 500 {
			msg = msg[:500]
		}
	}
	_, err = s.db.Exec(
		`UPDATE pending_ops SET attempts = attempts + 1, next_attempt_at = ?, last_error = ? WHERE id = ?`,
		next, msg, id)
	return err
}

// Cancel removes one queued operation (user action from the outbox list).
func (s *Store) Cancel(id string) error { return s.Done(id) }

func scanOps(rows *sql.Rows) ([]Op, error) {
	var out []Op
	for rows.Next() {
		var o Op
		var created, next string
		if err := rows.Scan(&o.ID, &o.AccountID, &o.Kind, &o.Payload, &created, &o.Attempts, &next, &o.LastError); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			o.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, next); err == nil {
			o.NextAttemptAt = t
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
