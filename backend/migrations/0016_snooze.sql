-- 0016_snooze.sql — deferred threads (snooze).
-- snooze_until  RFC 3339 moment when the thread returns to its folder.
-- snooze_folder local folder id to restore the thread into (usually Inbox).
-- The engine's snooze loop restores due rows: IMAP never sees this (purely
-- local scheduling), so no provider state is involved.
ALTER TABLE threads ADD COLUMN snooze_until TEXT NOT NULL DEFAULT '';
ALTER TABLE threads ADD COLUMN snooze_folder TEXT NOT NULL DEFAULT '';
