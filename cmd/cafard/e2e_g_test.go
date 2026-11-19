package main

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSearchFollowsChannelsAndNeverLeaksADirectMessage(t *testing.T) {
	s := startServer(t)
	_, a := s.agent("agent-a")
	_, b := s.agent("agent-b")
	_, c := s.agent("agent-c")
	call(t, a, "create_channel", map[string]any{"name": "dut1", "streams": []string{"push:lab/dut1"}})
	s.ingest("push:lab/dut1", "\x1b[33musb 1-1: USB disconnect, device number 2\x1b[0m\nboot ok\n")
	s.ingest("push:lab/bench", "usb hub on the bench\n")
	call(t, a, "send", map[string]any{"text": "usb looks flaky on dut1", "channel": "dut1"})
	call(t, a, "send", map[string]any{"text": "usb secret for b only", "to": "agent-b"})

	text, out := call(t, c, "search", map[string]any{"query": "usb"})
	if !strings.Contains(text, "USB disconnect") || !strings.Contains(text, "hub on the bench") || strings.Contains(text, "flaky") || strings.Contains(text, "secret") {
		t.Errorf("search without channel = %q", text)
	}
	if num(out["shown"]) != 2 {
		t.Errorf("shown = %v", out["shown"])
	}
	text, _ = call(t, c, "search", map[string]any{"query": "usb", "channel": "dut1"})
	if !strings.Contains(text, "USB disconnect") || !strings.Contains(text, "flaky") || strings.Contains(text, "bench") || strings.Contains(text, "secret") {
		t.Errorf("search in #dut1 = %q", text)
	}
	if strings.Index(text, "USB disconnect") > strings.Index(text, "flaky") {
		t.Errorf("not in seq order: %q", text)
	}
	for name, cs := range map[string]*mcp.ClientSession{"agent-b": b, "agent-c": c} {
		for _, args := range []map[string]any{
			{"query": "secret"},
			{"query": "secret", "channel": "dut1"},
			{"query": "secret", "channel": "general"},
			{"query": "secret", "streams": []string{"dm:*", "*"}},
		} {
			if text, _ := call(t, cs, "search", args); strings.Contains(text, "for b only") {
				t.Errorf("%s found the direct message with %v: %q", name, args, text)
			}
		}
	}
	if text, _ := call(t, c, "search", map[string]any{"query": "disconnect", "from": "-1h", "to": "now"}); !strings.Contains(text, "USB disconnect") {
		t.Errorf("from=-1h = %q", text)
	}
	if text, _ := call(t, c, "search", map[string]any{"query": "disconnect", "to": "-1h"}); strings.Contains(text, "USB disconnect") {
		t.Errorf("to=-1h = %q", text)
	}
	msg := callFails(t, c, "search", map[string]any{"query": `"usb`}, "bad_request")
	if !strings.Contains(msg, "not a valid full-text query") {
		t.Errorf("bad query message = %q", msg)
	}
	callFails(t, c, "search", map[string]any{"query": "usb", "from": "yesterday"}, "bad_request")
	callFails(t, c, "search", map[string]any{"query": "usb", "channel": "nope"}, "unknown_channel")
}

// proxied runs `cafard mcp` over pipes and connects an SDK client to it.
func (s *server) proxied(token string, opts *mcp.ClientOptions) (*mcp.ClientSession, <-chan error) {
	s.t.Helper()
	toProxy, fromClient := io.Pipe()
	toClient, fromProxy := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var stderr syncBuffer
	go func() {
		err := run(ctx, []string{"mcp", "--server", s.base}, toProxy, fromProxy, &stderr, func(k string) string {
			if k == "CAFARD_TOKEN" {
				return token
			}
			return ""
		})
		fromProxy.Close()
		done <- err
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-only", Version: "1"}, opts)
	cs, err := client.Connect(context.Background(), &mcp.IOTransport{Reader: toClient, Writer: fromClient}, nil)
	if err != nil {
		cancel()
		s.t.Fatalf("connecting through cafard mcp: %v (stderr: %s)", err, stderr.String())
	}
	s.t.Cleanup(func() {
		cs.Close()
		cancel()
	})
	return cs, done
}

func TestTheStdioProxyForwardsToolsResourcesAndSubscriptions(t *testing.T) {
	s := startServer(t)
	tok, _ := s.agent("stdio-agent")
	_, other := s.agent("other-agent")
	var mu sync.Mutex
	updated := map[string]int{}
	cs, done := s.proxied(tok, &mcp.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
			mu.Lock()
			updated[req.Params.URI]++
			mu.Unlock()
		},
	})
	if got := cs.InitializeResult().Instructions; !strings.Contains(got, "whoami") {
		t.Errorf("instructions = %q", got)
	}
	names := toolNames(t, cs)
	for _, want := range []string{"whoami", "read", "wait_for", "search", "send", "inbox"} {
		if !strings.Contains(strings.Join(names, ","), want) {
			t.Errorf("tools through the proxy: %v, missing %s", names, want)
		}
	}
	if text, _ := call(t, cs, "whoami", nil); !strings.Contains(text, "stdio-agent") {
		t.Errorf("whoami = %q", text)
	}

	// wait_for through the proxy, released by a line pushed afterwards,
	// with a progress token on the call.
	type result struct {
		text string
		out  map[string]any
	}
	waited := make(chan result, 1)
	go func() {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Meta: mcp.Meta{"progressToken": "wait-1"}, Name: "wait_for",
			Arguments: map[string]any{"pattern": "login:", "streams": []string{"push:lab/proxied"}, "timeout_s": 20},
		})
		if err != nil {
			waited <- result{text: err.Error()}
			return
		}
		var text strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text.WriteString(tc.Text)
			}
		}
		out, _ := res.StructuredContent.(map[string]any)
		waited <- result{text.String(), out}
	}()
	time.Sleep(300 * time.Millisecond)
	s.ingest("push:lab/proxied", "booting\nbuildroot login:\n")
	select {
	case r := <-waited:
		if r.out["matched"] != true || !strings.Contains(r.text, "buildroot login:") {
			t.Errorf("wait_for through the proxy = %q %v", r.text, r.out)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("wait_for through the proxy was not released")
	}

	// Tool errors come back as tool errors, with their code.
	callFails(t, cs, "search", map[string]any{"query": `"open`}, "bad_request")

	// Resources and templates.
	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "cafard://stream/push:lab/proxied"})
	if err != nil || !strings.Contains(res.Contents[0].Text, "buildroot login:") {
		t.Fatalf("stream resource = %v, %v", res, err)
	}
	if err := cs.Subscribe(context.Background(), &mcp.SubscribeParams{URI: "cafard://inbox"}); err != nil {
		t.Fatalf("subscribe through the proxy: %v", err)
	}
	call(t, other, "send", map[string]any{"text": "ping through the proxy", "to": "stdio-agent"})
	eventually(t, "the inbox notification through the proxy", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return updated["cafard://inbox"] > 0
	})
	if text, _ := call(t, cs, "inbox", nil); !strings.Contains(text, "ping through the proxy") {
		t.Errorf("inbox = %q", text)
	}

	cs.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("cafard mcp after the client left: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("cafard mcp did not stop when its client left")
	}
}

func TestTheStdioProxyRefusesABadToken(t *testing.T) {
	s := startServer(t)
	err := run(context.Background(), []string{"mcp", "--server", s.base, "--token", "cfd_1_nope"}, strings.NewReader(""), io.Discard, io.Discard, func(string) string { return "" })
	if err == nil {
		t.Fatal("want an error for a token the server refuses")
	}
}
