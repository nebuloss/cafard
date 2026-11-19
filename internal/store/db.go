package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is the database handle.
type Store struct {
	db   *sql.DB
	path string
}

// FileName is the database file inside the data directory.
const FileName = "cafard.db"

// EnsureDataDir creates the data directory with mode 0700, and tightens an
// existing one: it holds messages and the photos people dropped.
func EnsureDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// Open opens (creating if needed) the database at path, applies the
// migrations, and makes the file mode 0600.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("database path %q must not contain ? or #", path)
	}
	// busy_timeout covers the short transactions other tables run beside the
	// writer; _txlock=immediate makes them take the write lock up front
	// instead of failing on upgrade.
	dsn := "file:" + path +
		"?_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(10000)" +
		"&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.tightenModes(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// tightenModes sets 0600 on the database and its WAL files. SQLite creates
// them with the process umask.
func (s *Store) tightenModes() error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		err := os.Chmod(s.path+suffix, 0o600)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// DB is the handle for packages that own other tables. Never write entries
// or entries_fts through it: only the writer does.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }

// goMigrations are data migrations SQL cannot express well, run once each
// in name order among the SQL files.
var goMigrations = map[string]func(ctx context.Context, tx *sql.Tx) error{
	"0071_projects_import": importIntoProjects,
}

// migrate applies every migration not recorded yet, SQL files and Go
// steps, in name order, each in its own transaction.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	for name := range goMigrations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, base := range names {
		var done int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE name = ?`, base).Scan(&done); err != nil {
			return err
		}
		if done > 0 {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if step, ok := goMigrations[base]; ok {
			err = step(ctx, tx)
		} else {
			var body []byte
			if body, err = migrations.ReadFile("migrations/" + base); err == nil {
				_, err = tx.ExecContext(ctx, string(body))
			}
		}
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", base, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(name, applied_at) VALUES (?, ?)`, base, time.Now().UnixNano()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %s: %w", base, err)
		}
	}
	return nil
}

func nanos(t time.Time) int64 { return t.UTC().UnixNano() }

func fromNanos(n int64) time.Time { return time.Unix(0, n).UTC() }
