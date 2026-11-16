package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/store"
)

func callResult(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string, map[string]any) {
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
	return res, text.String(), structured
}

// callFails calls a tool that must fail with code.
func callFails(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, code string) string {
	t.Helper()
	res, text, out := callResult(t, cs, name, args)
	if !res.IsError || out["code"] != code {
		t.Fatalf("%s = %q %v, want error %s", name, text, out, code)
	}
	return text
}

func (s *server) agent(name string) (string, *mcp.ClientSession) {
	tok := s.token(name, auth.KindAgent, auth.ScopeRead, auth.ScopeMessage)
	return tok, s.mcp(tok)
}

func (s *server) ingest(stream, body string) {
	s.t.Helper()
	resp := s.post(s.admin, "/api/log?stream="+stream, "text/plain", body)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		s.t.Fatalf("ingest: %d %s", resp.StatusCode, b)
	}
}

func (s *server) get(token, path string) (*http.Response, []byte) {
	s.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, s.base+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func num(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
}

func TestAgentImageArrivesAsImageContent(t *testing.T) {
	s := startServer(t)
	_, a := s.agent("agent-a")
	tokB, b := s.agent("agent-b")
	tokC, _ := s.agent("agent-c")
	data := base64.StdEncoding.EncodeToString(testPNG(t, 1800, 1200))
	_, out := call(t, a, "send", map[string]any{"text": "the scope screen", "to": "agent-b", "images": []any{map[string]any{"data": data, "mime": "image/png"}}})
	seq := num(out["seq"])

	text, inbox := call(t, b, "inbox", nil)
	if !strings.Contains(text, "the scope screen") || !strings.Contains(text, "[1 image(s)]") || len(inbox["entries"].([]any)) != 1 {
		t.Fatalf("inbox = %q", text)
	}
	res, text, gm := callResult(t, b, "get_message", map[string]any{"seq": seq})
	if res.IsError {
		t.Fatalf("get_message: %s", text)
	}
	var images []*mcp.ImageContent
	for _, c := range res.Content {
		if ic, ok := c.(*mcp.ImageContent); ok {
			images = append(images, ic)
		}
	}
	if len(images) != 1 || images[0].MIMEType != "image/jpeg" {
		t.Fatalf("image content = %d %v", len(images), res.Content)
	}
	img, _, err := image.Decode(bytes.NewReader(images[0].Data))
	if err != nil || img.Bounds().Dx() != 1568 || img.Bounds().Dy() != 1045 {
		t.Errorf("agent variant = %v %v", img.Bounds(), err)
	}
	entry := gm["entry"].(map[string]any)
	att := entry["attachments"].([]any)[0].(map[string]any)
	sha := att["sha256"].(string)
	if entry["chat"] != "dm:agent-a" || att["thumb_url"] != "/blob/"+sha+"?v=thumb" {
		t.Errorf("entry = %v", entry)
	}

	// The blob is served to the parties only, with nosniff.
	resp, body := s.get(tokB, "/blob/"+sha+"?v=thumb")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Content-Disposition") != "inline" {
		t.Errorf("blob for b = %d %v", resp.StatusCode, resp.Header)
	}
	if thumb, _, err := image.Decode(bytes.NewReader(body)); err != nil || thumb.Bounds().Dx() != 320 {
		t.Errorf("thumb = %v", err)
	}
	if resp, _ := s.get(tokC, "/blob/"+sha); resp.StatusCode != http.StatusNotFound {
		t.Errorf("blob for c = %d", resp.StatusCode)
	}
	if resp, _ := s.get("", "/blob/"+sha); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("blob without a token = %d", resp.StatusCode)
	}
	if resp, _ := s.get(tokB, fmt.Sprintf("/api/messages/%d", seq)); resp.StatusCode != http.StatusOK {
		t.Errorf("GET message for b = %d", resp.StatusCode)
	}
	if resp, _ := s.get(tokC, fmt.Sprintf("/api/messages/%d", seq)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET message for c = %d", resp.StatusCode)
	}
}

func TestDirectMessageNeverReachesAThirdPartyOverMCP(t *testing.T) {
	s := startServer(t)
	_, a := s.agent("agent-a")
	_, b := s.agent("agent-b")
	_, c := s.agent("agent-c")
	admin := s.mcp(s.admin)
	for _, cs := range []*mcp.ClientSession{c, admin} {
		call(t, cs, "follow", map[string]any{"channel": "general"})
	}
	_, out := call(t, a, "send", map[string]any{"text": "the root password is hunter2", "to": "agent-b"})
	seq := num(out["seq"])
	if text, _ := call(t, b, "inbox", nil); !strings.Contains(text, "hunter2") {
		t.Errorf("b inbox = %q", text)
	}
	for name, cs := range map[string]*mcp.ClientSession{"agent-c": c, "admin": admin} {
		if text, _ := call(t, cs, "inbox", map[string]any{"include_acked": true}); strings.Contains(text, "hunter2") {
			t.Errorf("%s inbox shows the DM: %q", name, text)
		}
		callFails(t, cs, "get_message", map[string]any{"seq": seq}, "not_found")
		callFails(t, cs, "context", map[string]any{"seq": seq}, "not_found")
		callFails(t, cs, "get_log_attachment", map[string]any{"seq": seq, "index": 0}, "not_found")
		if text, _ := call(t, cs, "read", map[string]any{"streams": []string{"dm:*", "*"}, "since_seq": 0}); strings.Contains(text, "hunter2") {
			t.Errorf("%s read the DM: %q", name, text)
		}
		if text, _ := call(t, cs, "channel_timeline", map[string]any{"channel": "general"}); strings.Contains(text, "hunter2") {
			t.Errorf("%s timeline shows the DM", name)
		}
		if _, out := call(t, cs, "ack", map[string]any{"seqs": []int64{seq}}); num(out["acked"]) != 0 {
			t.Errorf("%s acked the DM", name)
		}
		_, out := call(t, cs, "wait_message", map[string]any{"timeout_s": 1})
		if out["timed_out"] != true {
			t.Errorf("%s wait_message returned %v", name, out)
		}
		callFails(t, cs, "send", map[string]any{"text": "re", "reply_to": seq}, "not_found")
		res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: fmt.Sprintf("cafard://message/%d", seq)})
		if err == nil {
			t.Errorf("%s read the DM resource: %v", name, res.Contents[0].Text)
		}
		res, err = cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "cafard://inbox"})
		if err != nil || strings.Contains(res.Contents[0].Text, "hunter2") {
			t.Errorf("%s inbox resource = %v %v", name, res, err)
		}
	}
	// A wait in progress is not woken by someone else's DM either.
	done := make(chan map[string]any, 1)
	go func() {
		_, out := call(t, c, "wait_message", map[string]any{"timeout_s": 2})
		done <- out
	}()
	time.Sleep(300 * time.Millisecond)
	call(t, a, "send", map[string]any{"text": "second secret", "to": "agent-b"})
	if out := <-done; out["timed_out"] != true {
		t.Errorf("c's wait was released by a DM to b: %v", out)
	}
}

func TestBroadcastIsSeenByBothAgentsAndAckedIndependently(t *testing.T) {
	s := startServer(t)
	_, a := s.agent("agent-a")
	_, b := s.agent("agent-b")
	for _, cs := range []*mcp.ClientSession{a, b} {
		call(t, cs, "follow", map[string]any{"channel": "general"})
	}
	// The human posts through POST /api/messages with a bearer token.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("chat", "chan:general")
	_ = mw.WriteField("text", "Photo of the bench after the reset")
	fw, _ := mw.CreateFormFile("files[]", "bench.png")
	_, _ = fw.Write(testPNG(t, 40, 30))
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, s.base+"/api/messages", &body)
	req.Header.Set("Authorization", "Bearer "+s.admin)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Cafard", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var entry map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&entry)
	if resp.StatusCode != http.StatusCreated || entry["sender"].(map[string]any)["name"] != "admin" || len(entry["attachments"].([]any)) != 1 {
		t.Fatalf("POST /api/messages = %d %v", resp.StatusCode, entry)
	}
	seq := num(entry["seq"])
	for name, cs := range map[string]*mcp.ClientSession{"a": a, "b": b} {
		if text, _ := call(t, cs, "inbox", nil); !strings.Contains(text, "Photo of the bench") {
			t.Errorf("%s inbox = %q", name, text)
		}
	}
	call(t, a, "ack", map[string]any{"seqs": []int64{seq}})
	if text, _ := call(t, a, "inbox", nil); !strings.Contains(text, "empty") {
		t.Errorf("a after ack = %q", text)
	}
	if text, _ := call(t, b, "inbox", nil); !strings.Contains(text, "Photo of the bench") {
		t.Errorf("b lost the message when a acked: %q", text)
	}
	call(t, b, "get_message", map[string]any{"seq": seq})
	_, gm := call(t, a, "get_message", map[string]any{"seq": seq, "ack": false})
	seen := gm["entry"].(map[string]any)["seen_by"].([]any)
	if len(seen) != 2 {
		t.Errorf("seen_by = %v", seen)
	}
	// A form post without X-Cafard gets a 303 to the chat.
	form := strings.NewReader("chat=chan%3Ageneral&text=no+js")
	req, _ = http.NewRequest(http.MethodPost, s.base+"/api/messages", form)
	req.Header.Set("Authorization", "Bearer "+s.admin)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/c/general" {
		t.Errorf("form post = %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	req, _ = http.NewRequest(http.MethodPost, s.base+"/api/messages", strings.NewReader("chat=chan%3Anope&text=x"))
	req.Header.Set("Authorization", "Bearer "+s.admin)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/c/nope?error=unknown_channel" {
		t.Errorf("failed form post = %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestASpammingAgentIsRateLimited(t *testing.T) {
	s := startServer(t)
	_, a := s.agent("agent-a")
	for i := range 10 {
		call(t, a, "send", map[string]any{"text": fmt.Sprintf("ping %d", i)})
	}
	text := callFails(t, a, "send", map[string]any{"text": "ping again"}, "rate_limited")
	if !strings.Contains(text, "per minute") {
		t.Errorf("rate limit sentence = %q", text)
	}
	_, _, out := callResult(t, a, "send", map[string]any{"text": "ping again"})
	if num(out["retry_after_s"]) < 1 {
		t.Errorf("retry_after_s = %v", out)
	}
}

func TestChannelBoundToAStreamDrivesTheLogTools(t *testing.T) {
	s := startServer(t)
	_, a := s.agent("agent-a")
	s.ingest("serial:dut1", "booting\n")
	s.ingest("serial:dut2", "other board\n")
	_, out := call(t, a, "create_channel", map[string]any{"name": "run-42", "streams": []string{"serial:dut1"}})
	if out["channel"].(map[string]any)["followed"] != true {
		t.Errorf("creator does not follow: %v", out)
	}

	got := make(chan map[string]any, 1)
	go func() {
		_, out := call(t, a, "wait_for", map[string]any{"pattern": "login:", "channel": "run-42", "timeout_s": 20})
		got <- out
	}()
	time.Sleep(300 * time.Millisecond)
	s.ingest("serial:dut2", "dut2 login: \n")
	s.ingest("serial:dut1", "dut1 login: \n")
	select {
	case out := <-got:
		if out["matched"] != true || out["streams"].([]any)[0] != "serial:dut1" {
			t.Errorf("wait_for = %v", out)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("wait_for channel=run-42 was not released")
	}
	text, out := call(t, a, "read", map[string]any{"channel": "run-42", "since_seq": 0})
	if !strings.Contains(text, "dut1 login") || strings.Contains(text, "dut2") {
		t.Errorf("read channel= %q", text)
	}
	if text, _ := call(t, a, "tail", map[string]any{"channel": "run-42"}); strings.Contains(text, "dut2") || !strings.Contains(text, "booting") {
		t.Errorf("tail channel= %q", text)
	}
	if _, out := call(t, a, "list_streams", map[string]any{"channel": "run-42"}); len(out["streams"].([]any)) != 1 {
		t.Errorf("list_streams channel= %v", out)
	}
	call(t, a, "claim", map[string]any{"resource": "serial:dut2"})
	if _, out := call(t, a, "list_claims", map[string]any{"channel": "run-42"}); len(out["claims"].([]any)) != 0 {
		t.Errorf("list_claims channel= %v", out)
	}
	text = callFails(t, a, "read", map[string]any{"channel": "run-43"}, "unknown_channel")
	if !strings.Contains(text, "general, run-42") {
		t.Errorf("unknown channel sentence = %q", text)
	}
	// Binding changes leave a marker in the chat.
	call(t, a, "update_channel", map[string]any{"name": "run-42", "add_streams": []string{"serial:dut2"}})
	if text, _ := call(t, a, "channel_timeline", map[string]any{"channel": "run-42"}); !strings.Contains(text, "agent-a bound serial:dut2") {
		t.Errorf("timeline = %q", text)
	}
	if text, _ := call(t, a, "list_channels", nil); !strings.Contains(text, "#run-42") || !strings.Contains(text, "serial:dut1 (") {
		t.Errorf("list_channels = %q", text)
	}
}

func TestGlobBindingPicksUpAStreamCreatedLater(t *testing.T) {
	s := startServer(t)
	_, a := s.agent("agent-a")
	call(t, a, "create_channel", map[string]any{"name": "dut1", "streams": []string{"syslog:dut1*"}})
	got := make(chan map[string]any, 1)
	go func() {
		_, out := call(t, a, "wait_for", map[string]any{"pattern": "hello", "channel": "dut1", "timeout_s": 20})
		got <- out
	}()
	time.Sleep(300 * time.Millisecond)
	s.ingest("syslog:dut1-bmc", "hello from a new stream\n")
	select {
	case out := <-got:
		if out["matched"] != true {
			t.Errorf("wait_for = %v", out)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a stream created after the binding did not release wait_for")
	}
	if text, _ := call(t, a, "read", map[string]any{"channel": "dut1", "since_seq": 0}); !strings.Contains(text, "syslog:dut1-bmc") {
		t.Errorf("read = %q", text)
	}
}

func TestLogAttachmentOf73LinesSurvivesRetention(t *testing.T) {
	s := startServer(t)
	_, a := s.agent("agent-a")
	_, b := s.agent("agent-b")
	var body strings.Builder
	for i := 1; i <= 73; i++ {
		fmt.Fprintf(&body, "boot line %d\n", i)
	}
	s.ingest("serial:dut1", body.String())
	_, page := call(t, a, "read", map[string]any{"streams": []string{"serial:dut1"}, "since_seq": 0})
	next := num(page["next_seq"])
	from, to := next-73, next-1
	_, out := call(t, a, "send", map[string]any{"text": "failure on boot 3", "to": "agent-b", "logs": []any{
		map[string]any{"stream": "serial:dut1", "from_seq": from, "to_seq": to, "title": "boot 3"},
		map[string]any{"stream": "serial:dut1", "from_seq": from},
	}})
	logs := out["logs"].([]any)
	if num(logs[0].(map[string]any)["lines"]) != 73 || logs[0].(map[string]any)["snapshot"] != true {
		t.Fatalf("send logs = %v", logs)
	}
	seq := num(out["seq"])
	text, _ := call(t, b, "get_message", map[string]any{"seq": seq})
	if !strings.Contains(text, "73 lines") || !strings.Contains(text, "boot line 1\n") || !strings.Contains(text, "boot line 73") {
		t.Errorf("get_message:\n%s", text)
	}

	// Retention deletes the stream's lines.
	st, err := store.Open(context.Background(), filepath.Join(s.dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`DELETE FROM entries WHERE stream = 'serial:dut1'`); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if text, _ := call(t, b, "read", map[string]any{"streams": []string{"serial:dut1"}, "since_seq": 0}); !strings.Contains(text, "No entries") {
		t.Fatalf("lines still there: %q", text)
	}
	text, _ = call(t, b, "get_message", map[string]any{"seq": seq, "ack": false})
	if !strings.Contains(text, "boot line 73") {
		t.Errorf("snapshot lost after retention:\n%s", text)
	}
	text, out = call(t, b, "get_log_attachment", map[string]any{"seq": seq, "index": 0})
	if out["snapshot"] != true || !strings.Contains(text, "boot line 42") {
		t.Errorf("get_log_attachment after retention = %q %v", text, out)
	}
	_, out = call(t, b, "get_log_attachment", map[string]any{"seq": seq, "index": 1})
	if out["expired"] != true {
		t.Errorf("open-ended after retention = %v", out)
	}

	// The open-ended attachment keeps delivering new lines.
	s.ingest("serial:dut1", "after the send 1\nafter the send 2\n")
	text, out = call(t, b, "get_log_attachment", map[string]any{"seq": seq, "index": 1, "since_seq": to + 1})
	if !strings.Contains(text, "after the send 2") || num(out["shown"]) != 2 {
		t.Errorf("open-ended = %q %v", text, out)
	}
	s.ingest("serial:dut1", "after the send 3\n")
	text, _ = call(t, b, "get_log_attachment", map[string]any{"seq": seq, "index": 1, "since_seq": num(out["next_seq"])})
	if !strings.Contains(text, "after the send 3") || strings.Contains(text, "after the send 2") {
		t.Errorf("open-ended continued = %q", text)
	}
	resp, raw := s.get(s.token("agent-z", auth.KindAgent, auth.ScopeRead), fmt.Sprintf("/api/messages/%d/attachments/0", seq))
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("third party HTTP attachment = %d %s", resp.StatusCode, raw)
	}
}

func TestClaimsAreExclusiveAndLeaveMarkers(t *testing.T) {
	s := startServer(t)
	_, a := s.agent("agent-a")
	_, b := s.agent("agent-b")
	s.ingest("serial:dut1", "idle\n")
	call(t, a, "claim", map[string]any{"resource": "serial:dut1", "note": "flashing"})
	text := callFails(t, b, "claim", map[string]any{"resource": "serial:dut1"}, "conflict")
	if !strings.Contains(text, "agent-a") {
		t.Errorf("conflict sentence = %q", text)
	}
	if text, out := call(t, b, "list_streams", nil); !strings.Contains(text, "claimed_by=agent-a") ||
		out["streams"].([]any)[0].(map[string]any)["claim"].(map[string]any)["principal"] != "agent-a" {
		t.Errorf("list_streams = %q", text)
	}
	call(t, a, "release", map[string]any{"resource": "serial:dut1"})
	call(t, b, "claim", map[string]any{"resource": "serial:dut1", "ttl_s": 1})
	callFails(t, a, "claim", map[string]any{"resource": "serial:dut1"}, "conflict")
	time.Sleep(1200 * time.Millisecond)
	call(t, a, "claim", map[string]any{"resource": "serial:dut1"})
	text, _ = call(t, a, "read", map[string]any{"streams": []string{"serial:dut1"}, "since_seq": 0})
	for _, want := range []string{"agent-a claimed serial:dut1", "agent-a released serial:dut1", "agent-b claimed serial:dut1 for 1s", "claim of agent-b on serial:dut1 expired"} {
		if !strings.Contains(text, want) {
			t.Errorf("markers lack %q:\n%s", want, text)
		}
	}
	if _, out := call(t, b, "list_claims", nil); out["claims"].([]any)[0].(map[string]any)["principal"] != "agent-a" {
		t.Errorf("list_claims = %v", out)
	}
}

func TestAskHumanIsAnsweredThroughTheAPI(t *testing.T) {
	s := startServer(t)
	_, a := s.agent("agent-a")
	got := make(chan map[string]any, 1)
	go func() {
		_, out := call(t, a, "ask_human", map[string]any{"question": "Press the reset button?", "choices": []string{"done", "can't"}, "timeout_s": 20})
		got <- out
	}()
	human := s.mcp(s.admin)
	var seq int64
	for deadline := time.Now().Add(10 * time.Second); seq == 0 && time.Now().Before(deadline); {
		_, out := call(t, human, "inbox", nil)
		for _, e := range out["entries"].([]any) {
			if m := e.(map[string]any); m["question"] != nil {
				seq = num(m["seq"])
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if seq == 0 {
		t.Fatal("the question did not reach the human")
	}
	resp := s.post(s.admin, "/api/messages", "application/x-www-form-urlencoded", fmt.Sprintf("reply_to=%d&choice=done&chat=chan%%3Ageneral", seq))
	// The 303 leads to the chat page, which takes a session: a bearer
	// client lands on the sign-in page that returns there.
	if u := resp.Request.URL; u.Path != "/c/general" && u.Query().Get("next") != "/c/general" {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("answer = %d %s", resp.StatusCode, b)
	}
	select {
	case out := <-got:
		ans, _ := out["answer"].(map[string]any)
		if ans == nil || ans["choice"] != "done" || ans["by"] != "admin" {
			t.Errorf("ask_human = %v", out)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("ask_human was not released")
	}
}

func TestInboxSubscriptionNotifiesOnlyItsPrincipal(t *testing.T) {
	s := startServer(t)
	_, a := s.agent("agent-a")
	tokB := s.token("agent-b", auth.KindAgent, auth.ScopeRead, auth.ScopeMessage)
	tokC := s.token("agent-c", auth.KindAgent, auth.ScopeRead, auth.ScopeMessage)
	notified := map[string]chan string{"b": make(chan string, 10), "c": make(chan string, 10)}
	connect := func(token, who string) *mcp.ClientSession {
		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{
			ResourceUpdatedHandler: func(_ context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
				notified[who] <- req.Params.URI
			},
		})
		cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
			Endpoint: s.base + "/mcp", HTTPClient: &http.Client{Transport: bearer{token: token, next: http.DefaultTransport}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cs.Close() })
		if err := cs.Subscribe(context.Background(), &mcp.SubscribeParams{URI: "cafard://inbox"}); err != nil {
			t.Fatal(err)
		}
		return cs
	}
	b := connect(tokB, "b")
	connect(tokC, "c")
	call(t, a, "send", map[string]any{"text": "for b only", "to": "agent-b"})
	select {
	case uri := <-notified["b"]:
		if uri != "cafard://inbox" {
			t.Errorf("uri = %s", uri)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("b was not notified")
	}
	select {
	case <-notified["c"]:
		t.Error("c was notified of b's message")
	case <-time.After(time.Second):
	}
	res, err := b.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "cafard://inbox"})
	if err != nil || !strings.Contains(res.Contents[0].Text, "for b only") {
		t.Errorf("inbox resource = %v %v", res, err)
	}
	s.ingest("serial:dut1", "x\n")
	res, err = b.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "cafard://stream/serial:dut1"})
	if err != nil || !strings.Contains(res.Contents[0].Text, "serial:dut1 INFO x") {
		t.Errorf("stream resource = %v %v", res, err)
	}
	res, err = b.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "cafard://channel/general"})
	if err != nil || !strings.Contains(res.Contents[0].Text, "#general") {
		t.Errorf("channel resource = %v %v", res, err)
	}
}

func TestMessageToolsFollowScopes(t *testing.T) {
	s := startServer(t)
	ro := s.mcp(s.token("reader", auth.KindAgent, auth.ScopeRead))
	res, err := ro.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"inbox", "get_message", "get_log_attachment", "wait_message", "list_channels", "channel_timeline", "list_claims", "list_principals"} {
		if !slices.Contains(names, want) {
			t.Errorf("read scope lacks %s", want)
		}
	}
	for _, never := range []string{"send", "ask_human", "ack", "claim", "create_channel", "follow"} {
		if slices.Contains(names, never) {
			t.Errorf("read scope offers %s", never)
		}
	}
}
