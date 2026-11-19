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
	// ErrAdminScope is a token, session or pairing asked with the admin
	// scope, which only the admin password gives.
	ErrAdminScope = errors.New("the admin scope comes only with the admin password; a token, a session or a pairing cannot carry it")
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
	Sink      string // "project/sink" of a sink token
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
	return ensurePrincipal(ctx, t.db, t.clock.Now(), name, kind)
}

func ensurePrincipal(ctx context.Context, db *sql.DB, now time.Time, name, kind string) (PrincipalRow, error) {
	if err := ValidateName(name); err != nil {
		return PrincipalRow{}, err
	}
	if !ValidKind(kind) {
		return PrincipalRow{}, fmt.Errorf("principal kind %q must be agent, human or device", kind)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO principals(name, kind, created_at) VALUES (?, ?, ?) ON CONFLICT(name) DO NOTHING`,
		name, kind, now.UTC().UnixNano()); err != nil {
		return PrincipalRow{}, err
	}
	p, err := lookupPrincipal(ctx, db, name)
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
	return lookupPrincipal(ctx, t.db, name)
}

func lookupPrincipal(ctx context.Context, db *sql.DB, name string) (PrincipalRow, error) {
	p, err := scanPrincipal(db.QueryRowContext(ctx, `SELECT `+principalColumns+` FROM principals WHERE name = ?`, name))
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

// CreateToken mints a token for an existing principal and returns it in
// clear, once. Only its hash is stored.
func (t *Tokens) CreateToken(ctx context.Context, principal, label string, scopes ScopeSet) (TokenRow, string, error) {
	return t.createToken(ctx, principal, label, scopes, 0, "")
}

// CreateSinkToken mints a token that writes into one sink only (sinks.id
// sinkID, named ref), for a device principal: scope ingest, no grants.
func (t *Tokens) CreateSinkToken(ctx context.Context, principal, label string, sinkID int64, ref string) (TokenRow, string, error) {
	return t.createToken(ctx, principal, label, NewScopeSet(ScopeIngest), sinkID, ref)
}

func (t *Tokens) createToken(ctx context.Context, principal, label string, scopes ScopeSet, sinkID int64, ref string) (TokenRow, string, error) {
	if len(scopes) == 0 {
		return TokenRow{}, "", errors.New("a token needs at least one scope")
	}
	if scopes.Has(ScopeAdmin) {
		return TokenRow{}, "", ErrAdminScope
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
	var sink any
	if sinkID != 0 {
		sink = sinkID
	}
	res, err := t.db.ExecContext(ctx, `INSERT INTO tokens(hash, principal, label, scopes, created_at, sink) VALUES (?, ?, ?, ?, ?, ?)`,
		HashToken(secret), principal, label, scopes.String(), now.UnixNano(), sink)
	if err != nil {
		return TokenRow{}, "", err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return TokenRow{}, "", err
	}
	return TokenRow{ID: id, Principal: principal, Kind: p.Kind, Label: label, Scopes: scopes, Sink: ref, CreatedAt: now}, secret, nil
}

// ListTokens lists tokens, newest first, optionally of one principal.
func (t *Tokens) ListTokens(ctx context.Context, principal string) ([]TokenRow, error) {
	rows, err := t.db.QueryContext(ctx, `SELECT t.id, t.principal, p.kind, coalesce(t.label, ''), t.scopes, t.created_at, t.last_used,
		coalesce(k.project || '/' || k.name, '')
		FROM tokens t JOIN principals p ON p.name = t.principal LEFT JOIN sinks k ON k.id = t.sink
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
		if err := rows.Scan(&tr.ID, &tr.Principal, &tr.Kind, &tr.Label, &scopes, &created, &lastUsed, &tr.Sink); err != nil {
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
	err := t.db.QueryRowContext(ctx, `SELECT t.id, t.principal, p.kind, t.scopes, coalesce(k.project || '/' || k.name, '') FROM tokens t
		JOIN principals p ON p.name = t.principal LEFT JOIN sinks k ON k.id = t.sink WHERE t.hash = ?`, HashToken(token)).
		Scan(&p.TokenID, &p.Name, &p.Kind, &scopes, &p.Sink)
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

// AdminName is the human principal the admin acts as once signed in with
// the password.
const AdminName = "admin"
