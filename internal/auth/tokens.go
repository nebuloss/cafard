package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nebuloss/cafard/internal/clock"
)

// TokenPrefix marks cafard tokens so secret scanners recognise them.
const TokenPrefix = "cfd_"

var (
	// ErrNoCredentials means the request carried no token or session.
	ErrNoCredentials = errors.New("no credentials")
	// ErrInvalid means the credentials were present but unknown.
	ErrInvalid = errors.New("invalid credentials")
	// ErrConflict is a principal that exists with another kind.
	ErrConflict = errors.New("conflict")
	// ErrNotFound is an unknown principal or token.
	ErrNotFound = errors.New("not found")
)

// PrincipalRow is a principal as stored.
type PrincipalRow struct {
	Name      string
	Kind      string
	CreatedAt time.Time
	LastSeen  *time.Time
}

// TokenRow is a token as stored, without its secret.
type TokenRow struct {
	ID        int64
	Principal string
	Kind      string
	Label     string
	Scopes    ScopeSet
	CreatedAt time.Time
	LastUsed  *time.Time
}

// Tokens owns the principals and tokens tables.
type Tokens struct {
	db    *sql.DB
	clock clock.Clock

	mu      sync.Mutex
	touched map[int64]time.Time // token id -> last time last_used was written
}

// NewTokens wraps the database.
func NewTokens(db *sql.DB, c clock.Clock) *Tokens {
	return &Tokens{db: db, clock: c, touched: map[int64]time.Time{}}
}

// HashToken is what the database keeps instead of a token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newSecret is 32 random bytes after prefix, which tells tokens and
// session secrets apart.
func newSecret(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// EnsurePrincipal creates a principal, or returns the existing one if its
// kind matches.
func (t *Tokens) EnsurePrincipal(ctx context.Context, name, kind string) (PrincipalRow, error) {
	if err := ValidateName(name); err != nil {
		return PrincipalRow{}, err
	}
	if !ValidKind(kind) {
		return PrincipalRow{}, fmt.Errorf("principal kind %q must be agent, human or device", kind)
	}
	now := t.clock.Now().UTC()
	if _, err := t.db.ExecContext(ctx, `INSERT INTO principals(name, kind, created_at) VALUES (?, ?, ?) ON CONFLICT(name) DO NOTHING`,
		name, kind, now.UnixNano()); err != nil {
		return PrincipalRow{}, err
	}
	p, err := t.Principal(ctx, name)
	if err != nil {
		return PrincipalRow{}, err
	}
	if p.Kind != kind {
		return PrincipalRow{}, fmt.Errorf("%w: principal %s exists and is a %s, not a %s", ErrConflict, name, p.Kind, kind)
	}
	return p, nil
}

const principalColumns = `name, kind, created_at, last_seen`

func scanPrincipal(row interface{ Scan(...any) error }) (PrincipalRow, error) {
	var (
		p        PrincipalRow
		created  int64
		lastSeen sql.NullInt64
	)
	if err := row.Scan(&p.Name, &p.Kind, &created, &lastSeen); err != nil {
		return PrincipalRow{}, err
	}
	p.CreatedAt = time.Unix(0, created).UTC()
	if lastSeen.Valid {
		ls := time.Unix(0, lastSeen.Int64).UTC()
		p.LastSeen = &ls
	}
	return p, nil
}

// Principal looks one up.
func (t *Tokens) Principal(ctx context.Context, name string) (PrincipalRow, error) {
	p, err := scanPrincipal(t.db.QueryRowContext(ctx, `SELECT `+principalColumns+` FROM principals WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return PrincipalRow{}, fmt.Errorf("%w: principal %s", ErrNotFound, name)
	}
	return p, err
}

// principalKind is a principal's kind, or sql.ErrNoRows.
func principalKind(ctx context.Context, db *sql.DB, name string) (string, error) {
	var kind string
	err := db.QueryRowContext(ctx, `SELECT kind FROM principals WHERE name = ?`, name).Scan(&kind)
	return kind, err
}

// Principals lists principals by name, optionally of one kind.
func (t *Tokens) Principals(ctx context.Context, kind string) ([]PrincipalRow, error) {
	rows, err := t.db.QueryContext(ctx, `SELECT `+principalColumns+` FROM principals
		WHERE ? = '' OR kind = ? ORDER BY name`, kind, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PrincipalRow
	for rows.Next() {
		p, err := scanPrincipal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountPrincipals is how many principals exist.
func (t *Tokens) CountPrincipals(ctx context.Context) (int, error) {
	var n int
	err := t.db.QueryRowContext(ctx, `SELECT count(*) FROM principals`).Scan(&n)
	return n, err
}

// CreateToken mints a token for an existing principal and returns it in
// clear, once. Only its hash is stored.
func (t *Tokens) CreateToken(ctx context.Context, principal, label string, scopes ScopeSet) (TokenRow, string, error) {
	if len(scopes) == 0 {
		return TokenRow{}, "", errors.New("a token needs at least one scope")
	}
	p, err := t.Principal(ctx, principal)
	if err != nil {
		return TokenRow{}, "", err
	}
	secret, err := newSecret(TokenPrefix)
	if err != nil {
		return TokenRow{}, "", err
	}
	now := t.clock.Now().UTC()
	res, err := t.db.ExecContext(ctx, `INSERT INTO tokens(hash, principal, label, scopes, created_at) VALUES (?, ?, ?, ?, ?)`,
		HashToken(secret), principal, label, scopes.String(), now.UnixNano())
	if err != nil {
		return TokenRow{}, "", err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return TokenRow{}, "", err
	}
	return TokenRow{ID: id, Principal: principal, Kind: p.Kind, Label: label, Scopes: scopes, CreatedAt: now}, secret, nil
}

// ListTokens lists tokens, newest first, optionally of one principal.
func (t *Tokens) ListTokens(ctx context.Context, principal string) ([]TokenRow, error) {
	rows, err := t.db.QueryContext(ctx, `SELECT t.id, t.principal, p.kind, coalesce(t.label, ''), t.scopes, t.created_at, t.last_used
		FROM tokens t JOIN principals p ON p.name = t.principal
		WHERE ? = '' OR t.principal = ? ORDER BY t.id DESC`, principal, principal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TokenRow
	for rows.Next() {
		var (
			tr       TokenRow
			scopes   string
			created  int64
			lastUsed sql.NullInt64
		)
		if err := rows.Scan(&tr.ID, &tr.Principal, &tr.Kind, &tr.Label, &scopes, &created, &lastUsed); err != nil {
			return nil, err
		}
		tr.Scopes, _ = ParseScopes(scopes)
		tr.CreatedAt = time.Unix(0, created).UTC()
		if lastUsed.Valid {
			lu := time.Unix(0, lastUsed.Int64).UTC()
			tr.LastUsed = &lu
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

// RevokeToken deletes a token.
func (t *Tokens) RevokeToken(ctx context.Context, id int64) error {
	res, err := t.db.ExecContext(ctx, `DELETE FROM tokens WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: token %d", ErrNotFound, id)
	}
	return nil
}

// CountAdminTokens is how many tokens carry the admin scope.
func (t *Tokens) CountAdminTokens(ctx context.Context) (int, error) {
	return countAdmin(ctx, t.db, `SELECT scopes FROM tokens`)
}

// countAdmin counts the rows of query, a list of scope sets, that hold
// the admin scope.
func countAdmin(ctx context.Context, db *sql.DB, query string, args ...any) (int, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return 0, err
		}
		if set, _ := ParseScopes(s); set.Has(ScopeAdmin) {
			n++
		}
	}
	return n, rows.Err()
}

// touchEvery bounds how often last_used and last_seen are written for one
// token, so a busy agent does not turn every request into a write.
const touchEvery = time.Minute

// Lookup resolves a bearer token to its principal.
func (t *Tokens) Lookup(ctx context.Context, token string) (Principal, error) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return Principal{}, ErrInvalid
	}
	var (
		p      Principal
		scopes string
	)
	err := t.db.QueryRowContext(ctx, `SELECT t.id, t.principal, p.kind, t.scopes FROM tokens t
		JOIN principals p ON p.name = t.principal WHERE t.hash = ?`, HashToken(token)).
		Scan(&p.TokenID, &p.Name, &p.Kind, &scopes)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrInvalid
	}
	if err != nil {
		return Principal{}, err
	}
	p.Scopes, err = ParseScopes(scopes)
	if err != nil {
		return Principal{}, err
	}
	t.touch(ctx, p)
	return p, nil
}

func (t *Tokens) touch(ctx context.Context, p Principal) {
	now := t.clock.Now()
	t.mu.Lock()
	last, ok := t.touched[p.TokenID]
	if ok && now.Sub(last) < touchEvery {
		t.mu.Unlock()
		return
	}
	t.touched[p.TokenID] = now
	t.mu.Unlock()
	ns := now.UTC().UnixNano()
	// Best effort: a lost update only makes "last seen" a little stale.
	_, _ = t.db.ExecContext(ctx, `UPDATE tokens SET last_used = ? WHERE id = ?`, ns, p.TokenID)
	_, _ = t.db.ExecContext(ctx, `UPDATE principals SET last_seen = ? WHERE name = ?`, ns, p.Name)
}

// AdminName is the human principal created on first start.
const AdminName = "admin"

// Bootstrap creates the admin principal and an admin token when the
// database has no principal at all. The token is returned in clear so the
// caller can print it once; on later starts it returns "".
func (t *Tokens) Bootstrap(ctx context.Context) (string, error) {
	n, err := t.CountPrincipals(ctx)
	if err != nil || n > 0 {
		return "", err
	}
	if _, err := t.EnsurePrincipal(ctx, AdminName, KindHuman); err != nil {
		return "", err
	}
	_, secret, err := t.CreateToken(ctx, AdminName, "first start",
		NewScopeSet(ScopeIngest, ScopeRead, ScopeMessage, ScopeAdmin))
	return secret, err
}
