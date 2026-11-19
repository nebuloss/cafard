// Package mcpproxy is `cafard mcp`: a stdio MCP server that forwards
// every request to a remote cafard over streamable HTTP, for clients that
// only speak stdio. It never opens a database; the remote server does
// all the work and checks every scope.
package mcpproxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/dto"
)

// Options configure a proxy.
type Options struct {
	Server  string // base URL of the remote cafard, e.g. http://10.0.50.2:8790
	Token   string
	Version string
	Log     *slog.Logger
}

// endpoint is the remote /mcp address, checked.
func (o Options) endpoint() (string, error) {
	if o.Server == "" {
		return "", errors.New("--server (or CAFARD_SERVER) is required")
	}
	if o.Token == "" {
		return "", errors.New("--token (or CAFARD_TOKEN) is required")
	}
	u, err := url.Parse(strings.TrimRight(o.Server, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("--server %q must be an http:// or https:// URL", o.Server)
	}
	if !strings.HasSuffix(u.Path, "/mcp") {
		u.Path += "/mcp"
	}
	return u.String(), nil
}

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// Proxy is one stdio session's link to the remote server.
type Proxy struct {
	opts     Options
	endpoint string
	http     *http.Client
	log      *slog.Logger
	local    *mcp.Server

	mu       sync.Mutex
	closed   bool
	remote   *mcp.ClientSession
	subs     map[string]bool        // URIs the local client subscribed to
	progress map[string]progressFor // remote progress token -> local session
}

type progressFor struct {
	session *mcp.ServerSession
	token   any
}

// New connects to the remote server and builds the local server from what
// it offers: the same tools, resources, templates and instructions.
func New(ctx context.Context, opts Options) (*Proxy, error) {
	endpoint, err := opts.endpoint()
	if err != nil {
		return nil, err
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	p := &Proxy{opts: opts, endpoint: endpoint, log: opts.Log,
		http: &http.Client{Transport: bearer{token: opts.Token, next: http.DefaultTransport}},
		subs: map[string]bool{}, progress: map[string]progressFor{}}
	remote, err := p.connect(ctx)
	if err != nil {
		return nil, err
	}
	p.remote = remote
	if err := p.build(ctx, remote); err != nil {
		remote.Close()
		return nil, err
	}
	return p, nil
}

func (p *Proxy) connect(ctx context.Context) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "cafard-mcp-proxy", Version: p.opts.Version}, &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{},
		ResourceUpdatedHandler: func(ctx context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
			if p.local != nil {
				if err := p.local.ResourceUpdated(ctx, req.Params); err != nil {
					p.log.Debug("forwarding a resource update failed", "err", err)
				}
			}
		},
		ProgressNotificationHandler: func(ctx context.Context, req *mcp.ProgressNotificationClientRequest) {
			p.mu.Lock()
			to, ok := p.progress[key(req.Params.ProgressToken)]
			p.mu.Unlock()
			if !ok {
				return
			}
			params := *req.Params
			params.ProgressToken = to.token
			_ = to.session.NotifyProgress(ctx, &params)
		},
	})
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: p.endpoint, HTTPClient: p.http}, nil)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", p.endpoint, err)
	}
	go p.watch(cs)
	return cs, nil
}

// watch reconnects in the background when the remote session ends on its
// own (the server restarted), so the next call finds a live session
// instead of failing once.
func (p *Proxy) watch(cs *mcp.ClientSession) {
	_ = cs.Wait()
	delay := 500 * time.Millisecond
	for {
		p.mu.Lock()
		current, closed := p.remote == cs, p.closed
		p.mu.Unlock()
		if !current || closed {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := p.reconnect(ctx, cs)
		cancel()
		if err == nil {
			return
		}
		p.log.Warn("the remote cafard is unreachable; retrying", "err", err)
		time.Sleep(delay)
		delay = min(2*delay, 30*time.Second)
	}
}

func key(token any) string { return fmt.Sprintf("%T:%v", token, token) }

// build mirrors the remote's offer on the local server.
func (p *Proxy) build(ctx context.Context, remote *mcp.ClientSession) error {
	init := remote.InitializeResult()
	info := &mcp.Implementation{Name: "cafard", Version: p.opts.Version}
	opts := &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}}
	subscribe := false
	if init != nil {
		if init.ServerInfo != nil {
			info = &mcp.Implementation{Name: init.ServerInfo.Name, Title: init.ServerInfo.Title, Version: init.ServerInfo.Version}
		}
		opts.Instructions = init.Instructions
		subscribe = init.Capabilities != nil && init.Capabilities.Resources != nil && init.Capabilities.Resources.Subscribe
	}
	if subscribe {
		opts.SubscribeHandler = p.subscribe
		opts.UnsubscribeHandler = p.unsubscribe
	}
	var tools []*mcp.Tool
	paths := false
	for tool, err := range remote.Tools(ctx, nil) {
		if err != nil {
			return fmt.Errorf("listing the remote tools: %w", err)
		}
		// The remote cannot read this machine's disk, so the proxy
		// offers file paths for images and reads them itself.
		if t, ok := withImagePaths(tool); ok {
			tool, paths = t, true
		}
		tools = append(tools, tool)
	}
	if paths {
		opts.Instructions = joinInstructions(opts.Instructions)
	}
	p.local = mcp.NewServer(info, opts)
	for _, tool := range tools {
		p.local.AddTool(tool, p.callTool)
	}
	if init != nil && init.Capabilities != nil && init.Capabilities.Resources != nil {
		for res, err := range remote.Resources(ctx, nil) {
			if err != nil {
				return fmt.Errorf("listing the remote resources: %w", err)
			}
			p.local.AddResource(res, p.readResource)
		}
		for tmpl, err := range remote.ResourceTemplates(ctx, nil) {
			if err != nil {
				return fmt.Errorf("listing the remote resource templates: %w", err)
			}
			p.local.AddResourceTemplate(tmpl, p.readResource)
		}
	}
	return nil
}

// Run serves the local side on t (stdio in `cafard mcp`) until the client
// leaves or ctx ends.
func (p *Proxy) Run(ctx context.Context, t mcp.Transport) error {
	defer p.Close()
	return p.local.Run(ctx, t)
}

// Close ends the remote session.
func (p *Proxy) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.remote == nil {
		return nil
	}
	err := p.remote.Close()
	p.remote = nil
	return err
}

// withRemote runs f on the remote session. A remote that forgot the
// session (it restarted) refused the request before running it, so the
// proxy reconnects, subscribes again and retries once.
func (p *Proxy) withRemote(ctx context.Context, f func(*mcp.ClientSession) error) error {
	p.mu.Lock()
	cs := p.remote
	p.mu.Unlock()
	if cs == nil {
		return mcp.ErrConnectionClosed
	}
	err := f(cs)
	if !errors.Is(err, mcp.ErrSessionMissing) && !errors.Is(err, mcp.ErrConnectionClosed) {
		return err
	}
	retry := errors.Is(err, mcp.ErrSessionMissing)
	fresh, cerr := p.reconnect(ctx, cs)
	if cerr != nil {
		p.log.Warn("the remote cafard is unreachable", "err", cerr)
		return err
	}
	if !retry {
		// The request may have run before the connection dropped; do
		// not send it twice.
		return err
	}
	return f(fresh)
}

func (p *Proxy) reconnect(ctx context.Context, old *mcp.ClientSession) (*mcp.ClientSession, error) {
	p.mu.Lock()
	if p.remote != old {
		cs := p.remote
		p.mu.Unlock()
		return cs, nil // someone else reconnected already
	}
	p.mu.Unlock()
	fresh, err := p.connect(ctx)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.closed || p.remote != old {
		p.mu.Unlock()
		fresh.Close()
		if p.closed {
			return nil, mcp.ErrConnectionClosed
		}
		return p.current(), nil
	}
	old.Close()
	p.remote = fresh
	uris := make([]string, 0, len(p.subs))
	for uri := range p.subs {
		uris = append(uris, uri)
	}
	p.mu.Unlock()
	p.log.Info("reconnected to the remote cafard")
	for _, uri := range uris {
		if err := fresh.Subscribe(ctx, &mcp.SubscribeParams{URI: uri}); err != nil {
			p.log.Warn("subscribing again failed", "uri", uri, "err", err)
		}
	}
	return fresh, nil
}

func (p *Proxy) current() *mcp.ClientSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.remote
}

func (p *Proxy) callTool(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.Params.Arguments
	if req.Params.Name == sendTool {
		var apiErr *dto.APIError
		if args, apiErr = rewriteImagePaths(args); apiErr != nil {
			return toolError(apiErr), nil
		}
	}
	params := &mcp.CallToolParams{Meta: ownMeta(req.Params.Meta), Name: req.Params.Name, Arguments: args}
	if token := req.Params.GetProgressToken(); token != nil && req.Session != nil {
		// The remote sees the local token; its notifications are mapped
		// back to the session that asked.
		k := key(token)
		p.mu.Lock()
		p.progress[k] = progressFor{session: req.Session, token: token}
		p.mu.Unlock()
		// Notifications are handled apart from responses, so the last
		// ones may be processed just after the result: keep the mapping
		// a little longer.
		defer time.AfterFunc(2*time.Second, func() {
			p.mu.Lock()
			delete(p.progress, k)
			p.mu.Unlock()
		})
	}
	var res *mcp.CallToolResult
	err := p.withRemote(ctx, func(cs *mcp.ClientSession) error {
		var err error
		res, err = cs.CallTool(ctx, params)
		return err
	})
	if res != nil {
		res.Meta = ownMeta(res.Meta)
	}
	return res, err
}

func (p *Proxy) readResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	var res *mcp.ReadResourceResult
	err := p.withRemote(ctx, func(cs *mcp.ClientSession) error {
		var err error
		res, err = cs.ReadResource(ctx, &mcp.ReadResourceParams{Meta: ownMeta(req.Params.Meta), URI: req.Params.URI})
		return err
	})
	if res != nil {
		res.Meta = ownMeta(res.Meta)
	}
	return res, err
}

func (p *Proxy) subscribe(ctx context.Context, req *mcp.SubscribeRequest) error {
	err := p.withRemote(ctx, func(cs *mcp.ClientSession) error {
		return cs.Subscribe(ctx, &mcp.SubscribeParams{URI: req.Params.URI})
	})
	if err == nil {
		p.mu.Lock()
		p.subs[req.Params.URI] = true
		p.mu.Unlock()
	}
	return err
}

func (p *Proxy) unsubscribe(ctx context.Context, req *mcp.UnsubscribeRequest) error {
	p.mu.Lock()
	delete(p.subs, req.Params.URI)
	p.mu.Unlock()
	return p.withRemote(ctx, func(cs *mcp.ClientSession) error {
		return cs.Unsubscribe(ctx, &mcp.UnsubscribeParams{URI: req.Params.URI})
	})
}

// ownMeta drops the protocol's own _meta keys (protocol version, client
// and server info): each side of the proxy negotiates its own, and the
// remote refuses a version it did not agree to. The rest, the progress
// token included, passes.
func ownMeta(m mcp.Meta) mcp.Meta {
	if len(m) == 0 {
		return m
	}
	out := mcp.Meta{}
	for k, v := range m {
		if !strings.HasPrefix(k, "io.modelcontextprotocol/") {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
