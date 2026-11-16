package messaging

import (
	"slices"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/dto"
)

func TestChatsAreOrderedPinnedThenByActivity(t *testing.T) {
	e := newEnv(t)
	h := e.human("guillaume")
	a := e.agent("codex")
	for _, name := range []string{"alpha", "beta"} {
		if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	e.clk.Advance(time.Minute)
	e.send(a, text("in beta").withChannel("beta"))
	e.clk.Advance(time.Minute)
	e.send(a, SendInput{SendRequest: sendReq("hi", "guillaume", "")})
	ids := func() []string {
		chats, err := e.svc.Chats(e.ctx, h, ChatsQuery{})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range chats {
			out = append(out, c.ID)
		}
		return out
	}
	if got := ids(); !slices.Equal(got, []string{"chan:beta", "chan:alpha", "chan:general", "dm:codex"}) {
		t.Errorf("order = %v", got)
	}
	pin := true
	if err := e.svc.SetPrefs(e.ctx, h, "chan:general", &pin, nil); err != nil {
		t.Fatal(err)
	}
	if got := ids(); got[0] != "chan:general" {
		t.Errorf("pinned not first: %v", got)
	}
	if _, err := e.svc.SetFollow(e.ctx, h, "alpha", false); err != nil {
		t.Fatal(err)
	}
	if got := ids(); slices.Contains(got, "chan:alpha") {
		t.Errorf("unfollowed listed: %v", got)
	}
	all, _ := e.svc.Chats(e.ctx, h, ChatsQuery{All: true})
	if len(all) != 4 {
		t.Errorf("all = %d", len(all))
	}
}

func (in SendInput) withChannel(ch string) SendInput {
	in.Channel = ch
	return in
}

func TestCountsAndNeedsYouRespectMute(t *testing.T) {
	e := newEnv(t)
	h := e.human("guillaume")
	a := e.agent("codex")
	if _, err := e.svc.AskHuman(e.ctx, a, "Reset?", []string{"yes", "no"}, "", 0, nil); err != nil {
		t.Fatal(err)
	}
	e.send(a, text("fyi"))
	e.send(a, SendInput{SendRequest: sendReq("direct", "guillaume", "")})
	c, err := e.svc.Counts(e.ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if c.OpenQuestions != 1 || c.UnreadDirect != 1 || len(c.Chats) != 2 {
		t.Fatalf("counts = %+v", c)
	}
	if c.Chats[0] != (dto.ChatCount{ID: "chan:general", Unread: 2, OpenQuestions: 1}) {
		t.Errorf("general = %+v", c.Chats[0])
	}
	muted := true
	if err := e.svc.SetPrefs(e.ctx, h, "chan:general", nil, &muted); err != nil {
		t.Fatal(err)
	}
	c, _ = e.svc.Counts(e.ctx, h)
	if c.OpenQuestions != 0 || len(c.Chats) != 1 {
		t.Errorf("muted counts = %+v", c)
	}
	// A question addressed by name gets through a mute.
	if _, err := e.svc.SendMessage(e.ctx, a, SendInput{SendRequest: sendReq("You, reset?", "guillaume", ""), Choices: []string{"ok"}}); err != nil {
		t.Fatal(err)
	}
	qs, _ := e.svc.QuestionsFor(e.ctx, h)
	if len(qs) != 1 || qs[0].Text != "You, reset?" {
		t.Errorf("questions = %v", qs)
	}
	// Agents get no @humans questions.
	if qs, _ := e.svc.QuestionsFor(e.ctx, e.agent("other")); len(qs) != 0 {
		t.Errorf("agent questions = %v", qs)
	}
}

func TestChatEventsTellAcksAnswersAndChanges(t *testing.T) {
	e := newEnv(t)
	h := e.human("guillaume")
	a := e.agent("codex")
	events, stop := e.svc.WatchChats(16)
	defer stop()
	next := func() ChatEvent {
		t.Helper()
		select {
		case ev := <-events:
			return ev
		case <-time.After(5 * time.Second):
			t.Fatal("no event")
		}
		return ChatEvent{}
	}
	res, err := e.svc.AskHuman(e.ctx, a, "Reset?", []string{"yes", "no"}, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Ack(e.ctx, h, []int64{res.Question.Seq}); err != nil {
		t.Fatal(err)
	}
	if ev := next(); ev.Kind != EventSeen || ev.Seq != res.Question.Seq || ev.By != "guillaume" || ev.Stream != "chan:general" {
		t.Errorf("seen = %+v", ev)
	}
	reply := e.send(h, SendInput{SendRequest: dto.SendRequest{ReplyTo: res.Question.Seq, Choice: "yes", Channel: "general"}})
	if ev := next(); ev.Kind != EventAnswered || ev.Answer == nil || ev.Answer.Choice != "yes" || ev.Answer.Seq != reply.Seq {
		t.Errorf("answered = %+v", ev)
	}
	// A second reply answers nothing new.
	e.send(e.human("bob"), SendInput{SendRequest: dto.SendRequest{ReplyTo: res.Question.Seq, Choice: "no", Channel: "general"}})
	pin := true
	if err := e.svc.SetPrefs(e.ctx, h, "chan:general", &pin, nil); err != nil {
		t.Fatal(err)
	}
	if ev := next(); ev.Kind != EventChat || ev.Only != "guillaume" {
		t.Errorf("prefs = %+v", ev)
	}
	if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "dut1"}); err != nil {
		t.Fatal(err)
	}
	if ev := next(); ev.Kind != EventChat || ev.Stream != "chan:dut1" || ev.Only != "" {
		t.Errorf("create = %+v", ev)
	}
}

func TestFilteredTimelineFillsItsPage(t *testing.T) {
	e := newEnv(t)
	a := e.agent("codex")
	var questions []int64
	for i := range 30 {
		e.clk.Advance(3 * time.Second)
		if i%10 == 0 {
			r, err := e.svc.AskHuman(e.ctx, a, "q", []string{"y"}, "", 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			questions = append(questions, r.Question.Seq)
			continue
		}
		e.send(a, text("chatter"))
	}
	last, _ := e.st.LastSeq(e.ctx)
	page, err := e.svc.FilteredTimeline(e.ctx, e.human("guillaume"), "chan:general", TimelineQuery{BeforeSeq: last + 1, Limit: 2}, FilterQuestions)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 2 || page.Rows[0].Seq != questions[1] || page.Rows[1].Seq != questions[2] || !page.HasMore {
		t.Errorf("page = %v more=%v", seqsOfRows(page.Rows), page.HasMore)
	}
	page, _ = e.svc.FilteredTimeline(e.ctx, e.human("guillaume"), "chan:general", TimelineQuery{BeforeSeq: questions[1], Limit: 2}, FilterQuestions)
	if len(page.Rows) != 1 || page.Rows[0].Seq != questions[0] || page.HasMore {
		t.Errorf("older page = %v more=%v", seqsOfRows(page.Rows), page.HasMore)
	}
}
