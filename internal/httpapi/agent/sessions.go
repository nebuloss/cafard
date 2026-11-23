package agent

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sort"
	"sync"
	"time"
)

// session is one MCP connection of an agent (docs/design.md, "Sessions"):
// which project it works in, and what it said it is doing.
//
// Sessions live in memory. A restart ends them all, and the client's next
// request gets a 404, which the specification says means "initialize
// again"; Claude Code does so by itself. Nothing an agent did is in a
// session: messages and read positions are in the store.
type session struct {
	id       string
	agentID  int64
	project  string
	task     string
	lastSeen time.Time
}

const (
	// idle ends a session nobody has used for this long: a client that
	// exited without DELETE.
	idle = 12 * time.Hour
	// perAgent bounds an agent's sessions, against a client that
	// initializes in a loop; the oldest goes first.
	perAgent = 64
)

type sessions struct {
	mu  sync.Mutex
	all map[string]*session
	now func() time.Time
	// onStart picks a project for a new session: the agent's only one.
	onStart func(r *http.Request, s *session)
}

func newSessions() *sessions {
	return &sessions{all: map[string]*session{}, now: time.Now}
}

// Start, Known and End are mcp.Sessions. The agent is the one the request
// was authenticated as: a session id is useless with another agent's token.
func (ss *sessions) Start(r *http.Request) (string, error) {
	who, ok := caller(r.Context())
	if !ok {
		return "", errNoCaller
	}
	b := make([]byte, 18)
	rand.Read(b)
	s := &session{id: base64.RawURLEncoding.EncodeToString(b), agentID: who.ID, lastSeen: ss.now()}
	if ss.onStart != nil {
		ss.onStart(r, s)
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.sweep()
	var mine []*session
	for _, o := range ss.all {
		if o.agentID == who.ID {
			mine = append(mine, o)
		}
	}
	if len(mine) >= perAgent {
		sort.Slice(mine, func(i, j int) bool { return mine[i].lastSeen.Before(mine[j].lastSeen) })
		for _, o := range mine[:len(mine)-perAgent+1] {
			delete(ss.all, o.id)
		}
	}
	ss.all[s.id] = s
	return s.id, nil
}

func (ss *sessions) Known(r *http.Request, id string) bool {
	_, ok := ss.get(r, id)
	return ok
}

func (ss *sessions) End(r *http.Request, id string) {
	if _, ok := ss.get(r, id); ok {
		ss.mu.Lock()
		delete(ss.all, id)
		ss.mu.Unlock()
	}
}

// get is the caller's session id, touched; a copy, so a tool reads it
// without the lock.
func (ss *sessions) get(r *http.Request, id string) (session, bool) {
	who, ok := caller(r.Context())
	if !ok {
		return session{}, false
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.all[id]
	if !ok || s.agentID != who.ID || ss.now().Sub(s.lastSeen) > idle {
		return session{}, false
	}
	s.lastSeen = ss.now()
	return *s, true
}

func (ss *sessions) lookup(id string) (session, bool) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.all[id]
	if !ok {
		return session{}, false
	}
	return *s, true
}

func (ss *sessions) set(id, project, task string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if s, ok := ss.all[id]; ok {
		s.project, s.task = project, task
	}
}

// tasks lists what an agent's live sessions in a project are on, for the
// member list: "upload drop", "ota rc3".
func (ss *sessions) tasks(agentID int64, project string) []string {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	var out []string
	for _, s := range ss.all {
		if s.agentID == agentID && s.project == project && ss.now().Sub(s.lastSeen) <= idle {
			t := s.task
			if t == "" {
				t = "no task named"
			}
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

func (ss *sessions) sweep() {
	now := ss.now()
	for id, s := range ss.all {
		if now.Sub(s.lastSeen) > idle {
			delete(ss.all, id)
		}
	}
}
