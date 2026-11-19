package mcpproxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/blob"
	"github.com/nebuloss/cafard/internal/dto"
)

// Limits of image_paths. The remote takes at most 6 inline images of
// 10 MiB each (messaging.MaxAgentImages and its inline limit); the proxy
// cannot read the remote's configuration, so it checks the same numbers
// to fail before uploading, and the remote still decides.
const (
	MaxImagePaths = 6
	MaxImageBytes = 10 << 20
)

const (
	sendTool       = "send"
	imagePathsArg  = "image_paths"
	imagePathsHelp = "local files (screenshots) to attach, png, jpeg, webp or gif, at most 6 of 10 MiB; read by the cafard mcp proxy on this machine. Prefer absolute paths: a relative one is relative to the proxy's working directory."
)

// Instructions is what the proxy adds to the remote's instructions when it
// offers image_paths.
const Instructions = `This is the cafard mcp proxy on your machine: to share a screenshot or any image file, pass its path in send(image_paths=[...]) instead of base64 images.`

// withImagePaths returns a copy of the send tool whose input schema also
// has image_paths, or false when the remote's send takes no images.
func withImagePaths(t *mcp.Tool) (*mcp.Tool, bool) {
	if t.Name != sendTool {
		return t, false
	}
	b, err := json.Marshal(t.InputSchema)
	if err != nil {
		return t, false
	}
	var schema map[string]any
	if json.Unmarshal(b, &schema) != nil {
		return t, false
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil || props["images"] == nil || props[imagePathsArg] != nil {
		return t, false
	}
	props[imagePathsArg] = map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"maxItems":    MaxImagePaths,
		"description": imagePathsHelp,
	}
	out := *t
	out.InputSchema = schema
	return &out, true
}

// rewriteImagePaths replaces image_paths in the arguments of send by the
// images entries the remote takes: each file read, typed by its content
// and base64 encoded. The paths never reach the remote. A nil error with
// unchanged args means there were no paths.
func rewriteImagePaths(args json.RawMessage) (json.RawMessage, *dto.APIError) {
	if len(args) == 0 || !bytes.Contains(args, []byte(imagePathsArg)) {
		return args, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return args, nil // not an object: the remote says what is wrong
	}
	raw, ok := fields[imagePathsArg]
	if !ok {
		return args, nil
	}
	delete(fields, imagePathsArg)
	var paths []string
	if string(raw) != "null" {
		if err := json.Unmarshal(raw, &paths); err != nil {
			return nil, dto.Errorf(dto.CodeBadRequest, "image_paths must be a list of file paths.")
		}
	}
	// Inline images pass as they came, for the remote to check.
	var images []json.RawMessage
	if r, ok := fields["images"]; ok && string(r) != "null" {
		if err := json.Unmarshal(r, &images); err != nil {
			return nil, dto.Errorf(dto.CodeBadRequest, "images must be a list of {data, mime}.")
		}
	}
	if n := len(images) + len(paths); n > MaxImagePaths {
		return nil, dto.Errorf(dto.CodeTooLarge, "A message carries at most %d images; this one has %d.", MaxImagePaths, n)
	}
	for _, path := range paths {
		img, apiErr := readImage(path)
		if apiErr != nil {
			return nil, apiErr
		}
		b, err := json.Marshal(img)
		if err != nil {
			return nil, dto.Errorf(dto.CodeBadRequest, "%s could not be encoded.", path)
		}
		images = append(images, b)
	}
	if len(images) > 0 {
		b, err := json.Marshal(images)
		if err != nil {
			return nil, dto.Errorf(dto.CodeBadRequest, "The images could not be encoded.")
		}
		fields["images"] = b
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, dto.Errorf(dto.CodeBadRequest, "The arguments could not be encoded.")
	}
	return out, nil
}

func readImage(path string) (dto.ImageUpload, *dto.APIError) {
	if path == "" {
		return dto.ImageUpload{}, dto.Errorf(dto.CodeBadRequest, "An image path is empty.")
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return dto.ImageUpload{}, dto.Errorf(dto.CodeNotFound, "%s: no such file on the machine that runs cafard mcp.", path)
	}
	if err != nil {
		return dto.ImageUpload{}, dto.Errorf(dto.CodeBadRequest, "%s could not be opened: %v.", path, err)
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.IsDir() {
		return dto.ImageUpload{}, dto.Errorf(dto.CodeBadRequest, "%s is a directory, not an image.", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxImageBytes+1))
	if err != nil {
		return dto.ImageUpload{}, dto.Errorf(dto.CodeBadRequest, "%s could not be read: %v.", path, err)
	}
	if len(data) > MaxImageBytes {
		return dto.ImageUpload{}, dto.Errorf(dto.CodeTooLarge, "%s is larger than %d MiB, the limit for an image sent by an agent.", path, MaxImageBytes>>20)
	}
	if len(data) == 0 {
		return dto.ImageUpload{}, dto.Errorf(dto.CodeBadRequest, "%s is empty.", path)
	}
	mime := blob.DetectImage(data[:min(len(data), 512)])
	if mime == "" {
		return dto.ImageUpload{}, dto.Errorf(dto.CodeBadRequest, "%s is not a png, jpeg, webp or gif image.", path)
	}
	return dto.ImageUpload{Data: base64.StdEncoding.EncodeToString(data), Mime: mime}, nil
}

// toolError is a tool error in the remote's shape (mcpserver.toolError),
// so clients read its code the same way.
func toolError(e *dto.APIError) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError:           true,
		Content:           []mcp.Content{&mcp.TextContent{Text: e.Message}},
		StructuredContent: e.Body(),
	}
}

func joinInstructions(remote string) string {
	if remote == "" {
		return Instructions
	}
	return fmt.Sprintf("%s\n\n%s", remote, Instructions)
}
