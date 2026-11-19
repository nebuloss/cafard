package store_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/store"
)

// An install made before projects: its data is written with the old ids,
// then the import step is run again as on the first start of this version.
func TestOldInstallMovesIntoImported(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), store.FileName)
	st, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	db := st.DB()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO principals(name, kind, created_at) VALUES ('codex', 'agent', 1), ('phone', 'human', 1), ('dev', 'device', 1), ('admin', 'human', 1), ('nobody', 'agent', 1)`)
	exec(`INSERT INTO tokens(hash, principal, scopes, created_at) VALUES ('h1', 'codex', 'read message', 1), ('h2', 'codex', 'serial_write', 1), ('h3', 'dev', 'ingest', 1)`)
	exec(`INSERT INTO channels(name, created_by, created_at, last_activity) VALUES ('general', 'cafard', 1, 1), ('dut1', 'codex', 1, 1)`)
	exec(`INSERT INTO channel_streams(channel, pattern) VALUES ('dut1', 'serial:dut1'), ('dut1', 'syslog:dut1*')`)
	exec(`INSERT INTO follows(principal, chat) VALUES ('codex', 'chan:dut1'), ('codex', 'dm:codex~phone')`)
	exec(`INSERT INTO claims(resource, principal, expires_at) VALUES ('serial:dut1', 'codex', 9)`)
	to := int64(7)
	write(t, st,
		line("serial:dut1", "boot", 6, t0),
		line("syslog:10.0.50.42", "hello", 6, t0),
		line("syslog:Big Host", "named", 6, t0),
		store.Entry{Stream: "chan:dut1", Kind: store.KindMessage, Text: "look", Sender: "codex", TS: t0,
			Attachments: []store.Attachment{{Type: "log", Stream: "serial:dut1", Streams: []string{"serial:dut1"}, FromSeq: 1, ToSeq: &to}}},
		store.Entry{Stream: "dm:codex~phone", Kind: store.KindMessage, Text: "psst", Sender: "codex", TS: t0},
	)
	exec(`DELETE FROM schema_migrations WHERE name = '0071_projects_import'`)
	exec(`DELETE FROM projects`)
	st.Close()

	st, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	db = st.DB()
	ids, _ := store.QueryColumn[string](ctx, db, `SELECT id FROM streams ORDER BY id`)
	want := "chan:imported/dut1 dm:codex~phone serial:imported/dut1 syslog:imported/10.0.50.42 syslog:imported/Big Host"
	if strings.Join(ids, " ") != want {
		t.Errorf("streams = %q, want %q", strings.Join(ids, " "), want)
	}
	got, _ := store.QueryColumn[string](ctx, db, `SELECT stream FROM entries ORDER BY seq`)
	if strings.Join(got, " ") != "serial:imported/dut1 syslog:imported/10.0.50.42 syslog:imported/Big Host chan:imported/dut1 dm:codex~phone" {
		t.Errorf("entries moved to %v", got)
	}
	chans, _ := store.QueryColumn[string](ctx, db, `SELECT name FROM channels ORDER BY name`)
	if strings.Join(chans, " ") != "imported/dut1 imported/general" {
		t.Errorf("channels = %v", chans)
	}
	pats, _ := store.QueryColumn[string](ctx, db, `SELECT channel || ' ' || pattern FROM channel_streams ORDER BY pattern`)
	if strings.Join(pats, ",") != "imported/dut1 serial:imported/dut1,imported/dut1 syslog:imported/dut1*" {
		t.Errorf("bindings = %v", pats)
	}
	follows, _ := store.QueryColumn[string](ctx, db, `SELECT chat FROM follows ORDER BY chat`)
	if strings.Join(follows, " ") != "chan:imported/dut1 dm:codex~phone" {
		t.Errorf("follows = %v", follows)
	}
	claims, _ := store.QueryColumn[string](ctx, db, `SELECT resource FROM claims`)
	if strings.Join(claims, " ") != "serial:imported/dut1" {
		t.Errorf("claims = %v", claims)
	}
	msg, err := st.Get(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	if a := msg.Attachments[0]; a.Stream != "serial:imported/dut1" || a.Streams[0] != "serial:imported/dut1" || *a.ToSeq != 7 {
		b, _ := json.Marshal(a)
		t.Errorf("attachment = %s", b)
	}
	sinks, _ := store.QueryColumn[string](ctx, db, `SELECT project || '/' || name FROM sinks ORDER BY name`)
	if strings.Join(sinks, " ") != "imported/10.0.50.42 imported/dut1" {
		t.Errorf("sinks = %v (a name that is no sink name gets none)", sinks)
	}
	sources, _ := store.QueryColumn[string](ctx, db, `SELECT k.name || ' ' || s.prefix FROM sink_sources s JOIN sinks k ON k.id = s.sink`)
	if strings.Join(sources, ",") != "10.0.50.42 10.0.50.42/32" {
		t.Errorf("sources = %v", sources)
	}
	grants, _ := store.QueryColumn[string](ctx, db, `SELECT principal || ' ' || project || ' ' || coalesce(channel, '*') || ' ' || scopes FROM grants ORDER BY principal`)
	if strings.Join(grants, ",") != "codex imported * read message serial_write,dev imported * ingest,phone imported * read message" {
		t.Errorf("grants = %v", grants)
	}
	var retention any
	if err := db.QueryRow(`SELECT lines_max_age FROM projects WHERE name = 'imported'`).Scan(&retention); err != nil || retention != nil {
		t.Errorf("imported retention = %v, %v: want NULL until the server fills it from the config", retention, err)
	}
}

func TestNewDatabaseHasNoProject(t *testing.T) {
	st := open(t)
	n, _ := store.QueryColumn[string](context.Background(), st.DB(), `SELECT name FROM projects`)
	if len(n) != 0 {
		t.Errorf("projects = %v", n)
	}
}

func TestRetentionPerProject(t *testing.T) {
	st := open(t)
	old := t0.Add(-48 * time.Hour)
	write(t, st,
		line("serial:lab/dut1", "lab old", 6, old),
		line("serial:keep/dut1", "keep old", 6, old),
		line("push:loose", "outside old", 6, old),
		store.Entry{Stream: "chan:lab/general", Kind: store.KindMessage, Text: "lab msg", Sender: "x", TS: old},
		store.Entry{Stream: "dm:a~b", Kind: store.KindMessage, Text: "dm", Sender: "a", TS: old},
	)
	ret := store.Retention{LinesMaxAge: 24 * time.Hour, MessagesMaxAge: 24 * time.Hour}
	j := store.NewJanitor(st, execDirect(st), ret, clock.NewFake(t0), slog.New(slog.NewTextHandler(io.Discard, nil)))
	j.SetProjects(func(context.Context) (map[string]store.Limits, error) {
		return map[string]store.Limits{
			"lab":  {LinesMaxAge: time.Hour, MessagesMaxAge: 0},
			"keep": {},
		}, nil
	})
	if _, err := j.PassEntries(context.Background()); err != nil {
		t.Fatal(err)
	}
	left, _ := store.QueryColumn[string](context.Background(), st.DB(), `SELECT text FROM entries ORDER BY seq`)
	if strings.Join(left, "|") != "keep old|lab msg" {
		t.Errorf("left = %v: lab lines age out, lab messages and keep stay, the rest follows the config", left)
	}
}

func TestSizeLimitPerProject(t *testing.T) {
	st := open(t)
	var es []store.Entry
	for range 10 {
		es = append(es, line("push:lab/a", strings.Repeat("x", 36), 6, t0)) // 100 bytes with the overhead
		es = append(es, line("push:other/a", strings.Repeat("y", 36), 6, t0))
	}
	write(t, st, es...)
	j := store.NewJanitor(st, execDirect(st), store.Retention{LinesMaxBytes: 1}, clock.NewFake(t0), slog.New(slog.NewTextHandler(io.Discard, nil)))
	j.SetProjects(func(context.Context) (map[string]store.Limits, error) {
		return map[string]store.Limits{"lab": {LinesMaxBytes: 500}, "other": {}}, nil
	})
	res, err := j.PassEntries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.BySize != 5 {
		t.Errorf("deleted %d by size, want 5 (lab only; the config's limit is for what is outside projects)", res.BySize)
	}
}

func TestProjectOf(t *testing.T) {
	for id, want := range map[string]string{
		"serial:lab/dut1": "lab", "chan:lab/general": "lab", "dm:a~b": "", "serial:dut1": "", "push:lab/a/b": "lab",
	} {
		if got := store.ProjectOf(id); got != want {
			t.Errorf("ProjectOf(%q) = %q, want %q", id, got, want)
		}
	}
	if got := store.LocalName("chan:lab/dut1"); got != "dut1" {
		t.Errorf("LocalName = %q", got)
	}
}
