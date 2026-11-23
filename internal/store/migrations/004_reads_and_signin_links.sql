-- How far each member has read each conversation: the last message id they
-- were shown. It is what "Read by dev-code" and unread counts come from.
CREATE TABLE reads (
  identity_id     INTEGER NOT NULL REFERENCES identities (id) ON DELETE CASCADE,
  conversation_id INTEGER NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
  message_id      INTEGER NOT NULL,
  at              INTEGER NOT NULL,
  PRIMARY KEY (identity_id, conversation_id)
) WITHOUT ROWID;

CREATE INDEX reads_by_conversation ON reads (conversation_id);

-- A fourth kind of link: "signin", a person signing in another phone from
-- their profile by QR code (docs/design.md, "Signing in"). SQLite cannot
-- change a CHECK constraint, so the table is rebuilt with it.
CREATE TABLE links_new (
  id           INTEGER PRIMARY KEY,
  hash         TEXT    NOT NULL UNIQUE,
  kind         TEXT    NOT NULL CHECK (kind IN ('person', 'password', 'agent', 'signin')),
  identity_id  INTEGER NOT NULL REFERENCES identities (id) ON DELETE CASCADE,
  created_by   TEXT    NOT NULL,
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,
  used_at      INTEGER,
  used_from    TEXT    NOT NULL DEFAULT '',
  cancelled_at INTEGER
);

INSERT INTO links_new SELECT id, hash, kind, identity_id, created_by, created_at, expires_at, used_at, used_from, cancelled_at FROM links;
DROP TABLE links;
ALTER TABLE links_new RENAME TO links;
CREATE INDEX links_by_identity ON links (identity_id);
