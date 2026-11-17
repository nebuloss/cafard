package store_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/store"
)

func open(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

var t0 = time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

func line(stream, text string, sev int, at time.Time) store.Entry {
	return store.Entry{Stream: stream, Kind: store.KindLine, Severity: sev, Text: text, TS: at}
}

func write(t *testing.T, st *store.Store, es ...store.Entry) []store.Entry {
	t.Helper()
	out, err := st.WriteBatch(context.Background(), es)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDatabaseFileIsPrivate(t *testing.T) {
	st := open(t)
	write(t, st, line("push:a", "x", 6, t0))
	var path string
	if err := st.DB().QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal"} {
		info, err := os.Stat(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode&0o077 != 0 && suffix == "" {
			t.Errorf("%s mode = %o, want 0600", path+suffix, mode)
		}
	}
}

func TestEnsureDataDirIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureDataDir(dir); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %o", info.Mode().Perm())
	}
}

func TestMigrationsAreRecordedAndReopenIsANoop(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, store.FileName)
	for range 2 {
		st, err := store.Open(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		var first string
		var count int
		if err := st.DB().QueryRow(`SELECT min(name), count(*) FROM schema_migrations`).Scan(&first, &count); err != nil {
			t.Fatal(err)
		}
		if first != "0001_0_core.sql" || count != len(migrationFiles(t)) {
			t.Errorf("migrations = %d from %s", count, first)
		}
		st.Close()
	}
}

// Migration 0060 takes the admin scope off every stored credential: from
// then on only the admin password gives it.
func TestTheAdminScopeMigrationStripsStoredCredentials(t *testing.T) {
	p := filepath.Join(t.TempDir(), store.FileName)
	st, err := store.Open(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO principals(name, kind, created_at) VALUES ('admin', 'human', 0)`,
		`INSERT INTO tokens(hash, principal, scopes, created_at) VALUES ('a', 'admin', 'ingest read message admin', 0)`,
		`INSERT INTO tokens(hash, principal, scopes, created_at) VALUES ('b', 'admin', 'admin', 0)`,
		`INSERT INTO tokens(hash, principal, scopes, created_at) VALUES ('c', 'admin', 'read', 0)`,
		`INSERT INTO sessions(hash, principal, scopes, created_at, expires_at) VALUES ('d', 'admin', 'read admin', 0, 1)`,
		`INSERT INTO pairings(code_hash, principal, scopes, created_at, expires_at) VALUES ('e', 'admin', 'message admin', 0, 1)`,
		// Run it again over these rows, as on an install that had them.
		`DELETE FROM schema_migrations WHERE name = '0060_auth_admin.sql'`,
	} {
		if _, err := st.DB().Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	st.Close()
	st = openAt(t, p)
	got := map[string]string{}
	rows, err := st.DB().Query(`SELECT hash, scopes FROM tokens UNION ALL SELECT hash, scopes FROM sessions
		UNION ALL SELECT code_hash, scopes FROM pairings`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		got[k] = v
	}
	want := map[string]string{"a": "ingest read message", "c": "read", "d": "read", "e": "message"}
	if len(got) != len(want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: scopes %q, want %q", k, got[k], v)
		}
	}
}

func openAt(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestForeignKeysCascade(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	db := st.DB()
	if _, err := db.ExecContext(ctx, `INSERT INTO principals(name, kind, created_at) VALUES ('a', 'agent', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tokens(hash, principal, scopes, created_at) VALUES ('h', 'a', 'read', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM principals WHERE name = 'a'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM tokens`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("tokens left after cascade: %d", n)
	}
}

func TestWriteBatchAssignsSeqAndRoundTrips(t *testing.T) {
	st := open(t)
	src := t0.Add(-time.Second)
	out := write(t, st,
		store.Entry{Stream: "serial:dut1", Kind: store.KindLine, Severity: 3, Host: "h", App: "a", Text: "boom", TS: t0, SrcTS: &src, Meta: []byte(`{"facility":1}`)},
		line("serial:dut1", "two", 6, t0),
	)
	if out[0].Seq != 1 || out[1].Seq != 2 {
		t.Fatalf("seqs = %d, %d", out[0].Seq, out[1].Seq)
	}
	got, err := st.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "boom" || got.Severity != 3 || got.Host != "h" || !got.TS.Equal(t0) || got.SrcTS == nil || !got.SrcTS.Equal(src) || string(got.Meta) != `{"facility":1}` {
		t.Errorf("round trip = %+v", got)
	}
	streams, err := st.Streams(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || streams[0].LastSeq != 2 || streams[0].Kind != "serial" || streams[0].LastLine != "two" {
		t.Errorf("streams = %+v", streams)
	}
	last, _ := st.LastSeq(context.Background())
	if last != 2 {
		t.Errorf("last seq = %d", last)
	}
}

func TestReadNeverReturnsMessagesUnlessNamed(t *testing.T) {
	st := open(t)
	write(t, st,
		line("serial:dut1", "a", 6, t0),
		store.Entry{Stream: "chan:general", Kind: store.KindMessage, Text: "hi", Sender: "x", TS: t0},
		store.Entry{Stream: "dm:a~b", Kind: store.KindMessage, Text: "secret", Sender: "a", TS: t0},
	)
	res, err := st.Read(context.Background(), store.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Stream != "serial:dut1" {
		t.Errorf("entries = %+v", res.Entries)
	}
	ids, _ := st.MatchStreams(context.Background(), []string{"*"})
	if len(ids) != 1 {
		t.Errorf("glob * matched %v, want only the log stream", ids)
	}
	ids, _ = st.MatchStreams(context.Background(), []string{"chan:*"})
	if len(ids) != 1 || ids[0] != "chan:general" {
		t.Errorf("chan:* matched %v", ids)
	}
}

func TestReadFiltersAndPages(t *testing.T) {
	st := open(t)
	var es []store.Entry
	for i := range 50 {
		sev := 6
		if i%10 == 0 {
			sev = 3
		}
		es = append(es, line("push:a", "line "+string(rune('a'+i%26)), sev, t0))
		es = append(es, line("push:b", "other", 6, t0))
	}
	write(t, st, es...)
	ctx := context.Background()

	res, err := st.Read(ctx, store.Query{Streams: []string{"push:a"}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 20 || !res.HasMore || res.NextSeq != res.Entries[19].Seq+1 {
		t.Fatalf("page 1: %d entries, more=%v next=%d", len(res.Entries), res.HasMore, res.NextSeq)
	}
	total := len(res.Entries)
	for res.HasMore {
		res, err = st.Read(ctx, store.Query{Streams: []string{"push:a"}, SinceSeq: res.NextSeq, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		total += len(res.Entries)
	}
	if total != 50 {
		t.Errorf("paged %d entries, want 50", total)
	}

	sev := 3
	res, _ = st.Read(ctx, store.Query{MinSeverity: &sev, Limit: 100})
	if len(res.Entries) != 5 {
		t.Errorf("severity filter: %d entries, want 5", len(res.Entries))
	}

	res, _ = st.Read(ctx, store.Query{Pattern: regexp.MustCompile(`line [ab]$`), Limit: 100})
	if len(res.Entries) != 4 {
		t.Errorf("pattern filter: %d entries, want 4", len(res.Entries))
	}

	res, _ = st.Read(ctx, store.Query{Streams: []string{"push:b"}, Limit: 3, Desc: true})
	if len(res.Entries) != 3 || res.Entries[2].Seq != 100 || res.Entries[0].Seq != 96 || !res.HasMore {
		t.Errorf("tail = %+v more=%v", seqs(res.Entries), res.HasMore)
	}

	res, _ = st.Read(ctx, store.Query{Streams: []string{}})
	if len(res.Entries) != 0 {
		t.Errorf("empty stream list read %d entries", len(res.Entries))
	}
}

func TestReadAtTheEndKeepsTheCursor(t *testing.T) {
	st := open(t)
	write(t, st, line("push:a", "x", 6, t0), line("push:a", "y", 6, t0))
	res, err := st.Read(context.Background(), store.Query{SinceSeq: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 0 || res.HasMore || res.NextSeq != 3 {
		t.Errorf("at end: %+v", res)
	}
	res, _ = st.Read(context.Background(), store.Query{})
	if res.NextSeq != 3 {
		t.Errorf("next after full read = %d, want 3", res.NextSeq)
	}
}

func TestPatternScanBudget(t *testing.T) {
	st := open(t)
	var es []store.Entry
	for range 3000 {
		es = append(es, line("push:a", "noise", 6, t0))
	}
	write(t, st, es...)
	res, err := st.Read(context.Background(), store.Query{Pattern: regexp.MustCompile("never"), Limit: 10, ScanLimit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasMore || len(res.Entries) != 0 || res.NextSeq < 1000 || res.NextSeq > 2001 {
		t.Errorf("budget: more=%v next=%d", res.HasMore, res.NextSeq)
	}
}

func TestAround(t *testing.T) {
	st := open(t)
	var es []store.Entry
	for range 10 {
		es = append(es, line("push:a", "a", 6, t0), line("push:b", "b", 6, t0))
	}
	write(t, st, es...)
	got, err := st.Around(context.Background(), 9, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if s := seqs(got); strings.Join(s, ",") != "5,7,9,11,13" {
		t.Errorf("around = %v", s)
	}
	if _, err := st.Around(context.Background(), 999, 1, 1); err != store.ErrNotFound {
		t.Errorf("missing seq err = %v", err)
	}
}

func TestFTSFollowsInsertsAndDeletes(t *testing.T) {
	st := open(t)
	write(t, st, line("push:a", "kernel panic here", 2, t0), line("push:a", "all fine", 6, t0))
	ctx := context.Background()
	count := func() int {
		var n int
		if err := st.DB().QueryRowContext(ctx, `SELECT count(*) FROM entries_fts WHERE entries_fts MATCH 'panic'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 1 {
		t.Fatal("fts missed the insert")
	}
	if _, err := st.DB().ExecContext(ctx, `DELETE FROM entries WHERE seq = 1`); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatal("fts kept a deleted row")
	}
}

func TestValidateStreamID(t *testing.T) {
	good := []string{"serial:dut1", "syslog:10.0.0.1", "syslog:fe80::1", "dm:a~b", "push:my-box_2"}
	bad := []string{"", "serial", ":x", "Serial:x", "push:", "push:a b", "push:a*", "push:a\x00"}
	for _, id := range good {
		if err := store.ValidateStreamID(id); err != nil {
			t.Errorf("%q: %v", id, err)
		}
	}
	for _, id := range bad {
		if err := store.ValidateStreamID(id); err == nil {
			t.Errorf("%q: want an error", id)
		}
	}
}

func execDirect(st *store.Store) store.Exec {
	return func(ctx context.Context, fn func(ctx context.Context, tx *sql.Tx) error) error {
		tx, err := st.DB().BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := fn(ctx, tx); err != nil {
			tx.Rollback()
			return err
		}
		return tx.Commit()
	}
}

func TestRetentionByAgeWithOverridesAndMessages(t *testing.T) {
	st := open(t)
	old := t0.Add(-20 * 24 * time.Hour)
	ancient := t0.Add(-40 * 24 * time.Hour)
	write(t, st,
		line("push:a", "old", 6, old),
		line("push:a", "new", 6, t0),
		line("serial:dut1", "old serial", 6, old),
		line("serial:dut1", "ancient serial", 6, ancient),
		store.Entry{Stream: "chan:general", Kind: store.KindMessage, Text: "old msg", Sender: "x", TS: old},
	)
	var ret store.Retention
	ret.SetDefaults()
	ret.Streams = []store.StreamRetention{{Pattern: "serial:*", LinesMaxAge: 30 * 24 * time.Hour}}
	j := store.NewJanitor(st, execDirect(st), ret, clock.NewFake(t0), slog.New(slog.NewTextHandler(io.Discard, nil)))
	j.Chunk = 1
	res, err := j.PassEntries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.ByAge != 2 {
		t.Errorf("deleted %d by age, want 2", res.ByAge)
	}
	left, _ := st.Read(context.Background(), store.Query{Streams: []string{"push:a", "serial:dut1", "chan:general"}, Limit: 10})
	var texts []string
	for _, e := range left.Entries {
		texts = append(texts, e.Text)
	}
	if strings.Join(texts, "|") != "new|old serial|old msg" {
		t.Errorf("left = %v", texts)
	}
	last, _ := st.LastSeq(context.Background())
	if last != 5 {
		t.Errorf("last seq after deletes = %d, want 5 (never reused)", last)
	}
}

func TestRetentionBySize(t *testing.T) {
	st := open(t)
	var es []store.Entry
	for range 100 {
		es = append(es, line("push:a", strings.Repeat("x", 36), 6, t0)) // 36 + 64 overhead = 100 bytes
	}
	es = append(es, store.Entry{Stream: "chan:general", Kind: store.KindMessage, Text: strings.Repeat("m", 1000), Sender: "x", TS: t0})
	write(t, st, es...)
	ret := store.Retention{LinesMaxBytes: 5000}
	j := store.NewJanitor(st, execDirect(st), ret, clock.NewFake(t0), slog.New(slog.NewTextHandler(io.Discard, nil)))
	res, err := j.PassEntries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.BySize != 50 {
		t.Errorf("deleted %d by size, want 50", res.BySize)
	}
	first, _ := st.Read(context.Background(), store.Query{Limit: 1})
	if first.Entries[0].Seq != 51 {
		t.Errorf("oldest left = %d, want 51", first.Entries[0].Seq)
	}
	if _, err := st.Get(context.Background(), 101); err != nil {
		t.Errorf("message was deleted by the line size limit: %v", err)
	}
}

func seqs(es []store.Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, itoa(e.Seq))
	}
	return out
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func migrationFiles(t *testing.T) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join("migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestStreamRowCarriesLabels(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	write(t, st, line("serial:dut1", "boot", 6, t0))
	if _, err := st.DB().ExecContext(ctx, `UPDATE streams SET labels = '{"board":"rk3588"}' WHERE id = 'serial:dut1'`); err != nil {
		t.Fatal(err)
	}
	row, err := st.StreamRow(ctx, "serial:dut1")
	if err != nil || row.Labels["board"] != "rk3588" || row.LastLine != "boot" {
		t.Errorf("row = %+v, %v", row, err)
	}
	if _, err := st.StreamRow(ctx, "serial:nope"); err != store.ErrNotFound {
		t.Errorf("missing stream err = %v", err)
	}
}
