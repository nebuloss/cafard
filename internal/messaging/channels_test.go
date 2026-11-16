package messaging

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

func ptr[T any](v T) *T { return &v }

func timelineSeqs(t *testing.T, e *env, p auth.Principal, stream string) (seqs []int64, events []bool) {
	t.Helper()
	page, err := e.svc.Timeline(e.ctx, p, stream, TimelineQuery{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range page.Rows {
		seqs = append(seqs, r.Seq)
		events = append(events, r.Event)
	}
	return seqs, events
}

func TestChannelNamesRightsAndUnknownChannel(t *testing.T) {
	e := newEnv(t)
	a, b := e.agent("agent-a"), e.agent("agent-b")
	admin := e.principal("root", auth.KindHuman, auth.ScopeRead, auth.ScopeMessage, auth.ScopeAdmin)
	for _, bad := range []string{"general", "dm", "Bad", "has space", strings.Repeat("x", 41), ""} {
		if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: bad}); code(err) != dto.CodeBadRequest {
			t.Errorf("name %q: %v", bad, err)
		}
	}
	if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "x", Patterns: []string{"serial:[dut"}}); code(err) != dto.CodeInvalidPattern {
		t.Errorf("bad glob: %v", err)
	}
	if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "x", Patterns: []string{"dm:*"}}); code(err) != dto.CodeInvalidPattern {
		t.Errorf("message stream binding: %v", err)
	}
	if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "dut1", Patterns: []string{"serial:dut1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateChannel(e.ctx, b, dto.ChannelCreate{Name: "dut1"}); code(err) != dto.CodeConflict {
		t.Errorf("duplicate: %v", err)
	}
	// Anyone may change bindings; only the creator or an admin renames,
	// archives or deletes.
	if _, err := e.svc.UpdateChannel(e.ctx, b, "dut1", dto.ChannelUpdate{AddPatterns: []string{"syslog:dut1*"}}); err != nil {
		t.Errorf("b binds: %v", err)
	}
	if _, err := e.svc.UpdateChannel(e.ctx, b, "dut1", dto.ChannelUpdate{Rename: "dut9"}); code(err) != dto.CodeForbidden {
		t.Errorf("b renames: %v", err)
	}
	if err := e.svc.DeleteChannel(e.ctx, b, "dut1"); code(err) != dto.CodeForbidden {
		t.Errorf("b deletes: %v", err)
	}
	if _, err := e.svc.UpdateChannel(e.ctx, admin, "general", dto.ChannelUpdate{Rename: "main"}); code(err) != dto.CodeForbidden {
		t.Errorf("rename general: %v", err)
	}
	if err := e.svc.DeleteChannel(e.ctx, admin, "general"); code(err) != dto.CodeForbidden {
		t.Errorf("delete general: %v", err)
	}
	_, err := e.svc.ResolveChannel(e.ctx, "dut3")
	if code(err) != dto.CodeUnknownChannel || !strings.Contains(err.Error(), "Existing channels: dut1, general") {
		t.Errorf("unknown channel = %v", err)
	}
	pats, err := e.svc.ResolveChannel(e.ctx, "dut1")
	if err != nil || !slices.Equal(pats, []string{"serial:dut1", "syslog:dut1*"}) {
		t.Errorf("resolve = %v %v", pats, err)
	}
	if pats, err := e.svc.ResolveChannel(e.ctx, "general"); err != nil || len(pats) != 0 {
		t.Errorf("general = %v %v", pats, err)
	}
}

func TestBindingChangesWriteMarkersAndRenameMovesMessages(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "run-42", Patterns: []string{"serial:dut1"}}); err != nil {
		t.Fatal(err)
	}
	m := e.send(a, SendInput{SendRequest: sendReq("starting", "", "run-42")})
	if _, err := e.svc.UpdateChannel(e.ctx, a, "run-42", dto.ChannelUpdate{AddPatterns: []string{"serial:dut2"}, RemovePatterns: []string{"serial:dut1"}}); err != nil {
		t.Fatal(err)
	}
	ch, err := e.svc.UpdateChannel(e.ctx, a, "run-42", dto.ChannelUpdate{Rename: "run-43", Topic: ptr("the rerun")})
	if err != nil {
		t.Fatal(err)
	}
	if ch.Name != "run-43" || ch.Topic != "the rerun" || !slices.Equal(ch.Patterns, []string{"serial:dut2"}) {
		t.Errorf("after rename: %+v", ch)
	}
	if _, err := e.svc.Channel(e.ctx, "run-42"); code(err) != dto.CodeUnknownChannel {
		t.Errorf("old name: %v", err)
	}
	moved, err := e.st.Get(e.ctx, m.Seq)
	if err != nil || moved.Stream != "chan:run-43" {
		t.Errorf("message after rename = %+v %v", moved, err)
	}
	res, _ := e.st.Read(e.ctx, store.Query{Streams: []string{"chan:run-43"}, Kinds: []store.Kind{store.KindMarker}})
	var texts []string
	for _, x := range res.Entries {
		texts = append(texts, x.Text)
	}
	want := []string{"agent-a bound serial:dut1", "agent-a bound serial:dut2; unbound serial:dut1", "agent-a renamed #run-42 to #run-43"}
	if !slices.Equal(texts, want) {
		t.Errorf("markers = %q", texts)
	}
	if st, _ := e.svc.Follow(e.ctx, a, "chan:run-43"); !st.Followed {
		t.Error("the follow did not move with the rename")
	}
	if err := e.svc.DeleteChannel(e.ctx, a, "run-43"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Get(e.ctx, m.Seq); err == nil {
		t.Error("deleting the channel kept its messages")
	}
}

func TestTimelineUnionBySurfaceAndRetroactiveBindings(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	h := e.human("guillaume")
	l1 := e.line("serial:dut1", "booting", 6)
	m1 := e.mark("serial:dut1", "port reconnected")
	l2 := e.line("serial:dut1", "Kernel panic", 2)
	x := e.mark("serial:other", "unrelated")
	if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "dut1"}); err != nil {
		t.Fatal(err)
	}
	msg := e.send(a, SendInput{SendRequest: sendReq("looking", "", "dut1")})
	seqs, _ := timelineSeqs(t, e, h, "chan:dut1")
	if !slices.Equal(seqs, []int64{msg.Seq}) {
		t.Fatalf("unbound timeline = %v", seqs)
	}
	// A binding added later changes the history: the union is on read.
	if _, err := e.svc.UpdateChannel(e.ctx, a, "dut1", dto.ChannelUpdate{AddPatterns: []string{"serial:dut*"}}); err != nil {
		t.Fatal(err)
	}
	seqs, events := timelineSeqs(t, e, h, "chan:dut1")
	if len(seqs) != 3 || seqs[0] != m1 || seqs[1] != msg.Seq || !events[0] || events[1] {
		t.Errorf("markers surface = %v %v (marker %d, message %d)", seqs, events, m1, msg.Seq)
	}
	if _, err := e.svc.UpdateChannel(e.ctx, a, "dut1", dto.ChannelUpdate{Surface: dto.SurfaceErrors}); err != nil {
		t.Fatal(err)
	}
	seqs, _ = timelineSeqs(t, e, h, "chan:dut1")
	if !slices.Contains(seqs, l2) || slices.Contains(seqs, l1) || !slices.Contains(seqs, m1) || slices.Contains(seqs, x) {
		t.Errorf("errors surface = %v (want %d and %d, not %d or %d)", seqs, m1, l2, l1, x)
	}
	if _, err := e.svc.UpdateChannel(e.ctx, a, "dut1", dto.ChannelUpdate{Surface: dto.SurfaceNone}); err != nil {
		t.Fatal(err)
	}
	seqs, _ = timelineSeqs(t, e, h, "chan:dut1")
	for _, s := range seqs {
		if s == m1 || s == l2 {
			t.Errorf("none surface shows %d", s)
		}
	}
	// Paging: forward and backward cover everything once.
	if _, err := e.svc.UpdateChannel(e.ctx, a, "dut1", dto.ChannelUpdate{Surface: dto.SurfaceErrors}); err != nil {
		t.Fatal(err)
	}
	all, _ := timelineSeqs(t, e, h, "chan:dut1")
	var fwd []int64
	q := TimelineQuery{Limit: 2}
	for {
		page, err := e.svc.Timeline(e.ctx, h, "chan:dut1", q)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Rows {
			fwd = append(fwd, r.Seq)
		}
		if !page.HasMore {
			break
		}
		q.SinceSeq = page.NextSeq
	}
	if !slices.Equal(fwd, all) {
		t.Errorf("forward paging = %v, want %v", fwd, all)
	}
	var back []int64
	bq := TimelineQuery{Limit: 2, BeforeSeq: 1 << 62}
	for {
		page, err := e.svc.Timeline(e.ctx, h, "chan:dut1", bq)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Rows) == 0 {
			break
		}
		back = append(seqsOfRows(page.Rows), back...)
		bq.BeforeSeq = page.Rows[0].Seq
	}
	if !slices.Equal(back, all) {
		t.Errorf("backward paging = %v, want %v", back, all)
	}
}

func seqsOfRows(rows []TimelineRow) []int64 {
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.Seq
	}
	return out
}

func TestGlobBindingMatchesStreamsCreatedLater(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	ch, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "dut1", Patterns: []string{"syslog:dut1*"}})
	if err != nil {
		t.Fatal(err)
	}
	if ids, _ := e.svc.BoundStreams(e.ctx, ch); len(ids) != 0 {
		t.Errorf("bound before any stream = %v", ids)
	}
	e.line("syslog:dut1-bmc", "hello", 6)
	e.line("syslog:dut2", "other", 6)
	ids, err := e.svc.BoundStreams(e.ctx, ch)
	if err != nil || !slices.Equal(ids, []string{"syslog:dut1-bmc"}) {
		t.Errorf("bound after = %v %v", ids, err)
	}
	chat, err := e.svc.ChannelChat(e.ctx, a, ch)
	if err != nil || len(chat.Streams) != 1 || chat.Streams[0].ID != "syslog:dut1-bmc" {
		t.Errorf("chat streams = %+v %v", chat.Streams, err)
	}
}

func TestConfigChannelsAreReappliedAtStart(t *testing.T) {
	cfg := func(o *Options) {
		o.Channels.List = []ChannelConfig{
			{Name: General, Topic: "anything not about one device"},
			{Name: "dut1", Title: "DUT 1", Streams: []string{"serial:dut1", "syslog:dut1*"}, Surface: dto.SurfaceErrors},
		}
	}
	e := newEnv(t, cfg)
	a := e.agent("agent-a")
	ch, err := e.svc.Channel(e.ctx, "dut1")
	if err != nil || ch.Source != dto.SourceConfig || ch.Title != "DUT 1" || ch.Surface != dto.SurfaceErrors || len(ch.Patterns) != 2 {
		t.Fatalf("config channel = %+v %v", ch, err)
	}
	if g, _ := e.svc.Channel(e.ctx, General); g.Topic != "anything not about one device" {
		t.Errorf("general topic = %q", g.Topic)
	}
	// Edits are allowed and last until the next start.
	if _, err := e.svc.UpdateChannel(e.ctx, a, "dut1", dto.ChannelUpdate{Title: ptr("edited"), RemovePatterns: []string{"serial:dut1"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteChannel(e.ctx, a, "dut1"); code(err) != dto.CodeForbidden {
		t.Errorf("deleting a config channel: %v", err)
	}
	o := defaultOptions()
	cfg(&o)
	again, err := New(e.ctx, e.d, o)
	if err != nil {
		t.Fatal(err)
	}
	ch, _ = again.Channel(e.ctx, "dut1")
	if ch.Title != "DUT 1" || !slices.Equal(ch.Patterns, []string{"serial:dut1", "syslog:dut1*"}) {
		t.Errorf("after restart = %+v", ch)
	}
	// A channel removed from the config becomes a runtime one.
	third, err := New(e.ctx, e.d, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if ch, _ := third.Channel(e.ctx, "dut1"); ch.Source != dto.SourceRuntime {
		t.Errorf("dropped from the config: %+v", ch)
	}
	if err := (&Channels{List: []ChannelConfig{{Name: General, Streams: []string{"x:y"}}}}).Validate(); err == nil {
		t.Error("general with bindings validated")
	}
	if err := (&Channels{List: []ChannelConfig{{Name: "Bad"}}}).Validate(); err == nil {
		t.Error("bad name validated")
	}
}

func TestAutoArchiveWithAFakeClock(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	for _, n := range []string{"quiet", "busy-logs", "busy-chat"} {
		if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: n, Patterns: []string{"serial:" + n}}); err != nil {
			t.Fatal(err)
		}
	}
	e.clk.Advance(13 * 24 * time.Hour)
	e.line("serial:busy-logs", "still alive", 6)
	e.send(a, SendInput{SendRequest: sendReq("still here", "", "busy-chat")})
	e.clk.Advance(2 * 24 * time.Hour)
	n, err := e.svc.ArchiveIdle(e.ctx)
	if err != nil || n != 1 {
		t.Fatalf("archived %d %v", n, err)
	}
	chs, _ := e.svc.Channels(e.ctx, false)
	var names []string
	for _, c := range chs {
		names = append(names, c.Name)
	}
	if !slices.Equal(names, []string{"busy-chat", "busy-logs", "general"}) {
		t.Errorf("listed = %v", names)
	}
	if ch, _ := e.svc.Channel(e.ctx, "quiet"); !ch.Archived() {
		t.Error("quiet is not archived")
	}
	// Still readable; posting unarchives.
	e.send(a, SendInput{SendRequest: sendReq("back", "", "quiet")})
	if ch, _ := e.svc.Channel(e.ctx, "quiet"); ch.Archived() {
		t.Error("posting did not unarchive")
	}
	e.clk.Advance(30 * 24 * time.Hour)
	if _, err := e.svc.ArchiveIdle(e.ctx); err != nil {
		t.Fatal(err)
	}
	if g, _ := e.svc.Channel(e.ctx, General); g.Archived() {
		t.Error("general was archived")
	}
}

func TestSegments(t *testing.T) {
	got := Segments("see https://example.com/a?b=1, run `make test` then:\n```sh\nls -l\n```\ndone. javascript:alert(1)")
	want := []dto.Segment{
		{T: dto.SegText, V: "see "},
		{T: dto.SegLink, V: "https://example.com/a?b=1", Href: "https://example.com/a?b=1"},
		{T: dto.SegText, V: ", run "},
		{T: dto.SegCode, V: "make test"},
		{T: dto.SegText, V: " then:\n"},
		{T: dto.SegCodeblock, V: "ls -l", Lang: "sh"},
		{T: dto.SegText, V: "\ndone. javascript:alert(1)"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("segments:\n%+v\nwant\n%+v", got, want)
	}
	if got := Segments("a `b"); !slices.Equal(got, []dto.Segment{{T: dto.SegText, V: "a `b"}}) {
		t.Errorf("unclosed = %+v", got)
	}
}
