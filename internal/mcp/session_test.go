package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeSessions keeps ids in a map, as a real one would per caller.
type fakeSessions struct {
	n     int
	alive map[string]bool
}

func (f *fakeSessions) Start(*http.Request) (string, error) {
	f.n++
	id := fmt.Sprintf("s%d", f.n)
	f.alive[id] = true
	return id, nil
}
func (f *fakeSessions) Known(_ *http.Request, id string) bool { return f.alive[id] }
func (f *fakeSessions) End(_ *http.Request, id string)        { delete(f.alive, id) }

func TestSessions(t *testing.T) {
	s := testServer()
	sessions := &fakeSessions{alive: map[string]bool{}}
	s.Sessions = sessions
	var seen string
	s.Tools = append(s.Tools, Tool{Name: "whoami", InputSchema: json.RawMessage(`{"type":"object"}`),
		Call: func(ctx context.Context, _ json.RawMessage) (any, error) { seen = SessionID(ctx); return "ok", nil }})

	status, _, h := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`)
	id := h.Get("Mcp-Session-Id")
	if status != 200 || id == "" {
		t.Fatalf("initialize: %d, session %q", status, id)
	}
	if status, _, _ := post(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); status != http.StatusBadRequest {
		t.Errorf("no session id: %d, want 400", status)
	}
	if status, _, _ := post(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "Mcp-Session-Id", "nope"); status != http.StatusNotFound {
		t.Errorf("an unknown session: %d, want 404", status)
	}
	if status, _, _ := post(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami"}}`, "Mcp-Session-Id", id); status != 200 || seen != id {
		t.Errorf("a call in the session: %d, saw %q", status, seen)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	r.Header.Set("Mcp-Session-Id", id)
	s.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || sessions.alive[id] {
		t.Errorf("DELETE: %d, still alive %v", w.Code, sessions.alive[id])
	}
	if status, _, _ := post(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`, "Mcp-Session-Id", id); status != http.StatusNotFound {
		t.Errorf("an ended session: %d, want 404", status)
	}
}

func TestContentAndRefusals(t *testing.T) {
	s := testServer()
	s.Tools = append(s.Tools,
		Tool{Name: "photo", InputSchema: json.RawMessage(`{"type":"object"}`),
			Call: func(context.Context, json.RawMessage) (any, error) {
				return Content{{Type: "text", Text: "IMG_1.jpg"}, {Type: "image", Data: "AAAA", MimeType: "image/jpeg"}}, nil
			}},
		Tool{Name: "first", InputSchema: json.RawMessage(`{"type":"object"}`),
			Call: func(context.Context, json.RawMessage) (any, error) {
				return nil, RefuseWith("no_project_selected", "Select a project first.", map[string]any{"tool": "select_project"})
			}})
	_, out, _ := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"photo"}}`)
	content := out["result"].(map[string]any)["content"].([]any)
	if len(content) != 2 || content[1].(map[string]any)["type"] != "image" || content[1].(map[string]any)["mimeType"] != "image/jpeg" {
		t.Errorf("content: %v", content)
	}
	_, out, _ = post(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"first"}}`)
	res := out["result"].(map[string]any)
	sc, _ := res["structuredContent"].(map[string]any)
	if res["isError"] != true || sc["error"] != "no_project_selected" || sc["next"].(map[string]any)["tool"] != "select_project" {
		t.Errorf("refusal: %v", res)
	}
}
