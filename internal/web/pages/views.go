package pages

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/web/view"
)

// The data of each partial with a twin. Zero values render the twin.

// LineView is partials/line.html.
type LineView struct {
	Kind      string // "" for a log line, "message" in Live with kinds=message
	Seq       int64
	Sev       int
	Tone      int
	ISO, Time string
	Stream    string
	SevLetter string
	SevName   string
	Segments  []view.Segment
}

// MarkerView is partials/marker.html.
type MarkerView struct {
	Seq       int64
	ISO, Time string
	Text      string
}

// StreamDotView is partials/stream_dot.html.
type StreamDotView struct {
	Status string
	Title  string
}

// StreamCardView is partials/stream_card.html.
type StreamCardView struct {
	ID, Status, Transport, Rate, Last, Preview, Dropped, Claim, LineState, Open string
}

// LogItem is a line or a marker of a logs view.
type LogItem struct {
	Line   LineView
	Marker *MarkerView
}

// LogTime is the time column: UTC with milliseconds. The bundle shows the
// same (src/render/format.ts), so server and live rows agree.
func LogTime(t time.Time) string { return t.UTC().Format("15:04:05.000") }

// Line is the view of a line entry.
func Line(e store.Entry) LineView {
	return LineView{
		Seq: e.Seq, Sev: e.Severity, Tone: view.Tone(e.Stream),
		ISO: e.TS.UTC().Format(time.RFC3339Nano), Time: LogTime(e.TS),
		Stream: e.Stream, SevLetter: view.SeverityLetter(e.Severity), SevName: view.SeverityName(e.Severity),
		Segments: view.ANSI(e.Text),
	}
}

// MessageLine is a message as a row of the Live page: the chat in the
// stream column (from the viewer's side), the sender before the text.
func MessageLine(e store.Entry, viewer string) LineView {
	chat := messaging.ChatID(viewer, e.Stream)
	// Line by line, so a multi-line message keeps its lines, as in the
	// bundle (white-space: pre-wrap).
	lines := strings.Split(strings.ReplaceAll(strings.TrimRight(e.Text, "\r\n"), "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = view.StripText(l)
	}
	text := strings.Join(lines, "\n")
	if e.Sender != "" {
		text = e.Sender + ": " + text
	}
	return LineView{
		Kind: string(store.KindMessage), Seq: e.Seq, Sev: e.Severity, Tone: view.Tone(chat),
		ISO: e.TS.UTC().Format(time.RFC3339Nano), Time: LogTime(e.TS),
		Stream: ChatLabel(chat), SevLetter: "»", SevName: "message",
		Segments: []view.Segment{{Text: text}},
	}
}

// Marker is the view of a marker entry.
func Marker(e store.Entry) MarkerView {
	return MarkerView{Seq: e.Seq, ISO: e.TS.UTC().Format(time.RFC3339Nano), Time: LogTime(e.TS), Text: e.Stream + ": " + view.StripText(e.Text)}
}

// LogItems converts entries.
func LogItems(es []store.Entry) []LogItem {
	out := make([]LogItem, 0, len(es))
	for _, e := range es {
		if e.Kind == store.KindMarker {
			m := Marker(e)
			out = append(out, LogItem{Marker: &m})
		} else {
			out = append(out, LogItem{Line: Line(e)})
		}
	}
	return out
}

// Activity is a last-activity time: the time of day when it is today
// (UTC), the date otherwise.
func Activity(t *dto.Time, now time.Time) string {
	if t == nil {
		return "never"
	}
	u := t.UTC()
	if u.Format("2006-01-02") == now.UTC().Format("2006-01-02") {
		return u.Format("15:04:05")
	}
	return u.Format("Jan 2 15:04")
}

func onOff(b *bool) string {
	if *b {
		return "on"
	}
	return "off"
}

// StreamCard is the view of a source.
func StreamCard(s dto.Stream, now time.Time) StreamCardView {
	v := StreamCardView{
		ID: s.ID, Status: string(s.Status), Transport: s.Kind + " / " + s.Transport,
		Rate: strconv.FormatFloat(s.RatePerMin, 'f', 1, 64), Last: Activity(s.LastActivity, now),
		Preview: s.LastLine, Dropped: strconv.FormatInt(s.Dropped, 10),
		Open: "/live?" + url.Values{"streams": {s.ID}}.Encode(),
	}
	if s.Claim != nil {
		v.Claim = s.Claim.Principal + " until " + s.Claim.ExpiresAt.UTC().Format("15:04")
	}
	if ls := s.LineState; ls != nil {
		var parts []string
		for _, l := range []struct {
			name string
			v    *bool
		}{{"DTR", ls.DTR}, {"RTS", ls.RTS}, {"CTS", ls.CTS}, {"DCD", ls.DCD}} {
			if l.v != nil {
				parts = append(parts, l.name+" "+onOff(l.v))
			}
		}
		v.LineState = strings.Join(parts, ", ")
	}
	return v
}

// Day is a date and time for lists of tokens and sessions.
func Day(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") }

func lastSeen(t *time.Time) string {
	if t == nil {
		return "never seen"
	}
	return "last seen " + Day(*t) + " UTC"
}
