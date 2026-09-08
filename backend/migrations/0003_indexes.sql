-- 0003_indexes.sql — common query indexes for fast local reads.

CREATE INDEX IF NOT EXISTS idx_threads_folder ON threads(folder);
CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(thread);
CREATE INDEX IF NOT EXISTS idx_messages_folder ON messages(folder);
CREATE INDEX IF NOT EXISTS idx_recipients_message ON recipients(message);
