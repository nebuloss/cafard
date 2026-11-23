-- An agent's or device's one token. Only its SHA-256 is kept: the token is
-- 32 random bytes, so a plain hash is enough, and a stolen database yields no
-- credential. Regenerating replaces the row.
CREATE TABLE tokens (
  identity_id    INTEGER PRIMARY KEY REFERENCES identities (id) ON DELETE CASCADE,
  hash           TEXT    NOT NULL UNIQUE,
  created_at     INTEGER NOT NULL,
  last_used_at   INTEGER,
  last_used_from TEXT    NOT NULL DEFAULT ''
);

-- A thread in a project. name is its short name, the second half of its
-- address (be98/wifi-drops); title is what people read.
CREATE TABLE conversations (
  id         INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
  name       TEXT    NOT NULL,
  title      TEXT    NOT NULL,
  created_by INTEGER REFERENCES identities (id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  closed_at  INTEGER,
  UNIQUE (project_id, name)
);

CREATE TABLE messages (
  id              INTEGER PRIMARY KEY,
  conversation_id INTEGER NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
  -- Never null and never cascaded: an identity is deleted by marking it, so
  -- every message keeps its author (rule 4).
  author_id       INTEGER NOT NULL REFERENCES identities (id),
  text            TEXT    NOT NULL DEFAULT '',
  created_at      INTEGER NOT NULL
);

CREATE INDEX messages_by_conversation ON messages (conversation_id, id);

-- A file attached to a message. sha256 is null while the upload is live
-- (still arriving, `curl -T -`) and once it was cut off; the bytes are then
-- in the blob store's staging area under the file's id, or gone.
CREATE TABLE files (
  id           INTEGER PRIMARY KEY,
  message_id   INTEGER NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
  name         TEXT    NOT NULL,
  content_type TEXT    NOT NULL,
  size         INTEGER NOT NULL DEFAULT 0,
  sha256       TEXT,
  state        TEXT    NOT NULL DEFAULT 'live' CHECK (state IN ('live', 'stored', 'cut', 'deleted')),
  created_at   INTEGER NOT NULL
);

CREATE INDEX files_by_message ON files (message_id);
CREATE INDEX files_by_sha256 ON files (sha256);
