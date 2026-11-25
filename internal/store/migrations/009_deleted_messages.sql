-- A message its sender deleted: its text is gone and its files with it, but
-- the message stays as "deleted", so the replies around it keep their place
-- and the read positions their ids. One its sender edited says so.
ALTER TABLE messages ADD COLUMN deleted_at INTEGER;
ALTER TABLE messages ADD COLUMN edited_at INTEGER;
