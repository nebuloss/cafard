package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"cafard/internal/blobs"
	"cafard/internal/httpapi/join"
	"cafard/internal/live"
	"cafard/internal/service/access"
	"cafard/internal/service/chat"
	"cafard/internal/store"
	"cafard/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Offline(m) }

var ctx = context.Background()

type rig struct {
	srv                     *httptest.Server
	agent, device, outsider string
	st                      *store.Store
	acc                     *access.Service
	admin                   access.Who
	hub                     *live.Hub
}

// newRig is a server with dev-code (agent) and dev-build (device) in be98,
// and lab-bench (agent) in no project, each with a token.
func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "cafard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bl, _ := blobs.Open(filepath.Join(dir, "files"))
	acc := access.New(st)
	g, _ := acc.CreateFirstAdmin(ctx, "guillaume", "", "test")
	admin := access.Who{Identity: g, Origin: "test"}
	p, _ := acc.CreateProject(ctx, admin, "be98", 30, 5e9)
	token := func(name string, kind store.Kind, inProject bool) string {
		i, err := acc.CreateIdentity(ctx, admin, access.NewIdentity{Kind: kind, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		if inProject {
			acc.AddMember(ctx, admin, p, i)
		}
		tok, err := acc.IssueToken(ctx, admin, i)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	r := &rig{
		agent:    token("dev-code", store.Agent, true),
		device:   token("dev-build", store.Device, true),
		outsider: token("lab-bench", store.Agent, false),
	}
	mux := http.NewServeMux()
	r.st, r.acc, r.admin, r.hub = st, acc, admin, live.New()
	st.Announce(r.hub)
	New(nil, acc, chat.New(st, bl, acc), r.hub, "http://cafard.example").Register(mux)
	join.New(nil, acc, "http://cafard.example").Register(mux)
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *rig) do(t *testing.T, method, path, token string, body io.Reader) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, r.srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := r.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func decode(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
}

// What `curl -T boot.log -H "Authorization: Bearer …" …/send/be98/wifi-drops/`
// does, then an agent reading it back by page.
func TestCurlSendsAndAnAgentReads(t *testing.T) {
	r := newRig(t)
	log := "[ 0.1] boot\n[ 212.4] wl0: dhd_bus_rxctl: resumed on timeout\n[ 212.5] HANG\n"
	resp, body := r.do(t, "PUT", "/api/v1/send/be98/wifi-drops/boot.log", r.device, strings.NewReader(log))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("send: %d %s", resp.StatusCode, body)
	}
	var sent struct {
		Address string
		Created bool
		Message messageJSON
	}
	decode(t, body, &sent)
	if sent.Address != "be98/wifi-drops" || !sent.Created || len(sent.Message.Files) != 1 {
		t.Fatalf("sent: %+v", sent)
	}
	f := sent.Message.Files[0]
	if f.State != "stored" || f.LinesURL == "" || f.Size != int64(len(log)) {
		t.Errorf("file: %+v", f)
	}

	resp, body = r.do(t, "POST", "/api/v1/send/be98/wifi-drops", r.agent, strings.NewReader("Found it: line 2."))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("send text: %d %s", resp.StatusCode, body)
	}

	resp, body = r.do(t, "GET", "/api/v1/projects/be98/conversations/wifi-drops/messages", r.agent, nil)
	var ms struct {
		Messages  []messageJSON
		NextAfter int64 `json:"next_after"`
	}
	decode(t, body, &ms)
	if resp.StatusCode != 200 || len(ms.Messages) != 2 || ms.Messages[0].Author.Name != "dev-build" || ms.Messages[1].Text != "Found it: line 2." {
		t.Fatalf("messages: %d %s", resp.StatusCode, body)
	}

	resp, body = r.do(t, "GET", f.LinesURL+"?grep=timeout", r.agent, nil)
	var page chat.LinesPage
	decode(t, body, &page)
	if resp.StatusCode != 200 || len(page.Lines) != 1 || page.Lines[0].N != 2 || page.Total != 3 {
		t.Errorf("grep: %d %s", resp.StatusCode, body)
	}

	resp, body = r.do(t, "GET", f.URL, r.agent, nil)
	if resp.StatusCode != 200 || string(body) != log {
		t.Errorf("download: %d %q", resp.StatusCode, body)
	}
}

// Files are never served as something that could run on cafard's origin.
func TestFilesAreNeverActiveContent(t *testing.T) {
	r := newRig(t)
	for _, tc := range []struct {
		name, body, wantType, wantDisposition string
	}{
		{"page.html", "<script>alert(1)</script>", "application/octet-stream", "attachment"},
		{"logo.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`, "application/octet-stream", "attachment"},
		{"boot.log", "hello\n", "text/plain; charset=utf-8", "attachment"},
		{"led.png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR", "image/png", "inline"},
	} {
		_, body := r.do(t, "PUT", "/api/v1/send/be98/files/"+tc.name, r.device, strings.NewReader(tc.body))
		var sent struct{ Message messageJSON }
		decode(t, body, &sent)
		resp, _ := r.do(t, "GET", sent.Message.Files[0].URL, r.agent, nil)
		h := resp.Header
		if h.Get("Content-Type") != tc.wantType || !strings.HasPrefix(h.Get("Content-Disposition"), tc.wantDisposition) {
			t.Errorf("%s: served as %q, %q", tc.name, h.Get("Content-Type"), h.Get("Content-Disposition"))
		}
		if !strings.HasPrefix(h.Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("%s: CSP %q", tc.name, h.Get("Content-Security-Policy"))
		}
	}
}

func TestEveryRouteNeedsAToken(t *testing.T) {
	r := newRig(t)
	for _, p := range []string{"/api/v1/me", "/api/v1/projects", "/api/v1/projects/be98/conversations", "/api/v1/files/1"} {
		resp, body := r.do(t, "GET", p, "", nil)
		if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") == "" || !strings.Contains(string(body), "Authorization: Bearer") {
			t.Errorf("%s without a token: %d %s", p, resp.StatusCode, body)
		}
		resp, _ = r.do(t, "GET", p, "cfd_ag_notatoken", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s with a bad token: %d", p, resp.StatusCode)
		}
	}
	resp, _ := r.do(t, "PUT", "/api/v1/send/be98/x/a.log", "", strings.NewReader("x"))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("send without a token: %d", resp.StatusCode)
	}
}

func TestRefusalsHaveTheirStatus(t *testing.T) {
	r := newRig(t)
	r.do(t, "POST", "/api/v1/send/be98/x", r.agent, strings.NewReader("hello"))
	for _, tc := range []struct {
		what, method, path, token string
		status                    int
		code                      string
	}{
		{"device reads", "GET", "/api/v1/projects/be98/conversations", r.device, 403, "send_only"},
		{"outsider reads", "GET", "/api/v1/projects/be98/conversations", r.outsider, 403, "not_member"},
		{"outsider sends", "POST", "/api/v1/send/be98/x", r.outsider, 403, "not_member"},
		{"unknown project", "GET", "/api/v1/projects/nope/conversations", r.agent, 404, "not_found"},
		{"unknown conversation", "GET", "/api/v1/projects/be98/conversations/nope/messages", r.agent, 404, "not_found"},
		{"bad conversation name", "POST", "/api/v1/send/be98/Not_OK", r.agent, 400, "bad_name"},
		{"no file name", "PUT", "/api/v1/send/be98/x/", r.device, 400, "bad_name"},
		{"unknown route", "GET", "/api/v1/nothing", r.agent, 404, "not_found"},
	} {
		var body io.Reader
		if tc.method != "GET" {
			body = strings.NewReader("text")
		}
		resp, b := r.do(t, tc.method, tc.path, tc.token, body)
		var e struct{ Error, Message string }
		json.Unmarshal(b, &e)
		if resp.StatusCode != tc.status || e.Error != tc.code || e.Message == "" {
			t.Errorf("%s: %d %s, want %d %s", tc.what, resp.StatusCode, b, tc.status, tc.code)
		}
	}
}

func TestMe(t *testing.T) {
	r := newRig(t)
	_, body := r.do(t, "GET", "/api/v1/me", r.agent, nil)
	var me struct {
		Member   memberJSON
		Projects []string
	}
	decode(t, body, &me)
	if me.Member.Name != "dev-code" || me.Member.Kind != "agent" || len(me.Projects) != 1 || me.Projects[0] != "be98" {
		t.Errorf("me: %s", body)
	}
}
