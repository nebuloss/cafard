package mcpserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

func TestFTSQueryQuotesWhatFTS5CannotTake(t *testing.T) {
	for in, want := range map[string]string{
		"kernel panic":            "kernel panic",
		"usb-1.2 reset":           `"usb-1.2" reset`,
		"error: -110":             `"error:" "-110"`,
		`"exact phrase" OR dwc3*`: `"exact phrase" OR dwc3*`,
		"NEAR(a b, 5)":            "NEAR(a b, 5)",
		"near something":          "near something",
		"/dev/ttyUSB0*":           `"/dev/ttyUSB0"*`,
		"^boot":                   "^boot",
		`say "hi`:                 `say "hi`,
		"fe80::1 NOT eth0":        `"fe80::1" NOT eth0`,
		"données":                 "données",
		"a, b":                    "a  b",
	} {
		got, err := FTSQuery(in)
		if err != nil || got != want {
			t.Errorf("FTSQuery(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "   ", "()"} {
		if _, err := FTSQuery(in); err == nil {
			t.Errorf("FTSQuery(%q): want an error", in)
		}
	}
}

func TestParseTimeTakesRFC3339AndRelative(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"":                          {},
		"now":                       now,
		"-2h":                       now.Add(-2 * time.Hour),
		"-1h30m":                    now.Add(-90 * time.Minute),
		"-1d":                       now.Add(-24 * time.Hour),
		"-1w":                       now.Add(-7 * 24 * time.Hour),
		"-90s":                      now.Add(-90 * time.Second),
		"-1.5h":                     now.Add(-90 * time.Minute),
		"2026-11-16T10:00:00Z":      time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC),
		"2026-11-16T11:00:00+01:00": time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC),
		"2026-11-16":                time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC),
	} {
		got, err := ParseTime(in, now, "from")
		if err != nil || !got.Equal(want) {
			t.Errorf("ParseTime(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"yesterday", "2h", "-2x", "16/11/2026"} {
		_, err := ParseTime(in, now, "from")
		if e, ok := err.(*dto.APIError); !ok || e.Code != dto.CodeBadRequest || !strings.Contains(e.Message, "from=") {
			t.Errorf("ParseTime(%q) err = %v", in, err)
		}
	}
}

func TestSearchFindsColouredLinesAndNeverMessages(t *testing.T) {
	s, w, _ := newTestServer(t)
	ctx := context.Background()
	if _, err := w.CommitBatch(ctx, []store.Entry{
		{Stream: "serial:lab/dut1", Text: "\x1b[31mdwc3 fe800000.usb: error -110\x1b[0m", Severity: 3},
		{Stream: "serial:lab/dut1", Text: "login:", Severity: 6},
		{Stream: "dm:a~b", Kind: store.KindMessage, Text: "the dwc3 password", Sender: "b", Severity: 6},
		{Stream: "chan:lab/general", Kind: store.KindMessage, Text: "dwc3 again", Sender: "b", Severity: 6},
	}); err != nil {
		t.Fatal(err)
	}
	text, page, err := s.search(ctx, agent, searchIn{Query: "dwc3 fe800000.usb"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Shown != 1 || !strings.Contains(text, "error -110") || strings.Contains(text, "\x1b") {
		t.Errorf("search = %q %+v", text, page)
	}
	for _, streams := range [][]string{nil, {"*"}, {"dm:*"}, {"chan:lab/*", "dm:a~b"}} {
		text, _, err := s.search(ctx, agent, searchIn{Query: "dwc3", Streams: streams})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "password") || strings.Contains(text, "again") {
			t.Errorf("streams %v returned a message: %q", streams, text)
		}
	}
	if _, _, err := s.search(ctx, agent, searchIn{Query: `"unterminated`}); err == nil {
		t.Error("an unterminated phrase: want bad_request")
	} else if e, ok := err.(*dto.APIError); !ok || e.Code != dto.CodeBadRequest {
		t.Errorf("err = %v", err)
	}
	if _, _, err := s.search(ctx, agent, searchIn{Query: "x", From: "-1h", To: "-2h"}); err == nil {
		t.Error("to before from: want bad_request")
	}
	// Everything here was received just now.
	if _, page, _ := s.search(ctx, agent, searchIn{Query: "login", From: "-1m"}); page.Shown != 1 {
		t.Errorf("from=-1m shown %d", page.Shown)
	}
	if _, page, _ := s.search(ctx, agent, searchIn{Query: "login", To: "-1m"}); page.Shown != 0 {
		t.Errorf("to=-1m shown %d", page.Shown)
	}
}
