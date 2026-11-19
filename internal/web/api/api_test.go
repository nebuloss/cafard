package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/web/api"
	"github.com/nebuloss/cafard/internal/web/view"
	"github.com/nebuloss/cafard/internal/web/webtest"
)

func newEnv(t *testing.T) *webtest.Env {
	t.Helper()
	e := webtest.New(t, webtest.Options{})
	pairings := auth.NewPairings(e.Store.DB(), e.Clock, e.Sessions, e.Deps.Projects)
	api.New(e.Deps, e.Sessions, pairings, view.NewKinds(e.Tokens)).Mount(e.Mux, e.AdminMux)
	return e
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode: %v\n%s", err, b)
	}
	return v
}

func get[T any](t *testing.T, e *webtest.Env, path, token string) (int, T) {
	t.Helper()
	rec := e.Do("GET", path, nil, nil, "Authorization", "Bearer "+token)
	return rec.Code, decode[T](t, rec.Body.Bytes())
}

func TestEntriesNeverReturnMessages(t *testing.T) {
	e := newEnv(t)
	tok := e.Principal("codex", auth.KindAgent, auth.ScopeRead)
	e.Commit(
		webtest.Line("serial:lab/dut1", "boot"),
		store.Entry{Stream: "chan:lab/general", Kind: store.KindMessage, Severity: 6, Text: "secret plans", Sender: "alice"},
		store.Entry{Stream: "dm:alice~bob", Kind: store.KindMessage, Severity: 6, Text: "private", Sender: "alice", Recipient: "bob"},
		webtest.Line("serial:lab/dut1", "login:"),
	)
	for _, q := range []string{"", "?streams=*", "?streams=chan:*", "?streams=dm:*,chan:general", "?streams=chan:general,serial:dut1", "?since_seq=1"} {
		code, page := get[dto.Page[dto.Entry]](t, e, "/api/entries"+q, tok)
		if code != 200 {
			t.Fatalf("%s: %d", q, code)
		}
		for _, it := range page.Items {
			if store.IsMessageStream(it.Stream) || it.Kind == dto.EntryMessage {
				t.Errorf("%s returned %+v", q, it)
			}
		}
	}
	code, page := get[dto.Page[dto.Entry]](t, e, "/api/entries?streams=serial:lab/dut1", tok)
	if code != 200 || len(page.Items) != 2 || page.Items[1].Text != "login:" || page.NextSeq != 5 {
		t.Errorf("serial page: %d %+v", code, page)
	}
	if code, _ := get[dto.Error](t, e, "/api/entries?kinds=message", tok); code != 400 {
		t.Errorf("kinds=message: %d", code)
	}
	if code, errBody := get[dto.Error](t, e, "/api/entries?streams=serial:[", tok); code != 400 || errBody.Error.Code != dto.CodeInvalidPattern {
		t.Errorf("bad glob: %d %+v", code, errBody)
	}
	if code, errBody := get[dto.Error](t, e, "/api/entries?channel=dut1", tok); code != 400 || errBody.Error.Code != dto.CodeUnknownChannel {
		t.Errorf("channel without messaging: %d %+v", code, errBody)
	}
	// The context of a message is not a log context.
	if code, _ := get[dto.Error](t, e, "/api/entries/2/context", tok); code != 404 {
		t.Errorf("context of a message: %d", code)
	}
	code, ctxPage := get[dto.Page[dto.Entry]](t, e, "/api/entries/4/context?before=5&after=5", tok)
	if code != 200 || ctxPage.FocusSeq != 4 || len(ctxPage.Items) != 2 {
		t.Errorf("context: %d %+v", code, ctxPage)
	}
}

func TestRangesAndPaging(t *testing.T) {
	e := newEnv(t)
	tok := e.Principal("codex", auth.KindAgent, auth.ScopeRead)
	for i := range 30 {
		sev := store.DefaultSeverity
		if i%10 == 0 {
			sev = 3
		}
		e.Commit(store.Entry{Stream: "push:lab/a", Kind: store.KindLine, Severity: sev, Text: "line " + strconv.Itoa(i+1)})
	}
	_, page := get[dto.Page[dto.Entry]](t, e, "/api/entries?limit=10", tok)
	if len(page.Items) != 10 || page.Items[0].Seq != 21 || page.NextSeq != 31 || page.NextBeforeSeq != 21 {
		t.Errorf("tail: first=%d next=%d before=%d", page.Items[0].Seq, page.NextSeq, page.NextBeforeSeq)
	}
	_, page = get[dto.Page[dto.Entry]](t, e, "/api/entries?limit=10&before_seq=21", tok)
	if len(page.Items) != 10 || page.Items[9].Seq != 20 {
		t.Errorf("backwards: %+v", page.Items)
	}
	_, page = get[dto.Page[dto.Entry]](t, e, "/api/entries?from_seq=5&to_seq=8", tok)
	if len(page.Items) != 4 || page.Items[0].Seq != 5 || page.HasMore {
		t.Errorf("range: %d items, has_more %v", len(page.Items), page.HasMore)
	}
	_, page = get[dto.Page[dto.Entry]](t, e, "/api/entries?sev=err&re=line%201%24", tok)
	if len(page.Items) != 1 || page.Items[0].Text != "line 1" || page.Items[0].Severity != 3 {
		t.Errorf("sev and re: %+v", page.Items)
	}
}

func TestMeMarksAndPrincipals(t *testing.T) {
	e := newEnv(t)
	tok := e.Principal("codex", auth.KindAgent, auth.ScopeRead, auth.ScopeMessage)
	e.Commit(webtest.Line("serial:lab/dut1", "x"))
	_, me := get[dto.Me](t, e, "/api/me", tok)
	if me.Name != "codex" || me.SessionID != 0 || me.Theme != dto.ThemeSystem || len(me.Scopes) != 2 {
		t.Errorf("me = %+v", me)
	}
	rec := e.Do("POST", "/api/marks", nil, `{"stream":"serial:lab/dut1","label":"test 42 starts"}`, "Authorization", "Bearer "+tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("mark: %d %s", rec.Code, rec.Body)
	}
	created := decode[dto.MarkCreated](t, rec.Body.Bytes())
	m, err := e.Store.Get(t.Context(), created.Seq)
	if err != nil || m.Kind != store.KindMarker || m.Sender != "codex" || m.Text != "codex: test 42 starts" {
		t.Errorf("marker %+v %v", m, err)
	}
	if rec := e.Do("POST", "/api/marks", nil, `{"stream":"chan:lab/general","label":"x"}`, "Authorization", "Bearer "+tok); rec.Code != 400 {
		t.Errorf("mark in a chat: %d", rec.Code)
	}
	if rec := e.Do("POST", "/api/marks", nil, `{"stream":"serial:lab/nope","label":"x"}`, "Authorization", "Bearer "+tok); rec.Code != 404 {
		t.Errorf("mark in an unknown stream: %d", rec.Code)
	}
	e.Principal("alice", auth.KindHuman, auth.ScopeRead)
	_, ps := get[map[string][]dto.Principal](t, e, "/api/principals?kind=human", tok)
	if len(ps["principals"]) != 1 || ps["principals"][0].Name != "alice" {
		t.Errorf("principals = %+v", ps)
	}
	_, match := get[map[string][]dto.Stream](t, e, "/api/streams/match?pattern=serial:lab/*", tok)
	if len(match["streams"]) != 1 || match["streams"][0].ID != "serial:lab/dut1" {
		t.Errorf("match = %+v", match)
	}
	if rec := e.Do("GET", "/api/me", nil, nil); rec.Code != 401 {
		t.Errorf("anonymous: %d", rec.Code)
	}
	if code, _ := get[dto.Error](t, e, "/api/tokens", tok); code != 403 {
		t.Errorf("tokens without admin: %d", code)
	}
}

func TestPairingsCannotGrantMoreThanTheAdminHolds(t *testing.T) {
	e := newEnv(t)
	admin := e.Admin()
	rec := e.Do("POST", "/api/pairings", admin, `{"new_name":"phone"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("pairing: %d %s", rec.Code, rec.Body)
	}
	p := decode[dto.Pairing](t, rec.Body.Bytes())
	if !strings.HasPrefix(p.URL, "https://cafard.lan/p/") || p.QRURL != "/pair/"+webtest.Itoa(p.ID)+"/qr.svg" || p.NewName != "phone" {
		t.Errorf("pairing = %+v", p)
	}
	rec = e.Do("POST", "/api/pairings", admin, `{"new_name":"phone2","scopes":["serial_write"]}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("escalation: %d", rec.Code)
	}
	rec = e.Do("POST", "/api/pairings", admin, `{"new_name":"phone3","scopes":["read","admin"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a pairing with the admin scope: %d %s", rec.Code, rec.Body)
	}
	e.Principal("codex", auth.KindAgent, auth.ScopeRead)
	if rec := e.Do("POST", "/api/pairings", admin, `{"principal":"codex"}`); rec.Code != http.StatusConflict {
		t.Errorf("pairing an agent: %d", rec.Code)
	}
}

// Tokens no longer carry admin, so the admin API answers only an admin
// session, and only on the admin interface.
func TestTheAdminAPINeedsAnAdminSessionOnTheAdminInterface(t *testing.T) {
	e := newEnv(t)
	admin := e.Admin()
	human := e.Principal("guillaume", auth.KindHuman, auth.ScopeRead, auth.ScopeMessage, auth.ScopeIngest)
	if rec := e.Do("GET", "/api/tokens", nil, nil, "Authorization", "Bearer "+human); rec.Code != http.StatusForbidden {
		t.Errorf("a bearer token on the admin API: %d", rec.Code)
	}
	if rec := e.Do("GET", "/api/tokens", admin, nil); rec.Code != http.StatusOK {
		t.Errorf("an admin session: %d %s", rec.Code, rec.Body)
	}
	rec := e.Do("POST", "/api/tokens", admin, `{"principal":"codex","kind":"agent","scopes":["read","admin"]}`)
	if body := decode[dto.Error](t, rec.Body.Bytes()); rec.Code != http.StatusBadRequest || !strings.Contains(body.Error.Message, "admin") {
		t.Errorf("a token with the admin scope: %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/api/tokens", "/api/pairings"} {
		if rec := e.DoPublic("GET", path, admin, nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s on the public interface: %d", path, rec.Code)
		}
	}
	// The public interface ignores the admin cookie on the routes it serves.
	if rec := e.DoPublic("GET", "/api/me", admin, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("the admin cookie on the public interface: %d", rec.Code)
	}
}
