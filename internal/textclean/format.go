package textclean

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/store"
)

// Limits caps the agent-facing text. Zero or negative means unlimited.
type Limits struct{ MaxLines, MaxBytes, MaxLineLen int }

// AgentDefaults are the PLAN section 7 output rules.
var AgentDefaults = Limits{400, 60 << 10, 2000}

// Formatted is the agent-facing rendering of a run of entries.
type Formatted struct {
	Text    string // header line(s) + one line per entry, PLAN section 7 format
	Shown   int    // entries represented, collapsed repeats included
	NextSeq int64  // seq of the last represented entry + 1; 0 when nothing is shown
	HasMore bool   // entries were left out because of the cap
}

// TruncatedMarker ends a text cut at MaxLineLen runes.
const TruncatedMarker = "[...truncated]"

// NewlineMarker replaces a newline inside an entry's text, so one entry
// stays one output line.
const NewlineMarker = " ⏎ "

var sevNames = [...]string{"EMERG", "ALERT", "CRIT", "ERR", "WARN", "NOTICE", "INFO", "DEBUG"}

// SeverityName is the short name of a syslog severity, or its number when
// out of range.
func SeverityName(sev int) string {
	if sev >= 0 && sev < len(sevNames) {
		return sevNames[sev]
	}
	return strconv.Itoa(sev)
}

// leadingTS matches the timestamp producers often put at the start of a
// line, so repeated lines that differ only by it still collapse.
var leadingTS = regexp.MustCompile(`^(?:` +
	`\[\s*\d+\.\d+\]` + // kernel: [   12.345678]
	`|\[?\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?\]?` + // RFC 3339 / ISO
	`|\[?\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\]?` + // 10:00:00.123
	`|[A-Z][a-z]{2} [ \d]\d \d{2}:\d{2}:\d{2}` + // syslog: Nov 16 10:00:00
	`)\s*`)

// Format renders entries for agents (PLAN section 7): one line per entry,
// `<seq> <HH:MM:SS.mmm> <stream> <SEV> <text>`, a `# <date> (<loc>)`
// header before the first entry and on every date change, ANSI stripped,
// newlines shown as NewlineMarker, repeats collapsed, long texts
// truncated, and the output capped by lim. loc nil means UTC.
//
// The first entry is always shown, even past MaxBytes, so a caller that
// continues from NextSeq always makes progress.
func Format(entries []store.Entry, lim Limits, loc *time.Location) Formatted {
	if loc == nil {
		loc = time.UTC
	}
	var (
		out   strings.Builder
		res   Formatted
		lines int
		date  string

		// The open collapse group: its rendered line and key, and how
		// many extra copies it holds.
		groupLine string
		groupKey  string
		groupDate string
		extra     int
	)
	groupCost := func(extra int) int {
		n := len(groupLine)
		if extra > 0 {
			n += len(repeatedLine(extra))
		}
		return n
	}
	flush := func() {
		out.WriteString(groupLine)
		if extra > 0 {
			out.WriteString(repeatedLine(extra))
		}
		groupLine, groupKey, extra = "", "", 0
	}
	fits := func(total int, i int) bool {
		if lim.MaxBytes <= 0 || res.Shown == 0 {
			return true
		}
		if i < len(entries)-1 {
			// Leave room for the footer in case this is the last one shown.
			total += len(moreLine(entries[i].Seq + 1))
		}
		return total <= lim.MaxBytes
	}

	for i := range entries {
		e := &entries[i]
		ts := e.TS.In(loc)
		day := ts.Format(time.DateOnly)
		text := entryText(e)
		key := e.Stream + "\x00" + strconv.Itoa(e.Severity) + "\x00" + leadingTS.ReplaceAllString(text, "")

		if groupLine != "" && key == groupKey && day == groupDate {
			total := out.Len() + groupCost(extra+1)
			if !fits(total, i) {
				res.HasMore = true
				break
			}
			extra++
			res.Shown++
			res.NextSeq = e.Seq + 1
			continue
		}

		header := ""
		if day != date {
			header = "# " + day + " (" + loc.String() + ")\n"
		}
		line := strconv.FormatInt(e.Seq, 10) + " " + ts.Format("15:04:05.000") + " " +
			e.Stream + " " + SeverityName(e.Severity) + " " + truncate(text, lim.MaxLineLen) + "\n"
		total := out.Len() + groupCost(extra) + len(header) + len(line)
		if res.Shown > 0 && lim.MaxLines > 0 && lines+1 > lim.MaxLines || !fits(total, i) {
			res.HasMore = true
			break
		}
		flush()
		out.WriteString(header)
		date = day
		groupLine, groupKey, groupDate = line, key, day
		lines++
		res.Shown++
		res.NextSeq = e.Seq + 1
	}
	flush()
	if res.HasMore {
		out.WriteString(moreLine(res.NextSeq))
	}
	res.Text = out.String()
	return res
}

func repeatedLine(n int) string {
	return "[repeated " + strconv.Itoa(n) + " times]\n"
}

func moreLine(next int64) string {
	return "[more available: continue with since_seq=" + strconv.FormatInt(next, 10) + "]\n"
}

// entryText is the cleaned text of an entry, decorated by kind.
func entryText(e *store.Entry) string {
	text := cleanText(e.Text)
	switch e.Kind {
	case store.KindMarker:
		return "-- " + text + " --"
	case store.KindMessage:
		if e.Recipient != "" {
			text = "@" + e.Recipient + " " + text
		}
		if e.Sender != "" {
			text = e.Sender + ": " + text
		}
	}
	return text
}

// cleanText strips ANSI and control characters and joins the lines of a
// multi-line text with NewlineMarker. Lines are stripped one by one so an
// unterminated escape cannot swallow the following lines.
func cleanText(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if !strings.ContainsAny(s, "\r\n") {
		return StripANSI(s)
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	parts := strings.Split(s, "\n")
	for i, p := range parts {
		parts[i] = StripANSI(p)
	}
	return strings.Join(parts, NewlineMarker)
}

// truncate cuts s at max runes and appends TruncatedMarker.
func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i] + TruncatedMarker
		}
		n++
	}
	return s
}
