-- Projects hold channels and log sinks; grants say who reaches them.
-- A project's name is in its stream ids (chan:<project>/<channel>,
-- <kind>:<project>/<sink>), so it is never renamed. Retention columns are
-- NULL only for the project the next step makes from an old install: the
-- server fills them from the config at start, which SQL cannot read.
CREATE TABLE IF NOT EXISTS projects (
  name TEXT PRIMARY KEY,
  title TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  lines_max_age INTEGER,
  lines_max_bytes INTEGER,
  messages_max_age INTEGER,
  blobs_max_bytes INTEGER
);

CREATE TABLE IF NOT EXISTS sinks (
  id INTEGER PRIMARY KEY,
  project TEXT NOT NULL REFERENCES projects(name) ON DELETE CASCADE,
  name TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  UNIQUE (project, name)
);

-- Syslog and netconsole senders of a sink: an IP or a masked CIDR.
CREATE TABLE IF NOT EXISTS sink_sources (
  sink INTEGER NOT NULL REFERENCES sinks(id) ON DELETE CASCADE,
  prefix TEXT NOT NULL,
  PRIMARY KEY (sink, prefix)
) WITHOUT ROWID;

-- At most one serial port per sink; exactly one of device, rfc2217, tcp.
CREATE TABLE IF NOT EXISTS sink_serial (
  sink INTEGER PRIMARY KEY REFERENCES sinks(id) ON DELETE CASCADE,
  device TEXT,
  rfc2217 TEXT,
  tcp TEXT,
  baud INTEGER,
  data_bits INTEGER,
  parity TEXT,
  stop_bits REAL,
  writable INTEGER NOT NULL DEFAULT 0
);

-- channel NULL grants the whole project. SQLite lets NULLs repeat in a
-- UNIQUE constraint, so the index folds them to ''.
CREATE TABLE IF NOT EXISTS grants (
  id INTEGER PRIMARY KEY,
  principal TEXT NOT NULL REFERENCES principals(name) ON DELETE CASCADE,
  project TEXT NOT NULL REFERENCES projects(name) ON DELETE CASCADE,
  channel TEXT,
  scopes TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS grants_target ON grants(principal, project, coalesce(channel, ''));
CREATE INDEX IF NOT EXISTS grants_project ON grants(project);

-- Items of the old config (serial ports, netconsole names) already made
-- into sinks, so a sink the admin deleted does not come back at start.
CREATE TABLE IF NOT EXISTS config_imports (
  key TEXT PRIMARY KEY,
  at INTEGER NOT NULL
);

-- A sink token writes only into its sink; deleting the sink revokes it.
ALTER TABLE tokens ADD COLUMN sink INTEGER REFERENCES sinks(id) ON DELETE CASCADE;

-- The grants a sign-in link gives, "project[:channel]=scopes" separated
-- by spaces; added to the person when the link is redeemed.
ALTER TABLE pairings ADD COLUMN grants TEXT;
