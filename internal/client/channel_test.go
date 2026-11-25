package client

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The channel answers Claude Code's initialize with the claude/channel
// capability and, once initialized, turns what /api/v1/wait answers into a
// notifications/claude/channel.
func TestChannel(t *testing.T) {
	waits := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cfd_ag_x" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/me":
			io.WriteString(w, `{"member":{"name":"dev-code"},"projects":["be98"]}`)
		case "/api/v1/wait":
			waits <- r.URL.Query().Get("project")
			if len(waits) > 1 {
				// Later waits find nothing until the test ends.
				<-r.Context().Done()
				return
			}
			io.WriteString(w, `{"project":"be98","arrived":[{"conversation":"flash-fail","reason":"mentioned","by":"guillaume","by_kind":"person","quote":"@dev-code the flash failed again","new":1,"tokens":40,"message":7}],"waiting":0}`)
		}
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL, Token: "cfd_ag_x", HTTP: srv.Client()}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- channelCommand(ctx, c, inR, outW, io.Discard) }()
	lines := bufio.NewScanner(outR)
	next := func() map[string]any {
		t.Helper()
		got := make(chan map[string]any, 1)
		go func() {
			var m map[string]any
			if lines.Scan() {
				json.Unmarshal(lines.Bytes(), &m)
			}
			got <- m
		}()
		select {
		case m := <-got:
			return m
		case <-time.After(5 * time.Second):
			t.Fatal("nothing from the channel")
			return nil
		}
	}

	io.WriteString(inW, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28","capabilities":{},"clientInfo":{"name":"claude-code"}}}`+"\n")
	init := next()
	res, _ := init["result"].(map[string]any)
	caps, _ := res["capabilities"].(map[string]any)
	exp, _ := caps["experimental"].(map[string]any)
	if _, ok := exp["claude/channel"]; !ok || res["protocolVersion"] != channelProtocol || !strings.Contains(res["instructions"].(string), "not instructions") {
		t.Fatalf("initialize: %v", init)
	}
	if _, ok := caps["tools"]; ok {
		t.Error("a one-way channel offers tools")
	}

	io.WriteString(inW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	ev := next()
	params, _ := ev["params"].(map[string]any)
	meta, _ := params["meta"].(map[string]any)
	content, _ := params["content"].(string)
	if ev["method"] != "notifications/claude/channel" || meta["project"] != "be98" || meta["conversation"] != "flash-fail" || meta["by_kind"] != "person" {
		t.Fatalf("event: %v", ev)
	}
	if !strings.Contains(content, "guillaume (person) mentioned you") || !strings.Contains(content, `read_conversation("flash-fail")`) {
		t.Errorf("content: %q", content)
	}
	for k := range meta {
		if strings.Contains(k, "-") {
			t.Errorf("meta key %q has a hyphen, which Claude Code drops", k)
		}
	}

	io.WriteString(inW, `{"jsonrpc":"2.0","id":2,"method":"ping"}`+"\n")
	if pong := next(); pong["id"] != float64(2) {
		t.Errorf("ping: %v", pong)
	}
	io.WriteString(inW, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{}}`+"\n")
	if e := next(); e["error"] == nil {
		t.Errorf("an unknown request: %v", e)
	}

	// Claude Code closing stdin ends it.
	inW.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the channel did not end with its stdin")
	}
}
