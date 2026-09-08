-- 0014_import.sql — raw storage for imported messages (M11).
-- Imported messages have no provider to re-fetch from: their raw MIME lives
-- here so re-export stays faithful. Everything else in the encrypted DB.
CREATE TABLE IF NOT EXISTS import_raw (
  message_id TEXT PRIMARY KEY,
  raw        BLOB NOT NULL
);
