// Package admin is the admin UI and /admin/api, on the admin listener only
// (docs/design.md, "Who manages what"). It is where people, agents, devices
// and projects are created, and where membership is changed in one picker.
//
// Every route but sign-in needs an admin's sign-in, a cookie scoped to this
// listener; access.Session refuses a person who is no longer an admin at the
// next request. Writes refuse a cross-site request: the cookie is
// SameSite=Strict, and Sec-Fetch-Site or Origin is checked as well, because
// a SameSite cookie alone does not cover every browser that will open this.
//
// It decides nothing: every change goes through service/access, under the
// admin's name in the audit log. It reads the store directly for what it
// lists, which only admins reach.
//
//	POST   /admin/api/signin                  {username, password}
//	POST   /admin/api/signout
//	GET    /admin/api/me
//	GET    /admin/api/members                 people, agents and devices
//	POST   /admin/api/members                 create one; a person or an agent comes with its link
//	PATCH  /admin/api/members/{name}          enabled, admin, description, projects (the full set)
//	DELETE /admin/api/members/{name}
//	POST   /admin/api/members/{name}/link     a new join, password or install link
//	POST   /admin/api/members/{name}/token    a new token, for a device
//	GET    /admin/api/members/{name}/signins
//	DELETE /admin/api/members/{name}/signins/{id}
//	GET    /admin/api/projects
//	POST   /admin/api/projects
//	PATCH  /admin/api/projects/{name}         archived, retention, quota, members (the full set)
//	DELETE /admin/api/projects/{name}         {confirm: name}
//	GET    /admin/api/links
//	DELETE /admin/api/links/{id}
//	GET    /admin/api/audit
//	GET    /admin/api/config
package admin

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"rsc.io/qr"

	"cafard/internal/httpapi/httpx"
	"cafard/internal/ratelimit"
	"cafard/internal/service/access"
	"cafard/internal/store"
	"cafard/internal/version"
)

const cookieName = "cafard_admin"

// Config is what the configuration page shows, and what links are built on.
type Config struct {
	Listen, AdminListen, PublicURL, StateDir string
	MaxUpload                                int64
}

type Admin struct {
	log    *slog.Logger
	st     *store.Store
	acc    *access.Service
	cfg    Config
	ui     fs.FS
	limit  *ratelimit.Limiter
	secure bool
}

// New builds the admin surface. ui is the admin UI's files, served at /.
func New(log *slog.Logger, st *store.Store, acc *access.Service, cfg Config, ui fs.FS) *Admin {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Admin{log: log, st: st, acc: acc, cfg: cfg, ui: ui, limit: ratelimit.New(10, time.Minute),
		secure: strings.HasPrefix(cfg.PublicURL, "https://")}
}

func (a *Admin) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/api/signin", a.signin)
	mux.HandleFunc("POST /admin/api/signout", a.auth(a.signout))
	mux.HandleFunc("GET /admin/api/me", a.auth(a.me))
	mux.HandleFunc("GET /admin/api/members", a.auth(a.members))
	mux.HandleFunc("POST /admin/api/members", a.auth(a.createMember))
	mux.HandleFunc("PATCH /admin/api/members/{name}", a.auth(a.patchMember))
	mux.HandleFunc("DELETE /admin/api/members/{name}", a.auth(a.deleteMember))
	mux.HandleFunc("POST /admin/api/members/{name}/link", a.auth(a.memberLink))
	mux.HandleFunc("POST /admin/api/members/{name}/token", a.auth(a.memberToken))
	mux.HandleFunc("GET /admin/api/members/{name}/signins", a.auth(a.signins))
	mux.HandleFunc("DELETE /admin/api/members/{name}/signins/{id}", a.auth(a.deleteSignin))
	mux.HandleFunc("GET /admin/api/projects", a.auth(a.projects))
	mux.HandleFunc("POST /admin/api/projects", a.auth(a.createProject))
	mux.HandleFunc("PATCH /admin/api/projects/{name}", a.auth(a.patchProject))
	mux.HandleFunc("DELETE /admin/api/projects/{name}", a.auth(a.deleteProject))
	mux.HandleFunc("GET /admin/api/links", a.auth(a.links))
	mux.HandleFunc("DELETE /admin/api/links/{id}", a.auth(a.cancelLink))
	mux.HandleFunc("GET /admin/api/audit", a.auth(a.audit))
	mux.HandleFunc("GET /admin/api/config", a.auth(a.config))
	mux.HandleFunc("/admin/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "No route "+r.Method+" "+r.URL.Path+".")
	})
	// "/" and not "GET /": a pattern with a method and a shorter path
	// conflicts with "/admin/api/" in ServeMux, which panics. static answers
	// anything but GET and HEAD with 405 itself.
	mux.Handle("/", a.static())
}

func clientIP(r *http.Request) string { return httpx.ClientIP(r) }

// sameOrigin refuses a write from another site.
func sameOrigin(r *http.Request) bool { return httpx.SameOrigin(r) }

type handler func(w http.ResponseWriter, r *http.Request, who access.Who)

func (a *Admin) auth(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross_site", "Writes from another site are refused.")
			return
		}
		c, _ := r.Cookie(cookieName)
		var value string
		if c != nil {
			value = c.Value
		}
		from := clientIP(r)
		id, si, err := a.acc.Session(r.Context(), access.AdminSurface, value, from)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		r = r.WithContext(withSignin(r.Context(), si))
		next(w, r, access.Who{Identity: id, Origin: "admin · " + from})
	}
}

var statusOf = map[access.Code]int{
	access.Unauthorized: http.StatusUnauthorized,
	// A wrong password is 403, not 401: the pages treat 401 as "you are signed
	// out" and draw the sign-in form again, which would wipe the message.
	access.Wrong:      http.StatusForbidden,
	access.NotAllowed: http.StatusForbidden, access.NotAdmin: http.StatusForbidden, access.Self: http.StatusForbidden,
	access.Paused: http.StatusForbidden, access.Removed: http.StatusForbidden, access.NotMember: http.StatusConflict,
	access.Archived: http.StatusConflict, access.Exists: http.StatusConflict, access.Spent: http.StatusGone,
	access.NotFound: http.StatusNotFound, access.BadName: http.StatusBadRequest, access.BadValue: http.StatusBadRequest,
}

func (a *Admin) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ref *access.Refusal
	if errors.As(err, &ref) {
		status, ok := statusOf[ref.Code]
		if !ok {
			status = http.StatusBadRequest
		}
		writeError(w, status, string(ref.Code), ref.Message)
		return
	}
	a.log.Error("admin request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "cafard could not do that; the error is in its log.")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &access.Refusal{Code: access.BadValue, Message: "The request is not the JSON this route takes: " + err.Error()}
	}
	return nil
}

// Sign-in.

func (a *Admin) signin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross_site", "Writes from another site are refused.")
		return
	}
	var in struct{ Username, Password string }
	if err := readJSON(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	from := clientIP(r)
	// Per address and per username: one guesser cannot try many names fast,
	// and many addresses cannot try one name fast.
	for _, key := range []string{"ip " + from, "user " + strings.ToLower(in.Username)} {
		if ok, wait := a.limit.Allow(key); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "slow_down", fmt.Sprintf("Too many attempts. Try again in %d seconds.", int(wait.Seconds())+1))
			return
		}
	}
	cookie, id, err := a.acc.SignIn(r.Context(), access.AdminSurface, in.Username, in.Password, from, r.UserAgent())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: cookie, Path: "/", HttpOnly: true, Secure: a.secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int((12 * time.Hour).Seconds())})
	writeJSON(w, http.StatusOK, map[string]any{"member": memberOf(id)})
}

func (a *Admin) signout(w http.ResponseWriter, r *http.Request, who access.Who) {
	if si, ok := signinFrom(r.Context()); ok {
		a.acc.SignOut(r.Context(), who, si)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: a.secure,
		SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (a *Admin) me(w http.ResponseWriter, r *http.Request, who access.Who) {
	writeJSON(w, http.StatusOK, map[string]any{"member": memberOf(who.Identity), "version": version.Version})
}

// Wire shapes.

type memberRef struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name,omitempty"`
}

func memberOf(i store.Identity) memberRef {
	return memberRef{Name: i.Name, Kind: string(i.Kind), DisplayName: i.DisplayName}
}

type linkJSON struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	Member    string     `json:"member"`
	State     string     `json:"state"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	UsedFrom  string     `json:"used_from,omitempty"`
	// URL and QR only when the link was just made: the code is not kept.
	URL     string `json:"url,omitempty"`
	QR      string `json:"qr,omitempty"`
	Command string `json:"command,omitempty"`
}

func linkOf(l store.Link) linkJSON {
	return linkJSON{ID: l.ID, Kind: string(l.Kind), Member: l.Identity, State: l.State(time.Now()), CreatedBy: l.CreatedBy,
		CreatedAt: l.CreatedAt.UTC(), ExpiresAt: l.ExpiresAt.UTC(), UsedAt: utc(l.UsedAt), UsedFrom: l.UsedFrom}
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

type memberJSON struct {
	memberRef
	Description string     `json:"description,omitempty"`
	Admin       bool       `json:"admin,omitempty"`
	Enabled     bool       `json:"enabled"`
	Removed     bool       `json:"removed,omitempty"`
	Joined      bool       `json:"joined"`
	Projects    []string   `json:"projects"`
	LastUsed    *time.Time `json:"last_used,omitempty"`
	LastFrom    string     `json:"last_from,omitempty"`
	Signins     int        `json:"signins,omitempty"`
	Link        *linkJSON  `json:"link,omitempty"`
}

func (a *Admin) memberList(r *http.Request) ([]memberJSON, error) {
	ctx := r.Context()
	ids, err := a.st.Identities(ctx)
	if err != nil {
		return nil, err
	}
	joined, err := a.st.Joined(ctx)
	if err != nil {
		return nil, err
	}
	tokens, err := a.st.TokenUses(ctx)
	if err != nil {
		return nil, err
	}
	signins, err := a.st.LastSignins(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := a.st.SigninCounts(ctx)
	if err != nil {
		return nil, err
	}
	links, err := a.st.Links(ctx, 1000)
	if err != nil {
		return nil, err
	}
	latest := map[int64]store.Link{}
	for _, l := range links { // newest first
		if _, seen := latest[l.IdentityID]; !seen {
			latest[l.IdentityID] = l
		}
	}
	includeRemoved := r.URL.Query().Get("removed") == "1"
	out := []memberJSON{}
	for _, i := range ids {
		if i.Deleted() && !includeRemoved {
			continue
		}
		ps, err := a.st.ProjectsOf(ctx, i.ID)
		if err != nil {
			return nil, err
		}
		m := memberJSON{memberRef: memberOf(i), Description: i.Description, Admin: i.Admin, Enabled: i.Enabled,
			Removed: i.Deleted(), Projects: []string{}, Signins: counts[i.ID]}
		for _, p := range ps {
			m.Projects = append(m.Projects, p.Name)
		}
		switch i.Kind {
		case store.Person:
			m.Joined = joined[i.ID]
			if si, ok := signins[i.ID]; ok {
				t := si.LastUsed.UTC()
				m.LastUsed, m.LastFrom = &t, si.From
			}
		default:
			if u, ok := tokens[i.ID]; ok {
				m.Joined = true
				m.LastUsed, m.LastFrom = utc(u.LastUsed), u.From
			}
		}
		if l, ok := latest[i.ID]; ok {
			lj := linkOf(l)
			m.Link = &lj
		}
		out = append(out, m)
	}
	return out, nil
}

func (a *Admin) members(w http.ResponseWriter, r *http.Request, who access.Who) {
	ms, err := a.memberList(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": ms})
}

func (a *Admin) identity(r *http.Request) (store.Identity, error) {
	name := r.PathValue("name")
	i, err := a.st.IdentityByName(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		return i, &access.Refusal{Code: access.NotFound, Message: fmt.Sprintf("No member %q.", name)}
	}
	return i, err
}

func (a *Admin) project(r *http.Request, name string) (store.Project, error) {
	p, err := a.st.ProjectByName(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		return p, &access.Refusal{Code: access.NotFound, Message: fmt.Sprintf("No project %q.", name)}
	}
	return p, err
}

// newLink makes a link for i and returns it with its URL, QR code and, for
// an agent, the command to run.
func (a *Admin) newLink(r *http.Request, who access.Who, i store.Identity, hours int) (*linkJSON, error) {
	code, l, err := a.acc.IssueLink(r.Context(), who, i, time.Duration(hours)*time.Hour)
	if err != nil {
		return nil, err
	}
	lj := linkOf(l)
	lj.URL = strings.TrimSuffix(a.cfg.PublicURL, "/") + "/join/" + code
	if l.Kind == store.AgentLink {
		lj.Command = "curl -fsSL " + lj.URL + " | sh"
	}
	if q, err := qr.Encode(lj.URL, qr.M); err == nil {
		lj.QR = "data:image/png;base64," + base64.StdEncoding.EncodeToString(q.PNG())
	}
	return &lj, nil
}

func (a *Admin) createMember(w http.ResponseWriter, r *http.Request, who access.Who) {
	var in struct {
		Kind        string   `json:"kind"`
		Name        string   `json:"name"`
		DisplayName string   `json:"display_name"`
		Description string   `json:"description"`
		Projects    []string `json:"projects"`
		LinkHours   int      `json:"link_hours"`
	}
	if err := readJSON(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	var ps []store.Project
	for _, n := range in.Projects {
		p, err := a.project(r, n)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		ps = append(ps, p)
	}
	i, err := a.acc.CreateIdentity(r.Context(), who, access.NewIdentity{Kind: store.Kind(in.Kind), Name: strings.TrimSpace(in.Name),
		DisplayName: strings.TrimSpace(in.DisplayName), Description: strings.TrimSpace(in.Description)})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	for _, p := range ps {
		if err := a.acc.AddMember(r.Context(), who, p, i); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	resp := map[string]any{"member": memberOf(i)}
	if i.Kind == store.Device {
		tok, err := a.acc.IssueToken(r.Context(), who, i)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		resp["token"] = tok
	} else {
		l, err := a.newLink(r, who, i, in.LinkHours)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		resp["link"] = l
	}
	writeJSON(w, http.StatusCreated, resp)
}

// setProjects makes i a member of exactly names, through access one change
// at a time. It stops at the first refusal, which it returns; the changes
// before it stay made, and the picker reloads what is now true.
func (a *Admin) setProjects(r *http.Request, who access.Who, i store.Identity, names []string) error {
	current, err := a.st.ProjectsOf(r.Context(), i.ID)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	for _, p := range current {
		if !want[p.Name] {
			if err := a.acc.RemoveMember(r.Context(), who, p, i); err != nil {
				return err
			}
		}
		delete(want, p.Name)
	}
	for _, n := range sortedKeys(want) {
		p, err := a.project(r, n)
		if err != nil {
			return err
		}
		if err := a.acc.AddMember(r.Context(), who, p, i); err != nil {
			return err
		}
	}
	return nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (a *Admin) patchMember(w http.ResponseWriter, r *http.Request, who access.Who) {
	var in struct {
		Enabled     *bool     `json:"enabled"`
		Admin       *bool     `json:"admin"`
		Description *string   `json:"description"`
		Projects    *[]string `json:"projects"`
	}
	if err := readJSON(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	i, err := a.identity(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	ctx := r.Context()
	switch {
	case in.Enabled != nil && *in.Enabled != i.Enabled:
		err = a.acc.SetPaused(ctx, who, i, !*in.Enabled)
	case in.Admin != nil && *in.Admin != i.Admin:
		err = a.acc.SetAdmin(ctx, who, i, *in.Admin)
	case in.Description != nil && strings.TrimSpace(*in.Description) != i.Description:
		err = a.acc.SetDescription(ctx, who, i, strings.TrimSpace(*in.Description))
	case in.Projects != nil:
		err = a.setProjects(r, who, i, *in.Projects)
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.members(w, r, who)
}

func (a *Admin) deleteMember(w http.ResponseWriter, r *http.Request, who access.Who) {
	i, err := a.identity(r)
	if err == nil {
		err = a.acc.DeleteIdentity(r.Context(), who, i)
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Admin) memberLink(w http.ResponseWriter, r *http.Request, who access.Who) {
	var in struct {
		Hours int `json:"hours"`
	}
	if r.ContentLength != 0 {
		if err := readJSON(r, &in); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	i, err := a.identity(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	l, err := a.newLink(r, who, i, in.Hours)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"link": l})
}

func (a *Admin) memberToken(w http.ResponseWriter, r *http.Request, who access.Who) {
	i, err := a.identity(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if i.Kind == store.Agent {
		writeError(w, http.StatusBadRequest, string(access.NotAllowed),
			"An agent's token is never shown: make a new install link, and run it on the agent's machine.")
		return
	}
	tok, err := a.acc.IssueToken(r.Context(), who, i)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": tok})
}

func (a *Admin) signins(w http.ResponseWriter, r *http.Request, who access.Who) {
	i, err := a.identity(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	ss, err := a.st.Signins(r.Context(), i.ID)
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

func (a *Admin) deleteSignin(w http.ResponseWriter, r *http.Request, who access.Who) {
	i, err := a.identity(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	ss, err := a.st.Signins(r.Context(), i.ID)
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

type projectJSON struct {
	Name          string      `json:"name"`
	Archived      bool        `json:"archived,omitempty"`
	RetentionDays int         `json:"retention_days"`
	QuotaBytes    int64       `json:"quota_bytes"`
	Members       []memberRef `json:"members"`
	Conversations int         `json:"conversations"`
	Files         int         `json:"files"`
	Bytes         int64       `json:"bytes"`
}

func (a *Admin) projectList(r *http.Request) ([]projectJSON, error) {
	ctx := r.Context()
	ps, err := a.st.Projects(ctx)
	if err != nil {
		return nil, err
	}
	stats, err := a.st.Stats(ctx)
	if err != nil {
		return nil, err
	}
	out := []projectJSON{}
	for _, p := range ps {
		ms, err := a.st.Members(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		pj := projectJSON{Name: p.Name, Archived: p.Archived(), RetentionDays: p.RetentionDays, QuotaBytes: p.QuotaBytes, Members: []memberRef{},
			Conversations: stats[p.ID].Conversations, Files: stats[p.ID].Files, Bytes: stats[p.ID].Bytes}
		for _, m := range ms {
			pj.Members = append(pj.Members, memberOf(m))
		}
		out = append(out, pj)
	}
	return out, nil
}

func (a *Admin) projects(w http.ResponseWriter, r *http.Request, who access.Who) {
	a.writeProjects(w, r, http.StatusOK)
}

func (a *Admin) writeProjects(w http.ResponseWriter, r *http.Request, status int) {
	ps, err := a.projectList(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, status, map[string]any{"projects": ps})
}

func (a *Admin) createProject(w http.ResponseWriter, r *http.Request, who access.Who) {
	var in struct {
		Name          string   `json:"name"`
		RetentionDays int      `json:"retention_days"`
		QuotaBytes    int64    `json:"quota_bytes"`
		Members       []string `json:"members"`
	}
	if err := readJSON(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if in.RetentionDays == 0 {
		in.RetentionDays = 30
	}
	if in.QuotaBytes == 0 {
		in.QuotaBytes = 5 << 30
	}
	var ids []store.Identity
	for _, n := range in.Members {
		i, err := a.st.IdentityByName(r.Context(), n)
		if err != nil {
			writeError(w, http.StatusNotFound, string(access.NotFound), fmt.Sprintf("No member %q.", n))
			return
		}
		ids = append(ids, i)
	}
	p, err := a.acc.CreateProject(r.Context(), who, strings.TrimSpace(in.Name), in.RetentionDays, in.QuotaBytes)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	for _, i := range ids {
		if err := a.acc.AddMember(r.Context(), who, p, i); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	a.writeProjects(w, r, http.StatusCreated)
}

func (a *Admin) patchProject(w http.ResponseWriter, r *http.Request, who access.Who) {
	var in struct {
		Archived      *bool     `json:"archived"`
		RetentionDays *int      `json:"retention_days"`
		QuotaBytes    *int64    `json:"quota_bytes"`
		Members       *[]string `json:"members"`
	}
	if err := readJSON(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	p, err := a.project(r, r.PathValue("name"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	ctx := r.Context()
	switch {
	case in.Archived != nil && *in.Archived != p.Archived():
		err = a.acc.SetArchived(ctx, who, p, *in.Archived)
	case in.RetentionDays != nil || in.QuotaBytes != nil:
		ret, quota := p.RetentionDays, p.QuotaBytes
		if in.RetentionDays != nil {
			ret = *in.RetentionDays
		}
		if in.QuotaBytes != nil {
			quota = *in.QuotaBytes
		}
		err = a.acc.SetProjectLimits(ctx, who, p, ret, quota)
	case in.Members != nil:
		err = a.setMembers(r, who, p, *in.Members)
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.projects(w, r, who)
}

// setMembers makes exactly names the members of p, as setProjects does the
// other way round.
func (a *Admin) setMembers(r *http.Request, who access.Who, p store.Project, names []string) error {
	current, err := a.st.Members(r.Context(), p.ID)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	for _, m := range current {
		if !want[m.Name] {
			if err := a.acc.RemoveMember(r.Context(), who, p, m); err != nil {
				return err
			}
		}
		delete(want, m.Name)
	}
	for _, n := range sortedKeys(want) {
		i, err := a.st.IdentityByName(r.Context(), n)
		if err != nil {
			return &access.Refusal{Code: access.NotFound, Message: fmt.Sprintf("No member %q.", n)}
		}
		if err := a.acc.AddMember(r.Context(), who, p, i); err != nil {
			return err
		}
	}
	return nil
}

func (a *Admin) deleteProject(w http.ResponseWriter, r *http.Request, who access.Who) {
	var in struct {
		Confirm string `json:"confirm"`
	}
	if err := readJSON(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	p, err := a.project(r, r.PathValue("name"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// The name typed again, on the server as well as in the page: a delete
	// cannot be undone, and a script calling this by mistake should fail.
	if in.Confirm != p.Name {
		writeError(w, http.StatusBadRequest, string(access.BadValue), "Type the project's name to confirm: deleting it cannot be undone.")
		return
	}
	if err := a.acc.DeleteProject(r.Context(), who, p); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Admin) links(w http.ResponseWriter, r *http.Request, who access.Who) {
	ls, err := a.st.Links(r.Context(), 200)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := []linkJSON{}
	for _, l := range ls {
		out = append(out, linkOf(l))
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": out})
}

func (a *Admin) cancelLink(w http.ResponseWriter, r *http.Request, who access.Who) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	l, err := a.st.Link(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, string(access.NotFound), "No such link.")
		return
	}
	if err == nil {
		err = a.acc.CancelLink(r.Context(), who, l)
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Admin) audit(w http.ResponseWriter, r *http.Request, who access.Who) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	es, err := a.st.Audit(r.Context(), limit)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	type entry struct {
		At     time.Time `json:"at"`
		Actor  string    `json:"actor"`
		Action string    `json:"action"`
		Target string    `json:"target"`
		Origin string    `json:"origin"`
	}
	out := []entry{}
	for _, e := range es {
		out = append(out, entry{At: e.At.UTC(), Actor: e.Actor, Action: e.Action, Target: e.Target, Origin: e.Origin})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

func (a *Admin) config(w http.ResponseWriter, r *http.Request, who access.Who) {
	writeJSON(w, http.StatusOK, map[string]any{
		"listen": a.cfg.Listen, "admin_listen": a.cfg.AdminListen, "public_url": a.cfg.PublicURL, "state_dir": a.cfg.StateDir,
		"max_upload": a.cfg.MaxUpload, "version": version.String(),
		"link_hours": int(access.DefaultLinkLife.Hours()), "link_max_hours": int(access.MaxLinkLife.Hours()),
		"admin_signin_hours": 12, "chat_signin_days": 90,
	})
}
