package store

import (
	"context"
	"database/sql/driver"
	"strings"
	"time"

	"modernc.org/sqlite"

	"github.com/nebuloss/cafard/internal/ansi"
)

// ftsTextFunc is the SQL function the FTS triggers index text through
// (migration 0050): the text without ANSI escapes or control characters.
// It is registered on the driver, so every connection of every process
// that opens the store has it, the CLI subcommands included.
const ftsTextFunc = "cafard_fts_text"

func init() {
	sqlite.MustRegisterDeterministicScalarFunction(ftsTextFunc, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		switch v := args[0].(type) {
		case string:
			return ansi.Strip(v), nil
		case []byte:
			return ansi.Strip(string(v)), nil
		case nil:
			return nil, nil
		default:
			return v, nil
		}
	})
}

// QueryError is an FTS5 query SQLite refused; Reason is its words.
type QueryError struct{ Reason string }

func (e *QueryError) Error() string { return "full-text query: " + e.Reason }

// SearchQuery selects entries whose text matches an FTS5 query.
type SearchQuery struct {
	Match    string   // FTS5 query
	Streams  []string // resolved stream ids; nil means every log stream
	Kinds    []Kind   // empty: all
	From, To time.Time
	SinceSeq int64
	Limit    int
	// Keep drops entries after the query (message visibility); nil keeps
	// all.
	Keep func(Entry) bool
}

// Search runs q in seq order. Result.NextSeq continues after the last
// entry examined.
func (s *Store) Search(ctx context.Context, q SearchQuery) (Result, error) {
	if q.Limit <= 0 {
		q.Limit = 100
	}
	if q.Streams != nil && len(q.Streams) == 0 {
		return Result{NextSeq: q.SinceSeq}, nil
	}
	where, args := Query{Streams: q.Streams, Kinds: q.Kinds}.filters()
	if !q.From.IsZero() {
		where = append(where, "e.ts >= ?")
		args = append(args, nanos(q.From))
	}
	if !q.To.IsZero() {
		where = append(where, "e.ts <= ?")
		args = append(args, nanos(q.To))
	}
	cols := "e." + strings.ReplaceAll(entryColumns, ", ", ", e.")
	// FTS5 walks its index in rowid order and stops at the limit, so a
	// common word does not materialise every match.
	stmt := `SELECT ` + cols + ` FROM entries_fts JOIN entries e ON e.seq = entries_fts.rowid
		WHERE entries_fts MATCH ? AND entries_fts.rowid >= ? AND ` + strings.Join(where, " AND ") + `
		ORDER BY entries_fts.rowid LIMIT ?`
	chunk := q.Limit + 1
	if q.Keep != nil {
		chunk = max(chunk, 500)
	}
	var (
		matched []Entry
		cursor  = q.SinceSeq
		more    bool
	)
	for scanned := 0; len(matched) <= q.Limit && scanned < 100000; {
		a := append([]any{q.Match, cursor}, args...)
		a = append(a, chunk)
		rows, err := s.db.QueryContext(ctx, stmt, a...)
		if err != nil {
			return Result{}, queryError(err)
		}
		n := 0
		for rows.Next() {
			e, err := scanEntry(rows)
			if err != nil {
				rows.Close()
				return Result{}, err
			}
			n++
			scanned++
			cursor = e.Seq + 1
			if q.Keep == nil || q.Keep(e) {
				matched = append(matched, e)
				if len(matched) > q.Limit {
					break
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return Result{}, queryError(err)
		}
		if n < chunk {
			break
		}
		more = scanned >= 100000
	}
	res := Result{NextSeq: max(cursor, q.SinceSeq), HasMore: more}
	if len(matched) > q.Limit {
		matched = matched[:q.Limit]
		res.HasMore = true
		res.NextSeq = matched[len(matched)-1].Seq + 1
	}
	res.Entries = matched
	return res, nil
}

// queryError turns SQLite's refusal of an FTS5 expression into a
// QueryError; other errors pass through.
func queryError(err error) error {
	msg := err.Error()
	if i := strings.Index(msg, "fts5: "); i >= 0 {
		reason := strings.TrimSuffix(msg[i+len("fts5: "):], " (1)")
		return &QueryError{Reason: reason}
	}
	if strings.Contains(msg, "unterminated string") || strings.Contains(msg, "malformed MATCH") {
		return &QueryError{Reason: msg}
	}
	return err
}
