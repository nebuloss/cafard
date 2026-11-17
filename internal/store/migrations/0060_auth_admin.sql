-- The admin signs in with a password on the admin interface, and the admin
-- scope now lives only on those sessions, which are kept in memory. One row
-- at most: there is one admin account.
CREATE TABLE IF NOT EXISTS admin (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  password_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- Scopes are stored space separated in canonical order, so admin is a whole
-- word. A credential left with no scope at all opened nothing but admin
-- pages, and is removed.
UPDATE tokens SET scopes = trim(replace(' ' || scopes || ' ', ' admin ', ' '))
  WHERE ' ' || scopes || ' ' LIKE '% admin %';
DELETE FROM tokens WHERE scopes = '';
UPDATE sessions SET scopes = trim(replace(' ' || scopes || ' ', ' admin ', ' '))
  WHERE ' ' || scopes || ' ' LIKE '% admin %';
DELETE FROM sessions WHERE scopes = '';
UPDATE pairings SET scopes = trim(replace(' ' || scopes || ' ', ' admin ', ' '))
  WHERE ' ' || scopes || ' ' LIKE '% admin %';
DELETE FROM pairings WHERE scopes = '';
