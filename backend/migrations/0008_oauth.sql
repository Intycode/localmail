-- 0008_oauth.sql — OAuth2 (XOAUTH2) authentication support (M4).
-- "password" (default) keeps the previous behavior (LOGIN/PLAIN);
-- "oauth" authenticates IMAP/SMTP with an XOAUTH2 access token instead.
ALTER TABLE accounts ADD COLUMN imap_auth TEXT NOT NULL DEFAULT 'password';
ALTER TABLE accounts ADD COLUMN smtp_auth TEXT NOT NULL DEFAULT 'password';
