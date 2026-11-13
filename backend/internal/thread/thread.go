// Package thread stores threads/messages and resolves raw MIME into threads.
package thread

import (
	"database/sql"
	"fmt"
	"strings"
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

// Page selects one page of a newest-first thread list: the threads after
// Before (the last thread of the previous page; empty = first page), at most
// Limit of them (<= 0 = no limit).
type Page struct {
	BeforeID   string
	BeforeDate time.Time // fallback when BeforeID was deleted meanwhile
	Limit      int
}

// keyset appends the "older than the cursor" condition and the ordering. The
// cursor's last_date is read back from the row itself: stored dates are not
// all in one text format, so a client-side value would not compare exactly.
func (p Page) keyset(q string, args []any) (string, []any) {
	if p.BeforeID != "" {
		q += ` AND (t.last_date, t.id) < (COALESCE((SELECT last_date FROM threads WHERE id=?), ?), ?)`
		args = append(args, p.BeforeID, p.BeforeDate.UTC().Format(time.RFC3339Nano), p.BeforeID)
	}
	q += ` ORDER BY t.last_date DESC, t.id DESC`
	if p.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, p.Limit)
	}
	return q, args
}

// ListByFolder returns one page of a folder's threads, newest first.
func (s *Store) ListByFolder(folderID string, page Page) ([]domain.Thread, error) {
	q, args := page.keyset(`SELECT `+threadCols+` FROM threads t WHERE t.folder=?`, []any{folderID})
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanThreads(rows)
}

// ListUnified returns one page of threads across accounts for one folder type
// (the unified inbox joins on folders.type, so every account contributes its
// own folder), newest first. An empty accountID means "all accounts".
func (s *Store) ListUnified(accountID, folderType string, page Page) ([]domain.Thread, error) {
	q := `SELECT t.id,t.folder,t.account,t.subject,t.snippet,t.last_date,t.is_unread,t.is_starred,t.message_count,t.has_attach,t.from_name,t.from_email
		 FROM threads t JOIN folders f ON f.id = t.folder
		 WHERE f.type = ?`
	args := []any{folderType}
	if accountID != "" {
		q += ` AND t.account = ?`
		args = append(args, accountID)
	}
	q, args = page.keyset(q, args)
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

// FillMessageIDs sets MessageIDs on every thread of a list page with one
// query (oldest first per thread), instead of one query per thread.
func (s *Store) FillMessageIDs(threads []domain.Thread) error {
	if len(threads) == 0 {
		return nil
	}
	idx := make(map[string]int, len(threads))
	args := make([]any, len(threads))
	for i := range threads {
		idx[threads[i].ID] = i
		args[i] = threads[i].ID
	}
	rows, err := s.db.Query(
		`SELECT thread, id FROM messages WHERE thread IN (?`+strings.Repeat(",?", len(threads)-1)+`) ORDER BY date ASC`,
		args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tid, id string
		if err := rows.Scan(&tid, &id); err != nil {
			return err
		}
		t := &threads[idx[tid]]
		t.MessageIDs = append(t.MessageIDs, id)
	}
	return rows.Err()
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
