// Package thread stores threads/messages and resolves raw MIME into threads.
package thread

import (
	"database/sql"
	"fmt"
	"time"

	"mail/backend/internal/domain"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// CreateThread inserts a thread row.
func (s *Store) CreateThread(t domain.Thread) error {
	_, err := s.db.Exec(
		`INSERT INTO threads(id,folder,account,subject,snippet,last_date,is_unread,is_starred,message_count,has_attach)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.FolderID, t.AccountID, t.Subject, t.Snippet,
		t.Date.UTC().Format(time.RFC3339), boolInt(t.IsUnread), boolInt(t.IsStarred),
		t.MessageCount, boolInt(t.HasAttach),
	)
	if err != nil {
		return fmt.Errorf("create thread: %w", err)
	}
	for _, id := range t.MessageIDs {
		if err := s.linkMessage(t.ID, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) linkMessage(threadID, messageID string) error {
	_, err := s.db.Exec(`UPDATE messages SET thread=? WHERE id=?`, threadID, messageID)
	return err
}

// threadCols is the shared projection for thread list/get queries: the sender
// of the latest message (from_name/from_email) ships with every row so list
// views — including the unified inbox — never need per-thread follow-ups.
const threadCols = `id,folder,account,subject,snippet,last_date,is_unread,is_starred,message_count,has_attach,from_name,from_email`

// ListByFolder returns threads for a folder, newest first.
func (s *Store) ListByFolder(folderID string) ([]domain.Thread, error) {
	rows, err := s.db.Query(
		`SELECT `+threadCols+` FROM threads WHERE folder=? ORDER BY last_date DESC`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanThreads(rows)
}

// ListUnified returns threads across accounts for one folder type (the
// unified inbox joins on folders.type, so every account contributes its own
// folder), newest first. An empty accountID means "all accounts".
func (s *Store) ListUnified(accountID, folderType string, limit int) ([]domain.Thread, error) {
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT t.id,t.folder,t.account,t.subject,t.snippet,t.last_date,t.is_unread,t.is_starred,t.message_count,t.has_attach,t.from_name,t.from_email
		 FROM threads t JOIN folders f ON f.id = t.folder
		 WHERE f.type = ?`
	args := []any{folderType}
	if accountID != "" {
		q += ` AND t.account = ?`
		args = append(args, accountID)
	}
	q += ` ORDER BY t.last_date DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanThreads(rows)
}

// Get returns one thread by id.
func (s *Store) Get(id string) (domain.Thread, error) {
	var t domain.Thread
	row := s.db.QueryRow(
		`SELECT `+threadCols+` FROM threads WHERE id=?`, id)
	if err := scanOne(row, &t); err != nil {
		return t, err
	}
	ids, err := s.MessageIDs(id)
	if err != nil {
		return t, err
	}
	t.MessageIDs = ids
	return t, nil
}

func (s *Store) MessageIDs(threadID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM messages WHERE thread=? ORDER BY date ASC`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) MarkRead(messageID string) error {
	_, err := s.db.Exec(`UPDATE messages SET is_unread=0 WHERE id=?`, messageID)
	return err
}

// MoveToFolder moves a thread (and its messages) to another folder.
func (s *Store) MoveToFolder(threadID, folderID string) error {
	if _, err := s.db.Exec(`UPDATE threads SET folder=? WHERE id=?`, folderID, threadID); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE messages SET folder=? WHERE thread=?`, folderID, threadID)
	return err
}

// Snooze schedules a thread to come back to folderID at until (local-only:
// RFC 3339 UTC, compared lexicographically like the outbox timestamps).
func (s *Store) Snooze(threadID, folderID string, until time.Time) error {
	_, err := s.db.Exec(
		`UPDATE threads SET snooze_until=?, snooze_folder=? WHERE id=?`,
		until.UTC().Format(time.RFC3339Nano), folderID, threadID,
	)
	return err
}

// Unsnooze clears the snooze schedule (the caller moves the thread back).
func (s *Store) Unsnooze(threadID string) error {
	_, err := s.db.Exec(`UPDATE threads SET snooze_until='', snooze_folder='' WHERE id=?`, threadID)
	return err
}

// DueSnooze is one snoozed thread whose return moment has passed.
type DueSnooze struct {
	ThreadID string
	FolderID string
}

// DueSnoozes lists the threads the snooze loop must restore now.
func (s *Store) DueSnoozes(now time.Time) ([]DueSnooze, error) {
	rows, err := s.db.Query(
		`SELECT id, snooze_folder FROM threads
		  WHERE snooze_until<>'' AND snooze_folder<>'' AND snooze_until <= ?`,
		now.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueSnooze
	for rows.Next() {
		var d DueSnooze
		if err := rows.Scan(&d.ThreadID, &d.FolderID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) DeleteMessage(messageID string) error {
	_, err := s.db.Exec(`DELETE FROM messages WHERE id=?`, messageID)
	return err
}

func scanThreads(rows *sql.Rows) ([]domain.Thread, error) {
	var out []domain.Thread
	for rows.Next() {
		var t domain.Thread
		if err := scanOne(rows, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanOne(row interface{ Scan(...any) error }, t *domain.Thread) error {
	var last string
	var unread, starred, has int
	if err := row.Scan(&t.ID, &t.FolderID, &t.AccountID, &t.Subject, &t.Snippet,
		&last, &unread, &starred, &t.MessageCount, &has, &t.From.Name, &t.From.Email); err != nil {
		return err
	}
	date, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return err
	}
	t.Date = date
	t.IsUnread = unread == 1
	t.IsStarred = starred == 1
	t.HasAttach = has == 1
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
