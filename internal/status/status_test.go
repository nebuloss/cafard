package status

import (
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/dto"
)

var t0 = time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

func TestStatesFollowActivityAndConnection(t *testing.T) {
	c := clock.NewFake(t0)
	m := New(c)
	m.Declare(Source{Stream: "serial:dut1", Transport: "local"})
	if s, _ := get(m, "serial:dut1"); s.Status != dto.StreamIdle || s.Transport != "local" {
		t.Errorf("declared = %+v", s)
	}
	m.Seen("serial:dut1", t0)
	if s, _ := get(m, "serial:dut1"); s.Status != dto.StreamConnected || s.LastActivity == nil {
		t.Errorf("after a line = %+v", s)
	}
	c.Advance(IdleAfter + time.Second)
	if s, _ := get(m, "serial:dut1"); s.Status != dto.StreamIdle {
		t.Errorf("after silence = %s", s.Status)
	}
	m.SetConnected("serial:dut1", false, "unplugged")
	if s, _ := get(m, "serial:dut1"); s.Status != dto.StreamDown {
		t.Errorf("after unplug = %s", s.Status)
	}
	m.SetConnected("serial:dut1", true, "")
	m.Seen("serial:dut1", c.Now())
	if s, _ := get(m, "serial:dut1"); s.Status != dto.StreamConnected {
		t.Errorf("after replug = %s", s.Status)
	}
}

func TestRateIsLinesPerMinuteOverFiveMinutes(t *testing.T) {
	c := clock.NewFake(t0)
	m := New(c)
	for range 50 {
		m.Seen("push:a", c.Now())
	}
	c.Advance(time.Minute)
	for range 50 {
		m.Seen("push:a", c.Now())
	}
	if s, _ := get(m, "push:a"); s.RatePerMin != 20 {
		t.Errorf("rate = %v, want 20", s.RatePerMin)
	}
	c.Advance(5 * time.Minute)
	if s, _ := get(m, "push:a"); s.RatePerMin != 0 {
		t.Errorf("rate after the window = %v", s.RatePerMin)
	}
}

func TestMessagesAreNotSources(t *testing.T) {
	m := New(clock.NewFake(t0))
	m.Seen("chan:general", t0)
	m.Declare(Source{Stream: "dm:a~b"})
	if len(m.Snapshot()) != 0 {
		t.Errorf("snapshot = %+v", m.Snapshot())
	}
}

func TestWatchIsThrottledPerStream(t *testing.T) {
	c := clock.NewFake(t0)
	m := New(c)
	ch, cancel := m.Watch(10)
	defer cancel()
	m.Dropped("syslog:x", 1)
	m.Dropped("syslog:x", 1)
	m.Dropped("syslog:x", 1)
	if got := len(ch); got != 1 {
		t.Fatalf("%d notifications at once, want 1", got)
	}
	<-ch
	c.Advance(3 * time.Second)
	m.Tick()
	select {
	case s := <-ch:
		if s.Dropped != 3 {
			t.Errorf("flushed dropped = %d", s.Dropped)
		}
	default:
		t.Fatal("pending change was not flushed")
	}
	m.Seen("syslog:y", c.Now())
	c.Advance(IdleAfter + time.Second)
	<-ch
	m.Tick()
	select {
	case s := <-ch:
		if s.ID != "syslog:y" || s.Status != dto.StreamIdle {
			t.Errorf("idle notification = %+v", s)
		}
	default:
		t.Fatal("going idle was not notified")
	}
}

func get(m *Memory, id string) (dto.Stream, bool) {
	for _, s := range m.Snapshot() {
		if s.ID == id {
			return s, true
		}
	}
	return dto.Stream{}, false
}
