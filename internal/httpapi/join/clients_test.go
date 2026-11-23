package join

import (
	"net/http"
	"strings"
	"testing"
)

func TestParseClient(t *testing.T) {
	if c, err := ParseClient(""); err != nil || c.ID != "claude-code" {
		t.Errorf("none: %v %v", c, err)
	}
	if c, err := ParseClient("codex"); err != nil || c.Name != "Codex" {
		t.Errorf("codex: %v %v", c, err)
	}
	for _, q := range []string{"emacs", "codex,crush"} {
		if _, err := ParseClient(q); err == nil || !strings.Contains(err.Error(), "claude-code, codex, crush, opencode") {
			t.Errorf("%s: %v", q, err)
		}
	}
}

// The chosen client is the only one set up; cafard's own files always are.
func TestAgentBundleOfOneClient(t *testing.T) {
	for _, c := range []struct {
		id, files string
		runs      int
	}{
		{"claude-code", "cafard,cafard", 1},
		{"codex", "cafard,cafard,Codex,Codex", 0},
		{"crush", "cafard,cafard,crush", 0},
		{"opencode", "cafard,cafard,opencode", 0},
	} {
		client, _ := ParseClient(c.id)
		b := agentBundle("https://c", "dev-code", "cfd_ag_x", client)
		var files []string
		for _, f := range b.Files {
			files = append(files, f.Client)
		}
		if got := strings.Join(files, ","); got != c.files || len(b.Runs) != c.runs {
			t.Errorf("%s: files of %s, %d runs", c.id, got, len(b.Runs))
		}
	}
}

// A client mistyped in the command is refused before the link is spent.
func TestAnUnknownClientSpendsNothing(t *testing.T) {
	r := newRig(t)
	u := r.link(t, r.devCode)
	code, body, _ := get(t, u+"?shell=sh&client=emacs", "curl/8.5.0")
	if code != http.StatusOK || !strings.Contains(body, "emacs") || !strings.Contains(body, "exit 1") || tokenRE.MatchString(body) {
		t.Fatalf("refusal: %d\n%s", code, body)
	}
	if code, body, _ := get(t, u+"?shell=sh&client=crush", "curl/8.5.0"); code != http.StatusOK || !strings.Contains(body, "crush") || strings.Contains(body, "codex") {
		t.Errorf("the link after the refusal: %d\n%s", code, body)
	}
}
