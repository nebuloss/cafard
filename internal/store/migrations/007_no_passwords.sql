-- People have no passwords any more: a person signs in by opening a one-time
-- sign-in link an admin made (a "person" link, the first of which is their
-- join link), or by scanning the QR code a phone already signed in shows (a
-- "signin" link). The admin UI's password is the box's, in the config.
--
-- The password links still waiting are cancelled, and the hashes go. The
-- sign-ins people have stay: nobody is signed out by this.
UPDATE links SET cancelled_at = CAST(strftime('%s', 'now') AS INTEGER) * 1000
  WHERE kind = 'password' AND used_at IS NULL AND cancelled_at IS NULL;
DROP TABLE passwords;
