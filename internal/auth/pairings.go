package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nebuloss/cafard/internal/clock"
)

const (
	// PairingTTL is how long a pairing code can be redeemed, unless its
	// maker chose otherwise.
	PairingTTL = 5 * time.Minute
	// MaxPairingTTL bounds that choice: a code is a credential until used.
	MaxPairingTTL = 24 * time.Hour
)

// PairingScopes is what a paired phone gets unless the admin chose others.
var PairingScopes = NewScopeSet(ScopeRead, ScopeMessage, ScopeIngest)

// ErrPairingUsed is a code that is unknown, expired or already redeemed.
// The three are one error on purpose: telling them apart helps a guesser.
var ErrPairingUsed = errors.New("this pairing code is unknown, expired or already used")

// ErrBadPairing wraps a pairing request that cannot be made as asked.
var ErrBadPairing = errors.New("bad pairing")

// PairingRow is a pending pairing.
type PairingRow struct {
	ID        int64
	Principal string // existing human, or ""
	NewName   string // name for a new human, or ""
	Scopes    ScopeSet
	// Grants are the "project[:channel]=scopes" specs, space separated,
	// the person gets on redeeming it.
	Grants    string
	CreatedBy string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Name is the principal the pairing signs in as.
func (p PairingRow) Name() string {
	if p.Principal != "" {
		return p.Principal
	}
	return p.NewName
}

// Pairings owns the pairings table. Codes are stored hashed; the clear code
// of a pending pairing is also kept in memory while it lasts, so the QR can
// be drawn again on a page reload. A restart forgets them, and the admin
// makes a new one. A code made by cafard pair is never in this memory: the
// command prints it instead.
type Pairings struct {
	db       *sql.DB
	clock    clock.Clock
	sessions *Sessions
	grants   PairingGrants

	mu    sync.Mutex
	codes map[int64]string
}

// PairingGrants stores the grants of a redeemed pairing. It lives in
// internal/projects, which imports this package.
type PairingGrants interface {
	// CheckGrants refuses specs that are malformed or name no project
	// or channel, so a link is not made that would fail when scanned.
	CheckGrants(ctx context.Context, grants string) error
	// RedeemGrants adds the specs to principal inside tx, the one that
	// opens the session.
	RedeemGrants(ctx context.Context, tx *sql.Tx, principal, grants string) error
	// Invalidate runs after the commit, so access is computed again.
	Invalidate()
}

// NewPairings wraps the database; redeemed codes open sessions in sessions
// and add their grants through grants, which may be nil when no pairing
// carries any.
func NewPairings(db *sql.DB, c clock.Clock, sessions *Sessions, grants PairingGrants) *Pairings {
	return &Pairings{db: db, clock: c, sessions: sessions, grants: grants, codes: map[int64]string{}}
}

// codeAlphabet has no look-alike characters: the code is also typed by hand
// from the URL under the QR.
const codeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

func newCode() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, c := range b {
		sb.WriteByte(codeAlphabet[int(c)%len(codeAlphabet)])
	}
	return sb.String(), nil
}

// Create makes a one-time code that signs a browser in as principal (an
// existing human) or as a new human named newName. Exactly one is set. The
// code works for ttl, PairingTTL when 0, and gives the person grants.
func (p *Pairings) Create(ctx context.Context, principal, newName string, scopes ScopeSet, grants, createdBy string, ttl time.Duration) (PairingRow, string, error) {
	if (principal == "") == (newName == "") {
		return PairingRow{}, "", fmt.Errorf("%w: choose an existing person or type a new name, not both", ErrBadPairing)
	}
	if len(scopes) == 0 {
		scopes = PairingScopes
	}
	if scopes.Has(ScopeAdmin) {
		return PairingRow{}, "", fmt.Errorf("%w: %v", ErrBadPairing, ErrAdminScope)
	}
	if ttl == 0 {
		ttl = PairingTTL
	}
	if ttl < 0 || ttl > MaxPairingTTL {
		return PairingRow{}, "", fmt.Errorf("%w: a pairing code lasts at most %s", ErrBadPairing, MaxPairingTTL)
	}
	name := principal + newName
	if err := ValidateName(name); err != nil {
		return PairingRow{}, "", fmt.Errorf("%w: %v", ErrBadPairing, err)
	}
	grants = strings.Join(strings.Fields(grants), " ")
	if grants != "" {
		if p.grants == nil {
			return PairingRow{}, "", fmt.Errorf("%w: this server cannot store grants", ErrBadPairing)
		}
		if err := p.grants.CheckGrants(ctx, grants); err != nil {
			return PairingRow{}, "", fmt.Errorf("%w: %v", ErrBadPairing, err)
		}
	}
	kind, err := principalKind(ctx, p.db, name)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if principal != "" {
			return PairingRow{}, "", fmt.Errorf("%w: principal %s", ErrNotFound, principal)
		}
	case err != nil:
		return PairingRow{}, "", err
	case kind != KindHuman:
		return PairingRow{}, "", fmt.Errorf("%w: %s is a %s; only humans sign in with a browser", ErrConflict, name, kind)
	default:
		// A "new" name that already is a human just reuses that human.
		principal, newName = name, ""
	}
	code, err := newCode()
	if err != nil {
		return PairingRow{}, "", err
	}
	now := p.clock.Now().UTC()
	row := PairingRow{Principal: principal, NewName: newName, Scopes: scopes, Grants: grants, CreatedBy: createdBy, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	res, err := p.db.ExecContext(ctx, `INSERT INTO pairings(code_hash, principal, new_name, scopes, grants, created_by, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, HashToken(code), nullable(principal), nullable(newName), scopes.String(), nullable(grants), createdBy,
		now.UnixNano(), row.ExpiresAt.UnixNano())
	if err != nil {
		return PairingRow{}, "", err
	}
	if row.ID, err = res.LastInsertId(); err != nil {
		return PairingRow{}, "", err
	}
	p.mu.Lock()
	p.codes[row.ID] = code
	p.mu.Unlock()
	return row, code, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Pending lists the pairings that can still be redeemed, newest first.
func (p *Pairings) Pending(ctx context.Context) ([]PairingRow, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id, coalesce(principal, ''), coalesce(new_name, ''), scopes, coalesce(grants, ''), coalesce(created_by, ''), created_at, expires_at
		FROM pairings WHERE used_at IS NULL AND expires_at > ? ORDER BY id DESC`, p.clock.Now().UTC().UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PairingRow
	for rows.Next() {
		var (
			r                PairingRow
			scopes           string
			created, expires int64
		)
		if err := rows.Scan(&r.ID, &r.Principal, &r.NewName, &scopes, &r.Grants, &r.CreatedBy, &created, &expires); err != nil {
			return nil, err
		}
		r.Scopes, _ = ParseScopes(scopes)
		r.CreatedAt, r.ExpiresAt = time.Unix(0, created).UTC(), time.Unix(0, expires).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// Code returns the clear code of a pending pairing, if this process made it
// and it has not expired.
func (p *Pairings) Code(ctx context.Context, id int64) (string, PairingRow, bool) {
	pending, err := p.Pending(ctx)
	if err != nil {
		return "", PairingRow{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	live := map[int64]bool{}
	var found PairingRow
	for _, r := range pending {
		live[r.ID] = true
		if r.ID == id {
			found = r
		}
	}
	for k := range p.codes {
		if !live[k] {
			delete(p.codes, k)
		}
	}
	code, ok := p.codes[id]
	return code, found, ok
}

// Redeem consumes a code and opens a session for its principal, creating
// the human first when the admin typed a new name, and adding the grants
// the code carries. A code works once; all of it happens or none.
func (p *Pairings) Redeem(ctx context.Context, code, userAgent string) (SessionRow, string, error) {
	now := p.clock.Now().UTC()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionRow{}, "", err
	}
	defer tx.Rollback()
	var (
		id                      int64
		principal, name, grants sql.NullString
		scopes                  string
	)
	// One statement, so two phones scanning the same code cannot both win.
	err = tx.QueryRowContext(ctx, `UPDATE pairings SET used_at = ? WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?
		RETURNING id, principal, new_name, scopes, grants`, now.UnixNano(), HashToken(code), now.UnixNano()).
		Scan(&id, &principal, &name, &scopes, &grants)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRow{}, "", ErrPairingUsed
	}
	if err != nil {
		return SessionRow{}, "", err
	}
	who := principal.String
	if who == "" {
		who = name.String
		if _, err := tx.ExecContext(ctx, `INSERT INTO principals(name, kind, created_at) VALUES (?, ?, ?) ON CONFLICT(name) DO NOTHING`,
			who, KindHuman, now.UnixNano()); err != nil {
			return SessionRow{}, "", err
		}
	}
	kind, err := principalKind(ctx, tx, who)
	if err != nil {
		return SessionRow{}, "", err
	}
	if kind != KindHuman {
		return SessionRow{}, "", fmt.Errorf("%w: %s is a %s; only humans sign in with a browser", ErrConflict, who, kind)
	}
	set, err := ParseScopes(scopes)
	if err != nil {
		return SessionRow{}, "", err
	}
	if grants.String != "" {
		if p.grants == nil {
			return SessionRow{}, "", errors.New("this pairing carries grants and the server cannot store them")
		}
		if err := p.grants.RedeemGrants(ctx, tx, who, grants.String); err != nil {
			return SessionRow{}, "", err
		}
	}
	row, secret, err := p.sessions.create(ctx, tx, who, set, SessionLabel(userAgent)+" (paired)", userAgent)
	if err != nil {
		return SessionRow{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return SessionRow{}, "", err
	}
	p.mu.Lock()
	delete(p.codes, id)
	p.mu.Unlock()
	if p.grants != nil && grants.String != "" {
		p.grants.Invalidate()
	}
	return row, secret, nil
}
