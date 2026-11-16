package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// ErrNotFound is returned when an entry or stream does not exist.
var ErrNotFound = errors.New("not found")

const entryColumns = `seq, ts, src_ts, stream, kind, severity, host, app, text, attachments, sender, recipient, reply_to, meta`

type scanner interface {
	Scan(dest ...any) error
}

func scanEntry(row scanner) (Entry, error) {
	var (
		e                                    Entry
		ts                                   int64
		srcTS, replyTo                       sql.NullInt64
		host, app, atts, sender, recip, meta sql.NullString
		kind                                 string
	)
	if err := row.Scan(&e.Seq, &ts, &srcTS, &e.Stream, &kind, &e.Severity, &host, &app, &e.Text, &atts, &sender, &recip, &replyTo, &meta); err != nil {
		return Entry{}, err
	}
	e.TS = fromNanos(ts)
	if srcTS.Valid {
		t := fromNanos(srcTS.Int64)
		e.SrcTS = &t
	}
	e.Kind = Kind(kind)
	e.Host, e.App = host.String, app.String
	e.Sender, e.Recipient = sender.String, recip.String
	e.ReplyTo = replyTo.Int64
	if meta.Valid && meta.String != "" {
		e.Meta = json.RawMessage(meta.String)
	}
	if atts.Valid && atts.String != "" {
		if err := json.Unmarshal([]byte(atts.String), &e.Attachments); err != nil {
			return Entry{}, fmt.Errorf("entry %d: attachments: %w", e.Seq, err)
		}
	}
	return e, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// WriteBatch inserts entries in one transaction and returns them with their
// seq. Only the writer calls it: seq order must be commit order.
func (s *Store) WriteBatch(ctx context.Context, entries []Entry) ([]Entry, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out, err := insertEntries(ctx, tx, entries)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func insertEntries(ctx context.Context, tx *sql.Tx, entries []Entry) ([]Entry, error) {
	ensure, err := tx.PrepareContext(ctx, `INSERT INTO streams(id, kind, created_at) VALUES (?, ?, ?) ON CONFLICT(id) DO NOTHING`)
	if err != nil {
		return nil, err
	}
	defer ensure.Close()
	insert, err := tx.PrepareContext(ctx, `INSERT INTO entries(ts, src_ts, stream, kind, severity, host, app, text, attachments, sender, recipient, reply_to, meta)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING seq`)
	if err != nil {
		return nil, err
	}
	defer insert.Close()

	out := make([]Entry, len(entries))
	last := map[string]int64{}
	seen := map[string]bool{}
	for i, e := range entries {
		if !seen[e.Stream] {
			if _, err := ensure.ExecContext(ctx, e.Stream, StreamKind(e.Stream), nanos(e.TS)); err != nil {
				return nil, err
			}
			seen[e.Stream] = true
		}
		var srcTS any
		if e.SrcTS != nil {
			srcTS = nanos(*e.SrcTS)
		}
		var atts any
		if len(e.Attachments) > 0 {
			b, err := json.Marshal(e.Attachments)
			if err != nil {
				return nil, err
			}
			atts = string(b)
		}
		var meta any
		if len(e.Meta) > 0 {
			meta = string(e.Meta)
		}
		if err := insert.QueryRowContext(ctx, nanos(e.TS), srcTS, e.Stream, string(e.Kind), e.Severity,
			nullString(e.Host), nullString(e.App), e.Text, atts,
			nullString(e.Sender), nullString(e.Recipient), nullInt(e.ReplyTo), meta).Scan(&e.Seq); err != nil {
			return nil, err
		}
		out[i] = e
		last[e.Stream] = e.Seq
	}
	for stream, seq := range last {
		if _, err := tx.ExecContext(ctx, `UPDATE streams SET last_seq = ? WHERE id = ?`, seq, stream); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// LastSeq is the highest seq ever assigned, deleted entries included.
func (s *Store) LastSeq(ctx context.Context) (int64, error) {
	var seq sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name = 'entries'`).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return seq.Int64, err
}

// Get returns one entry.
func (s *Store) Get(ctx context.Context, seq int64) (Entry, error) {
	e, err := scanEntry(s.db.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM entries WHERE seq = ?`, seq))
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	return e, err
}

// Stream is one row of the streams table, with its latest entry.
type Stream struct {
	ID        string
	Kind      string
	Labels    map[string]string
	CreatedAt time.Time
	LastSeq   int64
	LastTS    *time.Time
	LastLine  string
}

// Streams lists every stream, message streams included, by id.
func (s *Store) Streams(ctx context.Context) ([]Stream, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id, s.kind, s.labels, s.created_at, s.last_seq, e.ts, e.text
		FROM streams s LEFT JOIN entries e ON e.seq = s.last_seq ORDER BY s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stream
	for rows.Next() {
		var (
			st      Stream
			labels  string
			created int64
			ts      sql.NullInt64
			text    sql.NullString
		)
		if err := rows.Scan(&st.ID, &st.Kind, &labels, &created, &st.LastSeq, &ts, &text); err != nil {
			return nil, err
		}
		st.CreatedAt = fromNanos(created)
		if ts.Valid {
			t := fromNanos(ts.Int64)
			st.LastTS = &t
		}
		st.LastLine = text.String
		if err := json.Unmarshal([]byte(labels), &st.Labels); err != nil {
			st.Labels = nil
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// MatchStreams resolves stream ids and path.Match globs against the
// existing streams. An empty pattern list means every log stream. Message
// streams are only returned when a pattern names them explicitly.
func (s *Store) MatchStreams(ctx context.Context, patterns []string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM streams ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		all = append(all, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return MatchPatterns(all, patterns), nil
}

// MatchPatterns filters ids by patterns with the rule of MatchStreams.
func MatchPatterns(ids, patterns []string) []string {
	var out []string
	for _, id := range ids {
		if StreamMatches(id, patterns) {
			out = append(out, id)
		}
	}
	return out
}

// StreamMatches applies the rule of MatchStreams to one id. A glob never
// matches a message stream unless it starts with that stream's kind prefix.
func StreamMatches(id string, patterns []string) bool {
	msg := IsMessageStream(id)
	if len(patterns) == 0 {
		return !msg
	}
	for _, p := range patterns {
		if p == id {
			return true
		}
		if msg && !(strings.HasPrefix(p, "chan:") || strings.HasPrefix(p, "dm:")) {
			continue
		}
		if ok, _ := path.Match(p, id); ok {
			return true
		}
	}
	return false
}

// ValidatePattern checks a stream id or glob.
func ValidatePattern(p string) error {
	if p == "" {
		return errors.New("empty stream pattern")
	}
	if _, err := path.Match(p, ""); err != nil {
		return fmt.Errorf("stream pattern %q: %w", p, err)
	}
	return nil
}

// Query selects entries. Seq bounds are inclusive.
type Query struct {
	Streams     []string // resolved stream ids; nil means every log stream
	SinceSeq    int64    // first seq to consider (0: from the start)
	UntilSeq    int64    // last seq to consider (0: no bound)
	BeforeSeq   int64    // exclusive upper bound, for paging backwards (0: none)
	Kinds       []Kind   // empty: all
	MinSeverity *int     // keep severity <= *MinSeverity (0 is the most severe); nil keeps all
	Pattern     *regexp.Regexp
	Limit       int
	Desc        bool // newest first, for tails and backward paging
	// ScanLimit caps the rows examined when Pattern filters in Go. Zero
	// means 100000.
	ScanLimit int
}

// Result is a page of entries.
type Result struct {
	Entries []Entry // always in ascending seq order
	HasMore bool
	// NextSeq continues an ascending read: pass it back as SinceSeq.
	NextSeq int64
}

// Read runs q. With Desc it returns the newest Limit matches, still in
// ascending order in Result.Entries.
func (s *Store) Read(ctx context.Context, q Query) (Result, error) {
	if q.Limit <= 0 {
		q.Limit = 100
	}
	if q.ScanLimit <= 0 {
		q.ScanLimit = 100000
	}
	if q.Streams != nil && len(q.Streams) == 0 {
		return Result{NextSeq: q.SinceSeq}, nil
	}
	where, args := q.filters()
	chunk := q.Limit + 1
	if q.Pattern != nil && chunk < 1000 {
		chunk = 1000
	}

	var (
		matched   []Entry
		scanned   int
		exhausted bool
		cursor    = q.SinceSeq // ascending: next seq to examine
		upper     = int64(1 << 62)
	)
	if q.BeforeSeq > 0 {
		upper = q.BeforeSeq
	}
	stmt := `SELECT ` + entryColumns + ` FROM entries WHERE ` + strings.Join(where, " AND ")
	if q.Desc {
		stmt += ` AND seq >= ? AND seq < ? ORDER BY seq DESC LIMIT ?`
	} else {
		stmt += ` AND seq >= ? ORDER BY seq ASC LIMIT ?`
	}

	for len(matched) <= q.Limit && scanned < q.ScanLimit {
		a := append([]any{}, args...)
		if q.Desc {
			a = append(a, q.SinceSeq, upper, chunk)
		} else {
			a = append(a, cursor, chunk)
		}
		rows, err := s.db.QueryContext(ctx, stmt, a...)
		if err != nil {
			return Result{}, err
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
			if q.Desc {
				upper = e.Seq
			} else {
				cursor = e.Seq + 1
			}
			if q.Pattern == nil || q.Pattern.MatchString(e.Text) {
				matched = append(matched, e)
				if len(matched) > q.Limit {
					break
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return Result{}, err
		}
		if n < chunk && len(matched) <= q.Limit {
			exhausted = true
			break
		}
	}

	res := Result{NextSeq: q.SinceSeq}
	switch {
	case len(matched) > q.Limit:
		res.HasMore = true
		matched = matched[:q.Limit]
	case !exhausted:
		res.HasMore = true // scan budget ran out
	}
	if q.Desc {
		for i, j := 0, len(matched)-1; i < j; i, j = i+1, j-1 {
			matched[i], matched[j] = matched[j], matched[i]
		}
		if len(matched) > 0 {
			res.NextSeq = matched[len(matched)-1].Seq + 1
		}
	} else if res.HasMore && len(matched) == q.Limit {
		res.NextSeq = matched[len(matched)-1].Seq + 1
	} else {
		res.NextSeq = max(cursor, q.SinceSeq)
	}
	res.Entries = matched
	return res, nil
}

func (q Query) filters() (where []string, args []any) {
	if q.Streams != nil {
		ph := strings.Repeat("?,", len(q.Streams))
		where = append(where, "stream IN ("+ph[:len(ph)-1]+")")
		for _, id := range q.Streams {
			args = append(args, id)
		}
	} else {
		where = append(where, "stream NOT LIKE 'chan:%' AND stream NOT LIKE 'dm:%'")
	}
	if len(q.Kinds) > 0 {
		ph := strings.Repeat("?,", len(q.Kinds))
		where = append(where, "kind IN ("+ph[:len(ph)-1]+")")
		for _, k := range q.Kinds {
			args = append(args, string(k))
		}
	}
	if q.MinSeverity != nil && *q.MinSeverity < 7 {
		where = append(where, "severity <= ?")
		args = append(args, *q.MinSeverity)
	}
	if q.UntilSeq > 0 {
		where = append(where, "seq <= ?")
		args = append(args, q.UntilSeq)
	}
	return where, args
}

// Around returns the entry at seq and up to before/after entries of the same
// stream around it, in ascending order.
func (s *Store) Around(ctx context.Context, seq int64, before, after int) ([]Entry, error) {
	focus, err := s.Get(ctx, seq)
	if err != nil {
		return nil, err
	}
	prev, err := s.Read(ctx, Query{Streams: []string{focus.Stream}, BeforeSeq: seq, Limit: max(before, 1), Desc: true})
	if err != nil {
		return nil, err
	}
	next, err := s.Read(ctx, Query{Streams: []string{focus.Stream}, SinceSeq: seq + 1, Limit: max(after, 1)})
	if err != nil {
		return nil, err
	}
	var out []Entry
	if before > 0 {
		out = append(out, prev.Entries...)
	}
	out = append(out, focus)
	if after > 0 {
		out = append(out, next.Entries...)
	}
	return out, nil
}
