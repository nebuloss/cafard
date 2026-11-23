package uploads

import (
	"strings"
	"testing"
	"time"
)

func TestALinkWorksOnce(t *testing.T) {
	s := New()
	code, g := s.Issue(Grant{IdentityID: 7, Project: "be98", Conversation: "wifi-drops", Name: "build.log"})
	if g.Expires.Sub(time.Now()) > Life {
		t.Errorf("expires in %v", g.Expires.Sub(time.Now()))
	}
	got, err := s.Redeem(code)
	if err != nil || got.IdentityID != 7 || got.Conversation != "wifi-drops" || got.Name != "build.log" {
		t.Fatalf("redeem: %+v %v", got, err)
	}
	if _, err := s.Redeem(code); err != ErrSpent {
		t.Errorf("redeemed twice: %v", err)
	}
}

func TestLinksThatMustNotWork(t *testing.T) {
	s := New()
	code, _ := s.Issue(Grant{IdentityID: 7, Project: "be98", Conversation: "x", Name: "a"})
	payload, sig, _ := strings.Cut(code, ".")

	// Another identity, the same signature: refused.
	forged := strings.Replace(payload, payload[:4], "AAAA", 1) + "." + sig
	if _, err := s.Redeem(forged); err != ErrSpent {
		t.Errorf("a changed payload: %v", err)
	}
	// A link from another server (another key): refused.
	other := New()
	foreign, _ := other.Issue(Grant{IdentityID: 7, Project: "be98", Conversation: "x", Name: "a"})
	if _, err := s.Redeem(foreign); err != ErrSpent {
		t.Errorf("a link signed elsewhere: %v", err)
	}
	for _, bad := range []string{"", "nodot", "a.b", ".", code + "x"} {
		if _, err := s.Redeem(bad); err != ErrSpent {
			t.Errorf("Redeem(%q): %v", bad, err)
		}
	}
	// Expired.
	start := time.Now()
	s.now = func() time.Time { return start }
	late, _ := s.Issue(Grant{IdentityID: 7, Project: "be98", Conversation: "x", Name: "a"})
	s.now = func() time.Time { return start.Add(Life + time.Second) }
	if _, err := s.Redeem(late); err != ErrSpent {
		t.Errorf("an expired link: %v", err)
	}
}
