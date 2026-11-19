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
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/projects"
	"github.com/nebuloss/cafard/internal/push"
	"github.com/nebuloss/cafard/internal/store"
)

// server is a whole cafard started on a loopback port for one test.
type server struct {
	t    *testing.T
	base string
	// adminBase is the admin interface: base itself unless the test set
	// admin_listen.
	adminBase string
	// password is the admin password the first start printed; admin is a
	// token of the admin principal, without the admin scope no token has.
	password string
	admin    string
	dir      string
	cancel   context.CancelFunc
	logs     *syncBuffer

	dbOnce sync.Once
	st     *store.Store
}

var passwordLine = regexp.MustCompile(`(?m)^\s+([0-9a-f]{36})$`)

// testProject is the project every test server has, with testSinks,
// where token grants its principals.
const testProject = "lab"

// testSinks are the sinks of testProject the tests write logs into.
var testSinks = []string{"bench", "box", "checkout", "dut", "dut1", "dut1-bmc", "dut2", "proxied", "seq", "x"}

func startServer(t *testing.T, opts ...func(*config.Config)) *server {
	t.Helper()
	return startServerWith(t, testSinks, opts...)
}

// startServerWith starts a server whose testProject has sinks, given as
// "name" or "name=sources" (IPs and CIDRs).
func startServerWith(t *testing.T, sinks []string, opts ...func(*config.Config)) *server {
	t.Helper()
	cfg := testConfig()
	cfg.HTTPListen = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	seedProject(t, cfg.DataDir, sinks)
	for _, o := range opts {
		o(&cfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	addrs := make(chan [2]net.Addr, 1)
	done := make(chan error, 1)
	var out syncBuffer
	logs := new(syncBuffer)
	go func() {
		done <- serve(ctx, &cfg, slog.New(slog.NewTextHandler(logs, nil)), &out, func(public, admin net.Addr) { addrs <- [2]net.Addr{public, admin} })
	}()
	var bound [2]net.Addr
	select {
	case bound = <-addrs:
	case err := <-done:
		t.Fatalf("serve: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not start")
	}
	m := passwordLine.FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no admin password printed on first start:\n%s", out.String())
	}
	s := &server{t: t, base: "http://" + bound[0].String(), password: m[1], dir: cfg.DataDir, cancel: cancel, logs: logs}
	s.adminBase = s.base
	if bound[1] != nil {
		s.adminBase = "http://" + bound[1].String()
	}
	s.admin = s.token(auth.AdminName, auth.KindHuman, auth.ScopeIngest, auth.ScopeRead, auth.ScopeMessage)
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

// seedProject makes testProject and its sinks before the server starts,
// as the admin would on the project page.
func seedProject(t *testing.T, dir string, sinks []string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	projs, err := projects.New(ctx, st.DB(), clock.System{}, store.Limits{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projs.Create(ctx, testProject, ""); err != nil {
		t.Fatal(err)
	}
	for _, spec := range sinks {
		name, sources, _ := strings.Cut(spec, "=")
		k := projects.Sink{Project: testProject, Name: name}
		if k.Sources, err = projects.ParseSources(sources); err != nil {
			t.Fatal(err)
		}
		if _, err := projs.SaveSink(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
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

// db is the running server's database, opened beside it once and closed
// at the end of the test.
func (s *server) db() *store.Store {
	s.t.Helper()
	s.dbOnce.Do(func() {
		st, err := store.Open(context.Background(), filepath.Join(s.dir, store.FileName))
		if err != nil {
			s.t.Fatal(err)
		}
		s.st = st
		s.t.Cleanup(func() { st.Close() })
	})
	return s.st
}

// token creates a principal and a token in the running server's
// database, and grants the principal the token's scopes on the whole of
// testProject.
func (s *server) token(name, kind string, scopes ...auth.Scope) string {
	s.t.Helper()
	secret := s.tokenOnly(name, kind, scopes...)
	s.grant(name, testProject, scopes...)
	return secret
}

// tokenOnly is token without the grant.
func (s *server) tokenOnly(name, kind string, scopes ...auth.Scope) string {
	s.t.Helper()
	tokens := auth.NewTokens(s.db().DB(), clock.System{})
	if _, err := tokens.EnsurePrincipal(context.Background(), name, kind); err != nil {
		s.t.Fatal(err)
	}
	_, secret, err := tokens.CreateToken(context.Background(), name, "test", auth.NewScopeSet(scopes...))
	if err != nil {
		s.t.Fatal(err)
	}
	return secret
}

// grant adds scopes (those a grant may hold) on the whole of project, made
// when missing, as cafard token create --grant does. The server sees it
// at once for a principal it has not served yet, and within 5 s else.
func (s *server) grant(name, project string, scopes ...auth.Scope) {
	s.t.Helper()
	ctx := context.Background()
	projs, err := projects.New(ctx, s.db().DB(), clock.System{}, store.Limits{}, slog.New(slog.DiscardHandler))
	if err != nil {
		s.t.Fatal(err)
	}
	var granted []auth.Scope
	for _, sc := range scopes {
		if projects.GrantScopes.Has(sc) {
			granted = append(granted, sc)
		}
	}
	if len(granted) == 0 {
		return
	}
	if err := projs.AddGrants(ctx, projects.Grant{Principal: name, Project: project, Scopes: auth.NewScopeSet(granted...)}); err != nil {
		s.t.Fatal(err)
	}
}

func (s *server) count(stream string) int {
	s.t.Helper()
	var n int
	if err := s.db().DB().QueryRow(`SELECT count(*) FROM entries WHERE stream = ?`, stream).Scan(&n); err != nil {
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
	res, text, structured := callResult(t, cs, name, args)
	if res.IsError {
		t.Fatalf("%s returned an error: %s", name, text)
	}
	return text, structured
}

func TestSeq100000ThroughPushLands(t *testing.T) {
	s := startServer(t)
	var b strings.Builder
	for i := 1; i <= 100000; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	vars := map[string]string{"CAFARD_SERVER": s.base, "CAFARD_TOKEN": s.admin}
	var out, errOut strings.Builder
	err := run(context.Background(), []string{"push", "--stream", testProject + "/seq"}, strings.NewReader(b.String()), &out, &errOut,
		func(k string) string { return vars[k] })
	if err != nil {
		t.Fatalf("push: %v\n%s", err, errOut.String())
	}
	if n := s.count("push:lab/seq"); n != 100000 {
		t.Fatalf("%d lines landed, want 100000", n)
	}

	cs := s.mcp(s.admin)
	text, page := call(t, cs, "tail", map[string]any{"streams": []string{"push:lab/seq"}, "lines": 3})
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
		resp := s.post(s.admin, "/api/log?stream=lab/dut&severity=info", "text/plain", text+"\n")
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
		text, out := call(t, cs, "wait_for", map[string]any{"pattern": "login:", "streams": []string{"push:lab/*"}, "timeout_s": 20})
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
	body := `{"text":"\u001b[31mred error\u001b[0m","severity":"err","stream":"serial:lab/dut1","ts":"2026-11-16T10:00:00Z"}
{"text":"plain","stream":"lab/dut1"}
{"text":"other","stream":"box"}
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
	if !strings.Contains(text, "push:lab/dut1") || !strings.Contains(text, "push:lab/box") {
		t.Errorf("list_streams = %q", text)
	}
	_ = out

	text, out = call(t, cs, "read", map[string]any{"streams": []string{"push:lab/dut1"}, "since_seq": 0})
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

	_, out = call(t, cs, "mark", map[string]any{"stream": "push:lab/dut1", "label": "test 42 starts"})
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
	if n := s.count("push:lab/x"); n != 3 {
		t.Errorf("%d lines in push:lab/x, want 3", n)
	}
}

func TestLogsLandInTheirSinkOrAreDropped(t *testing.T) {
	s := startServer(t)
	accepted := func(token, path, ctype, body string) (acc dto.LogAccepted) {
		t.Helper()
		resp := s.post(token, path, ctype, body)
		if err := json.NewDecoder(resp.Body).Decode(&acc); err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %v", path, resp.StatusCode, err)
		}
		return acc
	}
	// A grant holder names the sink; a name no sink has is dropped, not an error.
	if acc := accepted(s.admin, "/api/log", "application/x-ndjson", `{"text":"in","stream":"dut2"}`+"\n"+`{"text":"out","stream":"nowhere"}`); acc.Accepted != 1 || acc.Dropped != 1 {
		t.Errorf("mixed = %+v", acc)
	}
	// A token without a grant reaches no sink.
	loner := s.tokenOnly("loner", auth.KindDevice, auth.ScopeIngest)
	if acc := accepted(loner, "/api/log?stream=lab/dut2", "text/plain", "x\n"); acc.Accepted != 0 || acc.Dropped != 1 {
		t.Errorf("no grant = %+v", acc)
	}
	// A sink token writes into its sink whatever it names, and needs no stream.
	k, err := projects.New(context.Background(), s.db().DB(), clock.System{}, store.Limits{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	sink, err := k.Sink(context.Background(), testProject, "bench")
	if err != nil {
		t.Fatal(err)
	}
	tokens := auth.NewTokens(s.db().DB(), clock.System{})
	if _, err := tokens.EnsurePrincipal(context.Background(), "bench-pi", auth.KindDevice); err != nil {
		t.Fatal(err)
	}
	_, secret, err := tokens.CreateSinkToken(context.Background(), "bench-pi", "test", sink.ID, sink.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if acc := accepted(secret, "/api/log?stream=lab/dut2", "text/plain", "one\n"); acc.Accepted != 1 {
		t.Errorf("sink token naming another sink = %+v", acc)
	}
	if acc := accepted(secret, "/api/log", "text/plain", "two\n"); acc.Accepted != 1 {
		t.Errorf("sink token without a stream = %+v", acc)
	}
	if n, m := s.count("push:lab/bench"), s.count("push:lab/dut2"); n != 2 || m != 1 {
		t.Errorf("bench has %d lines, dut2 %d; want 2 and 1", n, m)
	}
	// A sink token reads nothing.
	resp := s.post(secret, "/mcp", "application/json", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Errorf("a sink token opened MCP: %d", resp.StatusCode)
	}
	var list dto.StreamList
	_, b := s.browser(s.session(), "GET", "/api/streams", "", nil, true)
	if err := json.Unmarshal(b, &list); err != nil || list.Unrouted["http"] != 2 {
		t.Errorf("unrouted for the admin = %v %v", list.Unrouted, err)
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
