// Package install writes the setup scripts an agent's join link serves:
// every MCP client on a machine pointed at cafard, by one pasted command.
//
// What goes where is decided elsewhere (internal/httpapi/join, which knows
// the clients); this turns it into a script for one shell, the query of the
// link saying which:
//
//	curl -fsSL 'https://cafard.example/join/<code>?shell=sh' | sh
//	irm 'https://cafard.example/join/<code>?shell=ps1' | iex
//
// Ported from claudication's internal/service/install, without what serves
// its links from memory or updates a client already set up: a join link is
// kept by internal/service/access, and running a new one is the update.
//
// A client is configured only where it is installed: each file or command
// names the programs that say so (File.Detect, Run.Program), and the script
// says "<client>: not installed" for the others rather than leave a
// configuration nothing reads. The scripts back up every file they change,
// beside it, so running a newer command over an older setup is an update and
// never a loss. A client's own config — Codex's, crush's, opencode's — is
// merged rather than replaced: cafard's entry is rewritten and the rest of
// the file is left alone (see Merge, and the helpers under merge/).
package install

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The merge helpers, carried inside the scripts. JSON on Linux and macOS goes
// through python3 or node, whichever the machine has, since sh has nothing
// that reads it; TOML through awk, which every one has. Windows has
// PowerShell for both.
var (
	//go:embed merge/merge.py
	mergePy string
	//go:embed merge/merge.js
	mergeJS string
	//go:embed merge/merge.awk
	mergeAwk string
	//go:embed merge/merge.ps1
	mergePS1 string
	//go:embed merge/env.awk
	envAwk string
)

// File is one file a client is configured with, and where it goes.
type File struct {
	// Client is the client's name, for the script's messages: "Codex".
	Client string
	// Path is where it goes, relative to the home directory, with forward
	// slashes: ".codex/config.toml".
	Path string
	// WindowsPath is where it goes on Windows when that differs, relative to
	// the home directory as well. No client needs one today.
	WindowsPath string
	// Env marks a file of `export NAME=value` lines meant to be in every
	// shell: sourced from the shell profile on Linux and macOS, and set as the
	// user's environment variables on Windows, where there is no profile to
	// source it from.
	Env bool
	// Private is a file that holds a token: mode 600 on Linux and macOS, even
	// when it was there already with another. On Windows the home directory
	// is the user's alone already.
	Private bool
	Body    string
	// Merge, when set, folds the file into the one the client already has
	// instead of replacing it. Absent for a file that is cafard's alone.
	Merge *Merge
	// Detect are the programs that say the client is installed: the file is
	// written only when one of them is on PATH. None is a file written
	// whatever the machine has.
	Detect []string
}

// Run is a command run where its program is installed, for a client that
// keeps its configuration where only it writes: Claude Code, whose MCP
// servers are in ~/.claude.json beside everything else it remembers.
type Run struct {
	Client string
	// Program is what is run, and what says the client is installed.
	Program string
	Steps   []Step
}

// Step is one run of the program, with these arguments.
type Step struct {
	Args []string
	// MayFail is a step whose failure is no failure: removing what an
	// earlier install registered, which a first install has not.
	MayFail bool
}

// Merge is how a file is folded into a client config that holds more than
// cafard's settings: Codex's other MCP servers, crush's providers, opencode's
// theme. Writing the whole file over it would lose all of that.
type Merge struct {
	// Format is "json" (JSONC too), "toml", or "env" for an env file.
	Format string
	// Owns is, for JSON, the dotted paths cafard owns: each is replaced
	// whole, so a setting cafard stopped sending does not linger inside it.
	// The file's other top-level keys are added only when absent. TOML needs
	// none: what cafard owns there is what its file defines — the top-level
	// keys and the tables — since none of it is a map shared with anything
	// else.
	Owns []string
	// Keep is, for an env file, the variables whose lines stay as they are in
	// the file on disk — on Windows, the user variables as they are set — the
	// rest being rewritten from the new file. Each must be there, or nothing
	// is written.
	Keep []string
}

// ownedPath is a dotted JSON path of plain names, safe to pass to a shell.
var ownedPath = regexp.MustCompile(`^[A-Za-z0-9_$-]+(\.[A-Za-z0-9_$-]+)*$`)

// envName is a variable's name, safe to pass to a shell.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// program is a program's name, looked up on PATH: no path, nothing a shell
// would read as anything but the name.
var program = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func (m *Merge) validate(env bool) error {
	if m == nil {
		return nil
	}
	if (m.Format == "env") != env {
		return errors.New("an env file merges as env, and only an env file does")
	}
	if m.Format != "env" && len(m.Keep) != 0 {
		return errors.New("only an env merge keeps variables")
	}
	switch m.Format {
	case "env":
		if len(m.Keep) == 0 || len(m.Owns) != 0 {
			return errors.New("an env merge names the variables it keeps, and owns nothing")
		}
		for _, v := range m.Keep {
			if !envName.MatchString(v) {
				return fmt.Errorf("%q is not a variable's name", v)
			}
		}
	case "json":
		if len(m.Owns) == 0 {
			return errors.New("a JSON merge names what it owns")
		}
		for _, p := range m.Owns {
			if !ownedPath.MatchString(p) {
				return fmt.Errorf("%q is not a path of plain names", p)
			}
		}
	case "toml":
		if len(m.Owns) != 0 {
			return errors.New("a TOML merge owns what its file defines, and names nothing")
		}
	default:
		return fmt.Errorf("no merge for %q files", m.Format)
	}
	return nil
}

// Bundle is what one command installs: an agent's files and commands.
type Bundle struct {
	// Name is the agent's, for the script's first line.
	Name  string
	Files []File
	Runs  []Run
}

// Command is the line that runs a join link's script: curl into sh on Linux
// and macOS, irm into iex on Windows.
//
// clients, if any, are the clients the script sets up, by id; none is every
// client the machine has.
func Command(link string, windows bool, clients ...string) string {
	q := ""
	if len(clients) > 0 {
		q = "&clients=" + strings.Join(clients, ",")
	}
	if windows {
		return "irm '" + link + "?shell=ps1" + q + "' | iex"
	}
	return "curl -fsSL '" + link + "?shell=sh" + q + "' | sh"
}

// Limits on what a bundle may hold: enough for every client there is
// with room to spare.
const (
	maxFiles = 16
	maxBytes = 4 << 20
)

// safePath is a home-relative path made of ordinary names: no absolute paths,
// no "..", nothing a shell or PowerShell would read as anything but text.
var safePath = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// Validate refuses a bundle that could write anywhere but under the home
// directory, run anything but a program by its name, or that is larger than
// any real configuration.
func (b Bundle) Validate() error {
	if len(b.Files)+len(b.Runs) == 0 {
		return errors.New("nothing to install")
	}
	if len(b.Files) > maxFiles || len(b.Runs) > maxFiles {
		return fmt.Errorf("at most %d files and %d commands", maxFiles, maxFiles)
	}
	total := 0
	detect := map[string][]string{}
	for _, f := range b.Files {
		paths := []string{f.Path}
		if f.WindowsPath != "" {
			paths = append(paths, f.WindowsPath)
		}
		for _, p := range paths {
			if !safePath.MatchString(p) || strings.Contains("/"+p+"/", "/../") || strings.Contains("/"+p+"/", "/./") {
				return fmt.Errorf("%q is not a path under the home directory", p)
			}
		}
		if err := f.Merge.validate(f.Env); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
		for _, p := range f.Detect {
			if !program.MatchString(p) {
				return fmt.Errorf("%s: %q is not a program's name", f.Path, p)
			}
		}
		// A client is installed or not: its files cannot disagree on what
		// says so, since the script checks once for all of them.
		if d, ok := detect[f.Client]; ok && !slices.Equal(d, f.Detect) {
			return fmt.Errorf("%s: the files of %s name different programs", f.Path, f.Client)
		}
		detect[f.Client] = f.Detect
		total += len(f.Body)
	}
	for _, r := range b.Runs {
		if !program.MatchString(r.Program) {
			return fmt.Errorf("%q is not a program's name", r.Program)
		}
		if _, ok := detect[r.Client]; ok {
			return fmt.Errorf("%s is configured by files or by a command, not both", r.Client)
		}
		if len(r.Steps) == 0 {
			return fmt.Errorf("%s: a command with nothing to run", r.Client)
		}
		for _, st := range r.Steps {
			for _, a := range st.Args {
				total += len(a)
			}
		}
	}
	if total > maxBytes {
		return fmt.Errorf("the files come to more than %d bytes", maxBytes)
	}
	return nil
}

// group is one client's part of a script: its files, or its command, under
// one check that it is installed.
type group struct {
	client string
	detect []string
	files  []File
	run    *Run
}

// groups are the bundle's clients in the order first named, files before
// commands.
func (b Bundle) groups() []group {
	var out []group
	at := map[string]int{}
	for _, f := range b.Files {
		i, ok := at[f.Client]
		if !ok {
			i = len(out)
			at[f.Client] = i
			out = append(out, group{client: f.Client, detect: f.Detect})
		}
		out[i].files = append(out[i].files, f)
	}
	for _, r := range b.Runs {
		out = append(out, group{client: r.Client, detect: []string{r.Program}, run: &r})
	}
	return out
}

// heredocEnd is a terminator no file can contain by accident: random, and
// checked against the body anyway.
func heredocEnd(body string) string {
	for {
		raw := make([]byte, 6)
		_, _ = rand.Read(raw)
		end := "CAFARD_" + strings.ToUpper(hex.EncodeToString(raw))
		if !strings.Contains(body, end) {
			return end
		}
	}
}

// profileMarker marks the line that sources an env file from a shell
// profile, so it is added once.
const profileMarker = "# cafard"

// Shell is the script for Linux and macOS: POSIX sh, run as `curl … | sh`.
//
// It runs under umask 077: what it creates — a token file, a client's new
// config holding the token, a backup — is its user's alone. A file already
// there keeps its mode, but for a Private one.
func Shell(b Bundle) string {
	var s strings.Builder
	fmt.Fprintf(&s, "#!/bin/sh\n# cafard: connect this machine as the agent %s.\n", shQuote(b.Name))
	s.WriteString(`set -eu
umask 077
stamp=$(date +%Y%m%d%H%M%S)
put() {
  # The file goes in place; whatever was there is kept beside it.
  mkdir -p "$(dirname "$1")"
  if [ -f "$1" ]; then cp "$1" "$1.bak.$stamp"; fi
  cat > "$1"
  echo "cafard: wrote $1"
}
installed() {
  for p in "$@"; do
    if command -v "$p" >/dev/null 2>&1; then return 0; fi
  done
  return 1
}
sourced=""
failed=""
`)
	if b.merges() {
		s.WriteString(shellMergeHelpers())
	}
	s.WriteString(shellPreflight(b))
	for _, g := range b.groups() {
		fmt.Fprintf(&s, "\n# %s\n", g.client)
		if len(g.detect) > 0 {
			s.WriteString("if installed")
			for _, p := range g.detect {
				s.WriteString(" " + shQuote(p))
			}
			s.WriteString("; then\n")
		}
		for _, f := range g.files {
			shellFile(&s, f)
		}
		if g.run != nil {
			shellRun(&s, *g.run)
		}
		if len(g.detect) > 0 {
			fmt.Fprintf(&s, "else\n  echo %s\nfi\n", shQuote("cafard: "+g.client+": not installed"))
		}
	}
	s.WriteString(`
if [ -n "$sourced" ]; then
  echo "cafard: done. Open a new shell, or run: . $sourced"
else
  echo "cafard: done."
fi
if [ -n "$failed" ]; then exit 1; fi
`)
	return s.String()
}

func shellFile(s *strings.Builder, f File) {
	path := `$HOME/` + f.Path
	switch {
	case f.Merge == nil:
		fmt.Fprintf(s, "put \"%s\" ", path)
	case f.Merge.Format == "env":
		fmt.Fprintf(s, "merge_env \"%s\" %s ", path, strings.Join(f.Merge.Keep, " "))
	default:
		fmt.Fprintf(s, "merge_into \"%s\" %s", path, f.Merge.Format)
		for _, p := range f.Merge.Owns {
			s.WriteString(" " + shQuote(p))
		}
		s.WriteString(" ")
	}
	writeHeredoc(s, f.Body)
	if f.Private {
		fmt.Fprintf(s, "chmod 600 \"%s\"\n", path)
	}
	if !f.Env {
		return
	}
	// Every shell that already has a profile sources it; with none, the one
	// sh itself reads is made.
	fmt.Fprintf(s, `added=""
for rc in "$HOME/.bashrc" "$HOME/.zshrc"; do
  if [ -f "$rc" ]; then
    if ! grep -qF '%[2]s' "$rc"; then
      printf '\n%[1]s\n[ -f "%[2]s" ] && . "%[2]s"\n' >> "$rc"
      echo "cafard: $rc now sources %[2]s"
    fi
    added=1
  fi
done
if [ -z "$added" ]; then
  if ! grep -qsF '%[2]s' "$HOME/.profile"; then
    printf '\n%[1]s\n[ -f "%[2]s" ] && . "%[2]s"\n' >> "$HOME/.profile"
    echo "cafard: $HOME/.profile now sources %[2]s"
  fi
fi
sourced="%[2]s"
`, profileMarker, path)
}

// shellRun runs a client's command. Its output is not shown, since it may
// repeat its arguments, token included; a failure says so and fails the
// script once the others are done.
func shellRun(s *strings.Builder, r Run) {
	prog := shQuote(r.Program)
	s.WriteString("ok=1\n")
	for _, st := range r.Steps {
		args := prog
		for _, a := range st.Args {
			args += " " + shQuote(a)
		}
		if st.MayFail {
			fmt.Fprintf(s, "%s >/dev/null 2>&1 || true\n", args)
			continue
		}
		fmt.Fprintf(s, "if ! %s >/dev/null; then\n  echo %s >&2\n  ok=\"\"\n  failed=1\nfi\n",
			args, shQuote("cafard: "+r.Client+": "+r.Program+" failed"))
	}
	fmt.Fprintf(s, "[ -z \"$ok\" ] || echo %s\n", shQuote("cafard: "+r.Client+": set up"))
}

// writeHeredoc feeds body to the command just written, as a quoted heredoc so
// nothing in it is expanded.
func writeHeredoc(s *strings.Builder, body string) {
	end := heredocEnd(body)
	fmt.Fprintf(s, "<<'%s'\n%s", end, body)
	if !strings.HasSuffix(body, "\n") {
		s.WriteString("\n")
	}
	s.WriteString(end + "\n")
}

// merges reports whether any file is merged, in one of formats or any.
func (b Bundle) merges(formats ...string) bool {
	for _, f := range b.Files {
		if f.Merge != nil && (len(formats) == 0 || slices.Contains(formats, f.Merge.Format)) {
			return true
		}
	}
	return false
}

// preflight are the env files whose kept variables must be on disk.
func (b Bundle) preflight() []File {
	var out []File
	for _, f := range b.Files {
		if f.Merge != nil && f.Merge.Format == "env" {
			out = append(out, f)
		}
	}
	return out
}

// shellPreflight checks, before the script writes anything, that each env
// file keeping variables from disk is there and holds them, for each client
// installed: written without them, the file would set nothing in their place.
func shellPreflight(b Bundle) string {
	need := b.preflight()
	if len(need) == 0 {
		return ""
	}
	var s strings.Builder
	s.WriteString("\n# What this keeps is on disk, or nothing is written.\nnever() { echo \"cafard: $1\" >&2; exit 1; }\n")
	for _, f := range need {
		path := `$HOME/` + f.Path
		cond := ""
		if len(f.Detect) > 0 {
			cond = "! installed"
			for _, p := range f.Detect {
				cond += " " + shQuote(p)
			}
			cond += " || "
		}
		fmt.Fprintf(&s, "%[2]s[ -s \"%[1]s\" ] || never \"there is no %[1]s\"\n", path, cond)
		for _, v := range f.Merge.Keep {
			fmt.Fprintf(&s, "%[3]sgrep -q '^export %[2]s=' \"%[1]s\" || never \"%[1]s sets no %[2]s\"\n", path, v, cond)
		}
	}
	return s.String()
}

// shellMergeHelpers defines merge_into, which folds the file on its stdin
// into the one at $1, and merge_env.
//
// A file that is not there yet, or empty, is simply written. One that cannot
// be merged — JSON on a machine with neither python3 nor node, or a file that
// does not parse — is left exactly as it is, with cafard's version beside it
// as .cafard and a line saying so: replacing it would be the loss this exists
// to prevent.
func shellMergeHelpers() string {
	var s strings.Builder
	s.WriteString(`
helpers=$(mktemp -d)
trap 'rm -rf "$helpers"' EXIT
`)
	for _, h := range []struct{ name, body string }{
		{"merge.py", mergePy}, {"merge.js", mergeJS}, {"merge.awk", mergeAwk}, {"env.awk", envAwk},
	} {
		fmt.Fprintf(&s, "cat > \"$helpers/%s\" ", h.name)
		writeHeredoc(&s, h.body)
	}
	s.WriteString(`# macOS ships a python3 that only offers to install the real one, so a
# python3 that cannot run is no python3.
have_python() { command -v python3 >/dev/null 2>&1 && python3 -c '' >/dev/null 2>&1; }
merge_into() {
  target=$1; format=$2; shift 2
  mkdir -p "$(dirname "$target")"
  cat > "$helpers/new"
  if [ ! -s "$target" ]; then
    mv "$helpers/new" "$target"
    echo "cafard: wrote $target"
    return 0
  fi
  cp "$target" "$target.bak.$stamp"
  status=0
  case "$format" in
    toml)
      awk -v new="$helpers/new" -f "$helpers/merge.awk" "$target" > "$helpers/merged" || status=$?
      [ "$status" -ne 0 ] || cat "$helpers/merged" > "$target" ;;
    json)
      if have_python; then python3 -I "$helpers/merge.py" "$target" "$helpers/new" "$@" || status=$?
      elif command -v node >/dev/null 2>&1; then node "$helpers/merge.js" "$target" "$helpers/new" "$@" || status=$?
      else status=127; fi ;;
  esac
  case "$status" in
    0) echo "cafard: updated $target, keeping the rest of it (it was $target.bak.$stamp)" ;;
    127)
      mv "$helpers/new" "$target.cafard"
      echo "cafard: left $target as it was: merging into it needs python3 or node. cafard's settings are in $target.cafard" >&2 ;;
    *)
      mv "$helpers/new" "$target.cafard"
      echo "cafard: left $target as it was: it could not be read. cafard's settings are in $target.cafard" >&2 ;;
  esac
  rm -f "$helpers/new" "$helpers/merged"
}
# An env file rewritten from the one on stdin, keeping the lines of the
# variables named from the file at $1. The preflight has checked the file and
# the lines are there.
merge_env() {
  target=$1; shift
  cat > "$helpers/new"
  cp "$target" "$target.bak.$stamp"
  awk -v keep="$*" -f "$helpers/env.awk" "$target" "$helpers/new" > "$helpers/merged"
  cat "$helpers/merged" > "$target"
  rm -f "$helpers/new" "$helpers/merged"
  echo "cafard: updated $target, keeping its $* (it was $target.bak.$stamp)"
}
`)
	return s.String()
}

// exportLine is `export NAME=value`, the only form the env files use.
var exportLine = regexp.MustCompile(`^export ([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

// PowerShell is the script for Windows, run as `irm … | iex`.
//
// Files are carried base64-encoded, so nothing in them — quotes, backticks,
// a $ — is ever read as PowerShell. An env file's variables are set for the
// user instead, which is what every new terminal then has, and for this one.
func PowerShell(b Bundle) string {
	var s strings.Builder
	fmt.Fprintf(&s, "# cafard: connect this machine as the agent %s.\n", psQuote(b.Name))
	s.WriteString(`$ErrorActionPreference = 'Stop'
$stamp = Get-Date -Format 'yyyyMMddHHmmss'
$cafardFailed = $false
function Put-CafardFile([string]$rel, [string]$b64) {
  $path = Join-Path $HOME $rel
  New-Item -ItemType Directory -Force -Path (Split-Path -Parent $path) | Out-Null
  if (Test-Path $path) { Copy-Item $path "$path.bak.$stamp" }
  [IO.File]::WriteAllBytes($path, [Convert]::FromBase64String($b64))
  Write-Host "cafard: wrote $path"
}
function Test-CafardInstalled([string[]]$names) {
  foreach ($n in $names) { if (Get-Command $n -ErrorAction SilentlyContinue) { return $true } }
  $false
}
`)
	if b.merges("json", "toml") {
		s.WriteString(mergePS1)
		s.WriteString(`
# A file that is not there yet is written; one that cannot be read as what it
# should be is left as it was, with cafard's version beside it.
function Merge-CafardFile([string]$rel, [string]$format, [string]$b64, [string[]]$owned) {
  $path = Join-Path $HOME $rel
  $new = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($b64))
  if (-not (Test-Path $path) -or (Get-Item $path).Length -eq 0) { Put-CafardFile $rel $b64; return }
  Copy-Item $path "$path.bak.$stamp"
  $ok = $true
  try {
    if ($format -eq 'toml') { Merge-CafardToml $path $new } else { $ok = Merge-CafardJson $path $new $owned }
  } catch { $ok = $false }
  if ($ok) {
    Write-Host "cafard: updated $path, keeping the rest of it (it was $path.bak.$stamp)"
  } else {
    [IO.File]::WriteAllText("$path.cafard", $new, (New-Object Text.UTF8Encoding $false))
    Write-Warning "cafard: left $path as it was: it could not be read. cafard's settings are in $path.cafard"
  }
}
`)
	}
	s.WriteString(psPreflight(b))
	for _, g := range b.groups() {
		fmt.Fprintf(&s, "\n# %s\n", g.client)
		if g.run != nil {
			psRun(&s, *g.run)
			continue
		}
		if len(g.detect) > 0 {
			fmt.Fprintf(&s, "if (Test-CafardInstalled %s) {\n", psList(g.detect))
		}
		for _, f := range g.files {
			psFile(&s, f)
		}
		if len(g.detect) > 0 {
			fmt.Fprintf(&s, "} else {\n  Write-Host %s\n}\n", psQuote("cafard: "+g.client+": not installed"))
		}
	}
	s.WriteString(`
Write-Host 'cafard: done. Open a new terminal for the variables to apply.'
if ($cafardFailed) { Write-Error 'cafard: some of it failed; see above.' }
`)
	return s.String()
}

func psFile(s *strings.Builder, f File) {
	if f.Env {
		var keep []string
		if f.Merge != nil {
			keep = f.Merge.Keep
		}
		for _, line := range strings.Split(f.Body, "\n") {
			m := exportLine.FindStringSubmatch(strings.TrimSpace(line))
			// A kept variable stays as the user has it set.
			if m == nil || slices.Contains(keep, m[1]) {
				continue
			}
			name, value := psQuote(m[1]), psQuote(unquote(m[2]))
			fmt.Fprintf(s, "[Environment]::SetEnvironmentVariable(%s, %s, 'User')\n", name, value)
			fmt.Fprintf(s, "Set-Item -Path ('env:' + %s) -Value %s\n", name, value)
		}
		fmt.Fprintf(s, "Write-Host %s\n", psQuote("cafard: "+f.Client+" variables set for new terminals"))
		return
	}
	path := f.Path
	if f.WindowsPath != "" {
		path = f.WindowsPath
	}
	rel := psQuote(strings.ReplaceAll(path, "/", `\`))
	b64 := base64.StdEncoding.EncodeToString([]byte(f.Body))
	if f.Merge == nil {
		fmt.Fprintf(s, "Put-CafardFile %s '%s'\n", rel, b64)
		return
	}
	owns := make([]string, len(f.Merge.Owns))
	for i, p := range f.Merge.Owns {
		owns[i] = psQuote(p)
	}
	fmt.Fprintf(s, "Merge-CafardFile %s %s '%s' @(%s)\n", rel, psQuote(f.Merge.Format), b64, strings.Join(owns, ", "))
}

// psRun runs a client's command. It looks the program up as an application
// only — an .exe or a .cmd shim — since a .ps1 one may be refused by the
// machine's execution policy, which `irm | iex` itself is not. Native output
// is dropped for the reason shellRun gives; under ErrorActionPreference Stop
// a program writing to stderr can throw, hence the try around each.
func psRun(s *strings.Builder, r Run) {
	fmt.Fprintf(s, "$cafardExe = Get-Command %s -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1\n", psQuote(r.Program))
	s.WriteString("if ($cafardExe) {\n  $cafardOk = $true\n")
	for _, st := range r.Steps {
		args := make([]string, len(st.Args))
		for i, a := range st.Args {
			args[i] = psQuote(a)
		}
		call := "& $cafardExe.Source " + strings.Join(args, " ")
		if st.MayFail {
			fmt.Fprintf(s, "  try { %s *> $null } catch {}\n", call)
			continue
		}
		fmt.Fprintf(s, "  try { %s | Out-Null; if ($LASTEXITCODE -ne 0) { $cafardOk = $false } } catch { $cafardOk = $false }\n", call)
	}
	fmt.Fprintf(s, "  if ($cafardOk) { Write-Host %s } else { Write-Warning %s; $cafardFailed = $true }\n",
		psQuote("cafard: "+r.Client+": set up"), psQuote("cafard: "+r.Client+": "+r.Program+" failed"))
	fmt.Fprintf(s, "} else {\n  Write-Host %s\n}\n", psQuote("cafard: "+r.Client+": not installed"))
}

// psPreflight is shellPreflight for Windows, where an env file is the user's
// variables: each kept one must be set, for each client installed, before
// anything is written. A throw under ErrorActionPreference Stop ends the
// script that iex is running.
func psPreflight(b Bundle) string {
	need := b.preflight()
	if len(need) == 0 {
		return ""
	}
	var s strings.Builder
	s.WriteString("\n# What this keeps is there, or nothing is written.\n")
	for _, f := range need {
		cond := ""
		if len(f.Detect) > 0 {
			cond = "(Test-CafardInstalled " + psList(f.Detect) + ") -and "
		}
		for _, v := range f.Merge.Keep {
			fmt.Fprintf(&s, "if (%s-not [Environment]::GetEnvironmentVariable(%s, 'User')) { throw %s }\n",
				cond, psQuote(v), psQuote("cafard: the user variable "+v+" is not set."))
		}
	}
	return s.String()
}

func psList(vs []string) string {
	q := make([]string, len(vs))
	for i, v := range vs {
		q[i] = psQuote(v)
	}
	return "@(" + strings.Join(q, ", ") + ")"
}

// psQuote is a single-quoted PowerShell string: only ' needs doubling.
func psQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// unquote drops one level of shell quoting from an export's value.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// Failure is the script sent instead when there is nothing to install — a
// link spent or unknown, or a person's: it fails loudly rather than as an
// HTML page piped into a shell. Write-Error rather than exit for PowerShell:
// iex runs in the person's own session, which exit would close.
func Failure(powershell bool, msg string) string {
	msg = "cafard: " + msg
	if powershell {
		return "Write-Error " + psQuote(msg) + "\n"
	}
	return "#!/bin/sh\necho " + shQuote(msg) + " >&2\nexit 1\n"
}

// shQuote is a single-quoted sh string.
func shQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}
