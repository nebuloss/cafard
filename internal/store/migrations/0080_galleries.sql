-- A channel is a conversation (chat) or a gallery of images with titles;
-- the messages are the same, only their presentation and one rule (a
-- gallery item needs an image) differ.
ALTER TABLE channels ADD COLUMN kind TEXT NOT NULL DEFAULT 'chat';
