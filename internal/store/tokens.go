package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SetToken gives an agent or a device its token, replacing any it had: the
// old one stops working in the same transaction.
func (s *Store) SetToken(ctx context.Context, identityID int64, hash string, a AuditEntry) error {
	return s.change(ctx, a, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO tokens (identity_id, hash, created_at) VALUES (?, ?, ?)
			ON CONFLICT (identity_id) DO UPDATE SET hash = excluded.hash, created_at = excluded.created_at,
				last_used_at = NULL, last_used_from = ''`,
			identityID, hash, s.now().UnixMilli())
		return err
	})
}

// IdentityByTokenHash is the identity a token belongs to.
func (s *Store) IdentityByTokenHash(ctx context.Context, hash string) (Identity, error) {
	return scanIdentity(s.db.QueryRowContext(ctx, `SELECT `+prefixed("i", identityCols)+` FROM identities i
		JOIN tokens t ON t.identity_id = i.id WHERE t.hash = ?`, hash))
}

// TokenUse is when and from where a token was last used.
type TokenUse struct {
	CreatedAt time.Time
	LastUsed  *time.Time
	From      string
}

func (s *Store) TokenUse(ctx context.Context, identityID int64) (TokenUse, error) {
	var u TokenUse
	var created int64
	var last sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT created_at, last_used_at, last_used_from FROM tokens WHERE identity_id = ?`,
		identityID).Scan(&created, &last, &u.From)
	if errors.Is(err, sql.ErrNoRows) {
		return TokenUse{}, ErrNotFound
	}
	if err != nil {
		return TokenUse{}, err
	}
	u.CreatedAt = time.UnixMilli(created)
	u.LastUsed = fromMillis(last)
	return u, nil
}

// touchEvery is how stale "last used" may get. A device streaming a log
// authenticates once per request, and a write per request to say "still
// here" would be most of the database's writes; claudication holds the same
// figure for its keys for the same reason.
const touchEvery = time.Minute

// TouchToken records a use of an identity's token, at most once a minute.
// Not audited: using access is not changing it.
func (s *Store) TouchToken(ctx context.Context, identityID int64, from string) error {
	now := s.now()
	_, err := s.db.ExecContext(ctx, `UPDATE tokens SET last_used_at = ?, last_used_from = ?
		WHERE identity_id = ? AND (last_used_at IS NULL OR last_used_at < ? OR last_used_from <> ?)`,
		now.UnixMilli(), from, identityID, now.Add(-touchEvery).UnixMilli(), from)
	return err
}
