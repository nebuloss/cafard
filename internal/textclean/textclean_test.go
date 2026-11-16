package textclean

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nebuloss/cafard/internal/store"
)

var t0 = time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

type emitted struct {
	line string
	at   time.Time
}

type rec struct{ got []emitted }

func (r *rec) emit(line string, at time.Time) { r.got = append(r.got, emitted{line, at}) }

func (r *rec) lines() []string {
	out := make([]string, len(r.got))
	for i, e := range r.got {
		out[i] = e.line
	}
	return out
}

func eqLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("line %d: got %q, want %q (all: %q)", i, got[i], want[i], got)
		}
	}
}

func TestSplitter(t *testing.T) {
	tests := []struct {
		name    string
		writes  []string
		maxLine int
		close   bool
		want    []string
	}{
		{"lf", []string{"a\nb\n"}, 0, false, []string{"a", "b"}},
		{"crlf", []string{"a\r\nb\r\n"}, 0, false, []string{"a", "b"}},
		{"lone cr", []string{"a\rb\rc"}, 0, true, []string{"a", "b", "c"}},
		{"lone cr at end waits", []string{"a\r"}, 0, false, nil},
		{"lone cr at end then text", []string{"a\r", "b\n"}, 0, false, []string{"a", "b"}},
		{"crlf split", []string{"a\r", "\nb\n"}, 0, false, []string{"a", "b"}},
		{"crlf split many", []string{"a\r", "\n", "b\r", "\n"}, 0, false, []string{"a", "b"}},
		{"cr cr lf", []string{"a\r\r\n"}, 0, false, []string{"a", ""}},
		{"cr then cr split", []string{"a\r", "\r", "\n"}, 0, false, []string{"a", ""}},
		{"empty lines", []string{"\n\n"}, 0, false, []string{"", ""}},
		{"empty cr lines", []string{"\r\r\n\r"}, 0, true, []string{"", "", ""}},
		{"partial kept", []string{"abc"}, 0, false, nil},
		{"partial joined", []string{"ab", "c\n"}, 0, false, []string{"abc"}},
		{"close flushes", []string{"login: "}, 0, true, []string{"login: "}},
		{"close flushes cr", []string{"x\r"}, 0, true, []string{"x"}},
		{"close nothing", []string{"x\n"}, 0, true, []string{"x"}},
		{"utf8 split", []string{"h\xc3", "\xa9!\n"}, 0, false, []string{"hé!"}},
		{"utf8 split 3 ways", []string{"\xe2", "\x82", "\xac\n"}, 0, false, []string{"€"}},
		{"utf8 4 bytes split", []string{"\xf0\x9f", "\x98\x80\n"}, 0, false, []string{"😀"}},
		{"invalid byte", []string{"a\xffb\n"}, 0, false, []string{"a\uFFFDb"}},
		{"truncated rune before lf", []string{"a\xe2\x82\nb\n"}, 0, false, []string{"a\uFFFD\uFFFD", "b"}},
		{"truncated rune at close", []string{"a\xe2\x82"}, 0, true, []string{"a\uFFFD\uFFFD"}},
		{"truncated rune then ascii", []string{"\xe2", "A\n"}, 0, false, []string{"\uFFFDA"}},
		{"ansi kept raw", []string{"\x1b[31mred\x1b[0m\n"}, 0, false, []string{"\x1b[31mred\x1b[0m"}},
		{"long line", []string{"abcdefghij\n"}, 4, false, []string{"abcd" + ContinuationMarker, "efgh" + ContinuationMarker, "ij"}},
		{"exact max", []string{"abcd\n"}, 4, false, []string{"abcd"}},
		{"max plus one", []string{"abcde\n"}, 4, false, []string{"abcd" + ContinuationMarker, "e"}},
		{"long line across writes", []string{"abc", "def", "ghi\n"}, 4, false, []string{"abcd" + ContinuationMarker, "efgh" + ContinuationMarker, "i"}},
		{"long line rune boundary", []string{"ab€cd\n"}, 4, false, []string{"ab" + ContinuationMarker, "€c" + ContinuationMarker, "d"}},
		{"rune larger than max", []string{"€€\n"}, 1, false, []string{"€" + ContinuationMarker, "€"}},
		{"long partial at close", []string{"abcdef"}, 4, true, []string{"abcd" + ContinuationMarker, "ef"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r rec
			s := NewSplitter(DefaultFlushAfter, tt.maxLine, r.emit)
			for i, w := range tt.writes {
				s.Write([]byte(w), t0.Add(time.Duration(i)*time.Millisecond))
			}
			if tt.close {
				s.Close()
			}
			eqLines(t, r.lines(), tt.want)
		})
	}
}

func TestSplitterAt(t *testing.T) {
	var r rec
	s := NewSplitter(DefaultFlushAfter, 0, r.emit)
	s.Write([]byte("ab"), t0)
	s.Write([]byte("c\nd"), t0.Add(time.Second))
	s.Write([]byte("\n"), t0.Add(2*time.Second))
	eqLines(t, r.lines(), []string{"abc", "d"})
	if !r.got[0].at.Equal(t0) || !r.got[1].at.Equal(t0.Add(time.Second)) {
		t.Fatalf("times: %v", r.got)
	}
}

func TestSplitterTick(t *testing.T) {
	var r rec
	s := NewSplitter(500*time.Millisecond, 0, r.emit)
	s.Tick(t0) // nothing pending
	s.Write([]byte("login: "), t0)
	s.Tick(t0.Add(499 * time.Millisecond))
	if len(r.got) != 0 {
		t.Fatalf("flushed too early: %q", r.lines())
	}
	s.Tick(t0.Add(500 * time.Millisecond))
	eqLines(t, r.lines(), []string{"login: "})
	if !r.got[0].at.Equal(t0) {
		t.Fatalf("at = %v", r.got[0].at)
	}
	s.Tick(t0.Add(2 * time.Second)) // nothing more
	s.Write([]byte("root\n"), t0.Add(3*time.Second))
	s.Close()
	eqLines(t, r.lines(), []string{"login: ", "root"})
}

func TestSplitterTickCR(t *testing.T) {
	var r rec
	s := NewSplitter(500*time.Millisecond, 0, r.emit)
	s.Write([]byte("a\r"), t0)
	s.Tick(t0.Add(time.Second))
	eqLines(t, r.lines(), []string{"a"})
	// The \n that completes the \r\n must not make an empty line.
	s.Write([]byte("\nb\n"), t0.Add(2*time.Second))
	eqLines(t, r.lines(), []string{"a", "b"})

	r.got = nil
	s.Write([]byte("c\r"), t0)
	s.Tick(t0.Add(time.Second))
	s.Write([]byte("d\n"), t0.Add(2*time.Second))
	eqLines(t, r.lines(), []string{"c", "d"})

	r.got = nil
	s.Write([]byte("\r"), t0) // empty line ended by lone \r
	s.Tick(t0.Add(time.Second))
	s.Write([]byte("\n\n"), t0.Add(2*time.Second))
	eqLines(t, r.lines(), []string{"", ""})
}

func TestSplitterTickKeepsRune(t *testing.T) {
	var r rec
	s := NewSplitter(500*time.Millisecond, 0, r.emit)
	s.Write([]byte("ab\xc3"), t0)
	s.Tick(t0.Add(time.Second))
	eqLines(t, r.lines(), []string{"ab"})
	s.Write([]byte("\xa9\n"), t0.Add(2*time.Second))
	eqLines(t, r.lines(), []string{"ab", "é"})
	if !r.got[1].at.Equal(t0) {
		t.Fatalf("at of rune line = %v, want start of its first byte", r.got[1].at)
	}

	r.got = nil
	s.Write([]byte("\xc3"), t0)
	s.Tick(t0.Add(time.Second)) // only an incomplete rune: nothing to flush
	if len(r.got) != 0 {
		t.Fatalf("flushed %q", r.lines())
	}
	s.Close()
	eqLines(t, r.lines(), []string{"\uFFFD"})
}

func TestStripANSI(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"plain", "hello world", "hello world"},
		{"tab kept", "a\tb", "a\tb"},
		{"sgr", "\x1b[1;31mred\x1b[0m done", "red done"},
		{"sgr 256", "\x1b[38;5;196mx\x1b[m", "x"},
		{"cursor", "a\x1b[2Kb\x1b[10;20Hc\x1b[?25l", "abc"},
		{"osc bel", "\x1b]0;my title\x07prompt$ ", "prompt$ "},
		{"osc st", "\x1b]2;title\x1b\\after", "after"},
		{"osc hyperlink", "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"osc unterminated", "a\x1b]0;title", "a"},
		{"dcs", "a\x1bPq#0;2;0;0;0\x1b\\b", "ab"},
		{"charset", "\x1b(Bx\x1b)0y", "xy"},
		{"two byte", "\x1b7a\x1b8\x1bMb\x1b=", "ab"},
		{"lone esc", "a\x1b", "a"},
		{"esc then control", "a\x1b\x01b", "ab"},
		{"c1 csi", "a\u009b31mb", "ab"},
		{"c1 osc", "a\u009d0;t\u009cb", "ab"},
		{"control chars", "a\x00b\x07c\x08d\x7fe\rf\ng\x0c", "abcdefg"},
		{"c1 control", "a\u0085b", "ab"},
		{"utf8 kept", "héllo €", "héllo €"},
		{"invalid utf8", "a\xffb", "a\uFFFDb"},
		{"csi aborted", "\x1b[12\nx", "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripANSI(tt.in); got != tt.want {
				t.Fatalf("StripANSI(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func line(seq int64, ts time.Time, stream string, sev int, text string) store.Entry {
	return store.Entry{Seq: seq, TS: ts, Stream: stream, Kind: store.KindLine, Severity: sev, Text: text}
}

func TestFormat(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	ts := t0.Add(123 * time.Millisecond)
	tests := []struct {
		name    string
		entries []store.Entry
		lim     Limits
		loc     *time.Location
		want    string
		shown   int
		next    int64
		more    bool
	}{
		{
			name: "empty",
			lim:  AgentDefaults,
		},
		{
			name:    "one line",
			entries: []store.Entry{line(5, ts, "serial:board", 6, "hello")},
			lim:     AgentDefaults,
			want:    "# 2026-11-16 (UTC)\n5 10:00:00.123 serial:board INFO hello\n",
			shown:   1, next: 6,
		},
		{
			name: "severities",
			entries: []store.Entry{
				line(1, ts, "s:a", 0, "a"), line(2, ts, "s:a", 1, "b"), line(3, ts, "s:a", 2, "c"),
				line(4, ts, "s:a", 3, "d"), line(5, ts, "s:a", 4, "e"), line(6, ts, "s:a", 5, "f"),
				line(7, ts, "s:a", 7, "g"), line(8, ts, "s:a", 9, "h"),
			},
			want: "# 2026-11-16 (UTC)\n" +
				"1 10:00:00.123 s:a EMERG a\n2 10:00:00.123 s:a ALERT b\n3 10:00:00.123 s:a CRIT c\n" +
				"4 10:00:00.123 s:a ERR d\n5 10:00:00.123 s:a WARN e\n6 10:00:00.123 s:a NOTICE f\n" +
				"7 10:00:00.123 s:a DEBUG g\n8 10:00:00.123 s:a 9 h\n",
			shown: 8, next: 9,
		},
		{
			name: "location",
			entries: []store.Entry{
				line(1, time.Date(2026, 11, 16, 22, 59, 0, 0, time.UTC), "s:a", 6, "x"),
				line(2, time.Date(2026, 11, 16, 23, 1, 0, 0, time.UTC), "s:a", 6, "y"),
			},
			loc: paris,
			want: "# 2026-11-16 (Europe/Paris)\n1 23:59:00.000 s:a INFO x\n" +
				"# 2026-11-17 (Europe/Paris)\n2 00:01:00.000 s:a INFO y\n",
			shown: 2, next: 3,
		},
		{
			name: "date changes",
			entries: []store.Entry{
				line(1, t0, "s:a", 6, "x"),
				line(2, t0.Add(time.Hour), "s:a", 6, "y"),
				line(3, t0.Add(24*time.Hour), "s:a", 6, "z"),
			},
			want: "# 2026-11-16 (UTC)\n1 10:00:00.000 s:a INFO x\n2 11:00:00.000 s:a INFO y\n" +
				"# 2026-11-17 (UTC)\n3 10:00:00.000 s:a INFO z\n",
			shown: 3, next: 4,
		},
		{
			name: "ansi and newlines",
			entries: []store.Entry{
				line(1, t0, "s:a", 6, "\x1b[31mred\x1b[0m\tok\x07"),
				line(2, t0, "s:a", 6, "one\ntwo\r\nthree\n"),
			},
			want:  "# 2026-11-16 (UTC)\n1 10:00:00.000 s:a INFO red\tok\n2 10:00:00.000 s:a INFO one ⏎ two ⏎ three\n",
			shown: 2, next: 3,
		},
		{
			name: "marker and messages",
			entries: []store.Entry{
				{Seq: 1, TS: t0, Stream: "serial:a", Kind: store.KindMarker, Severity: 5, Text: "test started"},
				{Seq: 2, TS: t0, Stream: "chan:general", Kind: store.KindMessage, Severity: 6, Sender: "claude", Text: "hi all"},
				{Seq: 3, TS: t0, Stream: "dm:claude,guillaume", Kind: store.KindMessage, Severity: 6, Sender: "claude", Recipient: "guillaume", Text: "hello"},
			},
			want: "# 2026-11-16 (UTC)\n1 10:00:00.000 serial:a NOTICE -- test started --\n" +
				"2 10:00:00.000 chan:general INFO claude: hi all\n" +
				"3 10:00:00.000 dm:claude,guillaume INFO claude: @guillaume hello\n",
			shown: 3, next: 4,
		},
		{
			name: "collapse",
			entries: []store.Entry{
				line(1, t0, "s:a", 6, "boot"),
				line(2, t0, "s:a", 6, "retry"),
				line(3, t0, "s:a", 6, "retry"),
				line(4, t0, "s:a", 6, "\x1b[1mretry\x1b[0m"),
				line(5, t0, "s:a", 6, "done"),
			},
			want: "# 2026-11-16 (UTC)\n1 10:00:00.000 s:a INFO boot\n2 10:00:00.000 s:a INFO retry\n" +
				"[repeated 2 times]\n5 10:00:00.000 s:a INFO done\n",
			shown: 5, next: 6,
		},
		{
			name: "collapse at end",
			entries: []store.Entry{
				line(1, t0, "s:a", 6, "x"), line(2, t0, "s:a", 6, "x"),
			},
			want:  "# 2026-11-16 (UTC)\n1 10:00:00.000 s:a INFO x\n[repeated 1 times]\n",
			shown: 2, next: 3,
		},
		{
			name: "collapse with leading timestamps",
			entries: []store.Entry{
				line(1, t0, "s:a", 6, "[   12.345678] usb 1-1: reset"),
				line(2, t0, "s:a", 6, "[   13.000001] usb 1-1: reset"),
				line(3, t0, "s:b", 6, "2026-11-16T10:00:00Z tick"),
				line(4, t0, "s:b", 6, "2026-11-16T10:00:01.5+01:00 tick"),
				line(5, t0, "s:b", 6, "[2026-11-16 10:00:02,123] tick"),
				line(6, t0, "s:c", 6, "10:00:00.123 ping"),
				line(7, t0, "s:c", 6, "10:00:01.456 ping"),
				line(8, t0, "s:d", 6, "Nov 16 10:00:00 host app: hi"),
				line(9, t0, "s:d", 6, "Nov  6 10:00:01 host app: hi"),
			},
			want: "# 2026-11-16 (UTC)\n" +
				"1 10:00:00.000 s:a INFO [   12.345678] usb 1-1: reset\n[repeated 1 times]\n" +
				"3 10:00:00.000 s:b INFO 2026-11-16T10:00:00Z tick\n[repeated 2 times]\n" +
				"6 10:00:00.000 s:c INFO 10:00:00.123 ping\n[repeated 1 times]\n" +
				"8 10:00:00.000 s:d INFO Nov 16 10:00:00 host app: hi\n[repeated 1 times]\n",
			shown: 9, next: 10,
		},
		{
			name: "no collapse across streams, severities or different text",
			entries: []store.Entry{
				line(1, t0, "s:a", 6, "x"), line(2, t0, "s:b", 6, "x"),
				line(3, t0, "s:b", 3, "x"), line(4, t0, "s:b", 3, "y"),
			},
			want: "# 2026-11-16 (UTC)\n1 10:00:00.000 s:a INFO x\n2 10:00:00.000 s:b INFO x\n" +
				"3 10:00:00.000 s:b ERR x\n4 10:00:00.000 s:b ERR y\n",
			shown: 4, next: 5,
		},
		{
			name: "no collapse across dates",
			entries: []store.Entry{
				line(1, t0, "s:a", 6, "x"), line(2, t0.Add(24*time.Hour), "s:a", 6, "x"),
			},
			want: "# 2026-11-16 (UTC)\n1 10:00:00.000 s:a INFO x\n" +
				"# 2026-11-17 (UTC)\n2 10:00:00.000 s:a INFO x\n",
			shown: 2, next: 3,
		},
		{
			name:    "truncate runes",
			entries: []store.Entry{line(1, t0, "s:a", 6, "héllo world"), line(2, t0, "s:a", 6, "short")},
			lim:     Limits{MaxLineLen: 5},
			want:    "# 2026-11-16 (UTC)\n1 10:00:00.000 s:a INFO héllo[...truncated]\n2 10:00:00.000 s:a INFO short\n",
			shown:   2, next: 3,
		},
		{
			name:    "truncate exact length kept",
			entries: []store.Entry{line(1, t0, "s:a", 6, "héllo")},
			lim:     Limits{MaxLineLen: 5},
			want:    "# 2026-11-16 (UTC)\n1 10:00:00.000 s:a INFO héllo\n",
			shown:   1, next: 2,
		},
		{
			name: "cap lines",
			entries: []store.Entry{
				line(1, t0, "s:a", 6, "a"), line(2, t0, "s:a", 6, "b"),
				line(3, t0, "s:a", 6, "b"), line(4, t0, "s:a", 6, "c"),
			},
			lim: Limits{MaxLines: 2},
			want: "# 2026-11-16 (UTC)\n1 10:00:00.000 s:a INFO a\n2 10:00:00.000 s:a INFO b\n[repeated 1 times]\n" +
				"[more available: continue with since_seq=4]\n",
			shown: 3, next: 4, more: true,
		},
		{
			name: "cap lines exact no more",
			entries: []store.Entry{
				line(1, t0, "s:a", 6, "a"), line(2, t0, "s:a", 6, "b"),
			},
			lim:   Limits{MaxLines: 2},
			want:  "# 2026-11-16 (UTC)\n1 10:00:00.000 s:a INFO a\n2 10:00:00.000 s:a INFO b\n",
			shown: 2, next: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Format(tt.entries, tt.lim, tt.loc)
			if f.Text != tt.want {
				t.Fatalf("text:\n%s\nwant:\n%s", f.Text, tt.want)
			}
			if f.Shown != tt.shown || f.NextSeq != tt.next || f.HasMore != tt.more {
				t.Fatalf("shown=%d next=%d more=%v, want %d %d %v", f.Shown, f.NextSeq, f.HasMore, tt.shown, tt.next, tt.more)
			}
		})
	}
}

func TestFormatCapBytes(t *testing.T) {
	var entries []store.Entry
	for i := range 100 {
		entries = append(entries, line(int64(i+1), t0, "s:a", 6, fmt.Sprintf("line number %d", i)))
	}
	for _, max := range []int{60, 100, 200, 333, 1000} {
		f := Format(entries, Limits{MaxBytes: max}, nil)
		if !f.HasMore {
			t.Fatalf("max %d: HasMore false", max)
		}
		if len(f.Text) > max && f.Shown > 1 {
			t.Fatalf("max %d: %d bytes:\n%s", max, len(f.Text), f.Text)
		}
		if f.NextSeq != int64(f.Shown)+1 {
			t.Fatalf("max %d: next %d shown %d", max, f.NextSeq, f.Shown)
		}
		want := fmt.Sprintf("[more available: continue with since_seq=%d]\n", f.NextSeq)
		if !strings.HasSuffix(f.Text, want) {
			t.Fatalf("max %d: no footer:\n%s", max, f.Text)
		}
		if n := strings.Count(f.Text, "\n") - 2; n != f.Shown {
			t.Fatalf("max %d: %d entry lines, shown %d", max, n, f.Shown)
		}
		// One more entry would not have fitted.
		g := Format(entries[:f.Shown+1], Limits{MaxBytes: max}, nil)
		if g.Shown == f.Shown+1 && len(g.Text) <= max && f.Shown+1 < len(entries) {
			// Fits alone without a footer, which is fine only because
			// the footer reservation applied in f.
			withFooter := len(g.Text) + len(moreLine(g.NextSeq))
			if withFooter <= max {
				t.Fatalf("max %d: entry %d would have fitted", max, f.Shown+1)
			}
		}
	}

	// Unlimited.
	f := Format(entries, Limits{}, nil)
	if f.Shown != 100 || f.HasMore || f.NextSeq != 101 {
		t.Fatalf("unlimited: %+v", f)
	}

	// Bytes cap applies to collapsed counters too.
	same := []store.Entry{line(1, t0, "s:a", 6, "x")}
	for i := 2; i <= 20; i++ {
		same = append(same, line(int64(i), t0, "s:a", 6, "x"))
	}
	full := Format(same, Limits{}, nil)
	if full.Shown != 20 || !strings.Contains(full.Text, "[repeated 19 times]") {
		t.Fatalf("collapse: %+v", full)
	}

	// The last entry needs no footer room.
	two := []store.Entry{line(1, t0, "s:a", 6, "a"), line(2, t0, "s:a", 6, "b")}
	exact := Format(two, Limits{}, nil)
	f = Format(two, Limits{MaxBytes: len(exact.Text)}, nil)
	if f.HasMore || f.Shown != 2 {
		t.Fatalf("exact fit: %+v", f)
	}
	f = Format(two, Limits{MaxBytes: len(exact.Text) - 1}, nil)
	if !f.HasMore || f.Shown != 1 || f.NextSeq != 2 {
		t.Fatalf("one byte short: %+v", f)
	}

	// The first entry is always shown so callers make progress.
	f = Format(two, Limits{MaxBytes: 5}, nil)
	if f.Shown != 1 || !f.HasMore || f.NextSeq != 2 {
		t.Fatalf("tiny cap: %+v", f)
	}
}

// normalise is what the Splitter should produce for in: UTF-8 repaired
// byte by byte and every line ending turned into \n.
func normalise(in []byte) string {
	var b strings.Builder
	for i := 0; i < len(in); {
		r, size := utf8.DecodeRune(in[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(utf8.RuneError)
		} else {
			b.Write(in[i : i+size])
		}
		i += size
	}
	s := strings.ReplaceAll(b.String(), "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

func FuzzSplitter(f *testing.F) {
	f.Add([]byte("a\r\nb\rc\n\n"), uint8(3), uint8(0))
	f.Add([]byte("h\xc3\xa9\xe2\x82\xac\xff\r"), uint8(1), uint8(5))
	f.Add([]byte("\r\r\n\n\r"), uint8(2), uint8(8))
	f.Add([]byte("abcdefghij€€😀\n"), uint8(7), uint8(4))
	f.Fuzz(func(t *testing.T, in []byte, chunk uint8, maxLine uint8) {
		if len(in) > 4096 {
			return
		}
		ml := 0 // no long lines: inputs are far below MaxLineBytes
		if maxLine >= 4 {
			ml = int(maxLine)
		}
		var lines []string
		s := NewSplitter(DefaultFlushAfter, ml, func(l string, _ time.Time) {
			if !utf8.ValidString(l) {
				t.Fatalf("invalid UTF-8 line %q", l)
			}
			lines = append(lines, l)
		})
		step := int(chunk%16) + 1
		for i := 0; i < len(in); i += step {
			s.Write(in[i:min(i+step, len(in))], t0)
		}
		s.Close()

		var b strings.Builder
		for i, l := range lines {
			if ml > 0 && len(l) > ml {
				// Only a continued piece is longer than maxLine.
				body, ok := strings.CutSuffix(l, ContinuationMarker)
				if !ok || len(body) > ml {
					t.Fatalf("long piece %q (max %d)", l, ml)
				}
				b.WriteString(body)
				continue
			}
			b.WriteString(l)
			if i < len(lines)-1 {
				b.WriteByte('\n')
			}
		}
		want := strings.TrimSuffix(normalise(in), "\n")
		if got := b.String(); got != want {
			t.Fatalf("in %q chunk %d max %d:\ngot  %q\nwant %q (lines %q)", in, step, ml, got, want, lines)
		}
	})
}

func FuzzStripANSI(f *testing.F) {
	for _, s := range []string{
		"\x1b[1;31mred\x1b[0m", "\x1b]0;t\x07x", "\x1b]2;t\x1b\\y", "a\tb\x00\r\n",
		"\x1b", "\x1b[", "\x1b]", "\u009b1m\u009d0;x\u009c", "\xff\xfe", "\x1bP\x1b\x1b\\",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := StripANSI(in)
		for _, r := range out {
			if r == 0x1b || (unicode.IsControl(r) && r != '\t') {
				t.Fatalf("StripANSI(%q) = %q has control %U", in, out, r)
			}
		}
		if !utf8.ValidString(out) {
			t.Fatalf("StripANSI(%q) = %q is not valid UTF-8", in, out)
		}
		if StripANSI(out) != out {
			t.Fatalf("StripANSI not idempotent on %q: %q", in, out)
		}
	})
}
