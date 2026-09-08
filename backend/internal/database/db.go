// Package database opens SQLite and applies the schema. It is the local
// source of truth for the mail engine. FTS5 full-text search lives here too.
//
// Encryption: with a key the database is encrypted at rest (the whole file,
// including the header — it no longer starts with "SQLite format 3"). The
// production key is generated on first run and kept in the OS keychain by
// Tauri (src-tauri/src/secrets.rs) and reaches the engine via MAIL_DB_KEY;
// it never touches disk next to the database. Tests and the smoke binary run
// without a key on plain SQLite.
package database

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"time"

	"mail/backend/migrations"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	fts5ext "github.com/ncruces/go-sqlite3/ext/fts5"
	_ "github.com/ncruces/go-sqlite3/vfs/adiantum" // registers the encrypting VFS
)

// plaintextHeader is the magic every unencrypted SQLite file starts with.
var plaintextHeader = []byte("SQLite format 3\x00")

// Open opens (and creates) the SQLite database at path and applies all
// migrations. A non-empty key encrypts the database at rest (raw hex key).
// A pre-existing PLAINTEXT database is moved aside (*.plain.bak) so the
// upgrade to encrypted-at-rest is self-healing instead of erroring forever.
func Open(path string, key ...string) (*sql.DB, error) {
	k := ""
	if len(key) > 0 {
		k = key[0]
	}
	if k != "" {
		if err := archivePlaintext(path); err != nil {
			return nil, err
		}
	}

	dsn := "file:" + path
	if k != "" {
		// the adiantum VFS encrypts every page at rest; the key material is
		// 32 bytes (64 hex digits) and travels only through the DSN
		dsn += "?vfs=adiantum&hexkey=" + k
	}
	// driver.Open (not sql.Open): the per-connection hook registers FTS5 on
	// every new pooled connection (the translated runtime ships it opt-in)
	db, err := driver.Open(dsn, func(conn *sqlite3.Conn) error {
		return fts5ext.Register(conn)
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite: single writer

	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set WAL: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if err := applyMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// archivePlaintext detects a legacy unencrypted database and moves it (with
// its WAL/SHM sidecars) out of the way: the next Open creates a fresh
// encrypted database. Only runs when a key is provided.
func archivePlaintext(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // fresh database: nothing to do
		}
		return fmt.Errorf("read db header: %w", err)
	}
	defer f.Close()
	header := make([]byte, len(plaintextHeader))
	if _, err := f.Read(header); err != nil {
		return nil // empty or unreadable: let SQLite handle it
	}
	if !bytes.Equal(header, plaintextHeader) {
		return nil // already encrypted (or not SQLite at all): keep going
	}
	// legacy plaintext database: archive it
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Rename(path+suffix, path+".plain.bak"+suffix)
	}
	fmt.Fprintf(os.Stderr, "database: trovato db in chiaro (pre-M3): archiviato come %s.plain.bak — i dati dev precedenti non sono migrati\n", path)
	return nil
}

// applyMigrations applies pending migrations, tracked in schema_migrations.
// Each migration runs in its own transaction together with its bookkeeping.
func applyMigrations(db *sql.DB) error {
	if _, err := db.Exec(
		`CREATE TABLE IF NOT EXISTS schema_migrations (
		   name       TEXT PRIMARY KEY,
		   applied_at TEXT NOT NULL
		 )`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	for _, name := range migrations.Names() {
		var applied string
		err := db.QueryRow(`SELECT name FROM schema_migrations WHERE name=?`, name).Scan(&applied)
		if err == nil {
			continue // already applied
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		content, err := migrations.Read(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(content); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations(name, applied_at) VALUES(?,?)`,
			name, time.Now().UTC().Format(time.RFC3339)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}
