package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/projects"
)

// refsWorld: agent-a in lab and other; narrow reads lab's #general only.
// lab/shots holds seven items (the first two in "login flow"), other/pics
// one.
type refsWorld struct {
	a, narrow auth.Principal
	items     []int64 // lab/shots, oldest first
	foreign   int64   // other/pics
	text      int64   // a text message in lab/general
}

func newRefsWorld(t *testing.T, e *env) refsWorld {
	t.Helper()
	if _, err := e.d.Projects.Create(e.ctx, "other", ""); err != nil {
		t.Fatal(err)
	}
	w := refsWorld{a: e.agent("agent-a"), narrow: e.outsider("narrow")}
	e.grant(projects.Grant{Principal: "agent-a", Project: "other", Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage)})
	e.grant(projects.Grant{Principal: "narrow", Project: "lab", Channel: "general", Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage)})
	for _, name := range []string{"lab/shots", "other/pics"} {
		if _, err := e.svc.CreateChannel(e.ctx, w.a, dto.ChannelCreate{Name: name, Kind: dto.ChannelGallery}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 7 {
		section := ""
		if i < 2 {
			section = "login flow"
		}
		_, out, err := e.svc.toolSend(e.ctx, call(w.a), shot(t, 20+i, fmt.Sprintf("screen %d", i+1), "lab/shots", section))
		if err != nil {
			t.Fatal(err)
		}
		w.items = append(w.items, out.Seq)
	}
	e.clk.Advance(time.Minute)
	_, out, err := e.svc.toolSend(e.ctx, call(w.a), shot(t, 40, "foreign", "other/pics", ""))
	if err != nil {
		t.Fatal(err)
	}
	w.foreign = out.Seq
	w.text = e.send(w.a, SendInput{SendRequest: sendReq("plain words", "", "lab/general")}).Seq
	return w
}

// bare is an "unavailable" ref that says nothing else.
func bare(r dto.GalleryRef) bool {
	b, _ := json.Marshal(r)
	return string(b) == `{"state":"unavailable"}`
}

func refsSend(e *env, p auth.Principal, channel, text string, seqs ...int64) (sendOut, error) {
	_, out, err := e.svc.toolSend(e.ctx, call(p), sendIn{Text: text, Channel: channel, GalleryRefs: seqs})
	return out, err
}

func TestGalleryRefsAreCheckedAgainstTheSendersGrants(t *testing.T) {
	e := newEnv(t)
	w := newRefsWorld(t, e)
	e.clk.Advance(time.Minute)

	if _, err := refsSend(e, w.a, "lab/general", "", w.items[0], w.items[1]); err != nil {
		t.Fatalf("two items of the project's gallery: %v", err)
	}
	// Whatever the reason, the same answer: it says nothing about what the
	// sender cannot see.
	answer := func(seq int64) string {
		_, err := refsSend(e, w.a, "lab/general", "x", seq)
		if code(err) != dto.CodeBadRequest {
			t.Fatalf("ref %d: %v", seq, err)
		}
		return strings.ReplaceAll(err.Error(), fmt.Sprint(seq), "N")
	}
	want := answer(999999)
	for name, seq := range map[string]int64{"another project's item": w.foreign, "a text message": w.text, "a log line": e.line("serial:lab/dut1", "boot", 6)} {
		if got := answer(seq); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	// narrow reads #general but not the gallery.
	if _, err := refsSend(e, w.narrow, "lab/general", "", w.items[0]); code(err) != dto.CodeBadRequest {
		t.Errorf("a ref the sender cannot read: %v", err)
	}
	if _, err := refsSend(e, w.a, "lab/shots", "", w.items[0]); code(err) != dto.CodeBadRequest {
		t.Errorf("a ref in a gallery: %v", err)
	}
	if _, _, err := e.svc.toolSend(e.ctx, call(w.a), sendIn{Text: "x", To: "narrow", GalleryRefs: []int64{w.items[0]}}); code(err) != dto.CodeBadRequest {
		t.Errorf("a ref in a direct message: %v", err)
	}
	many := make([]int64, MaxGalleryRefs+1)
	for i := range many {
		many[i] = w.items[0]
	}
	if _, err := refsSend(e, w.a, "lab/general", "", many...); code(err) != dto.CodeBadRequest {
		t.Errorf("%d refs: %v", len(many), err)
	}
}

func TestGalleryRefsResolvePerReader(t *testing.T) {
	e := newEnv(t)
	w := newRefsWorld(t, e)
	e.clk.Advance(time.Minute)
	text := fmt.Sprintf("compare [[lab/shots#%d]] with [[lab/shots|login flow]], [[other/pics#%d]] and https://cafard.lan/g/lab/shots/%d",
		w.items[6], w.foreign, w.items[5])
	out, err := refsSend(e, w.a, "lab/general", text)
	if err != nil {
		t.Fatal(err)
	}
	m, err := e.st.Get(e.ctx, out.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if refs := readMeta(m).Refs; len(refs) != 3 {
		t.Fatalf("refs = %+v (another project's is not one)", refs)
	}

	d, err := e.svc.EntryDTO(e.ctx, w.a, m)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("compare with , [[other/pics#%d]] and", w.foreign); d.Text != want {
		t.Errorf("text = %q, want %q", d.Text, want)
	}
	if strings.Contains(string(d.Meta), "refs") {
		t.Errorf("meta = %s", d.Meta)
	}
	if len(d.GalleryRefs) != 3 {
		t.Fatalf("refs = %+v", d.GalleryRefs)
	}
	item, section, link := d.GalleryRefs[0], d.GalleryRefs[1], d.GalleryRefs[2]
	if item.State != dto.RefOK || item.Item == nil || item.Item.Title != "screen 7" || item.Href != ItemHref("lab/shots", w.items[6]) || item.Gallery != "lab/shots" {
		t.Errorf("item ref = %+v", item)
	}
	if section.State != dto.RefOK || section.Count != 2 || len(section.Items) != 2 || section.Items[0].Seq != w.items[1] || section.Href != "/g/lab/shots?section=login+flow" {
		t.Errorf("section ref = %+v", section)
	}
	if link.State != dto.RefOK || link.Seq != w.items[5] {
		t.Errorf("link ref = %+v", link)
	}

	// narrow reads the message but not the gallery: three bare
	// "unavailable", and nothing in the DTO names the gallery or its items.
	nd, err := e.svc.EntryDTO(e.ctx, w.narrow, m)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range nd.GalleryRefs {
		if !bare(r) {
			t.Errorf("narrow's ref %d = %+v", i, r)
		}
	}
	raw, _ := json.Marshal(nd)
	for _, leak := range []string{"lab/shots", "login flow", "screen ", fmt.Sprint(w.items[6])} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("narrow's DTO holds %q: %s", leak, raw)
		}
	}
	gm, _, err := e.svc.toolGetMessage(e.ctx, call(w.narrow), getMessageIn{Seq: out.Seq, Ack: ptr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(gm, "unavailable to you") != 3 || strings.Contains(gm, "screen ") {
		t.Errorf("narrow's get_message:\n%s", gm)
	}

	// Retention removes an item: who could see it learns it is gone, the
	// others still only that it is unavailable.
	if err := e.w.Exec(e.ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE seq = ?`, w.items[6])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if d, err = e.svc.EntryDTO(e.ctx, w.a, m); err != nil || d.GalleryRefs[0].State != dto.RefGone || d.GalleryRefs[0].Item != nil {
		t.Errorf("a removed item = %+v, %v", d.GalleryRefs, err)
	}
	if nd, err = e.svc.EntryDTO(e.ctx, w.narrow, m); err != nil || !bare(nd.GalleryRefs[0]) {
		t.Errorf("a removed item for narrow = %+v, %v", nd.GalleryRefs, err)
	}
}

func TestGetMessageReturnsRefImagesWithinTheLimit(t *testing.T) {
	e := newEnv(t)
	w := newRefsWorld(t, e)
	e.clk.Advance(time.Minute)
	out, err := refsSend(e, w.a, "lab/general", "", w.items...)
	if err != nil {
		t.Fatal(err)
	}
	b := e.agent("agent-b")
	text, gm, err := e.svc.toolGetMessage(e.ctx, call(b), getMessageIn{Seq: out.Seq, Ack: ptr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if gm.ImagesShown != maxImagesPerCall || len(gm.images) != maxImagesPerCall {
		t.Errorf("shown %d, images %d", gm.ImagesShown, len(gm.images))
	}
	if strings.Count(text, "(a reference, not a copy)") != 7 || strings.Count(text, "attached below") != 6 || !strings.Contains(text, "not attached") {
		t.Errorf("get_message:\n%s", text)
	}
	if !strings.Contains(text, fmt.Sprintf("[[lab/shots#%d]] \"screen 1\" in section \"login flow\"", w.items[0])) {
		t.Errorf("an item line:\n%s", text)
	}

	// Previews of a message that is only refs.
	ch, err := e.svc.Channel(e.ctx, b, "lab/general")
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.svc.ChannelChat(e.ctx, b, ch)
	if err != nil || c.Preview == nil || c.Preview.Text != "[7 gallery images]" {
		t.Errorf("preview = %+v, %v", c.Preview, err)
	}
	m, err := e.st.Get(e.ctx, out.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if line := e.svc.summary(e.ctx, b, m); !strings.Contains(line, "[7 gallery images]") {
		t.Errorf("inbox line: %s", line)
	}
}
