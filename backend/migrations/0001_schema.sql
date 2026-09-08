-- 0001_schema.sql — core tables.
-- SQLite is the local source of truth. All email data lives here.
-- Cascades keep purge paths (account remove, folder wipe) FK-safe.

CREATE TABLE IF NOT EXISTS accounts (
  id       TEXT PRIMARY KEY,
  name     TEXT NOT NULL,
  email    TEXT NOT NULL,
  provider TEXT NOT NULL DEFAULT 'mock',
  active   INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS folders (
  id        TEXT PRIMARY KEY,
  account   TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  name      TEXT NOT NULL,
  type      TEXT NOT NULL DEFAULT 'custom',
  total     INTEGER NOT NULL DEFAULT 0,
  unread    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS threads (
  id           TEXT PRIMARY KEY,
  folder       TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
  account      TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  subject      TEXT NOT NULL DEFAULT '',
  snippet      TEXT NOT NULL DEFAULT '',
  last_date    TEXT NOT NULL,
  is_unread    INTEGER NOT NULL DEFAULT 0,
  is_starred   INTEGER NOT NULL DEFAULT 0,
  message_count INTEGER NOT NULL DEFAULT 1,
  has_attach   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS messages (
  id          TEXT PRIMARY KEY,
  thread      TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
  folder      TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
  account     TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  subject     TEXT NOT NULL DEFAULT '',
  from_name   TEXT NOT NULL DEFAULT '',
  from_email  TEXT NOT NULL DEFAULT '',
  body        TEXT NOT NULL DEFAULT '',
  body_text   TEXT NOT NULL DEFAULT '',
  date        TEXT NOT NULL,
  is_unread   INTEGER NOT NULL DEFAULT 0,
  is_starred  INTEGER NOT NULL DEFAULT 0,
  provider_msg_id TEXT
);

CREATE TABLE IF NOT EXISTS recipients (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  message    TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL DEFAULT 'to',  -- to | cc | bcc
  name       TEXT NOT NULL DEFAULT '',
  email      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS attachments (
  id           TEXT PRIMARY KEY,
  message      TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  size         INTEGER NOT NULL DEFAULT 0,
  content_type TEXT NOT NULL DEFAULT 'application/octet-stream'
);

CREATE TABLE IF NOT EXISTS labels (
  id    TEXT PRIMARY KEY,
  name  TEXT NOT NULL,
  color TEXT
);

CREATE TABLE IF NOT EXISTS message_labels (
  message TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  label   TEXT NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
  PRIMARY KEY (message, label)
);

CREATE TABLE IF NOT EXISTS sync_state (
  account     TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
  last_uid    INTEGER NOT NULL DEFAULT 0,
  last_sync   TEXT,
  state       TEXT NOT NULL DEFAULT 'idle'
);