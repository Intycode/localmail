-- 0009_jmap.sql — JMAP provider state (M5).
-- jmap_map keeps the stable mapping between JMAP Email ids (opaque server
-- strings) and the numeric UIDs the sync engine consumes. Rows persist even
-- after an email leaves a mailbox: the provider consults the account-wide
-- mapping to skip re-downloading emails that merely moved between folders.
CREATE TABLE IF NOT EXISTS jmap_map (
  account_id TEXT NOT NULL,
  folder_id  TEXT NOT NULL,
  email_id   TEXT NOT NULL,
  uid        INTEGER NOT NULL,
  PRIMARY KEY (folder_id, email_id)
);

CREATE INDEX IF NOT EXISTS idx_jmap_map_account_email ON jmap_map(account_id, email_id);
CREATE INDEX IF NOT EXISTS idx_jmap_map_folder_uid ON jmap_map(folder_id, uid);
