package messaging

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

func sendReq(textValue, to, channel string) dto.SendRequest {
	return dto.SendRequest{Text: textValue, To: to, Channel: channel}
}

func code(err error) string {
	var apiErr *dto.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	if err == nil {
		return ""
	}
	return "other: " + err.Error()
}

func inbox(t *testing.T, e *env, p auth.Principal) []int64 {
	t.Helper()
	list, _, err := e.svc.Inbox(e.ctx, p, InboxQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return seqsOf(list)
}

func TestSenderComesFromThePrincipalAndStreamsFromTo(t *testing.T) {
	e := newEnv(t)
	a, b := e.agent("agent-a"), e.agent("agent-b")
	dm := e.send(a, SendInput{SendRequest: sendReq("hi b", "agent-b", "ignored")})
	if dm.Stream != "dm:agent-a~agent-b" || dm.Recipient != "agent-b" || dm.Sender != "agent-a" {
		t.Errorf("dm = %+v", dm)
	}
	humans := e.send(b, SendInput{SendRequest: sendReq("need a hand", dto.RecipientHumans, "")})
	if humans.Stream != "chan:lab/general" || humans.Recipient != dto.RecipientHumans {
		t.Errorf("@humans = %+v", humans)
	}
	bc := e.send(a, text("hello all"))
	if bc.Stream != "chan:lab/general" || bc.Recipient != "" {
		t.Errorf("broadcast = %+v", bc)
	}
	if _, err := e.svc.SendMessage(e.ctx, a, SendInput{SendRequest: sendReq("x", "", "nope")}); code(err) != dto.CodeUnknownChannel {
		t.Errorf("unknown channel: %v", err)
	}
	if _, err := e.svc.SendMessage(e.ctx, a, SendInput{SendRequest: sendReq("x", "ghost", "")}); code(err) != dto.CodeNotFound {
		t.Errorf("unknown principal: %v", err)
	}
	if _, err := e.svc.SendMessage(e.ctx, a, SendInput{SendRequest: sendReq(strings.Repeat("x", MaxTextBytes+1), "", "")}); code(err) != dto.CodeTooLarge {
		t.Errorf("too long: %v", err)
	}
	reader := e.principal("reader", auth.KindAgent, auth.ScopeRead)
	if _, err := e.svc.SendMessage(e.ctx, reader, text("x")); code(err) != dto.CodeForbidden {
		t.Errorf("no message scope: %v", err)
	}
}

func TestInboxVisibilityRules(t *testing.T) {
	e := newEnv(t)
	a, b, c := e.agent("agent-a"), e.agent("agent-b"), e.agent("agent-c")
	h := e.human("guillaume")
	if _, err := e.svc.CreateChannel(e.ctx, c, dto.ChannelCreate{Name: "dut1"}); err != nil {
		t.Fatal(err)
	}

	dm := e.send(a, SendInput{SendRequest: sendReq("secret", "agent-b", "")})
	humans := e.send(a, SendInput{SendRequest: sendReq("press reset", dto.RecipientHumans, "")})
	general := e.send(a, text("broadcast in general"))
	dut1 := e.send(a, SendInput{SendRequest: sendReq("broadcast in dut1", "", "dut1")})

	// b: the DM only (follows nothing).
	if got := inbox(t, e, b); !slices.Equal(got, []int64{dm.Seq}) {
		t.Errorf("b inbox = %v, want the DM %d", got, dm.Seq)
	}
	// c: created dut1 so follows it; never the DM.
	if got := inbox(t, e, c); !slices.Equal(got, []int64{dut1.Seq}) {
		t.Errorf("c inbox = %v, want %d", got, dut1.Seq)
	}
	// The human follows every channel and gets @humans.
	if got := inbox(t, e, h); !slices.Equal(got, []int64{humans.Seq, general.Seq, dut1.Seq}) {
		t.Errorf("human inbox = %v", got)
	}
	// The sender's own messages are never in its inbox.
	if got := inbox(t, e, a); len(got) != 0 {
		t.Errorf("a inbox = %v", got)
	}

	// A DM is invisible to a third party everywhere, admin included.
	admin := e.principal("root", auth.KindHuman, auth.ScopeRead, auth.ScopeMessage, auth.ScopeAdmin)
	for _, p := range []auth.Principal{c, h, admin} {
		if e.svc.Visible(e.ctx, p, dm) {
			t.Errorf("%s sees the DM", p.Name)
		}
		if _, err := e.svc.Message(e.ctx, p, dm.Seq); code(err) != dto.CodeNotFound {
			t.Errorf("%s get DM: %v", p.Name, err)
		}
		if _, err := e.svc.Timeline(e.ctx, p, dm.Stream, TimelineQuery{}); code(err) != dto.CodeNotFound {
			t.Errorf("%s timeline of the DM: %v", p.Name, err)
		}
		if n, _ := e.svc.Ack(e.ctx, p, []int64{dm.Seq}); n != 0 {
			t.Errorf("%s acked the DM", p.Name)
		}
		if _, err := e.svc.SendMessage(e.ctx, p, SendInput{SendRequest: dto.SendRequest{Text: "re", ReplyTo: dm.Seq}}); code(err) != dto.CodeNotFound {
			t.Errorf("%s replied to the DM: %v", p.Name, err)
		}
	}
	for _, p := range []auth.Principal{a, b} {
		if !e.svc.Visible(e.ctx, p, dm) {
			t.Errorf("%s cannot see its DM", p.Name)
		}
	}
	// The hub filter applies it too.
	sub := e.hub.Subscribe(hubFilterFor(c), 10)
	defer sub.Close()
	e.send(a, SendInput{SendRequest: sendReq("another secret", "agent-b", "")})
	pub := e.send(a, text("public"))
	select {
	case got := <-sub.C():
		if got.Seq != pub.Seq {
			t.Errorf("c's subscription got %d (%s), want the public %d", got.Seq, got.Stream, pub.Seq)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no entry")
	}
}

func TestAcksArePerPrincipalAndSeenBy(t *testing.T) {
	e := newEnv(t)
	a, b := e.agent("agent-a"), e.agent("agent-b")
	h := e.human("guillaume")
	for _, p := range []auth.Principal{a, b} {
		if _, err := e.svc.SetFollow(e.ctx, p, "general", true); err != nil {
			t.Fatal(err)
		}
	}
	m := e.send(h, text("photo of the bench"))
	if got := inbox(t, e, a); !slices.Equal(got, []int64{m.Seq}) {
		t.Fatalf("a inbox = %v", got)
	}
	if n, err := e.svc.Ack(e.ctx, a, []int64{m.Seq, m.Seq}); err != nil || n != 1 {
		t.Fatalf("ack = %d %v", n, err)
	}
	if got := inbox(t, e, a); len(got) != 0 {
		t.Errorf("a still has %v", got)
	}
	if got := inbox(t, e, b); !slices.Equal(got, []int64{m.Seq}) {
		t.Errorf("b's ack state followed a's: %v", got)
	}
	if _, err := e.svc.Ack(e.ctx, b, []int64{m.Seq}); err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.EntryDTO(e.ctx, h, m)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.SeenBy, []string{"agent-a", "agent-b"}) {
		t.Errorf("seen_by = %v", d.SeenBy)
	}
	// The sender's own ack does not count as a receipt.
	if _, err := e.svc.Ack(e.ctx, h, []int64{m.Seq}); err != nil {
		t.Fatal(err)
	}
	if d, _ = e.svc.EntryDTO(e.ctx, h, m); len(d.SeenBy) != 2 {
		t.Errorf("seen_by after the sender acked = %v", d.SeenBy)
	}
}

func TestFollowsDecideBroadcastDelivery(t *testing.T) {
	e := newEnv(t)
	a, b := e.agent("agent-a"), e.agent("agent-b")
	h := e.human("guillaume")
	m1 := e.send(a, text("one"))
	if got := inbox(t, e, b); len(got) != 0 {
		t.Errorf("b follows nothing but got %v", got)
	}
	if _, err := e.svc.SetFollow(e.ctx, b, "general", true); err != nil {
		t.Fatal(err)
	}
	if got := inbox(t, e, b); !slices.Equal(got, []int64{m1.Seq}) {
		t.Errorf("after follow: %v", got)
	}
	if _, err := e.svc.SetFollow(e.ctx, h, "#general", false); err != nil {
		t.Fatal(err)
	}
	if got := inbox(t, e, h); len(got) != 0 {
		t.Errorf("human unfollowed general but got %v", got)
	}
	// @humans still reaches an unfollowing human.
	q := e.send(a, SendInput{SendRequest: sendReq("look", dto.RecipientHumans, "")})
	if got := inbox(t, e, h); !slices.Equal(got, []int64{q.Seq}) {
		t.Errorf("@humans: %v", got)
	}
	// Sending to a channel makes an agent follow it; reading it with
	// channel= does too.
	c := e.agent("agent-c")
	if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "dut1"}); err != nil {
		t.Fatal(err)
	}
	e.send(c, SendInput{SendRequest: sendReq("hi", "", "dut1")})
	if st, _ := e.svc.Follow(e.ctx, c, "chan:lab/dut1"); !st.Followed {
		t.Error("sending did not follow")
	}
	d := e.agent("agent-d")
	if _, _, err := e.svc.ResolveChannel(e.ctx, d, "dut1"); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.svc.Follow(e.ctx, d, "chan:lab/dut1"); !st.Followed {
		t.Error("channel= did not follow")
	}
	// An explicit unfollow sticks through later reads.
	if _, err := e.svc.SetFollow(e.ctx, d, "dut1", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.ResolveChannel(e.ctx, d, "dut1"); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.svc.Follow(e.ctx, d, "chan:lab/dut1"); st.Followed {
		t.Error("reading re-followed after an explicit unfollow")
	}
}

func TestRateLimiter(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	for i := range 10 {
		if _, err := e.svc.SendMessage(e.ctx, a, text("spam")); err != nil {
			t.Fatalf("message %d of the burst refused: %v", i+1, err)
		}
	}
	_, err := e.svc.SendMessage(e.ctx, a, text("one too many"))
	var apiErr *dto.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != dto.CodeRateLimited || apiErr.RetryAfterS != 2 {
		t.Fatalf("11th = %v", err)
	}
	if !strings.Contains(apiErr.Message, "30 per minute") {
		t.Errorf("message does not explain: %q", apiErr.Message)
	}
	// Another principal has its own bucket.
	if _, err := e.svc.SendMessage(e.ctx, e.agent("agent-b"), text("fine")); err != nil {
		t.Errorf("b: %v", err)
	}
	// 30 per minute refills one every 2 s.
	e.clk.Advance(2 * time.Second)
	if _, err := e.svc.SendMessage(e.ctx, a, text("after 2 s")); err != nil {
		t.Errorf("after refill: %v", err)
	}
	if _, err := e.svc.SendMessage(e.ctx, a, text("again")); code(err) != dto.CodeRateLimited {
		t.Errorf("no token left: %v", err)
	}
	e.clk.Advance(time.Minute)
	for range 10 {
		if _, err := e.svc.SendMessage(e.ctx, a, text("refilled")); err != nil {
			t.Fatalf("after a minute: %v", err)
		}
	}
	if _, err := e.svc.SendMessage(e.ctx, a, text("burst is 10")); code(err) != dto.CodeRateLimited {
		t.Errorf("burst cap: %v", err)
	}
}

func TestChannelCreationIsRateLimited(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	for i := range 10 {
		if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "run-" + string(rune('a'+i))}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "run-z"}); code(err) != dto.CodeRateLimited {
		t.Errorf("11th channel: %v", err)
	}
	e.clk.Advance(6 * time.Minute)
	if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "run-z"}); err != nil {
		t.Errorf("after 6 min: %v", err)
	}
}

func TestRepliesAreFlatAndQuestionsCloseOnTheFirstHumanReply(t *testing.T) {
	e := newEnv(t)
	a, b := e.agent("agent-a"), e.agent("agent-b")
	h, h2 := e.human("guillaume"), e.human("marie")
	q := e.send(a, SendInput{SendRequest: sendReq("Is the board on?", dto.RecipientHumans, ""), Choices: []string{"yes", "no"}})
	r1 := e.send(b, SendInput{SendRequest: dto.SendRequest{Text: "I think so", ReplyTo: q.Seq}})
	if d, _ := e.svc.EntryDTO(e.ctx, h, q); d.Question == nil || !d.Question.Open {
		t.Fatalf("an agent's reply closed the question: %+v", d.Question)
	}
	r2 := e.send(b, SendInput{SendRequest: dto.SendRequest{Text: "reply to a reply", ReplyTo: r1.Seq}})
	if r2.ReplyTo != q.Seq {
		t.Errorf("reply_to = %d, want the root %d", r2.ReplyTo, q.Seq)
	}
	if _, err := e.svc.SendMessage(e.ctx, h, SendInput{SendRequest: dto.SendRequest{ReplyTo: q.Seq, Choice: "maybe"}}); code(err) != dto.CodeBadRequest {
		t.Errorf("choice outside the list: %v", err)
	}
	ans := e.send(h, SendInput{SendRequest: dto.SendRequest{ReplyTo: q.Seq, Choice: "yes"}})
	if ans.Text != "yes" {
		t.Errorf("a choice-only reply has text %q", ans.Text)
	}
	e.send(h2, SendInput{SendRequest: dto.SendRequest{Text: "no actually", ReplyTo: q.Seq, Choice: "no"}})
	d, err := e.svc.EntryDTO(e.ctx, a, q)
	if err != nil {
		t.Fatal(err)
	}
	if d.Question.Open || d.Question.Answer == nil || d.Question.Answer.By != "guillaume" || d.Question.Answer.Choice != "yes" || d.Question.Answer.Seq != ans.Seq {
		t.Errorf("question = %+v answer %+v", d.Question, d.Question.Answer)
	}
	if n, _ := e.svc.openQuestions(e.ctx, "chan:lab/general"); n != 0 {
		t.Errorf("open questions = %d", n)
	}
}

func TestAskHumanRoundTrip(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	h := e.human("guillaume")
	type result struct {
		r   AskResult
		err error
	}
	got := make(chan result, 1)
	go func() {
		r, err := e.svc.AskHuman(e.ctx, a, "Plug the USB cable?", []string{"done", "can't"}, "", 10*time.Second, nil)
		got <- result{r, err}
	}()
	var q store.Entry
	deadline := time.Now().Add(5 * time.Second)
	for {
		list, _, _ := e.svc.Inbox(e.ctx, h, InboxQuery{})
		if len(list) > 0 {
			q = list[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the question did not reach the human's inbox")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !IsQuestion(q) || q.Recipient != dto.RecipientHumans {
		t.Fatalf("question = %+v", q)
	}
	// An agent's reply does not release ask_human.
	e.send(e.agent("agent-b"), SendInput{SendRequest: dto.SendRequest{Text: "not me", ReplyTo: q.Seq}})
	e.send(h, SendInput{SendRequest: dto.SendRequest{ReplyTo: q.Seq, Choice: "done"}})
	select {
	case r := <-got:
		if r.err != nil || r.r.Reply == nil || r.r.Choice != "done" || r.r.Reply.Sender != "guillaume" {
			t.Fatalf("ask = %+v %v", r.r, r.err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("ask_human was not released by the human's answer")
	}

	// Timed out: the question stays open and the answer is collected later.
	r, err := e.svc.AskHuman(e.ctx, a, "Still there?", nil, "", 50*time.Millisecond, nil)
	if err != nil || r.Reply != nil {
		t.Fatalf("timeout = %+v %v", r, err)
	}
	e.send(h, SendInput{SendRequest: dto.SendRequest{Text: "yes", ReplyTo: r.Question.Seq}})
	reply, ok, err := e.svc.Wait(e.ctx, a, WaitQuery{ReplyTo: r.Question.Seq, Timeout: time.Second})
	if err != nil || !ok || reply.Text != "yes" {
		t.Errorf("wait_message(reply_to) = %+v %v %v", reply, ok, err)
	}
	// With timeout 0 it returns at once.
	r, err = e.svc.AskHuman(e.ctx, a, "Async?", nil, "", 0, nil)
	if err != nil || r.Question.Seq == 0 || r.Reply != nil {
		t.Errorf("timeout 0 = %+v %v", r, err)
	}
}

func TestWaitMessageSeesWhatArrivesWhileSubscribing(t *testing.T) {
	e := newEnv(t)
	a, b := e.agent("agent-a"), e.agent("agent-b")
	for i := range 20 {
		hh := &hookHub{inner: e.hub}
		e.svc.hub = hh
		msg := "m" + string(rune('a'+i))
		hh.onSubscribe = func() {
			go func() {
				e.clk.Advance(3 * time.Second) // keep the rate limiter happy
				if _, err := e.svc.SendMessage(e.ctx, a, SendInput{SendRequest: sendReq(msg, "agent-b", "")}); err != nil {
					t.Error(err)
				}
			}()
		}
		got, ok, err := e.svc.Wait(e.ctx, b, WaitQuery{From: "agent-a", Timeout: 5 * time.Second})
		if err != nil || !ok || got.Text != msg {
			t.Fatalf("round %d: %+v %v %v", i, got, ok, err)
		}
		if _, err := e.svc.Ack(e.ctx, b, []int64{got.Seq}); err != nil {
			t.Fatal(err)
		}
	}
	// Cancelling releases the wait at once.
	e.svc.hub = e.hub
	ctx, cancel := context.WithTimeout(e.ctx, 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, _, err := e.svc.Wait(ctx, b, WaitQuery{Timeout: time.Minute}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("cancelled wait = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("cancel took too long")
	}
}

func TestClaimContentionAndExpiry(t *testing.T) {
	e := newEnv(t)
	a, b := e.agent("agent-a"), e.agent("agent-b")
	if _, _, err := e.svc.Claim(e.ctx, a, "serial:lab/dut1", time.Minute, "flashing"); err != nil {
		t.Fatal(err)
	}
	_, _, err := e.svc.Claim(e.ctx, b, "serial:lab/dut1", time.Minute, "")
	var apiErr *dto.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != dto.CodeConflict || !strings.Contains(apiErr.Message, "agent-a") {
		t.Fatalf("second claim = %v", err)
	}
	if err := e.svc.Release(e.ctx, b, "serial:lab/dut1"); code(err) != dto.CodeConflict {
		t.Errorf("b released a's claim: %v", err)
	}
	if _, renewed, err := e.svc.Claim(e.ctx, a, "serial:lab/dut1", 2*time.Minute, ""); err != nil || !renewed {
		t.Errorf("renew = %v %v", renewed, err)
	}
	if ok, _ := e.svc.Holds(e.ctx, a, "serial:lab/dut1"); !ok {
		t.Error("a does not hold its claim")
	}
	if err := e.svc.Release(e.ctx, a, "serial:lab/dut1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Claim(e.ctx, b, "serial:lab/dut1", time.Minute, ""); err != nil {
		t.Fatalf("b after release: %v", err)
	}
	// Expiry through the janitor.
	e.clk.Advance(61 * time.Second)
	if ok, _ := e.svc.Holds(e.ctx, b, "serial:lab/dut1"); ok {
		t.Error("an expired claim still holds")
	}
	if err := e.jan.Pass(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.svc.ExpireClaims(e.ctx); n != 0 {
		t.Errorf("expired twice: %d", n)
	}
	if _, _, err := e.svc.Claim(e.ctx, a, "serial:lab/dut1", time.Minute, ""); err != nil {
		t.Fatalf("a after expiry: %v", err)
	}
	// Expiry found lazily by the next claim.
	e.clk.Advance(61 * time.Second)
	if _, _, err := e.svc.Claim(e.ctx, b, "serial:lab/dut1", time.Minute, ""); err != nil {
		t.Fatalf("b after lazy expiry: %v", err)
	}
	res, err := e.st.Read(e.ctx, store.Query{Streams: []string{"serial:lab/dut1"}, Kinds: []store.Kind{store.KindMarker}})
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, m := range res.Entries {
		texts = append(texts, m.Text)
	}
	want := []string{
		"agent-a claimed serial:lab/dut1 for 1m0s (flashing)",
		"agent-a renewed the claim on serial:lab/dut1 for 2m0s",
		"agent-a released serial:lab/dut1",
		"agent-b claimed serial:lab/dut1 for 1m0s",
		"claim of agent-b on serial:lab/dut1 expired",
		"agent-a claimed serial:lab/dut1 for 1m0s",
		"claim of agent-a on serial:lab/dut1 expired",
		"agent-b claimed serial:lab/dut1 for 1m0s",
	}
	if !slices.Equal(texts, want) {
		t.Errorf("markers:\n%s\nwant:\n%s", strings.Join(texts, "\n"), strings.Join(want, "\n"))
	}
	if _, _, err := e.svc.Claim(e.ctx, a, "chan:lab/general", time.Minute, ""); code(err) != dto.CodeBadRequest {
		t.Errorf("claim on a message stream: %v", err)
	}
	claims, _ := e.svc.ActiveClaims(e.ctx)
	if claims["serial:lab/dut1"].Principal != "agent-b" {
		t.Errorf("active claims = %v", claims)
	}
}
