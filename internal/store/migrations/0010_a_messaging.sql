-- Track A: channels and their bindings, follows, acks, claims.

CREATE TABLE IF NOT EXISTS channels (
  name TEXT PRIMARY KEY,
  title TEXT,
  topic TEXT,
  surface TEXT NOT NULL DEFAULT 'markers' CHECK (surface IN ('none', 'markers', 'errors')),
  created_by TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_activity INTEGER NOT NULL,
  archived_at INTEGER,
  source TEXT NOT NULL DEFAULT 'runtime' CHECK (source IN ('runtime', 'config'))
);

CREATE TABLE IF NOT EXISTS channel_streams (
  channel TEXT NOT NULL REFERENCES channels(name) ON DELETE CASCADE,
  pattern TEXT NOT NULL,
  PRIMARY KEY (channel, pattern)
) WITHOUT ROWID;

-- followed = 0 is an explicit unfollow: humans follow every channel
-- unless a row says otherwise, agents only when a row says so.
CREATE TABLE IF NOT EXISTS follows (
  principal TEXT NOT NULL REFERENCES principals(name) ON DELETE CASCADE,
  chat TEXT NOT NULL,
  followed INTEGER NOT NULL DEFAULT 1,
  pinned INTEGER NOT NULL DEFAULT 0,
  muted INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (principal, chat)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS acks (
  principal TEXT NOT NULL,
  seq INTEGER NOT NULL,
  at INTEGER NOT NULL,
  PRIMARY KEY (principal, seq)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS acks_seq ON acks(seq);

CREATE TABLE IF NOT EXISTS claims (
  resource TEXT PRIMARY KEY,
  principal TEXT NOT NULL,
  note TEXT,
  expires_at INTEGER NOT NULL
);
