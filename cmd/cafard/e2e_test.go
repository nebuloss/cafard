package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/config"
	"github.com/nebuloss/cafard/internal/push"
	"github.com/nebuloss/cafard/internal/store"
)

// server is a whole cafard started on a loopback port for one test.
type server struct {
	t      *testing.T
	base   string
	admin  string
	dir    string
	cancel context.CancelFunc
}

var tokenLine = regexp.MustCompile(`(?m)^\s+(cfd_[A-Za-z0-9_-]+)$`)

func startServer(t *testing.T) *server {
	t.Helper()
	cfg := config.Default()
	cfg.HTTPListen = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	addrs := make(chan net.Addr, 1)
	done := make(chan error, 1)
	var out syncBuffer
	go func() {
		done <- serve(ctx, &cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), &out, func(a net.Addr) { addrs <- a })
	}()
	var addr net.Addr
	select {
	case addr = <-addrs:
	case err := <-done:
		t.Fatalf("serve: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not start")
	}
	m := tokenLine.FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no admin token printed on first start:\n%s", out.String())
	}
	s := &server{t: t, base: "http://" + addr.String(), admin: m[1], dir: cfg.DataDir, cancel: cancel}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("server did not stop")
		}
	})
	return s
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// token creates a principal and a token in the running server's database.
func (s *server) token(name, kind string, scopes ...auth.Scope) string {
	s.t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(s.dir, store.FileName))
	if err != nil {
		s.t.Fatal(err)
	}
	defer st.Close()
	tokens := auth.NewTokens(st.DB(), clock.System{})
	if _, err := tokens.EnsurePrincipal(context.Background(), name, kind); err != nil {
		s.t.Fatal(err)
	}
	_, secret, err := tokens.CreateToken(context.Background(), name, "test", auth.NewScopeSet(scopes...))
	if err != nil {
		s.t.Fatal(err)
	}
	return secret
}

func (s *server) count(stream string) int {
	s.t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(s.dir, store.FileName))
	if err != nil {
		s.t.Fatal(err)
	}
	defer st.Close()
	var n int
	if err := st.DB().QueryRow(`SELECT count(*) FROM entries WHERE stream = ?`, stream).Scan(&n); err != nil {
		s.t.Fatal(err)
	}
	return n
}

func (s *server) post(token, path, ctype, body string) *http.Response {
	s.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, s.base+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", ctype)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { resp.Body.Close() })
	return resp
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

func (s *server) mcp(token string) *mcp.ClientSession {
	s.t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   s.base + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{token: token, next: http.DefaultTransport}},
	}
	cs, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, map[string]any) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	var structured map[string]any
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &structured)
	}
	if res.IsError {
		t.Fatalf("%s returned an error: %s", name, text.String())
	}
	return text.String(), structured
}

func TestSeq100000ThroughPushLands(t *testing.T) {
	s := startServer(t)
	var b strings.Builder
	for i := 1; i <= 100000; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	vars := map[string]string{"CAFARD_SERVER": s.base, "CAFARD_TOKEN": s.admin}
	var out, errOut strings.Builder
	err := run(context.Background(), []string{"push", "--stream", "seq"}, strings.NewReader(b.String()), &out, &errOut,
		func(k string) string { return vars[k] })
	if err != nil {
		t.Fatalf("push: %v\n%s", err, errOut.String())
	}
	if n := s.count("push:seq"); n != 100000 {
		t.Fatalf("%d lines landed, want 100000", n)
	}

	cs := s.mcp(s.admin)
	text, page := call(t, cs, "tail", map[string]any{"streams": []string{"push:seq"}, "lines": 3})
	if !strings.Contains(text, " 99999") || !strings.HasSuffix(strings.TrimSpace(lastLine(text)), "100000") {
		t.Errorf("tail = %q", text)
	}
	if page["next_seq"].(float64) != 100001 {
		t.Errorf("tail next_seq = %v", page["next_seq"])
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func TestWaitForIsReleasedByALaterLineAndByOneBeforeTheCall(t *testing.T) {
	s := startServer(t)
	cs := s.mcp(s.admin)
	ingest := func(text string) {
		resp := s.post(s.admin, "/api/log?stream=dut&severity=info", "text/plain", text+"\n")
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("ingest: %d %s", resp.StatusCode, body)
		}
	}
	ingest("booting")

	// A line pushed after the call releases it.
	type result struct {
		text string
		out  map[string]any
	}
	got := make(chan result, 1)
	go func() {
		text, out := call(t, cs, "wait_for", map[string]any{"pattern": "login:", "streams": []string{"push:*"}, "timeout_s": 20})
		got <- result{text, out}
	}()
	time.Sleep(300 * time.Millisecond)
	ingest("still booting")
	ingest("dut login: ")
	var r result
	select {
	case r = <-got:
	case <-time.After(15 * time.Second):
		t.Fatal("wait_for was not released by a later line")
	}
	if r.out["matched"] != true || !strings.Contains(r.text, "dut login:") || !strings.Contains(r.text, "still booting") {
		t.Errorf("wait_for = %q %v", r.text, r.out)
	}
	cursor := r.out["next_seq"].(float64)

	// A line pushed between since_seq and the call releases it at once.
	ingest("Kernel panic - not syncing")
	start := time.Now()
	text, out := call(t, cs, "wait_for", map[string]any{"pattern": "Kernel panic", "since_seq": cursor, "timeout_s": 20})
	if out["matched"] != true || !strings.Contains(text, "Kernel panic") {
		t.Errorf("wait_for since_seq = %q %v", text, out)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("wait_for took %s for a line already stored", time.Since(start))
	}

	// Timing out reports the lines seen and a cursor.
	text, out = call(t, cs, "wait_for", map[string]any{"pattern": "never", "timeout_s": 1})
	if out["timed_out"] != true || out["next_seq"] == nil {
		t.Errorf("timeout = %q %v", text, out)
	}
}

func TestWaitForIsCancelledWithTheCall(t *testing.T) {
	s := startServer(t)
	cs := s.mcp(s.admin)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "wait_for", Arguments: map[string]any{"pattern": "x", "timeout_s": 60}})
	if err == nil {
		t.Fatal("want an error from a cancelled call")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("cancel took %s", time.Since(start))
	}
	// The session still works afterwards.
	call(t, cs, "whoami", nil)
}

func TestLogToolsReadMarkAndContext(t *testing.T) {
	s := startServer(t)
	cs := s.mcp(s.admin)
	body := `{"text":"\u001b[31mred error\u001b[0m","severity":"err","stream":"serial:dut1","ts":"2026-11-16T10:00:00Z"}
{"text":"plain","stream":"serial:dut1"}
{"text":"other","stream":"syslog:box"}
`
	if resp := s.post(s.admin, "/api/log", "application/x-ndjson", body); resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("ingest: %d %s", resp.StatusCode, b)
	}

	text, out := call(t, cs, "whoami", nil)
	if out["name"] != "admin" || !strings.Contains(text, "admin") {
		t.Errorf("whoami = %q %v", text, out)
	}

	text, out = call(t, cs, "list_streams", nil)
	if !strings.Contains(text, "serial:dut1") || !strings.Contains(text, "syslog:box") {
		t.Errorf("list_streams = %q", text)
	}
	_ = out

	text, out = call(t, cs, "read", map[string]any{"streams": []string{"serial:*"}, "since_seq": 0})
	if strings.Contains(text, "\x1b") || !strings.Contains(text, "ERR red error") || strings.Contains(text, "other") {
		t.Errorf("read = %q", text)
	}
	if out["next_seq"].(float64) != 3 || out["has_more"] != false {
		t.Errorf("read page = %v", out)
	}

	sev := 3
	text, _ = call(t, cs, "read", map[string]any{"min_severity": sev, "since_seq": 0})
	if !strings.Contains(text, "red error") || strings.Contains(text, "plain") {
		t.Errorf("read min_severity = %q", text)
	}

	_, out = call(t, cs, "mark", map[string]any{"stream": "serial:dut1", "label": "test 42 starts"})
	markSeq := out["seq"].(float64)
	text, _ = call(t, cs, "context", map[string]any{"seq": markSeq, "before": 1, "after": 0})
	if !strings.Contains(text, "admin: test 42 starts") || !strings.Contains(text, "plain") {
		t.Errorf("context = %q", text)
	}

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "read", Arguments: map[string]any{"grep": "("}})
	if err != nil || !res.IsError {
		t.Fatalf("bad grep: %v %+v", err, res)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), `"code":"invalid_pattern"`) {
		t.Errorf("bad grep structured = %s", b)
	}
}

func TestToolListFollowsScopes(t *testing.T) {
	s := startServer(t)
	reader := s.token("reader", auth.KindAgent, auth.ScopeRead)
	res, err := s.mcp(reader).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	if slices.Contains(names, "mark") || !slices.Contains(names, "wait_for") {
		t.Errorf("read-only tools = %v", names)
	}
	res, _ = s.mcp(s.admin).ListTools(context.Background(), nil)
	names = nil
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Contains(names, "mark") {
		t.Errorf("admin tools = %v", names)
	}
}

func TestMCPRefusesBadCredentialsAndForeignOrigins(t *testing.T) {
	s := startServer(t)
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`
	do := func(token, origin string) int {
		req, _ := http.NewRequest(http.MethodPost, s.base+"/mcp", strings.NewReader(initBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := do("", ""); code != http.StatusUnauthorized {
		t.Errorf("no token: %d", code)
	}
	if code := do("cfd_nope", ""); code != http.StatusUnauthorized {
		t.Errorf("bad token: %d", code)
	}
	ingestOnly := s.token("dev1", auth.KindDevice, auth.ScopeIngest)
	if code := do(ingestOnly, ""); code != http.StatusUnauthorized {
		t.Errorf("token without read: %d", code)
	}
	if code := do(s.admin, "http://evil.example"); code != http.StatusForbidden {
		t.Errorf("foreign origin: %d", code)
	}
	if code := do(s.admin, s.base); code != http.StatusOK {
		t.Errorf("own origin: %d", code)
	}
}

func TestLogIngestNeedsTheIngestScope(t *testing.T) {
	s := startServer(t)
	reader := s.token("reader", auth.KindAgent, auth.ScopeRead)
	if resp := s.post(reader, "/api/log?stream=x", "text/plain", "hi\n"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("read token: %d", resp.StatusCode)
	}
	if resp := s.post("", "/api/log?stream=x", "text/plain", "hi\n"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %d", resp.StatusCode)
	}
	resp := s.post(s.admin, "/api/log?stream=x", "text/plain", "a\r\nb\rc")
	var acc struct {
		Accepted int   `json:"accepted"`
		LastSeq  int64 `json:"last_seq"`
	}
	if err := json.NewDecoder(bufio.NewReader(resp.Body)).Decode(&acc); err != nil || acc.Accepted != 3 || acc.LastSeq != 3 {
		t.Errorf("accepted = %+v, %v", acc, err)
	}
	if resp := s.post(s.admin, "/api/log?stream=chan:general", "text/plain", "hi"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("message stream: %d", resp.StatusCode)
	}
	if resp := s.post(s.admin, "/api/log", "application/x-ndjson", `{"stream":"a"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("ndjson without text: %d", resp.StatusCode)
	}
}

func TestPushReportsARefusal(t *testing.T) {
	s := startServer(t)
	reader := s.token("reader", auth.KindAgent, auth.ScopeRead)
	p, err := push.New(push.Options{Server: s.base, Token: reader, Stream: "x"})
	if err != nil {
		t.Fatal(err)
	}
	err = p.Reader(context.Background(), strings.NewReader("one\ntwo\n"))
	if err == nil || !strings.Contains(err.Error(), "ingest") {
		t.Fatalf("err = %v, want the server's sentence about the ingest scope", err)
	}
}

func TestRoutableBindNeedsAnAdminToken(t *testing.T) {
	cfg := config.Default()
	cfg.HTTPListen = "0.0.0.0:0"
	cfg.DataDir = t.TempDir()
	st, err := openStore(context.Background(), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	tokens := auth.NewTokens(st.DB(), clock.System{})
	if _, err := tokens.EnsurePrincipal(context.Background(), "someone", auth.KindAgent); err != nil {
		t.Fatal(err)
	}
	st.Close()
	err = serve(context.Background(), &cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "admin") {
		t.Fatalf("err = %v, want the exposure guard", err)
	}
}
