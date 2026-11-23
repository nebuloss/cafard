package admin_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"cafard/internal/httpapi/admin"
	"cafard/internal/httpapi/join"
	"cafard/internal/service/access"
	"cafard/internal/store"
	"cafard/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Offline(m) }

// fakeUI stands in for the built admin UI, which only `make web` produces:
// these tests are of how it is served, not of what it is.
var fakeUI = fstest.MapFS{
	"index.html":      {Data: []byte("<!doctype html><title>cafard admin</title><script type=module src=/assets/main.js></script>")},
	"assets/main.js":  {Data: []byte("console.log('admin')")},
	"assets/main.css": {Data: []byte("body{}")},
}

var ctx = context.Background()

// rig is the two listeners side by side, as cafard serves them: the admin
// surface on one server, the join pages on the other.
type rig struct {
	t         *testing.T
	st        *store.Store
	acc       *access.Service
	adminSrv  *httptest.Server
	publicSrv *httptest.Server
	client    *http.Client
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cafard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	acc := access.New(st)
	r := &rig{t: t, st: st, acc: acc}

	pub := http.NewServeMux()
	r.publicSrv = httptest.NewServer(pub)
	t.Cleanup(r.publicSrv.Close)
	join.New(nil, acc, r.publicSrv.URL).Register(pub)

	adm := http.NewServeMux()
	hash, err := access.HashAdminPassword(adminPassword)
	if err != nil {
		t.Fatal(err)
	}
	admin.New(nil, st, acc, admin.Config{PublicURL: r.publicSrv.URL, PasswordHash: hash}, fakeUI).Register(adm)
	r.adminSrv = httptest.NewServer(adm)
	t.Cleanup(r.adminSrv.Close)

	jar, _ := cookiejar.New(nil)
	r.client = &http.Client{Jar: jar}

	// A person to put in projects.
	if _, err := acc.CreateIdentity(ctx, access.Admin("test"), access.NewIdentity{Kind: store.Person, Name: "guillaume", DisplayName: "Guillaume"}); err != nil {
		t.Fatal(err)
	}
	return r
}

// call sends JSON to the admin API as a browser on the page would.
func (r *rig) call(method, path string, body any) (int, map[string]any) {
	r.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, r.adminSrv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := r.client.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// joinWith opens a person's link and chooses a password, as their phone would.
func (r *rig) joinWith(linkURL, password string) int {
	r.t.Helper()
	resp, err := http.PostForm(linkURL, url.Values{"password": {password}, "again": {password}})
	if err != nil {
		r.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

const adminPassword = "the admin's password"

func (r *rig) signIn() {
	r.t.Helper()
	if code, out := r.call("POST", "/admin/api/signin", map[string]string{"password": adminPassword}); code != 200 {
		r.t.Fatalf("sign in: %d %v", code, out)
	}
}

func TestSignInIsNeeded(t *testing.T) {
	r := newRig(t)
	if code, out := r.call("GET", "/admin/api/members", nil); code != 401 || out["error"] != "unauthorized" {
		t.Errorf("without signing in: %d %v", code, out)
	}
	if code, out := r.call("POST", "/admin/api/signin", map[string]string{"password": "not the password"}); code != 403 || out["error"] != "wrong" {
		t.Errorf("a wrong password: %d %v", code, out)
	}
	if code, _ := r.call("POST", "/admin/api/signin", map[string]string{"username": "guillaume", "password": adminPassword}); code != 400 {
		t.Errorf("a username is not taken: %d", code)
	}
	r.signIn()
	if code, out := r.call("GET", "/admin/api/me", nil); code != 200 || out["admin"] != true {
		t.Errorf("me: %d %v", code, out)
	}
	if code, _ := r.call("POST", "/admin/api/signout", nil); code != 204 {
		t.Errorf("sign out: %d", code)
	}
	if code, _ := r.call("GET", "/admin/api/me", nil); code != 401 {
		t.Errorf("after signing out: %d", code)
	}
}

func TestCrossSiteWritesAreRefused(t *testing.T) {
	r := newRig(t)
	r.signIn()
	req, _ := http.NewRequest("POST", r.adminSrv.URL+"/admin/api/projects", strings.NewReader(`{"name":"evil"}`))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, _ := r.client.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("a cross-site write: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest("POST", r.adminSrv.URL+"/admin/api/projects", strings.NewReader(`{"name":"evil"}`))
	req.Header.Set("Origin", "https://evil.example")
	resp, _ = r.client.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("a write with a foreign Origin: %d", resp.StatusCode)
	}
	if _, err := r.st.ProjectByName(ctx, "evil"); err == nil {
		t.Error("the cross-site project was created")
	}
}

// A person's password never opens the admin UI, even a person who knows
// their way in to the chat.
func TestOnlyTheAdminPasswordOpens(t *testing.T) {
	r := newRig(t)
	r.signIn()
	_, out := r.call("POST", "/admin/api/members", map[string]any{"kind": "person", "name": "camille"})
	link := out["link"].(map[string]any)
	if r.joinWith(link["url"].(string), "camille's password") != 200 {
		t.Fatal("camille could not join")
	}
	other := &rig{t: t, adminSrv: r.adminSrv, client: &http.Client{}}
	if code, _ := other.call("POST", "/admin/api/signin", map[string]string{"password": "camille's password"}); code != 403 {
		t.Errorf("a person's password into the admin UI: %d", code)
	}
	if code, _ := other.call("GET", "/admin/api/members", nil); code != 401 {
		t.Errorf("members without the admin password: %d", code)
	}
}

func TestCreatePeopleAgentsAndDevices(t *testing.T) {
	r := newRig(t)
	r.signIn()
	r.call("POST", "/admin/api/projects", map[string]any{"name": "be98", "members": []string{"guillaume"}})

	code, out := r.call("POST", "/admin/api/members", map[string]any{"kind": "person", "name": "camille", "display_name": "Camille", "projects": []string{"be98"}})
	if code != 201 {
		t.Fatalf("create person: %d %v", code, out)
	}
	link := out["link"].(map[string]any)
	if !strings.HasPrefix(link["url"].(string), r.publicSrv.URL+"/join/") || !strings.HasPrefix(link["qr"].(string), "data:image/png;base64,") || link["state"] != "waiting" {
		t.Errorf("person link: %v", link)
	}

	code, out = r.call("POST", "/admin/api/members", map[string]any{"kind": "agent", "name": "dev-code", "description": "source and git", "projects": []string{"be98"}})
	if code != 201 || out["token"] != nil {
		t.Fatalf("create agent: %d %v", code, out)
	}
	agentLink := out["link"].(map[string]any)
	if cmd := agentLink["command"].(string); cmd != "curl -fsSL "+agentLink["url"].(string)+" | sh" {
		t.Errorf("agent command: %q", cmd)
	}

	code, out = r.call("POST", "/admin/api/members", map[string]any{"kind": "device", "name": "dev-build", "projects": []string{"be98"}})
	if code != 201 || !strings.HasPrefix(out["token"].(string), "cfd_dv_") || out["link"] != nil {
		t.Fatalf("create device: %d %v", code, out)
	}

	// The install script, fetched the way curl does, then its token exchange.
	req, _ := http.NewRequest("GET", agentLink["url"].(string), nil)
	req.Header.Set("User-Agent", "curl/8.5.0")
	resp, _ := http.DefaultClient.Do(req)
	script, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(string(script), "#!/bin/sh") || !strings.Contains(string(script), "/token") || strings.Contains(string(script), "cfd_ag_") {
		t.Errorf("install script:\n%s", script)
	}
	resp, _ = http.Post(agentLink["url"].(string)+"/token", "", nil)
	tok, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(string(tok), "cfd_ag_") {
		t.Errorf("token exchange: %d %s", resp.StatusCode, tok)
	}

	_, out = r.call("GET", "/admin/api/members", nil)
	byName := map[string]map[string]any{}
	for _, m := range out["members"].([]any) {
		mm := m.(map[string]any)
		byName[mm["name"].(string)] = mm
	}
	if byName["dev-code"]["joined"] != true || byName["dev-code"]["link"].(map[string]any)["state"] != "used" {
		t.Errorf("dev-code after its link: %v", byName["dev-code"])
	}
	if byName["camille"]["joined"] != false || byName["camille"]["link"].(map[string]any)["state"] != "waiting" {
		t.Errorf("camille before joining: %v", byName["camille"])
	}
	if code, out := r.call("POST", "/admin/api/members/dev-code/token", nil); code != 400 {
		t.Errorf("showing an agent's token: %d %v", code, out)
	}
}

// The picker sends the full set; the server adds and removes through access.
func TestPickerSetsMembership(t *testing.T) {
	r := newRig(t)
	r.signIn()
	r.call("POST", "/admin/api/projects", map[string]any{"name": "be98"})
	r.call("POST", "/admin/api/projects", map[string]any{"name": "zeeos"})
	r.call("POST", "/admin/api/members", map[string]any{"kind": "agent", "name": "dev-code", "projects": []string{"be98"}})
	r.call("POST", "/admin/api/members", map[string]any{"kind": "agent", "name": "lab-bench"})

	if code, out := r.call("PATCH", "/admin/api/projects/be98", map[string]any{"members": []string{"guillaume", "lab-bench"}}); code != 200 {
		t.Fatalf("set be98's members: %d %v", code, out)
	}
	if ok, _ := r.isMember("be98", "dev-code"); ok {
		t.Error("dev-code is still in be98")
	}
	if ok, _ := r.isMember("be98", "lab-bench"); !ok {
		t.Error("lab-bench is not in be98")
	}
	if code, _ := r.call("PATCH", "/admin/api/members/lab-bench", map[string]any{"projects": []string{"zeeos"}}); code != 200 {
		t.Fatal("set lab-bench's projects")
	}
	if ok, _ := r.isMember("be98", "lab-bench"); ok {
		t.Error("lab-bench is still in be98")
	}
	if ok, _ := r.isMember("zeeos", "lab-bench"); !ok {
		t.Error("lab-bench is not in zeeos")
	}

	log, _ := r.st.Audit(ctx, 50)
	found := false
	for _, e := range log {
		if e.Actor == "admin" && e.Action == "removed dev-code from" && e.Target == "be98" && strings.HasPrefix(e.Origin, "admin · ") {
			found = true
		}
	}
	if !found {
		t.Error("the picker's change is not in the audit log under admin, from the admin UI")
	}
}

func (r *rig) isMember(project, name string) (bool, error) {
	p, err := r.st.ProjectByName(ctx, project)
	if err != nil {
		return false, err
	}
	ms, err := r.st.Members(ctx, p.ID)
	for _, m := range ms {
		if m.Name == name {
			return true, err
		}
	}
	return false, err
}

func TestDeletingAProjectNeedsItsName(t *testing.T) {
	r := newRig(t)
	r.signIn()
	r.call("POST", "/admin/api/projects", map[string]any{"name": "zeeos-qa"})
	if code, _ := r.call("DELETE", "/admin/api/projects/zeeos-qa", map[string]any{"confirm": "zeeos"}); code != 400 {
		t.Errorf("the wrong name: %d", code)
	}
	if code, _ := r.call("DELETE", "/admin/api/projects/zeeos-qa", map[string]any{"confirm": "zeeos-qa"}); code != 204 {
		t.Errorf("the right name: %d", code)
	}
	if _, err := r.st.ProjectByName(ctx, "zeeos-qa"); err == nil {
		t.Error("the project is still there")
	}
}

func TestArchiveAndLimits(t *testing.T) {
	r := newRig(t)
	r.signIn()
	r.call("POST", "/admin/api/projects", map[string]any{"name": "be98"})
	r.call("PATCH", "/admin/api/projects/be98", map[string]any{"retention_days": 7, "quota_bytes": 2 << 30})
	r.call("PATCH", "/admin/api/projects/be98", map[string]any{"archived": true})
	p, _ := r.st.ProjectByName(ctx, "be98")
	if p.RetentionDays != 7 || p.QuotaBytes != 2<<30 || !p.Archived() {
		t.Errorf("after the changes: %+v", p)
	}
	if code, out := r.call("PATCH", "/admin/api/projects/be98", map[string]any{"members": []string{"guillaume"}}); code != 409 || out["error"] != "archived" {
		t.Errorf("changing an archived project's members: %d %v", code, out)
	}
}

func TestUIIsServedWithItsPolicy(t *testing.T) {
	r := newRig(t)
	for _, p := range []string{"/", "/assets/main.js", "/assets/main.css", "/some/deep/link"} {
		resp, err := http.Get(r.adminSrv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") {
			t.Errorf("%s: %d, CSP %q", p, resp.StatusCode, resp.Header.Get("Content-Security-Policy"))
		}
		if p == "/some/deep/link" && !strings.Contains(string(body), "<title>cafard admin</title>") {
			t.Errorf("a deep link does not get the page")
		}
	}
}

func TestAMissingUISaysHowToBuildIt(t *testing.T) {
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cafard.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mux := http.NewServeMux()
	admin.New(nil, st, access.New(st), admin.Config{}, nil).Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 404 || !strings.Contains(string(body), "make build") {
		t.Errorf("no UI: %d %s", resp.StatusCode, body)
	}
}
