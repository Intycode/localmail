-- 0015_semantic.sql — optional semantic search (M10).
-- message_embeddings keeps one vector per message (BLOB of float32 LE) in the
-- encrypted DB; cosine similarity is computed in Go over the scope rows —
-- no SQLite extension needed. app_config is a tiny engine-side key/value
-- store (search model name today, more later).

CREATE TABLE IF NOT EXISTS message_embeddings (
  message_id TEXT PRIMARY KEY,
  account    TEXT NOT NULL,
  model      TEXT NOT NULL,
  dim        INTEGER NOT NULL,
  vector     BLOB NOT NULL,
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_embeddings_account ON message_embeddings(account);

CREATE TABLE IF NOT EXISTS app_config (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
