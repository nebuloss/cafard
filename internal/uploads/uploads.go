// Package uploads makes the one-time URLs an agent sends a file to
// (docs/design.md, "The MCP server": uploads do not pass through the model).
//
// The MCP server is remote and cannot read the agent's disk, and a file sent
// as a tool argument would cross the model's context as base64. So
// upload_link returns a URL, and the agent runs `curl -T file URL` with its
// own shell: the bytes go from its disk to cafard and nowhere near the model.
//
// A link is signed, not stored. It carries who may send, to which
// conversation, under which name, and until when, sealed with an HMAC key
// made when the server starts; its single use is remembered in memory until
// it expires. A restart forgets the key, so every link made before it stops
// working: they last minutes, and an agent asks for another.
package uploads

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// Life is how long a link works: an agent asks for one when it is about to
// send.
const Life = 10 * time.Minute

// Grant is what a link allows: one file, from one member, into one
// conversation.
type Grant struct {
	IdentityID   int64     `json:"i"`
	Project      string    `json:"p"`
	Conversation string    `json:"c"`
	Name         string    `json:"n"`
	Expires      time.Time `json:"e"`
	Nonce        string    `json:"r"`
}

// ErrSpent is a link that is used, expired, or not one this server made.
var ErrSpent = errors.New("this upload link no longer works: ask upload_link for another")

type Signer struct {
	key  []byte
	now  func() time.Time
	mu   sync.Mutex
	used map[string]time.Time
}

// New makes a signer with a fresh key.
func New() *Signer {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return &Signer{key: key, now: time.Now, used: map[string]time.Time{}}
}

var enc = base64.RawURLEncoding

// Issue makes the code of a link for g; g.Expires is set to Life from now.
func (s *Signer) Issue(g Grant) (string, Grant) {
	nonce := make([]byte, 12)
	rand.Read(nonce)
	g.Nonce = enc.EncodeToString(nonce)
	g.Expires = s.now().Add(Life).Truncate(time.Second)
	body, _ := json.Marshal(g)
	payload := enc.EncodeToString(body)
	return payload + "." + s.sign(payload), g
}

func (s *Signer) sign(payload string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return enc.EncodeToString(m.Sum(nil))
}

// Redeem checks a code and uses it: a second Redeem of the same code fails.
func (s *Signer) Redeem(code string) (Grant, error) {
	payload, sig, ok := strings.Cut(code, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return Grant{}, ErrSpent
	}
	body, err := enc.DecodeString(payload)
	if err != nil {
		return Grant{}, ErrSpent
	}
	var g Grant
	if err := json.Unmarshal(body, &g); err != nil {
		return Grant{}, ErrSpent
	}
	now := s.now()
	if !now.Before(g.Expires) {
		return Grant{}, ErrSpent
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for n, exp := range s.used {
		if !now.Before(exp) {
			delete(s.used, n)
		}
	}
	if _, done := s.used[g.Nonce]; done {
		return Grant{}, ErrSpent
	}
	s.used[g.Nonce] = g.Expires
	return g, nil
}
