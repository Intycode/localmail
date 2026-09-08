// Package message persists parsed messages (and their thread aggregates) in
// SQLite. Writes are batched in single transactions: SQLite is a single
// writer, and batching keeps the initial sync fast.
package message

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"mail/backend/internal/domain"
)

// SaveRaw stores the raw RFC5322 bytes of an imported message (no provider
// exists to re-fetch from). GetRaw returns them for re-export.
func (s *Store) SaveRaw(messageID string, raw []byte) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO import_raw(message_id,raw) VALUES(?,?)`, messageID, raw)
	if err != nil {
		return fmt.Errorf("save raw %s: %w", messageID, err)
	}
	return nil
}

// GetRaw returns the stored raw bytes; nil when the message was not
// imported (for provider messages the raw comes from the provider).
func (s *Store) GetRaw(messageID string) ([]byte, error) {
	var raw []byte
	err := s.db.QueryRow(`SELECT raw FROM import_raw WHERE message_id=?`, messageID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get raw %s: %w", messageID, err)
	}
	return raw, nil
}

// encodePhishing/decodePhishing marshal the phishing flag list as JSON text
// in the messages.phishing column.
// ListByFolder returns every message stored in a folder, oldest first (the
// order mbox archives use for export). Metadata only, no raw bytes.
func (s *Store) ListByFolder(folderID string) ([]domain.Message, error) {
	rows, err := s.db.Query(
		`SELECT id,thread,folder,account,subject,from_name,from_email,body,body_text,date,is_unread,is_starred,provider_msg_id,message_id,remote_images,spam_score,phishing,is_encrypted,sig_status
		 FROM messages WHERE folder=? ORDER BY date, id`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Message{}
	for rows.Next() {
		var m domain.Message
		var threadID string
		var unread, starred, enc int
		var date, phishing string
		if err := rows.Scan(&m.ID, &threadID, &m.FolderID, &m.AccountID, &m.Subject,
			&m.From.Name, &m.From.Email, &m.Body, &m.BodyText, &date, &unread, &starred,
			&m.ProviderMessageID, &m.MessageID, &m.RemoteImages, &m.SpamScore, &phishing, &enc, &m.SigStatus); err != nil {
			return nil, err
		}
		m.ThreadID = threadID
		m.IsUnread = unread == 1
		m.IsStarred = starred == 1
		m.IsEncrypted = enc == 1
		m.Phishing = decodePhishing(phishing)
		if d, derr := parseDate(date); derr == nil {
			m.Date = d
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func encodePhishing(flags []string) string {
	if len(flags) == 0 {
		return ""
	}
	b, err := json.Marshal(flags)
	if err != nil {
		return ""
	}
	return string(b)
}

func decodePhishing(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// chunkSize bounds IN(...) lists to keep SQLite variables low.
const chunkSize = 500

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// UpsertBatch persists messages + recipients + thread aggregates in ONE
// transaction. Messages are keyed by id (m:<account>:<folder>:<uid>) so a
// re-sync is idempotent; REPLACE refreshes FTS via its triggers.
func (s *Store) UpsertBatch(msgs []domain.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for i := range msgs {
		if err := upsertOne(tx, &msgs[i]); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit upsert: %w", err)
	}
	return nil
}

func upsertOne(tx *sql.Tx, m *domain.Message) error {
	// Thread row FIRST: messages.thread references threads(id).
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO threads(id,folder,account,subject,snippet,last_date,is_unread,is_starred,message_count,has_attach)
		 VALUES(?,?,?,?,?,?,?,0,1,0)`,
		m.ThreadID, m.FolderID, m.AccountID, m.Subject, snippetOf(m), m.Date.UTC().Format(timeFmt), boolInt(m.IsUnread),
	); err != nil {
		return fmt.Errorf("insert thread %s: %w", m.ThreadID, err)
	}
	if _, err := tx.Exec(
		`DELETE FROM recipients WHERE message=?`, m.ID,
	); err != nil {
		return fmt.Errorf("clear recipients: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO messages(id,thread,folder,account,subject,from_name,from_email,body,body_text,date,is_unread,is_starred,provider_msg_id,message_id,in_reply_to,references_hdr,remote_images,spam_score,phishing,is_encrypted,sig_status)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ThreadID, m.FolderID, m.AccountID, m.Subject, m.From.Name, m.From.Email,
		m.Body, m.BodyText, m.Date.UTC().Format(timeFmt), boolInt(m.IsUnread), boolInt(m.IsStarred),
		m.ProviderMessageID, m.MessageID, strings.Join(m.InReplyTo, " "), strings.Join(m.References, " "), m.RemoteImages, m.SpamScore, encodePhishing(m.Phishing), boolInt(m.IsEncrypted), m.SigStatus,
	); err != nil {
		return fmt.Errorf("insert message %s: %w", m.ID, err)
	}
	for _, r := range m.To {
		if err := insertRecipient(tx, m.ID, "to", r); err != nil {
			return err
		}
	}
	for _, r := range m.CC {
		if err := insertRecipient(tx, m.ID, "cc", r); err != nil {
			return err
		}
	}
	for _, r := range m.BCC {
		if err := insertRecipient(tx, m.ID, "bcc", r); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM attachments WHERE message=?`, m.ID); err != nil {
		return fmt.Errorf("clear attachments: %w", err)
	}
	for i, a := range m.Attachments {
		id := fmt.Sprintf("a:%s:%d", m.ID, i)
		if _, err := tx.Exec(
			`INSERT INTO attachments(id,message,name,size,content_type) VALUES(?,?,?,?,?)`,
			id, m.ID, a.Name, a.Size, a.ContentType,
		); err != nil {
			return fmt.Errorf("insert attachment %s: %w", a.Name, err)
		}
	}
	// Aggregate recompute for the thread (after the message insert).
	if _, err := tx.Exec(
		`UPDATE threads SET
		   last_date     = (SELECT COALESCE(MAX(date), last_date) FROM messages WHERE thread=threads.id),
		   message_count = (SELECT COUNT(*) FROM messages WHERE thread=threads.id),
		   is_unread     = (SELECT COALESCE(MAX(is_unread),0) FROM messages WHERE thread=threads.id),
		   has_attach    = EXISTS(SELECT 1 FROM attachments a JOIN messages msg ON a.message=msg.id WHERE msg.thread=threads.id),
		   snippet       = (SELECT substr(COALESCE(body_text, subject),1,140) FROM messages WHERE thread=threads.id ORDER BY date DESC LIMIT 1),
		   from_name     = (SELECT COALESCE(from_name,'')   FROM messages WHERE thread=threads.id ORDER BY date DESC LIMIT 1),
		   from_email    = (SELECT COALESCE(from_email,'')  FROM messages WHERE thread=threads.id ORDER BY date DESC LIMIT 1)
		 WHERE id=?`, m.ThreadID,
	); err != nil {
		return fmt.Errorf("update thread %s: %w", m.ThreadID, err)
	}
	return nil
}

func insertRecipient(tx *sql.Tx, messageID, kind string, r domain.Recipient) error {
	_, err := tx.Exec(`INSERT INTO recipients(message,kind,name,email) VALUES(?,?,?,?)`,
		messageID, kind, r.Name, r.Email)
	return err
}

// LocalUIDs returns the provider UIDs currently stored for a folder.
func (s *Store) LocalUIDs(folderID string) ([]uint32, error) {
	rows, err := s.db.Query(`SELECT provider_msg_id FROM messages WHERE folder=?`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var raw sql.NullString
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if u, err := strconv.ParseUint(raw.String, 10, 32); err == nil {
			out = append(out, uint32(u))
		}
	}
	return out, rows.Err()
}

// DeleteUIDs removes messages whose provider UID is not on the server anymore
// (server-side deletions), then prunes empty threads. Chunked IN(...) lists.
func (s *Store) DeleteUIDs(folderID string, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for start := 0; start < len(uids); start += chunkSize {
		end := start + chunkSize
		if end > len(uids) {
			end = len(uids)
		}
		batch := uids[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch))
		for _, u := range batch {
			args = append(args, strconv.FormatUint(uint64(u), 10))
		}
		if _, err := tx.Exec(
			`DELETE FROM messages WHERE folder=? AND provider_msg_id IN (`+placeholders+`)`,
			append([]any{folderID}, args...)...,
		); err != nil {
			return fmt.Errorf("delete uids: %w", err)
		}
	}
	if _, err := tx.Exec(
		`UPDATE threads SET message_count=(SELECT COUNT(*) FROM messages WHERE thread=threads.id) WHERE folder=?`,
		folderID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM threads WHERE folder=? AND message_count=0`, folderID); err != nil {
		return err
	}
	return tx.Commit()
}

// WipeFolder removes all local data of a folder (UIDVALIDITY changed).
func (s *Store) WipeFolder(folderID string) error {
	if _, err := s.db.Exec(`DELETE FROM messages WHERE folder=?`, folderID); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM threads WHERE folder=?`, folderID); err != nil {
		return err
	}
	return nil
}

// Get returns one message with its recipients.
func (s *Store) Get(id string) (domain.Message, error) {
	var m domain.Message
	var threadID, date string
	row := s.db.QueryRow(
		`SELECT id,thread,folder,account,subject,from_name,from_email,body,body_text,date,is_unread,is_starred,provider_msg_id,message_id,remote_images,spam_score,phishing,is_encrypted,sig_status
		 FROM messages WHERE id=?`, id)
	var unread, starred int
	var phishing string
	var enc int
	if err := row.Scan(&m.ID, &threadID, &m.FolderID, &m.AccountID, &m.Subject,
		&m.From.Name, &m.From.Email, &m.Body, &m.BodyText, &date, &unread, &starred, &m.ProviderMessageID, &m.MessageID, &m.RemoteImages, &m.SpamScore, &phishing, &enc, &m.SigStatus); err != nil {
		return m, fmt.Errorf("message %s: %w", id, err)
	}
	m.ThreadID = threadID
	m.IsUnread = unread == 1
	m.IsStarred = starred == 1
	m.IsEncrypted = enc == 1
	if d, err := parseDate(date); err == nil {
		m.Date = d
	}
	m.Phishing = decodePhishing(phishing)
	recs, err := s.recipients(id)
	if err != nil {
		return m, err
	}
	m.To = recs["to"]
	m.CC = recs["cc"]
	m.BCC = recs["bcc"]
	atts, err := s.attachments(id)
	if err != nil {
		return m, err
	}
	m.Attachments = atts
	return m, nil
}

// attachments loads one message's attachment metadata (no binaries).
func (s *Store) attachments(messageID string) ([]domain.Attachment, error) {
	rows, err := s.db.Query(
		`SELECT name,size,content_type FROM attachments WHERE message=? ORDER BY id`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Attachment
	for rows.Next() {
		var a domain.Attachment
		if err := rows.Scan(&a.Name, &a.Size, &a.ContentType); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) recipients(messageID string) (map[string][]domain.Recipient, error) {
	rows, err := s.db.Query(`SELECT kind,name,email FROM recipients WHERE message=?`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]domain.Recipient{}
	for rows.Next() {
		var kind string
		var r domain.Recipient
		if err := rows.Scan(&kind, &r.Name, &r.Email); err != nil {
			return nil, err
		}
		out[kind] = append(out[kind], r)
	}
	return out, rows.Err()
}

// ThreadUIDs returns the provider UIDs of the messages of a thread and the
// mailbox they live in (for server-side round-trips like move/delete).
func (s *Store) ThreadUIDs(threadID string) (mailbox string, uids []uint32, err error) {
	rows, err := s.db.Query(
		`SELECT f.imap_name, m.provider_msg_id FROM messages m
		 JOIN folders f ON f.id = m.folder
		 WHERE m.thread=? ORDER BY m.date`, threadID)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			return "", nil, err
		}
		if mailbox == "" {
			mailbox = name
		}
		if u, perr := strconv.ParseUint(raw, 10, 32); perr == nil {
			uids = append(uids, uint32(u))
		}
	}
	return mailbox, uids, rows.Err()
}

// DeleteFull removes a message and its children (FK-safe); FTS rows go via
// the DELETE trigger, and the thread aggregate is refreshed.
func (s *Store) DeleteFull(messageID string) error {
	var threadID string
	err := s.db.QueryRow(`SELECT thread FROM messages WHERE id=?`, messageID).Scan(&threadID)
	if err == sql.ErrNoRows {
		return nil // already gone
	}
	if err != nil {
		return fmt.Errorf("lookup message %s: %w", messageID, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM message_labels WHERE message=?`,
		`DELETE FROM attachments WHERE message=?`,
		`DELETE FROM recipients WHERE message=?`,
		`DELETE FROM messages WHERE id=?`,
	} {
		if _, err := tx.Exec(q, messageID); err != nil {
			return fmt.Errorf("delete message %s: %w", messageID, err)
		}
	}
	if _, err := tx.Exec(
		`UPDATE threads SET
		   message_count = (SELECT COUNT(*) FROM messages WHERE thread=?),
		   is_unread     = (SELECT COALESCE(MAX(is_unread),0) FROM messages WHERE thread=?)
		 WHERE id=?`, threadID, threadID, threadID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`DELETE FROM threads WHERE id=? AND message_count=0`, threadID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// ActionTarget returns where a message lives (for provider round-trips).
// folders.imap_name is NOT NULL, so no COALESCE is needed.
func (s *Store) ActionTarget(messageID string) (accountID, folderID, mailbox string, uid uint32, err error) {
	var raw sql.NullString
	err = s.db.QueryRow(
		`SELECT m.account, m.folder, f.imap_name, m.provider_msg_id
		 FROM messages m JOIN folders f ON f.id = m.folder
		 WHERE m.id=?`, messageID,
	).Scan(&accountID, &folderID, &mailbox, &raw)
	if err != nil {
		return "", "", "", 0, fmt.Errorf("action target %s: %w", messageID, err)
	}
	if u, perr := strconv.ParseUint(raw.String, 10, 32); perr == nil {
		uid = uint32(u)
	}
	return accountID, folderID, mailbox, uid, nil
}

// MarkRead updates the local flag.
func (s *Store) MarkRead(messageID string) error {
	_, err := s.db.Exec(`UPDATE messages SET is_unread=0 WHERE id=?`, messageID)
	return err
}

// FlagTarget is one message's provider location, for flag round-trips.
type FlagTarget struct {
	ID       string
	FolderID string
	Mailbox  string
	UID      uint32
	IsUnread bool
}

// ThreadFlagTargets returns the provider location of every message in a
// thread (folders.imap_name is NOT NULL, so no COALESCE is needed). Used by
// the thread-level mark-read/mark-unread ops.
func (s *Store) ThreadFlagTargets(threadID string) ([]FlagTarget, error) {
	rows, err := s.db.Query(
		`SELECT m.id, m.folder, f.imap_name, m.provider_msg_id, m.is_unread
		 FROM messages m JOIN folders f ON f.id = m.folder
		 WHERE m.thread=? ORDER BY m.date ASC`, threadID)
	if err != nil {
		return nil, fmt.Errorf("flag targets %s: %w", threadID, err)
	}
	defer rows.Close()
	var out []FlagTarget
	for rows.Next() {
		var t FlagTarget
		var unread int
		var raw sql.NullString
		if err := rows.Scan(&t.ID, &t.FolderID, &t.Mailbox, &raw, &unread); err != nil {
			return nil, err
		}
		t.IsUnread = unread != 0
		if u, perr := strconv.ParseUint(raw.String, 10, 32); perr == nil {
			t.UID = uint32(u)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MarkThreadRead sets every message of a thread read and refreshes the
// thread aggregate.
func (s *Store) MarkThreadRead(threadID string) error {
	if _, err := s.db.Exec(`UPDATE messages SET is_unread=0 WHERE thread=?`, threadID); err != nil {
		return err
	}
	return s.UpdateThreadRead(threadID)
}

// MarkThreadUnread sets every message of a thread unread and refreshes the
// thread aggregate.
func (s *Store) MarkThreadUnread(threadID string) error {
	if _, err := s.db.Exec(`UPDATE messages SET is_unread=1 WHERE thread=?`, threadID); err != nil {
		return err
	}
	return s.UpdateThreadRead(threadID)
}

// UpdateFolderCounters recomputes a folder's total/unread thread badges from
// the local thread table. Called after flag or move operations so the UI
// sidebar counters follow immediately instead of waiting for a sync.
func (s *Store) UpdateFolderCounters(folderID string) error {
	if _, err := s.db.Exec(
		`UPDATE folders SET
		   total  = (SELECT COUNT(*) FROM threads WHERE folder = folders.id),
		   unread = (SELECT COUNT(*) FROM threads WHERE folder = folders.id AND is_unread = 1)
		 WHERE id = ?`, folderID,
	); err != nil {
		return fmt.Errorf("folder counters %s: %w", folderID, err)
	}
	return nil
}

// UpdateThreadRead keeps the thread aggregate consistent after mark-read.
func (s *Store) UpdateThreadRead(threadID string) error {
	_, err := s.db.Exec(
		`UPDATE threads SET is_unread=(SELECT COALESCE(MAX(is_unread),0) FROM messages WHERE thread=threads.id) WHERE id=?`,
		threadID,
	)
	return err
}

func snippetOf(m *domain.Message) string {
	s := m.BodyText
	if s == "" {
		s = m.Subject
	}
	return snippetTruncate(s, 140)
}

func snippetTruncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "\u2026"
}

const timeFmt = "2006-01-02T15:04:05.000000000Z07:00"

func parseDate(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}