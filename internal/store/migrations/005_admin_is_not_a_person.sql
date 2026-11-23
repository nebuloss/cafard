-- The admin UI opens with one password, as claudication's does: its hash is
-- in the configuration file (admin-password-hash), not here, and admin
-- sign-ins live in the server's memory. No person is an admin any more, and
-- no person's sign-in reaches the admin UI.
DELETE FROM signins WHERE surface = 'admin';
UPDATE identities SET admin = 0;
