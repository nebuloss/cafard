package mcpproxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func writePNG(t *testing.T, dir, name string) (string, []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{200, 10, 10, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, buf.Bytes()
}

func toolText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func errorCode(res *mcp.CallToolResult) string {
	b, _ := json.Marshal(res.StructuredContent)
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(b, &body)
	return body.Code
}

func TestSendAdvertisesImagePaths(t *testing.T) {
	r := newToyRemote(t)
	p, err := New(context.Background(), Options{Server: r.srv.URL, Token: "cfd_toy"})
	if err != nil {
		t.Fatal(err)
	}
	cs := connectThrough(t, p, nil)
	for tool, err := range cs.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Properties map[string]struct {
				Type        any    `json:"type"`
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(b, &schema); err != nil {
			t.Fatal(err)
		}
		_, has := schema.Properties[imagePathsArg]
		switch tool.Name {
		case "send":
			got := schema.Properties[imagePathsArg]
			if got.Type != "array" || !strings.Contains(got.Description, "read by the cafard mcp proxy on this machine") {
				t.Errorf("send's image_paths = %+v (schema %s)", got, b)
			}
			if _, ok := schema.Properties["images"]; !ok {
				t.Errorf("send lost images: %s", b)
			}
		default:
			if has {
				t.Errorf("%s has image_paths too", tool.Name)
			}
		}
	}
}

func TestImagePathsAreReadHereAndNeverForwarded(t *testing.T) {
	r := newToyRemote(t)
	p, err := New(context.Background(), Options{Server: r.srv.URL, Token: "cfd_toy"})
	if err != nil {
		t.Fatal(err)
	}
	cs := connectThrough(t, p, nil)
	dir := t.TempDir()
	shot, data := writePNG(t, dir, "shot.png")
	inline := base64.StdEncoding.EncodeToString(data)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "send", Arguments: map[string]any{
		"text": "login page after fix", "image_paths": []string{shot}, "section": "login",
		"images": []any{map[string]any{"data": inline, "mime": "image/png"}},
	}})
	if err != nil || res.IsError {
		t.Fatalf("send = %v %v", res, err)
	}
	if got := toolText(res); got != "sent with 2 images" {
		t.Errorf("remote said %q", got)
	}
	var args map[string]json.RawMessage
	select {
	case raw := <-r.sent:
		if err := json.Unmarshal(raw, &args); err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the remote got no send")
	}
	if _, ok := args[imagePathsArg]; ok || strings.Contains(string(args["images"]), dir) {
		t.Errorf("paths reached the remote: %v", args)
	}
	if string(args["section"]) != `"login"` || string(args["text"]) != `"login page after fix"` {
		t.Errorf("other arguments changed on the way: %v", args)
	}
	var images []struct{ Data, Mime string }
	if err := json.Unmarshal(args["images"], &images); err != nil || len(images) != 2 {
		t.Fatalf("images = %s %v", args["images"], err)
	}
	if images[1].Mime != "image/png" || images[1].Data != inline {
		t.Errorf("the file became %s %d bytes of base64", images[1].Mime, len(images[1].Data))
	}

	// A send without paths passes untouched.
	if res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "send", Arguments: map[string]any{"text": "plain"}}); err != nil || res.IsError {
		t.Fatalf("plain send = %v %v", res, err)
	}
	if raw := <-r.sent; string(raw) != `{"text":"plain"}` {
		t.Errorf("plain send reached the remote as %s", raw)
	}
}

func TestImagePathsAreChecked(t *testing.T) {
	r := newToyRemote(t)
	p, err := New(context.Background(), Options{Server: r.srv.URL, Token: "cfd_toy"})
	if err != nil {
		t.Fatal(err)
	}
	cs := connectThrough(t, p, nil)
	dir := t.TempDir()
	shot, _ := writePNG(t, dir, "shot.png")
	text := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(text, []byte("not an image at all\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.png")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxImageBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	empty := filepath.Join(dir, "empty.png")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	seven := make([]string, MaxImagePaths+1)
	for i := range seven {
		seven[i] = shot
	}
	for _, c := range []struct {
		name  string
		paths any
		code  string
		says  string
	}{
		{"missing", []string{filepath.Join(dir, "nope.png")}, "not_found", "no such file"},
		{"not an image", []string{text}, "bad_request", "not a png"},
		{"a directory", []string{dir}, "bad_request", "directory"},
		{"too large", []string{big}, "too_large", "10 MiB"},
		{"empty", []string{empty}, "bad_request", "empty"},
		{"too many", seven, "too_large", "at most 6"},
		{"not a list", "shot.png", "bad_request", "list of file paths"},
	} {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "send", Arguments: map[string]any{"text": "x", "image_paths": c.paths}})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !res.IsError || errorCode(res) != c.code || !strings.Contains(toolText(res), c.says) {
			t.Errorf("%s: %v %q code %q", c.name, res.IsError, toolText(res), errorCode(res))
		}
	}
	select {
	case raw := <-r.sent:
		t.Errorf("a refused send reached the remote: %s", raw)
	default:
	}
}
