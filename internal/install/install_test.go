package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Nothing a bundle names may land outside the home directory, or run
// anything but a program by its name.
func TestBundleStaysUnderHome(t *testing.T) {
	for _, p := range []string{"/etc/passwd", "../x", ".codex/../../x", "a/./b", "a b", "a;b", "$HOME/x", ""} {
		b := Bundle{Files: []File{{Path: p, Body: "x"}}}
		if err := b.Validate(); err == nil {
			t.Errorf("%q was accepted", p)
		}
		b = Bundle{Files: []File{{Path: ".ok", WindowsPath: p, Body: "x"}}}
		if p != "" && b.Validate() == nil {
			t.Errorf("windows path %q was accepted", p)
		}
	}
	for _, prog := range []string{"", "/bin/sh", "a b", "$(x)", "-rf", "../claude"} {
		if (Bundle{Files: []File{{Path: ".ok", Body: "x", Detect: []string{prog}}}}).Validate() == nil {
			t.Errorf("detecting %q was accepted", prog)
		}
		if (Bundle{Runs: []Run{{Client: "c", Program: prog, Steps: []Step{{Args: []string{"x"}}}}}}).Validate() == nil {
			t.Errorf("running %q was accepted", prog)
		}
	}
	if (Bundle{Files: []File{
		{Client: "Codex", Path: ".a", Detect: []string{"codex"}},
		{Client: "Codex", Path: ".b", Detect: []string{"other"}},
	}}).Validate() == nil {
		t.Error("one client's files naming different programs were accepted")
	}
	if (Bundle{Files: []File{{Client: "C", Path: ".a"}}, Runs: []Run{{Client: "C", Program: "c", Steps: []Step{{}}}}}).Validate() == nil {
		t.Error("a client configured by files and a command was accepted")
	}
	if err := (Bundle{Files: []File{{Path: ".config/crush/crush.json", WindowsPath: "AppData/Local/crush/crush.json", Body: "x"}}}).Validate(); err != nil {
		t.Errorf("a real bundle was refused: %v", err)
	}
}

// fakeProgram puts a program in dir that appends its arguments, one per
// line and then "--", to log, and exits with status.
func fakeProgram(t *testing.T, dir, name, log string, status int) {
	t.Helper()
	body := fmt.Sprintf("#!/bin/sh\nfor a in \"$@\"; do printf '%%s\\n' \"$a\"; done >> '%[1]s'\necho -- >> '%[1]s'\nexit %[2]d\n", log, status)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// The shell script, run for real against a scratch home: it writes every file
// byte for byte, the token's for its user alone, sources the env file from
// the profile once, and on a second run keeps what it replaces.
func TestShellScriptWritesTheFiles(t *testing.T) {
	path := toolbox(t)
	fakeProgram(t, path, "codex", filepath.Join(t.TempDir(), "log"), 0)
	home := t.TempDir()
	write(t, filepath.Join(home, ".bashrc"), "# mine\n")
	// Quotes, dollars, backticks and a line that looks like a terminator:
	// none of it may be read by the shell.
	tricky := "a = '$HOME' \"`x`\"\nCAFARD_EOF\n\\n end"
	env := "export CAFARD_TOKEN='cfd_ag_x'\n"
	b := Bundle{Name: "laptop", Files: []File{
		{Client: "cafard", Path: ".config/cafard/token", Private: true, Body: "cfd_ag_x\n"},
		{Client: "Codex", Path: ".codex/notes.toml", Body: tricky, Detect: []string{"codex"}},
		{Client: "Codex", Path: ".config/cafard/env.sh", Env: true, Private: true, Body: env, Detect: []string{"codex"}},
	}}

	out := runShell(t, b, home, path)
	if got := read(t, filepath.Join(home, ".codex/notes.toml")); got != tricky+"\n" {
		t.Errorf("notes.toml = %q", got)
	}
	if got := read(t, filepath.Join(home, ".config/cafard/env.sh")); got != env {
		t.Errorf("env.sh = %q", got)
	}
	for _, p := range []string{".config/cafard/token", ".config/cafard/env.sh"} {
		if fi, err := os.Stat(filepath.Join(home, p)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v, want mode 600", p, err)
		}
	}
	if !strings.Contains(out, "run: . "+home+"/.config/cafard/env.sh") {
		t.Errorf("the script did not say how to load the variables:\n%s", out)
	}

	// A token file left readable by an earlier install is made private again.
	if err := os.Chmod(filepath.Join(home, ".config/cafard/token"), 0o644); err != nil {
		t.Fatal(err)
	}
	runShell(t, b, home, path)
	if fi, _ := os.Stat(filepath.Join(home, ".config/cafard/token")); fi.Mode().Perm() != 0o600 {
		t.Errorf("the token file is %v after a second run", fi.Mode())
	}
	rc := read(t, filepath.Join(home, ".bashrc"))
	if n := strings.Count(rc, "env.sh\" ] && ."); n != 1 {
		t.Errorf("the profile sources the file %d times, want once:\n%s", n, rc)
	}
	if !strings.HasPrefix(rc, "# mine\n") {
		t.Error("the profile's own contents were not kept")
	}
	backups, _ := filepath.Glob(filepath.Join(home, ".codex/notes.toml.bak.*"))
	if len(backups) != 1 {
		t.Errorf("backups after the second run: %v", backups)
	}
}

// A client that is not installed gets nothing, and the script says so once.
func TestShellScriptSkipsWhatIsNotInstalled(t *testing.T) {
	home := t.TempDir()
	b := Bundle{Name: "laptop", Files: []File{
		{Client: "cafard", Path: ".config/cafard/token", Body: "t\n"},
		{Client: "Codex", Path: ".codex/config.toml", Body: "x = 1\n", Merge: &Merge{Format: "toml"}, Detect: []string{"codex"}},
		{Client: "Codex", Path: ".config/cafard/env.sh", Env: true, Body: "export CAFARD_TOKEN=t\n", Detect: []string{"codex"}},
	}, Runs: []Run{{Client: "Claude Code", Program: "claude", Steps: []Step{{Args: []string{"mcp", "list"}}}}}}
	out := runShell(t, b, home, toolbox(t))
	for _, want := range []string{"cafard: Codex: not installed", "cafard: Claude Code: not installed"} {
		if strings.Count(out, want) != 1 {
			t.Errorf("want %q once in:\n%s", want, out)
		}
	}
	for _, p := range []string{".codex/config.toml", ".config/cafard/env.sh", ".bashrc", ".profile"} {
		if _, err := os.Stat(filepath.Join(home, p)); err == nil {
			t.Errorf("%s was written for a client not installed", p)
		}
	}
	if read(t, filepath.Join(home, ".config/cafard/token")) != "t\n" {
		t.Error("the token file was not written")
	}
}

// A client's command runs where its program is, a step that may fail
// failing harmlessly; a step that fails otherwise fails the script, after
// the rest is done.
func TestShellScriptRunsACommand(t *testing.T) {
	b := Bundle{Name: "laptop", Runs: []Run{{Client: "Claude Code", Program: "claude", Steps: []Step{
		{Args: []string{"mcp", "remove", "cafard"}, MayFail: true},
		{Args: []string{"mcp", "add", "--header", "Authorization: Bearer it's $x"}},
	}}}}
	want := "mcp\nremove\ncafard\n--\nmcp\nadd\n--header\nAuthorization: Bearer it's $x\n--\n"
	for _, status := range []int{0, 1} {
		path := toolbox(t)
		log := filepath.Join(t.TempDir(), "log")
		fakeProgram(t, path, "claude", log, status)
		cmd := exec.Command(filepath.Join(path, "sh"))
		cmd.Stdin = strings.NewReader(Shell(b))
		cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=" + path}
		out, err := cmd.CombinedOutput()
		if got := read(t, log); got != want {
			t.Errorf("status %d: claude was run with\n%s", status, got)
		}
		if status == 0 && (err != nil || !strings.Contains(string(out), "Claude Code: set up")) {
			t.Errorf("a command that worked: %v\n%s", err, out)
		}
		if status == 1 && (err == nil || !strings.Contains(string(out), "Claude Code: claude failed") || strings.Contains(string(out), "set up")) {
			t.Errorf("a command that failed: %v\n%s", err, out)
		}
	}
}

// The PowerShell script carries files base64-encoded, sets an env file's
// variables for the user, checks each client is installed, and quotes
// everything so nothing in it is read as PowerShell. Nothing here runs
// PowerShell: the test checks the script's text.
func TestPowerShellScript(t *testing.T) {
	b := Bundle{Name: "o'brien", Files: []File{
		{Client: "crush", Path: ".config/crush/crush.json", WindowsPath: "AppData/Local/crush/crush.json", Body: `{"$x": "y"}`, Detect: []string{"crush"}},
		{Client: "Codex", Path: ".config/cafard/env.sh", Env: true, Detect: []string{"codex"},
			Body: "export CAFARD_URL=https://cafard\nexport CAFARD_TOKEN='cfd_ag_x'\nclaude() {\n}\n"},
	}, Runs: []Run{{Client: "Claude Code", Program: "claude", Steps: []Step{
		{Args: []string{"mcp", "remove", "cafard"}, MayFail: true},
		{Args: []string{"mcp", "add", "Bearer it's"}},
	}}}}
	ps := PowerShell(b)
	for _, want := range []string{
		`if (Test-CafardInstalled @('crush')) {`,
		`Put-CafardFile 'AppData\Local\crush\crush.json' 'eyIkeCI6ICJ5In0='`,
		`Write-Host 'cafard: crush: not installed'`,
		`if (Test-CafardInstalled @('codex')) {`,
		`[Environment]::SetEnvironmentVariable('CAFARD_URL', 'https://cafard', 'User')`,
		`[Environment]::SetEnvironmentVariable('CAFARD_TOKEN', 'cfd_ag_x', 'User')`,
		`Set-Item -Path ('env:' + 'CAFARD_TOKEN') -Value 'cfd_ag_x'`,
		`Get-Command 'claude' -CommandType Application`,
		`try { & $cafardExe.Source 'mcp' 'remove' 'cafard' *> $null } catch {}`,
		`& $cafardExe.Source 'mcp' 'add' 'Bearer it''s' | Out-Null`,
		`Write-Host 'cafard: Claude Code: not installed'`,
		`'o''brien'`,
	} {
		if !strings.Contains(ps, want) {
			t.Errorf("missing %s in:\n%s", want, ps)
		}
	}
	if strings.Contains(ps, "claude()") {
		t.Error("a shell function leaked into the PowerShell script")
	}
}

// A failure is a script that says why and fails, in either shell.
func TestFailure(t *testing.T) {
	if got := Failure(true, "it's gone"); got != "Write-Error 'cafard: it''s gone'\n" {
		t.Errorf("PowerShell: %q", got)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	cmd := exec.Command(sh)
	cmd.Stdin = strings.NewReader(Failure(false, "it's gone"))
	out, err := cmd.CombinedOutput()
	if err == nil || string(out) != "cafard: it's gone\n" {
		t.Errorf("sh: %v %q", err, out)
	}
}

func TestCommand(t *testing.T) {
	if got := Command("https://c/join/x", false); got != "curl -fsSL 'https://c/join/x?shell=sh' | sh" {
		t.Errorf("sh: %s", got)
	}
	if got := Command("https://c/join/x", true); got != "irm 'https://c/join/x?shell=ps1' | iex" {
		t.Errorf("ps1: %s", got)
	}
}
