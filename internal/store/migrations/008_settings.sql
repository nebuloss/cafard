-- Settings the admin changes in the admin UI, over the configuration file:
-- the file holds what the box needs to start, these what may change while it
-- runs. A key that is not here is the file's.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
