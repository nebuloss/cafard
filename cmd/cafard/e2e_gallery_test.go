package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/dto"
)

// Galleries from an agent's machine: screenshots by path through the
// stdio proxy, with a section, then refs to them in a chat; and the same
// from a shell with cafard send.
func TestGalleryArgumentsPassThroughTheProxy(t *testing.T) {
	s := startServer(t)
	tok, direct := s.agent("stdio-agent")
	call(t, direct, "create_channel", map[string]any{"name": "shots", "kind": "gallery"})
	cs, _ := s.proxied(tok, nil)
	shot := tempFile(t, "login.png", testPNG(t, 320, 200))

	_, out := call(t, cs, "send", map[string]any{"text": "login page", "channel": "lab/shots", "image_paths": []string{shot}, "section": "login flow"})
	item := num(out["seq"])
	e := s.message(tok, item)
	if e.Section != "login flow" || len(e.Attachments) != 1 || e.Attachments[0].Image == nil || e.Attachments[0].Image.Width != 320 {
		t.Fatalf("gallery item through the proxy = %+v", e)
	}
	// The gallery still refuses what is not a screenshot, through the proxy too.
	callFails(t, cs, "send", map[string]any{"text": "no image", "channel": "lab/shots", "section": "login flow"}, "bad_request")

	_, out = call(t, cs, "send", map[string]any{"text": "before and after:", "channel": "lab/general", "gallery_refs": []int64{item}})
	ref := s.message(tok, num(out["seq"]))
	if len(ref.GalleryRefs) != 1 || ref.GalleryRefs[0].State != dto.RefOK || ref.GalleryRefs[0].Item == nil || ref.GalleryRefs[0].Item.Seq != item {
		t.Fatalf("refs through the proxy = %+v", ref.GalleryRefs)
	}
	// get_message through the proxy returns the referenced image.
	text, gm := call(t, cs, "get_message", map[string]any{"seq": ref.Seq, "ack": false})
	if num(gm["images_shown"]) != 1 || !strings.Contains(text, fmt.Sprintf("[[lab/shots#%d]] \"login page\" in section \"login flow\"", item)) {
		t.Errorf("get_message = %v\n%s", gm, text)
	}

	// cafard send --section posts an item and prints its own page.
	got, err := s.sendCLI(tok, "", "--channel", "lab/shots", "--section", "login flow", "--image", shot, "after submit")
	if err != nil {
		t.Fatalf("cafard send --section: %v\n%s", err, got)
	}
	var seq int64
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) == 2 {
		_, _ = fmt.Sscanf(lines[0], "seq %d in #lab/shots", &seq)
	}
	if seq == 0 || lines[1] != fmt.Sprintf("%s/projects/lab/g/shots/%d", s.base, seq) {
		t.Fatalf("cafard send --section printed %q", got)
	}
	if e := s.message(tok, seq); e.Section != "login flow" || e.Text != "after submit" {
		t.Errorf("item = %+v", e)
	}
	for _, c := range []struct {
		args []string
		says string
	}{
		{[]string{"--channel", "lab/general", "--section", "x", "hello"}, "section"},
		{[]string{"--channel", "lab/shots", "words only"}, "is a gallery"},
	} {
		if out, err := s.sendCLI(tok, "", c.args...); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("send %v = %v %q, want %q", c.args, err, out, c.says)
		}
	}
}
