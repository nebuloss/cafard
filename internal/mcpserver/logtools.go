package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/hub"
	"github.com/nebuloss/cafard/internal/projects"
	"github.com/nebuloss/cafard/internal/status"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/textclean"
)

// Page is the structured content of every tool that returns entries.
type Page struct {
	NextSeq int64    `json:"next_seq" jsonschema:"pass this back as since_seq to continue"`
	HasMore bool     `json:"has_more" jsonschema:"more entries are available after next_seq"`
	Shown   int      `json:"shown" jsonschema:"entries represented in the text"`
	Streams []string `json:"streams,omitempty" jsonschema:"the streams that were read"`
}

type whoamiOut struct {
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	Scopes []string `json:"scopes"`
}

type listStreamsIn struct {
	Kind    string `json:"kind,omitempty" jsonschema:"only streams of this kind, e.g. serial, syslog, push"`
	Channel string `json:"channel,omitempty" jsonschema:"only the streams bound to this channel"`
	Project string `json:"project,omitempty" jsonschema:"only the streams of this project"`
}

type listStreamsOut struct {
	Streams []dto.Stream `json:"streams"`
}

type selectIn struct {
	Channel string   `json:"channel,omitempty" jsonschema:"read the streams bound to this channel"`
	Streams []string `json:"streams,omitempty" jsonschema:"stream ids or globs such as serial:lab/dut1 or syslog:lab/*; default every log stream you may read"`
}

type readIn struct {
	selectIn
	SinceSeq    *int64 `json:"since_seq,omitempty" jsonschema:"first seq to return, usually the next_seq of the previous call; default the last 100 lines"`
	UntilSeq    *int64 `json:"until_seq,omitempty" jsonschema:"last seq to return"`
	Limit       int    `json:"limit,omitempty" jsonschema:"maximum lines, at most 400"`
	MinSeverity *int   `json:"min_severity,omitempty" jsonschema:"keep severity <= this (0 emerg ... 3 err, 4 warn, 6 info, 7 debug)"`
	Grep        string `json:"grep,omitempty" jsonschema:"RE2 regular expression the text must match"`
}

type tailIn struct {
	selectIn
	Lines int `json:"lines,omitempty" jsonschema:"how many of the latest lines, default 50"`
}

type waitIn struct {
	selectIn
	Pattern      string `json:"pattern" jsonschema:"RE2 regular expression to wait for"`
	SinceSeq     *int64 `json:"since_seq,omitempty" jsonschema:"also match entries from this seq on; default only new entries"`
	TimeoutS     int    `json:"timeout_s,omitempty" jsonschema:"seconds to wait, default 60, at most 600"`
	ContextLines *int   `json:"context_lines,omitempty" jsonschema:"lines before the match to include, default 20"`
}

type waitOut struct {
	Page
	Matched  bool  `json:"matched"`
	MatchSeq int64 `json:"match_seq,omitempty"`
	TimedOut bool  `json:"timed_out"`
}

type contextIn struct {
	Seq    int64 `json:"seq" jsonschema:"the entry to show"`
	Before *int  `json:"before,omitempty" jsonschema:"entries before it in its stream, default 20"`
	After  *int  `json:"after,omitempty" jsonschema:"entries after it in its stream, default 20"`
}

type markIn struct {
	Stream string `json:"stream" jsonschema:"the stream to mark, e.g. serial:lab/dut1"`
	Label  string `json:"label" jsonschema:"what happens from here, e.g. test 42 starts"`
}

type markOut struct {
	Seq int64 `json:"seq" jsonschema:"the marker's seq, a cursor for everything after it"`
}

const maxLabel = 1000

// logKinds are what the log tools return: never messages.
var logKinds = []store.Kind{store.KindLine, store.KindMarker}

func registerLogTools(s *Server) {
	AddTool(s, auth.ScopeRead, mcp.Tool{Name: "whoami", Description: "Who you are to cafard: principal name, kind and scopes."}, s.whoami)
	AddTool(s, auth.ScopeRead, mcp.Tool{Name: "list_streams", Description: "The log sources: id, status (connected, idle, down), rate, last activity, drops."}, s.listStreams)
	AddTool(s, auth.ScopeRead, mcp.Tool{Name: "read", Description: "Read log lines in seq order from a cursor. Keep next_seq and pass it back as since_seq. Never returns messages."}, s.read)
	AddTool(s, auth.ScopeRead, mcp.Tool{Name: "tail", Description: "The latest log lines, and the next_seq to continue from."}, s.tail)
	AddTool(s, auth.ScopeRead, mcp.Tool{Name: "wait_for", Description: "Wait until a log line matches a pattern, e.g. \"login:|Kernel panic\". Returns the match with the lines before it, or timed_out. Use it instead of polling read."}, s.waitFor)
	AddTool(s, auth.ScopeRead, mcp.Tool{Name: "context", Description: "The lines around one entry, in its stream."}, s.context)
	registerSearch(s)
	AddTool(s, auth.ScopeMessage, mcp.Tool{Name: "mark", Description: "Write a marker into a stream and get its seq: a cursor for \"everything since test X started\"."}, s.mark)
}

func (s *Server) whoami(_ context.Context, c *Call, _ struct{}) (string, whoamiOut, error) {
	p := c.Principal
	out := whoamiOut{Name: p.Name, Kind: p.Kind, Scopes: p.Scopes.Strings()}
	return fmt.Sprintf("You are %s (%s), with scopes: %s.", p.Name, p.Kind, strings.Join(out.Scopes, ", ")), out, nil
}

// access is what p may reach in the projects.
func (s *Server) access(ctx context.Context, p auth.Principal) *projects.Access {
	return s.deps.Projects.Access(ctx, p)
}

// resolve turns channel= and streams= into stream patterns. Message streams
// are dropped: log tools never return messages. nil means every log stream
// p may read; the caller narrows to them (streamIDs, or the hub filter).
func (s *Server) resolve(ctx context.Context, p auth.Principal, in selectIn) ([]string, error) {
	var patterns []string
	if in.Channel != "" {
		if s.deps.Messaging == nil {
			return nil, dto.Errorf(dto.CodeUnknownChannel, "There is no channel %q: there are no channels yet.", in.Channel)
		}
		_, bound, err := s.deps.Messaging.ResolveChannel(ctx, p, in.Channel)
		if err != nil {
			return nil, err
		}
		if len(bound) == 0 && len(in.Streams) == 0 {
			return []string{}, nil
		}
		patterns = append(patterns, bound...)
	}
	for _, p := range in.Streams {
		if err := store.ValidatePattern(p); err != nil {
			return nil, dto.Errorf(dto.CodeInvalidPattern, "%v.", err)
		}
		if !store.IsMessageStream(p) {
			patterns = append(patterns, p)
		}
	}
	if in.Channel == "" && len(in.Streams) > 0 && len(patterns) == 0 {
		return []string{}, nil
	}
	return patterns, nil
}

// visiblePatterns is patterns, or for nil the log streams a may read.
// nil comes back only for the admin: every log stream.
func visiblePatterns(a *projects.Access, patterns []string) []string {
	if patterns == nil && !a.All() {
		return a.LogPatterns()
	}
	return patterns
}

// streamIDs resolves patterns to the existing log streams a may read; nil
// patterns mean every one, and nil ids every one for the admin.
func (s *Server) streamIDs(ctx context.Context, a *projects.Access, patterns []string) ([]string, error) {
	patterns = visiblePatterns(a, patterns)
	if patterns == nil {
		return nil, nil
	}
	if len(patterns) == 0 {
		return []string{}, nil
	}
	ids, err := s.deps.Store.MatchStreams(ctx, patterns)
	if err != nil {
		return nil, err
	}
	ids = slices.DeleteFunc(ids, func(id string) bool { return store.IsMessageStream(id) || !a.Can(id, auth.ScopeRead) })
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

// selectIDs resolves channel= and streams= to the existing log streams p
// may read; nil means all, for the admin.
func (s *Server) selectIDs(ctx context.Context, p auth.Principal, in selectIn) ([]string, error) {
	patterns, err := s.resolve(ctx, p, in)
	if err != nil {
		return nil, err
	}
	return s.streamIDs(ctx, s.access(ctx, p), patterns)
}

func compile(pattern, what string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, dto.Errorf(dto.CodeInvalidPattern, "The %s %q is not a valid RE2 expression: %v.", what, pattern, err)
	}
	return re, nil
}

func (s *Server) listStreams(ctx context.Context, c *Call, in listStreamsIn) (string, listStreamsOut, error) {
	var keep map[string]bool
	if in.Channel != "" {
		ids, err := s.selectIDs(ctx, c.Principal, selectIn{Channel: in.Channel})
		if err != nil {
			return "", listStreamsOut{}, err
		}
		keep = map[string]bool{}
		for _, id := range ids {
			keep[id] = true
		}
	}
	streams, err := s.Streams(ctx, c.Principal)
	if err != nil {
		return "", listStreamsOut{}, err
	}
	out := listStreamsOut{Streams: []dto.Stream{}}
	for _, st := range streams {
		if (in.Kind != "" && st.Kind != in.Kind) || (keep != nil && !keep[st.ID]) || (in.Project != "" && st.Project != in.Project) {
			continue
		}
		out.Streams = append(out.Streams, st)
	}
	if len(out.Streams) == 0 {
		return "No log streams match.", out, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d streams (id, status, lines/min over 5 min, last activity, last seq, dropped):\n", len(out.Streams))
	for _, st := range out.Streams {
		last := "never"
		if st.LastActivity != nil {
			last = st.LastActivity.UTC().Format("2006-01-02 15:04:05Z")
		}
		claim := ""
		if st.Claim != nil {
			claim = fmt.Sprintf(" claimed_by=%s until %s", st.Claim.Principal, st.Claim.ExpiresAt.UTC().Format("15:04:05Z"))
		}
		fmt.Fprintf(&b, "%s %s %.1f/min last=%s last_seq=%d dropped=%d%s\n", st.ID, st.Status, st.RatePerMin, last, st.LastSeq, st.Dropped, claim)
	}
	return b.String(), out, nil
}

// Streams merges the store's streams p may read with the live status.
// Message streams are left out.
func (s *Server) Streams(ctx context.Context, p auth.Principal) ([]dto.Stream, error) {
	out, err := status.Streams(ctx, s.deps.Store, s.deps.Status)
	if err != nil {
		return nil, err
	}
	a := s.access(ctx, p)
	out = slices.DeleteFunc(out, func(st dto.Stream) bool { return !a.Can(st.ID, auth.ScopeRead) })
	if cs, ok := s.deps.Messaging.(ClaimSource); ok {
		claims, err := cs.ActiveClaims(ctx)
		if err != nil {
			return nil, err
		}
		for i := range out {
			if c, ok := claims[out[i].ID]; ok {
				out[i].Claim = &c
			}
		}
	}
	return out, nil
}

// ClaimSource is implemented by deps.Messaging when it holds the claims,
// so list_streams can show who holds each stream.
type ClaimSource interface {
	ActiveClaims(ctx context.Context) (map[string]dto.Claim, error)
}

func clampLimit(n, def, max int) int {
	if n <= 0 {
		return def
	}
	return min(n, max)
}

func formatPage(entries []store.Entry, more bool, next int64) (string, Page) {
	f := textclean.Format(entries, textclean.AgentDefaults, time.UTC)
	page := Page{NextSeq: next, HasMore: more, Shown: f.Shown}
	if f.HasMore {
		page.NextSeq, page.HasMore = f.NextSeq, true
	}
	text := f.Text
	if len(entries) == 0 {
		text = "No entries."
	} else if more && !f.HasMore {
		text += fmt.Sprintf("[more available: continue with since_seq=%d]\n", page.NextSeq)
	}
	return text, page
}

func (s *Server) read(ctx context.Context, c *Call, in readIn) (string, Page, error) {
	ids, err := s.selectIDs(ctx, c.Principal, in.selectIn)
	if err != nil {
		return "", Page{}, err
	}
	re, err := compile(in.Grep, "grep")
	if err != nil {
		return "", Page{}, err
	}
	q := store.Query{Streams: ids, MinSeverity: in.MinSeverity, Pattern: re, Kinds: logKinds}
	if in.UntilSeq != nil {
		q.UntilSeq = *in.UntilSeq
	}
	if in.SinceSeq == nil {
		// No cursor: the latest lines, and a cursor to continue forward.
		last, err := s.deps.Store.LastSeq(ctx)
		if err != nil {
			return "", Page{}, err
		}
		q.Desc, q.Limit = true, clampLimit(in.Limit, 100, textclean.AgentDefaults.MaxLines)
		res, err := s.deps.Store.Read(ctx, q)
		if err != nil {
			return "", Page{}, err
		}
		text, page := formatPage(res.Entries, false, last+1)
		page.Streams = ids
		return text, page, nil
	}
	q.SinceSeq, q.Limit = *in.SinceSeq, clampLimit(in.Limit, textclean.AgentDefaults.MaxLines, textclean.AgentDefaults.MaxLines)
	res, err := s.deps.Store.Read(ctx, q)
	if err != nil {
		return "", Page{}, err
	}
	text, page := formatPage(res.Entries, res.HasMore, res.NextSeq)
	page.Streams = ids
	return text, page, nil
}

func (s *Server) tail(ctx context.Context, c *Call, in tailIn) (string, Page, error) {
	ids, err := s.selectIDs(ctx, c.Principal, in.selectIn)
	if err != nil {
		return "", Page{}, err
	}
	// Taken before reading, so the cursor can only repeat a line, never
	// skip one committed during the read.
	last, err := s.deps.Store.LastSeq(ctx)
	if err != nil {
		return "", Page{}, err
	}
	res, err := s.deps.Store.Read(ctx, store.Query{Streams: ids, Desc: true, Limit: clampLimit(in.Lines, 50, textclean.AgentDefaults.MaxLines),
		Kinds: logKinds})
	if err != nil {
		return "", Page{}, err
	}
	text, page := formatPage(res.Entries, false, last+1)
	page.HasMore = false
	page.Streams = ids
	return text, page, nil
}

const (
	waitDefault     = 60 * time.Second
	waitMax         = 600 * time.Second
	progressEvery   = 10 * time.Second
	waitSeenKept    = 50
	waitSubBuffer   = 8192
	waitReadBatch   = 1000
	defaultContextN = 20
)

func (s *Server) waitFor(ctx context.Context, c *Call, in waitIn) (string, waitOut, error) {
	re, err := compile(in.Pattern, "pattern")
	if err != nil {
		return "", waitOut{}, err
	}
	if re == nil {
		return "", waitOut{}, dto.Errorf(dto.CodeBadRequest, "wait_for needs a pattern.")
	}
	patterns, err := s.resolve(ctx, c.Principal, in.selectIn)
	if err != nil {
		return "", waitOut{}, err
	}
	a := s.access(ctx, c.Principal)
	patterns = visiblePatterns(a, patterns)
	timeout := waitDefault
	if in.TimeoutS > 0 {
		timeout = min(time.Duration(in.TimeoutS)*time.Second, waitMax)
	}
	contextN := defaultContextN
	if in.ContextLines != nil {
		contextN = max(0, min(*in.ContextLines, 200))
	}

	w := &waiter{s: s, access: a, re: re, patterns: patterns, kinds: logKinds}
	if patterns != nil && len(patterns) == 0 {
		return "No stream matches, so nothing can arrive.", waitOut{TimedOut: true}, nil
	}
	// "Now" is taken before subscribing, so an entry committed while the
	// subscription is set up still counts as new.
	if in.SinceSeq != nil {
		w.cursor = *in.SinceSeq
	} else {
		last, err := s.deps.Store.LastSeq(ctx)
		if err != nil {
			return "", waitOut{}, err
		}
		w.cursor = last + 1
	}
	// Subscribe before reading the store: an entry committed in between is
	// then in the subscription, and the seq check drops duplicates.
	sub := s.deps.Hub.Subscribe(hub.Filter{Streams: patterns, Kinds: w.kinds, MinSeverity: 7}, waitSubBuffer)
	defer func() { sub.Close() }()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(progressEvery)
	defer ticker.Stop()
	started := time.Now()

	match, err := w.catchUp(ctx)
	for match == nil && err == nil {
		select {
		case e := <-sub.C():
			match = w.consider(e)
		case <-sub.Lagged():
			sub.Close()
			sub = s.deps.Hub.Subscribe(hub.Filter{Streams: patterns, Kinds: w.kinds, MinSeverity: 7}, waitSubBuffer)
			match, err = w.catchUp(ctx)
		case <-ticker.C:
			c.Progress(ctx, time.Since(started).Seconds(), fmt.Sprintf("waiting for /%s/, %d lines seen", in.Pattern, w.seenCount))
		case <-deadline.C:
			return w.timedOut(timeout)
		case <-ctx.Done():
			return "", waitOut{}, ctx.Err()
		}
	}
	if err != nil {
		return "", waitOut{}, err
	}
	return w.matched(ctx, *match, contextN)
}

type waiter struct {
	s         *Server
	access    *projects.Access
	re        *regexp.Regexp
	patterns  []string
	kinds     []store.Kind
	cursor    int64 // next seq not yet considered
	seen      []store.Entry
	seenCount int
}

func (w *waiter) consider(e store.Entry) *store.Entry {
	if e.Seq < w.cursor || store.IsMessageStream(e.Stream) || !w.access.Can(e.Stream, auth.ScopeRead) {
		return nil
	}
	w.cursor = e.Seq + 1
	if w.re.MatchString(e.Text) {
		return &e
	}
	w.seenCount++
	w.seen = append(w.seen, e)
	if len(w.seen) > waitSeenKept {
		w.seen = w.seen[len(w.seen)-waitSeenKept:]
	}
	return nil
}

// catchUp reads the store from the cursor to its end.
func (w *waiter) catchUp(ctx context.Context) (*store.Entry, error) {
	for {
		ids, err := w.s.streamIDs(ctx, w.access, w.patterns)
		if err != nil {
			return nil, err
		}
		res, err := w.s.deps.Store.Read(ctx, store.Query{Streams: ids, SinceSeq: w.cursor, Limit: waitReadBatch, Kinds: w.kinds})
		if err != nil {
			return nil, err
		}
		for _, e := range res.Entries {
			if m := w.consider(e); m != nil {
				return m, nil
			}
		}
		w.cursor = max(w.cursor, res.NextSeq)
		if !res.HasMore {
			return nil, nil
		}
	}
}

func (w *waiter) matched(ctx context.Context, m store.Entry, contextN int) (string, waitOut, error) {
	entries := []store.Entry{m}
	if contextN > 0 {
		around, err := w.s.deps.Store.Around(ctx, m.Seq, contextN, 0)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return "", waitOut{}, err
		}
		if len(around) > 0 {
			entries = around
		}
	}
	f := textclean.Format(entries, textclean.AgentDefaults, time.UTC)
	out := waitOut{Page: Page{NextSeq: m.Seq + 1, Shown: f.Shown, Streams: []string{m.Stream}}, Matched: true, MatchSeq: m.Seq}
	text := fmt.Sprintf("Matched at seq %d in %s:\n%s", m.Seq, m.Stream, f.Text)
	return text, out, nil
}

func (w *waiter) timedOut(timeout time.Duration) (string, waitOut, error) {
	out := waitOut{Page: Page{NextSeq: w.cursor}, TimedOut: true}
	if len(w.seen) == 0 {
		return fmt.Sprintf("Timed out after %s: no new lines arrived. Continue with since_seq=%d.", timeout, w.cursor), out, nil
	}
	f := textclean.Format(w.seen, textclean.AgentDefaults, time.UTC)
	out.Shown = f.Shown
	return fmt.Sprintf("Timed out after %s without a match. %d lines arrived; the last ones:\n%sContinue with since_seq=%d.",
		timeout, w.seenCount, f.Text, w.cursor), out, nil
}

func (s *Server) context(ctx context.Context, c *Call, in contextIn) (string, Page, error) {
	before, after := defaultContextN, defaultContextN
	if in.Before != nil {
		before = max(0, min(*in.Before, 200))
	}
	if in.After != nil {
		after = max(0, min(*in.After, 200))
	}
	focus, err := s.deps.Store.Get(ctx, in.Seq)
	if errors.Is(err, store.ErrNotFound) {
		return "", Page{}, dto.Errorf(dto.CodeNotFound, "There is no entry %d; retention may have removed it.", in.Seq)
	}
	if err != nil {
		return "", Page{}, err
	}
	visible := func(e store.Entry) bool {
		if store.IsMessageStream(e.Stream) {
			return s.deps.Messaging != nil && s.deps.Messaging.Visible(ctx, c.Principal, e)
		}
		return s.access(ctx, c.Principal).Can(e.Stream, auth.ScopeRead)
	}
	if !visible(focus) {
		return "", Page{}, dto.Errorf(dto.CodeNotFound, "There is no entry %d; retention may have removed it.", in.Seq)
	}
	entries, err := s.deps.Store.Around(ctx, in.Seq, before, after)
	if err != nil {
		return "", Page{}, err
	}
	entries = slices.DeleteFunc(entries, func(e store.Entry) bool { return !visible(e) })
	f := textclean.Format(entries, textclean.AgentDefaults, time.UTC)
	next := entries[len(entries)-1].Seq + 1
	return f.Text, Page{NextSeq: next, Shown: f.Shown, Streams: []string{focus.Stream}}, nil
}

func (s *Server) mark(ctx context.Context, c *Call, in markIn) (string, markOut, error) {
	label := strings.TrimSpace(in.Label)
	if label == "" {
		return "", markOut{}, dto.Errorf(dto.CodeBadRequest, "A marker needs a label.")
	}
	if len(label) > maxLabel {
		return "", markOut{}, dto.Errorf(dto.CodeTooLarge, "A marker label is at most %d bytes.", maxLabel)
	}
	if store.IsMessageStream(in.Stream) {
		return "", markOut{}, dto.Errorf(dto.CodeBadRequest, "mark writes into log streams; post in a chat with send instead.")
	}
	ids, err := s.deps.Store.MatchStreams(ctx, []string{in.Stream})
	if err != nil {
		return "", markOut{}, err
	}
	a := s.access(ctx, c.Principal)
	if !slices.Contains(ids, in.Stream) || !a.Can(in.Stream, auth.ScopeRead) {
		return "", markOut{}, dto.Errorf(dto.CodeNotFound, "There is no stream %q you can read; list_streams shows them.", in.Stream)
	}
	if !a.Can(in.Stream, auth.ScopeMessage) {
		return "", markOut{}, dto.Errorf(dto.CodeForbidden, "Marking %s needs message on it; ask the admin for a grant.", in.Stream)
	}
	seq, err := s.deps.Writer.Commit(ctx, store.Entry{
		Stream: in.Stream, Kind: store.KindMarker, Severity: store.DefaultSeverity,
		Text: c.Principal.Name + ": " + label, Sender: c.Principal.Name,
	})
	if err != nil {
		return "", markOut{}, err
	}
	return fmt.Sprintf("Marker %d written in %s. Pass since_seq=%d to read what follows.", seq, in.Stream, seq+1), markOut{Seq: seq}, nil
}
