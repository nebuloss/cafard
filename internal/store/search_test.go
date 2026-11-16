package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/store"
)

func searchSeqs(t *testing.T, st *store.Store, q store.SearchQuery) []int64 {
	t.Helper()
	res, err := st.Search(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	var out []int64
	for _, e := range res.Entries {
		out = append(out, e.Seq)
	}
	return out
}

func TestSearchIndexesTextWithoutEscapes(t *testing.T) {
	st := open(t)
	write(t, st,
		line("serial:dut1", "\x1b[31merror\x1b[0m: dwc3 timeout", 3, t0),
		line("serial:dut1", "plain line", 6, t0),
		store.Entry{Stream: "chan:general", Kind: store.KindMessage, Severity: 6, Text: "error in a message", TS: t0},
	)
	if got := searchSeqs(t, st, store.SearchQuery{Match: "error"}); len(got) != 1 || got[0] != 1 {
		t.Errorf("error matched %v, want [1] (messages are not log streams)", got)
	}
	if got := searchSeqs(t, st, store.SearchQuery{Match: "31merror"}); len(got) != 0 {
		t.Errorf("the escape sequence was indexed: %v", got)
	}
	if got := searchSeqs(t, st, store.SearchQuery{Match: "error", Streams: []string{"chan:general"}}); len(got) != 1 || got[0] != 3 {
		t.Errorf("explicit message stream matched %v", got)
	}
	// Deletes still find the indexed form.
	if _, err := st.DB().Exec(`DELETE FROM entries WHERE seq = 1`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT count(*) FROM entries_fts WHERE entries_fts MATCH 'dwc3'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("after delete: %d rows, %v", n, err)
	}
}

func TestSearchPagesInSeqOrderWithTimeBounds(t *testing.T) {
	st := open(t)
	var es []store.Entry
	for i := range 30 {
		es = append(es, line("push:a", "usb reset", 6, t0.Add(time.Duration(i)*time.Minute)))
		es = append(es, line("push:b", "other", 6, t0))
	}
	write(t, st, es...)
	res, err := st.Search(context.Background(), store.SearchQuery{Match: "usb", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 10 || !res.HasMore || res.Entries[0].Seq != 1 || res.NextSeq != res.Entries[9].Seq+1 {
		t.Fatalf("first page: %d entries, more=%v, next=%d", len(res.Entries), res.HasMore, res.NextSeq)
	}
	got := searchSeqs(t, st, store.SearchQuery{Match: "usb", From: t0.Add(10 * time.Minute), To: t0.Add(12 * time.Minute), Limit: 100})
	if len(got) != 3 {
		t.Errorf("time window: %v", got)
	}
	got = searchSeqs(t, st, store.SearchQuery{Match: "usb", Keep: func(e store.Entry) bool { return e.Seq%4 == 1 }, Limit: 100})
	if len(got) != 15 {
		t.Errorf("keep: %d entries", len(got))
	}
}

func TestSearchReportsBadQueries(t *testing.T) {
	st := open(t)
	write(t, st, line("push:a", "x", 6, t0))
	for _, q := range []string{`"unterminated`, `a AND`, `(a`} {
		_, err := st.Search(context.Background(), store.SearchQuery{Match: q})
		var qe *store.QueryError
		if !errors.As(err, &qe) {
			t.Errorf("%q: err = %v, want a QueryError", q, err)
		}
	}
}

func TestMigrationReindexesAnOldDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), store.FileName)
	ctx := context.Background()
	st, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	write(t, st, line("push:a", "\x1b[1mbold\x1b[0m word", 6, t0))
	// Put the database back as track 0 left it: raw text in the index.
	for _, stmt := range []string{
		`DELETE FROM schema_migrations WHERE name = '0050_g_fts_stripped.sql'`,
		`INSERT INTO entries_fts(entries_fts) VALUES ('delete-all')`,
		`INSERT INTO entries_fts(rowid, text) SELECT seq, text FROM entries`,
	} {
		if _, err := st.DB().Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if got := searchSeqs(t, st, store.SearchQuery{Match: "bold"}); len(got) != 0 {
		t.Fatalf("setup: raw index found %v", got)
	}
	st.Close()
	st, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if got := searchSeqs(t, st, store.SearchQuery{Match: "bold"}); len(got) != 1 {
		t.Errorf("after the migration: %v", got)
	}
}
