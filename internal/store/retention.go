package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"time"

	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/units"
)

// Retention is the `retention:` config section.
type Retention struct {
	LinesMaxAge    time.Duration     `yaml:"lines_max_age"`
	LinesMaxBytes  units.ByteSize    `yaml:"lines_max_bytes"`
	MessagesMaxAge time.Duration     `yaml:"messages_max_age"`
	BlobsMaxBytes  units.ByteSize    `yaml:"blobs_max_bytes"`
	Streams        []StreamRetention `yaml:"streams,omitempty"`
}

// StreamRetention overrides the line age for streams matching Pattern.
// The first matching override wins.
type StreamRetention struct {
	Pattern     string        `yaml:"pattern"`
	LinesMaxAge time.Duration `yaml:"lines_max_age"`
}

// SetDefaults implements config.Defaulter.
func (r *Retention) SetDefaults() {
	r.LinesMaxAge = 14 * 24 * time.Hour
	r.LinesMaxBytes = 2e9
	r.MessagesMaxAge = 90 * 24 * time.Hour
	r.BlobsMaxBytes = 5e9
}

// Validate implements config.Validator.
func (r *Retention) Validate() error {
	if r.LinesMaxAge < 0 || r.MessagesMaxAge < 0 || r.LinesMaxBytes < 0 || r.BlobsMaxBytes < 0 {
		return errors.New("limits must not be negative (0 means no limit)")
	}
	for i, s := range r.Streams {
		if err := ValidatePattern(s.Pattern); err != nil {
			return fmt.Errorf("streams[%d]: %w", i, err)
		}
		if s.LinesMaxAge < 0 {
			return fmt.Errorf("streams[%d]: lines_max_age must not be negative", i)
		}
	}
	return nil
}

// Limits are the four retention limits of a project, or of everything
// outside projects; 0 means none.
type Limits struct {
	LinesMaxAge    time.Duration
	LinesMaxBytes  int64
	MessagesMaxAge time.Duration
	BlobsMaxBytes  int64
}

// Limits are the config's values: the defaults of a new project and the
// limits of what is outside projects.
func (r *Retention) Limits() Limits {
	return Limits{LinesMaxAge: r.LinesMaxAge, LinesMaxBytes: int64(r.LinesMaxBytes),
		MessagesMaxAge: r.MessagesMaxAge, BlobsMaxBytes: int64(r.BlobsMaxBytes)}
}

// maxAge is the age limit for one stream under lim; 0 means none. A
// retention.streams override wins over the project's line age.
func (r *Retention) maxAge(stream string, lim Limits) time.Duration {
	if IsMessageStream(stream) {
		return lim.MessagesMaxAge
	}
	for _, s := range r.Streams {
		if ok, _ := path.Match(s.Pattern, stream); ok || s.Pattern == stream {
			return s.LinesMaxAge
		}
	}
	return lim.LinesMaxAge
}

// ProjectLimits returns the limits of every project, by name.
type ProjectLimits func(ctx context.Context) (map[string]Limits, error)

// Exec runs fn inside the writer, in a transaction of its own.
type Exec func(ctx context.Context, fn func(ctx context.Context, tx *sql.Tx) error) error

// Task is extra janitor work another package registers (acks, blobs,
// claims, sessions).
type Task struct {
	Name string
	Run  func(ctx context.Context) error
}

// Janitor applies retention every Every.
type Janitor struct {
	store    *Store
	exec     Exec
	ret      Retention
	projects ProjectLimits
	clock    clock.Clock
	log      *slog.Logger
	tasks    []Task

	Every time.Duration
	Chunk int
}

// NewJanitor builds a janitor that deletes through exec.
func NewJanitor(st *Store, exec Exec, ret Retention, c clock.Clock, log *slog.Logger) *Janitor {
	return &Janitor{store: st, exec: exec, ret: ret, clock: c, log: log, Every: 10 * time.Minute, Chunk: 5000}
}

// SetProjects gives the janitor each project's limits; without it every
// stream follows the config's.
func (j *Janitor) SetProjects(f ProjectLimits) { j.projects = f }

// Add registers a task that runs after the entry passes.
func (j *Janitor) Add(t Task) { j.tasks = append(j.tasks, t) }

// Run passes every Every until ctx ends.
func (j *Janitor) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-j.clock.After(j.Every):
		}
		if err := j.Pass(ctx); err != nil && ctx.Err() == nil {
			j.log.Error("retention pass failed", "err", err)
		}
	}
}

// PassResult counts what a pass deleted.
type PassResult struct {
	ByAge, BySize int64
}

// Pass deletes by age, then by size, then runs the registered tasks.
func (j *Janitor) Pass(ctx context.Context) error {
	res, err := j.PassEntries(ctx)
	if err != nil {
		return err
	}
	if res.ByAge+res.BySize > 0 {
		j.log.Info("retention", "deleted_by_age", res.ByAge, "deleted_by_size", res.BySize)
	}
	for _, t := range j.tasks {
		if err := t.Run(ctx); err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
	}
	return nil
}

// PassEntries applies the entry limits only.
func (j *Janitor) PassEntries(ctx context.Context) (PassResult, error) {
	var res PassResult
	streams, err := j.store.Streams(ctx)
	if err != nil {
		return res, err
	}
	projects := map[string]Limits{}
	if j.projects != nil {
		if projects, err = j.projects(ctx); err != nil {
			return res, err
		}
	}
	outside := j.ret.Limits()
	// Log streams by the size limit that applies to them: their project's,
	// or the config's for what is outside projects.
	bySize := map[string][]string{}
	now := j.clock.Now()
	for _, s := range streams {
		lim, ok := projects[ProjectOf(s.ID)]
		if !ok {
			lim = outside
		}
		if !IsMessageStream(s.ID) {
			key := ""
			if ok {
				key = ProjectOf(s.ID)
			}
			bySize[key] = append(bySize[key], s.ID)
		}
		age := j.ret.maxAge(s.ID, lim)
		if age <= 0 {
			continue
		}
		n, err := j.deleteChunks(ctx, `DELETE FROM entries WHERE seq IN
			(SELECT seq FROM entries WHERE stream = ? AND ts < ? ORDER BY seq LIMIT ?)`, s.ID, nanos(now.Add(-age)))
		res.ByAge += n
		if err != nil {
			return res, err
		}
	}
	for key, ids := range bySize {
		limit := outside.LinesMaxBytes
		if key != "" {
			limit = projects[key].LinesMaxBytes
		}
		if limit <= 0 {
			continue
		}
		n, err := j.trimLines(ctx, ids, limit)
		res.BySize += n
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

// entryOverhead approximates a row's bytes beyond its text.
const entryOverhead = 64

// deviceWhere keeps the log streams, leaving messages out.
const deviceWhere = `stream NOT LIKE 'chan:%' AND stream NOT LIKE 'dm:%'`

// inStreams keeps the entries of the streams in the JSON array bound
// first, so a long list is one parameter.
const inStreams = `stream IN (SELECT value FROM json_each(?))`

// trimLines deletes the oldest entries of the log streams ids until their
// size is under limit.
func (j *Janitor) trimLines(ctx context.Context, ids []string, limit int64) (int64, error) {
	list, err := json.Marshal(ids)
	if err != nil {
		return 0, err
	}
	var total int64
	if err := j.store.db.QueryRowContext(ctx, `SELECT coalesce(sum(length(CAST(text AS BLOB)) + ?), 0) FROM entries WHERE `+inStreams,
		entryOverhead, string(list)).Scan(&total); err != nil {
		return 0, err
	}
	excess := total - limit
	if excess <= 0 {
		return 0, nil
	}
	rows, err := j.store.db.QueryContext(ctx, `SELECT seq, length(CAST(text AS BLOB)) + ? FROM entries WHERE `+inStreams+` ORDER BY seq`, entryOverhead, string(list))
	if err != nil {
		return 0, err
	}
	var cutoff, acc int64
	for rows.Next() {
		var seq, size int64
		if err := rows.Scan(&seq, &size); err != nil {
			rows.Close()
			return 0, err
		}
		acc += size
		cutoff = seq
		if acc >= excess {
			break
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return j.deleteChunks(ctx, `DELETE FROM entries WHERE seq IN
		(SELECT seq FROM entries WHERE `+inStreams+` AND seq <= ? ORDER BY seq LIMIT ?)`, string(list), cutoff)
}

// deleteChunks repeats a DELETE ending in "LIMIT ?" until it deletes
// nothing, one writer transaction per chunk so ingest keeps flowing.
func (j *Janitor) deleteChunks(ctx context.Context, stmt string, args ...any) (int64, error) {
	var total int64
	for {
		var n int64
		err := j.exec(ctx, func(ctx context.Context, tx *sql.Tx) error {
			r, err := tx.ExecContext(ctx, stmt, append(append([]any{}, args...), j.Chunk)...)
			if err != nil {
				return err
			}
			n, err = r.RowsAffected()
			return err
		})
		total += n
		if err != nil || n == 0 {
			return total, err
		}
	}
}
