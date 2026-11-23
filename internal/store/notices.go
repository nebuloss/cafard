package store

import (
	"context"
	"time"
)

// NoticesRead are the admin's notices marked read: when, by notice id. The
// notices themselves are not stored; they are worked out from the state
// (internal/httpapi/admin), as claudication's are.
func (s *Store) NoticesRead(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, read_at FROM notices_read`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var id string
		var at int64
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		out[id] = time.UnixMilli(at)
	}
	return out, rows.Err()
}

// MarkNotices marks notices read, or unread with read false. Marking one
// read again keeps when it was first read.
func (s *Store) MarkNotices(ctx context.Context, ids []string, read bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	for _, id := range ids {
		if read {
			_, err = tx.ExecContext(ctx, `INSERT INTO notices_read (id, read_at) VALUES (?, ?) ON CONFLICT (id) DO NOTHING`, id, now)
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM notices_read WHERE id = ?`, id)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ForgetNoticesRead drops the marks of notices not among current: their
// problem is gone, and if it comes back it is news again.
func (s *Store) ForgetNoticesRead(ctx context.Context, current map[string]bool) error {
	marks, err := s.NoticesRead(ctx)
	if err != nil {
		return err
	}
	var gone []string
	for id := range marks {
		if !current[id] {
			gone = append(gone, id)
		}
	}
	if len(gone) == 0 {
		return nil
	}
	return s.MarkNotices(ctx, gone, false)
}
