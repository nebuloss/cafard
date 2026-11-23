package join

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"cafard/internal/install"
)

// The MCP clients an agent's link sets up, and how: each registers cafard's
// MCP server under mcpServer, and each merge owns only that entry, so the
// user's other servers stay. Kept here rather than in internal/install
// because they are what this link installs, and nothing else installs
// anything; install knows how to write a file, not which.
//
// The entries are claudication's (docs/clients.md there, "Agent tools
// (MCP)"), measured live against each client:
//
//   - Claude Code: `claude mcp add --scope user`, once for the machine, the
//     header carrying the token. Its MCP servers are in ~/.claude.json, which
//     holds everything else it remembers, so it writes them itself.
//   - Codex: [mcp_servers.cafard] with bearer_token_env_var: Codex refuses an
//     inline token for an HTTP server. The variable is in an env file sourced
//     from the shell profile, or a user variable on Windows.
//   - crush: mcp.cafard, type "http", reading ~/.config on Windows too.
//   - opencode: mcp.cafard, type "remote", with oauth false, or it tries an
//     OAuth sign-in on any refusal.
//
// The paths are the same on every system, under the home directory.
const (
	mcpServer = "cafard"
	mcpPath   = "/mcp"
	tokenEnv  = "CAFARD_TOKEN"
)

// Client is an MCP client an agent's link can set up, as the admin UI and
// the command name it: its id in ?client=, and its name.
type Client struct {
	ID, Name string
}

// Clients are the clients a link can set up, in the order the admin UI lists
// them; the first is the one set up when the command names none.
var Clients = []Client{
	{"claude-code", "Claude Code"},
	{"codex", "Codex"},
	{"crush", "crush"},
	{"opencode", "opencode"},
}

// ParseClient reads ?client=: one of Clients, by id, or the first of them
// when it is empty. One command sets up one client, as claudication's do: a
// machine with two runs a second link.
func ParseClient(id string) (Client, error) {
	if id == "" {
		return Clients[0], nil
	}
	for _, c := range Clients {
		if c.ID == id {
			return c, nil
		}
	}
	var ids []string
	for _, c := range Clients {
		ids = append(ids, c.ID)
	}
	return Client{}, fmt.Errorf("no client %q: the clients are %s", id, strings.Join(ids, ", "))
}

// agentBundle is what an agent's link installs on its machine: the token, in
// ~/.config/cafard/token for anything else on the machine that wants it, and
// the one MCP client chosen, if the machine has it.
func agentBundle(base, name, token string, c Client) install.Bundle {
	b := everyClient(base, name, token)
	keep := func(client string) bool { return client == "cafard" || client == c.Name }
	b.Files = slices.DeleteFunc(b.Files, func(f install.File) bool { return !keep(f.Client) })
	b.Runs = slices.DeleteFunc(b.Runs, func(r install.Run) bool { return !keep(r.Client) })
	return b
}

func everyClient(base, name, token string) install.Bundle {
	mcp := base + mcpPath
	bearer := "Bearer " + token
	return install.Bundle{Name: name, Files: []install.File{
		{Client: "cafard", Path: ".config/cafard/token", Private: true, Body: token + "\n"},
		{Client: "cafard", Path: ".config/cafard/config", Body: "url=" + base + "\nagent=" + name + "\n"},
		{Client: "Codex", Path: ".codex/config.toml", Detect: []string{"codex"}, Merge: &install.Merge{Format: "toml"},
			Body: fmt.Sprintf("[mcp_servers.%s]\nurl = %s\nbearer_token_env_var = %q\n", mcpServer, strconv.Quote(mcp), tokenEnv)},
		{Client: "Codex", Path: ".config/cafard/env.sh", Detect: []string{"codex"}, Env: true, Private: true,
			Body: "export " + tokenEnv + "='" + token + "'\n"},
		{Client: "crush", Path: ".config/crush/crush.json", Detect: []string{"crush"},
			Merge: &install.Merge{Format: "json", Owns: []string{"mcp." + mcpServer}},
			Body:  mcpJSON(map[string]any{"type": "http", "url": mcp, "headers": map[string]string{"Authorization": bearer}})},
		{Client: "opencode", Path: ".config/opencode/opencode.jsonc", Detect: []string{"opencode"},
			Merge: &install.Merge{Format: "json", Owns: []string{"mcp." + mcpServer}},
			Body:  mcpJSON(map[string]any{"type": "remote", "url": mcp, "oauth": false, "headers": map[string]string{"Authorization": bearer}})},
	}, Runs: []install.Run{{Client: "Claude Code", Program: "claude", Steps: []install.Step{
		// A registration left by an earlier link is replaced, so running a
		// new link is how a token is changed.
		{Args: []string{"mcp", "remove", "--scope", "user", mcpServer}, MayFail: true},
		{Args: []string{"mcp", "add", "--scope", "user", "--transport", "http", mcpServer, mcp, "--header", "Authorization: " + bearer}},
	}}}}
}

// mcpJSON is a JSON config holding cafard's entry alone under mcp.
func mcpJSON(entry map[string]any) string {
	b, _ := json.MarshalIndent(map[string]any{"mcp": map[string]any{mcpServer: entry}}, "", "  ")
	return string(b) + "\n"
}
