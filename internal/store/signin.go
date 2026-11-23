package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"cafard/internal/live"
)

// Joined lists the people who have signed in at least once, by identity id:
// whoever used a link of theirs that signs in (their join link, a later
// sign-in link, a phone's QR code, or a password link from before people
// had no passwords).
func (s *Store) Joined(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT identity_id FROM links
		WHERE used_at IS NOT NULL AND kind IN ('person', 'password', 'signin')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Signin is a browser or a phone signed in as a person.
type Signin struct {
	ID         int64
	IdentityID int64
	Surface    string
	UserAgent  string
	CreatedAt  time.Time
	LastUsed   time.Time
	From       string
	ExpiresAt  time.Time
}

// CreateSignin records a new sign-in.
func (s *Store) CreateSignin(ctx context.Context, si Signin, hash string, a AuditEntry) (Signin, error) {
	now := s.now()
	si.CreatedAt, si.LastUsed = now, now
	err := s.change(ctx, a, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO signins (identity_id, hash, surface, user_agent, created_at, last_used_at, last_used_from, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, si.IdentityID, hash, si.Surface, si.UserAgent, now.UnixMilli(), now.UnixMilli(), si.From, si.ExpiresAt.UnixMilli())
		if err != nil {
			return err
		}
		si.ID, err = res.LastInsertId()
		return err
	})
	return si, err
}

const signinCols = `id, identity_id, surface, user_agent, created_at, last_used_at, last_used_from, expires_at`

func scanSignin(r scanner) (Signin, error) {
	var si Signin
	var created, last, expires int64
	err := r.Scan(&si.ID, &si.IdentityID, &si.Surface, &si.UserAgent, &created, &last, &si.From, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Signin{}, ErrNotFound
	}
	si.CreatedAt, si.LastUsed, si.ExpiresAt = time.UnixMilli(created), time.UnixMilli(last), time.UnixMilli(expires)
	return si, err
}

// SigninByHash is the sign-in a cookie belongs to, if it has not expired.
func (s *Store) SigninByHash(ctx context.Context, hash, surface string) (Signin, error) {
	return scanSignin(s.db.QueryRowContext(ctx, `SELECT `+signinCols+` FROM signins
		WHERE hash = ? AND surface = ? AND expires_at > ?`, hash, surface, s.now().UnixMilli()))
}

// TouchSignin records a use and slides the expiry, at most once a minute.
func (s *Store) TouchSignin(ctx context.Context, id int64, from string, expires time.Time) error {
	now := s.now()
	_, err := s.db.ExecContext(ctx, `UPDATE signins SET last_used_at = ?, last_used_from = ?, expires_at = ?
		WHERE id = ? AND (last_used_at < ? OR last_used_from <> ?)`,
		now.UnixMilli(), from, expires.UnixMilli(), id, now.Add(-touchEvery).UnixMilli(), from)
	return err
}

// Signins lists a person's sign-ins, most recently used first.
func (s *Store) Signins(ctx context.Context, identityID int64) ([]Signin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+signinCols+` FROM signins WHERE identity_id = ? AND expires_at > ?
		ORDER BY last_used_at DESC`, identityID, s.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Signin
	for rows.Next() {
		si, err := scanSignin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, si)
	}
	return out, rows.Err()
}

// DeleteSignin signs one browser out.
func (s *Store) DeleteSignin(ctx context.Context, id int64, a AuditEntry) error {
	return s.change(ctx, a, func(tx *sql.Tx) error {
		return one(tx.ExecContext(ctx, `DELETE FROM signins WHERE id = ?`, id))
	})
}

// LinkKind is what a one-time link grants.
type LinkKind string

const (
	// PersonLink signs a person in, in the browser that opens it: their
	// join link, and any later sign-in link an admin makes them.
	PersonLink LinkKind = "person"
	// PasswordLink set a person's password, before people had none. None is
	// made now; old ones show in the history.
	PasswordLink LinkKind = "password"
	AgentLink    LinkKind = "agent"
	// SigninLink signs in another browser as the person who made it, from
	// their profile: the QR code a phone scans.
	SigninLink LinkKind = "signin"
)

// Link is a one-time link.
type Link struct {
	ID          int64
	Kind        LinkKind
	IdentityID  int64
	Identity    string
	CreatedBy   string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	UsedAt      *time.Time
	UsedFrom    string
	CancelledAt *time.Time
}

// State is waiting, used, cancelled or expired, at now.
func (l Link) State(now time.Time) string {
	switch {
	case l.UsedAt != nil:
		return "used"
	case l.CancelledAt != nil:
		return "cancelled"
	case !now.Before(l.ExpiresAt):
		return "expired"
	}
	return "waiting"
}

// CreateLink records a new link. Any earlier link of the same identity still
// waiting is cancelled in the same transaction: one person or agent has at
// most one way in at a time, so an old QR code left on a screen stops
// working when a new one is made.
func (s *Store) CreateLink(ctx context.Context, l Link, hash string, a AuditEntry) (Link, error) {
	now := s.now()
	l.CreatedAt = now
	err := s.change(ctx, a, func(tx *sql.Tx) error {
		// A phone's QR code and an admin's link are different ways in and
		// do not cancel each other: showing a QR code to sign in a phone
		// must not void a sign-in link an admin just sent.
		same := `kind <> 'signin'`
		if l.Kind == SigninLink {
			same = `kind = 'signin'`
		}
		if _, err := tx.ExecContext(ctx, `UPDATE links SET cancelled_at = ? WHERE identity_id = ? AND used_at IS NULL AND cancelled_at IS NULL AND `+same,
			now.UnixMilli(), l.IdentityID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO links (hash, kind, identity_id, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
			hash, l.Kind, l.IdentityID, l.CreatedBy, now.UnixMilli(), l.ExpiresAt.UnixMilli())
		if err != nil {
			return err
		}
		l.ID, err = res.LastInsertId()
		return err
	})
	return l, err
}

const linkCols = `l.id, l.kind, l.identity_id, i.name, l.created_by, l.created_at, l.expires_at, l.used_at, l.used_from, l.cancelled_at`

func scanLink(r scanner) (Link, error) {
	var l Link
	var created, expires int64
	var used, cancelled sql.NullInt64
	err := r.Scan(&l.ID, &l.Kind, &l.IdentityID, &l.Identity, &l.CreatedBy, &created, &expires, &used, &l.UsedFrom, &cancelled)
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	l.CreatedAt, l.ExpiresAt = time.UnixMilli(created), time.UnixMilli(expires)
	l.UsedAt, l.CancelledAt = fromMillis(used), fromMillis(cancelled)
	return l, err
}

func (s *Store) LinkByHash(ctx context.Context, hash string) (Link, error) {
	return scanLink(s.db.QueryRowContext(ctx, `SELECT `+linkCols+` FROM links l JOIN identities i ON i.id = l.identity_id WHERE l.hash = ?`, hash))
}

func (s *Store) Link(ctx context.Context, id int64) (Link, error) {
	return scanLink(s.db.QueryRowContext(ctx, `SELECT `+linkCols+` FROM links l JOIN identities i ON i.id = l.identity_id WHERE l.id = ?`, id))
}

// Links lists the newest links first, at most limit.
func (s *Store) Links(ctx context.Context, limit int) ([]Link, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+linkCols+` FROM links l JOIN identities i ON i.id = l.identity_id
		ORDER BY l.created_at DESC, l.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CancelLink stops a waiting link from working.
func (s *Store) CancelLink(ctx context.Context, id int64, a AuditEntry) error {
	return s.change(ctx, a, func(tx *sql.Tx) error {
		return one(tx.ExecContext(ctx, `UPDATE links SET cancelled_at = ? WHERE id = ? AND used_at IS NULL AND cancelled_at IS NULL`,
			s.now().UnixMilli(), id))
	})
}

// ErrLinkSpent is a link that is used, cancelled or expired.
var ErrLinkSpent = errors.New("link already used, cancelled or expired")

// useLink marks a link used, if it is still waiting. One statement, so two
// requests with the same link cannot both get through.
func useLink(ctx context.Context, tx *sql.Tx, id int64, from string, now time.Time) error {
	res, err := tx.ExecContext(ctx, `UPDATE links SET used_at = ?, used_from = ?
		WHERE id = ? AND used_at IS NULL AND cancelled_at IS NULL AND expires_at > ?`, now.UnixMilli(), from, id, now.UnixMilli())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrLinkSpent
	}
	return nil
}

// RedeemAgentLink uses an agent link and gives the agent its token, in one
// transaction; the previous token stops working.
func (s *Store) RedeemAgentLink(ctx context.Context, l Link, tokenHash, from string, a AuditEntry) error {
	now := s.now()
	return s.change(ctx, a, func(tx *sql.Tx) error {
		if err := useLink(ctx, tx, l.ID, from, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO tokens (identity_id, hash, created_at, last_used_from) VALUES (?, ?, ?, ?)
			ON CONFLICT (identity_id) DO UPDATE SET hash = excluded.hash, created_at = excluded.created_at,
				last_used_at = NULL, last_used_from = excluded.last_used_from`,
			l.IdentityID, tokenHash, now.UnixMilli(), from)
		return err
	})
}

// SetProjectLimits changes a project's retention and quota.
func (s *Store) SetProjectLimits(ctx context.Context, id int64, retentionDays int, quotaBytes int64, a AuditEntry) error {
	err := s.change(ctx, a, func(tx *sql.Tx) error {
		return one(tx.ExecContext(ctx, `UPDATE projects SET retention_days = ?, quota_bytes = ? WHERE id = ?`, retentionDays, quotaBytes, id))
	})
	if err == nil {
		s.publish(live.ProjectKey(id))
	}
	return err
}

// ProjectStats is what a project holds.
type ProjectStats struct {
	Conversations int
	Files         int
	Bytes         int64
}

// Stats is every project's ProjectStats, by project id. A file counts in each
// project it was sent to: the quota is what a project's members sent, even
// where the bytes are stored once for two projects.
func (s *Store) Stats(ctx context.Context) (map[int64]ProjectStats, error) {
	out := map[int64]ProjectStats{}
	rows, err := s.db.QueryContext(ctx, `SELECT project_id, COUNT(*) FROM conversations GROUP BY project_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		st := out[id]
		st.Conversations = n
		out[id] = st
	}
	rows.Close()
	rows, err = s.db.QueryContext(ctx, `SELECT c.project_id, COUNT(f.id), COALESCE(SUM(f.size), 0) FROM files f
		JOIN messages m ON m.id = f.message_id JOIN conversations c ON c.id = m.conversation_id
		WHERE f.state IN ('stored', 'live') GROUP BY c.project_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n int
		var b int64
		if err := rows.Scan(&id, &n, &b); err != nil {
			return nil, err
		}
		st := out[id]
		st.Files, st.Bytes = n, b
		out[id] = st
	}
	return out, rows.Err()
}

// TokenUses is every token's use, by identity id.
func (s *Store) TokenUses(ctx context.Context) (map[int64]TokenUse, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT identity_id, created_at, last_used_at, last_used_from FROM tokens`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]TokenUse{}
	for rows.Next() {
		var id, created int64
		var last sql.NullInt64
		var u TokenUse
		if err := rows.Scan(&id, &created, &last, &u.From); err != nil {
			return nil, err
		}
		u.CreatedAt, u.LastUsed = time.UnixMilli(created), fromMillis(last)
		out[id] = u
	}
	return out, rows.Err()
}

// LastSignins is each person's most recent sign-in use, by identity id.
func (s *Store) LastSignins(ctx context.Context) (map[int64]Signin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+signinCols+` FROM signins s1 WHERE last_used_at = (
		SELECT MAX(last_used_at) FROM signins s2 WHERE s2.identity_id = s1.identity_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]Signin{}
	for rows.Next() {
		si, err := scanSignin(rows)
		if err != nil {
			return nil, err
		}
		out[si.IdentityID] = si
	}
	return out, rows.Err()
}

// SigninCounts is how many live sign-ins each person has, by identity id.
func (s *Store) SigninCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT identity_id, COUNT(*) FROM signins WHERE expires_at > ? GROUP BY identity_id`, s.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// RedeemSigninLink uses a link that signs a person in (an admin's sign-in
// link, or a phone's QR code) and records the browser's sign-in, in one
// transaction.
func (s *Store) RedeemSigninLink(ctx context.Context, l Link, si Signin, cookieHash string, a AuditEntry) (Signin, error) {
	now := s.now()
	si.IdentityID, si.CreatedAt, si.LastUsed = l.IdentityID, now, now
	err := s.change(ctx, a, func(tx *sql.Tx) error {
		if err := useLink(ctx, tx, l.ID, si.From, now); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO signins (identity_id, hash, surface, user_agent, created_at, last_used_at, last_used_from, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, l.IdentityID, cookieHash, si.Surface, si.UserAgent, now.UnixMilli(), now.UnixMilli(), si.From, si.ExpiresAt.UnixMilli())
		if err != nil {
			return err
		}
		si.ID, err = res.LastInsertId()
		return err
	})
	return si, err
}
