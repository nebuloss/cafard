package store

import (
	"context"
	"database/sql"
	"errors"
)

// SettingPublicURL is the public URL the admin set over the file's.
const SettingPublicURL = "public-url"

// Setting is a setting's value, "" when the admin has not set it.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting sets a setting, or with "" goes back to the file's, and records
// a: where links point is the admin's to change, and worth knowing who did.
func (s *Store) SetSetting(ctx context.Context, a AuditEntry, key, value string) error {
	return s.change(ctx, a, func(tx *sql.Tx) error {
		var err error
		if value == "" {
			_, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
				ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
		}
		return err
	})
}
