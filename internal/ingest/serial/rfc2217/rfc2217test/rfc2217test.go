// Package rfc2217test is a fake RFC 2217 server (ser2net) for tests.
package rfc2217test

import (
	"net"
	"slices"
	"sync"
	"testing"

	"github.com/nebuloss/cafard/internal/ingest/serial/rfc2217"
)

// Peer is the far end of an RFC 2217 link. It accepts every Telnet
// option, answers every COM-PORT-OPTION command, and records the data,
// SET-CONTROL values and SET-BAUDRATE argument it receives. The zero
// value is ready; set the options before serving.
type Peer struct {
	// Refuse answers DONT to WILL COM-PORT-OPTION, as a plain Telnet
	// server does.
	Refuse bool
	// Raw makes it a raw TCP serial server: every byte is data.
	Raw bool

	mu      sync.Mutex
	data    []byte
	control []byte
	baud    []byte
}

// Serve reads c until it fails. Answers go out from another goroutine,
// in order, so a synchronous net.Pipe does not block both ends.
func (p *Peer) Serve(c net.Conn) {
	out := newQueue(c)
	defer out.close()
	var (
		sub   []byte
		verb  byte
		state int // 0 data, 1 IAC, 2 verb, 3 SB, 4 SB IAC
	)
	buf := make([]byte, 512)
	for {
		n, err := c.Read(buf)
		p.mu.Lock()
		for _, b := range buf[:n] {
			if p.Raw {
				p.data = append(p.data, b)
				continue
			}
			switch state {
			case 0:
				if b == rfc2217.IAC {
					state = 1
				} else {
					p.data = append(p.data, b)
				}
			case 1:
				switch b {
				case rfc2217.IAC:
					p.data, state = append(p.data, b), 0
				case rfc2217.WILL, rfc2217.DO, rfc2217.WONT, rfc2217.DONT:
					verb, state = b, 2
				case rfc2217.SB:
					sub, state = nil, 3
				default:
					state = 0
				}
			case 2:
				state = 0
				switch {
				case verb == rfc2217.WILL && b == rfc2217.OptComPort && p.Refuse:
					out.send(rfc2217.IAC, rfc2217.DONT, b)
				case verb == rfc2217.WILL:
					out.send(rfc2217.IAC, rfc2217.DO, b)
				case verb == rfc2217.DO:
					out.send(rfc2217.IAC, rfc2217.WILL, b)
				}
			case 3:
				if b == rfc2217.IAC {
					state = 4
				} else {
					sub = append(sub, b)
				}
			case 4:
				if b != rfc2217.SE {
					sub, state = append(sub, b), 3
					continue
				}
				state = 0
				if len(sub) < 2 {
					continue
				}
				switch sub[1] {
				case rfc2217.CmdSetControl:
					p.control = append(p.control, sub[2:]...)
				case rfc2217.CmdSetBaudRate:
					p.baud = slices.Clone(sub[2:])
				}
				reply := append([]byte{rfc2217.IAC, rfc2217.SB, sub[0], sub[1] + rfc2217.ServerOffset}, sub[2:]...)
				out.send(append(reply, rfc2217.IAC, rfc2217.SE)...)
			}
		}
		p.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// Listen serves p on a loopback TCP port until the test ends and returns
// its address.
func (p *Peer) Listen(t testing.TB) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu    sync.Mutex
		conns []net.Conn
	)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			go p.Serve(c)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	return ln.Addr().String()
}

// Data is what arrived as data, Telnet escapes undone.
func (p *Peer) Data() []byte { return p.get(&p.data) }

// Control is every SET-CONTROL value, in order.
func (p *Peer) Control() []byte { return p.get(&p.control) }

// Baud is the last SET-BAUDRATE argument (4 bytes, big endian), or nil.
func (p *Peer) Baud() []byte { return p.get(&p.baud) }

func (p *Peer) get(b *[]byte) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(*b)
}

// queue writes answers to a conn in order without blocking the reader.
type queue struct {
	mu      sync.Mutex
	pending [][]byte
	wake    chan struct{}
	done    chan struct{}
}

func newQueue(c net.Conn) *queue {
	q := &queue{wake: make(chan struct{}, 1), done: make(chan struct{})}
	go func() {
		for {
			select {
			case <-q.wake:
			case <-q.done:
				return
			}
			q.mu.Lock()
			out := q.pending
			q.pending = nil
			q.mu.Unlock()
			for _, b := range out {
				if _, err := c.Write(b); err != nil {
					return
				}
			}
		}
	}()
	return q
}

func (q *queue) send(b ...byte) {
	q.mu.Lock()
	q.pending = append(q.pending, b)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *queue) close() { close(q.done) }
