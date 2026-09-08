-- 0010_remote_images.sql — anti-tracking (M6).
-- remote_images = number of remote <img> the ingest sanitizer left in the
-- HTML body (tracking pixels are removed outright); the UI gates them behind
-- explicit consent per message.
ALTER TABLE messages ADD COLUMN remote_images INTEGER NOT NULL DEFAULT 0;
