package syslog

import (
	"bytes"
	"strconv"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/store"
)

// Format is how a message was understood.
type Format string

const (
	FormatRFC5424 Format = "rfc5424"
	FormatRFC3164 Format = "rfc3164"
	// FormatRaw is a line with no valid PRI: kept whole, severity 6.
	FormatRaw Format = "raw"
)

// Message is one parsed syslog message. Parsing never fails: what cannot
// be read stays in Text.
type Message struct {
	Format   Format
	Facility int // -1 when there was no PRI
	Severity int
	Time     *time.Time
	Hostname string
	AppName  string
	ProcID   string
	MsgID    string
	// SD is RFC 5424 structured data: SD-ID to its params.
	SD   map[string]map[string]string
	Text []byte
}

var facilityNames = []string{
	"kern", "user", "mail", "daemon", "auth", "syslog", "lpr", "news",
	"uucp", "cron", "authpriv", "ftp", "ntp", "security", "console", "solaris-cron",
	"local0", "local1", "local2", "local3", "local4", "local5", "local6", "local7",
}

// FacilityName is the usual keyword for a facility number.
func FacilityName(f int) string {
	if f < 0 || f >= len(facilityNames) {
		return ""
	}
	return facilityNames[f]
}

// Parse reads an RFC 5424 or RFC 3164 message, best effort. now and loc
// resolve RFC 3164 timestamps, which have neither year nor zone.
func Parse(b []byte, now time.Time, loc *time.Location) Message {
	raw := Message{Format: FormatRaw, Facility: -1, Severity: store.DefaultSeverity, Text: b}
	pri, rest, ok := parsePRI(b)
	if !ok {
		return raw
	}
	m := Message{Facility: pri >> 3, Severity: pri & 7}
	if r, ok := parse5424(rest, &m); ok {
		m.Format = FormatRFC5424
		m.Text = r
		return m
	}
	m = Message{Facility: pri >> 3, Severity: pri & 7, Format: FormatRFC3164}
	m.Text = parse3164(rest, &m, now, loc)
	return m
}

func parsePRI(b []byte) (int, []byte, bool) {
	if len(b) < 3 || b[0] != '<' {
		return 0, nil, false
	}
	end := bytes.IndexByte(b[:min(len(b), 5)], '>')
	if end < 2 {
		return 0, nil, false
	}
	digits := b[1:end]
	if len(digits) > 1 && digits[0] == '0' {
		return 0, nil, false
	}
	n := 0
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, nil, false
		}
		n = n*10 + int(c-'0')
	}
	if n > 191 {
		return 0, nil, false
	}
	return n, b[end+1:], true
}

// nextField cuts b at the first space.
func nextField(b []byte) (field, rest []byte, ok bool) {
	i := bytes.IndexByte(b, ' ')
	if i < 0 {
		return b, nil, false
	}
	return b[:i], b[i+1:], true
}

func nilValue(b []byte) string {
	if len(b) == 1 && b[0] == '-' {
		return ""
	}
	return string(b)
}

// parse5424 reads VERSION SP TIMESTAMP SP HOSTNAME SP APP-NAME SP PROCID
// SP MSGID SP STRUCTURED-DATA [SP MSG] and returns MSG.
func parse5424(b []byte, m *Message) ([]byte, bool) {
	ver, rest, ok := nextField(b)
	if !ok || len(ver) == 0 || len(ver) > 2 || ver[0] < '1' || ver[0] > '9' {
		return nil, false
	}
	for _, c := range ver {
		if c < '0' || c > '9' {
			return nil, false
		}
	}
	var f [5][]byte
	for i := range f {
		if f[i], rest, ok = nextField(rest); !ok || len(f[i]) == 0 {
			return nil, false
		}
	}
	if ts := nilValue(f[0]); ts != "" {
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			return nil, false
		}
		t = t.UTC()
		m.Time = &t
	}
	m.Hostname, m.AppName, m.ProcID, m.MsgID = nilValue(f[1]), nilValue(f[2]), nilValue(f[3]), nilValue(f[4])
	sd, msg, ok := parseSD(rest)
	if !ok {
		return nil, false
	}
	m.SD = sd
	msg = bytes.TrimPrefix(msg, []byte("\xef\xbb\xbf"))
	return msg, true
}

// parseSD reads "-" or one or more [id k="v" ...] elements, then an
// optional SP and MSG.
func parseSD(b []byte) (map[string]map[string]string, []byte, bool) {
	if len(b) == 0 {
		return nil, nil, false
	}
	if b[0] == '-' {
		if len(b) == 1 {
			return nil, nil, true
		}
		if b[1] != ' ' {
			return nil, nil, false
		}
		return nil, b[2:], true
	}
	sd := map[string]map[string]string{}
	i := 0
	for i < len(b) && b[i] == '[' {
		i++
		start := i
		for i < len(b) && b[i] != ' ' && b[i] != ']' {
			i++
		}
		if i == start || i == len(b) {
			return nil, nil, false
		}
		id := string(b[start:i])
		params := sd[id]
		if params == nil {
			params = map[string]string{}
			sd[id] = params
		}
		for i < len(b) && b[i] == ' ' {
			i++
			ns := i
			for i < len(b) && b[i] != '=' && b[i] != ' ' && b[i] != ']' {
				i++
			}
			if i == ns || i+1 >= len(b) || b[i] != '=' || b[i+1] != '"' {
				return nil, nil, false
			}
			name := string(b[ns:i])
			i += 2
			var val []byte
			for {
				if i >= len(b) {
					return nil, nil, false
				}
				c := b[i]
				if c == '\\' && i+1 < len(b) && (b[i+1] == '"' || b[i+1] == '\\' || b[i+1] == ']') {
					val = append(val, b[i+1])
					i += 2
					continue
				}
				if c == '"' {
					i++
					break
				}
				val = append(val, c)
				i++
			}
			params[name] = string(val)
		}
		if i >= len(b) || b[i] != ']' {
			return nil, nil, false
		}
		i++
	}
	if i == 0 {
		return nil, nil, false
	}
	if i == len(b) {
		return sd, nil, true
	}
	if b[i] != ' ' {
		return nil, nil, false
	}
	return sd, b[i+1:], true
}

// parse3164 reads [TIMESTAMP SP] [HOSTNAME SP] [TAG[PID]: ]MSG and
// returns MSG. Senders disagree on every part, so each one is optional.
func parse3164(b []byte, m *Message, now time.Time, loc *time.Location) []byte {
	rest := b
	if t, r, ok := parseStamp(rest, now, loc); ok {
		m.Time = &t
		rest = r
		// Only a message with a timestamp is trusted to name its host:
		// "<13>hello world" has none.
		if host, r2, ok := nextField(rest); ok && len(host) > 0 && !looksLikeTag(host) && isHostname(host) {
			m.Hostname = string(host)
			rest = r2
		}
	}
	if app, pid, r, ok := parseTag(rest); ok {
		m.AppName, m.ProcID = app, pid
		rest = r
	}
	return rest
}

var months = map[string]time.Month{
	"Jan": 1, "Feb": 2, "Mar": 3, "Apr": 4, "May": 5, "Jun": 6,
	"Jul": 7, "Aug": 8, "Sep": 9, "Oct": 10, "Nov": 11, "Dec": 12,
}

// parseStamp reads "Mmm dd hh:mm:ss" (RFC 3164, with an optional
// ".frac" and an optional year after it, as some devices send) or an
// RFC 3339 time (rsyslog's high-precision format), then a space.
func parseStamp(b []byte, now time.Time, loc *time.Location) (time.Time, []byte, bool) {
	if f, rest, ok := nextField(b); ok && len(f) >= 19 && f[4] == '-' && f[10] == 'T' {
		if t, err := time.Parse(time.RFC3339Nano, string(f)); err == nil {
			return t.UTC(), rest, true
		}
	}
	if len(b) < 15 {
		return time.Time{}, nil, false
	}
	mon, ok := months[string(b[0:3])]
	if !ok || b[3] != ' ' {
		return time.Time{}, nil, false
	}
	day, err := strconv.Atoi(strings.TrimLeft(string(b[4:6]), " "))
	if err != nil || day < 1 || day > 31 || b[6] != ' ' {
		return time.Time{}, nil, false
	}
	clock := string(b[7:15])
	if clock[2] != ':' || clock[5] != ':' {
		return time.Time{}, nil, false
	}
	hh, e1 := strconv.Atoi(clock[0:2])
	mm, e2 := strconv.Atoi(clock[3:5])
	ss, e3 := strconv.Atoi(clock[6:8])
	if e1 != nil || e2 != nil || e3 != nil || hh > 23 || mm > 59 || ss > 60 {
		return time.Time{}, nil, false
	}
	rest := b[15:]
	nsec := 0
	if len(rest) > 1 && rest[0] == '.' {
		j := 1
		for j < len(rest) && j <= 9 && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j > 1 {
			frac := string(rest[1:j]) + strings.Repeat("0", 10-j)
			nsec, _ = strconv.Atoi(frac[:9])
			rest = rest[j:]
		}
	}
	year := 0
	if len(rest) >= 5 && rest[0] == ' ' && (len(rest) == 5 || rest[5] == ' ') {
		if y, err := strconv.Atoi(string(rest[1:5])); err == nil && y >= 1970 && y <= 9999 {
			year = y
			rest = rest[5:]
		}
	}
	if len(rest) > 0 {
		if rest[0] != ' ' {
			return time.Time{}, nil, false
		}
		rest = rest[1:]
	}
	if loc == nil {
		loc = time.Local
	}
	nowLoc := now.In(loc)
	y := year
	if y == 0 {
		y = nowLoc.Year()
	}
	t := time.Date(y, mon, day, hh, mm, ss, nsec, loc)
	// No year: a December line read in January belongs to last year.
	if year == 0 && t.Sub(nowLoc) > 48*time.Hour {
		t = t.AddDate(-1, 0, 0)
	}
	return t.UTC(), rest, true
}

func looksLikeTag(f []byte) bool {
	return bytes.HasSuffix(f, []byte(":")) || bytes.IndexByte(f, '[') >= 0
}

func isHostname(f []byte) bool {
	for _, c := range f {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '-', c == '_', c == ':', c == '%':
		default:
			return false
		}
	}
	return true
}

// parseTag reads "name[pid]: " or "name: " at the start of b.
func parseTag(b []byte) (app, pid string, rest []byte, ok bool) {
	i := 0
	for i < len(b) && i < 64 {
		c := b[i]
		if c == ':' || c == '[' || c == ' ' {
			break
		}
		if c < '!' || c > '~' {
			return "", "", nil, false
		}
		i++
	}
	if i == 0 || i >= len(b) {
		return "", "", nil, false
	}
	app = string(b[:i])
	if b[i] == '[' {
		end := bytes.IndexByte(b[i:], ']')
		if end < 0 || end > 33 {
			return "", "", nil, false
		}
		pid = string(b[i+1 : i+end])
		i += end + 1
	}
	if i >= len(b) || b[i] != ':' {
		return "", "", nil, false
	}
	i++
	if i < len(b) && b[i] == ' ' {
		i++
	}
	return app, pid, b[i:], true
}
