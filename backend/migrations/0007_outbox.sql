-- 0007_outbox.sql — offline-first queue.
-- Every operation the UI requested while the engine could not reach the mail
-- server (send, flag changes, moves, deletes) lands here. The outbox worker
-- replays items with exponential backoff and publishes outbox.updated /
-- connection.state events. Secrets are never stored: the payload references
-- the account, the credential comes from the runtime vault at replay time.
CREATE TABLE IF NOT EXISTS pending_ops (
  id              TEXT PRIMARY KEY,
  account_id      TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  kind            TEXT NOT NULL,
  payload         TEXT NOT NULL,
  created_at      TEXT NOT NULL,
  attempts        INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT NOT NULL,
  last_error      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_pending_ops_due ON pending_ops(next_attempt_at);
