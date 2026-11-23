package install

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// What join writes for crush and Codex, beside what a user already has.
const (
	crushNew = `{
  "$schema": "https://charm.land/crush.json",
  "mcp": {"cafard": {"type": "http", "url": "https://cafard/mcp", "headers": {"Authorization": "Bearer cfd_ag_new"}}}
}
`
	crushMine = `{
  // my servers
  "mcp": {
    "git": {"type": "stdio", "command": "uvx"},
    "cafard": {"type": "http", "url": "https://old/mcp", "stale": true},
  },
  "options": {"debug": true}
}
`
	codexNew = `[mcp_servers.cafard]
url = "https://cafard/mcp"
bearer_token_env_var = "CAFARD_TOKEN"
`
	codexMine = `# mine
model = "gpt-5"
approval_policy = "never"

[mcp_servers.git]
command = "uvx"

[mcp_servers.cafard]
url = "https://old/mcp"

[mcp_servers.cafard.env]
x = "1"
`
)

func mergeBundle() Bundle {
	return Bundle{Name: "laptop", Files: []File{
		{Client: "crush", Path: ".config/crush/crush.json", Body: crushNew,
			Merge: &Merge{Format: "json", Owns: []string{"mcp.cafard"}}},
		{Client: "Codex", Path: ".codex/config.toml", Body: codexNew, Merge: &Merge{Format: "toml"}},
	}}
}

// toolbox is a PATH holding the tools the script needs, plus the JSON
// engines named, and no others: which engine merges, and which clients are
// installed, is then the test's choice rather than the machine's.
func toolbox(t *testing.T, engines ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range append([]string{"sh", "cat", "cp", "mv", "mkdir", "dirname", "date", "rm", "mktemp", "awk", "grep", "chmod"}, engines...) {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("no %s here", tool)
		}
		if err := os.Symlink(p, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runShell(t *testing.T, b Bundle, home, path string) string {
	t.Helper()
	cmd := exec.Command(filepath.Join(path, "sh"))
	cmd.Stdin = strings.NewReader(Shell(b))
	cmd.Env = []string{"HOME=" + home, "PATH=" + path}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	return string(out)
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A client's own config keeps everything that is not cafard's, cafard's
// entry comes out exactly as sent, and a second run changes nothing — with
// python3 and with node, which must agree.
func TestShellScriptMergesClientConfigs(t *testing.T) {
	var merged []string
	for _, engine := range []string{"python3", "node"} {
		t.Run(engine, func(t *testing.T) {
			path := toolbox(t, engine)
			home := t.TempDir()
			crush := filepath.Join(home, ".config/crush/crush.json")
			codex := filepath.Join(home, ".codex/config.toml")
			write(t, crush, crushMine)
			write(t, codex, codexMine)

			runShell(t, mergeBundle(), home, path)
			first := read(t, crush)
			var got, want map[string]any
			if err := json.Unmarshal([]byte(first), &got); err != nil {
				t.Fatalf("merged crush.json does not parse: %v\n%s", err, first)
			}
			_ = json.Unmarshal([]byte(crushNew), &want)
			servers := got["mcp"].(map[string]any)
			if _, ok := servers["git"]; !ok {
				t.Error("the user's own MCP server was dropped")
			}
			if c, _ := json.Marshal(servers["cafard"]); string(c) != mustJSON(want["mcp"].(map[string]any)["cafard"]) {
				t.Errorf("cafard's entry is %s, want it exactly as sent", c)
			}
			if got["options"] == nil || got["$schema"] == nil {
				t.Errorf("options kept and $schema added, got %v", got)
			}

			toml := read(t, codex)
			for _, keep := range []string{`model = "gpt-5"`, `approval_policy = "never"`, "[mcp_servers.git]", `command = "uvx"`, "# mine",
				`bearer_token_env_var = "CAFARD_TOKEN"`} {
				if !strings.Contains(toml, keep) {
					t.Errorf("config.toml lacks %q:\n%s", keep, toml)
				}
			}
			for _, gone := range []string{"https://old/mcp", "[mcp_servers.cafard.env]"} {
				if strings.Contains(toml, gone) {
					t.Errorf("config.toml still has %q:\n%s", gone, toml)
				}
			}

			runShell(t, mergeBundle(), home, path)
			if again := read(t, crush); again != first {
				t.Errorf("a second run changed crush.json:\n%s\nthen\n%s", first, again)
			}
			if again := read(t, codex); again != toml {
				t.Errorf("a second run changed config.toml:\n%s\nthen\n%s", toml, again)
			}
			if backups, _ := filepath.Glob(crush + ".bak.*"); len(backups) == 0 {
				t.Error("no backup of crush.json")
			}
			merged = append(merged, first)
		})
	}
	if len(merged) == 2 && merged[0] != merged[1] {
		t.Errorf("python3 and node merge differently:\n%s\n%s", merged[0], merged[1])
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// A file that cannot be merged is left exactly as it was, with cafard's
// version beside it: no JSON engine on the machine, or a file that does not
// parse. A file that is not there yet is simply written.
func TestShellScriptNeverReplacesWhatItCannotMerge(t *testing.T) {
	b := Bundle{Name: "laptop", Files: mergeBundle().Files[:1]}
	crushRel := ".config/crush/crush.json"

	home := t.TempDir()
	write(t, filepath.Join(home, crushRel), crushMine)
	out := runShell(t, b, home, toolbox(t))
	if read(t, filepath.Join(home, crushRel)) != crushMine {
		t.Error("crush.json was changed with nothing to merge it")
	}
	if read(t, filepath.Join(home, crushRel+".cafard")) != crushNew {
		t.Error("cafard's version was not put beside it")
	}
	if !strings.Contains(out, "needs python3 or node") {
		t.Errorf("the script did not say why:\n%s", out)
	}

	home = t.TempDir()
	write(t, filepath.Join(home, crushRel), "[1, 2")
	runShell(t, b, home, toolbox(t, "python3"))
	if read(t, filepath.Join(home, crushRel)) != "[1, 2" {
		t.Error("a file that does not parse was replaced")
	}

	home = t.TempDir()
	runShell(t, b, home, toolbox(t))
	if read(t, filepath.Join(home, crushRel)) != crushNew {
		t.Error("a new file was not written as sent")
	}
}

// An env merge keeps the variables it names from the file on disk, and
// refuses to write when they are not there.
func TestShellScriptKeepsEnvVariables(t *testing.T) {
	b := Bundle{Name: "laptop", Files: []File{{Client: "Codex", Path: ".config/cafard/env.sh", Env: true,
		Body: "export CAFARD_URL=https://new\nexport CAFARD_TOKEN=placeholder\n", Merge: &Merge{Format: "env", Keep: []string{"CAFARD_TOKEN"}}}}}
	home := t.TempDir()
	env := filepath.Join(home, ".config/cafard/env.sh")
	write(t, env, "export CAFARD_URL=https://old\nexport CAFARD_TOKEN=cfd_ag_kept\n")
	runShell(t, b, home, toolbox(t))
	if got := read(t, env); got != "export CAFARD_URL=https://new\nexport CAFARD_TOKEN=cfd_ag_kept\n" {
		t.Errorf("env.sh = %q", got)
	}

	home = t.TempDir()
	cmd := exec.Command(filepath.Join(toolbox(t), "sh"))
	cmd.Stdin = strings.NewReader(Shell(b))
	cmd.Env = []string{"HOME=" + home, "PATH=" + filepath.Dir(cmd.Path)}
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "there is no") {
		t.Errorf("a missing env file: %v\n%s", err, out)
	}
}

func TestMergeIsValidated(t *testing.T) {
	file := func(m *Merge) Bundle { return Bundle{Files: []File{{Path: ".x", Body: "{}", Merge: m}}} }
	for _, bad := range []*Merge{
		{Format: "json"},
		{Format: "json", Owns: []string{"a b"}},
		{Format: "json", Owns: []string{"a;rm"}},
		{Format: "json", Owns: []string{"a..b"}},
		{Format: "toml", Owns: []string{"model"}},
		{Format: "env", Keep: []string{"X"}},
		{Format: "json", Owns: []string{"a"}, Keep: []string{"X"}},
		{Format: "yaml"},
	} {
		if file(bad).Validate() == nil {
			t.Errorf("%+v was accepted", *bad)
		}
	}
	if err := file(&Merge{Format: "json", Owns: []string{"$schema", "mcp.cafard"}}).Validate(); err != nil {
		t.Errorf("a real merge was refused: %v", err)
	}
	env := Bundle{Files: []File{{Path: ".x", Env: true, Merge: &Merge{Format: "env"}}}}
	if env.Validate() == nil {
		t.Error("an env merge keeping nothing was accepted")
	}
}

// The PowerShell script merges the same files through its own helpers.
func TestPowerShellScriptMerges(t *testing.T) {
	ps := PowerShell(mergeBundle())
	for _, want := range []string{
		"function Merge-CafardJson",
		"function Merge-CafardToml",
		`Merge-CafardFile '.config\crush\crush.json' 'json' '`,
		`@('mcp.cafard')`,
		`Merge-CafardFile '.codex\config.toml' 'toml' '`,
	} {
		if !strings.Contains(ps, want) {
			t.Errorf("missing %s", want)
		}
	}
}
