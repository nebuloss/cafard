-- A person's password, argon2id in PHC string form. A person with no row
-- here has not joined yet: their join link is what sets it.
CREATE TABLE passwords (
  identity_id INTEGER PRIMARY KEY REFERENCES identities (id) ON DELETE CASCADE,
  hash        TEXT    NOT NULL,
  set_at      INTEGER NOT NULL
);

-- A signed-in browser or phone: a "sign-in", never a "device", which is the
-- send-only identity. Only the SHA-256 of the cookie is kept. surface is
-- where it signs in to: the admin UI and the chat app have sign-ins of their
-- own, so signing in to one is not signing in to the other.
CREATE TABLE signins (
  id             INTEGER PRIMARY KEY,
  identity_id    INTEGER NOT NULL REFERENCES identities (id) ON DELETE CASCADE,
  hash           TEXT    NOT NULL UNIQUE,
  surface        TEXT    NOT NULL CHECK (surface IN ('admin', 'chat')),
  user_agent     TEXT    NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  last_used_at   INTEGER NOT NULL,
  last_used_from TEXT    NOT NULL DEFAULT '',
  expires_at     INTEGER NOT NULL
);

CREATE INDEX signins_by_identity ON signins (identity_id);

-- One-time links (docs/design.md, "Creating an identity"). The code is in
-- the URL and only its SHA-256 here. kind:
--   person    a new person chooses a password
--   password  a person who has one sets a new one, ending every sign-in
--   agent     an agent's machine exchanges it for the agent's token
CREATE TABLE links (
  id           INTEGER PRIMARY KEY,
  hash         TEXT    NOT NULL UNIQUE,
  kind         TEXT    NOT NULL CHECK (kind IN ('person', 'password', 'agent')),
  identity_id  INTEGER NOT NULL REFERENCES identities (id) ON DELETE CASCADE,
  created_by   TEXT    NOT NULL,
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,
  used_at      INTEGER,
  used_from    TEXT    NOT NULL DEFAULT '',
  cancelled_at INTEGER
);

CREATE INDEX links_by_identity ON links (identity_id);
