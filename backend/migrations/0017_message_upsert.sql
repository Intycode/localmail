-- 0017_message_upsert.sql — messages are UPSERTed (ON CONFLICT DO UPDATE)
-- instead of INSERT OR REPLACE: REPLACE skipped the FTS delete trigger
-- (duplicate search rows) and cascade-deleted the message's labels.
-- An update now refreshes the message's FTS row when searchable text changes.
CREATE TRIGGER IF NOT EXISTS mail_fts_update AFTER UPDATE OF subject, from_name, from_email, body_text, thread ON messages
WHEN old.subject IS NOT new.subject OR old.from_name IS NOT new.from_name OR old.from_email IS NOT new.from_email
  OR old.body_text IS NOT new.body_text OR old.thread IS NOT new.thread
BEGIN
  DELETE FROM mail_fts WHERE message_id = old.id;
  INSERT INTO mail_fts(subject, sender_name, sender_email, recipient, body, thread_id, message_id)
  SELECT new.subject, new.from_name, new.from_email,
         (SELECT group_concat(email, ' ') FROM recipients WHERE message = new.id),
         new.body_text, new.thread, new.id;
END;

-- drop the duplicates REPLACE left behind (keep the newest row per message)
DELETE FROM mail_fts WHERE rowid NOT IN (SELECT MAX(rowid) FROM mail_fts GROUP BY message_id);
