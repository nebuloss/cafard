package join

import (
	"net/http"
	"strings"
	"testing"

	"cafard/internal/install"
)

func TestParseClients(t *testing.T) {
	if got, err := ParseClients(""); err != nil || got != nil {
		t.Errorf("none: %v %v", got, err)
	}
	if got, err := ParseClients("codex, crush,codex"); err != nil || strings.Join(got, ",") != "codex,crush" {
		t.Errorf("two: %v %v", got, err)
	}
	if _, err := ParseClients("codex,emacs"); err == nil || !strings.Contains(err.Error(), "claude-code, codex, crush, opencode") {
		t.Errorf("an unknown client: %v", err)
	}
}

// Chosen clients are the only ones set up; cafard's own files always are.
func TestAgentBundleOfChosenClients(t *testing.T) {
	b := agentBundle("https://c", "dev-code", "cfd_ag_x", "crush")
	var clients []string
	for _, f := range b.Files {
		clients = append(clients, f.Client)
	}
	if got := strings.Join(clients, ","); got != "cafard,cafard,crush" || len(b.Runs) != 0 {
		t.Errorf("crush alone: files of %s, %d runs", got, len(b.Runs))
	}
	if b := agentBundle("https://c", "dev-code", "cfd_ag_x", "claude-code"); len(b.Runs) != 1 || len(b.Files) != 2 {
		t.Errorf("Claude Code alone: %d files, %d runs", len(b.Files), len(b.Runs))
	}
	if all := agentBundle("https://c", "dev-code", "cfd_ag_x"); len(all.Files) != 6 || len(all.Runs) != 1 {
		t.Errorf("every client: %d files, %d runs", len(all.Files), len(all.Runs))
	}
	if got := install.Command("https://c/join/x", false, "codex", "crush"); got != "curl -fsSL 'https://c/join/x?shell=sh&clients=codex,crush' | sh" {
		t.Errorf("command: %s", got)
	}
}

// A client mistyped in the command is refused before the link is spent.
func TestAnUnknownClientSpendsNothing(t *testing.T) {
	r := newRig(t)
	u := r.link(t, r.devCode)
	code, body, _ := get(t, u+"?shell=sh&clients=emacs", "curl/8.5.0")
	if code != http.StatusOK || !strings.Contains(body, "no client \\\"emacs\\\"") && !strings.Contains(body, `no client "emacs"`) || !strings.Contains(body, "exit 1") {
		t.Fatalf("refusal: %d\n%s", code, body)
	}
	if code, body, _ := get(t, u+"?shell=sh&clients=crush", "curl/8.5.0"); code != http.StatusOK || !strings.Contains(body, "crush") || strings.Contains(body, "codex") {
		t.Errorf("the link after the refusal: %d\n%s", code, body)
	}
}
