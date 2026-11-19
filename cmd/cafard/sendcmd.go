package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/blob"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/web/view"
)

const sendUsage = `Usage:
  cafard send [--server URL] [--token T] [--channel C | --to PRINCIPAL] [--reply-to SEQ] [--image FILE]... [TEXT | -]

Posts a message, with images, as the token's principal:

  cafard send --image shot.png "login page after fix"
  make test 2>&1 | tail -20 | cafard send --channel dut1 -

TEXT "-" reads the text from stdin. Without --channel or --to the message
goes to #general; --to @humans addresses the humans of the channel.
--image takes a png, jpeg, webp, gif or heic file and may be repeated (the
server takes 10 files of 20 MiB by default). It prints the message's seq
and a link to it in the web UI. --server and --token may come from
CAFARD_SERVER and CAFARD_TOKEN.
`

// imageFlag collects repeated --image flags.
type imageFlag []string

func (f *imageFlag) String() string     { return strings.Join(*f, ",") }
func (f *imageFlag) Set(v string) error { *f = append(*f, v); return nil }

func cmdSend(ctx context.Context, e env, args []string) error {
	set := flag.NewFlagSet("cafard send", flag.ContinueOnError)
	rf := addRemoteFlags(set, e, "server URL")
	channel := set.String("channel", "", "channel (default general)")
	to := set.String("to", "", "a principal for a direct message, or @humans")
	replyTo := set.Int64("reply-to", 0, "seq of the message this answers")
	var images imageFlag
	set.Var(&images, "image", "image file to attach (repeatable)")
	set.Usage = func() { fmt.Fprint(e.stderr, sendUsage) }
	set.SetOutput(e.stderr)
	// Flags may come after the text too: cafard send "text" --image x.png.
	var words []string
	for {
		if err := set.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if set.NArg() == 0 {
			break
		}
		words = append(words, set.Arg(0))
		args = set.Args()[1:]
	}
	if len(words) > 1 {
		return fmt.Errorf("send takes one text argument; quote it (got %q and %q)", words[0], words[1])
	}
	base, err := serverBase(rf.server)
	if err != nil {
		return err
	}
	token := rf.bearer()
	if token == "" {
		return errors.New("--token (or CAFARD_TOKEN) is required")
	}
	text := ""
	if len(words) == 1 {
		text = words[0]
	}
	if text == "-" {
		b, err := io.ReadAll(io.LimitReader(e.stdin, messaging.MaxTextBytes+1))
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}
		text = strings.TrimRight(string(b), "\r\n")
	}
	if strings.TrimSpace(text) == "" && len(images) == 0 {
		fmt.Fprint(e.stderr, sendUsage)
		return errors.New("send needs a text or an --image")
	}
	for _, path := range images {
		if err := checkImage(path); err != nil {
			return err
		}
	}

	reply := ""
	if *replyTo != 0 {
		reply = strconv.FormatInt(*replyTo, 10)
	}
	// One line per form field of POST /api/messages; empty ones are not sent.
	fields := []formField{
		{"channel", *channel},
		{"to", *to},
		{"reply_to", reply},
		{"text", text},
	}
	entry, err := postMessage(ctx, base, token, fields, images)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "seq %d in %s\n", entry.Seq, view.ChatLabel(entry.Chat))
	if entry.Chat != "" {
		fmt.Fprintf(e.stdout, "%s%s#m%d\n", base, view.ChatHref(entry.Chat), entry.Seq)
	}
	return nil
}

// serverBase is --server checked, without a trailing slash or /mcp, so
// the address given to cafard mcp works here too.
func serverBase(server string) (string, error) {
	if server == "" {
		return "", errors.New("--server (or CAFARD_SERVER) is required")
	}
	u, err := url.Parse(strings.TrimRight(server, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("--server %q must be an http:// or https:// URL", server)
	}
	u.Path = strings.TrimSuffix(u.Path, "/mcp")
	u.RawQuery, u.Fragment = "", ""
	return strings.TrimRight(u.String(), "/"), nil
}

// checkImage refuses a missing or non-image file before anything is sent;
// the server has the last word on sizes and counts.
func checkImage(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("--image %s: no such file", path)
	}
	if err != nil {
		return fmt.Errorf("--image %s: %w", path, err)
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.IsDir() {
		return fmt.Errorf("--image %s is a directory", path)
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	if n == 0 {
		return fmt.Errorf("--image %s is empty", path)
	}
	if blob.DetectImage(head[:n]) == "" {
		return fmt.Errorf("--image %s is not a png, jpeg, webp, gif or heic image", path)
	}
	return nil
}

// postMessage sends POST /api/messages as multipart, streaming the files,
// and returns the committed entry.
func postMessage(ctx context.Context, base, token string, fields []formField, files []string) (dto.Entry, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		pw.CloseWithError(writeForm(mw, fields, files))
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/messages", pr)
	if err != nil {
		pr.Close()
		return dto.Entry{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cafard-send/"+version)
	resp, err := http.DefaultClient.Do(req)
	pr.Close()
	if err != nil {
		return dto.Entry{}, fmt.Errorf("sending to %s: %w", base, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return dto.Entry{}, fmt.Errorf("reading the answer of %s: %w", base, err)
	}
	if resp.StatusCode != http.StatusCreated {
		var e dto.Error
		if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
			return dto.Entry{}, fmt.Errorf("the server refused the message (%s): %s", e.Error.Code, e.Error.Message)
		}
		return dto.Entry{}, fmt.Errorf("the server answered %s", resp.Status)
	}
	var entry dto.Entry
	if err := json.Unmarshal(body, &entry); err != nil {
		return dto.Entry{}, fmt.Errorf("the server's answer is not a message: %w", err)
	}
	return entry, nil
}

// formField is one text field of the multipart form.
type formField struct{ name, value string }

func writeForm(mw *multipart.Writer, fields []formField, files []string) error {
	for _, f := range fields {
		if f.value != "" {
			if err := mw.WriteField(f.name, f.value); err != nil {
				return err
			}
		}
	}
	for _, path := range files {
		if err := writeFile(mw, path); err != nil {
			return err
		}
	}
	return mw.Close()
}

func writeFile(mw *multipart.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w, err := mw.CreateFormFile("files[]", filepath.Base(path))
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}
