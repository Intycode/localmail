-- 0005_smtp.sql — optional outgoing SMTP configuration per account.
-- IMPORTANT: no password columns, ever (same rule as 0004_imap.sql). The SMTP
-- password is the account secret: it lives in the OS keychain and reaches the
-- engine in memory only. When the columns are empty the engine derives a
-- default (smtp.<imap-host>:465, implicit TLS, same username).

ALTER TABLE accounts ADD COLUMN smtp_host TEXT NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN smtp_port INTEGER NOT NULL DEFAULT 465;
ALTER TABLE accounts ADD COLUMN smtp_security TEXT NOT NULL DEFAULT 'tls'; -- tls | starttls | plain
ALTER TABLE accounts ADD COLUMN smtp_username TEXT NOT NULL DEFAULT '';
