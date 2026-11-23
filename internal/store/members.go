package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"cafard/internal/live"
)

// Kind is what an identity is. It is also its access level: a person and an
// agent read and write in their projects, a device only sends to them.
type Kind string

const (
	Person Kind = "person"
	Agent  Kind = "agent"
	Device Kind = "device"
)

// Identity is a person, an agent or a device.
type Identity struct {
	ID          int64
	Name        string
	Kind        Kind
	DisplayName string
	Description string
	Admin       bool
	Enabled     bool
	CreatedAt   time.Time
	DeletedAt   *time.Time
}

// Deleted is an identity removed from cafard; its row stays for its history.
func (i Identity) Deleted() bool { return i.DeletedAt != nil }

// Project is who sees what.
type Project struct {
	ID            int64
	Name          string
	RetentionDays int
	QuotaBytes    int64
	ArchivedAt    *time.Time
	CreatedAt     time.Time
}

func (p Project) Archived() bool { return p.ArchivedAt != nil }

const identityCols = `id, name, kind, display_name, description, admin, enabled, created_at, deleted_at`

type scanner interface{ Scan(dest ...any) error }

func scanIdentity(r scanner) (Identity, error) {
	var i Identity
	var created int64
	var deleted sql.NullInt64
	err := r.Scan(&i.ID, &i.Name, &i.Kind, &i.DisplayName, &i.Description, &i.Admin, &i.Enabled, &created, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrNotFound
	}
	if err != nil {
		return Identity{}, err
	}
	i.CreatedAt = time.UnixMilli(created)
	i.DeletedAt = fromMillis(deleted)
	return i, nil
}

const projectCols = `id, name, retention_days, quota_bytes, archived_at, created_at`

func scanProject(r scanner) (Project, error) {
	var p Project
	var created int64
	var archived sql.NullInt64
	err := r.Scan(&p.ID, &p.Name, &p.RetentionDays, &p.QuotaBytes, &archived, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	p.CreatedAt = time.UnixMilli(created)
	p.ArchivedAt = fromMillis(archived)
	return p, nil
}

// CreateIdentity adds i. Its name must be free, deleted identities included.
func (s *Store) CreateIdentity(ctx context.Context, i Identity, a AuditEntry) (Identity, error) {
	i.CreatedAt = s.now()
	i.Enabled = true
	err := s.change(ctx, a, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO identities (name, kind, display_name, description, admin, enabled, created_at)
			VALUES (?, ?, ?, ?, ?, 1, ?)`, i.Name, i.Kind, i.DisplayName, i.Description, i.Admin, i.CreatedAt.UnixMilli())
		if isUnique(err) {
			return ErrExists
		}
		if err != nil {
			return fmt.Errorf("create %s %s: %w", i.Kind, i.Name, err)
		}
		i.ID, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return Identity{}, err
	}
	return i, nil
}

func (s *Store) Identity(ctx context.Context, id int64) (Identity, error) {
	return scanIdentity(s.db.QueryRowContext(ctx, `SELECT `+identityCols+` FROM identities WHERE id = ?`, id))
}

func (s *Store) IdentityByName(ctx context.Context, name string) (Identity, error) {
	return scanIdentity(s.db.QueryRowContext(ctx, `SELECT `+identityCols+` FROM identities WHERE name = ?`, name))
}

// Identities lists every identity, deleted ones included, by name.
func (s *Store) Identities(ctx context.Context) ([]Identity, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+identityCols+` FROM identities ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Identity
	for rows.Next() {
		i, err := scanIdentity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// SetEnabled pauses or resumes an identity.
func (s *Store) SetEnabled(ctx context.Context, id int64, enabled bool, a AuditEntry) error {
	err := s.change(ctx, a, func(tx *sql.Tx) error {
		return one(tx.ExecContext(ctx, `UPDATE identities SET enabled = ? WHERE id = ? AND deleted_at IS NULL`, enabled, id))
	})
	if err == nil {
		s.publish(live.MemberKey(id))
	}
	return err
}

// SetAdmin grants or withdraws admin from a person.
func (s *Store) SetAdmin(ctx context.Context, id int64, admin bool, a AuditEntry) error {
	err := s.change(ctx, a, func(tx *sql.Tx) error {
		return one(tx.ExecContext(ctx, `UPDATE identities SET admin = ? WHERE id = ? AND kind = 'person' AND deleted_at IS NULL`, admin, id))
	})
	if err == nil {
		s.publish(live.MemberKey(id))
	}
	return err
}

// SetDescription changes what an agent's machine is said to do.
func (s *Store) SetDescription(ctx context.Context, id int64, description string, a AuditEntry) error {
	return s.change(ctx, a, func(tx *sql.Tx) error {
		return one(tx.ExecContext(ctx, `UPDATE identities SET description = ? WHERE id = ? AND deleted_at IS NULL`, description, id))
	})
}

// DeleteIdentity removes an identity from cafard. The row stays, marked
// deleted and disabled, so its history keeps its author. Everything that let
// it in goes: memberships, token, password, sign-ins, and any link still
// waiting.
func (s *Store) DeleteIdentity(ctx context.Context, id int64, a AuditEntry) error {
	now := s.now().UnixMilli()
	err := s.change(ctx, a, func(tx *sql.Tx) error {
		if err := one(tx.ExecContext(ctx, `UPDATE identities SET deleted_at = ?, enabled = 0, admin = 0 WHERE id = ? AND deleted_at IS NULL`,
			now, id)); err != nil {
			return err
		}
		for _, q := range []string{
			`DELETE FROM memberships WHERE identity_id = ?`,
			`DELETE FROM tokens WHERE identity_id = ?`,
			`DELETE FROM passwords WHERE identity_id = ?`,
			`DELETE FROM signins WHERE identity_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE links SET cancelled_at = ? WHERE identity_id = ? AND used_at IS NULL AND cancelled_at IS NULL`, now, id)
		return err
	})
	if err == nil {
		s.publish(live.MemberKey(id))
	}
	return err
}

// CreateProject adds p. Its name must be free.
func (s *Store) CreateProject(ctx context.Context, p Project, a AuditEntry) (Project, error) {
	p.CreatedAt = s.now()
	p.ArchivedAt = nil
	err := s.change(ctx, a, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO projects (name, retention_days, quota_bytes, created_at) VALUES (?, ?, ?, ?)`,
			p.Name, p.RetentionDays, p.QuotaBytes, p.CreatedAt.UnixMilli())
		if isUnique(err) {
			return ErrExists
		}
		if err != nil {
			return fmt.Errorf("create project %s: %w", p.Name, err)
		}
		p.ID, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

func (s *Store) Project(ctx context.Context, id int64) (Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE id = ?`, id))
}

func (s *Store) ProjectByName(ctx context.Context, name string) (Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE name = ?`, name))
}

// Projects lists every project by name.
func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	return s.projects(ctx, `SELECT `+projectCols+` FROM projects ORDER BY name`)
}

// ProjectsOf lists the projects an identity is a member of, by name.
func (s *Store) ProjectsOf(ctx context.Context, identityID int64) ([]Project, error) {
	return s.projects(ctx, `SELECT `+prefixed("p", projectCols)+` FROM projects p
		JOIN memberships m ON m.project_id = p.id WHERE m.identity_id = ? ORDER BY p.name`, identityID)
}

func (s *Store) projects(ctx context.Context, q string, args ...any) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// prefixed qualifies every column of a list with a table alias, for the
// queries that join.
func prefixed(alias, cols string) string {
	parts := strings.Split(cols, ", ")
	for i, c := range parts {
		parts[i] = alias + "." + c
	}
	return strings.Join(parts, ", ")
}

// SetArchived archives a project, or brings it back.
func (s *Store) SetArchived(ctx context.Context, id int64, archived bool, a AuditEntry) error {
	var at any
	if archived {
		at = s.now().UnixMilli()
	}
	err := s.change(ctx, a, func(tx *sql.Tx) error {
		return one(tx.ExecContext(ctx, `UPDATE projects SET archived_at = ? WHERE id = ?`, at, id))
	})
	if err == nil {
		s.publish(live.ProjectKey(id))
	}
	return err
}

// DeleteProject removes a project and every membership of it.
func (s *Store) DeleteProject(ctx context.Context, id int64, a AuditEntry) error {
	// Its members are told too: the project leaves their lists.
	members, _ := s.Members(ctx, id)
	err := s.change(ctx, a, func(tx *sql.Tx) error {
		return one(tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id))
	})
	if err == nil {
		keys := []string{live.ProjectKey(id)}
		for _, m := range members {
			keys = append(keys, live.MemberKey(m.ID))
		}
		s.publish(keys...)
	}
	return err
}

// AddMembership puts an identity in a project. addedBy is the person who did
// it, recorded beside the membership as well as in the audit log.
func (s *Store) AddMembership(ctx context.Context, projectID, identityID, addedBy int64, a AuditEntry) error {
	err := s.change(ctx, a, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO memberships (project_id, identity_id, added_by, added_at) VALUES (?, ?, ?, ?)`,
			projectID, identityID, addedBy, s.now().UnixMilli())
		if isUnique(err) {
			return ErrExists
		}
		return err
	})
	if err == nil {
		s.publish(live.MemberKey(identityID), live.ProjectKey(projectID))
	}
	return err
}

// RemoveMembership takes an identity out of a project.
func (s *Store) RemoveMembership(ctx context.Context, projectID, identityID int64, a AuditEntry) error {
	err := s.change(ctx, a, func(tx *sql.Tx) error {
		return one(tx.ExecContext(ctx, `DELETE FROM memberships WHERE project_id = ? AND identity_id = ?`, projectID, identityID))
	})
	if err == nil {
		s.publish(live.MemberKey(identityID), live.ProjectKey(projectID))
	}
	return err
}

// IsMember reports whether an identity is in a project. For service/access.
func (s *Store) IsMember(ctx context.Context, projectID, identityID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memberships WHERE project_id = ? AND identity_id = ?`,
		projectID, identityID).Scan(&n)
	return n > 0, err
}

// ShareProject reports whether two identities are in at least one project
// together. For service/access.
func (s *Store) ShareProject(ctx context.Context, a, b int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memberships x JOIN memberships y
		ON x.project_id = y.project_id WHERE x.identity_id = ? AND y.identity_id = ?`, a, b).Scan(&n)
	return n > 0, err
}

// Members lists the identities in a project, by name.
func (s *Store) Members(ctx context.Context, projectID int64) ([]Identity, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+prefixed("i", identityCols)+` FROM identities i
		JOIN memberships m ON m.identity_id = i.id WHERE m.project_id = ? ORDER BY i.name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Identity
	for rows.Next() {
		i, err := scanIdentity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// one checks that a write touched exactly the row it was aimed at, and turns
// a write that matched nothing into ErrNotFound.
func one(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
