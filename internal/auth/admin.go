package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/scrypt"

	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/ratelimit"
)

const (
	// AdminCookie holds an admin session. Only the admin interface reads
	// it, so a copy that reaches the public listener opens nothing there.
	AdminCookie = "cafard_admin"
	// AdminSessionTTL is fixed: an admin session is not renewed by use.
	AdminSessionTTL = 12 * time.Hour
	// MinPasswordLength counts characters, not bytes.
	MinPasswordLength = 8

	adminSessionPrefix = "cfa_"
)

// AdminScopes are the scopes of an admin session, the only credential
// that carries admin.
var AdminScopes = NewScopeSet(ScopeIngest, ScopeRead, ScopeMessage, ScopeAdmin)

var (
	// ErrWrongPassword is a password that does not match, or one tried
	// while none is set: the two are one error on purpose.
	ErrWrongPassword = errors.New("wrong password")
	// ErrRateLimited is a sign-in refused before the password is checked.
	ErrRateLimited = errors.New("too many sign-in attempts")
	// ErrShortPassword is a new password under MinPasswordLength.
	ErrShortPassword = fmt.Errorf("a password needs at least %d characters", MinPasswordLength)
)

// The scrypt parameters of new hashes. r and p are fixed; N is stored in
// the hash so it can be raised later without locking the admin out.
const (
	scryptN      = 32768
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 32
	scryptSalt   = 16
	scryptMaxN   = 1 << 18
)

// Admin is the one admin account: its password, stored hashed in the
// database, and its sessions, kept in memory only so a restart signs the
// admin out.
type Admin struct {
	db    *sql.DB
	clock clock.Clock
	limit *ratelimit.Limiter
	// gate bounds concurrent key derivations: each takes 32 MiB and most
	// of a core, and sign-in is reachable before any authentication.
	gate chan struct{}

	mu       sync.Mutex
	sessions map[string]time.Time // hash of the secret -> expiry
}

// NewAdmin wraps the database. Sign-in attempts are limited per remote
// address to 10 a minute.
func NewAdmin(db *sql.DB, c clock.Clock) *Admin {
	return &Admin{db: db, clock: c, limit: ratelimit.New(c, 10.0/60, 10), gate: make(chan struct{}, 2),
		sessions: map[string]time.Time{}}
}

// storedHash is the password hash, or "" when none is set.
func (a *Admin) storedHash(ctx context.Context) (string, error) {
	var h string
	err := a.db.QueryRowContext(ctx, `SELECT password_hash FROM admin WHERE id = 1`).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return h, err
}

// HasPassword reports whether a password is set.
func (a *Admin) HasPassword(ctx context.Context) (bool, error) {
	h, err := a.storedHash(ctx)
	return h != "", err
}

// SetPassword replaces the password without asking for the old one, and
// ends this process's admin sessions. It creates the admin principal when
// missing.
func (a *Admin) SetPassword(ctx context.Context, password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return ErrShortPassword
	}
	var h string
	if err := a.derive(ctx, func() (err error) { h, err = hashPassword(password); return err }); err != nil {
		return err
	}
	now := a.clock.Now()
	if _, err := ensurePrincipal(ctx, a.db, now, AdminName, KindHuman); err != nil {
		return err
	}
	ns := now.UTC().UnixNano()
	if _, err := a.db.ExecContext(ctx, `INSERT INTO admin(id, password_hash, created_at, updated_at) VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET password_hash = excluded.password_hash, updated_at = excluded.updated_at`, h, ns, ns); err != nil {
		return err
	}
	a.mu.Lock()
	clear(a.sessions)
	a.mu.Unlock()
	return nil
}

// ChangePassword is SetPassword for someone who must know the current one.
func (a *Admin) ChangePassword(ctx context.Context, current, next string) error {
	if utf8.RuneCountInString(next) < MinPasswordLength {
		return ErrShortPassword
	}
	if err := a.check(ctx, current); err != nil {
		return err
	}
	return a.SetPassword(ctx, next)
}

// EnsurePassword sets a random password when none is set, and returns it
// so the caller can show it once. It returns "" when one is set already.
func (a *Admin) EnsurePassword(ctx context.Context) (string, error) {
	ok, err := a.HasPassword(ctx)
	if err != nil || ok {
		return "", err
	}
	password, err := RandomPassword()
	if err != nil {
		return "", err
	}
	if err := a.SetPassword(ctx, password); err != nil {
		return "", err
	}
	return password, nil
}

// RandomPassword is 18 random bytes in hex.
func RandomPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// SignIn checks the password and opens an admin session, returning its
// secret for the cookie. remote is the caller's address, the key of the
// rate limit.
func (a *Admin) SignIn(ctx context.Context, remote, password string) (string, error) {
	if ok, _ := a.limit.Take(remote); !ok {
		return "", ErrRateLimited
	}
	if err := a.check(ctx, password); err != nil {
		return "", err
	}
	return a.NewSession()
}

// check returns nil when password is the admin's. It costs one derivation
// whatever happens, so timing tells nothing, not even whether a password
// is set.
func (a *Admin) check(ctx context.Context, password string) error {
	stored, err := a.storedHash(ctx)
	if err != nil {
		return err
	}
	match := false
	err = a.derive(ctx, func() error {
		if stored == "" {
			_, err := hashPassword(password)
			return err
		}
		var err error
		match, err = checkPassword(password, stored)
		return err
	})
	if err != nil {
		return err
	}
	if !match {
		return ErrWrongPassword
	}
	return nil
}

func (a *Admin) derive(ctx context.Context, f func() error) error {
	select {
	case a.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-a.gate }()
	return f()
}

// NewSession opens an admin session without a password. SignIn calls it
// once the password matched; tests call it to skip the key derivation.
func (a *Admin) NewSession() (string, error) {
	secret, err := newSecret(adminSessionPrefix)
	if err != nil {
		return "", err
	}
	now := a.clock.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	for h, expires := range a.sessions {
		if !now.Before(expires) {
			delete(a.sessions, h)
		}
	}
	a.sessions[HashToken(secret)] = now.Add(AdminSessionTTL)
	return secret, nil
}

// Lookup resolves an admin cookie to the admin principal.
func (a *Admin) Lookup(secret string) (Principal, error) {
	if !strings.HasPrefix(secret, adminSessionPrefix) {
		return Principal{}, ErrInvalid
	}
	h := HashToken(secret)
	a.mu.Lock()
	defer a.mu.Unlock()
	expires, ok := a.sessions[h]
	if !ok || !a.clock.Now().Before(expires) {
		delete(a.sessions, h)
		return Principal{}, ErrInvalid
	}
	return Principal{Name: AdminName, Kind: KindHuman, Scopes: AdminScopes}, nil
}

// SignOut ends one admin session.
func (a *Admin) SignOut(secret string) {
	a.mu.Lock()
	delete(a.sessions, HashToken(secret))
	a.mu.Unlock()
}

// hashPassword is "scrypt$N$salt$key", salt and key in hex.
func hashPassword(password string) (string, error) {
	salt := make([]byte, scryptSalt)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("scrypt$%d$%x$%x", scryptN, salt, key), nil
}

var errBadHash = errors.New("the stored admin password hash is malformed; set a new password with cafard passwd")

func checkPassword(password, stored string) (bool, error) {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "scrypt" {
		return false, errBadHash
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n < 2 || n > scryptMaxN || n&(n-1) != 0 {
		return false, errBadHash
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return false, errBadHash
	}
	want, err := hex.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false, errBadHash
	}
	got, err := scrypt.Key([]byte(password), salt, n, scryptR, scryptP, len(want))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
