package store

import (
	"context"
	"strings"
	"time"
)

// The audit log as the admin UI reads it: a page at a time, newest first,
// narrowed by the values of its columns, as claudication's request log is.
// An entry's kind and source are worked out from what is stored rather than
// stored beside it: the log is written in many places, and a column added
// there would be one more thing each of them could forget.

// auditKind is an entry's kind, from its action: what sort of change it was.
const auditKind = `CASE
	WHEN action LIKE '%signed %' OR action LIKE '%sign-in%' OR action LIKE '%sign in%' OR action LIKE '%code to sign%' OR action LIKE '%password%' THEN 'sign-ins'
	WHEN action LIKE '%link%' OR action LIKE '%token%' THEN 'links'
	WHEN action LIKE '%public URL%' THEN 'settings'
	WHEN action LIKE '%project%' OR action LIKE '%retention%' OR action LIKE 'archived%' OR action LIKE 'unarchived%' THEN 'projects'
	ELSE 'members' END`

// auditSource is where an entry came from: the part of its origin before
// " · " (admin, chat, cli, join, mcp, api), the whole of it without one.
const auditSource = `CASE WHEN instr(origin, ' · ') > 0 THEN substr(origin, 1, instr(origin, ' · ') - 1) ELSE origin END`

// AuditFilter narrows the log: an entry is shown when, for every field with
// values, it has one of them.
type AuditFilter struct {
	Actors, Targets, Kinds, Sources []string
	// Before is the id to read before, for the next page; 0 for the newest.
	Before int64
	Limit  int
}

// AuditRow is one entry with its id, kind and source.
type AuditRow struct {
	ID int64
	AuditEntry
	Kind, Source string
}

// AuditPage reads a page of the log, newest first, and the id to read the
// next one before, or 0 at the end.
func (s *Store) AuditPage(ctx context.Context, f AuditFilter) ([]AuditRow, int64, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	var where []string
	var args []any
	in := func(expr string, values []string) {
		if len(values) == 0 {
			return
		}
		where = append(where, expr+` IN (`+strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")+`)`)
		for _, v := range values {
			args = append(args, v)
		}
	}
	in("actor", f.Actors)
	in("target", f.Targets)
	in(auditKind, f.Kinds)
	in(auditSource, f.Sources)
	if f.Before > 0 {
		where = append(where, "id < ?")
		args = append(args, f.Before)
	}
	q := `SELECT id, at, actor, action, target, origin, ` + auditKind + `, ` + auditSource + ` FROM audit`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	// One more than the page, to know whether there is a next.
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit+1)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		var at int64
		if err := rows.Scan(&r.ID, &at, &r.Actor, &r.Action, &r.Target, &r.Origin, &r.Kind, &r.Source); err != nil {
			return nil, 0, err
		}
		r.At = time.UnixMilli(at)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var next int64
	if len(out) > f.Limit {
		out = out[:f.Limit]
		next = out[len(out)-1].ID
	}
	return out, next, nil
}

// Facet is one value of a column, and how many entries have it.
type Facet struct {
	Value string
	Count int
}

// AuditFacets are the values of the log's filterable columns, the commonest
// first: what a column's menu offers.
func (s *Store) AuditFacets(ctx context.Context) (map[string][]Facet, error) {
	out := map[string][]Facet{}
	for field, expr := range map[string]string{"actor": "actor", "target": "target", "kind": auditKind, "source": auditSource} {
		rows, err := s.db.QueryContext(ctx, `SELECT `+expr+` AS v, COUNT(*) AS n FROM audit GROUP BY v ORDER BY n DESC, v LIMIT 200`)
		if err != nil {
			return nil, err
		}
		list := []Facet{}
		for rows.Next() {
			var f Facet
			if err := rows.Scan(&f.Value, &f.Count); err != nil {
				rows.Close()
				return nil, err
			}
			list = append(list, f)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		out[field] = list
	}
	return out, nil
}
