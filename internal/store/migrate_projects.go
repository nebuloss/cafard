package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// ImportedProject holds what an install had before projects: its channels,
// its log streams, a sink per stream name, and a grant per principal.
const ImportedProject = "imported"

// sinkNameRE is auth.ValidateName's rule as it stood when this migration
// was written; a migration must not change when that rule does.
var sinkNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

// grantable are the scopes a grant may hold, as they stood then.
var grantable = []string{"ingest", "read", "message", "serial_write"}

// importIntoProjects moves the channels and log streams of an install made
// before projects into the project "imported", so no history is lost and
// every principal keeps what it could reach. A new database has nothing to
// move and gets no project.
func importIntoProjects(ctx context.Context, tx *sql.Tx) error {
	streams, err := QueryColumn[string](ctx, tx, `SELECT id FROM streams WHERE id NOT LIKE 'dm:%' AND instr(id, '/') = 0`)
	if err != nil {
		return err
	}
	channels, err := QueryColumn[string](ctx, tx, `SELECT name FROM channels WHERE instr(name, '/') = 0`)
	if err != nil {
		return err
	}
	if len(streams) == 0 && len(channels) == 0 {
		return nil
	}
	now := time.Now().UnixNano()
	// Rows move from one key to another; checking the references at commit
	// lets parents and children move in either order.
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO projects(name, title, created_at) VALUES (?, 'Imported', ?)
		ON CONFLICT(name) DO NOTHING`, ImportedProject, now); err != nil {
		return err
	}
	for _, old := range streams {
		id := importedID(old)
		for _, stmt := range []string{
			`INSERT INTO streams(id, kind, labels, created_at, last_seq, retention)
				SELECT ?2, kind, labels, created_at, last_seq, retention FROM streams WHERE id = ?1 ON CONFLICT(id) DO NOTHING`,
			`UPDATE entries SET stream = ?2 WHERE stream = ?1`,
			`DELETE FROM streams WHERE id = ?1`,
			`UPDATE claims SET resource = ?2 WHERE resource = ?1`,
		} {
			if _, err := tx.ExecContext(ctx, stmt, old, id); err != nil {
				return err
			}
		}
		if IsMessageStream(old) {
			continue
		}
		if err := importSink(ctx, tx, old, now); err != nil {
			return err
		}
	}
	if err := importChannels(ctx, tx); err != nil {
		return err
	}
	if err := importAttachments(ctx, tx); err != nil {
		return err
	}
	return importGrants(ctx, tx)
}

// importedID is an old stream id in the project: serial:dut1 becomes
// serial:imported/dut1.
func importedID(id string) string {
	kind, name, _ := strings.Cut(id, ":")
	return kind + ":" + ImportedProject + "/" + name
}

// importedPattern moves a binding into the project. A pattern without a
// kind matched no stream before but "*", which matched every one.
func importedPattern(p string) string {
	if strings.Contains(p, ":") {
		if kind, name, _ := strings.Cut(p, ":"); !strings.Contains(name, "/") {
			return kind + ":" + ImportedProject + "/" + name
		}
		return p
	}
	if p == "*" {
		return "*:" + ImportedProject + "/*"
	}
	return p
}

// importSink makes the sink an old stream's name stands for, with the
// name as its source address when it is one: syslog and netconsole named
// their streams by IP when nothing else named them.
func importSink(ctx context.Context, tx *sql.Tx, old string, now int64) error {
	kind, name, _ := strings.Cut(old, ":")
	if !sinkNameRE.MatchString(name) {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sinks(project, name, created_at) VALUES (?, ?, ?)
		ON CONFLICT(project, name) DO NOTHING`, ImportedProject, name, now); err != nil {
		return err
	}
	ip, err := netip.ParseAddr(name)
	if err != nil || (kind != "syslog" && kind != "netconsole") {
		return nil
	}
	prefix := netip.PrefixFrom(ip, ip.BitLen()).String()
	_, err = tx.ExecContext(ctx, `INSERT INTO sink_sources(sink, prefix)
		SELECT id, ? FROM sinks WHERE project = ? AND name = ? ON CONFLICT DO NOTHING`, prefix, ImportedProject, name)
	return err
}

func importChannels(ctx context.Context, tx *sql.Tx) error {
	type binding struct{ channel, pattern string }
	rows, err := tx.QueryContext(ctx, `SELECT channel, pattern FROM channel_streams WHERE instr(channel, '/') = 0`)
	if err != nil {
		return err
	}
	var bindings []binding
	for rows.Next() {
		var b binding
		if err := rows.Scan(&b.channel, &b.pattern); err != nil {
			rows.Close()
			return err
		}
		bindings = append(bindings, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	prefix := ImportedProject + "/"
	for _, stmt := range []string{
		`DELETE FROM channel_streams WHERE instr(channel, '/') = 0`,
		`UPDATE channels SET name = ? || name WHERE instr(name, '/') = 0`,
		`UPDATE follows SET chat = 'chan:' || ? || substr(chat, 6) WHERE chat LIKE 'chan:%' AND instr(chat, '/') = 0`,
	} {
		args := []any{prefix}
		if strings.HasPrefix(stmt, "DELETE") {
			args = nil
		}
		if _, err := tx.ExecContext(ctx, stmt, args...); err != nil {
			return err
		}
	}
	for _, b := range bindings {
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_streams(channel, pattern) VALUES (?, ?) ON CONFLICT DO NOTHING`,
			prefix+b.channel, importedPattern(b.pattern)); err != nil {
			return err
		}
	}
	return nil
}

// importAttachments rewrites the streams and channel of log attachments,
// so a message keeps pointing at the lines it was sent with.
func importAttachments(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT seq, attachments FROM entries
		WHERE kind = 'message' AND attachments LIKE '%"type":"log"%'`)
	if err != nil {
		return err
	}
	type fix struct {
		seq  int64
		atts string
	}
	var fixes []fix
	for rows.Next() {
		var (
			seq int64
			raw string
		)
		if err := rows.Scan(&seq, &raw); err != nil {
			rows.Close()
			return err
		}
		var atts []Attachment
		if json.Unmarshal([]byte(raw), &atts) != nil {
			continue
		}
		for i := range atts {
			a := &atts[i]
			if a.Type != "log" {
				continue
			}
			if a.Stream != "" && !strings.Contains(a.Stream, "/") {
				a.Stream = importedID(a.Stream)
			}
			if a.Channel != "" && !strings.Contains(a.Channel, "/") {
				a.Channel = ImportedProject + "/" + a.Channel
			}
			for j, s := range a.Streams {
				if !strings.Contains(s, "/") {
					a.Streams[j] = importedID(s)
				}
			}
		}
		b, err := json.Marshal(atts)
		if err != nil {
			rows.Close()
			return err
		}
		fixes = append(fixes, fix{seq, string(b)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, f := range fixes {
		if _, err := tx.ExecContext(ctx, `UPDATE entries SET attachments = ? WHERE seq = ?`, f.atts, f.seq); err != nil {
			return err
		}
	}
	return nil
}

// importGrants gives every principal the whole imported project with what
// its tokens and sessions carried, so nobody loses access on upgrade.
// People with neither get what reading and talking need; the admin needs
// no grant.
func importGrants(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT p.name, p.kind,
		coalesce((SELECT group_concat(scopes, ' ') FROM tokens WHERE principal = p.name), '') || ' ' ||
		coalesce((SELECT group_concat(scopes, ' ') FROM sessions WHERE principal = p.name), '')
		FROM principals p WHERE p.name <> 'admin'`)
	if err != nil {
		return err
	}
	type grant struct{ principal, scopes string }
	var grants []grant
	for rows.Next() {
		var name, kind, all string
		if err := rows.Scan(&name, &kind, &all); err != nil {
			rows.Close()
			return err
		}
		have := strings.Fields(all)
		var keep []string
		for _, sc := range grantable {
			for _, h := range have {
				if h == sc {
					keep = append(keep, sc)
					break
				}
			}
		}
		if len(keep) == 0 && kind == "human" {
			keep = []string{"read", "message"}
		}
		if len(keep) > 0 {
			grants = append(grants, grant{name, strings.Join(keep, " ")})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, g := range grants {
		if _, err := tx.ExecContext(ctx, `INSERT INTO grants(principal, project, channel, scopes) VALUES (?, ?, NULL, ?)
			ON CONFLICT DO NOTHING`, g.principal, ImportedProject, g.scopes); err != nil {
			return err
		}
	}
	return nil
}
