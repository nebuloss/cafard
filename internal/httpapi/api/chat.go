package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rsc.io/qr"

	"cafard/internal/httpapi/httpx"
	"cafard/internal/live"
	"cafard/internal/service/access"
	"cafard/internal/store"
)

type signinKey struct{}

func withSignin(ctx context.Context, si store.Signin) context.Context {
	return context.WithValue(ctx, signinKey{}, si)
}

// signinFrom is the sign-in a request was made with, if it was made with one.
func signinFrom(ctx context.Context) (store.Signin, bool) {
	si, ok := ctx.Value(signinKey{}).(store.Signin)
	return si, ok
}

// readJSON decodes a small JSON body, refusing fields the route does not take.
func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &access.Refusal{Code: access.BadValue, Message: "The request is not the JSON this route takes: " + err.Error()}
	}
	return nil
}

// Signing in.

func (a *API) signout(w http.ResponseWriter, r *http.Request, who access.Who) {
	if si, ok := signinFrom(r.Context()); ok {
		a.acc.SignOut(r.Context(), who, si)
	}
	httpx.ClearChatCookie(w, a.secure)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) mySignins(w http.ResponseWriter, r *http.Request, who access.Who) {
	ss, err := a.acc.Signins(r.Context(), who)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	type signinJSON struct {
		ID       int64     `json:"id"`
		Surface  string    `json:"surface"`
		What     string    `json:"what"`
		From     string    `json:"from"`
		LastUsed time.Time `json:"last_used"`
		Since    time.Time `json:"since"`
		Current  bool      `json:"current,omitempty"`
	}
	cur, _ := signinFrom(r.Context())
	out := []signinJSON{}
	for _, s := range ss {
		out = append(out, signinJSON{ID: s.ID, Surface: s.Surface, What: access.DescribeAgent(s.UserAgent), From: s.From,
			LastUsed: s.LastUsed.UTC(), Since: s.CreatedAt.UTC(), Current: s.ID == cur.ID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"signins": out})
}

func (a *API) deleteMySignin(w http.ResponseWriter, r *http.Request, who access.Who) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	ss, err := a.acc.Signins(r.Context(), who)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	for _, s := range ss {
		if s.ID == id {
			if err := a.acc.SignOut(r.Context(), who, s); err != nil {
				a.fail(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeError(w, http.StatusNotFound, string(access.NotFound), "No such sign-in.")
}

// phoneCode is "Sign in another phone": a code a phone scans to be signed
// in as the person, for two minutes, once. It needs the chat app's sign-in,
// a person's, rather than a token; people have no password to ask again.
func (a *API) phoneCode(w http.ResponseWriter, r *http.Request, who access.Who) {
	if _, ok := signinFrom(r.Context()); !ok {
		writeError(w, http.StatusForbidden, string(access.NotAllowed), "Only a person signed in to the chat app can sign in another phone.")
		return
	}
	if ok, wait := a.limit.Allow("phone " + who.Name); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "slow_down", "Too many attempts. Wait a minute.")
		return
	}
	code, l, err := a.acc.IssueSigninLink(r.Context(), who)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	url := a.publicURL + "/join/" + code
	resp := map[string]any{"url": url, "expires_at": l.ExpiresAt.UTC()}
	if q, err := qr.Encode(url, qr.M); err == nil {
		resp["qr"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(q.PNG())
	}
	writeJSON(w, http.StatusCreated, resp)
}

// Conversations.

func (a *API) startConversation(w http.ResponseWriter, r *http.Request, who access.Who) {
	var in struct{ Text, Title string }
	if err := readJSON(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	sent, err := a.chat.StartConversation(r.Context(), who, r.PathValue("project"), in.Title, in.Text)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	resp := sentJSON(sent)
	if sent.Message.ID == 0 {
		delete(resp, "message")
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (a *API) updateConversation(w http.ResponseWriter, r *http.Request, who access.Who) {
	var in struct {
		Title  *string `json:"title"`
		Closed *bool   `json:"closed"`
	}
	if err := readJSON(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	project := r.PathValue("project")
	c, err := a.chat.UpdateConversation(r.Context(), who, project, r.PathValue("conversation"), in.Title, in.Closed)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": conversation(project, c)})
}

// Members.

type directoryJSON struct {
	memberJSON
	Description string `json:"description,omitempty"`
}

func (a *API) members(w http.ResponseWriter, r *http.Request, who access.Who) {
	ms, err := a.chat.Members(r.Context(), who, r.PathValue("project"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := []directoryJSON{}
	for _, m := range ms {
		out = append(out, directoryJSON{memberJSON: member(m), Description: m.Description})
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (a *API) addMember(w http.ResponseWriter, r *http.Request, who access.Who) {
	var in struct{ Name string }
	if err := readJSON(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.chat.AddMember(r.Context(), who, r.PathValue("project"), strings.TrimSpace(in.Name)); err != nil {
		a.fail(w, r, err)
		return
	}
	a.members(w, r, who)
}

func (a *API) removeMember(w http.ResponseWriter, r *http.Request, who access.Who) {
	if err := a.chat.RemoveMember(r.Context(), who, r.PathValue("project"), r.PathValue("name")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) directory(w http.ResponseWriter, r *http.Request, who access.Who) {
	ids, err := a.chat.Directory(r.Context(), who)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := []directoryJSON{}
	for _, i := range ids {
		out = append(out, directoryJSON{memberJSON: member(i), Description: i.Description})
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

// Files.

func (a *API) deleteFile(w http.ResponseWriter, r *http.Request, who access.Who) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, string(access.NotFound), "No such file.")
		return
	}
	if err := a.chat.DeleteFile(r.Context(), who, id); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Live.

// Event stream timing: notices are gathered for batchWindow so a burst of
// writes (a live log growing, a dozen reads) is one event per thing, and a
// comment line every keepAlive keeps proxies from closing a quiet stream.
const (
	batchWindow = 250 * time.Millisecond
	keepAlive   = 25 * time.Second
)

// events is the page's one stream of what changed (docs/design.md, "What
// carries over"): server-sent events, each naming a project or a
// conversation that changed, and nothing of what changed: the page reads it
// again through the routes above, which check access. A member hears only
// about projects they can read; when their own memberships change they are
// told "me", and the stream follows their new set of projects.
//
//	event: changed
//	data: {"topic":"conversation","project":3,"conversation":12}
func (a *API) events(w http.ResponseWriter, r *http.Request, who access.Who) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "This server cannot stream events.")
		return
	}
	ctx := r.Context()
	allowed, err := a.readable(ctx, who)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	sub := a.hub.Subscribe(accept(allowed, who.ID))
	defer a.hub.Unsubscribe(sub)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	}
	fmt.Fprint(w, "retry: 3000\n\n")
	send("ready", map[string]any{})
	flusher.Flush()

	alive := time.NewTicker(keepAlive)
	defer alive.Stop()
	var batch <-chan time.Time
	pending := map[string]bool{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-alive.C:
			fmt.Fprint(w, ": still here\n\n")
			flusher.Flush()
		case n, open := <-sub.C:
			if !open {
				return // the server is stopping
			}
			if n.Resync {
				send("changed", map[string]string{"topic": "resync"})
				flusher.Flush()
				continue
			}
			pending[n.Key] = true
			if batch == nil {
				batch = time.After(batchWindow)
			}
		case <-batch:
			batch = nil
			for key := range pending {
				p, ok := live.Parse(key)
				if !ok {
					continue
				}
				switch p.Kind {
				case 'm':
					// Their own access changed: follow the new set of projects,
					// and tell the page to read everything again.
					if allowed, err = a.readable(ctx, who); err != nil {
						return
					}
					a.hub.SetAccept(sub, accept(allowed, who.ID))
					send("changed", map[string]string{"topic": "me"})
				case 'p':
					if allowed[p.ProjectID] {
						send("changed", map[string]any{"topic": "project", "project": p.ProjectID})
					}
				case 'c':
					if allowed[p.ProjectID] {
						send("changed", map[string]any{"topic": "conversation", "project": p.ProjectID, "conversation": p.ConversationID})
					}
				}
			}
			pending = map[string]bool{}
			flusher.Flush()
		}
	}
}

// readable is the ids of the projects who can read now.
func (a *API) readable(ctx context.Context, who access.Who) (map[int64]bool, error) {
	ps, err := a.chat.Projects(ctx, who)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(ps))
	for _, p := range ps {
		out[p.ID] = true
	}
	return out, nil
}

// accept lets a stream hear its own member key and the projects it may read.
func accept(allowed map[int64]bool, me int64) func(string) bool {
	return func(key string) bool {
		p, ok := live.Parse(key)
		if !ok {
			return false
		}
		if p.Kind == 'm' {
			return p.IdentityID == me
		}
		return allowed[p.ProjectID]
	}
}
