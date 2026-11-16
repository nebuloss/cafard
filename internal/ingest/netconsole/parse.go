package netconsole

import (
	"bytes"
	"strconv"
	"strings"
)

// Record is one extended netconsole message:
// "<level>,<seq>,<timestamp>,<flags>[,field...];<message>\n" then
// dictionary lines starting with a space (" SUBSYSTEM=net").
type Record struct {
	Level    int // syslog severity, the low 3 bits of the prefix
	Facility int // the rest of the prefix; 0 for the kernel
	Seq      uint64
	TSUsec   uint64 // microseconds since boot
	Flags    string // "-" or "c" (continuation, old kernels)
	Fields   map[string]string
	Frag     *Fragment
	// Body is everything after ';', still unsplit.
	Body []byte
}

// Fragment is the ncfrag=<offset>/<total> header field of a message the
// kernel split over several datagrams.
type Fragment struct {
	Offset, Total int
}

// ParseExtended reads an extended header. ok is false for a plain
// netconsole datagram.
func ParseExtended(b []byte) (Record, bool) {
	semi := bytes.IndexByte(b, ';')
	if semi < 7 || semi > 256 {
		return Record{}, false
	}
	head := string(b[:semi])
	if strings.ContainsAny(head, "\n\r ") {
		return Record{}, false
	}
	parts := strings.Split(head, ",")
	if len(parts) < 4 {
		return Record{}, false
	}
	prefix, err1 := strconv.Atoi(parts[0])
	seq, err2 := strconv.ParseUint(parts[1], 10, 64)
	ts, err3 := strconv.ParseUint(parts[2], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || prefix < 0 || prefix > 1<<20 || parts[3] == "" {
		return Record{}, false
	}
	r := Record{Level: prefix & 7, Facility: prefix >> 3, Seq: seq, TSUsec: ts, Flags: parts[3], Body: b[semi+1:]}
	for _, f := range parts[4:] {
		k, v, _ := strings.Cut(f, "=")
		if k == "ncfrag" {
			off, total, ok := strings.Cut(v, "/")
			o, e1 := strconv.Atoi(off)
			t, e2 := strconv.Atoi(total)
			if ok && e1 == nil && e2 == nil && o >= 0 && t > 0 && o < t && t <= maxMessage {
				r.Frag = &Fragment{Offset: o, Total: t}
			}
			continue
		}
		if k == "" {
			continue
		}
		if r.Fields == nil {
			r.Fields = map[string]string{}
		}
		r.Fields[k] = v
	}
	return r, true
}

// maxMessage bounds a reassembled message.
const maxMessage = 64 << 10

// SplitBody separates the message text from the dictionary lines
// (continuation lines that start with a space and hold KEY=value).
func SplitBody(body []byte) (text []byte, dict map[string]string) {
	lines := bytes.Split(body, []byte("\n"))
	var out [][]byte
	for i, l := range lines {
		if i > 0 && len(l) > 1 && l[0] == ' ' {
			if k, v, ok := strings.Cut(string(l[1:]), "="); ok && k != "" {
				if dict == nil {
					dict = map[string]string{}
				}
				dict[k] = v
				continue
			}
		}
		out = append(out, l)
	}
	return bytes.Join(out, []byte("\n")), dict
}
