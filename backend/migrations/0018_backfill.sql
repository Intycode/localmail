-- 0018_backfill.sql — full-history sync + paginated thread lists.
-- lowest_uid: oldest server UID already downloaded for the folder (0 =
-- unknown). The background backfill fetches UIDs below it, newest first, and
-- moves it down after every batch, so an interrupted download resumes there.
ALTER TABLE folders ADD COLUMN lowest_uid INTEGER NOT NULL DEFAULT 0;

-- keyset pagination of thread lists (newest first)
CREATE INDEX IF NOT EXISTS idx_threads_folder_date ON threads(folder, last_date DESC, id DESC);
