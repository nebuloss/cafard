// Package live tells open pages and waiting agents that something changed,
// so they read it again (docs/design.md, "What carries over": claudication's
// rule, here with topics per project and conversation).
//
// A notice carries no data, only which thing changed: the reader asks the
// API, which checks access as it always does. That keeps one source of
// truth, and keeps the stream from ever carrying something its reader may
// not see beyond the fact that a project it is in changed.
//
// Keys name what changed:
//
//	p:<project id>                     the project: its conversations, members, settings
//	c:<project id>:<conversation id>   a conversation: messages, files, reads, title
//	m:<identity id>                    a member: their projects, their access
//	admin                              anything the audit log records, and
//	                                   the admin's notices read: what the
//	                                   admin UI shows beyond the keys above
//
// The store publishes on every write that changes one of these; a
// subscriber chooses the keys it may hear.
package live

import (
	"strconv"
	"strings"
	"sync"
)

// buffer is how many notices a subscriber holds before it is behind; one
// that falls behind is told to read everything again rather than block the
// writer.
const buffer = 64

// Notice is one key that changed, or Resync: notices were lost.
type Notice struct {
	Key    string
	Resync bool
}

// Hub fans notices out to subscribers. The zero value is not usable; New
// makes one. A nil *Hub ignores everything, so code that runs without one
// (cafard-server admin on the box, tests) publishes into nothing.
type Hub struct {
	mu     sync.Mutex
	subs   map[*Sub]struct{}
	closed bool
}

// Close ends every subscriber: its channel is closed, which is how an event
// stream learns the server is stopping. A stream left open would hold the
// server's shutdown until it timed out. A subscriber made after Close is
// closed at once.
func (h *Hub) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for s := range h.subs {
		close(s.C)
		delete(h.subs, s)
	}
}

func New() *Hub { return &Hub{subs: map[*Sub]struct{}{}} }

// Sub is one subscriber: a page's event stream, or an agent's wait.
type Sub struct {
	C      chan Notice
	accept func(key string) bool
	lost   bool
}

// Subscribe starts a subscriber that hears the keys accept lets through.
func (h *Hub) Subscribe(accept func(key string) bool) *Sub {
	s := &Sub{C: make(chan Notice, buffer), accept: accept}
	if h == nil {
		return s
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(s.C)
		return s
	}
	h.subs[s] = struct{}{}
	return s
}

// Unsubscribe stops s.
func (h *Hub) Unsubscribe(s *Sub) {
	if h == nil {
		return
	}
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

// Accepts tells whether s hears key, for a stream that checks a notice
// again after its subscription changed.
func (s *Sub) Accepts(key string) bool { return s.accept == nil || s.accept(key) }

// SetAccept changes what s hears, as when a member joins a project.
func (h *Hub) SetAccept(s *Sub, accept func(key string) bool) {
	if h == nil {
		return
	}
	h.mu.Lock()
	s.accept = accept
	h.mu.Unlock()
}

// Publish tells every subscriber that accepts them that keys changed. It
// never blocks: a subscriber whose buffer is full is marked behind, and gets
// a Resync as soon as it has room.
func (h *Hub) Publish(keys ...string) {
	if h == nil || len(keys) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		for _, k := range keys {
			if s.accept != nil && !s.accept(k) {
				continue
			}
			n := Notice{Key: k}
			if s.lost {
				n = Notice{Resync: true}
			}
			select {
			case s.C <- n:
				s.lost = false
			default:
				s.lost = true
			}
		}
	}
}

// Keys.

// AdminKey is published with every write the audit log records: links made,
// used and cancelled, tokens, settings, members and projects. Only the
// admin's stream hears it.
const AdminKey = "admin"

func ProjectKey(projectID int64) string { return "p:" + strconv.FormatInt(projectID, 10) }

func ConversationKey(projectID, conversationID int64) string {
	return "c:" + strconv.FormatInt(projectID, 10) + ":" + strconv.FormatInt(conversationID, 10)
}

func MemberKey(identityID int64) string { return "m:" + strconv.FormatInt(identityID, 10) }

// Parsed is a key taken apart.
type Parsed struct {
	Kind           byte // 'p', 'c' or 'm'
	ProjectID      int64
	ConversationID int64
	IdentityID     int64
}

// Parse takes a key apart; ok is false for one that is not a key.
func Parse(key string) (p Parsed, ok bool) {
	parts := strings.Split(key, ":")
	nums := make([]int64, 0, 2)
	for _, s := range parts[1:] {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return Parsed{}, false
		}
		nums = append(nums, n)
	}
	switch {
	case parts[0] == "p" && len(nums) == 1:
		return Parsed{Kind: 'p', ProjectID: nums[0]}, true
	case parts[0] == "c" && len(nums) == 2:
		return Parsed{Kind: 'c', ProjectID: nums[0], ConversationID: nums[1]}, true
	case parts[0] == "m" && len(nums) == 1:
		return Parsed{Kind: 'm', IdentityID: nums[0]}, true
	}
	return Parsed{}, false
}
