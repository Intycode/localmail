-- 0011_assess.sql — local spam/phishing assessment (M7).
-- spam_score: 0..100 heuristic score computed on device at ingest time.
-- phishing:   JSON array of human-readable flags (masked links, IP/shortener
--             destinations, brand impersonation, dangerous attachments...).
ALTER TABLE messages ADD COLUMN spam_score REAL NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN phishing TEXT NOT NULL DEFAULT '';
