-- 0002_fts5.sql — full-text search.
-- Searchable fields: subject, sender, recipient, body.
-- The FTS5 virtual table keeps its own index; the UI reads results through Go.

CREATE VIRTUAL TABLE IF NOT EXISTS mail_fts USING fts5(
  subject,
  sender_name,
  sender_email,
  recipient,
  body,
  thread_id UNINDEXED,
  message_id UNINDEXED
);

-- keep FTS in sync with messages via triggers
CREATE TRIGGER IF NOT EXISTS mail_fts_insert AFTER INSERT ON messages BEGIN
  INSERT INTO mail_fts(subject, sender_name, sender_email, recipient, body, thread_id, message_id)
  SELECT new.subject, new.from_name, new.from_email,
         (SELECT group_concat(email, ' ') FROM recipients WHERE message = new.id),
         new.body_text, new.thread, new.id;
END;

CREATE TRIGGER IF NOT EXISTS mail_fts_delete AFTER DELETE ON messages BEGIN
  DELETE FROM mail_fts WHERE message_id = old.id;
END;
