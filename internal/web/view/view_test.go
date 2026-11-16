package view

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// The same ids are pinned in src/render/StreamTone.test.ts.
func TestToneIsStable(t *testing.T) {
	for id, want := range map[string]int{"serial:dut1": 5, "syslog:bench-pi": 4, "push:laptop": 5, "": 5} {
		if got := Tone(id); got != want {
			t.Errorf("%q: %d, want %d", id, got, want)
		}
	}
}

func TestEntryKeepsSeverityAndSender(t *testing.T) {
	at := time.Date(2026, 11, 16, 9, 41, 2, 0, time.UTC)
	e := Entry(context.Background(), store.Entry{Seq: 3, TS: at, Stream: "serial:dut1", Kind: store.KindMarker, Severity: 0, Text: "x", Sender: "codex"}, nil)
	if e.Severity != 0 || e.Sender == nil || e.Sender.Name != "codex" || e.Sender.Kind != dto.KindAgent || e.Kind != dto.EntryMarker {
		t.Errorf("entry = %+v", e)
	}
}

func TestSnippetsFollowScopes(t *testing.T) {
	read := TokenSnippets("https://cafard.lan", "codex", "cfd_x", auth.NewScopeSet(auth.ScopeRead))
	ingest := TokenSnippets("https://cafard.lan", "bench", "cfd_x", auth.NewScopeSet(auth.ScopeIngest))
	text := func(s []dto.Snippet) string {
		var b strings.Builder
		for _, x := range s {
			b.WriteString(x.Text + "\n")
		}
		return b.String()
	}
	if r := text(read); !strings.Contains(r, "claude mcp add --transport http cafard https://cafard.lan/mcp") || strings.Contains(r, "OTEL_") {
		t.Errorf("read snippets:\n%s", r)
	}
	if i := text(ingest); strings.Contains(i, "claude mcp") || !strings.Contains(i, "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=https://cafard.lan/v1/logs") {
		t.Errorf("ingest snippets:\n%s", i)
	}
	if got := SourceCommands("http://10.0.50.2:8790")[0].Text; got != "logger -n 10.0.50.2 -P 5514 -T hello" {
		t.Errorf("syslog command %q", got)
	}
}
