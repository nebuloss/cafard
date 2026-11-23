-- The admin's notices are worked out from the state on every read
-- (internal/httpapi/admin, notices.go): nothing about them is stored but
-- which ones the admin has marked read, by the notice's id. A mark is
-- forgotten when its notice is gone, so a problem that comes back is news
-- again.
CREATE TABLE notices_read (
  id      TEXT    PRIMARY KEY,
  read_at INTEGER NOT NULL
) WITHOUT ROWID;
