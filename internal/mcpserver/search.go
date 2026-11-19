package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/textclean"
)

type searchIn struct {
	Query    string   `json:"query" jsonschema:"full-text query (SQLite FTS5): words that must all appear, \"an exact phrase\", prefix*, OR, NOT, NEAR(a b, 5); punctuation inside a word is fine"`
	Channel  string   `json:"channel,omitempty" jsonschema:"search this channel: its messages and its bound streams"`
	Project  string   `json:"project,omitempty" jsonschema:"only the streams of this project"`
	Streams  []string `json:"streams,omitempty" jsonschema:"stream ids or globs; default every log stream"`
	From     string   `json:"from,omitempty" jsonschema:"earliest time: RFC 3339 (2026-11-16T10:00:00Z) or relative to now (-2h, -30m, -1d, -1h30m)"`
	To       string   `json:"to,omitempty" jsonschema:"latest time, same forms as from"`
	SinceSeq *int64   `json:"since_seq,omitempty" jsonschema:"continue a search: the next_seq of the previous call"`
	Limit    int      `json:"limit,omitempty" jsonschema:"maximum matches, default 100, at most 400"`
}

func registerSearch(s *Server) {
	AddTool(s, auth.ScopeRead, mcp.Tool{Name: "search", Description: "Full-text search of the logs (FTS5), in seq order. With channel= it also searches that channel's messages. Times are when cafard received the line. Use read or context around a match for the lines near it."}, s.search)
}

func (s *Server) search(ctx context.Context, c *Call, in searchIn) (string, Page, error) {
	match, err := FTSQuery(in.Query)
	if err != nil {
		return "", Page{}, err
	}
	now := s.deps.Clock.Now()
	from, err := ParseTime(in.From, now, "from")
	if err != nil {
		return "", Page{}, err
	}
	to, err := ParseTime(in.To, now, "to")
	if err != nil {
		return "", Page{}, err
	}
	if !from.IsZero() && !to.IsZero() && to.Before(from) {
		return "", Page{}, dto.Errorf(dto.CodeBadRequest, "to (%s) is before from (%s).", to.Format(time.RFC3339), from.Format(time.RFC3339))
	}
	ids, err := s.selectIDs(ctx, c.Principal, selectIn{Channel: in.Channel, Streams: in.Streams})
	if err != nil {
		return "", Page{}, err
	}
	if in.Project != "" {
		if ids == nil {
			if ids, err = s.deps.Store.MatchStreams(ctx, nil); err != nil {
				return "", Page{}, err
			}
		}
		ids = slices.DeleteFunc(ids, func(id string) bool { return store.ProjectOf(id) != in.Project })
	}
	kinds := logKinds
	var keep func(store.Entry) bool
	if in.Channel != "" && s.deps.Messaging != nil {
		// The channel's own messages, each checked for the caller: a
		// question to someone else in the channel is still visible, a
		// direct message never is (it lives in dm:*, not chan:*).
		chat, _, err := s.deps.Messaging.ResolveChannel(ctx, c.Principal, in.Channel)
		if err != nil {
			return "", Page{}, err
		}
		if ids == nil {
			if ids, err = s.deps.Store.MatchStreams(ctx, nil); err != nil {
				return "", Page{}, err
			}
		}
		ids = append(slices.Clone(ids), chat)
		kinds = slices.Concat(logKinds, []store.Kind{store.KindMessage})
		p := c.Principal
		keep = func(e store.Entry) bool {
			if !store.IsMessageStream(e.Stream) {
				return true
			}
			return e.Stream == chat && s.deps.Messaging.Visible(ctx, p, e)
		}
	}
	q := store.SearchQuery{Match: match, Streams: ids, Kinds: kinds, From: from, To: to, Keep: keep,
		Limit: clampLimit(in.Limit, 100, textclean.AgentDefaults.MaxLines)}
	if in.SinceSeq != nil {
		q.SinceSeq = *in.SinceSeq
	}
	res, err := s.deps.Store.Search(ctx, q)
	var qe *store.QueryError
	if errors.As(err, &qe) {
		return "", Page{}, dto.Errorf(dto.CodeBadRequest, "The search %q is not a valid full-text query (%s). Quote a phrase with double quotes, balance parentheses, and write AND, OR, NOT in capitals only as operators.", in.Query, qe.Reason)
	}
	if err != nil {
		return "", Page{}, err
	}
	text, page := formatPage(res.Entries, res.HasMore, res.NextSeq)
	page.Streams = ids
	if len(res.Entries) == 0 {
		text = fmt.Sprintf("No entry matches %q.", in.Query)
		if res.HasMore {
			text += fmt.Sprintf(" The search stopped early; continue with since_seq=%d.", res.NextSeq)
		}
	}
	return text, page, nil
}

var relative = regexp.MustCompile(`^-((?:\d+(?:\.\d+)?(?:ms|s|m|h|d|w))+)$`)
var relativePart = regexp.MustCompile(`(\d+(?:\.\d+)?)(ms|s|m|h|d|w)`)

// ParseTime reads a time argument: "" (no bound), "now", RFC 3339 (with
// or without fractions, or a date alone, taken as UTC midnight), or a
// duration before now such as -2h, -90s, -1d, -1h30m.
func ParseTime(s string, now time.Time, name string) (time.Time, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return time.Time{}, nil
	case strings.EqualFold(s, "now"):
		return now, nil
	}
	if m := relative.FindStringSubmatch(s); m != nil {
		var d time.Duration
		for _, part := range relativePart.FindAllStringSubmatch(m[1], -1) {
			n, _ := strconv.ParseFloat(part[1], 64)
			unit := map[string]time.Duration{"ms": time.Millisecond, "s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[part[2]]
			d += time.Duration(n * float64(unit))
		}
		return now.Add(-d), nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", time.DateOnly} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, dto.Errorf(dto.CodeBadRequest, "%s=%q is not a time: give RFC 3339 like 2026-11-16T10:00:00Z, or a time before now like -2h, -30m or -1d.", name, s)
}

// FTSQuery makes a user's search safe to hand to FTS5. Operators (AND,
// OR, NOT, NEAR(...), parentheses, "phrases", prefix*, ^initial) pass as
// they are; any other word holding characters FTS5 does not take in a
// bare word (usb-1.2, error:, 0x1f, /dev/ttyUSB0) is quoted, so it is
// searched as the phrase its tokens make. What is still wrong after that
// (an unterminated quote, unbalanced parentheses) is reported by SQLite
// and becomes a bad_request.
func FTSQuery(q string) (string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", dto.Errorf(dto.CodeBadRequest, "search needs a query.")
	}
	if len(q) > 1000 {
		return "", dto.Errorf(dto.CodeTooLarge, "A search query is at most 1000 bytes.")
	}
	var out strings.Builder
	words := 0
	var parens []bool // per open parenthesis: does it follow NEAR?
	for i := 0; i < len(q); {
		c := q[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			out.WriteByte(' ')
			i++
		case c == '"':
			// A phrase, copied with its doubled quotes; unterminated is
			// left for SQLite to report.
			j := i + 1
			for j < len(q) {
				if q[j] == '"' {
					if j+1 < len(q) && q[j+1] == '"' {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			out.WriteString(q[i:j])
			i = j
			words++
		case c == '(':
			parens = append(parens, strings.HasSuffix(out.String(), "NEAR"))
			out.WriteByte(c)
			i++
		case c == ')':
			if len(parens) > 0 {
				parens = parens[:len(parens)-1]
			}
			out.WriteByte(c)
			i++
		case c == ',':
			// Only NEAR(a b, 5) takes a comma; elsewhere it separates words.
			if len(parens) > 0 && parens[len(parens)-1] {
				out.WriteByte(c)
			} else {
				out.WriteByte(' ')
			}
			i++
		default:
			j := i
			for j < len(q) && !strings.ContainsRune(" \t\r\n\"(),", rune(q[j])) {
				j++
			}
			out.WriteString(ftsWord(q[i:j], j < len(q) && q[j] == '('))
			i = j
			words++
		}
	}
	if words == 0 {
		return "", dto.Errorf(dto.CodeBadRequest, "search needs at least one word to look for.")
	}
	return out.String(), nil
}

// ftsWord keeps an operator or a valid bare word, and quotes anything
// else. A trailing * stays a prefix search.
func ftsWord(w string, beforeParen bool) string {
	switch w {
	case "AND", "OR", "NOT", "+":
		return w
	case "NEAR":
		if beforeParen {
			return w
		}
	}
	core, star := strings.CutSuffix(w, "*")
	core, caret := strings.CutPrefix(core, "^")
	if core != "" && bareWord(core) {
		return w
	}
	if core == "" {
		core = w
		star, caret = false, false
	}
	quoted := `"` + strings.ReplaceAll(core, `"`, `""`) + `"`
	if caret {
		quoted = "^" + quoted
	}
	if star {
		quoted += "*"
	}
	return quoted
}

// bareWord is the FTS5 bareword alphabet: letters, digits, _, and any
// non-ASCII character.
func bareWord(s string) bool {
	for _, r := range s {
		if r >= utf8.RuneSelf {
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
