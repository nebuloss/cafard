package mcpproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/dto"
)

// toyRemote is a small streamable HTTP MCP server standing in for cafard:
// a tool that reports progress, a resource, and a bearer check.
type toyRemote struct {
	cancelled chan struct{}
	sent      chan json.RawMessage // the arguments of each send
	srv       *httptest.Server
	handler   atomic.Pointer[http.Handler]
	sessions  atomic.Int32
}

func newToyRemote(t *testing.T) *toyRemote {
	r := &toyRemote{cancelled: make(chan struct{}, 1), sent: make(chan json.RawMessage, 8)}
	r.reset()
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer cfd_toy" {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		(*r.handler.Load()).ServeHTTP(w, req)
	}))
	t.Cleanup(func() {
		r.srv.CloseClientConnections()
		r.srv.Close()
	})
	return r
}

// reset replaces the server, as a restart does: old sessions are gone.
func (r *toyRemote) reset() {
	s := mcp.NewServer(&mcp.Implementation{Name: "cafard", Version: "toy"}, &mcp.ServerOptions{
		Instructions:       "Call whoami first.",
		SubscribeHandler:   func(context.Context, *mcp.SubscribeRequest) error { return nil },
		UnsubscribeHandler: func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
	})
	mcp.AddTool(s, &mcp.Tool{Name: "slow", Description: "reports progress"}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		if tok := req.Params.GetProgressToken(); tok != nil {
			for i := range 2 {
				_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: tok, Progress: float64(i + 1), Message: "waiting"})
			}
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, struct{}{}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "block", Description: "waits until cancelled"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		<-ctx.Done()
		r.cancelled <- struct{}{}
		return nil, struct{}{}, ctx.Err()
	})
	mcp.AddTool(s, &mcp.Tool{Name: "send", Description: "sends"}, func(_ context.Context, req *mcp.CallToolRequest, in toySend) (*mcp.CallToolResult, struct{}, error) {
		r.sent <- req.Params.Arguments
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("sent with %d images", len(in.Images))}}}, struct{}{}, nil
	})
	s.AddResource(&mcp.Resource{URI: "cafard://inbox", Name: "inbox"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "cafard://inbox", Text: "empty"}}}, nil
	})
	var h http.Handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { r.sessions.Add(1); return s }, nil)
	r.handler.Store(&h)
}

type toySend struct {
	Text   string            `json:"text"`
	Images []dto.ImageUpload `json:"images,omitempty"`
	// Section stands for any send argument the proxy does not know.
	Section string `json:"section,omitempty"`
}

func connectThrough(t *testing.T, p *Proxy, opts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	local, remote := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx, local) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "stdio-client", Version: "1"}, opts).Connect(context.Background(), remote, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cs.Close()
		cancel()
		<-done
	})
	return cs
}

func TestProgressReachesTheLocalClientWithItsOwnToken(t *testing.T) {
	r := newToyRemote(t)
	p, err := New(context.Background(), Options{Server: r.srv.URL, Token: "cfd_toy", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var tokens []any
	cs := connectThrough(t, p, &mcp.ClientOptions{ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
		mu.Lock()
		tokens = append(tokens, req.Params.ProgressToken)
		mu.Unlock()
	}})
	if got := cs.InitializeResult().Instructions; got != "Call whoami first.\n\n"+Instructions {
		t.Errorf("instructions = %q", got)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Meta: mcp.Meta{"progressToken": "mine-7"}, Name: "slow"})
	if err != nil || res.IsError {
		t.Fatalf("call = %v %v", res, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(tokens)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(tokens) < 2 || tokens[0] != "mine-7" {
		t.Errorf("progress tokens seen locally: %v", tokens)
	}
}

func TestTheProxyReconnectsWhenTheRemoteForgetsItsSession(t *testing.T) {
	r := newToyRemote(t)
	p, err := New(context.Background(), Options{Server: r.srv.URL, Token: "cfd_toy"})
	if err != nil {
		t.Fatal(err)
	}
	cs := connectThrough(t, p, nil)
	if err := cs.Subscribe(context.Background(), &mcp.SubscribeParams{URI: "cafard://inbox"}); err != nil {
		t.Fatal(err)
	}
	before := r.sessions.Load()
	r.reset()
	r.srv.CloseClientConnections() // the restart drops every connection
	// The call that finds the session gone may fail once, when the SDK
	// noticed first and closed the session; the proxy is reconnected for
	// the next one either way.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "slow"})
	if err != nil {
		time.Sleep(100 * time.Millisecond)
		res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "slow"})
	}
	if err != nil || res.IsError {
		t.Fatalf("call after the remote restarted = %v %v", res, err)
	}
	if r.sessions.Load() <= before {
		t.Error("no new remote session was opened")
	}
	if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "cafard://inbox"}); err != nil {
		t.Errorf("read after reconnect: %v", err)
	}
}

func TestOptionsAreChecked(t *testing.T) {
	for _, o := range []Options{{Token: "t"}, {Server: "http://x"}, {Server: "ftp://x", Token: "t"}} {
		if _, err := o.endpoint(); err == nil {
			t.Errorf("%+v: want an error", o)
		}
	}
	if e, _ := (Options{Server: "http://h:8790/", Token: "t"}).endpoint(); e != "http://h:8790/mcp" {
		t.Errorf("endpoint = %q", e)
	}
}

func TestACancelledCallIsCancelledOnTheRemote(t *testing.T) {
	r := newToyRemote(t)
	p, err := New(context.Background(), Options{Server: r.srv.URL, Token: "cfd_toy"})
	if err != nil {
		t.Fatal(err)
	}
	cs := connectThrough(t, p, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "block"}); err == nil {
		t.Fatal("want the call to end with its context")
	}
	select {
	case <-r.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the remote call went on after the local client cancelled it")
	}
}
