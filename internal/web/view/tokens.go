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
	var raw []string
	for _, s := range req.Scopes {
		raw = append(raw, string(s))
	}
	scopes, err := auth.ParseScopes(strings.Join(raw, " "))
	if err != nil {
		return dto.TokenCreated{}, dto.Errorf(dto.CodeBadRequest, "%s.", capitalise(err.Error()))
	}
	if len(scopes) == 0 {
		return dto.TokenCreated{}, dto.Errorf(dto.CodeBadRequest, "Choose at least one scope.")
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
