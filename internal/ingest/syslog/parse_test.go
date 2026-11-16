package syslog

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func ts(s string) *time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	t = t.UTC()
	return &t
}

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Message
	}{
		{"rfc5424 full", `<34>1 2003-10-11T22:14:15.003Z mymachine.example.com su - ID47 - 'su root' failed for lonvick on /dev/pts/8`,
			Message{Format: FormatRFC5424, Facility: 4, Severity: 2, Time: ts("2003-10-11T22:14:15.003Z"), Hostname: "mymachine.example.com",
				AppName: "su", MsgID: "ID47", Text: []byte("'su root' failed for lonvick on /dev/pts/8")}},
		{"rfc5424 structured data and BOM", "<165>1 2003-08-24T05:14:15.000003-07:00 192.0.2.1 myproc 8710 - [exampleSDID@32473 iut=\"3\" eventSource=\"Application\"][x@1 a=\"q\\\"uo\\]te\"] \xef\xbb\xbfAn application event",
			Message{Format: FormatRFC5424, Facility: 20, Severity: 5, Time: ts("2003-08-24T12:14:15.000003Z"), Hostname: "192.0.2.1",
				AppName: "myproc", ProcID: "8710",
				SD:   map[string]map[string]string{"exampleSDID@32473": {"iut": "3", "eventSource": "Application"}, "x@1": {"a": `q"uo]te`}},
				Text: []byte("An application event")}},
		{"rfc5424 all nil, no msg", `<13>1 - - - - - -`,
			Message{Format: FormatRFC5424, Facility: 1, Severity: 5}},
		{"rfc5424 sd without msg", `<13>1 - host app - - [a b="c"]`,
			Message{Format: FormatRFC5424, Facility: 1, Severity: 5, Hostname: "host", AppName: "app", SD: map[string]map[string]string{"a": {"b": "c"}}}},
		{"rfc3164 classic", `<34>Oct 11 22:14:15 mymachine su: 'su root' failed`,
			Message{Format: FormatRFC3164, Facility: 4, Severity: 2, Time: ts("2026-10-11T22:14:15Z"), Hostname: "mymachine", AppName: "su", Text: []byte("'su root' failed")}},
		{"rfc3164 pid and padded day", `<13>Nov  6 09:00:00 dut1 sshd[1234]: Accepted key`,
			Message{Format: FormatRFC3164, Facility: 1, Severity: 5, Time: ts("2026-11-06T09:00:00Z"), Hostname: "dut1", AppName: "sshd", ProcID: "1234", Text: []byte("Accepted key")}},
		{"rfc3164 no hostname (local style)", `<30>Nov 16 10:00:00 systemd[1]: Started thing.`,
			Message{Format: FormatRFC3164, Facility: 3, Severity: 6, Time: ts("2026-11-16T10:00:00Z"), AppName: "systemd", ProcID: "1", Text: []byte("Started thing.")}},
		{"rfc3164 last year's december", `<14>Dec 31 23:59:59 h app: x`,
			Message{Format: FormatRFC3164, Facility: 1, Severity: 6, Time: ts("2025-12-31T23:59:59Z"), Hostname: "h", AppName: "app", Text: []byte("x")}},
		{"rfc3164 fraction and year", `<14>Nov 16 10:00:00.250 2026 h app: x`,
			Message{Format: FormatRFC3164, Facility: 1, Severity: 6, Time: ts("2026-11-16T10:00:00.25Z"), Hostname: "h", AppName: "app", Text: []byte("x")}},
		{"rfc3164 with rfc3339 stamp", `<14>2026-11-16T10:00:00.5+01:00 h app: x`,
			Message{Format: FormatRFC3164, Facility: 1, Severity: 6, Time: ts("2026-11-16T09:00:00.5Z"), Hostname: "h", AppName: "app", Text: []byte("x")}},
		{"pri only then text", `<11>hello world`,
			Message{Format: FormatRFC3164, Facility: 1, Severity: 3, Text: []byte("hello world")}},
		{"pri and tag, no time", `<11>app: hello`,
			Message{Format: FormatRFC3164, Facility: 1, Severity: 3, AppName: "app", Text: []byte("hello")}},
		{"no pri", `just a line`, Message{Format: FormatRaw, Facility: -1, Severity: 6, Text: []byte("just a line")}},
		{"pri too big", `<192>x`, Message{Format: FormatRaw, Facility: -1, Severity: 6, Text: []byte("<192>x")}},
		{"pri leading zero", `<013>x`, Message{Format: FormatRaw, Facility: -1, Severity: 6, Text: []byte("<013>x")}},
		{"pri unterminated", `<13 x`, Message{Format: FormatRaw, Facility: -1, Severity: 6, Text: []byte("<13 x")}},
		{"empty", ``, Message{Format: FormatRaw, Facility: -1, Severity: 6, Text: []byte("")}},
		{"5424 bad time falls back to 3164", `<13>1 yesterday host app - - - msg`,
			Message{Format: FormatRFC3164, Facility: 1, Severity: 5, Text: []byte("1 yesterday host app - - - msg")}},
		{"5424 broken sd falls back to 3164", `<13>1 - host app - - [a b=c] msg`,
			Message{Format: FormatRFC3164, Facility: 1, Severity: 5, Text: []byte("1 - host app - - [a b=c] msg")}},
		{"3164 bad clock", `<13>Nov 16 25:00:00 h app: x`,
			Message{Format: FormatRFC3164, Facility: 1, Severity: 5, Text: []byte("Nov 16 25:00:00 h app: x")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse([]byte(tt.in), now, time.UTC)
			if len(got.Text) == 0 {
				got.Text = nil
			}
			if len(tt.want.Text) == 0 {
				tt.want.Text = nil
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q)\n got %+v\nwant %+v", tt.in, got, tt.want)
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{
		`<34>1 2003-10-11T22:14:15.003Z mymachine.example.com su - ID47 - 'su root' failed`,
		`<165>1 2003-08-24T05:14:15.000003-07:00 192.0.2.1 myproc 8710 - [exampleSDID@32473 iut="3"] msg`,
		`<13>Nov  6 09:00:00 dut1 sshd[1234]: Accepted key`,
		`<14>Nov 16 10:00:00.250 2026 h app: x`, `<11>hello`, `x`, `<1`, `<13>1 - - - - - [a b="\`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		m := Parse(b, now, time.UTC)
		if m.Severity < 0 || m.Severity > 7 {
			t.Fatalf("severity %d", m.Severity)
		}
		if m.Format == FormatRaw && string(m.Text) != string(b) {
			t.Fatal("raw text must be the whole input")
		}
		for _, e := range MessageEntries(m, testIP, now) {
			if strings.ContainsAny(e.Text, "\n\r") {
				t.Fatalf("entry text %q has a line break", e.Text)
			}
		}
	})
}
