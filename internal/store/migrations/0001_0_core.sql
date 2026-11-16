-- Track 0: principals, tokens, streams and the entry timeline.

CREATE TABLE IF NOT EXISTS principals (
  name TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('agent', 'human', 'device')),
  created_at INTEGER NOT NULL,
  last_seen INTEGER
);

CREATE TABLE IF NOT EXISTS tokens (
  id INTEGER PRIMARY KEY,
  hash TEXT NOT NULL UNIQUE,
  principal TEXT NOT NULL REFERENCES principals(name) ON DELETE CASCADE,
  label TEXT,
  scopes TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_used INTEGER
);
CREATE INDEX IF NOT EXISTS tokens_principal ON tokens(principal);

CREATE TABLE IF NOT EXISTS streams (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  labels TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL,
  last_seq INTEGER NOT NULL DEFAULT 0,
  retention TEXT
);

CREATE TABLE IF NOT EXISTS entries (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  src_ts INTEGER,
  stream TEXT NOT NULL REFERENCES streams(id),
  kind TEXT NOT NULL,
  severity INTEGER NOT NULL DEFAULT 6,
  host TEXT,
  app TEXT,
  text TEXT NOT NULL,
  attachments TEXT,
  sender TEXT,
  recipient TEXT,
  reply_to INTEGER,
  meta TEXT
);
CREATE INDEX IF NOT EXISTS entries_stream_seq ON entries(stream, seq);
CREATE INDEX IF NOT EXISTS entries_messages ON entries(recipient, seq) WHERE kind = 'message';
CREATE INDEX IF NOT EXISTS entries_reply ON entries(reply_to) WHERE reply_to IS NOT NULL;
CREATE INDEX IF NOT EXISTS entries_ts ON entries(ts);

CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(text, content='entries', content_rowid='seq');

CREATE TRIGGER IF NOT EXISTS entries_fts_insert AFTER INSERT ON entries BEGIN
  INSERT INTO entries_fts(rowid, text) VALUES (new.seq, new.text);
END;

CREATE TRIGGER IF NOT EXISTS entries_fts_delete AFTER DELETE ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, text) VALUES ('delete', old.seq, old.text);
END;
