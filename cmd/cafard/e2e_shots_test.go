package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
)

// sendCLI runs cafard send as an agent would, with the token in the
// environment.
func (s *server) sendCLI(token, stdin string, args ...string) (string, error) {
	s.t.Helper()
	var out, errOut syncBuffer
	err := run(context.Background(), append([]string{"send", "--server", s.base}, args...), strings.NewReader(stdin), &out, &errOut, func(k string) string {
		if k == "CAFARD_TOKEN" {
			return token
		}
		return ""
	})
	if err != nil {
		return out.String() + errOut.String(), err
	}
	return out.String(), nil
}

func tempFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (s *server) message(token string, seq int64) dto.Entry {
	s.t.Helper()
	resp, body := s.get(token, fmt.Sprintf("/api/messages/%d", seq))
	var e dto.Entry
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &e) != nil {
		s.t.Fatalf("GET message %d = %d %s", seq, resp.StatusCode, body)
	}
	return e
}

func TestCLISendPostsAScreenshotThePersonSees(t *testing.T) {
	s := startServer(t)
	tok, _ := s.agent("claude-laptop")
	png := testPNG(t, 640, 400)
	shot := tempFile(t, "shot.png", png)

	out, err := s.sendCLI(tok, "", "--image", shot, "login page after fix")
	if err != nil {
		t.Fatalf("cafard send: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var seq int64
	if len(lines) == 2 {
		_, _ = fmt.Sscanf(lines[0], "seq %d in #general", &seq)
	}
	if seq == 0 {
		t.Fatalf("cafard send printed %q", out)
	}
	link, err := url.Parse(lines[1])
	if err != nil || !strings.HasPrefix(lines[1], s.base+"/") || link.Fragment != fmt.Sprintf("m%d", seq) {
		t.Fatalf("link = %q", lines[1])
	}

	e := s.message(tok, seq)
	if e.Text != "login page after fix" || e.Sender == nil || e.Sender.Name != "claude-laptop" || len(e.Attachments) != 1 || e.Attachments[0].Image == nil {
		t.Fatalf("message = %+v", e)
	}
	img := e.Attachments[0].Image
	if img.Mime != "image/png" || img.Width != 640 || img.Height != 400 {
		t.Errorf("image = %+v", img)
	}
	resp, body := s.get(tok, img.URL)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, png) {
		t.Errorf("blob = %d, %d bytes (want the file, %d)", resp.StatusCode, len(body), len(png))
	}

	// The person opens the link: the page shows the thumbnail, which
	// links to the original the viewer opens.
	cookie := s.session()
	resp, page := s.browser(cookie, "GET", link.Path, "", nil, false)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(page), fmt.Sprintf(`id="m%d"`, seq)) ||
		!strings.Contains(string(page), `<a class="image" href="`+img.URL+`"`) || !strings.Contains(string(page), img.ThumbURL) {
		t.Errorf("chat page %s = %d, no thumbnail of %s", link.Path, resp.StatusCode, img.URL)
	}

	// Two images and no text: the chat list says so, and the message
	// counts as unread for the person.
	two := tempFile(t, "second.png", testPNG(t, 300, 200))
	out, err = s.sendCLI(tok, "", "--image", shot, "--image", two)
	if err != nil {
		t.Fatalf("image-only send: %v\n%s", err, out)
	}
	resp, body = s.browser(cookie, "GET", "/api/chats", "", nil, true)
	var list struct{ Chats []dto.Chat }
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &list) != nil {
		t.Fatalf("chats = %d %s", resp.StatusCode, body)
	}
	found := false
	for _, c := range list.Chats {
		if c.Name == "general" {
			found = true
			if c.Preview == nil || c.Preview.Text != "[2 images]" || c.Preview.Sender != "claude-laptop" || c.Unread < 1 {
				t.Errorf("general = %+v, preview %+v", c, c.Preview)
			}
		}
	}
	if !found {
		t.Errorf("no general in %s", body)
	}

	// Text from stdin, flags after the text, a direct message.
	s.token("guillaume", auth.KindHuman, auth.ScopeRead)
	out, err = s.sendCLI(tok, "make test failed\nsee the log\n", "--to", "guillaume", "-", "--image", shot)
	if err != nil || !strings.Contains(out, "in @guillaume") || !strings.Contains(out, "/dm/guillaume#m") {
		t.Fatalf("stdin send = %v %q", err, out)
	}
	_, _ = fmt.Sscanf(out, "seq %d", &seq)
	if e := s.message(tok, seq); e.Text != "make test failed\nsee the log" || e.Recipient != "guillaume" || len(e.Attachments) != 1 {
		t.Errorf("stdin message = %+v", e)
	}
}

func TestCLISendRefusesBadFilesAndReportsTheServer(t *testing.T) {
	s := startServer(t)
	tok, _ := s.agent("claude-laptop")
	notes := tempFile(t, "notes.txt", []byte("just text\n"))
	for _, c := range []struct {
		args []string
		says string
	}{
		{[]string{"--image", filepath.Join(t.TempDir(), "nope.png"), "x"}, "no such file"},
		{[]string{"--image", notes, "x"}, "not a png"},
		{[]string{}, "needs a text or an --image"},
		{[]string{"one", "two"}, "one text argument"},
		{[]string{"--channel", "nope", "hello"}, "unknown_channel"},
	} {
		out, err := s.sendCLI(tok, "", c.args...)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("send %v = %v %q, want %q", c.args, err, out, c.says)
		}
	}
	if s.count("chan:general") != 0 {
		t.Error("a refused send left a message")
	}
	if _, err := s.sendCLI("cfd_1_nope", "", "hello"); err == nil || !strings.Contains(err.Error(), "unauthenticated") {
		t.Errorf("bad token = %v", err)
	}
}

func TestImagePathsWorkThroughTheProxyOnly(t *testing.T) {
	s := startServer(t)
	tok, direct := s.agent("stdio-agent")
	shot := tempFile(t, "shot.png", testPNG(t, 500, 300))
	cs, _ := s.proxied(tok, nil)
	if got := cs.InitializeResult().Instructions; !strings.Contains(got, "image_paths") {
		t.Errorf("instructions through the proxy = %q", got)
	}
	_, out := call(t, cs, "send", map[string]any{"text": "the login page", "image_paths": []string{shot}})
	e := s.message(tok, num(out["seq"]))
	if len(e.Attachments) != 1 || e.Attachments[0].Image == nil || e.Attachments[0].Image.Width != 500 {
		t.Fatalf("message through the proxy = %+v", e)
	}
	callFails(t, cs, "send", map[string]any{"text": "x", "image_paths": []string{shot + ".missing"}}, "not_found")

	// The remote cannot read the agent's disk: it refuses paths.
	callFails(t, direct, "send", map[string]any{"text": "x", "image_paths": []string{shot}}, "bad_request")
	for tool, err := range direct.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := json.Marshal(tool.InputSchema); strings.Contains(string(b), "image_paths") {
			t.Errorf("the remote advertises image_paths on %s", tool.Name)
		}
	}
}
