package messaging

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
)

// shot is a send of one small PNG to channel, with a title and a section.
func shot(t *testing.T, w int, title, channel, section string) sendIn {
	return sendIn{Text: title, Channel: channel, Section: section,
		Images: []dto.ImageUpload{{Data: base64.StdEncoding.EncodeToString(pngBytes(t, w, 20)), Mime: "image/png"}}}
}

func TestGalleryChannelsTakeImagesWithSections(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	_, out, err := e.svc.toolCreateChannel(e.ctx, call(a), createChannelIn{Name: "shots", Kind: dto.ChannelGallery})
	if err != nil || out.Channel.ChannelKind != dto.ChannelGallery {
		t.Fatalf("create = %+v, %v", out, err)
	}
	if _, _, err := e.svc.toolCreateChannel(e.ctx, call(a), createChannelIn{Name: "odd", Kind: "album"}); code(err) != dto.CodeBadRequest {
		t.Errorf("kind album: %v", err)
	}
	// A plain channel is a chat.
	_, plain, err := e.svc.toolCreateChannel(e.ctx, call(a), createChannelIn{Name: "run-1"})
	if err != nil || plain.Channel.ChannelKind != dto.ChannelChat {
		t.Fatalf("plain = %+v, %v", plain, err)
	}

	if _, _, err := e.svc.toolSend(e.ctx, call(a), sendIn{Text: "no image", Channel: "shots"}); code(err) != dto.CodeBadRequest || !strings.Contains(err.Error(), "is a gallery") {
		t.Errorf("text to a gallery: %v", err)
	}
	if _, _, err := e.svc.toolSend(e.ctx, call(a), sendIn{Text: "hi", Channel: "run-1", Section: "x"}); code(err) != dto.CodeBadRequest {
		t.Errorf("section on a chat: %v", err)
	}
	if _, _, err := e.svc.toolSend(e.ctx, call(a), shot(t, 10, "", "shots", strings.Repeat("x", 81))); code(err) != dto.CodeBadRequest {
		t.Errorf("81 characters: %v", err)
	}
	if _, _, err := e.svc.toolSend(e.ctx, call(a), shot(t, 10, "", "shots", "a [b]")); code(err) != dto.CodeBadRequest {
		t.Errorf("brackets: %v", err)
	}
	e.clk.Advance(10e9)
	_, first, err := e.svc.toolSend(e.ctx, call(a), shot(t, 11, "splash", "shots", ""))
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := e.svc.toolSend(e.ctx, call(a), shot(t, 12, "login page", "shots", "  login flow  "))
	if err != nil {
		t.Fatal(err)
	}
	_, third, err := e.svc.toolSend(e.ctx, call(a), shot(t, 13, "", "shots", "login flow"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := e.st.Get(e.ctx, second.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if readMeta(m).Section != "login flow" {
		t.Errorf("section stored as %q", readMeta(m).Section)
	}

	ch, err := e.svc.Gallery(e.ctx, a, "shots")
	if err != nil {
		t.Fatal(err)
	}
	secs, err := e.svc.GallerySections(e.ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 2 || secs[0].Name != "login flow" || secs[0].Items != 2 || secs[0].LastSeq != third.Seq || secs[1].Name != "" || secs[1].Items != 1 {
		t.Errorf("sections = %+v", secs)
	}
	section := "login flow"
	page, err := e.svc.GalleryPage(e.ctx, ch, &section, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Seq != third.Seq || page.Items[1].Title != "login page" || page.Items[0].Href != ItemHref("lab/shots", third.Seq) {
		t.Errorf("page = %+v", page)
	}
	it, newer, older, err := e.svc.GalleryItem(e.ctx, ch, second.Seq)
	if err != nil || it.Section != "login flow" || newer != third.Seq || older != 0 {
		t.Errorf("item = %+v newer %d older %d, %v", it, newer, older, err)
	}
	if _, _, _, err := e.svc.GalleryItem(e.ctx, ch, first.Seq+1000); code(err) != dto.CodeNotFound {
		t.Errorf("missing item: %v", err)
	}

	// Agents see the kind and the sections.
	text, lc, err := e.svc.toolListChannels(e.ctx, call(a), listChannelsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "#lab/shots, a gallery (post screenshots here); sections, newest first: login flow: 2, (unsorted): 1") {
		t.Errorf("list_channels:\n%s", text)
	}
	var found bool
	for _, c := range lc.Channels {
		if c.ID == "chan:lab/shots" {
			found = true
			if c.ChannelKind != dto.ChannelGallery || len(c.Sections) != 2 {
				t.Errorf("structured = %+v", c)
			}
		}
		if c.ID == "chan:lab/run-1" && (c.ChannelKind != dto.ChannelChat || c.Sections != nil) {
			t.Errorf("chat = %+v", c)
		}
	}
	if !found {
		t.Error("no #lab/shots in list_channels")
	}
	text, lp, err := e.svc.toolListProjects(e.ctx, call(a), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "gallery #lab/shots (post screenshots there) with sections login flow") {
		t.Errorf("list_projects:\n%s", text)
	}
	if len(lp.Projects) == 0 || len(lp.Projects[0].Galleries) != 1 {
		t.Errorf("projects = %+v", lp.Projects)
	}
}

func TestChannelKindChanges(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	if _, _, err := e.svc.toolCreateChannel(e.ctx, call(a), createChannelIn{Name: "run-1"}); err != nil {
		t.Fatal(err)
	}
	_, out, err := e.svc.toolUpdateChannel(e.ctx, call(a), updateChannelIn{Name: "run-1", Kind: dto.ChannelGallery})
	if err != nil || out.Channel.ChannelKind != dto.ChannelGallery {
		t.Fatalf("update = %+v, %v", out, err)
	}
	// The change leaves a marker, which is not an item.
	ch, err := e.svc.Gallery(e.ctx, a, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if secs, err := e.svc.GallerySections(e.ctx, ch); err != nil || len(secs) != 0 {
		t.Errorf("sections after the marker = %+v, %v", secs, err)
	}
	if _, _, err := e.svc.toolUpdateChannel(e.ctx, call(a), updateChannelIn{Name: "general", Kind: dto.ChannelGallery}); code(err) != dto.CodeBadRequest {
		t.Errorf("#general as a gallery: %v", err)
	}
	// Renaming keeps the kind.
	if _, out, err = e.svc.toolUpdateChannel(e.ctx, call(a), updateChannelIn{Name: "run-1", Rename: "run-2"}); err != nil || out.Channel.ChannelKind != dto.ChannelGallery {
		t.Errorf("renamed = %+v, %v", out, err)
	}
	// Reading a gallery needs read on it like any channel.
	outsider := auth.Principal{Name: "outsider", Kind: auth.KindAgent, Scopes: auth.NewScopeSet(auth.ScopeRead)}
	if _, err := e.svc.Gallery(e.ctx, outsider, "lab/run-2"); err == nil {
		t.Error("an agent with no grant reads the gallery")
	}
}
