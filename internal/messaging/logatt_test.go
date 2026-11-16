package messaging

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

func logReq(stream string, from int64, to *int64) dto.LogRequest {
	return dto.LogRequest{Stream: stream, FromSeq: from, ToSeq: to}
}

func (e *env) lines(stream string, n int, sev func(i int) int) (first, last int64) {
	e.t.Helper()
	es := make([]store.Entry, n)
	for i := range es {
		es[i] = store.Entry{Stream: stream, Text: fmt.Sprintf("line %d", i+1), Severity: sev(i)}
	}
	last, err := e.w.CommitBatch(e.ctx, es)
	if err != nil {
		e.t.Fatal(err)
	}
	return last - int64(n) + 1, last
}

func info(int) int { return 6 }

func TestLogAttachmentValidation(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	first, last := e.lines("serial:dut1", 10, info)
	cases := []struct {
		name string
		r    dto.LogRequest
		code string
	}{
		{"neither", dto.LogRequest{FromSeq: first}, dto.CodeBadRequest},
		{"both", dto.LogRequest{Stream: "serial:dut1", Channel: "general", FromSeq: first}, dto.CodeBadRequest},
		{"zero from", logReq("serial:dut1", 0, nil), dto.CodeBadRequest},
		{"reversed", logReq("serial:dut1", last, ptr(first)), dto.CodeBadRequest},
		{"missing stream", logReq("serial:nope", first, nil), dto.CodeNotFound},
		{"message stream", logReq("chan:general", first, nil), dto.CodeBadRequest},
		{"bad grep", dto.LogRequest{Stream: "serial:dut1", FromSeq: first, Grep: "("}, dto.CodeInvalidPattern},
		{"bad severity", dto.LogRequest{Stream: "serial:dut1", FromSeq: first, MinSeverity: ptr(9)}, dto.CodeBadRequest},
		{"unknown channel", dto.LogRequest{Channel: "nope", FromSeq: first}, dto.CodeUnknownChannel},
		{"unbound channel", dto.LogRequest{Channel: "general", FromSeq: first}, dto.CodeBadRequest},
	}
	for _, c := range cases {
		_, err := e.svc.SendMessage(e.ctx, a, SendInput{SendRequest: dto.SendRequest{Text: "x", Logs: []dto.LogRequest{c.r}}})
		if got := code(err); got != c.code {
			t.Errorf("%s: %v, want %s", c.name, err, c.code)
		}
	}
	six := make([]dto.LogRequest, 6)
	for i := range six {
		six[i] = logReq("serial:dut1", first, nil)
	}
	if _, err := e.svc.SendMessage(e.ctx, a, SendInput{SendRequest: dto.SendRequest{Text: "x", Logs: six}}); code(err) != dto.CodeBadRequest {
		t.Errorf("6 attachments: %v", err)
	}
}

func TestLogAttachmentRangeLimit(t *testing.T) {
	defer func(n int) { MaxRangeLines = n }(MaxRangeLines)
	MaxRangeLines = 3000
	e := newEnv(t)
	a := e.agent("agent-a")
	first, _ := e.lines("serial:big", MaxRangeLines+1, info)
	if _, err := e.svc.SendMessage(e.ctx, a, SendInput{SendRequest: dto.SendRequest{Text: "x", Logs: []dto.LogRequest{logReq("serial:big", first, nil)}}}); code(err) != dto.CodeTooLarge {
		t.Errorf("one line over the limit: %v", err)
	}
	if _, err := e.svc.SendMessage(e.ctx, a, SendInput{SendRequest: dto.SendRequest{Text: "x", Logs: []dto.LogRequest{
		{Stream: "serial:big", FromSeq: first, Grep: "^line 1$"}}}}); err != nil {
		t.Errorf("grep narrows the range: %v", err)
	}
}

func TestLogAttachmentSnapshotThresholdAndExpiry(t *testing.T) {
	e := newEnv(t)
	a, b := e.agent("agent-a"), e.agent("agent-b")
	f73, l73 := e.lines("serial:dut1", 73, info)
	fbig, lbig := e.lines("serial:dut1", SnapshotMaxLines+1, info)
	m := e.send(a, SendInput{SendRequest: dto.SendRequest{Text: "look", To: "agent-b", Logs: []dto.LogRequest{
		logReq("serial:dut1", f73, ptr(l73)),
		logReq("serial:dut1", fbig, ptr(lbig)),
		logReq("serial:dut1", f73, nil),
	}}})
	small, big, open := m.Attachments[0], m.Attachments[1], m.Attachments[2]
	if small.Lines != 73 || small.SnapshotSHA256 == "" || !slices.Equal(small.Streams, []string{"serial:dut1"}) || small.FirstTS == 0 {
		t.Errorf("small = %+v", small)
	}
	if big.Lines != SnapshotMaxLines+1 || big.SnapshotSHA256 != "" {
		t.Errorf("big = %+v", big)
	}
	if open.SnapshotSHA256 != "" || open.ToSeq != nil {
		t.Errorf("open-ended = %+v", open)
	}
	d, err := e.svc.EntryDTO(e.ctx, b, m)
	if err != nil {
		t.Fatal(err)
	}
	la := d.Attachments[0].Log
	if !la.Snapshot || la.Expired || len(la.Preview) != 5 || la.Preview[0].Text != "line 1" {
		t.Errorf("dto = %+v", la)
	}

	// A big snapshot by bytes: few lines but over 256 KiB.
	long := make([]store.Entry, 200)
	for i := range long {
		long[i] = store.Entry{Stream: "serial:wide", Text: fmt.Sprintf("%d %s", i, strings.Repeat("x", 1900)), Severity: 6}
	}
	lw, _ := e.w.CommitBatch(e.ctx, long)
	wide := e.send(a, SendInput{SendRequest: dto.SendRequest{Text: "wide", Logs: []dto.LogRequest{logReq("serial:wide", lw-199, ptr(lw))}}})
	if wide.Attachments[0].SnapshotSHA256 != "" {
		t.Error("a 380 KB range was snapshotted")
	}

	// Retention eats the stream's lines: the snapshot survives, the
	// references expire.
	e.clk.Advance(15 * 24 * time.Hour)
	if err := e.jan.Pass(e.ctx); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.st.Read(e.ctx, store.Query{Streams: []string{"serial:dut1"}}); len(res.Entries) != 0 {
		t.Fatalf("retention left %d lines", len(res.Entries))
	}
	page, _, err := e.svc.AttachmentLines(e.ctx, b, m.Seq, 0, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !page.Snapshot || len(page.Entries) != 73 || page.Entries[72].Text != "line 73" || page.Entries[0].Seq != f73 || page.HasMore {
		t.Errorf("snapshot page: %d entries snapshot=%v more=%v", len(page.Entries), page.Snapshot, page.HasMore)
	}
	part, _, _ := e.svc.AttachmentLines(e.ctx, b, m.Seq, 0, f73+70, 2)
	if len(part.Entries) != 2 || part.Entries[0].Seq != f73+70 || !part.HasMore || part.NextSeq != f73+72 {
		t.Errorf("snapshot paging = %+v", part)
	}
	d, _ = e.svc.EntryDTO(e.ctx, b, m)
	if d.Attachments[0].Log.Expired || !d.Attachments[1].Log.Expired || !d.Attachments[2].Log.Expired {
		t.Errorf("expired = %v %v %v", d.Attachments[0].Log.Expired, d.Attachments[1].Log.Expired, d.Attachments[2].Log.Expired)
	}
	page, _, _ = e.svc.AttachmentLines(e.ctx, b, m.Seq, 1, 0, 10)
	if !page.Expired || len(page.Entries) != 0 {
		t.Errorf("expired page = %+v", page)
	}
	// The snapshot blob is referenced, so the blob janitor keeps it.
	e.clk.Advance(2 * time.Hour)
	if err := e.jan.Pass(e.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Blobs().Stat(e.ctx, small.SnapshotSHA256); err != nil {
		t.Errorf("snapshot blob: %v", err)
	}

	// Visibility follows the message: a DM's attachment is hidden from a
	// third party.
	c := e.agent("agent-c")
	if _, _, err := e.svc.AttachmentLines(e.ctx, c, m.Seq, 0, 0, 10); code(err) != dto.CodeNotFound {
		t.Errorf("third party read the DM attachment: %v", err)
	}
	if ok, _ := e.svc.Blobs().VisibleTo(e.ctx, small.SnapshotSHA256, c); ok {
		t.Error("third party may fetch the DM's snapshot blob")
	}
	if ok, _ := e.svc.Blobs().VisibleTo(e.ctx, small.SnapshotSHA256, b); !ok {
		t.Error("the recipient may not fetch the snapshot blob")
	}
}

func TestLogAttachmentChannelFormGrepAndSeverity(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	sev := func(i int) int {
		if i%10 == 0 {
			return 3
		}
		return 6
	}
	first, last := e.lines("serial:dut1", 50, sev)
	e.lines("netconsole:dut1", 5, info)
	if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "dut1", Patterns: []string{"serial:dut1"}}); err != nil {
		t.Fatal(err)
	}
	m := e.send(a, SendInput{SendRequest: dto.SendRequest{Text: "errors", Logs: []dto.LogRequest{
		{Channel: "dut1", FromSeq: first, ToSeq: ptr(last + 5), MinSeverity: ptr(3), Grep: "line [0-9]1$", Title: "errs"},
	}}})
	att := m.Attachments[0]
	if att.Channel != "dut1" || !slices.Equal(att.Streams, []string{"serial:dut1"}) || att.Lines != 4 || att.Grep == "" || *att.MinSeverity != 3 {
		t.Fatalf("attachment = %+v", att)
	}
	// A later binding change does not alter what was attached.
	if _, err := e.svc.UpdateChannel(e.ctx, a, "dut1", dto.ChannelUpdate{AddPatterns: []string{"netconsole:dut1"}}); err != nil {
		t.Fatal(err)
	}
	got, _ := e.st.Get(e.ctx, m.Seq)
	if !slices.Equal(got.Attachments[0].Streams, []string{"serial:dut1"}) {
		t.Errorf("streams changed: %v", got.Attachments[0].Streams)
	}
	page, _, err := e.svc.AttachmentLines(e.ctx, a, m.Seq, 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, x := range page.Entries {
		texts = append(texts, x.Text)
		if x.Severity != 3 {
			t.Errorf("severity %d kept", x.Severity)
		}
	}
	if !slices.Equal(texts, []string{"line 11", "line 21", "line 31", "line 41"}) {
		t.Errorf("filtered lines = %v", texts)
	}
	text, _, err := e.svc.toolGetMessage(e.ctx, call(a), getMessageIn{Seq: m.Seq})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `grep "line [0-9]1$"`) || !strings.Contains(text, "min_severity 3") || !strings.Contains(text, "line 41") {
		t.Errorf("get_message text:\n%s", text)
	}
}

func TestOpenEndedAttachmentContinuesLive(t *testing.T) {
	e := newEnv(t)
	a, b := e.agent("agent-a"), e.agent("agent-b")
	first, _ := e.lines("serial:dut1", 3, info)
	m := e.send(a, SendInput{SendRequest: dto.SendRequest{Text: "watch this", To: "agent-b", Logs: []dto.LogRequest{logReq("serial:dut1", first, nil)}}})
	text, out, err := e.svc.toolGetLog(e.ctx, call(b), getLogIn{Seq: m.Seq})
	if err != nil || out.Shown != 3 || out.HasMore {
		t.Fatalf("first page = %q %+v %v", text, out, err)
	}
	e.lines("serial:dut1", 2, info)
	text, out2, err := e.svc.toolGetLog(e.ctx, call(b), getLogIn{Seq: m.Seq, SinceSeq: out.NextSeq})
	if err != nil || out2.Shown != 2 || !strings.Contains(text, "line 2") {
		t.Errorf("continued = %q %+v %v", text, out2, err)
	}
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 99, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSendImageAndGetMessageReturnsTheAgentVariant(t *testing.T) {
	e := newEnv(t)
	a, b := e.agent("agent-a"), e.agent("agent-b")
	data := base64.StdEncoding.EncodeToString(pngBytes(t, 2000, 1000))
	_, out, err := e.svc.toolSend(e.ctx, call(a), sendIn{Text: "the bench", To: "agent-b", Images: []dto.ImageUpload{{Data: data, Mime: "image/png"}}})
	if err != nil {
		t.Fatal(err)
	}
	text, gm, err := e.svc.toolGetMessage(e.ctx, call(b), getMessageIn{Seq: out.Seq})
	if err != nil {
		t.Fatal(err)
	}
	if len(gm.images) != 1 {
		t.Fatalf("images = %d\n%s", len(gm.images), text)
	}
	ic := gm.ExtraContent()[0].(*mcp.ImageContent)
	img, _, err := image.Decode(bytes.NewReader(ic.Data))
	if err != nil || ic.MIMEType != "image/jpeg" || img.Bounds().Dx() != 1568 || img.Bounds().Dy() != 784 {
		t.Errorf("agent variant = %s %v %v", ic.MIMEType, img.Bounds(), err)
	}
	att := gm.Entry.Attachments[0].Image
	if att.Width != 2000 || att.Height != 1000 || att.ThumbURL == "" || !att.Preview || att.URL != "/blob/"+att.SHA256 {
		t.Errorf("attachment = %+v", att)
	}
	if inboxed := inbox(t, e, b); len(inboxed) != 0 {
		t.Errorf("get_message did not ack: %v", inboxed)
	}
	if ok, _ := e.svc.Blobs().VisibleTo(e.ctx, att.SHA256, e.agent("agent-c")); ok {
		t.Error("a third party can fetch a DM's image")
	}
	seven := make([]dto.ImageUpload, 7)
	for i := range seven {
		seven[i] = dto.ImageUpload{Data: data, Mime: "image/png"}
	}
	if _, _, err := e.svc.toolSend(e.ctx, call(a), sendIn{Text: "many", Images: seven}); code(err) != dto.CodeTooLarge {
		t.Errorf("7 images: %v", err)
	}
	if _, _, err := e.svc.toolSend(e.ctx, call(a), sendIn{Text: "bad", Images: []dto.ImageUpload{{Data: "aGVsbG8=", Mime: "image/png"}}}); code(err) != dto.CodeBadRequest {
		t.Errorf("not an image: %v", err)
	}
}
