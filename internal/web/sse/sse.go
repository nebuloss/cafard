// Package sse is GET /api/events (CONTRACTS.md 4): one Server-Sent Events
// connection per page, carrying entries for the page's scope plus the
// counts and stream_status events.
package sse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/hub"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/textclean"
	"github.com/nebuloss/cafard/internal/web/view"
)

// Handler serves /api/events. The exported fields are tunable for tests.
type Handler struct {
	d     deps.Deps
	kinds *view.Kinds

	Heartbeat   time.Duration // ": ping" comments, so proxies keep the connection
	CountsEvery time.Duration // counts are recomputed at most this often
	ReplayMax   int           // entries replayed after since_seq before a reset
	Buffer      int           // hub buffer before the subscriber counts as lagged
	// Presence counts the open feeds per principal (the members panel's
	// "online"); nil counts nothing.
	Presence *view.Presence
}

// New returns the handler with the contract's values.
func New(d deps.Deps, kinds *view.Kinds) *Handler {
	return &Handler{d: d, kinds: kinds, Heartbeat: 20 * time.Second, CountsEvery: time.Second, ReplayMax: 5000, Buffer: 4096}
}

// Register mounts GET /api/events for a bearer token or a session with read.
func Register(mux *http.ServeMux, h *Handler) {
	mux.Handle("GET /api/events", auth.Require(h.d.Auth, auth.ScopeRead, h))
}

// selection is the hub filter of a connection's scope; nil means the
// connection carries no entries (status scope, or nothing matches).
func (h *Handler) selection(ctx context.Context, r *http.Request, p auth.Principal) (*hub.Filter, *dto.APIError) {
	q := r.URL.Query()
	if chat := q.Get("chat"); chat != "" {
		return h.chatSelection(ctx, chat, p)
	}
	switch q.Get("scope") {
	case "status":
		return nil, nil
	case "home":
		if h.d.Messaging == nil {
			return nil, nil
		}
		f := h.messageFilter(ctx, &p)
		return &f, nil
	case "", "live":
	default:
		return nil, dto.Errorf(dto.CodeBadRequest, "scope is status, home or live, not %q.", q.Get("scope"))
	}
	f, apiErr := view.ParseLogFilter(q)
	if apiErr != nil {
		return nil, apiErr
	}
	patterns, apiErr := f.Patterns(ctx, h.d, p)
	if apiErr != nil {
		return nil, apiErr
	}
	a := h.d.Projects.Access(ctx, p)
	if patterns == nil && !a.All() {
		patterns = a.LogPatterns()
	}
	filter := &hub.Filter{Streams: patterns, Kinds: slices.Clone(f.Kinds), MinSeverity: 7, Pattern: f.Re, Viewer: &p}
	if f.MinSev != nil {
		filter.MinSeverity = *f.MinSev
	}
	if f.Messages && h.d.Messaging != nil {
		if filter.Streams == nil {
			// The admin's every log stream, once message globs join the
			// list: those of projects, and any without one.
			filter.Streams = []string{"*:*/*", "*"}
		}
		msgs, err := view.MessagePatterns(ctx, h.d, p, f)
		if err != nil {
			return nil, dto.Errorf(dto.CodeUnknownChannel, "There is no channel %q you can read.", f.Channel)
		}
		filter.Streams = append(filter.Streams, msgs...)
		filter.Kinds = append(filter.Kinds, store.KindMessage)
	}
	if filter.Streams != nil && len(filter.Streams) == 0 {
		// Nothing matches; an empty hub list would mean "everything".
		return nil, nil
	}
	return filter, nil
}

// chatSelection: the chat's messages, and every entry of a channel's bound
// streams. Visibility is checked again on every message.
func (h *Handler) chatSelection(ctx context.Context, chat string, p auth.Principal) (*hub.Filter, *dto.APIError) {
	if h.d.Messaging == nil {
		return nil, dto.Errorf(dto.CodeNotFound, "There is no chat %q: there are no chats yet.", chat)
	}
	if name, ok := strings.CutPrefix(chat, "chan:"); ok && strings.Contains(name, "/") {
		stream, bound, err := h.d.Messaging.ResolveChannel(ctx, p, name)
		if err != nil {
			return nil, dto.Errorf(dto.CodeNotFound, "There is no chat %q.", chat)
		}
		streams := []string{stream}
		for _, b := range bound {
			if !store.IsMessageStream(b) {
				streams = append(streams, b)
			}
		}
		return &hub.Filter{Streams: streams, MinSeverity: 7, Viewer: &p}, nil
	}
	if peer, ok := strings.CutPrefix(chat, "dm:"); ok {
		if auth.ValidateName(peer) != nil {
			return nil, dto.Errorf(dto.CodeNotFound, "There is no chat %q.", chat)
		}
		return &hub.Filter{Streams: []string{messaging.DMStream(p.Name, peer)}, Kinds: []store.Kind{store.KindMessage}, MinSeverity: 7, Viewer: &p}, nil
	}
	return nil, dto.Errorf(dto.CodeBadRequest, "chat is chan:<project>/<name> or dm:<peer>, not %q.", chat)
}

// messageFilter is every message p may see, in any chat.
func (h *Handler) messageFilter(ctx context.Context, p *auth.Principal) hub.Filter {
	return hub.Filter{Streams: h.d.Projects.Access(ctx, *p).MessagePatterns(), Kinds: []store.Kind{store.KindMessage}, MinSeverity: 7, Viewer: p}
}

// chatWatcher is what messaging offers for the seen, answered and chat
// events.
type chatWatcher interface {
	WatchChats(buffer int) (<-chan messaging.ChatEvent, func())
	ChatOfStream(ctx context.Context, viewer auth.Principal, stream string) (dto.Chat, error)
}

// scope is what a connection shows: one chat's stream, or the home page.
type scope struct {
	chat string // stream id of the chat page, or ""
	home bool
}

func scopeOf(r *http.Request, p auth.Principal) scope {
	q := r.URL.Query()
	if chat := q.Get("chat"); chat != "" {
		stream, err := messaging.StreamOfChat(p.Name, chat)
		if err != nil {
			return scope{}
		}
		return scope{chat: stream}
	}
	return scope{home: q.Get("scope") == "home"}
}

// chatEvent turns a messaging event into the SSE event this connection
// should get, if any. dirty reports whether the viewer's badges may have
// changed.
func (h *Handler) chatEvent(ctx context.Context, w chatWatcher, sc scope, p auth.Principal, ev messaging.ChatEvent) (name string, payload any, dirty bool) {
	if ev.Only != "" && ev.Only != p.Name {
		return "", nil, false
	}
	visible := h.d.Messaging.Visible(ctx, p, store.Entry{Stream: ev.Stream, Kind: store.KindMessage})
	if !visible {
		return "", nil, false
	}
	switch ev.Kind {
	case messaging.EventSeen:
		dirty = ev.By == p.Name
		if sc.chat == ev.Stream {
			return "seen", dto.Seen{Seq: ev.Seq, By: ev.By}, dirty
		}
	case messaging.EventAnswered:
		if ev.Answer != nil && (sc.chat == ev.Stream || sc.home) {
			return "answered", dto.Answered{Seq: ev.Seq, Answer: *ev.Answer}, true
		}
		return "", nil, true
	case messaging.EventChat:
		if ev.Gone || (sc.chat == "" && !sc.home) {
			return "", nil, true
		}
		chat, err := w.ChatOfStream(ctx, p, ev.Stream)
		if err != nil {
			return "", nil, true
		}
		return "chat", chat, true
	}
	return "", nil, dirty
}

// since is where replay starts: since_seq, or one after Last-Event-ID.
func since(r *http.Request) (int64, *dto.APIError) {
	if s := r.URL.Query().Get("since_seq"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return 0, dto.Errorf(dto.CodeBadRequest, "since_seq must be a positive integer.")
		}
		return n, nil
	}
	if s := r.Header.Get("Last-Event-ID"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= 0 {
			return n + 1, nil
		}
	}
	return 0, nil
}

// stream is one connection's writer.
type stream struct {
	w     http.ResponseWriter
	rc    *http.ResponseController
	buf   bytes.Buffer
	count int
}

func (s *stream) event(name, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.buf.Reset()
	s.buf.WriteString("event: " + name + "\n")
	if id != "" {
		s.buf.WriteString("id: " + id + "\n")
	}
	s.buf.WriteString("data: ")
	s.buf.Write(b)
	s.buf.WriteString("\n\n")
	return s.write()
}

func (s *stream) comment(text string) error {
	s.buf.Reset()
	s.buf.WriteString(": " + text + "\n\n")
	return s.write()
}

func (s *stream) write() error {
	if _, err := s.w.Write(s.buf.Bytes()); err != nil {
		return err
	}
	s.count++
	return s.rc.Flush()
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		dto.WriteError(w, dto.Errorf(dto.CodeUnauthenticated, "Sign in or send a valid token."))
		return
	}
	filter, apiErr := h.selection(ctx, r, p)
	if apiErr != nil {
		dto.WriteError(w, apiErr)
		return
	}
	defer h.Presence.Enter(p.Name)()
	from, apiErr := since(r)
	if apiErr != nil {
		dto.WriteError(w, apiErr)
		return
	}

	// Subscribe before reading the store, so nothing committed in between
	// is lost; the seq check drops what both deliver.
	var sub hub.Subscription
	if filter != nil {
		sub = h.d.Hub.Subscribe(*filter, h.Buffer)
		defer func() { sub.Close() }()
	}
	var statusCh <-chan dto.Stream
	if h.d.Status != nil {
		ch, stop := h.d.Status.Watch(64)
		defer stop()
		statusCh = ch
	}
	watcher, _ := h.d.Messaging.(chatWatcher)
	var chatEvents <-chan messaging.ChatEvent
	if watcher != nil {
		ch, stop := watcher.WatchChats(256)
		defer stop()
		chatEvents = ch
	}
	sc := scopeOf(r, p)
	var countsSub hub.Subscription
	if _, ok := h.d.Messaging.(view.Counter); ok {
		// Any message the viewer can see may change the badges.
		countsSub = h.d.Hub.Subscribe(h.messageFilter(ctx, &p), 256)
		defer countsSub.Close()
	}

	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream; charset=utf-8")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Accel-Buffering", "no")
	hd.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	s := &stream{w: w, rc: http.NewResponseController(w)}

	var lastCounts []byte
	sendCounts := func() error {
		c := view.Counts(ctx, h.d, p)
		b, _ := json.Marshal(c)
		if bytes.Equal(b, lastCounts) {
			return nil
		}
		lastCounts = b
		return s.event("counts", "", c)
	}
	if err := sendCounts(); err != nil {
		return
	}

	var last int64 // highest seq sent
	if filter != nil && from > 0 {
		var reset bool
		var err error
		if last, reset, err = h.replay(ctx, s, filter, p, from); err != nil || reset {
			return
		}
	}

	heartbeat := time.NewTicker(h.Heartbeat)
	defer heartbeat.Stop()
	countsTick := time.NewTicker(h.CountsEvery)
	defer countsTick.Stop()
	dirty := false

	var entries <-chan store.Entry
	var lagged <-chan struct{}
	if sub != nil {
		entries, lagged = sub.C(), sub.Lagged()
	}
	var countsEntries <-chan store.Entry
	var countsLagged <-chan struct{}
	if countsSub != nil {
		countsEntries, countsLagged = countsSub.C(), countsSub.Lagged()
	}
	for {
		var err error
		select {
		case <-ctx.Done():
			return
		case e := <-entries:
			if e.Seq <= last || !h.visible(ctx, p, e) {
				continue
			}
			last = e.Seq
			err = s.event("entry", strconv.FormatInt(e.Seq, 10), view.Present(ctx, h.d, h.kinds, p, e))
		case <-lagged:
			_ = s.event("reset", "", dto.Reset{Reason: "lagged"})
			return
		case st := <-statusCh:
			if store.IsMessageStream(st.ID) || !h.d.Projects.Access(ctx, p).Can(st.ID, auth.ScopeRead) {
				continue
			}
			dirty = true
			err = s.event("stream_status", "", h.enrich(ctx, st))
		case <-countsEntries:
			dirty = true
		case <-countsLagged:
			countsSub.Close()
			countsEntries, countsLagged = nil, nil
			dirty = true
		case ev := <-chatEvents:
			name, payload, changed := h.chatEvent(ctx, watcher, sc, p, ev)
			dirty = dirty || changed
			if name != "" {
				err = s.event(name, "", payload)
			}
		case <-countsTick.C:
			if dirty {
				dirty = false
				err = sendCounts()
			}
		case <-heartbeat.C:
			err = s.comment("ping")
		}
		if err != nil {
			return
		}
	}
}

// visible: an entry reaches a browser only when p may read it, whatever
// the hub let through: a message through Messaging.Visible, a log line
// through p's grants. Without messaging there are no messages.
func (h *Handler) visible(ctx context.Context, p auth.Principal, e store.Entry) bool {
	if e.Kind != store.KindMessage && !store.IsMessageStream(e.Stream) {
		return h.d.Projects.Access(ctx, p).Can(e.Stream, auth.ScopeRead)
	}
	return h.d.Messaging != nil && h.d.Messaging.Visible(ctx, p, e)
}

// replay sends what was committed from seq from on, up to ReplayMax
// entries; beyond that the client must reload, and gets a reset.
func (h *Handler) replay(ctx context.Context, s *stream, f *hub.Filter, p auth.Principal, from int64) (last int64, reset bool, err error) {
	ids, err := h.d.Store.MatchStreams(ctx, f.Streams)
	if err != nil {
		return 0, false, err
	}
	if f.Streams == nil {
		ids = nil
	} else if ids == nil {
		ids = []string{}
	}
	q := store.Query{Streams: ids, SinceSeq: from, Kinds: f.Kinds, Pattern: f.Pattern, Limit: h.ReplayMax}
	if f.MinSeverity < 7 {
		sev := f.MinSeverity
		q.MinSeverity = &sev
	}
	res, err := h.d.Store.Read(ctx, q)
	if err != nil {
		return 0, false, err
	}
	if res.HasMore {
		return 0, true, s.event("reset", "", dto.Reset{Reason: fmt.Sprintf("more than %d entries since seq %d", h.ReplayMax, from)})
	}
	last = from - 1
	for _, e := range res.Entries {
		last = max(last, e.Seq)
		if !h.visible(ctx, p, e) {
			continue
		}
		if err := s.event("entry", strconv.FormatInt(e.Seq, 10), view.Present(ctx, h.d, h.kinds, p, e)); err != nil {
			return 0, false, err
		}
	}
	return last, false, nil
}

// enrich adds to a status change what the status registry does not know:
// the last line, last seq and labels from the store, and the claim
// holder, so the Sources page shows them live. A missing claim means none.
func (h *Handler) enrich(ctx context.Context, st dto.Stream) dto.Stream {
	if row, err := h.d.Store.StreamRow(ctx, st.ID); err == nil {
		st.LastSeq, st.Labels = row.LastSeq, row.Labels
		st.LastLine = textclean.StripANSI(row.LastLine)
	}
	st.Claim = nil
	if cs, ok := h.d.Messaging.(mcpserver.ClaimSource); ok {
		if claims, err := cs.ActiveClaims(ctx); err == nil {
			if c, ok := claims[st.ID]; ok {
				st.Claim = &c
			}
		}
	}
	return st
}
