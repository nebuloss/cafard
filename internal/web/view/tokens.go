package view

import (
	"context"
	"errors"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
)

// CreateToken makes a token from a TokenCreate, creating its principal
// when new, and returns it with its snippets. The /agents form and POST
// /api/tokens share it.
func CreateToken(ctx context.Context, tokens *auth.Tokens, base string, req dto.TokenCreate) (dto.TokenCreated, *dto.APIError) {
	name := strings.TrimSpace(req.Principal)
	if err := auth.ValidateName(name); err != nil {
		return dto.TokenCreated{}, dto.Errorf(dto.CodeBadRequest, "%s.", capitalise(err.Error()))
	}
	scopes, err := ParseScopes(req.Scopes)
	if err != nil {
		return dto.TokenCreated{}, dto.Errorf(dto.CodeBadRequest, "%s.", capitalise(err.Error()))
	}
	if len(scopes) == 0 {
		return dto.TokenCreated{}, dto.Errorf(dto.CodeBadRequest, "Choose at least one scope.")
	}
	if scopes.Has(auth.ScopeAdmin) {
		return dto.TokenCreated{}, dto.Errorf(dto.CodeBadRequest, "%s.", capitalise(auth.ErrAdminScope.Error()))
	}
	label := strings.TrimSpace(req.Label)
	if len(label) > 100 {
		return dto.TokenCreated{}, dto.Errorf(dto.CodeBadRequest, "A label is at most 100 bytes.")
	}
	if _, err := tokens.Principal(ctx, name); errors.Is(err, auth.ErrNotFound) {
		kind := string(req.Kind)
		if kind == "" {
			kind = auth.KindAgent
		}
		if _, err := tokens.EnsurePrincipal(ctx, name, kind); err != nil {
			return dto.TokenCreated{}, dto.Errorf(dto.CodeBadRequest, "%s.", capitalise(err.Error()))
		}
	} else if err != nil {
		return dto.TokenCreated{}, dto.Errorf("internal", "Reading the principals failed.")
	}
	row, secret, err := tokens.CreateToken(ctx, name, label, scopes)
	if err != nil {
		return dto.TokenCreated{}, dto.Errorf("internal", "Creating the token failed.")
	}
	return dto.TokenCreated{Token: Token(row), Secret: secret, Snippets: TokenSnippets(base, name, secret, scopes)}, nil
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// RevokeSession signs a browser out: any session for an admin, one's own
// for everyone else. A session p may not see is auth.ErrNotFound, so ids
// leak nothing; one already gone counts as revoked.
func RevokeSession(ctx context.Context, sessions *auth.Sessions, p auth.Principal, id int64) error {
	row, err := sessions.Get(ctx, id)
	if err != nil || (!p.Has(auth.ScopeAdmin) && row.Principal != p.Name) {
		return auth.ErrNotFound
	}
	if err := sessions.Revoke(ctx, id); err != nil && !errors.Is(err, auth.ErrNotFound) {
		return err
	}
	return nil
}

// ParseScopes checks scopes from a request.
func ParseScopes(s []dto.Scope) (auth.ScopeSet, error) {
	raw := make([]string, len(s))
	for i, sc := range s {
		raw[i] = string(sc)
	}
	return auth.ParseScopes(strings.Join(raw, " "))
}
