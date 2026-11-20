// Package store owns cafard's durable state.
//
// SQLite via modernc.org/sqlite: a pure-Go driver, so the binary builds with
// CGO_ENABLED=0 and needs nothing at runtime.
//
// The store records; it does not decide. Whether a member may do something
// is service/access's to answer, and the store's membership reads exist for
// it alone (internal/arch holds the rest of the code to that).
//
// Every write that changes access takes the AuditEntry describing it, and
// writes both in one transaction. A change that is not in the audit log
// cannot be made through this package.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNotFound is a lookup with nothing behind it.
var ErrNotFound = errors.New("not found")

// ErrExists is a create or an add that is already true.
var ErrExists = errors.New("already exists")

type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open connects to the database at path and brings its schema up to date.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// SQLite takes one writer at a time; a small pool avoids lock churn.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to database %s: %w", path, err)
	}
	if err := migrate(context.WithoutCancel(ctx), db); err != nil {
		db.Close()
		return nil, err
	}
	// SQLite creates the file honouring umask, which typically yields 0644.
	// The state directory is 0700; this is the inner guard.
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		db.Close()
		return nil, fmt.Errorf("secure database file: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// dsn is how every connection to the database at path is opened.
//
// The pragmas are claudication's, for the same reasons: WAL so readers do not
// block the writer; busy_timeout so a concurrent write waits instead of
// failing; foreign keys on, which SQLite leaves off by default; and
// _txlock=immediate, so a transaction takes the write lock when it begins,
// where busy_timeout can wait for it, rather than failing with SQLITE_BUSY
// when a read inside it turns into a write.
func dsn(path string) string {
	return "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=temp_store(memory)" +
		"&_txlock=immediate"
}

type step struct {
	version int
	name    string
}

// migrate applies, in order and each in its own transaction, every file in
// migrations/ newer than the version the database records. NNN_name.sql takes
// the database to version NNN. A database newer than this binary is refused:
// running an old release against it would write what the new schema forbids.
func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	var current int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("migrate: read version: %w", err)
	}
	steps, err := migrationSteps()
	if err != nil {
		return err
	}
	latest := steps[len(steps)-1].version
	if current > latest {
		return fmt.Errorf("the database is at schema version %d, newer than this release's %d: run the release that wrote it, or a later one",
			current, latest)
	}
	for _, st := range steps {
		if st.version <= current {
			continue
		}
		body, err := fs.ReadFile(migrationFS, "migrations/"+st.name)
		if err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		if err := applyStep(ctx, db, st, string(body)); err != nil {
			return err
		}
	}
	return nil
}

func applyStep(ctx context.Context, db *sql.DB, st step, body string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate to %d: %w", st.version, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("migrate to %d (%s): %w", st.version, st.name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		st.version, time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("migrate to %d: %w", st.version, err)
	}
	return tx.Commit()
}

func migrationSteps() ([]step, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	var steps []step
	seen := map[int]string{}
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || !strings.HasSuffix(e.Name(), ".sql") {
			return nil, fmt.Errorf("migrate: %s is not NNN_name.sql", e.Name())
		}
		if other, dup := seen[v]; dup {
			return nil, fmt.Errorf("migrate: %s and %s both take the database to version %d", other, e.Name(), v)
		}
		seen[v] = e.Name()
		steps = append(steps, step{version: v, name: e.Name()})
	}
	if len(steps) == 0 {
		return nil, errors.New("migrate: no migrations embedded")
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].version < steps[j].version })
	return steps, nil
}

// AuditEntry is one line of the audit log: "Guillaume added dev-code to
// speedtest", from where.
type AuditEntry struct {
	At     time.Time
	Actor  string
	Action string
	Target string
	Origin string
}

// change runs fn and records a in one transaction: both happen, or neither.
func (s *Store) change(ctx context.Context, a AuditEntry, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if a.At.IsZero() {
		a.At = s.now()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit (at, actor, action, target, origin) VALUES (?, ?, ?, ?, ?)`,
		a.At.UnixMilli(), a.Actor, a.Action, a.Target, a.Origin); err != nil {
		return fmt.Errorf("write audit: %w", err)
	}
	return tx.Commit()
}

// Audit returns the newest entries first, at most limit.
func (s *Store) Audit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at, actor, action, target, origin FROM audit ORDER BY at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var a AuditEntry
		var at int64
		if err := rows.Scan(&at, &a.Actor, &a.Action, &a.Target, &a.Origin); err != nil {
			return nil, err
		}
		a.At = time.UnixMilli(at)
		out = append(out, a)
	}
	return out, rows.Err()
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func fromMillis(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64)
	return &t
}
