package store

import (
	"database/sql"
	"testing"
)

func TestAuditPageNarrowsAndPages(t *testing.T) {
	s, _ := open(t)
	entries := []AuditEntry{
		{Actor: "admin", Action: "created project", Target: "be98", Origin: "admin · 10.0.50.21"},
		{Actor: "admin", Action: "created agent", Target: "dev-code", Origin: "cli"},
		{Actor: "admin", Action: "made an install link for", Target: "dev-code", Origin: "cli"},
		{Actor: "dev-code", Action: "redeemed its install link", Target: "dev-code", Origin: "join · 10.0.50.20"},
		{Actor: "camille", Action: "signed in to the chat app on", Target: "Safari on iPhone", Origin: "chat · 10.0.50.44"},
		{Actor: "admin", Action: "added dev-code to", Target: "be98", Origin: "admin · 10.0.50.21"},
	}
	for _, e := range entries {
		if err := s.change(ctx, e, func(*sql.Tx) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}

	all, next, err := s.AuditPage(ctx, AuditFilter{})
	if err != nil || len(all) != 6 || next != 0 {
		t.Fatalf("all: %d %d %v", len(all), next, err)
	}
	if all[0].Action != "added dev-code to" || all[0].Kind != "members" || all[0].Source != "admin" {
		t.Errorf("newest: %+v", all[0])
	}
	kinds := map[string]string{}
	for _, r := range all {
		kinds[r.Action] = r.Kind
	}
	for action, want := range map[string]string{
		"created project": "projects", "made an install link for": "links", "redeemed its install link": "links",
		"signed in to the chat app on": "sign-ins", "created agent": "members",
	} {
		if kinds[action] != want {
			t.Errorf("%q is %q, want %q", action, kinds[action], want)
		}
	}

	page, next, _ := s.AuditPage(ctx, AuditFilter{Limit: 4})
	rest, last, _ := s.AuditPage(ctx, AuditFilter{Limit: 4, Before: next})
	if len(page) != 4 || next == 0 || len(rest) != 2 || last != 0 || rest[0].ID >= page[3].ID {
		t.Errorf("pages: %d then %d, next %d, last %d", len(page), len(rest), next, last)
	}

	links, _, _ := s.AuditPage(ctx, AuditFilter{Kinds: []string{"links"}, Sources: []string{"cli"}})
	if len(links) != 1 || links[0].Action != "made an install link for" {
		t.Errorf("links from the cli: %+v", links)
	}
	devCode, _, _ := s.AuditPage(ctx, AuditFilter{Actors: []string{"admin", "dev-code"}, Targets: []string{"dev-code"}})
	if len(devCode) != 3 {
		t.Errorf("about dev-code, by admin or itself: %d", len(devCode))
	}

	facets, err := s.AuditFacets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f := facets["source"]; len(f) == 0 || f[0].Value != "admin" && f[0].Value != "cli" {
		t.Errorf("sources: %+v", f)
	}
	if f := facets["actor"]; len(f) == 0 || f[0].Value != "admin" || f[0].Count != 4 {
		t.Errorf("actors: %+v", f)
	}
}
