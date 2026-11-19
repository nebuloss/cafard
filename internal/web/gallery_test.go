package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/web/webtest"
)

// galleryShots is what seedGallery made: #lab/shots and its items, oldest
// first.
var galleryShots struct {
	items []int64
}

// seedGallery makes the gallery #lab/shots: a boot splash without a
// section, then two screenshots of the section "login flow".
func seedGallery(e *env) {
	t := e.T
	ctx := context.Background()
	if _, err := e.svc.CreateChannel(ctx, adminP, dto.ChannelCreate{Name: "shots", Title: "Screenshots", Kind: dto.ChannelGallery}); err != nil {
		t.Fatal(err)
	}
	galleryShots.items = nil
	for i, it := range []struct{ title, section string }{
		{"boot splash", ""}, {"login page", "login flow"}, {"after submit\nthe second line is not in the title", "login flow"},
	} {
		b, err := e.svc.Blobs().Put(ctx, bytes.NewReader(webtest.PNG(t, 40+8*i, 30+6*i)), "")
		if err != nil {
			t.Fatal(err)
		}
		m, err := e.svc.SendMessage(ctx, agentClaude, messaging.SendInput{SendRequest: dto.SendRequest{
			Text: it.title, Channel: "shots", Section: it.section}, Blobs: []dto.Blob{b}})
		if err != nil {
			t.Fatal(err)
		}
		galleryShots.items = append(galleryShots.items, m.Seq)
	}
}

func TestGalleryPageGroupsItemsBySectionWithoutTheBundle(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	seedGallery(e)
	cookie := e.Admin()
	rec := e.Do("GET", "/g/lab/shots", cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d\n%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	items := galleryShots.items
	for _, want := range []string{
		`data-component="GalleryView"`, `data-chat="chan:lab/shots"`, `data-live`,
		`<h3 class="list-heading">login flow</h3>`, `<h3 class="list-heading">Unsorted</h3>`,
		fmt.Sprintf(`href="/g/lab/shots/%d"`, items[0]), `alt="login page"`, "after submit ...",
		`href="/g/lab/shots?section=login&#43;flow"`, `href="/g/lab/shots?section="`, `href="/c/lab/shots"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("gallery page lacks %q", want)
		}
	}
	if strings.Contains(body, "the second line") {
		t.Error("a title is the first line of the text only")
	}
	// Newest section first, newest item first inside it.
	login, unsorted := strings.Index(body, ">login flow</h3>"), strings.Index(body, ">Unsorted</h3>")
	if login < 0 || unsorted < login {
		t.Errorf("sections out of order: login flow at %d, Unsorted at %d", login, unsorted)
	}
	if a, b := strings.Index(body, fmt.Sprintf(`data-seq="%d"`, items[2])), strings.Index(body, fmt.Sprintf(`data-seq="%d"`, items[1])); a < 0 || b < a {
		t.Errorf("items of login flow out of order: %d at %d, %d at %d", items[2], a, items[1], b)
	}

	// The filter shows one section and marks its chip.
	body = e.Do("GET", "/g/lab/shots?section=login+flow", cookie, nil).Body.String()
	if strings.Contains(body, ">Unsorted</h3>") || !strings.Contains(body, "data-filtered") || !strings.Contains(body, `aria-current="true">login flow`) {
		t.Errorf("filtered page:\n%s", body)
	}
	body = e.Do("GET", "/g/lab/shots?section=", cookie, nil).Body.String()
	if strings.Contains(body, ">login flow</h3>") || !strings.Contains(body, ">Unsorted</h3>") {
		t.Error("section= shows the unsorted items only")
	}

	// Paging by seq: before_seq shows the older ones and does not follow the feed.
	body = e.Do("GET", fmt.Sprintf("/g/lab/shots?before_seq=%d", items[1]), cookie, nil).Body.String()
	if strings.Contains(body, fmt.Sprintf(`data-seq="%d"`, items[1])) || !strings.Contains(body, fmt.Sprintf(`data-seq="%d"`, items[0])) ||
		strings.Contains(body, "data-live") || !strings.Contains(body, "Newest images") {
		t.Errorf("older page:\n%s", body)
	}

	// The item's own page: the image, the title, and its neighbours in the section.
	body = e.Do("GET", fmt.Sprintf("/g/lab/shots/%d", items[1]), cookie, nil).Body.String()
	for _, want := range []string{"login page", "?v=agent", fmt.Sprintf(`href="/g/lab/shots/%d" rel="prev"`, items[2]), `href="/g/lab/shots?section=login&#43;flow"`} {
		if !strings.Contains(body, want) {
			t.Errorf("item page lacks %q", want)
		}
	}
	if strings.Contains(body, `rel="next"`) {
		t.Error("the oldest item of a section has no older one; the unsorted item is in another section")
	}
	if rec := e.Do("GET", "/g/lab/shots/999999", cookie, nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing item: status %d", rec.Code)
	}
	// A chat channel is no gallery.
	if rec := e.Do("GET", "/g/lab/dut1", cookie, nil); rec.Code != http.StatusNotFound {
		t.Errorf("/g/lab/dut1: status %d", rec.Code)
	}

	// The chat list leads to the grid, the conversation header back to it.
	if body := e.Do("GET", "/", cookie, nil).Body.String(); !strings.Contains(body, `href="/g/lab/shots" data-chat="chan:lab/shots"`) {
		t.Error("the chat list row of a gallery does not lead to its grid")
	}
	if body := e.Do("GET", "/c/lab/shots", cookie, nil).Body.String(); !strings.Contains(body, `href="/g/lab/shots"`) {
		t.Error("the conversation of a gallery does not link to its grid")
	}
}

func TestGalleryRulesOnSend(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	seedGallery(e)
	ctx := context.Background()
	_, err := e.svc.SendMessage(ctx, agentCodex, messaging.SendInput{SendRequest: dto.SendRequest{Text: "only words", Channel: "shots"}})
	if err == nil || !strings.Contains(err.Error(), "is a gallery") {
		t.Errorf("text to a gallery: %v", err)
	}
	_, err = e.svc.SendMessage(ctx, agentCodex, messaging.SendInput{SendRequest: dto.SendRequest{Text: "hi", Channel: "dut1", Section: "x"}})
	if err == nil || !strings.Contains(err.Error(), "section") {
		t.Errorf("section on a chat channel: %v", err)
	}
	b, err := e.svc.Blobs().Put(ctx, bytes.NewReader(webtest.PNG(t, 90, 60)), "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.SendMessage(ctx, agentCodex, messaging.SendInput{SendRequest: dto.SendRequest{Channel: "shots", Section: strings.Repeat("é", 81)}, Blobs: []dto.Blob{b}})
	if err == nil || !strings.Contains(err.Error(), "80") {
		t.Errorf("an 81-character section: %v", err)
	}
	// Trimmed, 80 characters, and no title: still an item.
	m, err := e.svc.SendMessage(ctx, agentCodex, messaging.SendInput{SendRequest: dto.SendRequest{Channel: "shots", Section: "  " + strings.Repeat("é", 80) + " "}, Blobs: []dto.Blob{b}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.EntryDTO(ctx, adminP, m)
	if err != nil {
		t.Fatal(err)
	}
	if d.Section != strings.Repeat("é", 80) {
		t.Errorf("section = %q", d.Section)
	}

	// The JSON page carries the sections, newest first.
	cookie := e.Admin()
	getPage := func(target string) (page dto.GalleryPage) {
		t.Helper()
		rec := e.Do("GET", target, cookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d\n%s", target, rec.Code, rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := getPage("/api/galleries/lab%2Fshots")
	if len(page.Items) != 4 || len(page.Sections) != 3 || page.Sections[0].Name != strings.Repeat("é", 80) || page.Sections[1].Name != "login flow" {
		t.Errorf("page = %+v", page)
	}
	page = getPage("/api/galleries/lab%2Fshots?limit=1&section=" + url.QueryEscape("login flow"))
	if len(page.Items) != 1 || page.Items[0].Seq != galleryShots.items[2] || !page.HasMore || page.NextBeforeSeq != galleryShots.items[2] {
		t.Errorf("filtered page = %+v", page)
	}
}

func TestProjectPageMakesAndTurnsGalleries(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	cookie := e.Admin()
	form := url.Values{"name": {"screens"}, "kind": {"gallery"}}
	if rec := e.Do("POST", "/projects/lab/channels", cookie, form); rec.Code != http.StatusSeeOther {
		t.Fatalf("create: status %d\n%s", rec.Code, rec.Body)
	}
	ch, err := e.svc.Channel(context.Background(), adminP, "lab/screens")
	if err != nil || !ch.Gallery() {
		t.Fatalf("lab/screens = %+v, %v", ch, err)
	}
	body := e.Do("GET", "/projects/lab", cookie, nil).Body.String()
	for _, want := range []string{`data-channel="screens" data-kind="gallery"`, `href="/g/lab/screens"`, `action="/projects/lab/channels/screens/kind"`, "Make it a chat", `action="/projects/lab/channels/dut1/kind"`} {
		if !strings.Contains(body, want) {
			t.Errorf("project page lacks %q", want)
		}
	}
	if strings.Contains(body, `action="/projects/lab/channels/general/kind"`) {
		t.Error("#general offers a kind change")
	}
	if rec := e.Do("POST", "/projects/lab/channels/screens/kind", cookie, url.Values{"kind": {"chat"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("kind: status %d\n%s", rec.Code, rec.Body)
	}
	if ch, _ := e.svc.Channel(context.Background(), adminP, "lab/screens"); ch.Gallery() {
		t.Error("lab/screens is still a gallery")
	}
	if rec := e.Do("POST", "/projects/lab/channels/general/kind", cookie, url.Values{"kind": {"gallery"}}); rec.Code == http.StatusSeeOther {
		t.Error("#general became a gallery")
	}
}
