-- 0004_imap.sql — generic IMAP accounts.
-- IMPORTANT: no password columns, ever. Passwords/app-passwords live in the
-- OS keychain via Tauri and are passed to the engine in memory only.

ALTER TABLE accounts ADD COLUMN imap_host TEXT NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN imap_port INTEGER NOT NULL DEFAULT 993;
ALTER TABLE accounts ADD COLUMN imap_security TEXT NOT NULL DEFAULT 'tls'; -- tls | starttls | plain
ALTER TABLE accounts ADD COLUMN imap_username TEXT NOT NULL DEFAULT '';

-- per-folder IMAP sync bookkeeping
ALTER TABLE folders ADD COLUMN imap_name TEXT NOT NULL DEFAULT '';
ALTER TABLE folders ADD COLUMN uidvalidity INTEGER NOT NULL DEFAULT 0;
ALTER TABLE folders ADD COLUMN last_uid INTEGER NOT NULL DEFAULT 0;

ALTER TABLE sync_state ADD COLUMN uidvalidity INTEGER NOT NULL DEFAULT 0;

-- RFC 5322 threading metadata (kept locally only; not exposed to the UI).
-- 'references' is a reserved SQL word: the column is named references_hdr.
ALTER TABLE messages ADD COLUMN message_id TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN in_reply_to TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN references_hdr TEXT NOT NULL DEFAULT '';
