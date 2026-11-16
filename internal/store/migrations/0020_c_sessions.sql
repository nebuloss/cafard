-- Track C: browser sessions and one-time pairing codes. Both are stored
-- hashed like tokens, so a copy of the database cannot mint a cookie.

CREATE TABLE IF NOT EXISTS sessions (
  id INTEGER PRIMARY KEY,
  hash TEXT NOT NULL UNIQUE,
  principal TEXT NOT NULL REFERENCES principals(name) ON DELETE CASCADE,
  label TEXT,
  user_agent TEXT,
  scopes TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_principal ON sessions(principal);
CREATE INDEX IF NOT EXISTS sessions_expires ON sessions(expires_at);

-- id is what /pair/{id}/qr.svg names; the code itself is only hashed.
CREATE TABLE IF NOT EXISTS pairings (
  id INTEGER PRIMARY KEY,
  code_hash TEXT NOT NULL UNIQUE,
  principal TEXT,
  new_name TEXT,
  scopes TEXT NOT NULL,
  created_by TEXT,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  used_at INTEGER
);
CREATE INDEX IF NOT EXISTS pairings_expires ON pairings(expires_at);
