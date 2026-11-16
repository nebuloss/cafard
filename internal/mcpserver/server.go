// Package mcpserver is cafard's MCP server: the transports, the tool
// registry with per-scope tool lists, and the log tools.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/dto"
)

// Config is the `mcp:` config section.
type Config struct {
	// AllowedOrigins are extra Origin values accepted on /mcp, besides this
	// server's own origin and public_url.
	AllowedOrigins []string `yaml:"allowed_origins,omitempty"`
}

// Validate implements config.Validator.
func (c *Config) Validate() error {
	for _, o := range c.AllowedOrigins {
		u, err := url.Parse(o)
		if err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" {
			return fmt.Errorf("allowed_origins: %q is not an origin like https://host:port", o)
		}
	}
	return nil
}

// Instructions is the guide sent to every client at initialisation.
const Instructions = `Call whoami and list_channels first. When you work on a device or a test, use its channel: pass channel= to read, wait_for and search instead of listing streams, and post your findings there. Create a channel bound to the device for a new task if none fits. Keep the next_seq cursor from every read and pass it back as since_seq. Use wait_for instead of polling read. Check inbox at the start of a task and whenever you wait: the human drops photos and notes there, and other agents send results. Use ask_human when you need a physical action. Claim a device before driving it and release it after. Do not send acknowledgements or thanks to other agents.`

// Call is what a tool handler gets besides its arguments.
type Call struct {
	Principal auth.Principal
	Req       *mcp.CallToolRequest
}

// Progress sends a progress notification when the client asked for them.
func (c *Call) Progress(ctx context.Context, progress float64, message string) {
	if c.Req == nil || c.Req.Params == nil || c.Req.Session == nil {
		return
	}
	token := c.Req.Params.GetProgressToken()
	if token == nil {
		return
	}
	_ = c.Req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: token, Progress: progress, Message: message})
}

// Handler is a typed tool. text is what the agent reads; out is the
// structured content. Return a *dto.APIError for an error the agent should
// see as is.
type Handler[In, Out any] func(ctx context.Context, c *Call, in In) (text string, out Out, err error)

// ExtraContent is implemented by a tool output that carries content
// besides its text, such as the images of get_message. The structured
// content is still the output's JSON.
type ExtraContent interface {
	ExtraContent() []mcp.Content
}

type toolDef struct {
	scope   auth.Scope
	tool    *mcp.Tool
	handler mcp.ToolHandler
}

// Server holds the tools and builds one SDK server per scope set, so a
// token's tools/list only offers what it may call.
type Server struct {
	deps deps.Deps
	cfg  Config

	mu        sync.Mutex
	tools     []toolDef
	resources []resourceDef
	subs      Subscriptions
	servers   map[string]*principalServer
	sealed    bool
}

// New builds the server with the log tools registered.
func New(d deps.Deps, cfg Config) *Server {
	s := &Server{deps: d, cfg: cfg, servers: map[string]*principalServer{}}
	registerLogTools(s)
	return s
}

// Deps is what the server was built with, for tools added by other packages.
func (s *Server) Deps() deps.Deps { return s.deps }

// AddTool registers a tool that needs scope. Call it before serving.
func AddTool[In, Out any](s *Server, scope auth.Scope, t mcp.Tool, h Handler[In, Out]) {
	inSchema, err := jsonschema.For[In](schemaOptions)
	if err != nil {
		panic(fmt.Sprintf("tool %s: input schema: %v", t.Name, err))
	}
	outSchema, err := jsonschema.For[Out](schemaOptions)
	if err != nil {
		panic(fmt.Sprintf("tool %s: output schema: %v", t.Name, err))
	}
	t.InputSchema, t.OutputSchema = inSchema, outSchema
	raw := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		p, ok := principalOf(req)
		if !ok {
			return toolError(dto.Errorf(dto.CodeUnauthenticated, "This call carried no valid token.")), nil
		}
		if !p.Has(scope) {
			return toolError(auth.MissingScope(scope)), nil
		}
		var in In
		if args := req.Params.Arguments; len(args) > 0 && string(args) != "null" {
			dec := json.NewDecoder(bytes.NewReader(args))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&in); err != nil {
				return toolError(dto.Errorf(dto.CodeBadRequest, "The arguments of %s are not valid: %v.", t.Name, err)), nil
			}
		}
		// The principal rides in the context too, for code behind deps
		// interfaces (channel= auto-follows the agent that reads it).
		ctx = auth.WithPrincipal(ctx, p)
		text, out, err := h(ctx, &Call{Principal: p, Req: req}, in)
		if err != nil {
			var apiErr *dto.APIError
			if errors.As(err, &apiErr) {
				return toolError(apiErr), nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			s.deps.Log.Error("tool failed", "tool", t.Name, "principal", p.Name, "err", err)
			return toolError(dto.Errorf("internal", "%s failed inside cafard; the server log has the details.", t.Name)), nil
		}
		content := []mcp.Content{&mcp.TextContent{Text: text}}
		if extra, ok := any(out).(ExtraContent); ok {
			content = append(content, extra.ExtraContent()...)
		}
		return &mcp.CallToolResult{Content: content, StructuredContent: out}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sealed {
		panic("mcpserver.AddTool called after the server started serving")
	}
	tool := t
	s.tools = append(s.tools, toolDef{scope: scope, tool: &tool, handler: raw})
}

// schemaOptions describe the DTO types whose JSON is custom.
var schemaOptions = &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
	reflect.TypeFor[dto.Time]():        {Type: "string", Format: "date-time"},
	reflect.TypeFor[json.RawMessage](): {},
	reflect.TypeFor[dto.Attachment]():  {Type: "object"},
}}

func toolError(e *dto.APIError) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError:           true,
		Content:           []mcp.Content{&mcp.TextContent{Text: e.Message}},
		StructuredContent: e.Body(),
	}
}

const principalKey = "principal"

func principalOf(req *mcp.CallToolRequest) (auth.Principal, bool) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return auth.Principal{}, false
	}
	p, ok := req.Extra.TokenInfo.Extra[principalKey].(auth.Principal)
	return p, ok
}

// ToolNames lists the tools a scope set gets, for tests and docs.
func (s *Server) ToolNames(scopes auth.ScopeSet) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var names []string
	for _, t := range s.tools {
		if scopes.Has(t.scope) {
			names = append(names, t.tool.Name)
		}
	}
	slices.Sort(names)
	return names
}

// serverFor is the SDK server of one principal with its scopes. Servers
// are per principal, not per scope set, so a resource notification (an
// inbox changed) reaches only that principal's sessions.
func (s *Server) serverFor(p auth.Principal) *mcp.Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sealed = true
	key := p.Name + "\x00" + p.Scopes.String()
	if ps, ok := s.servers[key]; ok {
		return ps.srv
	}
	opts := &mcp.ServerOptions{
		Instructions: Instructions,
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	}
	if s.subs != nil {
		opts.SubscribeHandler = func(ctx context.Context, req *mcp.SubscribeRequest) error {
			return s.subs.Subscribe(ctx, p, req.Params.URI)
		}
		opts.UnsubscribeHandler = func(ctx context.Context, req *mcp.UnsubscribeRequest) error {
			s.subs.Unsubscribe(p, req.Params.URI)
			return nil
		}
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "cafard", Version: s.deps.Version}, opts)
	for _, t := range s.tools {
		if p.Scopes.Has(t.scope) {
			srv.AddTool(t.tool, t.handler)
		}
	}
	for _, r := range s.resources {
		if p.Scopes.Has(r.scope) {
			r.add(srv)
		}
	}
	s.servers[key] = &principalServer{principal: p.Name, srv: srv}
	return srv
}

// Handler serves streamable HTTP at /mcp: bearer tokens only, Origin
// checked, one session per client.
func (s *Server) Handler() http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		info := sdkauth.TokenInfoFromContext(r.Context())
		if info == nil {
			return nil
		}
		p, ok := info.Extra[principalKey].(auth.Principal)
		if !ok {
			return nil
		}
		return s.serverFor(p)
	}, &mcp.StreamableHTTPOptions{
		SessionTimeout: time.Hour,
		Logger:         s.deps.Log.With("component", "mcp"),
	})
	bearer := auth.BearerOnly{Next: s.deps.Auth}
	verify := func(_ context.Context, _ string, r *http.Request) (*sdkauth.TokenInfo, error) {
		p, err := bearer.FromRequest(r)
		if err != nil {
			return nil, sdkauth.ErrInvalidToken
		}
		if !p.Has(auth.ScopeRead) {
			return nil, sdkauth.ErrInvalidToken
		}
		// UserID ties a session to its principal, so another token cannot
		// take over a session id it learned.
		return &sdkauth.TokenInfo{Scopes: p.Scopes.Strings(), UserID: p.Name, Extra: map[string]any{principalKey: p}}, nil
	}
	protected := sdkauth.RequireBearerToken(verify, &sdkauth.RequireBearerTokenOptions{AllowMissingExpiration: true})(streamable)
	return s.checkOrigin(protected)
}

// checkOrigin enforces the MCP rule against DNS rebinding: a request with
// an Origin header must come from this server or an allowed origin.
func (s *Server) checkOrigin(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range s.cfg.AllowedOrigins {
		allowed[strings.ToLower(strings.TrimRight(o, "/"))] = true
	}
	if u, err := url.Parse(s.deps.Server.PublicURL); err == nil && u.Host != "" {
		allowed[strings.ToLower(u.Scheme+"://"+u.Host)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && !allowed[strings.ToLower(origin)] {
			u, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(u.Host, r.Host) {
				dto.WriteError(w, dto.Errorf(dto.CodeCrossOrigin, "Origin %s may not use /mcp. Add it to mcp.allowed_origins if it should.", origin))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Register mounts /mcp.
func Register(mux *http.ServeMux, s *Server) {
	h := s.Handler()
	mux.Handle("/mcp", h)
	s.deps.Log.Debug("mcp mounted", "tools", len(s.tools))
}
