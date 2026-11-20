-- Everyone who can reach cafard. People, agents and devices share one table
-- because they share one namespace: @name has to mean exactly one member, so
-- a person and an agent cannot both be called dev-code.
--
-- A deleted identity keeps its row, marked deleted_at, so its messages still
-- name their author ("dev-code (removed)"). Its name stays taken: a new
-- dev-code would otherwise appear to have written the old one's messages.
CREATE TABLE identities (
  id           INTEGER PRIMARY KEY,
  name         TEXT    NOT NULL UNIQUE,
  kind         TEXT    NOT NULL CHECK (kind IN ('person', 'agent', 'device')),
  display_name TEXT    NOT NULL DEFAULT '',
  -- For an agent, what its machine can do; agents read it to know whom to ask.
  description  TEXT    NOT NULL DEFAULT '',
  -- Only a person can be an admin: an agent that could manage access would
  -- be an agent that could widen its own.
  admin        INTEGER NOT NULL DEFAULT 0 CHECK (admin IN (0, 1) AND (admin = 0 OR kind = 'person')),
  enabled      INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  created_at   INTEGER NOT NULL,
  deleted_at   INTEGER
);

CREATE TABLE projects (
  id             INTEGER PRIMARY KEY,
  name           TEXT    NOT NULL UNIQUE,
  retention_days INTEGER NOT NULL CHECK (retention_days > 0),
  quota_bytes    INTEGER NOT NULL CHECK (quota_bytes > 0),
  archived_at    INTEGER,
  created_at     INTEGER NOT NULL
);

-- Membership is the only permission (docs/design.md, "Access and attention").
CREATE TABLE memberships (
  project_id  INTEGER NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
  identity_id INTEGER NOT NULL REFERENCES identities (id) ON DELETE CASCADE,
  added_by    INTEGER REFERENCES identities (id) ON DELETE SET NULL,
  added_at    INTEGER NOT NULL,
  PRIMARY KEY (project_id, identity_id)
) WITHOUT ROWID;

CREATE INDEX memberships_by_identity ON memberships (identity_id, project_id);

-- Every change to who can reach what. Names rather than ids, so a line still
-- reads correctly after what it names is deleted.
CREATE TABLE audit (
  id     INTEGER PRIMARY KEY,
  at     INTEGER NOT NULL,
  actor  TEXT    NOT NULL,
  action TEXT    NOT NULL,
  target TEXT    NOT NULL,
  origin TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX audit_by_time ON audit (at);
