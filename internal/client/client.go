// Package client is mouchard: say, send, log and run (cli.go), on a
// machine that holds an agent's or a device's token, over /api/v1. It adds no ability of its own (CLAUDE.md, rule 6): each
// call is one of the curl lines docs/design.md shows, made robust, with an
// ending a script can act on.
//
// Credentials come from the environment, CAFARD_URL and CAFARD_TOKEN, or
// else from ~/.config/cafard: url= in config, the token alone in token. An
// agent's install link writes that layout already, so a machine set up for
// MCP can send with nothing more.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Client calls one cafard with one token.
type Client struct {
	// URL is the public listener's address, without a trailing slash.
	URL   string
	Token string
	// HTTP has no timeout: a log streams for as long as it is written.
	HTTP *http.Client
	// Agent names the helper to the server, for its log.
	Agent string
}

// FromEnv is the client the environment and ~/.config/cafard describe. The
// environment wins, so a script can send as another identity than the
// machine's own.
func FromEnv() (*Client, error) {
	u, tok := os.Getenv("CAFARD_URL"), os.Getenv("CAFARD_TOKEN")
	dir := ""
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, ".config", "cafard")
	}
	if u == "" && dir != "" {
		u = readKey(filepath.Join(dir, "config"), "url")
	}
	if tok == "" && dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, "token")); err == nil {
			tok = strings.TrimSpace(string(b))
		}
	}
	switch {
	case u == "" && tok == "":
		return nil, errors.New("no cafard to send to: set CAFARD_URL and CAFARD_TOKEN, or run this machine's setup link, which writes ~/.config/cafard")
	case u == "":
		return nil, errors.New("no cafard address: set CAFARD_URL, or url= in ~/.config/cafard/config")
	case tok == "":
		return nil, errors.New("no token: set CAFARD_TOKEN, or put it in ~/.config/cafard/token")
	}
	return &Client{URL: strings.TrimSuffix(u, "/"), Token: tok, HTTP: &http.Client{}}, nil
}

// readKey reads key=value from a small file of such lines.
func readKey(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "="); ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Address is a conversation: project/conversation, created by the first
// thing sent to it.
type Address struct{ Project, Conversation string }

func (a Address) String() string { return a.Project + "/" + a.Conversation }

// ParseAddress reads project/conversation.
func ParseAddress(s string) (Address, error) {
	p, c, ok := strings.Cut(strings.Trim(s, "/"), "/")
	if !ok || p == "" || c == "" || strings.Contains(c, "/") {
		return Address{}, fmt.Errorf("%q is not a conversation: give it as project/conversation, e.g. be98/wifi-drops", s)
	}
	return Address{Project: p, Conversation: c}, nil
}

// Sent is what the server says it stored.
type Sent struct {
	Address string `json:"address"`
	Created bool   `json:"created"`
	Message struct {
		ID int64 `json:"id"`
	} `json:"message"`
	// Stopped says why an upload ended before its body did; what arrived is
	// stored.
	Stopped string `json:"stopped"`
}

// Error is a refusal from the server: what it said, and its code.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("cafard answered %d", e.Status)
}

// Say posts text to a conversation.
func (c *Client) Say(ctx context.Context, to Address, text string) (Sent, error) {
	return c.do(ctx, http.MethodPost, c.sendURL(to, "", nil), "text/plain; charset=utf-8", strings.NewReader(text), int64(len(text)))
}

// File is one file to send.
type File struct {
	Name string
	// Type is its Content-Type; empty lets the server decide from the name.
	Type string
	Body io.Reader
	// Size is the body's length, or -1 to stream it chunked: a log written
	// as it is sent.
	Size int64
	// Text goes with it as the message's text; ignored with Message.
	Text string
	// Message adds the file to that message of the sender's, instead of
	// starting a new one.
	Message int64
}

// Send sends one file.
func (c *Client) Send(ctx context.Context, to Address, f File) (Sent, error) {
	q := url.Values{}
	if f.Message > 0 {
		q.Set("message", strconv.FormatInt(f.Message, 10))
	} else if f.Text != "" {
		q.Set("text", f.Text)
	}
	return c.do(ctx, http.MethodPut, c.sendURL(to, f.Name, q), f.Type, f.Body, f.Size)
}

func (c *Client) sendURL(to Address, name string, q url.Values) string {
	u := c.URL + "/api/v1/send/" + url.PathEscape(to.Project) + "/" + url.PathEscape(to.Conversation)
	if name != "" {
		u += "/" + url.PathEscape(name)
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (c *Client) do(ctx context.Context, method, u, ctype string, body io.Reader, size int64) (Sent, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return Sent{}, err
	}
	req.ContentLength = size
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	agent := c.Agent
	if agent == "" {
		agent = "mouchard"
	}
	req.Header.Set("User-Agent", agent)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Sent{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var s Sent
	// A 413 still stored what arrived before the limit, and says so.
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusRequestEntityTooLarge {
		if err := json.Unmarshal(raw, &s); err == nil && s.Message.ID > 0 {
			return s, nil
		}
	}
	var e struct {
		Code    string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &e)
	return Sent{}, &Error{Status: resp.StatusCode, Code: e.Code, Message: e.Message}
}

// Duration says how long something took, as a person reads it: 45 s,
// 3 min 12 s, 2 h 05 min.
func Duration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min %d s", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%d h %02d min", int(d.Hours()), int(d.Minutes())%60)
	}
}
