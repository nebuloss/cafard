package access

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"cafard/internal/store"
)

// The admin password is argon2id, at the parameters OWASP gives as its
// baseline: 19 MiB, two passes, one lane. The containers cafard runs in
// have 512 MB; this keeps a check under a tenth of a second and a burst of
// them inside that memory, and the sign-in rate limit keeps it a burst.
// People have no passwords: they sign in with links (links.go).
const (
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16

	MaxPassword = 256
)

func hashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// checkPassword compares in constant time, reading the parameters from the
// hash so that raising them later leaves old hashes verifiable.
func checkPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is checked against when no admin password is set, so the
// answer takes as long either way.
var dummyHash, _ = hashPassword("cafard has no such person")

// Surfaces a person signs in to, each with sign-ins of its own.
const (
	AdminSurface = "admin"
	ChatSurface  = "chat"
)

// How long a sign-in lasts after its last use. The chat app's is long, so a
// phone stays signed in while it is used (docs/design.md, "Signing in"); the
// admin UI's is a working day.
var signinLife = map[string]time.Duration{
	AdminSurface: 12 * time.Hour,
	ChatSurface:  90 * 24 * time.Hour,
}

// Wrong is an admin password that does not match.
const Wrong Code = "wrong"

// Session is the person a sign-in cookie belongs to, if they may still act,
// and the sign-in itself. Each use slides its expiry.
func (s *Service) Session(ctx context.Context, surface, cookie, from string) (store.Identity, store.Signin, error) {
	expired := refuse(Unauthorized, "You are signed out. Sign in again.")
	if cookie == "" {
		return store.Identity{}, store.Signin{}, expired
	}
	si, err := s.st.SigninByHash(ctx, hashToken(cookie), surface)
	if errors.Is(err, store.ErrNotFound) {
		return store.Identity{}, store.Signin{}, expired
	}
	if err != nil {
		return store.Identity{}, store.Signin{}, err
	}
	id, err := s.st.Identity(ctx, si.IdentityID)
	if err != nil {
		return store.Identity{}, store.Signin{}, err
	}
	if err := active(id); err != nil {
		return store.Identity{}, store.Signin{}, err
	}
	if err := s.st.TouchSignin(ctx, si.ID, from, time.Now().Add(signinLife[surface])); err != nil {
		return store.Identity{}, store.Signin{}, err
	}
	return id, si, nil
}

// SignOut ends one sign-in: the asker's own, or, for an admin, anyone's.
func (s *Service) SignOut(ctx context.Context, who Who, si store.Signin) error {
	if si.IdentityID != who.ID {
		if err := requireAdmin(who); err != nil {
			return err
		}
	}
	err := s.st.DeleteSignin(ctx, si.ID, who.audit("signed out", "a sign-in"))
	if errors.Is(err, store.ErrNotFound) {
		return refuse(NotFound, "That sign-in has already ended.")
	}
	return err
}

func randomToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return strings.ToLower(tokenEncoding.EncodeToString(raw)), nil
}

// describeAgent turns a User-Agent into what a person recognises: "Firefox
// on Linux", "Chrome on Android". Only for the audit log and the list of
// sign-ins; nothing decides anything from it.
func describeAgent(ua string) string {
	browser := "a browser"
	for _, b := range []struct{ token, name string }{
		{"Firefox/", "Firefox"}, {"Edg/", "Edge"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"}, {"curl/", "curl"},
	} {
		if strings.Contains(ua, b.token) {
			browser = b.name
			break
		}
	}
	for _, o := range []struct{ token, name string }{
		{"Android", "Android"}, {"iPhone", "iPhone"}, {"iPad", "iPad"}, {"Windows", "Windows"}, {"Mac OS", "macOS"}, {"Linux", "Linux"},
	} {
		if strings.Contains(ua, o.token) {
			return browser + " on " + o.name
		}
	}
	return browser
}

// DescribeAgent is describeAgent, for the surfaces that list sign-ins.
func DescribeAgent(ua string) string { return describeAgent(ua) }
