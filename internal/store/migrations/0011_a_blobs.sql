-- Track A: content-addressed files and their image variants.

CREATE TABLE IF NOT EXISTS blobs (
  sha256 TEXT PRIMARY KEY,
  mime TEXT NOT NULL,
  size INTEGER NOT NULL,
  width INTEGER,
  height INTEGER,
  created_at INTEGER NOT NULL,
  agent_sha256 TEXT,
  thumb_sha256 TEXT
);
CREATE INDEX IF NOT EXISTS blobs_created ON blobs(created_at);
