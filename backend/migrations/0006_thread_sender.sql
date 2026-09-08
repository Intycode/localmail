-- Threads expose the sender of their latest message so list views (folder and
-- unified inbox) can show who wrote last without per-thread follow-up queries.
ALTER TABLE threads ADD COLUMN from_name  TEXT NOT NULL DEFAULT '';
ALTER TABLE threads ADD COLUMN from_email TEXT NOT NULL DEFAULT '';

-- Backfill existing rows from each thread's latest message.
UPDATE threads SET
  from_name  = COALESCE((SELECT from_name  FROM messages WHERE thread = threads.id ORDER BY date DESC LIMIT 1), ''),
  from_email = COALESCE((SELECT from_email FROM messages WHERE thread = threads.id ORDER BY date DESC LIMIT 1), '');

-- Search fix: the FTS insert trigger (0002) reads recipients at message-insert
-- time, but messages land before their recipients (UpsertBatch), so the FTS
-- recipient column was always empty and recipients were never searchable.
-- Recompute it whenever a recipient row lands, then repair existing rows.
CREATE TRIGGER IF NOT EXISTS mail_fts_recipient AFTER INSERT ON recipients BEGIN
  UPDATE mail_fts
     SET recipient = (SELECT group_concat(email, ' ') FROM recipients WHERE message = new.message)
   WHERE message_id = new.message;
END;

UPDATE mail_fts
   SET recipient = COALESCE((SELECT group_concat(email, ' ') FROM recipients WHERE message = mail_fts.message_id), '');
