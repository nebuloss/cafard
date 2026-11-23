// Package api is /api/v1 on the public listener: the one implementation
// that the chat app, curl and the MCP server all call (docs/design.md,
// "Surfaces"). Nothing the MCP server or the chat app does is out of its
// reach, and it decides nothing itself: service/chat does the work, and asks
// service/access first.
//
// Every route but sign-in needs a credential: an agent's or a device's
// token, `Authorization: Bearer cfd_…`, or a person's chat sign-in cookie.
// A write made with the cookie must come from this site's own pages
// (httpx.SameOrigin); one made with a token carries its credential in a
// header no other site can set.
//
//	POST   /api/v1/signin                               a person signs in to the chat app
//	POST   /api/v1/signout
//	GET    /api/v1/me                                   who the credential is, and their projects
//	GET    /api/v1/me/signins                           a person's sign-ins
//	DELETE /api/v1/me/signins/{id}
//	POST   /api/v1/me/phone                             {password}: a QR code that signs in another phone
//	GET    /api/v1/events                               what changed, as server-sent events (live)
//	PUT    /api/v1/send/{project}/{conversation}/{name} a file; `curl -T`; ?message=ID adds it to your message
//	POST   /api/v1/send/{project}/{conversation}        text; `curl --data-binary`
//	GET    /api/v1/projects                             my projects
//	GET    /api/v1/projects/{project}/conversations     a project's conversations, with my unread counts
//	POST   /api/v1/projects/{project}/conversations     {text, title?}: start one from its first message
//	PATCH  /api/v1/projects/{project}/conversations/{conversation}  {title?, closed?}
//	GET    /api/v1/projects/{project}/conversations/{conversation}/messages
//	GET    /api/v1/projects/{project}/members
//	POST   /api/v1/projects/{project}/members           {name}: people add existing people and agents
//	DELETE /api/v1/projects/{project}/members/{name}
//	GET    /api/v1/directory                            the people and agents a person can add
//	GET    /api/v1/files/{id}                           a file's bytes
//	GET    /api/v1/files/{id}/lines                     a page of a text file
//	DELETE /api/v1/files/{id}                           its sender or an admin
//
// A refusal is JSON, {"error": code, "message": sentence}, with the status
// the code calls for; the sentence says what to do instead where there is
// something to do.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cafard/internal/httpapi/httpx"
	"cafard/internal/live"
	"cafard/internal/ratelimit"
	"cafard/internal/service/access"
	"cafard/internal/service/chat"
	"cafard/internal/store"
)

// MaxUpload is the most one request may send. A live log that reaches it is
// stored up to there, and the sender told why it stopped.
const MaxUpload = 256 << 20

type API struct {
	log       *slog.Logger
	acc       *access.Service
	chat      *chat.Service
	hub       *live.Hub
	publicURL string
	secure    bool
	limit     *ratelimit.Limiter
}

// New builds /api/v1. hub is where live notices come from; publicURL is
// where this listener is reached, for the codes that sign in a phone.
func New(log *slog.Logger, acc *access.Service, c *chat.Service, hub *live.Hub, publicURL string) *API {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &API{log: log, acc: acc, chat: c, hub: hub, publicURL: strings.TrimSuffix(publicURL, "/"),
		secure: strings.HasPrefix(publicURL, "https://"), limit: ratelimit.New(10, time.Minute)}
}

// Register adds the routes to mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/signin", a.signin)
	mux.HandleFunc("POST /api/v1/signout", a.auth(a.signout))
	mux.HandleFunc("GET /api/v1/me", a.auth(a.me))
	mux.HandleFunc("GET /api/v1/me/signins", a.auth(a.mySignins))
	mux.HandleFunc("DELETE /api/v1/me/signins/{id}", a.auth(a.deleteMySignin))
	mux.HandleFunc("POST /api/v1/me/phone", a.auth(a.phoneCode))
	mux.HandleFunc("GET /api/v1/events", a.auth(a.events))
	mux.HandleFunc("PUT /api/v1/send/{project}/{conversation}/{name}", a.auth(a.sendFile))
	// `curl -T - …/wifi-drops/` sends no name: answered by SendFile with how
	// to give one, rather than by a 404 that says nothing.
	mux.HandleFunc("PUT /api/v1/send/{project}/{conversation}/{$}", a.auth(a.sendFile))
	mux.HandleFunc("POST /api/v1/send/{project}/{conversation}/{name}", a.auth(a.sendFile))
	mux.HandleFunc("POST /api/v1/send/{project}/{conversation}", a.auth(a.sendText))
	mux.HandleFunc("GET /api/v1/projects", a.auth(a.projects))
	mux.HandleFunc("GET /api/v1/projects/{project}/conversations", a.auth(a.conversations))
	mux.HandleFunc("POST /api/v1/projects/{project}/conversations", a.auth(a.startConversation))
	mux.HandleFunc("PATCH /api/v1/projects/{project}/conversations/{conversation}", a.auth(a.updateConversation))
	mux.HandleFunc("GET /api/v1/projects/{project}/conversations/{conversation}/messages", a.auth(a.messages))
	mux.HandleFunc("GET /api/v1/projects/{project}/members", a.auth(a.members))
	mux.HandleFunc("POST /api/v1/projects/{project}/members", a.auth(a.addMember))
	mux.HandleFunc("DELETE /api/v1/projects/{project}/members/{name}", a.auth(a.removeMember))
	mux.HandleFunc("GET /api/v1/directory", a.auth(a.directory))
	mux.HandleFunc("GET /api/v1/files/{id}", a.auth(a.file))
	mux.HandleFunc("DELETE /api/v1/files/{id}", a.auth(a.deleteFile))
	mux.HandleFunc("GET /api/v1/files/{id}/lines", a.auth(a.lines))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("No route %s %s. The routes are listed in `go doc cafard/internal/httpapi/api`.", r.Method, r.URL.Path))
	})
}

type handler func(w http.ResponseWriter, r *http.Request, who access.Who)

// auth resolves the credential to the member it belongs to: a bearer token
// if the request has one, else the chat sign-in cookie.
func (a *API) auth(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from := clientIP(r)
		if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && token != "" {
			id, err := a.acc.Authenticate(r.Context(), strings.TrimSpace(token), from)
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer realm="cafard", error="invalid_token"`)
				a.fail(w, r, err)
				return
			}
			next(w, r, access.Who{Identity: id, Origin: "api · " + from})
			return
		}
		cookie := httpx.ChatCookieValue(r)
		if cookie == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="cafard"`)
			writeError(w, http.StatusUnauthorized, string(access.Unauthorized),
				`Sign in, or send a token: -H "Authorization: Bearer cfd_…". An admin makes one with cafard admin create agent or device.`)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !httpx.SameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross_site", "Writes from another site are refused.")
			return
		}
		id, si, err := a.acc.Session(r.Context(), access.ChatSurface, cookie, from)
		if err != nil {
			if access.Is(err, access.Unauthorized) {
				httpx.ClearChatCookie(w, a.secure)
			}
			a.fail(w, r, err)
			return
		}
		r = r.WithContext(withSignin(r.Context(), si))
		next(w, r, access.Who{Identity: id, Origin: "chat · " + from})
	}
}

func clientIP(r *http.Request) string { return httpx.ClientIP(r) }

var statusOf = map[access.Code]int{
	access.Unauthorized: http.StatusUnauthorized,
	// 403 and not 401: the chat app treats 401 as "you are signed out".
	access.Wrong:           http.StatusForbidden,
	access.NotAllowed:      http.StatusForbidden,
	access.NotAdmin:        http.StatusForbidden,
	access.NotMember:       http.StatusForbidden,
	access.SendOnly:        http.StatusForbidden,
	access.Paused:          http.StatusForbidden,
	access.Removed:         http.StatusForbidden,
	access.NoSharedProject: http.StatusForbidden,
	access.Self:            http.StatusForbidden,
	access.Archived:        http.StatusConflict,
	access.Exists:          http.StatusConflict,
	access.NotFound:        http.StatusNotFound,
	access.BadName:         http.StatusBadRequest,
	access.BadValue:        http.StatusBadRequest,
	chat.Gone:              http.StatusGone,
}

// fail answers err: a refusal as itself, anything else as a 500 that names
// nothing internal, logged in full.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ref *access.Refusal
	if errors.As(err, &ref) {
		status, ok := statusOf[ref.Code]
		if !ok {
			status = http.StatusBadRequest
		}
		writeError(w, status, string(ref.Code), ref.Message)
		return
	}
	a.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "cafard could not do that; the error is in its log.")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// The wire shapes.

type memberJSON struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name,omitempty"`
	Removed     bool   `json:"removed,omitempty"`
}

func member(i store.Identity) memberJSON {
	return memberJSON{Name: i.Name, Kind: string(i.Kind), DisplayName: i.DisplayName, Removed: i.Deleted()}
}

type fileJSON struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"type"`
	Size        int64  `json:"size"`
	State       string `json:"state"`
	URL         string `json:"url"`
	LinesURL    string `json:"lines_url,omitempty"`
}

func file(f store.File) fileJSON {
	j := fileJSON{ID: f.ID, Name: f.Name, ContentType: f.ContentType, Size: f.Size, State: string(f.State),
		URL: fmt.Sprintf("/api/v1/files/%d", f.ID)}
	if isText(f.ContentType) {
		j.LinesURL = j.URL + "/lines"
	}
	return j
}

func isText(ctype string) bool {
	return strings.HasPrefix(ctype, "text/") || strings.Contains(ctype, "json") || strings.Contains(ctype, "yaml")
}

type messageJSON struct {
	ID     int64      `json:"id"`
	Author memberJSON `json:"author"`
	Text   string     `json:"text,omitempty"`
	At     time.Time  `json:"at"`
	Files  []fileJSON `json:"files,omitempty"`
}

func message(m store.Message) messageJSON {
	j := messageJSON{ID: m.ID, Author: member(m.Author), Text: m.Text, At: m.CreatedAt.UTC()}
	for _, f := range m.Files {
		j.Files = append(j.Files, file(f))
	}
	return j
}

type conversationJSON struct {
	ID       int64      `json:"id"`
	Address  string     `json:"address"`
	Name     string     `json:"name"`
	Title    string     `json:"title"`
	Messages int        `json:"messages"`
	Unread   int        `json:"unread,omitempty"`
	LastAt   *time.Time `json:"last_at,omitempty"`
	Closed   bool       `json:"closed,omitempty"`
}

func conversation(project string, c store.Conversation) conversationJSON {
	j := conversationJSON{ID: c.ID, Address: project + "/" + c.Name, Name: c.Name, Title: c.Title, Messages: c.Messages, Closed: c.ClosedAt != nil}
	if c.LastAt != nil {
		t := c.LastAt.UTC()
		j.LastAt = &t
	}
	return j
}

func (a *API) sendFile(w http.ResponseWriter, r *http.Request, who access.Who) {
	body := http.MaxBytesReader(w, r.Body, MaxUpload)
	q := r.URL.Query()
	var sent chat.Sent
	var err error
	if id, perr := strconv.ParseInt(q.Get("message"), 10, 64); perr == nil && id > 0 {
		sent, err = a.chat.AttachFile(r.Context(), who, r.PathValue("project"), r.PathValue("conversation"), id,
			r.PathValue("name"), r.Header.Get("Content-Type"), body)
	} else {
		sent, err = a.chat.SendFile(r.Context(), who, r.PathValue("project"), r.PathValue("conversation"),
			r.PathValue("name"), r.Header.Get("Content-Type"), q.Get("text"), body)
	}
	if sent.Message.ID == 0 {
		a.fail(w, r, err)
		return
	}
	resp := sentJSON(sent)
	status := http.StatusCreated
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		status = http.StatusRequestEntityTooLarge
		resp["stopped"] = fmt.Sprintf("The upload reached the %d MB a request may send; what arrived before that is stored.", MaxUpload>>20)
	case err != nil:
		// The sender went away mid-upload; what arrived is kept.
		a.log.Info("upload ended early", "file", sent.Message.Files[0].ID, "err", err)
		resp["stopped"] = "The upload ended early; what arrived is stored."
	}
	writeJSON(w, status, resp)
}

func (a *API) sendText(w http.ResponseWriter, r *http.Request, who access.Who) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, chat.MaxText+1))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	sent, err := a.chat.SendText(r.Context(), who, r.PathValue("project"), r.PathValue("conversation"), string(raw))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, sentJSON(sent))
}

func sentJSON(s chat.Sent) map[string]any {
	return map[string]any{
		"address":      s.Project.Name + "/" + s.Conversation.Name,
		"created":      s.Created,
		"conversation": conversation(s.Project.Name, s.Conversation),
		"message":      message(s.Message),
	}
}

func (a *API) me(w http.ResponseWriter, r *http.Request, who access.Who) {
	type meJSON struct {
		memberJSON
		Admin bool `json:"admin,omitempty"`
	}
	resp := map[string]any{"member": meJSON{memberJSON: member(who.Identity), Admin: who.Admin}}
	if ps, err := a.chat.Projects(r.Context(), who); err == nil {
		names := []string{}
		for _, p := range ps {
			names = append(names, p.Name)
		}
		resp["projects"] = names
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) projects(w http.ResponseWriter, r *http.Request, who access.Who) {
	ps, err := a.chat.Projects(r.Context(), who)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	type projectJSON struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Archived bool   `json:"archived,omitempty"`
	}
	out := []projectJSON{}
	for _, p := range ps {
		out = append(out, projectJSON{ID: p.ID, Name: p.Name, Archived: p.Archived()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

func (a *API) conversations(w http.ResponseWriter, r *http.Request, who access.Who) {
	project := r.PathValue("project")
	cs, err := a.chat.Conversations(r.Context(), who, project)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	unread, err := a.chat.Unread(r.Context(), who, project)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := []conversationJSON{}
	for _, c := range cs {
		j := conversation(project, c)
		j.Unread = unread[c.ID]
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": out})
}

func (a *API) messages(w http.ResponseWriter, r *http.Request, who access.Who) {
	q := r.URL.Query()
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	project, name := r.PathValue("project"), r.PathValue("conversation")
	c, ms, err := a.chat.Messages(r.Context(), who, project, name, after, limit)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	reads, err := a.chat.Reads(r.Context(), who, project, name)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := []messageJSON{}
	for _, m := range ms {
		out = append(out, message(m))
	}
	// read_by is how far each member has read: the chat app shows "Read by
	// dev-code" under the last message of yours that each has reached.
	type readJSON struct {
		memberJSON
		UpTo int64     `json:"up_to"`
		At   time.Time `json:"at"`
	}
	readBy := []readJSON{}
	for _, rd := range reads {
		readBy = append(readBy, readJSON{memberJSON: member(rd.Member), UpTo: rd.MessageID, At: rd.At.UTC()})
	}
	resp := map[string]any{"conversation": conversation(project, c), "title": c.Title, "messages": out, "read_by": readBy}
	if len(ms) > 0 {
		resp["next_after"] = ms[len(ms)-1].ID
	}
	writeJSON(w, http.StatusOK, resp)
}

// safeImages are shown in place. Everything else is a download, so nothing
// uploaded can run as a page on cafard's origin: no HTML, no SVG with its
// scripts (docs/design.md, "Files").
var safeImages = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "image/avif": true,
}

func (a *API) file(w http.ResponseWriter, r *http.Request, who access.Who) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, string(access.NotFound), "No such file.")
		return
	}
	f, rd, err := a.chat.File(r.Context(), who, id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer rd.Close()

	h := w.Header()
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'")
	h.Set("Cache-Control", "private, no-cache")
	disposition := "attachment"
	switch {
	case safeImages[f.ContentType]:
		h.Set("Content-Type", f.ContentType)
		disposition = "inline"
	case strings.HasPrefix(f.ContentType, "text/") && !strings.HasPrefix(f.ContentType, "text/html") && !strings.HasPrefix(f.ContentType, "text/xml"):
		h.Set("Content-Type", "text/plain; charset=utf-8")
	default:
		h.Set("Content-Type", "application/octet-stream")
	}
	h.Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, sanitizeFilename(f.Name)))

	if f.State == store.Live {
		// Still arriving: what has come so far, with no length to promise.
		h.Set("X-Cafard-Live", "1")
		w.WriteHeader(http.StatusOK)
		io.Copy(w, rd)
		return
	}
	http.ServeContent(w, r, "", f.CreatedAt, rd)
}

// sanitizeFilename keeps a name safe inside a quoted header value.
func sanitizeFilename(name string) string {
	return strings.Map(func(c rune) rune {
		if c < 0x20 || c == '"' || c == '\\' || c == 0x7f {
			return '_'
		}
		return c
	}, name)
}

func (a *API) lines(w http.ResponseWriter, r *http.Request, who access.Who) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, string(access.NotFound), "No such file.")
		return
	}
	q := r.URL.Query()
	num := func(k string) int { n, _ := strconv.Atoi(q.Get(k)); return n }
	page, err := a.chat.Lines(r.Context(), who, id, chat.LinesQuery{
		From: num("from"), Count: num("count"), Tail: num("tail"), Grep: q.Get("grep"), Context: num("context"),
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
