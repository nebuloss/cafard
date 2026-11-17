// Package auth holds principals, tokens and the checks that guard every
// route: who is calling, with which scopes, from which origin.
package auth

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Scope is one permission a token or session carries.
type Scope string

const (
	ScopeIngest      Scope = "ingest"
	ScopeRead        Scope = "read"
	ScopeMessage     Scope = "message"
	ScopeSerialWrite Scope = "serial_write"
	ScopeAdmin       Scope = "admin"
)

// AllScopes in canonical order.
var AllScopes = []Scope{ScopeIngest, ScopeRead, ScopeMessage, ScopeSerialWrite, ScopeAdmin}

// GrantableScopes are those a token, a browser session or a pairing may
// carry. The admin scope exists only on an admin session (Admin), which
// the password opens on the admin interface.
var GrantableScopes = []Scope{ScopeIngest, ScopeRead, ScopeMessage, ScopeSerialWrite}

// ScopeSet is a sorted, duplicate-free list of scopes. Scopes are
// independent: admin does not imply the others.
type ScopeSet []Scope

// ParseScopes reads scopes separated by commas or spaces.
func ParseScopes(s string) (ScopeSet, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
	set := make(ScopeSet, 0, len(fields))
	for _, f := range fields {
		sc := Scope(f)
		if !slices.Contains(AllScopes, sc) {
			return nil, fmt.Errorf("unknown scope %q (known: ingest, read, message, serial_write, admin)", f)
		}
		set = append(set, sc)
	}
	return NewScopeSet(set...), nil
}

// NewScopeSet sorts and deduplicates.
func NewScopeSet(scopes ...Scope) ScopeSet {
	out := make(ScopeSet, 0, len(scopes))
	for _, canonical := range AllScopes {
		if slices.Contains(scopes, canonical) {
			out = append(out, canonical)
		}
	}
	return out
}

func (s ScopeSet) Has(scope Scope) bool { return slices.Contains(s, scope) }

// String is the space separated form stored in the database.
func (s ScopeSet) String() string {
	parts := make([]string, len(s))
	for i, sc := range s {
		parts[i] = string(sc)
	}
	return strings.Join(parts, " ")
}

// Strings is the scopes as plain strings.
func (s ScopeSet) Strings() []string {
	return strings.Fields(s.String())
}

// Principal kinds.
const (
	KindAgent  = "agent"
	KindHuman  = "human"
	KindDevice = "device"
)

// ValidKind reports whether k is a principal kind.
func ValidKind(k string) bool {
	return k == KindAgent || k == KindHuman || k == KindDevice
}

// Principal is whoever made the request, as established by a token or a
// session. A message's sender always comes from here.
type Principal struct {
	Name      string
	Kind      string
	Scopes    ScopeSet
	TokenID   int64 // 0 for a session
	SessionID int64 // 0 for a bearer token
}

func (p Principal) Has(scope Scope) bool { return p.Scopes.Has(scope) }

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

// ValidateName checks a principal name: a lowercase slug of 1 to 40
// characters. "~" separates the two names of a direct conversation and "@"
// starts @humans, so neither may appear.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("principal name %q must be 1 to 40 characters of a-z, 0-9, '.', '_' or '-', starting with a letter or digit", name)
	}
	return nil
}

type ctxKey struct{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// PrincipalFrom returns the principal Require put in the context.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}
