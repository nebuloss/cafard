package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/projects"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/web/assets"
	"github.com/nebuloss/cafard/internal/web/webtest"
)

// The chat pages, their JSON API and the share target.

var (
	agentCodex  = auth.Principal{Name: "codex-build", Kind: auth.KindAgent, Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage), TokenID: 1}
	agentClaude = auth.Principal{Name: "claude-laptop", Kind: auth.KindAgent, Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage), TokenID: 2}
	adminP      = auth.Principal{Name: "admin", Kind: auth.KindHuman, Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage, auth.ScopeAdmin), SessionID: 1}
)

// seeded is what seedChats made, for the checks.
var seeded struct {
	sha      string
	question int64
	message  int64
	direct   int64
}

// seedChats: #lab/dut1 bound to serial:lab/dut1, two agents members of
// lab, an agent message with a photo
// and a log slice, an open question for the humans, a direct message.
func seedChats(e *env) {
	t := e.T
	ctx := context.Background()
	if _, err := e.Tokens.EnsurePrincipal(ctx, auth.AdminName, auth.KindHuman); err != nil {
		t.Fatal(err)
	}
	for _, p := range []auth.Principal{agentCodex, agentClaude} {
		e.Stack.Member(t, p.Name, p.Kind, p.Scopes...)
	}
	if _, err := e.svc.CreateChannel(ctx, adminP, dto.ChannelCreate{Name: "dut1", Title: "DUT 1 (x86 board)", Patterns: []string{"serial:lab/dut1"}}); err != nil {
		t.Fatal(err)
	}
	b, err := e.svc.Blobs().Put(ctx, bytes.NewReader(webtest.PNG(t, 64, 48)), "")
	if err != nil {
		t.Fatal(err)
	}
	to := int64(2)
	m, err := e.svc.SendMessage(ctx, agentCodex, messaging.SendInput{SendRequest: dto.SendRequest{
		Text: "image v42 ready, `dwc3` fails, see https://git.example.com/k/4a1f", Channel: "dut1",
		Logs: []dto.LogRequest{{Stream: "serial:lab/dut1", FromSeq: 1, ToSeq: &to, Title: "boot"}},
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
func noBundleChats() map[string][]string {
	q := fmt.Sprint(seeded.question)
	return map[string][]string{
		"/": {
			"Needs you", "Power-cycle dut1?", `action="/api/messages"`, `name="choice" value="done"`, `name="reply_to" value="` + q + `"`,
			`name="next" value="/"`, `href="/drop"`, "Build 812 is green.", "You are in 1 project and 1 direct conversation.",
			// The left bar: Home current, the direct messages and every project folded but none open.
			`<a class="side-item side-home" href="/" aria-current="page">`, `<details class="tree" data-node="dm">`,
			`<details class="tree tree-project" data-node="p:lab" data-project="lab">`, `href="/projects/lab/c/dut1" data-chat="chan:lab/dut1"`,
			// The admin makes projects from home.
			`action="/projects"`, `href="/admin"`,
		},
		"/dm": {`href="/dm/codex-build" data-chat="dm:codex-build"`, `<details class="tree" data-node="dm" open data-current>`,
			`<a class="side-item" href="/dm" aria-current="page">`},
		"/projects/lab/c/dut1": {
			"image v42 ready", "<code>dwc3</code>", `<a href="https://git.example.com/k/4a1f" rel="noopener noreferrer" target="_blank">`,
			`<form class="composer"`, `enctype="multipart/form-data"`, `name="files[]"`, `capture="environment"`, `name="chat" value="chan:lab/dut1"`,
			`class="event-row"`, "codex-build: flashed linux-6.18-rc3", `href="/projects/lab/c/dut1?view=logs"`, `href="/projects/lab/c/dut1?f=questions"`,
			`href="/blob/` + seeded.sha + `"`, `href="/live?from_seq=1&amp;streams=serial%3Alab%2Fdut1&amp;to_seq=2"`, "Power-cycle dut1?",
			`href="/projects/lab/logs/dut1"`, "1 question waiting for you",
			// The left bar: the project unfolded on its chats, sinks and
			// settings, as links; the drawer and the members sheet are
			// :target links.
			`<aside class="side" id="side" data-component="SideNav"`, `href="#side"`,
			`<details class="tree tree-project" data-node="p:lab" data-project="lab" open data-current>`,
			`<a class="chat-row" href="/projects/lab/c/dut1" data-chat="chan:lab/dut1" aria-current="page">`,
			`href="/projects/lab/c/general"`, `href="/projects/lab/logs"`, `href="/projects/lab/settings"`, `href="#main"`,
			// The members panel: who reads #dut1, by kind, each a link to a direct conversation.
			`<aside class="members" id="members" data-component="MembersPanel" data-channel="lab/dut1"`, `href="#members"`,
			`<section class="member-group" data-group="human"`, `<section class="member-group" data-group="agent"`,
			`<a class="member" href="/dm/codex-build" data-kind="agent"`,
		},
		"/projects/lab/c/dut1?view=logs": {`data-view="logs"`, "U-Boot 2024.04", `class="marker"`, `action="/projects/lab/c/dut1"`},
		"/dm/codex-build":                {"Build 812 is green.", `name="chat" value="dm:codex-build"`, `data-section="direct"`, `aria-current="page"`},
		"/drop":                          {`<select name="chat"`, `<option value="chan:lab/dut1">#dut1</option>`, `action="/api/messages"`, `name="back" value="/drop"`},
	}
}

func TestANoScriptRenderAcksWhatItShows(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	cookie := e.Admin()
	unread := func() int {
		ch, err := e.svc.Channel(context.Background(), adminP, "dut1")
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
	body := e.Do("GET", "/projects/lab/c/dut1", cookie, nil).Body.String()
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
	form := url.Values{"chat": {"chan:lab/dut1"}, "reply_to": {fmt.Sprint(seeded.question)}, "choice": {"done"}, "next": {"/"}, "back": {"/"}}
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
	form = url.Values{"chat": {"chan:lab/dut1"}, "reply_to": {fmt.Sprint(q2.Question.Seq)}, "choice": {"maybe"}, "next": {"/"}, "back": {"/"}}
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

func TestTheShareTargetStagesFilesForTheDropForm(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	cookie := e.Admin()
	shared := webtest.Multipart{Fields: map[string]string{"title": "scope", "text": "Screen after reset"}, Files: [][]byte{webtest.PNG(t, 40, 30)}}
	rec := e.Do("POST", "/share", cookie, shared, "Sec-Fetch-Site", "cross-site")
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site share: %d", rec.Code)
	}
	rec = e.Do("POST", "/share", cookie, shared, "Sec-Fetch-Site", "same-origin")
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
	rec = e.Do("POST", "/api/messages", cookie, url.Values{"chat": {"chan:lab/dut1"}, "staged": {id}, "text": {"from the share"}}, "X-Cafard", "1", "Sec-Fetch-Site", "same-origin")
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
	if strings.Join(ids, " ") != "chan:lab/dut1 chan:lab/general dm:codex-build" {
		t.Errorf("chats = %v", ids)
	}
	if c := list.Chats[0]; c.Unread != 2 || c.OpenQuestions != 1 || len(c.Streams) != 1 || c.Preview == nil {
		t.Errorf("dut1 = %+v", c)
	}

	var page dto.Page[dto.Entry]
	_ = json.Unmarshal(e.Do("GET", "/api/chats/chan:lab%2Fdut1/timeline", cookie, nil).Body.Bytes(), &page)
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
	_ = json.Unmarshal(e.Do("GET", "/api/chats/chan:lab%2Fdut1/timeline?f=questions", cookie, nil).Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].Question == nil {
		t.Errorf("questions filter = %+v", page.Items)
	}
	if rec := e.Do("GET", "/api/chats/chan%3Alab%2Fdut1/timeline", cookie, nil); rec.Code != http.StatusOK {
		t.Errorf("escaped chat id (as the bundle sends it): %d", rec.Code)
	}
	if rec := e.Do("GET", "/api/chats/chan:lab%2Fdut1/timeline?f=nope", cookie, nil); rec.Code != http.StatusBadRequest {
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
	rec := e.Do("POST", "/api/chats/chan:lab%2Fdut1/read", cookie, fmt.Sprintf(`{"up_to_seq":%d}`, seeded.question), so...)
	if err := json.Unmarshal(rec.Body.Bytes(), &acked); err != nil || acked.Acked != 2 {
		t.Errorf("read: %d %s", rec.Code, rec.Body)
	}
	rec = e.Do("POST", "/api/acks", cookie, fmt.Sprintf(`{"seqs":[%d]}`, seeded.direct), so...)
	if err := json.Unmarshal(rec.Body.Bytes(), &acked); err != nil || acked.Acked != 1 {
		t.Errorf("acks: %d %s", rec.Code, rec.Body)
	}

	var chat dto.Chat
	rec = e.Do("PUT", "/api/chats/chan:lab%2Fgeneral/prefs", cookie, `{"pinned":true,"muted":true}`, so...)
	if err := json.Unmarshal(rec.Body.Bytes(), &chat); err != nil || !chat.Pinned || !chat.Muted {
		t.Errorf("prefs: %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(e.Do("GET", "/api/chats", cookie, nil).Body.Bytes(), &list)
	if list.Chats[0].ID != "chan:lab/general" {
		t.Errorf("a pinned chat is not first: %s", list.Chats[0].ID)
	}
	rec = e.Do("PUT", "/api/chats/chan:lab%2Fgeneral/prefs", cookie, `{"followed":false}`, so...)
	if err := json.Unmarshal(rec.Body.Bytes(), &chat); err != nil || chat.Followed {
		t.Errorf("unfollow: %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(e.Do("GET", "/api/chats", cookie, nil).Body.Bytes(), &list)
	for _, c := range list.Chats {
		if c.ID == "chan:lab/general" {
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
	rec = e.Do("POST", "/api/channels", cookie, `{"name":"run-42","patterns":["serial:lab/*"]}`, so...)
	if err := json.Unmarshal(rec.Body.Bytes(), &chat); err != nil || rec.Code != http.StatusCreated || chat.ID != "chan:lab/run-42" || !chat.CanManage {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	rec = e.Do("PATCH", "/api/channels/run-42", cookie, `{"rename":"run-43","surface":"errors"}`, so...)
	if err := json.Unmarshal(rec.Body.Bytes(), &chat); err != nil || chat.ID != "chan:lab/run-43" || chat.Surface != dto.SurfaceErrors {
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
		{"PUT", "/api/chats/chan:lab%2Fdut1/prefs"}, {"POST", "/api/chats/chan:lab%2Fdut1/read"}, {"POST", "/api/acks"},
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
	body := e.Do("GET", "/projects/lab/c/dut1", cookie, nil).Body.String()
	if !shown(body, "step 59") || shown(body, "step 05") {
		t.Error("the page is not the latest 50")
	}
	m := regexp.MustCompile(`href="(/projects/lab/c/dut1\?before_seq=\d+)" data-slot="older"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Older link")
	}
	older := e.Do("GET", m[1], cookie, nil).Body.String()
	if !shown(older, "step 05") || shown(older, "step 59") || !strings.Contains(older, "Newest messages") {
		t.Error("the Older page is wrong")
	}
	q := e.Do("GET", "/projects/lab/c/dut1?f=questions", cookie, nil).Body.String()
	if !shown(q, "Power-cycle dut1?") || shown(q, "step 59") {
		t.Error("the questions filter is wrong")
	}
	img := e.Do("GET", "/projects/lab/c/dut1?f=images", cookie, nil).Body.String()
	if !strings.Contains(img, "image v42 ready") || shown(img, "step 59") {
		t.Error("the images filter is wrong")
	}
	if rec := e.Do("GET", "/projects/lab/c/nope", cookie, nil); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Channels you can read") {
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
	for _, path := range []string{"/", "/projects/lab/c/dut1"} {
		body := e.Do("GET", path, cookie, nil).Body.String()
		for _, bad := range []string{"<script>alert", `href="javascript:`, "\x1b"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: %q reached the page", path, bad)
			}
		}
	}
	e.Commit(store.Entry{Stream: "serial:lab/dut1", Kind: store.KindMarker, Severity: 6, Text: "<img src=x onerror=alert(4)>"})
	if body := e.Do("GET", "/projects/lab/c/dut1", cookie, nil).Body.String(); strings.Contains(body, "<img src=x") {
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

// The members panel and its API list who reads the channel, and no one
// from a project the reader has no grant in.
func TestChannelMembersShowOnlySharedProjects(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	ctx := context.Background()
	if _, err := e.Stack.Projects.Create(ctx, "rack", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Tokens.EnsurePrincipal(ctx, "rack-agent", auth.KindAgent); err != nil {
		t.Fatal(err)
	}
	if err := e.Stack.Projects.SetGrant(ctx, projects.Grant{Principal: "rack-agent", Project: "rack", Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage)}); err != nil {
		t.Fatal(err)
	}
	reader := e.Principal("reader", auth.KindHuman, auth.ScopeRead)
	rec := e.Do("GET", "/api/channels/lab%2Fdut1/members", nil, nil, "Authorization", "Bearer "+reader)
	if rec.Code != http.StatusOK {
		t.Fatalf("members: %d %s", rec.Code, rec.Body)
	}
	var list dto.MemberList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range list.Members {
		names = append(names, m.Name)
	}
	// reader is online (its request just now), so it comes first.
	if want := []string{"reader", "admin", agentClaude.Name, agentCodex.Name}; !slices.Equal(names, want) {
		t.Errorf("members = %v, want %v", names, want)
	}
	page := e.Do("GET", "/projects/lab/c/dut1", e.Admin(), nil).Body.String()
	_, panel, _ := strings.Cut(page, `data-component="MembersPanel"`)
	panel, _, _ = strings.Cut(panel, "</aside>")
	if !strings.Contains(panel, `href="/dm/codex-build"`) || strings.Contains(panel, "rack-agent") {
		t.Error("the panel lacks codex-build, or names rack-agent")
	}
	// rack's agent cannot read #dut1: unknown, and nobody named.
	rack := e.Token("rack-agent", auth.ScopeRead)
	rec = e.Do("GET", "/api/channels/lab%2Fdut1/members", nil, nil, "Authorization", "Bearer "+rack)
	if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "codex-build") {
		t.Errorf("an outsider got the members: %d %s", rec.Code, rec.Body)
	}
}
