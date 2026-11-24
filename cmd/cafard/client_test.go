package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// agentOn makes an agent in project be98 on a served box and returns its
// token, taken from its install link as the PowerShell script takes it.
func agentOn(t *testing.T, b *box, addr string) string {
	t.Helper()
	b.must("create", "project", "be98")
	out := b.must("create", "agent", "lab-bench", "-project", "be98")
	m := linkRE.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no install link:\n%s", out)
	}
	resp, err := http.Post("http://"+addr+"/join/"+m[1]+"/token", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	tok := tokenRE.FindString(string(raw))
	if tok == "" {
		t.Fatalf("no token: %d %s", resp.StatusCode, raw)
	}
	return tok
}

type msg struct {
	Text  string `json:"text"`
	Files []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"files"`
}

// messages reads a conversation as the agent.
func messages(t *testing.T, addr, tok, conv string) []msg {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://"+addr+"/api/v1/projects/be98/conversations/"+conv+"/messages", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Messages []msg `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("%s: %v", conv, err)
	}
	return out.Messages
}

func helper(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := clientCommand(args[0], args[1:], strings.NewReader(stdin), &out, &errOut)
	return out.String(), errOut.String(), code
}

// say, send and log, against a real server, with the credentials the
// environment gives.
func TestHelpersSend(t *testing.T) {
	b := newBox(t)
	addr := b.serve()
	tok := agentOn(t, b, addr)
	t.Setenv("CAFARD_URL", "http://"+addr+"/")
	t.Setenv("CAFARD_TOKEN", tok)

	if out, errOut, code := helper(t, "", "say", "be98/wifi-drops", "flash", "done"); code != 0 || !strings.Contains(out, "be98/wifi-drops (new conversation)") {
		t.Fatalf("say: %d %q %q", code, out, errOut)
	}
	if _, _, code := helper(t, "from stdin\n", "say", "be98/wifi-drops"); code != 0 {
		t.Errorf("say from stdin: %d", code)
	}

	dir := t.TempDir()
	a, c := filepath.Join(dir, "boot.log"), filepath.Join(dir, "dmesg.txt")
	os.WriteFile(a, []byte("U-Boot 2024.01\n"), 0o600)
	os.WriteFile(c, []byte("[0.0] Linux\n"), 0o600)
	if out, errOut, code := helper(t, "", "send", "be98/wifi-drops", a, c, "-m", "two logs"); code != 0 {
		t.Fatalf("send: %d %q %q", code, out, errOut)
	}
	if _, errOut, code := helper(t, "", "send", "be98/wifi-drops", a, filepath.Join(dir, "missing")); code != 1 || !strings.Contains(errOut, "missing") {
		t.Errorf("a missing file: %d %q", code, errOut)
	}

	if _, errOut, code := helper(t, "line 1\nline 2\n", "log", "be98/wifi-drops", "console.log"); code != 0 {
		t.Fatalf("log: %d %q", code, errOut)
	}

	ms := messages(t, addr, tok, "wifi-drops")
	if len(ms) != 4 {
		t.Fatalf("%d messages: %+v", len(ms), ms)
	}
	if ms[0].Text != "flash done" || ms[1].Text != "from stdin" {
		t.Errorf("texts: %q %q", ms[0].Text, ms[1].Text)
	}
	if ms[2].Text != "two logs" || len(ms[2].Files) != 2 || ms[2].Files[1].Name != "dmesg.txt" {
		t.Errorf("the files went in one message: %+v", ms[2])
	}
	if len(ms[3].Files) != 1 || ms[3].Files[0].Name != "console.log" || ms[3].Files[0].Size != int64(len("line 1\nline 2\n")) {
		t.Errorf("the log: %+v", ms[3])
	}

	// A refusal is the server's sentence, and a failure.
	if _, errOut, code := helper(t, "", "say", "zeeos/x", "hello"); code != 1 || errOut == "" {
		t.Errorf("a project it is not in: %d %q", code, errOut)
	}
}

// run streams the command's output and says how it ended, with its code.
func TestHelperRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command is sh")
	}
	b := newBox(t)
	addr := b.serve()
	tok := agentOn(t, b, addr)
	t.Setenv("CAFARD_URL", "http://"+addr)
	t.Setenv("CAFARD_TOKEN", tok)

	out, errOut, code := helper(t, "", "run", "be98/rc3", "-name", "build.log", "--", "sh", "-c", "echo compiling; echo oops >&2; exit 3")
	if code != 3 || !strings.Contains(out, "compiling") || !strings.Contains(out, "oops") {
		t.Fatalf("run: %d %q %q", code, out, errOut)
	}
	ms := messages(t, addr, tok, "rc3")
	if len(ms) != 2 {
		t.Fatalf("%d messages: %+v", len(ms), ms)
	}
	if !strings.HasPrefix(ms[0].Text, "$ sh -c ") || len(ms[0].Files) != 1 || ms[0].Files[0].Name != "build.log" || ms[0].Files[0].Size != int64(len("compiling\noops\n")) {
		t.Errorf("the output: %+v", ms[0])
	}
	if !strings.Contains(ms[1].Text, "exited 3 after") {
		t.Errorf("the ending: %q", ms[1].Text)
	}

	if _, _, code := helper(t, "", "run", "be98/rc3", "--", "true"); code != 0 {
		t.Errorf("true: %d", code)
	}
	if _, _, code := helper(t, "", "run", "be98/rc3", "--", "no-such-command-here"); code != 127 {
		t.Errorf("a command that does not exist: %d", code)
	}
	if ms := messages(t, addr, tok, "rc3"); !strings.Contains(ms[len(ms)-1].Text, "could not start") || !strings.Contains(ms[len(ms)-2].Text, "finished after") {
		t.Errorf("endings: %q, %q", ms[len(ms)-2].Text, ms[len(ms)-1].Text)
	}
}

// The credentials come from ~/.config/cafard when the environment has none,
// as an agent's install link leaves them.
func TestCredentialsFromHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CAFARD_URL", "")
	t.Setenv("CAFARD_TOKEN", "")
	if _, errOut, code := helper(t, "", "say", "be98/x", "hi"); code != 1 || !strings.Contains(errOut, "CAFARD_URL") {
		t.Errorf("nothing configured: %d %q", code, errOut)
	}
	dir := filepath.Join(home, ".config", "cafard")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "config"), []byte("url=http://127.0.0.1:1\nagent=lab-bench\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "token"), []byte("cfd_ag_x\n"), 0o600)
	// Configured: it reaches for the address, which refuses.
	if _, errOut, code := helper(t, "", "say", "be98/x", "hi"); code != 1 || strings.Contains(errOut, "CAFARD_URL") {
		t.Errorf("configured: %d %q", code, errOut)
	}
	if _, errOut, code := helper(t, "", "say", "be98", "hi"); code != 2 || !strings.Contains(errOut, "project/conversation") {
		t.Errorf("a bad address: %d %q", code, errOut)
	}
}

func TestPartName(t *testing.T) {
	for _, c := range []struct {
		name string
		part int
		want string
	}{{"build.log", 1, "build.log"}, {"build.log", 2, "build.2.log"}, {"console", 3, "console.3"}} {
		if got := partName(c.name, c.part); got != c.want {
			t.Errorf("partName(%q, %d) = %q", c.name, c.part, got)
		}
	}
}
