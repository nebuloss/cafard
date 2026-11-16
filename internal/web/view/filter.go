package view

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// LogFilter is the logs query shared by /live, /api/entries and the live
// scope of /api/events: streams, channel, from_seq, to_seq, sev, re, kinds
// (PLAN 9.6.4), plus the paging cursors of the JSON API.
type LogFilter struct {
	Streams   []string // ids or globs, as typed
	Channel   string
	FromSeq   int64
	ToSeq     int64
	SinceSeq  int64
	BeforeSeq int64
	MinSev    *int // nil: all
	Re        *regexp.Regexp
	ReText    string
	Kinds     []store.Kind // never empty after parsing
	Messages  bool         // kinds=message was asked for (SSE only)
	Limit     int
}

// ParseLogFilter reads a query. Lists may be repeated or comma separated.
func ParseLogFilter(q url.Values) (LogFilter, *dto.APIError) {
	var f LogFilter
	for _, p := range list(q["streams"]) {
		if err := store.ValidatePattern(p); err != nil {
			return f, dto.Errorf(dto.CodeInvalidPattern, "%v.", err)
		}
		f.Streams = append(f.Streams, p)
	}
	f.Channel = strings.TrimSpace(q.Get("channel"))
	var err *dto.APIError
	if f.FromSeq, err = seqParam(q, "from_seq"); err != nil {
		return f, err
	}
	if f.ToSeq, err = seqParam(q, "to_seq"); err != nil {
		return f, err
	}
	if f.SinceSeq, err = seqParam(q, "since_seq"); err != nil {
		return f, err
	}
	if f.BeforeSeq, err = seqParam(q, "before_seq"); err != nil {
		return f, err
	}
	if s := strings.TrimSpace(q.Get("sev")); s != "" && s != "7" && s != "all" {
		sev, perr := store.ParseSeverity(s)
		if perr != nil {
			return f, dto.Errorf(dto.CodeBadRequest, "%v.", perr)
		}
		f.MinSev = &sev
	}
	if re := q.Get("re"); re != "" {
		c, cerr := regexp.Compile(re)
		if cerr != nil {
			return f, dto.Errorf(dto.CodeInvalidPattern, "The regexp %q is not a valid RE2 expression: %v.", re, cerr)
		}
		f.Re, f.ReText = c, re
	}
	for _, k := range list(q["kinds"]) {
		switch store.Kind(k) {
		case store.KindLine, store.KindMarker:
			if !slices.Contains(f.Kinds, store.Kind(k)) {
				f.Kinds = append(f.Kinds, store.Kind(k))
			}
		case store.KindMessage:
			f.Messages = true
		default:
			return f, dto.Errorf(dto.CodeBadRequest, "kinds takes line, marker or message, not %q.", k)
		}
	}
	if len(f.Kinds) == 0 && !f.Messages {
		f.Kinds = []store.Kind{store.KindLine, store.KindMarker}
	}
	if s := q.Get("limit"); s != "" {
		n, perr := strconv.Atoi(s)
		if perr != nil || n <= 0 {
			return f, dto.Errorf(dto.CodeBadRequest, "limit must be a positive integer.")
		}
		f.Limit = n
	}
	return f, nil
}

func list(vs []string) []string {
	var out []string
	for _, v := range vs {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func seqParam(q url.Values, name string) (int64, *dto.APIError) {
	s := q.Get(name)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, dto.Errorf(dto.CodeBadRequest, "%s must be a positive integer.", name)
	}
	return n, nil
}

// Patterns resolves the channel (through messaging) and drops message
// streams: logs views never read chan:* or dm:*. nil means every log
// stream, an empty list means nothing matches.
func (f LogFilter) Patterns(ctx context.Context, d deps.Deps) ([]string, *dto.APIError) {
	var patterns []string
	if f.Channel != "" {
		if d.Messaging == nil {
			return nil, dto.Errorf(dto.CodeUnknownChannel, "There is no channel %q: there are no channels yet.", f.Channel)
		}
		bound, err := d.Messaging.ResolveChannel(ctx, f.Channel)
		if err != nil {
			if apiErr, ok := err.(*dto.APIError); ok {
				return nil, apiErr
			}
			return nil, dto.Errorf(dto.CodeUnknownChannel, "There is no channel %q.", f.Channel)
		}
		patterns = append(patterns, bound...)
		if len(patterns) == 0 && len(f.Streams) == 0 {
			return []string{}, nil
		}
	}
	for _, p := range f.Streams {
		if !store.IsMessageStream(p) {
			patterns = append(patterns, p)
		}
	}
	if f.Channel == "" && len(f.Streams) > 0 && len(patterns) == 0 {
		return []string{}, nil
	}
	return patterns, nil
}

// StreamIDs resolves patterns to existing log stream ids; nil means all.
func StreamIDs(ctx context.Context, st *store.Store, patterns []string) ([]string, error) {
	if patterns == nil {
		return nil, nil
	}
	if len(patterns) == 0 {
		return []string{}, nil
	}
	ids, err := st.MatchStreams(ctx, patterns)
	if err != nil {
		return nil, err
	}
	ids = slices.DeleteFunc(ids, store.IsMessageStream)
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

// Query is the store query of f for ids, without paging.
func (f LogFilter) Query(ids []string) store.Query {
	q := store.Query{Streams: ids, Kinds: f.Kinds, MinSeverity: f.MinSev, Pattern: f.Re, UntilSeq: f.ToSeq}
	if len(q.Kinds) == 0 {
		q.Kinds = []store.Kind{store.KindLine, store.KindMarker}
	}
	return q
}

// URL is f as a /live query string, for links and the LogView mount.
func (f LogFilter) URL() string {
	v := url.Values{}
	if len(f.Streams) > 0 {
		v.Set("streams", strings.Join(f.Streams, ","))
	}
	if f.Channel != "" {
		v.Set("channel", f.Channel)
	}
	if f.MinSev != nil {
		v.Set("sev", strconv.Itoa(*f.MinSev))
	}
	if f.ReText != "" {
		v.Set("re", f.ReText)
	}
	var kinds []string
	for _, k := range f.Kinds {
		kinds = append(kinds, string(k))
	}
	if f.Messages {
		kinds = append(kinds, string(store.KindMessage))
	}
	if len(kinds) > 0 && !(len(kinds) == 2 && !f.Messages) {
		v.Set("kinds", strings.Join(kinds, ","))
	}
	return v.Encode()
}
