package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"time"
)

// sessionLife is how long an admin sign-in lasts after its last use: a
// working day.
const sessionLife = 12 * time.Hour

// sessions are the admin UI's sign-ins, in memory as claudication keeps its
// own: the password is in the configuration file, so changing it means a
// restart, and the restart is what ends the sign-ins made with the old one.
// Only the cookie's hash is kept.
type sessions struct {
	mu  sync.Mutex
	all map[[32]byte]time.Time // expiry
	now func() time.Time
}

func newSessions() *sessions {
	return &sessions{all: map[[32]byte]time.Time{}, now: time.Now}
}

// start opens a sign-in and returns its cookie value.
func (s *sessions) start() string {
	raw := make([]byte, 32)
	rand.Read(raw)
	cookie := base64.RawURLEncoding.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, exp := range s.all {
		if !now.Before(exp) {
			delete(s.all, k)
		}
	}
	s.all[sha256.Sum256([]byte(cookie))] = now.Add(sessionLife)
	return cookie
}

// valid is whether cookie is a live sign-in; each use slides its expiry.
func (s *sessions) valid(cookie string) bool {
	if cookie == "" {
		return false
	}
	k := sha256.Sum256([]byte(cookie))
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.all[k]
	now := s.now()
	if !ok || !now.Before(exp) {
		delete(s.all, k)
		return false
	}
	s.all[k] = now.Add(sessionLife)
	return true
}

func (s *sessions) end(cookie string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.all, sha256.Sum256([]byte(cookie)))
}
