package netconsole

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/ingest/ingestutil/ingesttest"
)

func TestParseExtended(t *testing.T) {
	tests := []struct {
		in   string
		ok   bool
		want Record
	}{
		{"6,416,1758426,-;hello\n", true, Record{Level: 6, Seq: 416, TSUsec: 1758426, Flags: "-", Body: []byte("hello\n")}},
		{"3,1,2,c;cont", true, Record{Level: 3, Seq: 1, TSUsec: 2, Flags: "c", Body: []byte("cont")}},
		{"14,5,6,-,caller=T42;user msg", true, Record{Level: 6, Facility: 1, Seq: 5, TSUsec: 6, Flags: "-", Fields: map[string]string{"caller": "T42"}, Body: []byte("user msg")}},
		{"6,416,1758426,-,ncfrag=0/31;the first chunk,", true, Record{Level: 6, Seq: 416, TSUsec: 1758426, Flags: "-", Frag: &Fragment{0, 31}, Body: []byte("the first chunk,")}},
		{"6,416,1758426,-,ncfrag=40/31;bad frag ignored", true, Record{Level: 6, Seq: 416, TSUsec: 1758426, Flags: "-", Body: []byte("bad frag ignored")}},
		{"6,1,2,;no flags", false, Record{}},
		{"x,1,2,-;not numeric", false, Record{}},
		{"6,1;too few", false, Record{}},
		{"[   1.000] plain; with semicolon", false, Record{}},
		{"plain line", false, Record{}},
		{"6,1,2,-", false, Record{}},
	}
	for _, tt := range tests {
		got, ok := ParseExtended([]byte(tt.in))
		if ok != tt.ok || (ok && !reflect.DeepEqual(got, tt.want)) {
			t.Errorf("ParseExtended(%q) = %+v, %v; want %+v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestSplitBody(t *testing.T) {
	text, dict := SplitBody([]byte("link down\n SUBSYSTEM=net\n DEVICE=n2\nnot a dict line"))
	if string(text) != "link down\nnot a dict line" || !reflect.DeepEqual(dict, map[string]string{"SUBSYSTEM": "net", "DEVICE": "n2"}) {
		t.Errorf("text %q dict %v", text, dict)
	}
}

var (
	ip1 = netip.MustParseAddr("10.0.50.42")
	ip2 = netip.MustParseAddr("10.0.50.43")
	ip3 = netip.MustParseAddr("192.0.2.1")
)

// newServer routes ip1 to the sink dut1 and the rest of its /24 to bench.
func newServer(t testing.TB, sinks ...string) (*Server, *ingesttest.Writer, *ingesttest.Status) {
	t.Helper()
	w, st := ingesttest.NewWriter(), ingesttest.NewStatus()
	if len(sinks) == 0 {
		sinks = []string{"lab/dut1=10.0.50.42", "lab/bench=10.0.50.0/24"}
	}
	d := ingesttest.Deps(w, st)
	d.Projects = ingesttest.NewSinks(t, sinks...).Service
	return New(d), w, st
}

func TestHandle(t *testing.T) {
	s, w, st := newServer(t)
	ctx := context.Background()
	now := time.Now()
	s.Handle(ctx, []byte("3,812,90123456,-;eth0: link down\n SUBSYSTEM=net\n"), ip1, now)
	// pr_cont: one line over two plain datagrams, then a prompt left open.
	s.Handle(ctx, []byte("[    1.0] Booting "), ip2, now)
	s.Handle(ctx, []byte("Linux\nlogin: "), ip2, now)
	// A fragmented extended message, out of order.
	s.Handle(ctx, []byte("4,9,10,-,ncfrag=11/22;second half"), ip1, now)
	s.Handle(ctx, []byte("4,9,10,-,ncfrag=0/22;first half "), ip1, now)

	es := w.Entries()
	if len(es) != 3 {
		t.Fatalf("entries %+v", es)
	}
	if es[0].Stream != "netconsole:lab/dut1" || es[0].Severity != 3 || es[0].Text != "eth0: link down" || es[0].App != "kernel" {
		t.Errorf("extended %+v", es[0])
	}
	var meta map[string]any
	_ = json.Unmarshal(es[0].Meta, &meta)
	if meta["seq"] != float64(812) || meta["ts_usec"] != float64(90123456) || meta["dict"].(map[string]any)["SUBSYSTEM"] != "net" || meta["source_ip"] != "10.0.50.42" {
		t.Errorf("meta %s", es[0].Meta)
	}
	if es[1].Stream != "netconsole:lab/bench" || es[1].Host != "10.0.50.43" || es[1].Text != "[    1.0] Booting Linux" || es[1].Severity != 6 {
		t.Errorf("plain %+v", es[1])
	}
	if es[2].Text != "first half second half" || es[2].Severity != 4 {
		t.Errorf("fragments %+v", es[2])
	}

	s.tick(ctx, now.Add(time.Second))
	es = w.Entries()
	if len(es) != 4 || es[3].Text != "login: " {
		t.Fatalf("partial line not flushed: %+v", es)
	}

	// A message whose last fragment never comes is stored after a while.
	s.Handle(ctx, []byte("6,10,11,-,ncfrag=0/30;only the start"), ip1, now)
	s.tick(ctx, now.Add(time.Second))
	if len(w.Entries()) != 4 {
		t.Fatal("incomplete message stored too early")
	}
	s.tick(ctx, now.Add(3*time.Second))
	if es := w.Entries(); len(es) != 5 || es[4].Text != "only the start" {
		t.Fatalf("stale fragment: %+v", es)
	}
	st.Get(func(s *ingesttest.Status) {
		if s.Declared["netconsole:lab/dut1"] != "udp" {
			t.Errorf("declared %v", s.Declared)
		}
	})
}

func TestDropsAreCounted(t *testing.T) {
	s, w, st := newServer(t)
	w.SetFull(true)
	for range 3 {
		s.Handle(context.Background(), []byte("6,1,1,-;x"), ip1, time.Now())
	}
	st.Get(func(s *ingesttest.Status) {
		if s.Drops["netconsole:lab/dut1"] != 3 {
			t.Errorf("drops %v", s.Drops)
		}
	})
}

func TestSendersNoSinkListsAreDropped(t *testing.T) {
	s, w, st := newServer(t)
	s.Handle(context.Background(), []byte("6,1,1,-;who is this"), ip3, time.Now())
	s.Handle(context.Background(), []byte("a partial line"), ip3, time.Now())
	s.tick(context.Background(), time.Now().Add(time.Minute))
	if es := w.Entries(); len(es) != 0 {
		t.Fatalf("entries from an unlisted sender: %+v", es)
	}
	st.Get(func(s *ingesttest.Status) {
		if s.Unrouteds["netconsole"] != 2 {
			t.Errorf("unrouted %v", s.Unrouteds)
		}
	})
}

func TestListener(t *testing.T) {
	s, w, _ := newServer(t, "lab/vm=127.0.0.1")
	bound := make(chan net.Addr, 1)
	s.Bound = func(a net.Addr) { bound <- a }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, "127.0.0.1:0") }()
	addr := <-bound
	c, err := net.Dial("udp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("6,1,1,-;hello"))
	c.Write([]byte("partial without newline"))
	c.Close()
	if es := w.Wait(2, 5*time.Second); len(es) != 2 || es[0].Stream != "netconsole:lab/vm" || es[1].Text != "partial without newline" {
		t.Fatalf("entries %+v", es)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestConfig(t *testing.T) {
	var c Config
	c.SetDefaults()
	if c.UDP != ":6666" || c.Validate() != nil {
		t.Fatalf("defaults %+v", c)
	}
	c.Names = map[string]string{"10.0.50.42": "dut1", "fe80::1": "dut2"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, names := range []map[string]string{{"host": "x"}, {"10.0.0.1": "has space"}, {"10.0.0.1": ""}, {"10.0.0.1": "glob*"}} {
		if (&Config{Names: names}).Validate() == nil {
			t.Errorf("%v validated", names)
		}
	}
}

func FuzzHandle(f *testing.F) {
	f.Add([]byte("6,416,1758426,-;hello\n KEY=v\n"))
	f.Add([]byte("4,9,10,-,ncfrag=11/22;second half"))
	f.Add([]byte("plain\rline\n"))
	s, w, _ := newServer(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		s.Handle(context.Background(), b, ip1, time.Now())
		s.flushAll(context.Background())
		for _, e := range w.Entries() {
			if strings.ContainsAny(e.Text, "\r\n") || e.Severity < 0 || e.Severity > 7 || len(e.Meta) > 4096 {
				t.Fatalf("entry %+v", e)
			}
		}
	})
}
