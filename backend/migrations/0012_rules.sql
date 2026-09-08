-- 0012_rules.sql — local rules engine (M8).
-- One condition -> one action per rule, evaluated in "position" order at
-- ingest. account_id '' = the rule applies to every account.
-- field: from | to | subject | body | spam_score | has_attachment
-- op:    contains | equals | starts_with | ends_with | at_least | any
-- action: move | mark_read | star | mark_spam
CREATE TABLE IF NOT EXISTS rules (
  id         TEXT PRIMARY KEY,
  account_id TEXT NOT NULL DEFAULT '',
  name       TEXT NOT NULL,
  enabled    INTEGER NOT NULL DEFAULT 1,
  position   INTEGER NOT NULL DEFAULT 0,
  field      TEXT NOT NULL,
  op         TEXT NOT NULL,
  value      TEXT NOT NULL DEFAULT '',
  action     TEXT NOT NULL,
  target     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_rules_account ON rules(account_id, position);
