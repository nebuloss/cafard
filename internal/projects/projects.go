// Package projects holds the projects, their log sinks and the grants
// that say who reaches them, the per-principal Access every read and write
// checks, and the routing of logs into sinks.
package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// Imported is the project an install made before projects moved into,
// and where the old config keys are imported.
const Imported = store.ImportedProject

// Project is one row of projects.
type Project struct {
	Name      string
	Title     string
	CreatedAt time.Time
	Retention store.Limits
}

// Label is the title, or the name when there is none.
func (p Project) Label() string {
	if p.Title != "" {
		return p.Title
	}
	return p.Name
}

// ErrNotFound is an unknown project, sink or grant.
var ErrNotFound = errors.New("not found")

// Service owns the projects, sinks, sink_sources, sink_serial, grants and
// config_imports tables.
type Service struct {
	db       *sql.DB
	clock    clock.Clock
	defaults store.Limits
	log      *slog.Logger

	mu       sync.Mutex
	onCreate []func(ctx context.Context, project string) error
	onSinks  []func()

	routes atomic.Pointer[routes]
	cache  accessCache
}

// New opens the service. Projects whose retention is not set yet (the one
// an old install moved into) take defaults, the config's values.
func New(ctx context.Context, db *sql.DB, c clock.Clock, defaults store.Limits, log *slog.Logger) (*Service, error) {
	s := &Service{db: db, clock: c, defaults: defaults, log: log}
	s.cache.init()
	if _, err := db.ExecContext(ctx, `UPDATE projects SET
		lines_max_age = coalesce(lines_max_age, ?), lines_max_bytes = coalesce(lines_max_bytes, ?),
		messages_max_age = coalesce(messages_max_age, ?), blobs_max_bytes = coalesce(blobs_max_bytes, ?)
		WHERE lines_max_age IS NULL OR lines_max_bytes IS NULL OR messages_max_age IS NULL OR blobs_max_bytes IS NULL`,
		int64(defaults.LinesMaxAge), defaults.LinesMaxBytes, int64(defaults.MessagesMaxAge), defaults.BlobsMaxBytes); err != nil {
		return nil, err
	}
	if err := s.reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// OnCreate runs f in every project made from now on (messaging makes its
// #general). Call it before serving.
func (s *Service) OnCreate(f func(ctx context.Context, project string) error) {
	s.mu.Lock()
	s.onCreate = append(s.onCreate, f)
	s.mu.Unlock()
}

// ValidateName checks a project name: the principal name rule, since the
// name is in stream ids and URLs.
func ValidateName(name string) error {
	if err := auth.ValidateName(name); err != nil {
		return fmt.Errorf("project name %q must be 1 to 40 characters of a-z, 0-9, '.', '_' or '-', starting with a letter or digit", name)
	}
	return nil
}

const maxTitle = 200

// Create makes a project with the config's retention.
func (s *Service) Create(ctx context.Context, name, title string) (Project, error) {
	if err := ValidateName(name); err != nil {
		return Project{}, dto.Errorf(dto.CodeBadRequest, "%v.", err)
	}
	title = strings.TrimSpace(title)
	if len(title) > maxTitle {
		return Project{}, dto.Errorf(dto.CodeBadRequest, "A project title is at most %d bytes.", maxTitle)
	}
	now := s.clock.Now().UTC()
	d := s.defaults
	res, err := s.db.ExecContext(ctx, `INSERT INTO projects(name, title, created_at, lines_max_age, lines_max_bytes, messages_max_age, blobs_max_bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(name) DO NOTHING`,
		name, title, now.UnixNano(), int64(d.LinesMaxAge), d.LinesMaxBytes, int64(d.MessagesMaxAge), d.BlobsMaxBytes)
	if err != nil {
		return Project{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Project{}, dto.Errorf(dto.CodeConflict, "There is a project %s already.", name)
	}
	s.mu.Lock()
	hooks := append([]func(context.Context, string) error(nil), s.onCreate...)
	s.mu.Unlock()
	for _, f := range hooks {
		if err := f(ctx, name); err != nil {
			return Project{}, err
		}
	}
	s.Invalidate()
	return Project{Name: name, Title: title, CreatedAt: now, Retention: d}, nil
}

// Ensure returns a project, making it when missing.
func (s *Service) Ensure(ctx context.Context, name, title string) (Project, error) {
	p, err := s.Project(ctx, name)
	if errors.Is(err, ErrNotFound) {
		p, err = s.Create(ctx, name, title)
		var apiErr *dto.APIError
		if errors.As(err, &apiErr) && apiErr.Code == dto.CodeConflict {
			return s.Project(ctx, name)
		}
	}
	return p, err
}

const projectColumns = `name, title, created_at, coalesce(lines_max_age, 0), coalesce(lines_max_bytes, 0),
	coalesce(messages_max_age, 0), coalesce(blobs_max_bytes, 0)`

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var (
		p                Project
		created          int64
		linesAge, msgAge int64
	)
	if err := row.Scan(&p.Name, &p.Title, &created, &linesAge, &p.Retention.LinesMaxBytes, &msgAge, &p.Retention.BlobsMaxBytes); err != nil {
		return Project{}, err
	}
	p.CreatedAt = time.Unix(0, created).UTC()
	p.Retention.LinesMaxAge, p.Retention.MessagesMaxAge = time.Duration(linesAge), time.Duration(msgAge)
	return p, nil
}

// Projects lists every project by name.
func (s *Service) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectColumns+` FROM projects ORDER BY name`)
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

// Project looks one up.
func (s *Service) Project(ctx context.Context, name string) (Project, error) {
	p, err := scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, fmt.Errorf("%w: project %s", ErrNotFound, name)
	}
	return p, err
}

// SetRetention changes a project's limits; 0 means forever or no limit.
func (s *Service) SetRetention(ctx context.Context, name string, l store.Limits) error {
	if l.LinesMaxAge < 0 || l.LinesMaxBytes < 0 || l.MessagesMaxAge < 0 || l.BlobsMaxBytes < 0 {
		return dto.Errorf(dto.CodeBadRequest, "Retention limits must not be negative; 0 means no limit.")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET lines_max_age = ?, lines_max_bytes = ?, messages_max_age = ?, blobs_max_bytes = ?
		WHERE name = ?`, int64(l.LinesMaxAge), l.LinesMaxBytes, int64(l.MessagesMaxAge), l.BlobsMaxBytes, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: project %s", ErrNotFound, name)
	}
	return nil
}

// Limits are every project's retention, for the janitor.
func (s *Service) Limits(ctx context.Context) (map[string]store.Limits, error) {
	ps, err := s.Projects(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]store.Limits, len(ps))
	for _, p := range ps {
		out[p.Name] = p.Retention
	}
	return out, nil
}
