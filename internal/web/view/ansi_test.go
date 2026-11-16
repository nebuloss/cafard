package view

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite internal/web/testdata/ansi/vectors.json")

const vectorsPath = "../testdata/ansi/vectors.json"

// Vectors shared with src/render/AnsiParser.test.ts, so the server-rendered
// lines and the live ones colour the same text the same way.
var ansiInputs = []string{
	"plain text",
	"\x1b[31merror\x1b[0m done",
	"\x1b[1;32mbold green\x1b[22m green \x1b[39mplain",
	"\x1b[91;44mbright red on blue\x1b[m",
	"\x1b[38;5;3myellow\x1b[38;5;200m dropped\x1b[0m",
	"\x1b[38;2;255;0;0mtruecolour dropped\x1b[0m after",
	"\x1b]0;title\x07after osc",
	"\x1b[2Jcleared \x1b[Kline",
	"tab\tand\r\ncarriage\x00null\x7fdel",
	"<script>alert(1)</script>",
	"\x1b[31m<img src=x onerror=alert(1)>\x1b[0m",
	"\x1b[3;4mitalic underline\x1b[23;24m none",
	"unterminated \x1b[31",
	"\x1b[0;35;100mmagenta on grey\x1b[49m",
	"caf\u00e9 \u2192 \U0001F41E",
}

type vector struct {
	In  string    `json:"in"`
	Out []Segment `json:"out"`
}

func TestANSIVectors(t *testing.T) {
	var vs []vector
	for _, in := range ansiInputs {
		out := ANSI(in)
		if out == nil {
			out = []Segment{}
		}
		vs = append(vs, vector{In: in, Out: out})
	}
	got, err := json.MarshalIndent(vs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *update {
		if err := os.MkdirAll(filepath.Dir(vectorsPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorsPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	disk, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/web/view -update)", err)
	}
	if !bytes.Equal(disk, got) {
		t.Errorf("%s is stale (run go test ./internal/web/view -update)", vectorsPath)
	}
}

func TestANSIKeepsOnlyKnownClasses(t *testing.T) {
	segs := ANSI("\x1b[31mred\x1b[0m")
	if len(segs) != 1 || segs[0].Class != "ansi-fg-1" || segs[0].Text != "red" {
		t.Errorf("segments = %+v", segs)
	}
	for _, s := range ANSI("\x1b[38;5;999m\x1b[999mx") {
		if s.Class != "" {
			t.Errorf("out of range code kept: %+v", s)
		}
	}
}
