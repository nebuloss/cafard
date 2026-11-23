package api

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"cafard/internal/store"
)

// person is a browser signed in to the chat app as guillaume.
type person struct {
	t      *testing.T
	r      *rig
	client *http.Client
}

func (r *rig) person(t *testing.T) *person {
	t.Helper()
	code, _, err := r.acc.IssueLink(ctx, r.admin, r.admin.Identity, 0)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.PostForm(r.srv.URL+"/join/"+code, url.Values{"password": {"guillaume's password"}, "again": {"guillaume's password"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	jar, _ := cookiejar.New(nil)
	p := &person{t: t, r: r, client: &http.Client{Jar: jar}}
	if code, body := p.call("POST", "/api/v1/signin", map[string]string{"username": "guillaume", "password": "guillaume's password"}); code != 200 {
		t.Fatalf("sign in: %d %s", code, body)
	}
	// guillaume in be98, as a person in the project.
	be98, _ := r.st.ProjectByName(ctx, "be98")
	r.acc.AddMember(ctx, r.admin, be98, r.admin.Identity)
	return p
}

// call sends JSON as the chat app's page would.
func (p *person) call(method, path string, body any) (int, []byte) {
	p.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, p.r.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := p.client.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestPeopleSignInWithACookie(t *testing.T) {
	r := newRig(t)
	p := r.person(t)
	code, body := p.call("GET", "/api/v1/me", nil)
	if code != 200 || !strings.Contains(string(body), `"guillaume"`) || !strings.Contains(string(body), `"admin": true`) {
		t.Errorf("me: %d %s", code, body)
	}
	// A cookie write from another site is refused; the same write from the page works.
	req, _ := http.NewRequest("POST", r.srv.URL+"/api/v1/send/be98/x", strings.NewReader("hello"))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, _ := p.client.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("a cross-site write with the cookie: %d", resp.StatusCode)
	}
	if code, body := p.call("POST", "/api/v1/send/be98/x", "hello"); code != 201 {
		t.Errorf("a same-site write: %d %s", code, body)
	}
	if code, _ := p.call("POST", "/api/v1/signout", nil); code != 204 {
		t.Errorf("sign out: %d", code)
	}
	if code, _ := p.call("GET", "/api/v1/me", nil); code != 401 {
		t.Errorf("after signing out: %d", code)
	}
}

func TestStartAConversationWithPhotos(t *testing.T) {
	r := newRig(t)
	p := r.person(t)
	code, body := p.call("POST", "/api/v1/projects/be98/conversations", map[string]string{"text": "LED blinks orange after the second reboot."})
	if code != 201 {
		t.Fatalf("start: %d %s", code, body)
	}
	var sent struct {
		Address string
		Message messageJSON
	}
	json.Unmarshal(body, &sent)
	if sent.Address != "be98/led-blinks-orange-after-the-second-reboot" {
		t.Errorf("address: %s", sent.Address)
	}
	for _, name := range []string{"IMG_1.jpg", "IMG_2.jpg"} {
		req, _ := http.NewRequest("PUT", r.srv.URL+"/api/v1/send/"+sent.Address+"/"+name+"?message="+itoa(sent.Message.ID), strings.NewReader("\xff\xd8\xff"))
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		resp, err := p.client.Do(req)
		if err != nil || resp.StatusCode != 201 {
			t.Fatalf("attach %s: %v %d", name, err, resp.StatusCode)
		}
		resp.Body.Close()
	}
	// dev-code reads it: one message, two photos; guillaume then sees it read.
	_, mbody := r.do(t, "GET", "/api/v1/projects/be98/conversations/led-blinks-orange-after-the-second-reboot/messages", r.agent, nil)
	var ms struct{ Messages []messageJSON }
	json.Unmarshal(mbody, &ms)
	if len(ms.Messages) != 1 || len(ms.Messages[0].Files) != 2 {
		t.Fatalf("messages: %s", mbody)
	}
	_, mine := p.call("GET", "/api/v1/projects/be98/conversations/led-blinks-orange-after-the-second-reboot/messages", nil)
	if !strings.Contains(string(mine), `"read_by"`) || !strings.Contains(string(mine), `"dev-code"`) {
		t.Errorf("read_by: %s", mine)
	}
	_, list := p.call("GET", "/api/v1/projects/be98/conversations", nil)
	if !strings.Contains(string(list), `"id"`) {
		t.Errorf("conversations: %s", list)
	}

	title := "Wi-Fi drops after second reboot"
	if code, body := p.call("PATCH", "/api/v1/projects/be98/conversations/led-blinks-orange-after-the-second-reboot", map[string]any{"title": title}); code != 200 || !strings.Contains(string(body), title) {
		t.Errorf("rename: %d %s", code, body)
	}
}

func TestMembersFromTheChat(t *testing.T) {
	r := newRig(t)
	p := r.person(t)
	if code, body := p.call("GET", "/api/v1/directory", nil); code != 200 || !strings.Contains(string(body), "lab-bench") || strings.Contains(string(body), "dev-build") {
		t.Errorf("directory: %d %s", code, body)
	}
	if code, body := p.call("POST", "/api/v1/projects/be98/members", map[string]string{"name": "lab-bench"}); code != 200 || !strings.Contains(string(body), "lab-bench") {
		t.Errorf("add: %d %s", code, body)
	}
	if code, _ := p.call("DELETE", "/api/v1/projects/be98/members/lab-bench", nil); code != 204 {
		t.Errorf("remove: %d", code)
	}
	// An agent cannot.
	resp, _ := r.do(t, "POST", "/api/v1/projects/be98/members", r.agent, strings.NewReader(`{"name":"lab-bench"}`))
	if resp.StatusCode != 403 {
		t.Errorf("an agent adding a member: %d", resp.StatusCode)
	}
}

func TestDeleteAFile(t *testing.T) {
	r := newRig(t)
	p := r.person(t)
	_, body := r.do(t, "PUT", "/api/v1/send/be98/x/secret.log", r.device, strings.NewReader("password=hunter2\n"))
	var sent struct{ Message messageJSON }
	json.Unmarshal(body, &sent)
	id := itoa(sent.Message.Files[0].ID)
	if resp, _ := r.do(t, "DELETE", "/api/v1/files/"+id, r.agent, nil); resp.StatusCode != 403 {
		t.Errorf("an agent that did not send it: %d", resp.StatusCode)
	}
	if code, _ := p.call("DELETE", "/api/v1/files/"+id, nil); code != 204 {
		t.Errorf("an admin: %d", code)
	}
	if resp, _ := r.do(t, "GET", "/api/v1/files/"+id, r.agent, nil); resp.StatusCode != 410 {
		t.Errorf("the deleted file: %d", resp.StatusCode)
	}
}

// Sign in another phone: the password again, a code, and the phone that
// opens it is signed in and sent to the app.
func TestSignInAnotherPhone(t *testing.T) {
	r := newRig(t)
	p := r.person(t)
	if code, _ := p.call("POST", "/api/v1/me/phone", map[string]string{"password": "wrong"}); code != 403 {
		t.Errorf("a wrong password: %d", code)
	}
	code, body := p.call("POST", "/api/v1/me/phone", map[string]string{"password": "guillaume's password"})
	var out struct{ URL, QR string }
	json.Unmarshal(body, &out)
	if code != 201 || !strings.HasPrefix(out.URL, "http://cafard.example/join/") || !strings.HasPrefix(out.QR, "data:image/png;base64,") {
		t.Fatalf("code: %d %s", code, body)
	}
	jar, _ := cookiejar.New(nil)
	phone := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("GET", r.srv.URL+strings.TrimPrefix(out.URL, "http://cafard.example"), nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 15; Pixel 8) Chrome/140.0 Mobile Safari/537.36")
	resp, err := phone.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/" {
		t.Fatalf("opening the code: %d %v", resp.StatusCode, resp.Header)
	}
	me, _ := http.NewRequest("GET", r.srv.URL+"/api/v1/me", nil)
	resp, _ = phone.Do(me)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "guillaume") {
		t.Errorf("the phone, signed in: %d %s", resp.StatusCode, b)
	}
	_, list := p.call("GET", "/api/v1/me/signins", nil)
	if !strings.Contains(string(list), "Chrome on Android") || !strings.Contains(string(list), `"current": true`) {
		t.Errorf("sign-ins: %s", list)
	}
}

// The stream says what changed, for the projects the member can read only.
func TestEventsSayWhatChanged(t *testing.T) {
	r := newRig(t)
	p := r.person(t)
	req, _ := http.NewRequest("GET", r.srv.URL+"/api/v1/events", nil)
	resp, err := p.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	waitFor := func(want string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("the stream ended waiting for %q", want)
				}
				if strings.Contains(l, want) {
					return
				}
			case <-deadline:
				t.Fatalf("no %q on the stream", want)
			}
		}
	}
	waitFor("event: ready")

	// A project guillaume is not in: nothing about it reaches the stream.
	other, _ := r.acc.CreateProject(ctx, r.admin, "zeeos", 30, 5e9)
	dev, _ := r.st.IdentityByName(ctx, "lab-bench")
	r.acc.AddMember(ctx, r.admin, other, dev)
	r.st.Announce(r.hub)
	r.do(t, "POST", "/api/v1/send/be98/live-test", r.agent, strings.NewReader("hello"))
	waitFor(`"topic":"conversation"`)

	// guillaume joins zeeos: the stream says "me" and follows zeeos from then on.
	r.acc.AddMember(ctx, r.admin, other, r.admin.Identity)
	waitFor(`"topic":"me"`)
	_ = store.Agent
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
