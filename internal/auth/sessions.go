package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nebuloss/cafard/internal/clock"
)

// SessionPrefix marks session secrets, so one pasted as a bearer token is
// refused at once instead of being looked up as a token.
const SessionPrefix = "cfs_"

const (
	// SessionTTL is how long a session lives without being used.
	SessionTTL = 30 * 24 * time.Hour
	// sessionRenewBelow: a session used with less than this left is
	// extended to SessionTTL again. Writes happen at most every ~15 days per
	// session, so a busy page does not turn every request into a write.
	sessionRenewBelow = 15 * 24 * time.Hour
)

// SessionRow is a session as stored, without its secret.
type SessionRow struct {
	ID        int64
	Principal string
	Kind      string
	Label     string
	UserAgent string
	Scopes    ScopeSet
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Sessions owns the sessions table. Its only memory is when each session
// last bumped its principal's last_seen, so values over the same database
// agree on everything that matters.
type Sessions struct {
	db    *sql.DB
	clock clock.Clock

	mu      sync.Mutex
	touched map[int64]time.Time
}

// NewSessions wraps the database.
func NewSessions(db *sql.DB, c clock.Clock) *Sessions {
	return &Sessions{db: db, clock: c, touched: map[int64]time.Time{}}
}

// Create opens a session for an existing principal and returns its secret
// in clear, once, for the cookie.
func (s *Sessions) Create(ctx context.Context, principal string, scopes ScopeSet, label, userAgent string) (SessionRow, string, error) {
	if len(scopes) == 0 {
		return SessionRow{}, "", errors.New("a session needs at least one scope")
	}
	kind, err := principalKind(ctx, s.db, principal)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionRow{}, "", fmt.Errorf("%w: principal %s", ErrNotFound, principal)
		}
		return SessionRow{}, "", err
	}
	secret, err := newSecret(SessionPrefix)
	if err != nil {
		return SessionRow{}, "", err
	}
	now := s.clock.Now().UTC()
	row := SessionRow{Principal: principal, Kind: kind, Label: label, UserAgent: clip(userAgent, 300), Scopes: scopes,
		CreatedAt: now, ExpiresAt: now.Add(SessionTTL)}
	res, err := s.db.ExecContext(ctx, `INSERT INTO sessions(hash, principal, label, user_agent, scopes, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, HashToken(secret), principal, row.Label, row.UserAgent, scopes.String(),
		now.UnixNano(), row.ExpiresAt.UnixNano())
	if err != nil {
		return SessionRow{}, "", err
	}
	if row.ID, err = res.LastInsertId(); err != nil {
		return SessionRow{}, "", err
	}
	return row, secret, nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Lookup resolves a session cookie. It is the TokenAuthenticator's
// SessionLookup. An expired session is as invalid as an unknown one.
func (s *Sessions) Lookup(ctx context.Context, cookie string) (Principal, error) {
	if !strings.HasPrefix(cookie, SessionPrefix) {
		return Principal{}, ErrInvalid
	}
	var (
		p       Principal
		scopes  string
		expires int64
	)
	err := s.db.QueryRowContext(ctx, `SELECT s.id, s.principal, p.kind, s.scopes, s.expires_at FROM sessions s
		JOIN principals p ON p.name = s.principal WHERE s.hash = ?`, HashToken(cookie)).
		Scan(&p.SessionID, &p.Name, &p.Kind, &scopes, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrInvalid
	}
	if err != nil {
		return Principal{}, err
	}
	now := s.clock.Now().UTC()
	left := time.Unix(0, expires).Sub(now)
	if left <= 0 {
		return Principal{}, ErrInvalid
	}
	if p.Scopes, err = ParseScopes(scopes); err != nil {
		return Principal{}, err
	}
	if left < sessionRenewBelow {
		// Best effort: a lost renewal is retried on the next request.
		_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE id = ?`, now.Add(SessionTTL).UnixNano(), p.SessionID)
	}
	s.mu.Lock()
	last, seen := s.touched[p.SessionID]
	due := !seen || now.Sub(last) >= touchEvery
	if due {
		s.touched[p.SessionID] = now
	}
	s.mu.Unlock()
	if due {
		// A page load is not a write, except about once a minute.
		_, _ = s.db.ExecContext(ctx, `UPDATE principals SET last_seen = ? WHERE name = ?`, now.UnixNano(), p.Name)
	}
	return p, nil
}

const sessionColumns = `s.id, s.principal, p.kind, coalesce(s.label, ''), coalesce(s.user_agent, ''), s.scopes, s.created_at, s.expires_at`

func scanSession(row interface{ Scan(...any) error }) (SessionRow, error) {
	var (
		r                SessionRow
		scopes           string
		created, expires int64
	)
	if err := row.Scan(&r.ID, &r.Principal, &r.Kind, &r.Label, &r.UserAgent, &scopes, &created, &expires); err != nil {
		return SessionRow{}, err
	}
	r.Scopes, _ = ParseScopes(scopes)
	r.CreatedAt = time.Unix(0, created).UTC()
	r.ExpiresAt = time.Unix(0, expires).UTC()
	return r, nil
}

// List lists live sessions, newest first, optionally of one principal.
func (s *Sessions) List(ctx context.Context, principal string) ([]SessionRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionColumns+` FROM sessions s JOIN principals p ON p.name = s.principal
		WHERE (? = '' OR s.principal = ?) AND s.expires_at > ? ORDER BY s.id DESC`, principal, principal, s.clock.Now().UTC().UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRow
	for rows.Next() {
		r, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get returns one session.
func (s *Sessions) Get(ctx context.Context, id int64) (SessionRow, error) {
	r, err := scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM sessions s
		JOIN principals p ON p.name = s.principal WHERE s.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRow{}, fmt.Errorf("%w: session %d", ErrNotFound, id)
	}
	return r, err
}

// Revoke deletes a session.
func (s *Sessions) Revoke(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.touched, id)
	s.mu.Unlock()
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: session %d", ErrNotFound, id)
	}
	return nil
}

// CountAdmin is how many live sessions carry the admin scope, for the
// exposure guard.
func (s *Sessions) CountAdmin(ctx context.Context) (int, error) {
	return countAdmin(ctx, s.db, `SELECT scopes FROM sessions WHERE expires_at > ?`, s.clock.Now().UTC().UnixNano())
}

// Expire deletes expired sessions and pairings that are used or expired.
// The janitor runs it.
func (s *Sessions) Expire(ctx context.Context) error {
	now := s.clock.Now().UTC().UnixNano()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM pairings WHERE expires_at <= ? OR used_at IS NOT NULL`, now)
	return err
}

// SessionLabel names a browser from its User-Agent, for the session list:
// "Firefox on Linux". It is a hint for a person, not an identification.
func SessionLabel(userAgent string) string {
	ua := userAgent
	browser := "Browser"
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/") || strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	case strings.HasPrefix(ua, "curl/"):
		browser = "curl"
	}
	system := ""
	switch {
	case strings.Contains(ua, "Android"):
		system = "Android"
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad"):
		system = "iOS"
	case strings.Contains(ua, "Windows"):
		system = "Windows"
	case strings.Contains(ua, "Mac OS X") || strings.Contains(ua, "Macintosh"):
		system = "macOS"
	case strings.Contains(ua, "Linux"):
		system = "Linux"
	}
	if system == "" {
		return browser
	}
	return browser + " on " + system
}
