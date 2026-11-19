package projects

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
)

// GrantScopes are the scopes a grant may hold. ingest only on a whole
// project: a channel holds no logs to write into.
var GrantScopes = auth.NewScopeSet(auth.ScopeIngest, auth.ScopeRead, auth.ScopeMessage, auth.ScopeSerialWrite)

// Grant gives a principal scopes on a project, or on one of its channels.
type Grant struct {
	ID        int64
	Principal string
	Project   string
	Channel   string // "" = the whole project; else the bare channel name
	Scopes    auth.ScopeSet
}

// Target is "project" or "project/channel".
func (g Grant) Target() string {
	if g.Channel == "" {
		return g.Project
	}
	return g.Project + "/" + g.Channel
}

// Spec is the form ParseGrant reads: "lab=read,message", "lab:dut1=read".
func (g Grant) Spec() string {
	target := g.Project
	if g.Channel != "" {
		target += ":" + g.Channel
	}
	return target + "=" + strings.ReplaceAll(g.Scopes.String(), " ", ",")
}

// ParseGrant reads "project[:channel]=scope,scope".
func ParseGrant(spec string) (Grant, error) {
	target, scopes, ok := strings.Cut(strings.TrimSpace(spec), "=")
	if !ok {
		return Grant{}, fmt.Errorf("grant %q is not project[:channel]=scopes, such as lab=read,message", spec)
	}
	var g Grant
	g.Project, g.Channel, _ = strings.Cut(target, ":")
	sc, err := auth.ParseScopes(scopes)
	if err != nil {
		return Grant{}, fmt.Errorf("grant %q: %w", spec, err)
	}
	g.Scopes = sc
	return g, g.validate()
}

// ParseGrants reads several specs separated by spaces or lines.
func ParseGrants(text string) ([]Grant, error) {
	var out []Grant
	for _, f := range strings.Fields(text) {
		g, err := ParseGrant(f)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

// FormatGrants is the stored form of ParseGrants.
func FormatGrants(gs []Grant) string {
	specs := make([]string, len(gs))
	for i, g := range gs {
		specs[i] = g.Spec()
	}
	return strings.Join(specs, " ")
}

func (g Grant) validate() error {
	if err := ValidateName(g.Project); err != nil {
		return err
	}
	if g.Channel != "" && !channelNameOK(g.Channel) {
		return fmt.Errorf("grant on %s: %q is not a channel name", g.Project, g.Channel)
	}
	if len(g.Scopes) == 0 {
		return fmt.Errorf("a grant on %s needs at least one scope", g.Target())
	}
	for _, sc := range g.Scopes {
		if !GrantScopes.Has(sc) {
			return fmt.Errorf("a grant cannot carry %s; it takes read, message, ingest and serial_write", sc)
		}
	}
	if g.Channel != "" && g.Scopes.Has(auth.ScopeIngest) {
		return fmt.Errorf("ingest is granted on a whole project, not on the channel %s", g.Target())
	}
	return nil
}

func channelNameOK(name string) bool {
	if name == "" || len(name) > 40 {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// Grants lists grants, filtered by project and principal ("" for any).
func (s *Service) Grants(ctx context.Context, project, principal string) ([]Grant, error) {
	return queryGrants(ctx, s.db, `WHERE (? = '' OR project = ?) AND (? = '' OR principal = ?)`, project, project, principal, principal)
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func queryGrants(ctx context.Context, q querier, where string, args ...any) ([]Grant, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, principal, project, coalesce(channel, ''), scopes FROM grants `+where+
		` ORDER BY project, principal, coalesce(channel, '')`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var (
			g      Grant
			scopes string
		)
		if err := rows.Scan(&g.ID, &g.Principal, &g.Project, &g.Channel, &scopes); err != nil {
			return nil, err
		}
		g.Scopes, _ = auth.ParseScopes(scopes)
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetGrant gives exactly g's scopes on its target, replacing what the
// principal had there.
func (s *Service) SetGrant(ctx context.Context, g Grant) error {
	return s.writeGrants(ctx, false, g)
}

// AddGrants adds scopes to what the principal has on each target.
func (s *Service) AddGrants(ctx context.Context, gs ...Grant) error {
	return s.writeGrants(ctx, true, gs...)
}

func (s *Service) writeGrants(ctx context.Context, merge bool, gs ...Grant) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, g := range gs {
		if err := WriteGrant(ctx, tx, g, merge); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.Invalidate()
	return nil
}

// WriteGrant stores one grant inside tx, which pairing redemption shares
// with the session it opens. merge adds to the scopes already there.
func WriteGrant(ctx context.Context, tx *sql.Tx, g Grant, merge bool) error {
	if err := g.validate(); err != nil {
		return dto.Errorf(dto.CodeBadRequest, "%v.", err)
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE name = ?`, g.Project).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return dto.Errorf(dto.CodeNotFound, "There is no project %s.", g.Project)
	}
	if g.Channel != "" {
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM channels WHERE name = ?`, g.Target()).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return dto.Errorf(dto.CodeNotFound, "Project %s has no channel %s.", g.Project, g.Channel)
		}
	}
	existing, err := queryGrants(ctx, tx, `WHERE principal = ? AND project = ? AND coalesce(channel, '') = ?`, g.Principal, g.Project, g.Channel)
	if err != nil {
		return err
	}
	scopes := g.Scopes
	if merge && len(existing) > 0 {
		scopes = auth.NewScopeSet(append(append([]auth.Scope{}, existing[0].Scopes...), g.Scopes...)...)
	}
	if len(existing) > 0 {
		_, err = tx.ExecContext(ctx, `UPDATE grants SET scopes = ? WHERE id = ?`, scopes.String(), existing[0].ID)
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO grants(principal, project, channel, scopes) VALUES (?, ?, ?, ?)`,
		g.Principal, g.Project, nullText(g.Channel), scopes.String())
	return err
}

// DeleteGrant removes a grant of project.
func (s *Service) DeleteGrant(ctx context.Context, project string, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM grants WHERE id = ? AND project = ?`, id, project)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: grant %d", ErrNotFound, id)
	}
	s.Invalidate()
	return nil
}
