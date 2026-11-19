// Package store owns the SQLite database: the handle, the migrations and
// the entry queries. Only the writer inserts or deletes entries.
package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Kind is what an entry is.
type Kind string

const (
	KindLine    Kind = "line"
	KindMarker  Kind = "marker"
	KindMessage Kind = "message"
)

// Valid reports whether k is one of the three kinds.
func (k Kind) Valid() bool {
	return k == KindLine || k == KindMarker || k == KindMessage
}

// DefaultSeverity is syslog "informational".
const DefaultSeverity = 6

// MaxMetaBytes caps an entry's meta JSON.
const MaxMetaBytes = 4 << 10

// Entry is one item in the timeline.
type Entry struct {
	Seq         int64 // 0 until committed
	TS          time.Time
	SrcTS       *time.Time
	Stream      string
	Kind        Kind
	Severity    int // 0..7; 0 is emergency, so producers set DefaultSeverity explicitly
	Host, App   string
	Text        string
	Attachments []Attachment
	Sender      string
	Recipient   string
	ReplyTo     int64
	Meta        json.RawMessage
}

// IsMessageStream reports whether a stream holds messages (chan:*, dm:*).
// Log tools never return entries from these streams.
func IsMessageStream(stream string) bool {
	return strings.HasPrefix(stream, "chan:") || strings.HasPrefix(stream, "dm:")
}

// ProjectOf is the project a stream belongs to: "lab" for serial:lab/dut1
// and chan:lab/x; "" for a direct conversation and an id without one.
func ProjectOf(stream string) string {
	kind, rest, ok := strings.Cut(stream, ":")
	if !ok || kind == "dm" {
		return ""
	}
	project, _, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	return project
}

// LocalName is what follows the project in a stream id: the sink of
// serial:lab/dut1, the channel of chan:lab/x; "" without a project.
func LocalName(stream string) string {
	if ProjectOf(stream) == "" {
		return ""
	}
	_, rest, _ := strings.Cut(stream, "/")
	return rest
}

// Attachment is one typed attachment of a message, stored as JSON in
// entries.attachments. Type is "image" or "log".
type Attachment struct {
	Type string `json:"type"`

	// image
	SHA256 string `json:"sha256,omitempty"`
	Mime   string `json:"mime,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`

	// log
	Stream         string   `json:"stream,omitempty"`
	Channel        string   `json:"channel,omitempty"`
	Streams        []string `json:"streams,omitempty"`
	FromSeq        int64    `json:"from_seq,omitempty"`
	ToSeq          *int64   `json:"to_seq,omitempty"`
	Grep           string   `json:"grep,omitempty"`
	MinSeverity    *int     `json:"min_severity,omitempty"`
	Title          string   `json:"title,omitempty"`
	SnapshotSHA256 string   `json:"snapshot_sha256,omitempty"`
	Lines          int      `json:"lines,omitempty"`
	FirstTS        int64    `json:"first_ts,omitempty"` // UTC ns
	LastTS         int64    `json:"last_ts,omitempty"`
}

// ValidateStreamID checks the kind:name shape every stream id has.
func ValidateStreamID(id string) error {
	kind, name, ok := strings.Cut(id, ":")
	if !ok || kind == "" || name == "" {
		return fmt.Errorf("stream %q is not kind:name", id)
	}
	if len(id) > 200 {
		return fmt.Errorf("stream %q is longer than 200 bytes", id)
	}
	for _, r := range kind {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return fmt.Errorf("stream %q: kind must be lowercase letters and digits", id)
		}
	}
	for _, r := range name {
		if r <= ' ' || r == 0x7f || r == '*' || r == '?' || r == '[' || r == '\\' {
			return fmt.Errorf("stream %q: name has a space, control or glob character", id)
		}
	}
	return nil
}

// StreamKind is the part of a stream id before the colon.
func StreamKind(id string) string {
	kind, _, _ := strings.Cut(id, ":")
	return kind
}

var severityNames = map[string]int{
	"emerg": 0, "emergency": 0, "panic": 0, "fatal": 2,
	"alert": 1,
	"crit":  2, "critical": 2,
	"err": 3, "error": 3,
	"warn": 4, "warning": 4,
	"notice": 5,
	"info":   6, "informational": 6,
	"debug": 7, "trace": 7,
}

// ParseSeverity reads a syslog severity: a digit 0 to 7 or a name such as
// "err", "warning" or "info" (any case).
func ParseSeverity(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) == 1 && s[0] >= '0' && s[0] <= '7' {
		return int(s[0] - '0'), nil
	}
	if n, ok := severityNames[s]; ok {
		return n, nil
	}
	return 0, fmt.Errorf("severity %q is not 0 to 7 or a name like err, warning, info", s)
}
