package projects

import (
	"context"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
)

// A personal space is a hidden project per principal, "~<name>", that
// holds its private channels (DECISIONS 2026-11-20, "private channels").
// No project name may start with "~", so it cannot collide with one an
// admin makes. Its owner has read and message on the whole of it, each
// member read and message on the channels they were added to, and the
// admin nothing without a grant: unlike other projects, a personal space
// is not part of what the admin session reaches. It is never listed as a
// project; its channels show under the reader's direct messages.

// PersonalPrefix starts every personal space's name.
const PersonalPrefix = "~"

// Personal reports whether project is a personal space.
func Personal(project string) bool { return strings.HasPrefix(project, PersonalPrefix) }

// PersonalOf is the personal space of a principal.
func PersonalOf(owner string) string { return PersonalPrefix + owner }

// OwnerOf is the owner of a personal space, or "" for another project.
func OwnerOf(project string) string {
	owner, ok := strings.CutPrefix(project, PersonalPrefix)
	if !ok {
		return ""
	}
	return owner
}

// PersonalScopes are what an owner and a member get.
var PersonalScopes = auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage)

// EnsurePersonal makes owner's personal space when missing, without the
// hooks of Create (it has no #general), and grants it to its owner.
func (s *Service) EnsurePersonal(ctx context.Context, owner string) (Project, error) {
	if err := auth.ValidateName(owner); err != nil {
		return Project{}, dto.Errorf(dto.CodeBadRequest, "%v.", err)
	}
	name := PersonalOf(owner)
	now := s.clock.Now().UTC()
	d := s.defaults
	if _, err := s.db.ExecContext(ctx, `INSERT INTO projects(name, title, created_at, lines_max_age, lines_max_bytes, messages_max_age, blobs_max_bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(name) DO NOTHING`,
		name, "Private channels of "+owner, now.UnixNano(), int64(d.LinesMaxAge), d.LinesMaxBytes, int64(d.MessagesMaxAge), d.BlobsMaxBytes); err != nil {
		return Project{}, err
	}
	if err := s.AddGrants(ctx, Grant{Principal: owner, Project: name, Scopes: PersonalScopes}); err != nil {
		return Project{}, err
	}
	return s.Project(ctx, name)
}

// validProject checks the project of a grant: a project name, or a
// personal space.
func validProject(name string) error {
	if owner := OwnerOf(name); owner != "" {
		return auth.ValidateName(owner)
	}
	return ValidateName(name)
}
