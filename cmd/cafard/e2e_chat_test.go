package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
)

// Track D: the M3 "Done when" checks a test can run, through a real
// server and a browser session.

// noRedirect is a client that shows redirects instead of following them.
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// session signs in with the admin token, as the sign-in form does.
func (s *server) session() *http.Cookie {
	s.t.Helper()
	resp, err := noRedirect.PostForm(s.base+"/signin", url.Values{"token": {s.admin}})
	if err != nil {
		s.t.Fatal(err)
	}
	resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookie {
			return c
		}
	}
	s.t.Fatalf("sign in: %d, no cookie", resp.StatusCode)
	return nil
}

// browser sends what the bundle sends: the cookie, Sec-Fetch-Site as a
// browser sets it, and X-Cafard for JSON answers.
func (s *server) browser(cookie *http.Cookie, method, path, ctype string, body io.Reader, bundle bool) (*http.Response, []byte) {
	s.t.Helper()
	req, _ := http.NewRequest(method, s.base+path, body)
	req.AddCookie(cookie)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if bundle {
		req.Header.Set("X-Cafard", "1")
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func (s *server) sendForm(cookie *http.Cookie, fields map[string]string, files ...[]byte) dto.Entry {
	s.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for i, f := range files {
		w, _ := mw.CreateFormFile("files[]", fmt.Sprintf("IMG_%04d.jpg", i))
		_, _ = w.Write(f)
	}
	_ = mw.Close()
	resp, body := s.browser(cookie, "POST", "/api/messages", mw.FormDataContentType(), &buf, true)
	var e dto.Entry
	if resp.StatusCode != http.StatusCreated || json.Unmarshal(body, &e) != nil {
		s.t.Fatalf("send: %d %s", resp.StatusCode, body)
	}
	return e
}

func TestAPhotoPostedWithASessionReachesAnAgent(t *testing.T) {
	s := startServer(t)
	_, agent := s.agent("claude-laptop")
	call(t, agent, "follow", map[string]any{"channel": "general"})
	cookie := s.session()
	e := s.sendForm(cookie, map[string]string{"chat": "chan:general", "text": "the scope after the reset"}, testPNG(t, 1200, 900))
	if len(e.Attachments) != 1 || e.Attachments[0].Image == nil || e.Sender == nil || e.Sender.Name != "admin" {
		t.Fatalf("sent = %+v", e)
	}
	res, text, _ := callResult(t, agent, "get_message", map[string]any{"seq": e.Seq})
	var images int
	for _, c := range res.Content {
		if ic, ok := c.(*mcp.ImageContent); ok && ic.MIMEType == "image/jpeg" && len(ic.Data) > 0 {
			images++
		}
	}
	if res.IsError || images != 1 || !strings.Contains(text, "the scope after the reset") || !strings.Contains(text, "from admin (human)") {
		t.Fatalf("get_message = %q, %d images", text, images)
	}
	// The sender comes from the session, never from the form.
	e = s.sendForm(cookie, map[string]string{"chat": "chan:general", "text": "who am I", "sender": "claude-laptop", "from": "claude-laptop"})
	if e.Sender.Name != "admin" {
		t.Errorf("sender = %s", e.Sender.Name)
	}
}

func TestAQuestionIsAnsweredFromHomeWithThePlainForm(t *testing.T) {
	s := startServer(t)
	_, agent := s.agent("claude-laptop")
	cookie := s.session()
	got := make(chan map[string]any, 1)
	go func() {
		_, out := call(t, agent, "ask_human", map[string]any{"question": "Power-cycle dut1?", "choices": []string{"done", "can't"}, "timeout_s": 30})
		got <- out
	}()
	var home string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		_, b := s.browser(cookie, "GET", "/", "", nil, false)
		if home = string(b); strings.Contains(home, "Power-cycle dut1?") {
			break
		}
	}
	if !strings.Contains(home, "Needs you") || !strings.Contains(home, `name="choice" value="done"`) {
		t.Fatalf("home has no answerable question:\n%s", home)
	}
	var o dto.Overview
	_, b := s.browser(cookie, "GET", "/api/overview", "", nil, true)
	if err := json.Unmarshal(b, &o); err != nil || len(o.Questions) != 1 {
		t.Fatalf("overview = %s", b)
	}
	q := o.Questions[0]
	// The buttons of the card post this form; no script involved.
	form := url.Values{"chat": {q.Chat}, "reply_to": {fmt.Sprint(q.Seq)}, "back": {"/"}, "next": {"/"}, "choice": {"done"}, "text": {""}}
	resp, body := s.browser(cookie, "POST", "/api/messages", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), false)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("answer = %d %s %s", resp.StatusCode, resp.Header.Get("Location"), body)
	}
	select {
	case out := <-got:
		ans, _ := out["answer"].(map[string]any)
		if ans == nil || ans["choice"] != "done" || ans["by"] != "admin" {
			t.Errorf("ask_human = %v", out)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("ask_human was not released")
	}
}

func TestAButtonChoiceFromTheBundleReleasesAskHuman(t *testing.T) {
	s := startServer(t)
	_, agent := s.agent("claude-laptop")
	cookie := s.session()
	got := make(chan map[string]any, 1)
	go func() {
		_, out := call(t, agent, "ask_human", map[string]any{"question": "Flash it?", "choices": []string{"yes", "no"}, "timeout_s": 30})
		got <- out
	}()
	var o dto.Overview
	for deadline := time.Now().Add(10 * time.Second); len(o.Questions) == 0 && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		_, b := s.browser(cookie, "GET", "/api/overview", "", nil, true)
		_ = json.Unmarshal(b, &o)
	}
	if len(o.Questions) == 0 {
		t.Fatal("no question")
	}
	e := s.sendForm(cookie, map[string]string{"chat": o.Questions[0].Chat, "reply_to": fmt.Sprint(o.Questions[0].Seq), "choice": "yes"})
	if e.Text != "yes" {
		t.Errorf("reply = %+v", e)
	}
	select {
	case out := <-got:
		if ans, _ := out["answer"].(map[string]any); ans == nil || ans["choice"] != "yes" {
			t.Errorf("ask_human = %v", out)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("ask_human was not released")
	}
}

func TestALogRangeAttachedFromTheUIReachesAnAgent(t *testing.T) {
	s := startServer(t)
	_, agent := s.agent("codex-build")
	cookie := s.session()
	var nd strings.Builder
	for i := 1; i <= 40; i++ {
		sev := 6
		if i%10 == 0 {
			sev = 3
		}
		fmt.Fprintf(&nd, `{"text":"usb probe %02d","severity":%d}`+"\n", i, sev)
		fmt.Fprintf(&nd, `{"text":"other line %02d","severity":3}`+"\n", i)
	}
	if resp := s.post(s.admin, "/api/log?stream=serial:dut1", "application/x-ndjson", nd.String()); resp.StatusCode != http.StatusOK {
		t.Fatalf("ingest: %d", resp.StatusCode)
	}
	// What the logs pane shows with sev=3 and re=usb, as the UI reads it.
	_, b := s.browser(cookie, "GET", "/api/entries?streams=serial:dut1&sev=3&re=usb", "", nil, true)
	var page dto.Page[dto.Entry]
	if err := json.Unmarshal(b, &page); err != nil || len(page.Items) != 4 {
		t.Fatalf("entries = %s", b)
	}
	from, to := page.Items[0].Seq, page.Items[len(page.Items)-1].Seq
	sev := 3
	logs, _ := json.Marshal([]dto.LogRequest{{Stream: "serial:dut1", FromSeq: from, ToSeq: &to, Grep: "usb", MinSeverity: &sev, Title: "usb errors"}})
	e := s.sendForm(cookie, map[string]string{"chat": "dm:codex-build", "text": "see these", "logs": string(logs)})
	la := e.Attachments[0].Log
	if la == nil || la.Lines != 4 || la.Grep != "usb" || la.MinSeverity == nil || *la.MinSeverity != 3 {
		t.Fatalf("attachment = %+v", e.Attachments)
	}
	text, out := call(t, agent, "get_message", map[string]any{"seq": e.Seq})
	for _, want := range []string{"usb probe 10", "usb probe 20", "usb probe 30", "usb probe 40"} {
		if !strings.Contains(text, want) {
			t.Errorf("get_message lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "other line") || strings.Contains(text, "usb probe 11") {
		t.Errorf("get_message shows lines the UI did not:\n%s", text)
	}
	entry := out["entry"].(map[string]any)
	att := entry["attachments"].([]any)[0].(map[string]any)
	if att["grep"] != "usb" || num(att["min_severity"]) != 3 {
		t.Errorf("filters not carried: %v", att)
	}
}

// sseEvent is one event of /api/events.
type sseEvent struct {
	name string
	data string
}

func (s *server) events(ctx context.Context, cookie *http.Cookie, query string) <-chan sseEvent {
	s.t.Helper()
	req, _ := http.NewRequestWithContext(ctx, "GET", s.base+"/api/events?"+query, nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("events: %d", resp.StatusCode)
	}
	out := make(chan sseEvent, 64)
	go func() {
		defer resp.Body.Close()
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			case line == "" && ev.name != "":
				out <- ev
				ev = sseEvent{}
			}
		}
	}()
	return out
}

// waitEvent reads events until match accepts one.
func waitEvent(t *testing.T, ch <-chan sseEvent, what string, match func(sseEvent) bool) sseEvent {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("feed closed waiting for %s", what)
			}
			if match(ev) {
				return ev
			}
		case <-timeout:
			t.Fatalf("no %s", what)
		}
	}
}

func TestCountsUpdateWhenAQuestionArrives(t *testing.T) {
	s := startServer(t)
	_, agent := s.agent("claude-laptop")
	cookie := s.session()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feed := s.events(ctx, cookie, "scope=home")
	counts := func(ev sseEvent) dto.Counts {
		var c dto.Counts
		_ = json.Unmarshal([]byte(ev.data), &c)
		return c
	}
	first := waitEvent(t, feed, "first counts", func(ev sseEvent) bool { return ev.name == "counts" })
	if c := counts(first); c.OpenQuestions != 0 {
		t.Fatalf("first counts = %+v", c)
	}
	call(t, agent, "ask_human", map[string]any{"question": "Reset the board?", "choices": []string{"yes"}, "timeout_s": 0})
	waitEvent(t, feed, "the question entry", func(ev sseEvent) bool { return ev.name == "entry" && strings.Contains(ev.data, "Reset the board?") })
	ev := waitEvent(t, feed, "counts with the question", func(ev sseEvent) bool { return ev.name == "counts" && counts(ev).OpenQuestions == 1 })
	c := counts(ev)
	if len(c.Chats) != 1 || c.Chats[0].ID != "chan:general" || c.Chats[0].OpenQuestions != 1 {
		t.Errorf("counts = %+v", c)
	}
	// Answering sends "answered" and clears the badge.
	var o dto.Overview
	_, b := s.browser(cookie, "GET", "/api/overview", "", nil, true)
	_ = json.Unmarshal(b, &o)
	s.sendForm(cookie, map[string]string{"chat": "chan:general", "reply_to": fmt.Sprint(o.Questions[0].Seq), "choice": "yes"})
	waitEvent(t, feed, "answered", func(ev sseEvent) bool { return ev.name == "answered" && strings.Contains(ev.data, `"choice":"yes"`) })
	waitEvent(t, feed, "counts back to 0", func(ev sseEvent) bool { return ev.name == "counts" && counts(ev).OpenQuestions == 0 })
}

func TestAMarkerInABoundChannelIsInTheLogsAndTheChat(t *testing.T) {
	s := startServer(t)
	_, agent := s.agent("codex-build")
	cookie := s.session()
	s.ingest("serial:dut1", "U-Boot 2024.04\n")
	call(t, agent, "create_channel", map[string]any{"name": "run-42", "streams": []string{"serial:dut1"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feed := s.events(ctx, cookie, "chat=chan:run-42")
	waitEvent(t, feed, "counts", func(ev sseEvent) bool { return ev.name == "counts" })
	call(t, agent, "mark", map[string]any{"stream": "serial:dut1", "label": "test 42 starts"})
	s.ingest("serial:dut1", "a line after the marker\n")
	// The chat page's feed carries the marker and the bound stream's lines.
	waitEvent(t, feed, "the marker", func(ev sseEvent) bool {
		return ev.name == "entry" && strings.Contains(ev.data, "test 42 starts") && strings.Contains(ev.data, `"stream":"serial:dut1"`)
	})
	waitEvent(t, feed, "the line", func(ev sseEvent) bool {
		return ev.name == "entry" && strings.Contains(ev.data, "a line after the marker")
	})

	// Server-rendered: an event row in the chat, and a marker row in its logs.
	_, b := s.browser(cookie, "GET", "/c/run-42", "", nil, false)
	page := string(b)
	conv := page[strings.Index(page, `data-component="ConversationView"`):strings.Index(page, `data-component="LogsPane"`)]
	logs := page[strings.Index(page, `data-component="LogsPane"`):]
	if !strings.Contains(conv, `class="event-row"`) || !strings.Contains(conv, "codex-build: test 42 starts") {
		t.Errorf("no event row in the conversation")
	}
	if !strings.Contains(logs, `class="marker"`) || !strings.Contains(logs, "test 42 starts") || !strings.Contains(logs, "a line after the marker") {
		t.Errorf("the logs pane lacks the marker or the line")
	}
	_, b = s.browser(cookie, "GET", "/c/run-42?view=logs", "", nil, false)
	if !strings.Contains(string(b), `data-view="logs"`) || !strings.Contains(string(b), "test 42 starts") {
		t.Error("the logs view lacks the marker")
	}
	var tl dto.Page[dto.Entry]
	_, b = s.browser(cookie, "GET", "/api/chats/chan:run-42/timeline", "", nil, true)
	_ = json.Unmarshal(b, &tl)
	found := false
	for _, e := range tl.Items {
		if e.Event && strings.Contains(e.Text, "test 42 starts") {
			found = true
		}
	}
	if !found {
		t.Errorf("timeline = %s", b)
	}
}
