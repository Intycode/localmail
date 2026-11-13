// Package account manages accounts and folders in the local database.
// Account rows hold only non-secret IMAP metadata; the password lives in the
// OS keychain and is never written here.
package account

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"mail/backend/internal/domain"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// Create inserts a new account (and its default folders).
func (s *Store) Create(a domain.Account) error {
	var host, security, username, imapAuth string
	port := 993
	if a.Imap != nil {
		host, port, security, username = a.Imap.Host, a.Imap.Port, a.Imap.Security, a.Imap.Username
		imapAuth = a.Imap.Auth
	}
	if imapAuth == "" {
		imapAuth = "password"
	}
	var sHost, sSecurity, sUsername, smtpAuth string
	sPort := 465
	if a.Smtp != nil && a.Smtp.Host != "" {
		sHost, sPort, sSecurity, sUsername = a.Smtp.Host, a.Smtp.Port, a.Smtp.Security, a.Smtp.Username
		smtpAuth = a.Smtp.Auth
	}
	if smtpAuth == "" {
		smtpAuth = "password"
	}
	_, err := s.db.Exec(
		`INSERT INTO accounts(id,name,email,provider,active,created_at,imap_host,imap_port,imap_security,imap_username,imap_auth,smtp_host,smtp_port,smtp_security,smtp_username,smtp_auth)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Name, a.Email, a.Provider, boolInt(a.Active), time.Now().UTC().Format(time.RFC3339),
		host, port, security, username, imapAuth, sHost, sPort, sSecurity, sUsername, smtpAuth,
	)
	if err != nil {
		return fmt.Errorf("create account: %w", err)
	}
	for _, f := range a.Folders {
		if err := s.CreateFolder(f); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateFolder(f domain.Folder) error {
	_, err := s.db.Exec(
		`INSERT INTO folders(id,account,name,type,total,unread,imap_name,uidvalidity,last_uid) VALUES(?,?,?,?,?,?,?,0,0)`,
		f.ID, f.Account, f.Name, f.Type, f.Total, f.Unread, f.Name,
	)
	if err != nil {
		return fmt.Errorf("create folder: %w", err)
	}
	return nil
}

// List returns all accounts with their folders (no secrets: IMAP metadata only).
func (s *Store) List() ([]domain.Account, error) {
	rows, err := s.db.Query(
		`SELECT id,name,email,provider,active,imap_host,imap_port,imap_security,imap_username,COALESCE(imap_auth,'password'),
		        smtp_host,smtp_port,smtp_security,smtp_username,COALESCE(smtp_auth,'password')
		 FROM accounts ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	// scan everything first, then close rows before issuing folder queries
	// (SQLite is a single connection; nested queries while rows are open deadlock)
	var accs []domain.Account
	for rows.Next() {
		var a domain.Account
		var active int
		var host, security, username, imapAuth string
		var port int
		var sHost, sSecurity, sUsername, smtpAuth string
		var sPort int
		if err := rows.Scan(&a.ID, &a.Name, &a.Email, &a.Provider, &active, &host, &port, &security, &username, &imapAuth,
			&sHost, &sPort, &sSecurity, &sUsername, &smtpAuth); err != nil {
			rows.Close()
			return nil, err
		}
		a.Active = active == 1
		if a.Provider == "generic-imap" || a.Provider == "jmap" {
			// for jmap accounts, Imap.Host carries the JMAP session URL and
			// Smtp stays empty (sending goes through EmailSubmission)
			a.Imap = &domain.ImapConfig{Host: host, Port: port, Security: security, Username: username, Auth: imapAuth}
			if a.Provider == "generic-imap" && sHost != "" {
				a.Smtp = &domain.SmtpConfig{Host: sHost, Port: sPort, Security: sSecurity, Username: sUsername, Auth: smtpAuth}
			}
		}
		accs = append(accs, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	for i := range accs {
		folders, err := s.Folders(accs[i].ID)
		if err != nil {
			return nil, err
		}
		accs[i].Folders = folders
	}
	return accs, nil
}

// Folders returns the folders of one account.
func (s *Store) Folders(accountID string) ([]domain.Folder, error) {
	// Rank-based order (inbox, sent, drafts, archive, trash, junk, custom),
	// mirroring folderRank at account creation; alphabetical within a rank.
	rows, err := s.db.Query(
		`SELECT id,account,name,type,total,unread FROM folders WHERE account=?
		 ORDER BY CASE type
		   WHEN 'inbox' THEN 0
		   WHEN 'sent' THEN 1
		   WHEN 'drafts' THEN 2
		   WHEN 'archive' THEN 3
		   WHEN 'trash' THEN 4
		   ELSE 5
		 END, name`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Folder
	for rows.Next() {
		var f domain.Folder
		if err := rows.Scan(&f.ID, &f.Account, &f.Name, &f.Type, &f.Total, &f.Unread); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Exists reports whether an account with the same email+host already exists.
func (s *Store) Exists(email, imapHost string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM accounts WHERE email=? AND imap_host=?`, email, imapHost,
	).Scan(&n)
	return n > 0, err
}

// SyncRef is the per-folder incremental sync state.
type SyncRef struct {
	FolderID    string
	MailboxName string
	UIDValidity uint32
	LastUID     uint32
	// LowestUID is the oldest server UID already downloaded (0 = unknown):
	// the history backfill fetches the UIDs below it.
	LowestUID uint32
}

// SyncRefs returns the syncable folders of an account. Folders synced before
// the backfill existed (lowest_uid 0 but last_uid set) derive their cursor
// from the oldest message synced into them, or sit just above last_uid when
// the folder is empty locally.
func (s *Store) SyncRefs(accountID string) ([]SyncRef, error) {
	rows, err := s.db.Query(
		`SELECT id, imap_name, uidvalidity, last_uid,
		   CASE WHEN lowest_uid > 0 OR last_uid = 0 THEN lowest_uid
		        ELSE COALESCE(
		          (SELECT MIN(CAST(provider_msg_id AS INTEGER)) FROM messages
		            WHERE folder = folders.id
		              AND substr(id, 1, length('m:' || folders.account || ':' || folders.id || ':'))
		                  = 'm:' || folders.account || ':' || folders.id || ':'),
		          last_uid + 1)
		   END
		 FROM folders
		 WHERE account=? AND imap_name<>'' AND type<>'drafts'`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SyncRef
	for rows.Next() {
		var r SyncRef
		if err := rows.Scan(&r.FolderID, &r.MailboxName, &r.UIDValidity, &r.LastUID, &r.LowestUID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateFolderSyncState persists incremental state after a folder sync.
func (s *Store) UpdateFolderSyncState(folderID string, uidValidity, lastUID, lowestUID uint32) error {
	_, err := s.db.Exec(
		`UPDATE folders SET
		   uidvalidity=?,
		   last_uid=?,
		   lowest_uid=?,
		   total    =(SELECT COUNT(*) FROM threads WHERE folder=?),
		   unread   =(SELECT COUNT(*) FROM threads WHERE folder=? AND is_unread=1)
		 WHERE id=?`,
		uidValidity, lastUID, lowestUID, folderID, folderID, folderID,
	)
	return err
}

// UpdateFolderLowestUID moves the backfill cursor down after a stored batch
// (only when UIDVALIDITY still matches: a reset folder restarts from zero),
// and refreshes the counters the sidebar shows.
func (s *Store) UpdateFolderLowestUID(folderID string, uidValidity, lowestUID uint32) error {
	_, err := s.db.Exec(
		`UPDATE folders SET
		   lowest_uid=?,
		   total    =(SELECT COUNT(*) FROM threads WHERE folder=?),
		   unread   =(SELECT COUNT(*) FROM threads WHERE folder=? AND is_unread=1)
		 WHERE id=? AND uidvalidity=?`,
		lowestUID, folderID, folderID, folderID, uidValidity,
	)
	return err
}

// RefreshFolderCounters recomputes total/unread after out-of-band writes
// (sent messages, drafts) without touching the incremental sync state.
func (s *Store) RefreshFolderCounters(folderID string) error {
	_, err := s.db.Exec(
		`UPDATE folders SET
		   total =(SELECT COUNT(*) FROM threads WHERE folder=?),
		   unread=(SELECT COUNT(*) FROM threads WHERE folder=? AND is_unread=1)
		 WHERE id=?`,
		folderID, folderID, folderID,
	)
	return err
}

// EnsureDraftsFolder returns the account's drafts folder (id + IMAP mailbox
// name, empty when local-only), creating a local-only folder when the server
// has none. Drafts are local-first: the IMAP APPEND is best effort.
func (s *Store) EnsureDraftsFolder(accountID string) (string, string, error) {
	var id, mailbox string
	err := s.db.QueryRow(
		`SELECT id, COALESCE(imap_name,'') FROM folders WHERE account=? AND type='drafts' LIMIT 1`,
		accountID,
	).Scan(&id, &mailbox)
	if err == nil {
		return id, mailbox, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}
	id = fmt.Sprintf("f:%s:drafts", accountID)
	_, err = s.db.Exec(
		`INSERT INTO folders(id,account,name,type,imap_name,uidvalidity,last_uid,total,unread)
		 VALUES(?,?,?,?,?,0,0,0,0)`,
		id, accountID, "Bozze", "drafts", "",
	)
	if err != nil {
		return "", "", err
	}
	return id, "", nil
}

// EnsureImportFolder returns the account's local import folder (id, mailbox
// always empty), creating it when missing. Imported messages are local-only:
// an empty imap_name keeps them out of SyncRefs, so the sync engine never
// touches (and never wipes) them.
func (s *Store) EnsureImportFolder(accountID string) (string, error) {
	var id string
	err := s.db.QueryRow(
		`SELECT id FROM folders WHERE account=? AND type='import' LIMIT 1`,
		accountID,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = fmt.Sprintf("f:%s:import", accountID)
	_, err = s.db.Exec(
		`INSERT INTO folders(id,account,name,type,imap_name,uidvalidity,last_uid,total,unread)
		 VALUES(?,?,?,?,?,0,0,0,0)`,
		id, accountID, "Importate", "import", "",
	)
	if err != nil {
		return "", err
	}
	return id, nil
}

// EnsureSnoozedFolder returns the account's local snooze folder (id, mailbox
// always empty), creating it when missing. Snoozed threads live here while
// waiting: purely local scheduling, the IMAP server is never involved.
func (s *Store) EnsureSnoozedFolder(accountID string) (string, error) {
	var id string
	err := s.db.QueryRow(
		`SELECT id FROM folders WHERE account=? AND type='snoozed' LIMIT 1`,
		accountID,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = fmt.Sprintf("f:%s:snoozed", accountID)
	_, err = s.db.Exec(
		`INSERT INTO folders(id,account,name,type,imap_name,uidvalidity,last_uid,total,unread)
		 VALUES(?,?,?,?,?,0,0,0,0)`,
		id, accountID, "Snoozed", "snoozed", "",
	)
	if err != nil {
		return "", err
	}
	return id, nil
}

// Delete purges an account and all its local data (privacy: full local wipe).
// Order matters: children first, FTS rows go via their DELETE triggers.
func (s *Store) Delete(accountID string) error {
	stmts := []string{
		`DELETE FROM message_labels WHERE message IN (SELECT id FROM messages WHERE account=?)`,
		`DELETE FROM attachments WHERE message IN (SELECT id FROM messages WHERE account=?)`,
		`DELETE FROM recipients WHERE message IN (SELECT id FROM messages WHERE account=?)`,
		`DELETE FROM messages WHERE account=?`,
		`DELETE FROM threads WHERE account=?`,
		`DELETE FROM folders WHERE account=?`,
		`DELETE FROM sync_state WHERE account=?`,
		`DELETE FROM accounts WHERE id=?`,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range stmts {
		if _, err := tx.Exec(q, accountID); err != nil {
			return fmt.Errorf("delete account %s: %w", accountID, err)
		}
	}
	return tx.Commit()
}

// SetActive marks one account active and the rest inactive.
func (s *Store) SetActive(accountID string) error {
	if _, err := s.db.Exec(`UPDATE accounts SET active=0`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE accounts SET active=1 WHERE id=?`, accountID); err != nil {
		return err
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
