package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/projects"
	"github.com/nebuloss/cafard/internal/web/webtest"
)

// listLen is the length of the list under key in a JSON object.
func listLen(t *testing.T, body []byte, key string) int {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	var list []json.RawMessage
	if err := json.Unmarshal(m[key], &list); err != nil {
		t.Fatalf("%s: %v: %s", key, err, body)
	}
	return len(list)
}

// The pages, the JSON API and the blobs show a person only what their
// grants cover: nothing without one, one channel and its bound streams
// with a channel grant.
func TestPagesAndAPIFollowGrants(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	ctx := context.Background()
	if _, err := e.svc.CreateChannel(ctx, adminP, dto.ChannelCreate{Name: "bench", Patterns: []string{"syslog:lab/bench-pi"}}); err != nil {
		t.Fatal(err)
	}
	scopes := []auth.Scope{auth.ScopeRead, auth.ScopeMessage}
	for _, name := range []string{"nobody", "narrow"} {
		if _, err := e.Tokens.EnsurePrincipal(ctx, name, auth.KindHuman); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Deps.Projects.SetGrant(ctx, projects.Grant{Principal: "narrow", Project: "lab", Channel: "dut1", Scopes: auth.NewScopeSet(auth.ScopeRead)}); err != nil {
		t.Fatal(err)
	}
	nobody, narrow := e.SessionOnly("nobody", scopes...), e.SessionOnly("narrow", scopes...)
	get := func(c *http.Cookie, path string) (int, []byte) {
		rec := e.Do("GET", path, c, nil)
		return rec.Code, rec.Body.Bytes()
	}

	for path, key := range map[string]string{"/api/streams": "streams", "/api/chats?all=1": "chats", "/api/projects": "projects",
		"/api/entries?streams=serial:lab/dut1": "items", "/api/streams/match?pattern=serial:lab/*": "streams"} {
		if code, b := get(nobody, path); code != http.StatusOK || listLen(t, b, key) != 0 {
			t.Errorf("%s without a grant = %d %s", path, code, b)
		}
	}
	for _, path := range []string{"/c/lab/dut1", "/c/lab/general", "/blob/" + seeded.sha,
		fmt.Sprintf("/api/messages/%d", seeded.message), fmt.Sprintf("/api/messages/%d/attachments/1", seeded.message)} {
		if code, _ := get(nobody, path); code != http.StatusNotFound {
			t.Errorf("%s without a grant = %d", path, code)
		}
	}
	for _, path := range []string{"/streams", "/live?streams=serial:lab/dut1", "/"} {
		if _, b := get(nobody, path); strings.Contains(string(b), "U-Boot") || strings.Contains(string(b), "bench-pi") || strings.Contains(string(b), "#dut1") {
			t.Errorf("%s shows what no grant covers", path)
		}
	}

	if code, b := get(narrow, "/api/streams"); code != http.StatusOK || listLen(t, b, "streams") != 1 || !strings.Contains(string(b), `"serial:lab/dut1"`) {
		t.Errorf("streams with a channel grant = %d %s", code, b)
	}
	if code, b := get(narrow, "/api/chats?all=1"); code != http.StatusOK || listLen(t, b, "chats") != 1 {
		t.Errorf("chats with a channel grant = %d %s", code, b)
	}
	for path, want := range map[string]int{"/c/lab/dut1": 200, "/c/lab/general": 404, "/c/lab/bench": 404, "/blob/" + seeded.sha: 200,
		fmt.Sprintf("/api/messages/%d/attachments/1", seeded.message): 200} {
		if code, _ := get(narrow, path); code != want {
			t.Errorf("%s with a channel grant = %d, want %d", path, code, want)
		}
	}
	if _, b := get(narrow, "/streams"); !strings.Contains(string(b), "serial:lab/dut1") || strings.Contains(string(b), "bench-pi") {
		t.Error("Sources shows other streams than the bound one")
	}
	if _, b := get(narrow, "/api/entries?streams=syslog:lab/bench-pi"); listLen(t, b, "items") != 0 {
		t.Errorf("entries of an unbound stream: %s", b)
	}
}
