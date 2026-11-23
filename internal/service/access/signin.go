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

// Passwords are argon2id, at the parameters OWASP gives as its baseline:
// 19 MiB, two passes, one lane. The containers cafard runs in have 512 MB;
// this keeps a sign-in under a tenth of a second and a burst of them inside
// that memory, and the sign-in rate limit keeps it a burst.
const (
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16

	MinPassword = 10
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

// dummyHash is checked against when the username does not exist, so a
// wrong username takes as long as a wrong password and the two cannot be
// told apart by timing.
var dummyHash, _ = hashPassword("cafard has no such person")

// validPassword refuses what cannot be a password. Length is the only rule:
// composition rules make passwords harder to remember, not harder to guess.
func validPassword(p string) error {
	switch {
	case len(p) < MinPassword:
		return refuse(BadValue, "Use at least %d characters.", MinPassword)
	case len(p) > MaxPassword:
		return refuse(BadValue, "Use at most %d characters.", MaxPassword)
	}
	return nil
}

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

// Wrong is a username or password that does not match. One code for both,
// so a refusal does not say which.
const Wrong Code = "wrong"

// SignIn checks a person's username and password and returns a new
// sign-in's cookie value, for the chat app.
func (s *Service) SignIn(ctx context.Context, surface, username, password, from, userAgent string) (cookie string, who store.Identity, err error) {
	if surface == AdminSurface {
		return "", store.Identity{}, refuse(NotAdmin, "The admin UI opens with the admin password, not a person's.")
	}
	life, ok := signinLife[surface]
	if !ok {
		return "", store.Identity{}, fmt.Errorf("sign in: no surface %q", surface)
	}
	wrong := refuse(Wrong, "That username and password do not match.")
	id, err := s.st.IdentityByName(ctx, strings.ToLower(strings.TrimSpace(username)))
	hash := dummyHash
	if err == nil && id.Kind == store.Person {
		if h, herr := s.st.PasswordHash(ctx, id.ID); herr == nil {
			hash = h
		} else if !errors.Is(herr, store.ErrNotFound) {
			return "", store.Identity{}, herr
		}
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", store.Identity{}, err
	}
	if !checkPassword(hash, password) || hash == dummyHash {
		return "", store.Identity{}, wrong
	}
	if err := active(id); err != nil {
		return "", store.Identity{}, err
	}
	cookie, err = randomToken()
	if err != nil {
		return "", store.Identity{}, err
	}
	if len(userAgent) > 200 {
		userAgent = userAgent[:200]
	}
	_, err = s.st.CreateSignin(ctx, store.Signin{
		IdentityID: id.ID, Surface: surface, UserAgent: userAgent, From: from, ExpiresAt: time.Now().Add(life),
	}, hashToken(cookie), store.AuditEntry{Actor: id.Name, Action: "signed in to the " + surface + " app on", Target: describeAgent(userAgent), Origin: surface + " · " + from})
	if err != nil {
		return "", store.Identity{}, err
	}
	return cookie, id, nil
}

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
	return notFound(s.st.DeleteSignin(ctx, si.ID, who.audit("signed out", "a sign-in")), "the sign-in")
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
