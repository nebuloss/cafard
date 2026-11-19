package messaging

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/blob"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/textclean"
)

// Log attachment limits (PLAN section 6.2).
const (
	MaxLogAttachments = 5
	SnapshotMaxLines  = 2000
	snapshotMaxBytes  = 256 << 10
	previewLines      = 5
	maxLogTitle       = 200
	attachReadChunk   = 5000
)

// MaxRangeLines caps the lines of one log attachment. A variable so tests
// need not write 100000 lines under the race detector.
var MaxRangeLines = 100000

var logKinds = []store.Kind{store.KindLine, store.KindMarker}

// snapshotLimits render a snapshot: every line, each cut as agents see it.
var snapshotLimits = textclean.Limits{MaxLineLen: textclean.AgentDefaults.MaxLineLen}

// buildLogAttachment validates a request, resolves its streams (which the
// sender must be able to read), counts its lines and snapshots a small
// closed range.
func (s *Service) buildLogAttachment(ctx context.Context, from auth.Principal, i int, r dto.LogRequest) (store.Attachment, error) {
	bad := func(format string, args ...any) error {
		return dto.Errorf(dto.CodeBadRequest, "Log attachment %d: "+format, append([]any{i + 1}, args...)...)
	}
	if (r.Stream == "") == (r.Channel == "") {
		return store.Attachment{}, bad("give exactly one of stream and channel.")
	}
	if r.FromSeq < 1 {
		return store.Attachment{}, bad("from_seq must be a positive seq.")
	}
	if r.ToSeq != nil && *r.ToSeq < r.FromSeq {
		return store.Attachment{}, bad("to_seq %d is before from_seq %d.", *r.ToSeq, r.FromSeq)
	}
	if r.MinSeverity != nil && (*r.MinSeverity < 0 || *r.MinSeverity > 7) {
		return store.Attachment{}, bad("min_severity must be 0 to 7.")
	}
	if len(r.Title) > maxLogTitle {
		return store.Attachment{}, bad("the title is longer than %d bytes.", maxLogTitle)
	}
	var re *regexp.Regexp
	if r.Grep != "" {
		var err error
		if re, err = regexp.Compile(r.Grep); err != nil {
			return store.Attachment{}, dto.Errorf(dto.CodeInvalidPattern, "Log attachment %d: grep %q is not a valid RE2 expression: %v.", i+1, r.Grep, err)
		}
	}
	a := store.Attachment{Type: dto.AttachmentLog, Stream: r.Stream, FromSeq: r.FromSeq, ToSeq: r.ToSeq,
		Grep: r.Grep, MinSeverity: r.MinSeverity, Title: r.Title}
	if r.Stream != "" {
		if store.IsMessageStream(r.Stream) {
			return store.Attachment{}, bad("%s holds messages; attach log streams only.", r.Stream)
		}
		if err := store.ValidateStreamID(r.Stream); err != nil {
			return store.Attachment{}, bad("%v.", err)
		}
		ids, err := s.st.MatchStreams(ctx, []string{r.Stream})
		if err != nil {
			return store.Attachment{}, err
		}
		if !slices.Contains(ids, r.Stream) || !s.access(ctx, from).Can(r.Stream, auth.ScopeRead) {
			return store.Attachment{}, dto.Errorf(dto.CodeNotFound, "Log attachment %d: there is no stream %q you can read; list_streams shows them.", i+1, r.Stream)
		}
		a.Streams = []string{r.Stream}
	} else {
		ch, err := s.Channel(ctx, from, r.Channel)
		if err != nil {
			return store.Attachment{}, err
		}
		a.Channel = ch.Name
		if a.Streams, err = s.BoundStreams(ctx, ch); err != nil {
			return store.Attachment{}, err
		}
		if len(a.Streams) == 0 {
			return store.Attachment{}, bad("#%s is bound to no existing stream.", ch.Name)
		}
	}

	q := store.Query{Streams: a.Streams, SinceSeq: r.FromSeq, Kinds: logKinds, MinSeverity: r.MinSeverity, Pattern: re, Limit: attachReadChunk}
	if r.ToSeq != nil {
		q.UntilSeq = *r.ToSeq
	}
	var kept []store.Entry
	for {
		res, err := s.st.Read(ctx, q)
		if err != nil {
			return store.Attachment{}, err
		}
		for _, e := range res.Entries {
			a.Lines++
			if a.FirstTS == 0 {
				a.FirstTS = e.TS.UnixNano()
			}
			a.LastTS = e.TS.UnixNano()
			if len(kept) <= SnapshotMaxLines {
				kept = append(kept, e)
			}
		}
		if a.Lines > MaxRangeLines {
			return store.Attachment{}, dto.Errorf(dto.CodeTooLarge, "Log attachment %d holds more than %d lines; narrow the range or add grep.", i+1, MaxRangeLines)
		}
		if !res.HasMore || res.NextSeq <= q.SinceSeq {
			break
		}
		q.SinceSeq = res.NextSeq
	}
	if r.ToSeq != nil && a.Lines > 0 && a.Lines <= SnapshotMaxLines {
		f := textclean.Format(kept, snapshotLimits, time.UTC)
		if len(f.Text) <= snapshotMaxBytes {
			sha, err := s.blobs.PutText(ctx, f.Text)
			if err != nil {
				return store.Attachment{}, err
			}
			a.SnapshotSHA256 = sha
		}
	}
	return a, nil
}

// expired reports whether retention removed every line a reference
// attachment pointed at. Snapshots never expire.
func (s *Service) expired(ctx context.Context, a store.Attachment) (bool, error) {
	if a.SnapshotSHA256 != "" || a.Lines == 0 || len(a.Streams) == 0 {
		return false, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(a.Streams)), ",")
	args := []any{}
	for _, st := range a.Streams {
		args = append(args, st)
	}
	upper := int64(1 << 62)
	if a.ToSeq != nil {
		upper = *a.ToSeq
	}
	args = append(args, a.FromSeq, upper, a.LastTS)
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM entries WHERE stream IN (`+ph+`)
		AND seq >= ? AND seq <= ? AND ts <= ? LIMIT 1)`, args...).Scan(&n)
	return n == 0, err
}

// CanReadAttachment reports whether viewer may read every stream a log
// attachment covers: seeing the message is not enough, or a message would
// carry lines of a sink the reader has no grant on.
func (s *Service) CanReadAttachment(ctx context.Context, viewer auth.Principal, a store.Attachment) bool {
	acc := s.access(ctx, viewer)
	for _, st := range a.Streams {
		if !acc.Can(st, auth.ScopeRead) {
			return false
		}
	}
	return true
}

func (s *Service) logDTO(ctx context.Context, viewer auth.Principal, a store.Attachment) (dto.LogAttachment, error) {
	la := dto.LogAttachment{Stream: a.Stream, Channel: a.Channel, Streams: a.Streams, FromSeq: a.FromSeq, ToSeq: a.ToSeq,
		Grep: a.Grep, MinSeverity: a.MinSeverity, Title: a.Title, Lines: a.Lines, Snapshot: a.SnapshotSHA256 != ""}
	if !s.CanReadAttachment(ctx, viewer, a) {
		la.Hidden, la.Snapshot, la.Lines = true, false, 0
		return la, nil
	}
	if a.FirstTS != 0 {
		la.FirstTS = dto.TimePtr(time.Unix(0, a.FirstTS).UTC())
		la.LastTS = dto.TimePtr(time.Unix(0, a.LastTS).UTC())
	}
	var err error
	if la.Expired, err = s.expired(ctx, a); err != nil {
		return la, err
	}
	if la.Expired {
		return la, nil
	}
	page, err := s.attachmentPage(ctx, a, a.FromSeq, previewLines)
	if err != nil {
		return la, err
	}
	for _, pe := range page.Entries {
		la.Preview = append(la.Preview, BaseDTO(pe))
	}
	return la, nil
}

// AttachmentPage is a page of a log attachment's lines.
type AttachmentPage struct {
	Entries  []store.Entry
	HasMore  bool
	NextSeq  int64
	Snapshot bool // read from the snapshot, not the stream
	Expired  bool
}

// AttachmentLines pages through a log attachment from sinceSeq (0 means
// its start), checking that viewer can see the message. A snapshot is
// read when there is one; an open-ended attachment keeps returning new
// lines of its streams.
func (s *Service) AttachmentLines(ctx context.Context, viewer auth.Principal, seq int64, index int, sinceSeq int64, limit int) (AttachmentPage, store.Attachment, error) {
	e, err := s.Message(ctx, viewer, seq)
	if err != nil {
		return AttachmentPage{}, store.Attachment{}, err
	}
	if index < 0 || index >= len(e.Attachments) || e.Attachments[index].Type != dto.AttachmentLog {
		return AttachmentPage{}, store.Attachment{}, dto.Errorf(dto.CodeNotFound, "Message %d has no log attachment %d.", seq, index)
	}
	a := e.Attachments[index]
	if !s.CanReadAttachment(ctx, viewer, a) {
		return AttachmentPage{}, store.Attachment{}, dto.Errorf(dto.CodeNotFound,
			"Log attachment %d of message %d covers streams you cannot read; ask the admin for a grant.", index, seq)
	}
	since := max(sinceSeq, a.FromSeq)
	page, err := s.attachmentPage(ctx, a, since, limit)
	if err != nil {
		return page, a, err
	}
	if len(page.Entries) == 0 && !page.Snapshot && since <= a.FromSeq {
		if page.Expired, err = s.expired(ctx, a); err != nil {
			return page, a, err
		}
	}
	return page, a, nil
}

func (s *Service) attachmentPage(ctx context.Context, a store.Attachment, since int64, limit int) (AttachmentPage, error) {
	if limit <= 0 {
		limit = textclean.AgentDefaults.MaxLines
	}
	if a.SnapshotSHA256 != "" {
		data, _, err := s.blobs.ReadAll(ctx, a.SnapshotSHA256, blob.VariantOriginal)
		if err == nil {
			all := parseFormatted(string(data))
			start, _ := slices.BinarySearchFunc(all, since, func(e store.Entry, seq int64) int {
				return cmp.Compare(e.Seq, seq)
			})
			rest := all[start:]
			page := AttachmentPage{Snapshot: true}
			if len(rest) > limit {
				page.Entries, page.HasMore = rest[:limit], true
				page.NextSeq = rest[limit].Seq
			} else {
				page.Entries = rest
				page.NextSeq = *a.ToSeq + 1
			}
			return page, nil
		}
		if !errors.Is(err, blob.ErrNotFound) {
			return AttachmentPage{}, err
		}
		// The blob size limit removed the snapshot: fall back to the stream.
	}
	var re *regexp.Regexp
	if a.Grep != "" {
		re, _ = regexp.Compile(a.Grep)
	}
	q := store.Query{Streams: a.Streams, SinceSeq: since, Kinds: logKinds, MinSeverity: a.MinSeverity, Pattern: re, Limit: limit}
	if a.ToSeq != nil {
		q.UntilSeq = *a.ToSeq
	}
	if len(q.Streams) == 0 {
		return AttachmentPage{NextSeq: since}, nil
	}
	res, err := s.st.Read(ctx, q)
	if err != nil {
		return AttachmentPage{}, err
	}
	page := AttachmentPage{Entries: res.Entries, HasMore: res.HasMore, NextSeq: res.NextSeq}
	if a.ToSeq != nil && !page.HasMore {
		page.NextSeq = *a.ToSeq + 1
	}
	return page, nil
}

// parseFormatted reads back text written by textclean.Format into
// entries: seq, time, stream, severity and text. Collapsed repeats come
// back as their first line only.
func parseFormatted(text string) []store.Entry {
	var (
		out []store.Entry
		day time.Time
	)
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if rest, ok := strings.CutPrefix(line, "# "); ok {
			if d, err := time.Parse(time.DateOnly, strings.Fields(rest)[0]); err == nil {
				day = d
			}
			continue
		}
		f := strings.SplitN(line, " ", 5)
		if len(f) < 4 {
			continue
		}
		seq, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			continue
		}
		clock, err := time.Parse("15:04:05.000", f[1])
		if err != nil {
			continue
		}
		ts := day.Add(time.Duration(clock.Hour())*time.Hour + time.Duration(clock.Minute())*time.Minute +
			time.Duration(clock.Second())*time.Second + time.Duration(clock.Nanosecond()))
		e := store.Entry{Seq: seq, TS: ts, Stream: f[2], Kind: store.KindLine, Severity: severityOf(f[3])}
		if len(f) == 5 {
			e.Text = f[4]
		}
		if strings.HasPrefix(e.Text, "-- ") && strings.HasSuffix(e.Text, " --") {
			e.Kind, e.Text = store.KindMarker, strings.TrimSuffix(strings.TrimPrefix(e.Text, "-- "), " --")
		}
		out = append(out, e)
	}
	return out
}

func severityOf(name string) int {
	if n, err := store.ParseSeverity(name); err == nil {
		return n
	}
	return store.DefaultSeverity
}

// describeRange is the header of a log attachment for agents.
func describeRange(a store.Attachment) string {
	var b strings.Builder
	if a.Title != "" {
		fmt.Fprintf(&b, "%q: ", a.Title)
	}
	b.WriteString(strings.Join(a.Streams, ", "))
	if a.Channel != "" {
		fmt.Fprintf(&b, " (bound to #%s when sent)", a.Channel)
	}
	if a.ToSeq != nil {
		fmt.Fprintf(&b, ", seq %d to %d", a.FromSeq, *a.ToSeq)
	} else {
		fmt.Fprintf(&b, ", from seq %d on (open-ended)", a.FromSeq)
	}
	fmt.Fprintf(&b, ", %d lines", a.Lines)
	if a.FirstTS != 0 {
		fmt.Fprintf(&b, ", %s to %s", time.Unix(0, a.FirstTS).UTC().Format("2006-01-02 15:04:05Z"), time.Unix(0, a.LastTS).UTC().Format("15:04:05Z"))
	}
	if a.Grep != "" {
		fmt.Fprintf(&b, ", grep %q", a.Grep)
	}
	if a.MinSeverity != nil {
		fmt.Fprintf(&b, ", min_severity %d", *a.MinSeverity)
	}
	if a.SnapshotSHA256 != "" {
		b.WriteString(", snapshot kept")
	}
	return b.String()
}
