package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/web/assets"
	"github.com/nebuloss/cafard/internal/web/webtest"
)

// Track D: the chat pages, their JSON API and the share target.

var (
	agentCodex  = auth.Principal{Name: "codex-build", Kind: auth.KindAgent, Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage), TokenID: 1}
	agentClaude = auth.Principal{Name: "claude-laptop", Kind: auth.KindAgent, Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage), TokenID: 2}
	adminP      = auth.Principal{Name: "admin", Kind: auth.KindHuman, Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage, auth.ScopeAdmin), SessionID: 1}
)

func testPNG(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 4), 120, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// seeded is what seedChats made, for the checks.
var seeded struct {
	sha      string
	question int64
	message  int64
	direct   int64
}

// seedChats: #dut1 bound to serial:dut1, an agent message with a photo
// and a log slice, an open question for the humans, a direct message.
func seedChats(e *env) {
	t := e.T
	ctx := context.Background()
	for name, kind := range map[string]string{"admin": auth.KindHuman, "codex-build": auth.KindAgent, "claude-laptop": auth.KindAgent} {
		if _, err := e.Tokens.EnsurePrincipal(ctx, name, kind); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.CreateChannel(ctx, adminP, dto.ChannelCreate{Name: "dut1", Title: "DUT 1 (x86 board)", Patterns: []string{"serial:dut1"}}); err != nil {
		t.Fatal(err)
	}
	b, err := e.svc.Blobs().Put(ctx, bytes.NewReader(testPNG(t, 64, 48)), "")
	if err != nil {
		t.Fatal(err)
	}
	to := int64(2)
	m, err := e.svc.SendMessage(ctx, agentCodex, messaging.SendInput{SendRequest: dto.SendRequest{
		Text: "image v42 ready, `dwc3` fails, see https://git.example.com/k/4a1f", Channel: "dut1",
		Logs: []dto.LogRequest{{Stream: "serial:dut1", FromSeq: 1, ToSeq: &to, Title: "boot"}},
	}, Blobs: []dto.Blob{b}})
	if err != nil {
		t.Fatal(err)
	}
	q, err := e.svc.AskHuman(ctx, agentClaude, "Power-cycle dut1?", []string{"done", "can't"}, "dut1", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	dm, err := e.svc.SendMessage(ctx, agentCodex, messaging.SendInput{SendRequest: dto.SendRequest{Text: "Build 812 is green.", To: "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	seeded.sha, seeded.question, seeded.message, seeded.direct = b.SHA256, q.Question.Seq, m.Seq, dm.Seq
}

// noBundleChats is what the chat pages must render with no script.
func noBundleChats(_ *env) map[string][]string {
	q := fmt.Sprint(seeded.question)
	return map[string][]string{
		"/": {
			"Needs you", "Power-cycle dut1?", `action="/api/messages"`, `name="choice" value="done"`, `name="reply_to" value="` + q + `"`,
			`name="next" value="/"`, `href="/c/dut1"`, `href="/dm/codex-build"`, `href="/drop"`, `href="/?all=1"`, `href="/?archived=1"`,
			"Build 812 is green.",
		},
		"/c/dut1": {
			"image v42 ready", "<code>dwc3</code>", `<a href="https://git.example.com/k/4a1f" rel="noopener noreferrer" target="_blank">`,
			`<form class="composer"`, `enctype="multipart/form-data"`, `name="files[]"`, `capture="environment"`, `name="chat" value="chan:dut1"`,
			`class="event-row"`, "codex-build: flashed linux-6.18-rc3", `href="/c/dut1?view=logs"`, `href="/c/dut1?f=questions"`,
			`href="/blob/` + seeded.sha + `"`, `href="/live?from_seq=1&amp;streams=serial%3Adut1&amp;to_seq=2"`, "Power-cycle dut1?",
			`href="/live?streams=serial%3Adut1"`, "1 question waiting for you",
		},
		"/c/dut1?view=logs": {`data-view="logs"`, "U-Boot 2024.04", `class="marker"`, `action="/c/dut1"`},
		"/dm/codex-build":   {"Build 812 is green.", `name="chat" value="dm:codex-build"`},
		"/drop":             {`<select name="chat"`, `<option value="chan:dut1">#dut1</option>`, `action="/api/messages"`, `name="back" value="/drop"`},
	}
}

func TestANoScriptRenderAcksWhatItShows(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	cookie := e.Admin()
	unread := func() int {
		ch, err := e.svc.Channel(context.Background(), "dut1")
		if err != nil {
			t.Fatal(err)
		}
		c, err := e.svc.ChannelChat(context.Background(), adminP, ch)
		if err != nil {
			t.Fatal(err)
		}
		return c.Unread
	}
	if n := unread(); n != 2 {
		t.Fatalf("unread before = %d", n)
	}
	body := e.Do("GET", "/c/dut1", cookie, nil).Body.String()
	if !assets.BundleBuilt() {
		if n := unread(); n != 0 {
			t.Errorf("unread after a no-bundle render = %d", n)
		}
		return
	}
	// With a bundle, only a browser without scripts loads the beacon.
	m := regexp.MustCompile(`<noscript><img class="seen-beacon" src="([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no read beacon")
	}
	if n := unread(); n != 2 {
		t.Errorf("the render acked with a bundle: unread = %d", n)
	}
	src := strings.ReplaceAll(m[1], "&amp;", "&")
	if rec := e.Do("GET", src, cookie, nil, "Sec-Fetch-Site", "cross-site"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site beacon: %d", rec.Code)
	}
	if rec := e.Do("GET", src, cookie, nil, "Sec-Fetch-Site", "same-origin"); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/gif" {
		t.Errorf("beacon: %d", rec.Code)
	}
	if n := unread(); n != 0 {
		t.Errorf("unread after the beacon = %d", n)
	}
}

func TestHomeAnswersAQuestionWithThePlainForm(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	cookie := e.Admin()
	form := url.Values{"chat": {"chan:dut1"}, "reply_to": {fmt.Sprint(seeded.question)}, "choice": {"done"}, "next": {"/"}, "back": {"/"}}
	rec := e.Do("POST", "/api/messages", cookie, form, "Sec-Fetch-Site", "same-origin")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("answer: %d %s %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	body := e.Do("GET", "/", cookie, nil).Body.String()
	if strings.Contains(body, fmt.Sprintf(`data-seq="%d" data-open`, seeded.question)) {
		t.Error("the answered question is still open on home")
	}
	qs, err := e.svc.QuestionsFor(context.Background(), adminP)
	if err != nil || len(qs) != 0 {
		t.Errorf("questions after answering = %v %v", qs, err)
	}
	// A wrong choice comes back to home with a banner.
	q2, _ := e.svc.AskHuman(context.Background(), agentClaude, "Flash again?", []string{"yes", "no"}, "dut1", 0, nil)
	form = url.Values{"chat": {"chan:dut1"}, "reply_to": {fmt.Sprint(q2.Question.Seq)}, "choice": {"maybe"}, "next": {"/"}, "back": {"/"}}
	rec = e.Do("POST", "/api/messages", cookie, form, "Sec-Fetch-Site", "same-origin")
	if loc := rec.Header().Get("Location"); loc != "/?error=bad_request" {
		t.Fatalf("bad choice: %s", loc)
	}
	if body := e.Do("GET", "/?error=bad_request", cookie, nil).Body.String(); !strings.Contains(body, `class="banner error"`) {
		t.Error("no banner")
	}
	if body := e.Do("GET", "/?error=%3Cb%3Ecall+me", cookie, nil).Body.String(); strings.Contains(body, "call me") {
		t.Error("an unknown error code is echoed")
	}
	// Cross-site posts with the cookie are refused.
	if rec := e.Do("POST", "/api/messages", cookie, form, "Sec-Fetch-Site", "cross-site"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site post: %d", rec.Code)
	}
}

func multipartBody(t *testing.T, fields map[string]string, files ...[]byte) (string, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for i, f := range files {
		w, _ := mw.CreateFormFile("files[]", fmt.Sprintf("IMG_%d.png", i))
		_, _ = w.Write(f)
	}
	_ = mw.Close()
	return mw.FormDataContentType(), &buf
}

func (e *env) post(path string, cookie *http.Cookie, ctype string, body *bytes.Buffer, header ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, body)
	r.Header.Set("Content-Type", ctype)
	r.AddCookie(cookie)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	e.Mux.ServeHTTP(rec, r)
	return rec
}

func TestTheShareTargetStagesFilesForTheDropForm(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	cookie := e.Admin()
	ctype, body := multipartBody(t, map[string]string{"title": "scope", "text": "Screen after reset"}, testPNG(t, 40, 30))
	rec := e.post("/share", cookie, ctype, body, "Sec-Fetch-Site", "cross-site")
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site share: %d", rec.Code)
	}
	ctype, body = multipartBody(t, map[string]string{"title": "scope", "text": "Screen after reset"}, testPNG(t, 40, 30))
	rec = e.post("/share", cookie, ctype, body, "Sec-Fetch-Site", "same-origin")
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/drop?share=shr_") {
		t.Fatalf("share: %d %s", rec.Code, loc)
	}
	id := strings.TrimPrefix(loc, "/drop?share=")
	page := e.Do("GET", loc, cookie, nil).Body.String()
	if !strings.Contains(page, `name="staged" value="`+id+`"`) || !strings.Contains(page, "scope\nScreen after reset") {
		t.Errorf("drop page does not carry the share:\n%s", page)
	}
	var sh dto.Share
	rec = e.Do("GET", "/api/shares/"+id, cookie, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &sh); err != nil || len(sh.Files) != 1 {
		t.Fatalf("share api: %d %s", rec.Code, rec.Body)
	}
	// The staged photo is visible to its sharer before any message.
	if rec := e.Do("GET", sh.Files[0].ThumbURL, cookie, nil); rec.Code != http.StatusOK {
		t.Errorf("staged thumb: %d", rec.Code)
	}
	e.Principal("bob", auth.KindHuman, auth.ScopeRead, auth.ScopeMessage)
	bob := e.Session("bob", auth.ScopeRead, auth.ScopeMessage)
	if rec := e.Do("GET", "/api/shares/"+id, bob, nil); rec.Code != http.StatusNotFound {
		t.Errorf("someone else's share: %d", rec.Code)
	}
	if rec := e.Do("GET", sh.Files[0].URL, bob, nil); rec.Code != http.StatusNotFound {
		t.Errorf("someone else's staged photo: %d", rec.Code)
	}
	// Sending with staged= attaches the files.
	rec = e.Do("POST", "/api/messages", cookie, url.Values{"chat": {"chan:dut1"}, "staged": {id}, "text": {"from the share"}}, "X-Cafard", "1", "Sec-Fetch-Site", "same-origin")
	var sent dto.Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &sent); err != nil || rec.Code != http.StatusCreated || len(sent.Attachments) != 1 || sent.Attachments[0].Image.SHA256 != sh.Files[0].SHA256 {
		t.Fatalf("send staged: %d %s", rec.Code, rec.Body)
	}
	// After an hour the share is gone, and the form says so.
	e.Clock.Advance(61 * time.Minute)
	if body := e.Do("GET", loc, cookie, nil).Body.String(); !strings.Contains(body, "The shared files are gone") {
		t.Error("an expired share is not reported")
	}
}

func TestChatAPIListsPagesAndMarksRead(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	cookie := e.Admin()
	so := []string{"Sec-Fetch-Site", "same-origin"}

	var list struct{ Chats []dto.Chat }
	if err := json.Unmarshal(e.Do("GET", "/api/chats", cookie, nil).Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range list.Chats {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, " ") != "chan:dut1 chan:general dm:codex-build" {
		t.Errorf("chats = %v", ids)
	}
	if c := list.Chats[0]; c.Unread != 2 || c.OpenQuestions != 1 || len(c.Streams) != 1 || c.Preview == nil {
		t.Errorf("dut1 = %+v", c)
	}

	var page dto.Page[dto.Entry]
	_ = json.Unmarshal(e.Do("GET", "/api/chats/chan:dut1/timeline", cookie, nil).Body.Bytes(), &page)
	var kinds []string
	for _, it := range page.Items {
		k := string(it.Kind)
		if it.Event {
			k += "(event)"
		}
		kinds = append(kinds, k)
	}
	if strings.Join(kinds, " ") != "marker(event) marker message message" {
		t.Errorf("timeline kinds = %v", kinds)
	}
	_ = json.Unmarshal(e.Do("GET", "/api/chats/chan:dut1/timeline?f=questions", cookie, nil).Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].Question == nil {
		t.Errorf("questions filter = %+v", page.Items)
	}
	if rec := e.Do("GET", "/api/chats/chan%3Adut1/timeline", cookie, nil); rec.Code != http.StatusOK {
		t.Errorf("escaped chat id (as the bundle sends it): %d", rec.Code)
	}
	if rec := e.Do("GET", "/api/chats/chan:dut1/timeline?f=nope", cookie, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad filter: %d", rec.Code)
	}
	if rec := e.Do("GET", "/api/chats/dm:nobody~else/timeline", cookie, nil); rec.Code != http.StatusNotFound {
		t.Errorf("someone else's dm: %d", rec.Code)
	}

	var o dto.Overview
	_ = json.Unmarshal(e.Do("GET", "/api/overview", cookie, nil).Body.Bytes(), &o)
	if len(o.Questions) != 1 || len(o.Direct) != 1 || len(o.Recent) == 0 {
		t.Errorf("overview = %d %d %d", len(o.Questions), len(o.Direct), len(o.Recent))
	}

	var acked dto.Acked
	rec := e.Do("POST", "/api/chats/chan:dut1/read", cookie, fmt.Sprintf(`{"up_to_seq":%d}`, seeded.question), so...)
	if err := json.Unmarshal(rec.Body.Bytes(), &acked); err != nil || acked.Acked != 2 {
		t.Errorf("read: %d %s", rec.Code, rec.Body)
	}
	rec = e.Do("POST", "/api/acks", cookie, fmt.Sprintf(`{"seqs":[%d]}`, seeded.direct), so...)
	if err := json.Unmarshal(rec.Body.Bytes(), &acked); err != nil || acked.Acked != 1 {
		t.Errorf("acks: %d %s", rec.Code, rec.Body)
	}

	var chat dto.Chat
	rec = e.Do("PUT", "/api/chats/chan:general/prefs", cookie, `{"pinned":true,"muted":true}`, so...)
	if err := json.Unmarshal(rec.Body.Bytes(), &chat); err != nil || !chat.Pinned || !chat.Muted {
		t.Errorf("prefs: %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(e.Do("GET", "/api/chats", cookie, nil).Body.Bytes(), &list)
	if list.Chats[0].ID != "chan:general" {
		t.Errorf("a pinned chat is not first: %s", list.Chats[0].ID)
	}
	rec = e.Do("PUT", "/api/chats/chan:general/prefs", cookie, `{"followed":false}`, so...)
	if err := json.Unmarshal(rec.Body.Bytes(), &chat); err != nil || chat.Followed {
		t.Errorf("unfollow: %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(e.Do("GET", "/api/chats", cookie, nil).Body.Bytes(), &list)
	for _, c := range list.Chats {
		if c.ID == "chan:general" {
			t.Error("an unfollowed channel is listed without all=1")
		}
	}
	_ = json.Unmarshal(e.Do("GET", "/api/chats?all=1", cookie, nil).Body.Bytes(), &list)
	if len(list.Chats) != 3 {
		t.Errorf("all=1 lists %d", len(list.Chats))
	}
	if rec := e.Do("PUT", "/api/chats/dm:codex-build/prefs", cookie, `{"followed":false}`, so...); rec.Code != http.StatusBadRequest {
		t.Errorf("unfollow a dm: %d", rec.Code)
	}

	// Channels: create, bind, rename, delete with confirmation.
	rec = e.Do("POST", "/api/channels", cookie, `{"name":"run-42","patterns":["serial:*"]}`, so...)
	if err := json.Unmarshal(rec.Body.Bytes(), &chat); err != nil || rec.Code != http.StatusCreated || chat.ID != "chan:run-42" || !chat.CanManage {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	rec = e.Do("PATCH", "/api/channels/run-42", cookie, `{"rename":"run-43","surface":"errors"}`, so...)
	if err := json.Unmarshal(rec.Body.Bytes(), &chat); err != nil || chat.ID != "chan:run-43" || chat.Surface != dto.SurfaceErrors {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do("DELETE", "/api/channels/run-43", cookie, nil, so...); rec.Code != http.StatusBadRequest {
		t.Errorf("delete without confirm: %d", rec.Code)
	}
	if rec := e.Do("DELETE", "/api/channels/run-43?confirm=run-43", cookie, nil, so...); rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d %s", rec.Code, rec.Body)
	}
	for _, w := range []struct{ method, path string }{
		{"POST", "/api/channels"}, {"PATCH", "/api/channels/dut1"}, {"DELETE", "/api/channels/dut1"},
		{"PUT", "/api/chats/chan:dut1/prefs"}, {"POST", "/api/chats/chan:dut1/read"}, {"POST", "/api/acks"},
		{"POST", "/api/messages"}, {"POST", "/share"},
	} {
		if rec := e.Do(w.method, w.path, cookie, `{}`, "Sec-Fetch-Site", "cross-site"); rec.Code != http.StatusForbidden {
			t.Errorf("cross-site %s %s: %d", w.method, w.path, rec.Code)
		}
	}
}

func TestChatPageHasAnOlderLinkAndFilters(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	cookie := e.Admin()
	ctx := context.Background()
	for i := range 60 {
		e.Clock.Advance(3 * time.Second)
		if _, err := e.svc.SendMessage(ctx, agentCodex, messaging.SendInput{SendRequest: dto.SendRequest{Text: fmt.Sprintf("step %02d", i), Channel: "dut1"}}); err != nil {
			t.Fatal(err)
		}
	}
	shown := func(page, text string) bool { return strings.Contains(page, `data-slot="body">`+text+`</div>`) }
	body := e.Do("GET", "/c/dut1", cookie, nil).Body.String()
	if !shown(body, "step 59") || shown(body, "step 05") {
		t.Error("the page is not the latest 50")
	}
	m := regexp.MustCompile(`href="(/c/dut1\?before_seq=\d+)" data-slot="older"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Older link")
	}
	older := e.Do("GET", m[1], cookie, nil).Body.String()
	if !shown(older, "step 05") || shown(older, "step 59") || !strings.Contains(older, "Newest messages") {
		t.Error("the Older page is wrong")
	}
	q := e.Do("GET", "/c/dut1?f=questions", cookie, nil).Body.String()
	if !shown(q, "Power-cycle dut1?") || shown(q, "step 59") {
		t.Error("the questions filter is wrong")
	}
	img := e.Do("GET", "/c/dut1?f=images", cookie, nil).Body.String()
	if !strings.Contains(img, "image v42 ready") || shown(img, "step 59") {
		t.Error("the images filter is wrong")
	}
	if rec := e.Do("GET", "/c/nope", cookie, nil); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Existing channels") {
		t.Errorf("unknown channel: %d", rec.Code)
	}
	if rec := e.Do("GET", "/dm/admin", cookie, nil); rec.Code != http.StatusNotFound {
		t.Errorf("dm with oneself: %d", rec.Code)
	}
}

func TestChatPagesEscapeHostileMessages(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	cookie := e.Admin()
	if _, err := e.svc.SendMessage(context.Background(), agentClaude, messaging.SendInput{SendRequest: dto.SendRequest{
		Text: "<script>alert(1)</script> [x](javascript:alert(2)) javascript:alert(3) \x1b]0;owned\x07", Channel: "dut1"}}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/c/dut1"} {
		body := e.Do("GET", path, cookie, nil).Body.String()
		for _, bad := range []string{"<script>alert", `href="javascript:`, "\x1b"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: %q reached the page", path, bad)
			}
		}
	}
	e.Commit(store.Entry{Stream: "serial:dut1", Kind: store.KindMarker, Severity: 6, Text: "<img src=x onerror=alert(4)>"})
	if body := e.Do("GET", "/c/dut1", cookie, nil).Body.String(); strings.Contains(body, "<img src=x") {
		t.Error("a hostile marker reached the page")
	}
}

func TestTheServiceWorkerAndShareTargetAreServed(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	rec := e.Do("GET", "/sw.js", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") || !strings.Contains(rec.Body.String(), "/?share=") {
		t.Errorf("/sw.js: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if strings.Contains(rec.Body.String(), "/api/") {
		t.Error("the service worker names an API route; it must never cache one")
	}
	var m struct {
		ShareTarget struct {
			Action, Method, Enctype string
			Params                  struct {
				Files []struct{ Name string }
			}
		} `json:"share_target"`
	}
	if err := json.Unmarshal(e.Do("GET", "/manifest.webmanifest", nil, nil).Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	st := m.ShareTarget
	if st.Action != "/share" || st.Method != "POST" || st.Enctype != "multipart/form-data" || len(st.Params.Files) != 1 || st.Params.Files[0].Name != "files[]" {
		t.Errorf("share target = %+v", st)
	}
}
