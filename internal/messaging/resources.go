package messaging

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/hub"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/textclean"
)

// Resource URIs (PLAN section 7).
const (
	uriStreams       = "cafard://streams"
	uriStreamPrefix  = "cafard://stream/"
	uriMessagePrefix = "cafard://message/"
	uriInbox         = "cafard://inbox"
	uriChannelPrefix = "cafard://channel/"
	notifyEvery      = 2 * time.Second
	streamLines      = 200
	channelRows      = 50
)

func textResource(uri, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/plain", Text: text}}}
}

func (s *Service) registerResources(m *mcpserver.Server) {
	s.mcp = m
	r := auth.ScopeRead
	m.AddResource(r, mcp.Resource{URI: uriStreams, Name: "streams", Description: "The log sources and their status.", MIMEType: "text/plain"}, s.resStreams)
	m.AddResource(r, mcp.Resource{URI: uriInbox, Name: "inbox", Description: "Your unread messages. Subscribe to be told when it changes.", MIMEType: "text/plain"}, s.resInbox)
	m.AddResourceTemplate(r, mcp.ResourceTemplate{URITemplate: uriStreamPrefix + "{+id}", Name: "stream", Description: "The last 200 lines of a log stream, cleaned. Subscribe to be told about new lines.", MIMEType: "text/plain"}, s.resStream)
	m.AddResourceTemplate(r, mcp.ResourceTemplate{URITemplate: uriMessagePrefix + "{seq}", Name: "message", Description: "One message.", MIMEType: "text/plain"}, s.resMessage)
	m.AddResourceTemplate(r, mcp.ResourceTemplate{URITemplate: uriChannelPrefix + "{+name}", Name: "channel", Description: "A channel: topic, bindings and its last 50 timeline rows.", MIMEType: "text/plain"}, s.resChannel)
	m.SetSubscriptions(s)
}

func (s *Service) resStreams(ctx context.Context, _ auth.Principal, uri string) (*mcp.ReadResourceResult, error) {
	rows, err := s.st.Streams(ctx)
	if err != nil {
		return nil, err
	}
	status := map[string]dto.Stream{}
	if s.status != nil {
		for _, st := range s.status.Snapshot() {
			status[st.ID] = st
		}
	}
	claims, err := s.ActiveClaims(ctx)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, r := range rows {
		if store.IsMessageStream(r.ID) {
			continue
		}
		st := dto.StreamIdle
		rate := 0.0
		if live, ok := status[r.ID]; ok {
			st, rate = live.Status, live.RatePerMin
		}
		fmt.Fprintf(&b, "%s %s %.1f/min last_seq=%d", r.ID, st, rate, r.LastSeq)
		if c, ok := claims[r.ID]; ok {
			fmt.Fprintf(&b, " claimed_by=%s", c.Principal)
		}
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		b.WriteString("No log streams yet.\n")
	}
	return textResource(uri, b.String()), nil
}

func (s *Service) resStream(ctx context.Context, _ auth.Principal, uri string) (*mcp.ReadResourceResult, error) {
	id, err := url.PathUnescape(strings.TrimPrefix(uri, uriStreamPrefix))
	if err != nil || store.IsMessageStream(id) || store.ValidateStreamID(id) != nil {
		return nil, dto.Errorf(dto.CodeNotFound, "no such stream")
	}
	res, err := s.st.Read(ctx, store.Query{Streams: []string{id}, Desc: true, Limit: streamLines, Kinds: logKinds})
	if err != nil {
		return nil, err
	}
	if len(res.Entries) == 0 {
		return nil, dto.Errorf(dto.CodeNotFound, "no such stream")
	}
	f := textclean.Format(res.Entries, textclean.AgentDefaults, time.UTC)
	return textResource(uri, f.Text+fmt.Sprintf("Continue with read(streams=[%q], since_seq=%d).\n", id, res.Entries[len(res.Entries)-1].Seq+1)), nil
}

func (s *Service) resMessage(ctx context.Context, p auth.Principal, uri string) (*mcp.ReadResourceResult, error) {
	seq, err := strconv.ParseInt(strings.TrimPrefix(uri, uriMessagePrefix), 10, 64)
	if err != nil {
		return nil, dto.Errorf(dto.CodeNotFound, "no such message")
	}
	e, err := s.Message(ctx, p, seq)
	if err != nil {
		return nil, err
	}
	text := s.summary(ctx, p, e) + "\n" + e.Text + "\n"
	if len(e.Attachments) > 0 {
		text += fmt.Sprintf("Attachments: get_message(seq=%d) shows them.\n", e.Seq)
	}
	return textResource(uri, text), nil
}

func (s *Service) resInbox(ctx context.Context, p auth.Principal, uri string) (*mcp.ReadResourceResult, error) {
	list, more, err := s.Inbox(ctx, p, InboxQuery{Limit: 50})
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, e := range list {
		b.WriteString(s.summary(ctx, p, e))
		b.WriteByte('\n')
	}
	if len(list) == 0 {
		b.WriteString("Your inbox is empty.\n")
	} else if more {
		b.WriteString("[more: use the inbox tool]\n")
	}
	return textResource(uri, b.String()), nil
}

func (s *Service) resChannel(ctx context.Context, p auth.Principal, uri string) (*mcp.ReadResourceResult, error) {
	name, err := url.PathUnescape(strings.TrimPrefix(uri, uriChannelPrefix))
	if err != nil {
		return nil, dto.Errorf(dto.CodeNotFound, "no such channel")
	}
	ch, err := s.channel(ctx, name)
	if err != nil {
		return nil, dto.Errorf(dto.CodeNotFound, "no such channel")
	}
	chat, err := s.ChannelChat(ctx, p, ch)
	if err != nil {
		return nil, err
	}
	page, err := s.Timeline(ctx, p, ch.Stream(), TimelineQuery{BeforeSeq: 1 << 62, Limit: channelRows})
	if err != nil {
		return nil, err
	}
	entries := page.entries()
	text := channelLine(chat) + "\n"
	if len(entries) > 0 {
		text += textclean.Format(entries, textclean.AgentDefaults, time.UTC).Text
	}
	return textResource(uri, text), nil
}

// subscription is one principal's subscriptions to one URI.
type subscription struct {
	p     auth.Principal
	count int
}

// Subscribe implements mcpserver.Subscriptions: cafard://inbox and
// cafard://stream/{id} can be subscribed to.
func (s *Service) Subscribe(_ context.Context, p auth.Principal, uri string) error {
	if uri != uriInbox && !strings.HasPrefix(uri, uriStreamPrefix) {
		return fmt.Errorf("only %s and %s{id} can be subscribed to", uriInbox, uriStreamPrefix)
	}
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	m := s.subs[uri]
	if m == nil {
		m = map[string]*subscription{}
		s.subs[uri] = m
	}
	if sub := m[p.Name]; sub != nil {
		sub.count++
	} else {
		m[p.Name] = &subscription{p: p, count: 1}
	}
	return nil
}

// Unsubscribe implements mcpserver.Subscriptions.
func (s *Service) Unsubscribe(p auth.Principal, uri string) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	if sub := s.subs[uri][p.Name]; sub != nil {
		if sub.count--; sub.count <= 0 {
			delete(s.subs[uri], p.Name)
		}
	}
}

// throttle sends at most one notification per key every notifyEvery,
// with a trailing one so the last change is never lost.
type throttle struct {
	mu      sync.Mutex
	last    map[string]time.Time
	pending map[string]bool
}

func (t *throttle) do(key string, send func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending[key] {
		return
	}
	wait := notifyEvery - time.Since(t.last[key])
	if wait <= 0 {
		t.last[key] = time.Now()
		go send()
		return
	}
	t.pending[key] = true
	time.AfterFunc(wait, func() {
		t.mu.Lock()
		delete(t.pending, key)
		t.last[key] = time.Now()
		t.mu.Unlock()
		send()
	})
}

// watchResources tells subscribers about new lines and inbox messages
// until ctx ends.
func (s *Service) watchResources(ctx context.Context) error {
	if s.mcp == nil {
		return nil
	}
	th := &throttle{last: map[string]time.Time{}, pending: map[string]bool{}}
	// Two subscriptions: an empty stream list is every log stream, and
	// message streams are only matched by their own prefixes.
	logs := hub.Filter{MinSeverity: 7}
	msgs := hub.Filter{Streams: []string{"chan:*", "dm:*"}, Kinds: []store.Kind{store.KindMessage}, MinSeverity: 7}
	ls, ms := s.hub.Subscribe(logs, 4096), s.hub.Subscribe(msgs, 1024)
	defer func() { ls.Close(); ms.Close() }()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ls.Lagged():
			ls.Close()
			ls = s.hub.Subscribe(logs, 4096)
		case <-ms.Lagged():
			ms.Close()
			ms = s.hub.Subscribe(msgs, 1024)
		case e := <-ls.C():
			s.notifyEntry(ctx, th, e)
		case e := <-ms.C():
			s.notifyEntry(ctx, th, e)
		}
	}
}

func (s *Service) notifyEntry(ctx context.Context, th *throttle, e store.Entry) {
	if !store.IsMessageStream(e.Stream) {
		uri := uriStreamPrefix + e.Stream
		s.subsMu.Lock()
		n := len(s.subs[uri])
		s.subsMu.Unlock()
		if n > 0 {
			th.do(uri, func() { s.mcp.ResourceUpdated(context.WithoutCancel(ctx), "", uri) })
		}
		return
	}
	if e.Kind != store.KindMessage {
		return
	}
	s.subsMu.Lock()
	var who []auth.Principal
	for _, sub := range s.subs[uriInbox] {
		who = append(who, sub.p)
	}
	s.subsMu.Unlock()
	for _, p := range who {
		if ok, err := s.inInbox(ctx, p, e); err != nil || !ok {
			continue
		}
		name := p.Name
		th.do(name+"\x00"+uriInbox, func() { s.mcp.ResourceUpdated(context.WithoutCancel(ctx), name, uriInbox) })
	}
}
