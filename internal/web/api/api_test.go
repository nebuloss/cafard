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
	pairings := auth.NewPairings(e.Store.DB(), e.Clock, e.Sessions)
	api.New(e.Deps, e.Sessions, pairings, view.NewKinds(e.Tokens)).Mount(e.Mux)
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
		webtest.Line("serial:dut1", "boot"),
		store.Entry{Stream: "chan:general", Kind: store.KindMessage, Severity: 6, Text: "secret plans", Sender: "alice"},
		store.Entry{Stream: "dm:alice~bob", Kind: store.KindMessage, Severity: 6, Text: "private", Sender: "alice", Recipient: "bob"},
		webtest.Line("serial:dut1", "login:"),
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
	code, page := get[dto.Page[dto.Entry]](t, e, "/api/entries?streams=serial:dut1", tok)
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
		e.Commit(store.Entry{Stream: "push:a", Kind: store.KindLine, Severity: sev, Text: "line " + strconv.Itoa(i+1)})
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
	e.Commit(webtest.Line("serial:dut1", "x"))
	_, me := get[dto.Me](t, e, "/api/me", tok)
	if me.Name != "codex" || me.SessionID != 0 || me.Theme != dto.ThemeSystem || len(me.Scopes) != 2 {
		t.Errorf("me = %+v", me)
	}
	rec := e.Do("POST", "/api/marks", nil, `{"stream":"serial:dut1","label":"test 42 starts"}`, "Authorization", "Bearer "+tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("mark: %d %s", rec.Code, rec.Body)
	}
	created := decode[dto.MarkCreated](t, rec.Body.Bytes())
	m, err := e.Store.Get(t.Context(), created.Seq)
	if err != nil || m.Kind != store.KindMarker || m.Sender != "codex" || m.Text != "codex: test 42 starts" {
		t.Errorf("marker %+v %v", m, err)
	}
	if rec := e.Do("POST", "/api/marks", nil, `{"stream":"chan:general","label":"x"}`, "Authorization", "Bearer "+tok); rec.Code != 400 {
		t.Errorf("mark in a chat: %d", rec.Code)
	}
	if rec := e.Do("POST", "/api/marks", nil, `{"stream":"serial:nope","label":"x"}`, "Authorization", "Bearer "+tok); rec.Code != 404 {
		t.Errorf("mark in an unknown stream: %d", rec.Code)
	}
	e.Principal("alice", auth.KindHuman, auth.ScopeRead)
	_, ps := get[map[string][]dto.Principal](t, e, "/api/principals?kind=human", tok)
	if len(ps["principals"]) != 1 || ps["principals"][0].Name != "alice" {
		t.Errorf("principals = %+v", ps)
	}
	_, match := get[map[string][]dto.Stream](t, e, "/api/streams/match?pattern=serial:*", tok)
	if len(match["streams"]) != 1 || match["streams"][0].ID != "serial:dut1" {
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
	tok := e.Principal("admin", auth.KindHuman, auth.ScopeRead, auth.ScopeMessage, auth.ScopeIngest, auth.ScopeAdmin)
	rec := e.Do("POST", "/api/pairings", nil, `{"new_name":"phone"}`, "Authorization", "Bearer "+tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("pairing: %d %s", rec.Code, rec.Body)
	}
	p := decode[dto.Pairing](t, rec.Body.Bytes())
	if !strings.HasPrefix(p.URL, "https://cafard.lan/p/") || p.QRURL != "/pair/"+webtest.Itoa(p.ID)+"/qr.svg" || p.NewName != "phone" {
		t.Errorf("pairing = %+v", p)
	}
	rec = e.Do("POST", "/api/pairings", nil, `{"new_name":"phone2","scopes":["serial_write"]}`, "Authorization", "Bearer "+tok)
	if rec.Code != http.StatusForbidden {
		t.Errorf("escalation: %d", rec.Code)
	}
	e.Principal("codex", auth.KindAgent, auth.ScopeRead)
	if rec := e.Do("POST", "/api/pairings", nil, `{"principal":"codex"}`, "Authorization", "Bearer "+tok); rec.Code != http.StatusConflict {
		t.Errorf("pairing an agent: %d", rec.Code)
	}
}
