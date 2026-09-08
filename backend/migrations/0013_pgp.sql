-- 0013_pgp.sql — OpenPGP support (M9).
-- pgp_pubkeys: contact public keys (not secrets: they live in the encrypted
-- local DB). Private keys live in the OS keychain via Tauri, never here.
CREATE TABLE IF NOT EXISTS pgp_pubkeys (
  email       TEXT PRIMARY KEY,
  armored     TEXT NOT NULL,
  fingerprint TEXT NOT NULL
);
ALTER TABLE messages ADD COLUMN is_encrypted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN sig_status TEXT NOT NULL DEFAULT '';
