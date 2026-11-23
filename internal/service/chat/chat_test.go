package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cafard/internal/blobs"
	"cafard/internal/service/access"
	"cafard/internal/store"
)

var ctx = context.Background()

type world struct {
	st                         *store.Store
	svc                        *Service
	guillaume, devCode, device access.Who
	labBench                   access.Who
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "cafard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bl, err := blobs.Open(filepath.Join(dir, "files"))
	if err != nil {
		t.Fatal(err)
	}
	acc := access.New(st)
	g, err := acc.CreateIdentity(ctx, access.Admin("test"), access.NewIdentity{Kind: store.Person, Name: "guillaume"})
	if err != nil {
		t.Fatal(err)
	}
	w := &world{st: st, svc: New(st, bl, acc), guillaume: access.Who{Identity: g, Origin: "test"}}
	mk := func(name string, kind store.Kind) access.Who {
		i, err := acc.CreateIdentity(ctx, access.Admin("test"), access.NewIdentity{Kind: kind, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return access.Who{Identity: i, Origin: "test"}
	}
	w.devCode, w.device, w.labBench = mk("dev-code", store.Agent), mk("dev-build", store.Device), mk("lab-bench", store.Agent)
	be98, err := acc.CreateProject(ctx, access.Admin("test"), "be98", 30, 5e9)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acc.CreateProject(ctx, access.Admin("test"), "zeeos", 30, 5e9); err != nil {
		t.Fatal(err)
	}
	for _, m := range []access.Who{w.guillaume, w.devCode, w.device} {
		if err := acc.AddMember(ctx, access.Admin("test"), be98, m.Identity); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func readAll(t *testing.T, r io.ReadCloser) string {
	t.Helper()
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A device sends a file to a conversation that does not exist yet; an agent
// in the project reads it back.
func TestDeviceSendsAgentReads(t *testing.T) {
	w := newWorld(t)
	log := "[  212.481130] wl0: dhd_bus_rxctl: resumed on timeout\n[  212.481502] dhd_check_hang: Event HANG\n"
	sent, err := w.svc.SendFile(ctx, w.device, "be98", "wifi-drops", "boot.log", "", "", strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	if !sent.Created || sent.Conversation.Title != "Wifi drops" {
		t.Errorf("new conversation: %+v", sent.Conversation)
	}
	f := sent.Message.Files[0]
	if f.State != store.Stored || f.Size != int64(len(log)) || !strings.HasPrefix(f.ContentType, "text/plain") {
		t.Errorf("file: %+v", f)
	}

	_, ms, err := w.svc.Messages(ctx, w.devCode, "be98", "wifi-drops", 0, 0)
	if err != nil || len(ms) != 1 || ms[0].Author.Name != "dev-build" || len(ms[0].Files) != 1 {
		t.Fatalf("messages: %+v %v", ms, err)
	}
	_, r, err := w.svc.File(ctx, w.devCode, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r); got != log {
		t.Errorf("read back %q", got)
	}

	if _, err := w.svc.SendText(ctx, w.devCode, "be98", "wifi-drops", "Found it at line 1."); err != nil {
		t.Fatal(err)
	}
	cs, err := w.svc.Conversations(ctx, w.guillaume, "be98")
	if err != nil || len(cs) != 1 || cs[0].Messages != 2 {
		t.Errorf("conversations: %+v %v", cs, err)
	}
}

func TestWhoMayReadAndSend(t *testing.T) {
	w := newWorld(t)
	sent, err := w.svc.SendText(ctx, w.guillaume, "be98", "x", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Messages(ctx, w.device, "be98", "x", 0, 0); !access.Is(err, access.SendOnly) {
		t.Errorf("a device read messages: %v", err)
	}
	if _, err := w.svc.SendText(ctx, w.labBench, "be98", "x", "hi"); !access.Is(err, access.NotMember) {
		t.Errorf("a non-member sent: %v", err)
	}
	if _, _, err := w.svc.Messages(ctx, w.labBench, "be98", "x", 0, 0); !access.Is(err, access.NotMember) {
		t.Errorf("a non-member read: %v", err)
	}
	if _, err := w.svc.SendText(ctx, w.guillaume, "nope", "x", "hi"); !access.Is(err, access.NotFound) {
		t.Errorf("an unknown project: %v", err)
	}
	if _, err := w.svc.SendText(ctx, w.guillaume, "be98", "Bad Name", "hi"); !access.Is(err, access.BadName) {
		t.Errorf("a bad conversation name: %v", err)
	}
	if _, err := w.svc.SendText(ctx, w.guillaume, "be98", "x", "   "); !access.Is(err, access.BadValue) {
		t.Errorf("an empty message: %v", err)
	}
	if _, err := w.svc.SendText(ctx, w.guillaume, "be98", "x", strings.Repeat("a", MaxText+1)); !access.Is(err, access.BadValue) {
		t.Errorf("a too long message: %v", err)
	}
	if _, err := w.svc.SendFile(ctx, w.device, "be98", "x", "-", "", "", strings.NewReader("x")); !access.Is(err, access.BadName) {
		t.Errorf("a file named -: %v", err)
	}

	// A file id says nothing about a project the reader is not in.
	f, _ := w.svc.SendFile(ctx, w.device, "be98", "x", "a.txt", "", "", strings.NewReader("secret"))
	if _, _, err := w.svc.File(ctx, w.labBench, f.Message.Files[0].ID); !access.Is(err, access.NotFound) {
		t.Errorf("a non-member opened a file: %v", err)
	}
	if ps, err := w.svc.Projects(ctx, w.devCode); err != nil || len(ps) != 1 || ps[0].Name != "be98" {
		t.Errorf("dev-code's projects: %+v %v", ps, err)
	}
	if _, err := w.svc.Projects(ctx, w.device); !access.Is(err, access.SendOnly) {
		t.Errorf("a device listed projects: %v", err)
	}
	_ = sent
}

// slowBody hands out its lines one at a time, waiting for the test between
// them, the way `logread -f | curl -T -` would.
type slowBody struct {
	lines []string
	next  chan struct{}
}

func (b *slowBody) Read(p []byte) (int, error) {
	if len(b.lines) == 0 {
		return 0, io.EOF
	}
	<-b.next
	n := copy(p, b.lines[0])
	b.lines = b.lines[1:]
	return n, nil
}

func TestLiveFileIsReadableWhileItArrives(t *testing.T) {
	w := newWorld(t)
	body := &slowBody{lines: []string{"line 1\n", "line 2\n", "line 3\n"}, next: make(chan struct{})}
	done := make(chan Sent, 1)
	go func() {
		s, err := w.svc.SendFile(ctx, w.device, "be98", "serial", "console.log", "", "", body)
		if err != nil {
			t.Error(err)
		}
		done <- s
	}()

	body.next <- struct{}{} // line 1 arrives
	var fileID int64
	deadline := time.Now().Add(5 * time.Second)
	for fileID == 0 && time.Now().Before(deadline) {
		_, ms, _ := w.svc.Messages(ctx, w.devCode, "be98", "serial", 0, 0)
		if len(ms) == 1 {
			fileID = ms[0].Files[0].ID
			if ms[0].Files[0].State != store.Live {
				t.Errorf("state while arriving: %s", ms[0].Files[0].State)
			}
		}
	}
	if fileID == 0 {
		t.Fatal("the message never appeared while the file was arriving")
	}
	var page LinesPage
	for time.Now().Before(deadline) {
		var err error
		page, err = w.svc.Lines(ctx, w.devCode, fileID, LinesQuery{})
		if err == nil && len(page.Lines) == 1 {
			break
		}
	}
	if len(page.Lines) != 1 || page.Lines[0].Text != "line 1" {
		t.Errorf("while live: %+v", page)
	}

	body.next <- struct{}{}
	body.next <- struct{}{}
	sent := <-done
	f := sent.Message.Files[0]
	if f.State != store.Stored || f.Size != 21 {
		t.Errorf("after the upload: %+v", f)
	}
	page, err := w.svc.Lines(ctx, w.devCode, fileID, LinesQuery{})
	if err != nil || page.Total != 3 || page.Lines[2].Text != "line 3" {
		t.Errorf("after the upload: %+v %v", page, err)
	}
}

type brokenBody struct{ sent bool }

func (b *brokenBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, errors.New("connection reset by peer")
	}
	b.sent = true
	return copy(p, "the part that arrived\n"), nil
}

// Half a log is still the log up to where it broke: kept, and the error
// still reported.
func TestCutUploadKeepsWhatArrived(t *testing.T) {
	w := newWorld(t)
	sent, err := w.svc.SendFile(ctx, w.device, "be98", "x", "half.log", "", "", &brokenBody{})
	if err == nil {
		t.Error("the broken upload reported no error")
	}
	f := sent.Message.Files[0]
	if f.State != store.Stored || f.Size != 22 {
		t.Fatalf("file: %+v", f)
	}
	page, _ := w.svc.Lines(ctx, w.devCode, f.ID, LinesQuery{})
	if len(page.Lines) != 1 || page.Lines[0].Text != "the part that arrived" {
		t.Errorf("lines: %+v", page)
	}
}

func TestUploadsLiveAtARestartAreCut(t *testing.T) {
	w := newWorld(t)
	c, _, _ := w.st.EnsureConversation(ctx, 1, "x", "x", w.device.ID)
	m, _ := w.st.AddMessage(ctx, c.ID, w.device.ID, "", []store.NewFile{{Name: "a.log", ContentType: "text/plain"}})
	if n, err := w.svc.Start(ctx); err != nil || n != 1 {
		t.Fatalf("start: %d %v", n, err)
	}
	if _, _, err := w.svc.File(ctx, w.devCode, m.Files[0].ID); !access.Is(err, Gone) {
		t.Errorf("a cut file: %v", err)
	}
}

func TestDetectType(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	for _, tc := range []struct {
		name, declared string
		head           []byte
		want           string
	}{
		{"IMG_4821.jpg", "", nil, "image/jpeg"},
		{"screenshot", "", png, "image/png"},
		{"boot.log", "", []byte("[ 0.0] Booting Linux\n"), "text/plain; charset=utf-8"},
		{"boot.log", "application/x-www-form-urlencoded", []byte("hello"), "text/plain; charset=utf-8"},
		{"firmware.bin", "", []byte{0, 1, 2, 0xff, 0xfe}, "application/octet-stream"},
	} {
		if got := detectType(tc.name, tc.declared, tc.head); got != tc.want {
			t.Errorf("detectType(%q, %q) = %q, want %q", tc.name, tc.declared, got, tc.want)
		}
	}
}

func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestReadLines(t *testing.T) {
	text := numbered(1000)
	p, _ := ReadLines(strings.NewReader(text), LinesQuery{From: 10, Count: 5})
	if len(p.Lines) != 5 || p.Lines[0].N != 10 || p.Lines[0].Text != "line 10" || p.Total != 1000 || p.Next != 15 {
		t.Errorf("range: %+v", p)
	}
	p, _ = ReadLines(strings.NewReader(text), LinesQuery{})
	if len(p.Lines) != MaxLines || p.Next != MaxLines+1 {
		t.Errorf("default page: %d lines, next %d", len(p.Lines), p.Next)
	}
	p, _ = ReadLines(strings.NewReader(text), LinesQuery{From: 998})
	if len(p.Lines) != 3 || p.Next != 0 {
		t.Errorf("last page: %+v", p)
	}
	p, _ = ReadLines(strings.NewReader(text), LinesQuery{Tail: 3})
	if len(p.Lines) != 3 || p.Lines[0].N != 998 || p.Lines[2].Text != "line 1000" {
		t.Errorf("tail: %+v", p)
	}
	p, _ = ReadLines(strings.NewReader("a\nb\n"), LinesQuery{Tail: 10})
	if len(p.Lines) != 2 || p.Total != 2 {
		t.Errorf("tail longer than the file: %+v", p)
	}
}

func TestGrep(t *testing.T) {
	log := "boot\nok\nwl0: link UP\nok\nok\nwl0: dhd_bus_rxctl: resumed on TIMEOUT\nHANG\nok\n"
	p, _ := ReadLines(strings.NewReader(log), LinesQuery{Grep: "timeout", Context: 1})
	var got []int
	for _, l := range p.Lines {
		got = append(got, l.N)
	}
	if fmt.Sprint(got) != "[5 6 7]" || p.Matches != 1 {
		t.Errorf("grep timeout with context 1: lines %v, matches %d", got, p.Matches)
	}
	p, _ = ReadLines(strings.NewReader(log), LinesQuery{Grep: "wl0", Context: 2})
	got = got[:0]
	for _, l := range p.Lines {
		got = append(got, l.N)
	}
	// Overlapping context is listed once, in order.
	if fmt.Sprint(got) != "[1 2 3 4 5 6 7 8]" || p.Matches != 2 {
		t.Errorf("grep wl0 with context 2: lines %v, matches %d", got, p.Matches)
	}
	p, _ = ReadLines(strings.NewReader(numbered(1000)), LinesQuery{Grep: "line", Count: 10})
	if len(p.Lines) != 10 || p.Matches != 1000 || p.Next != 11 {
		t.Errorf("a grep matching more than a page: %d lines, matches %d, next %d", len(p.Lines), p.Matches, p.Next)
	}
}

func TestLongAndBrokenLines(t *testing.T) {
	long := strings.Repeat("x", MaxLineBytes*3)
	p, _ := ReadLines(strings.NewReader("short\n"+long+"\nafter\r\n\xff\xfebad\n"), LinesQuery{})
	if len(p.Lines) != 4 {
		t.Fatalf("lines: %+v", p)
	}
	if !p.Lines[1].Cut || len(p.Lines[1].Text) != MaxLineBytes {
		t.Errorf("a long line: cut %v, %d bytes", p.Lines[1].Cut, len(p.Lines[1].Text))
	}
	if p.Lines[2].Text != "after" {
		t.Errorf("CRLF: %q", p.Lines[2].Text)
	}
	// One replacement character per run of invalid bytes.
	if p.Lines[3].Text != "�bad" {
		t.Errorf("invalid UTF-8: %q", p.Lines[3].Text)
	}
}
