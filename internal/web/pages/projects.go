package pages

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/projects"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/units"
	"github.com/nebuloss/cafard/internal/web/view"
)

// The admin's project pages (PLAN section 4, "Projects, sinks and
// grants"): plain form posts on the admin interface, as /agents and /pair.

func (s *Site) mountProjects(mux *http.ServeMux) {
	mux.HandleFunc("GET /projects", s.Auth(auth.ScopeAdmin, s.projectList))
	mux.Handle("POST /projects", s.Write(auth.ScopeAdmin, s.createProject))
	mux.HandleFunc("GET /projects/{project}", s.Auth(auth.ScopeAdmin, s.project))
	mux.Handle("POST /projects/{project}/retention", s.Write(auth.ScopeAdmin, s.saveRetention))
	mux.Handle("POST /projects/{project}/channels", s.Write(auth.ScopeAdmin, s.createProjectChannel))
	mux.Handle("POST /projects/{project}/sinks", s.Write(auth.ScopeAdmin, s.saveSink))
	mux.Handle("POST /projects/{project}/sinks/{sink}", s.Write(auth.ScopeAdmin, s.saveSink))
	mux.Handle("POST /projects/{project}/sinks/{sink}/delete", s.Write(auth.ScopeAdmin, s.deleteSink))
	mux.Handle("POST /projects/{project}/sinks/{sink}/tokens", s.Write(auth.ScopeAdmin, s.createSinkToken))
	mux.Handle("POST /projects/{project}/grants", s.Write(auth.ScopeAdmin, s.saveGrant))
	mux.Handle("POST /projects/{project}/grants/{id}/delete", s.Write(auth.ScopeAdmin, s.deleteGrant))
	mux.Handle("POST /projects/{project}/pairings", s.Write(auth.ScopeAdmin, s.createProjectPairing))
}

type projectRowView struct {
	Name, Title, Href, Created string
	Channels, Sinks, Members   int
}

type projectsData struct {
	Projects []projectRowView
}

func (s *Site) projectList(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	s.renderProjects(w, r, p, http.StatusOK, "")
}

func (s *Site) renderProjects(w http.ResponseWriter, r *http.Request, p auth.Principal, status int, errText string) {
	ctx := r.Context()
	all, err := s.d.Projects.Projects(ctx)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	var chans []messaging.Channel
	if s.chats != nil {
		if chans, err = s.chats.svc.Channels(ctx, p, false); err != nil {
			s.Oops(w, r, &p, err)
			return
		}
	}
	grants, err := s.d.Projects.Grants(ctx, "", "")
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	data := projectsData{}
	for _, pr := range all {
		sinks, err := s.d.Projects.Sinks(ctx, pr.Name)
		if err != nil {
			s.Oops(w, r, &p, err)
			return
		}
		row := projectRowView{Name: pr.Name, Title: pr.Title, Href: projectHref(pr.Name), Created: Day(pr.CreatedAt), Sinks: len(sinks)}
		for _, c := range chans {
			if c.Project() == pr.Name {
				row.Channels++
			}
		}
		members := map[string]bool{}
		for _, g := range grants {
			if g.Project == pr.Name {
				members[g.Principal] = true
			}
		}
		row.Members = len(members)
		data.Projects = append(data.Projects, row)
	}
	l := s.Shell(r, &p, "Projects", "projects", "projects")
	l.Error = errText
	s.Render(w, status, "projects", l, []string{"tpl-toast"}, data)
}

func projectHref(name string) string { return "/projects/" + url.PathEscape(name) }

func (s *Site) createProject(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	pr, err := s.d.Projects.Create(r.Context(), strings.TrimSpace(r.PostFormValue("name")), r.PostFormValue("title"))
	if err != nil {
		var apiErr *dto.APIError
		if errors.As(err, &apiErr) {
			s.renderProjects(w, r, p, dto.HTTPStatus(apiErr.Code), apiErr.Message)
			return
		}
		s.Oops(w, r, &p, err)
		return
	}
	http.Redirect(w, r, projectHref(pr.Name), http.StatusSeeOther)
}

type projectChannelView struct {
	Name, Href, Title, Topic, Bindings string
}

type sinkView struct {
	Name, Title, Action, LiveHref     string
	Sources, SourcesText              string
	SerialKind, SerialAddress         string
	Baud, DataBits, Parity, StopBits  string
	Writable                          bool
	SerialText, DefaultTokenPrincipal string
	Tokens                            []sinkTokenView
}

type sinkTokenView struct {
	ID                                  int64
	Label, Principal, Created, LastUsed string
}

type grantView struct {
	ID                            int64
	Principal, Kind, Target       string
	Scopes, DeleteAction, Channel string
	WholeProject                  bool
}

type retentionView struct {
	LinesMaxAge, LinesMaxBytes, MessagesMaxAge, BlobsMaxBytes string
}

type projectData struct {
	Name, Title, Created, Base string
	HasChats                   bool
	Retention                  retentionView
	Channels                   []projectChannelView
	ChannelNames               []string
	Sinks                      []sinkView
	NewSink                    sinkView
	Grants                     []grantView
	Principals, Humans         []string
	Scopes                     []scopeOption
	Token                      *createdView // a sink token, shown once
	Pending                    []pendingView
}

// projectPage is what a handler wants shown on top of the project page.
type projectPage struct {
	status  int
	errText string
	notice  string
	created *createdView
}

func (s *Site) project(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	notice := ""
	switch r.URL.Query().Get("done") {
	case "retention":
		notice = "Retention saved; the janitor applies it within 10 minutes."
	case "sink":
		notice = "Sink saved."
	case "sink-deleted":
		notice = "Sink deleted. Its port is closed and its tokens revoked; its history ages out by retention."
	case "grant":
		notice = "Grant saved."
	case "grant-deleted":
		notice = "Grant removed."
	case "channel":
		notice = "Channel made."
	}
	s.renderProject(w, r, p, projectPage{status: http.StatusOK, notice: notice})
}

func (s *Site) renderProject(w http.ResponseWriter, r *http.Request, p auth.Principal, pg projectPage) {
	ctx := r.Context()
	pr, err := s.d.Projects.Project(ctx, r.PathValue("project"))
	if errors.Is(err, projects.ErrNotFound) {
		s.Fail(w, r, &p, http.StatusNotFound, "Not found", "There is no such project.")
		return
	}
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	data, err := s.projectData(ctx, p, pr)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	data.Token = pg.created
	l := s.Shell(r, &p, pr.Label(), "project", "projects")
	l.Back = "/projects"
	l.Error, l.Notice = pg.errText, pg.notice
	s.Render(w, pg.status, "project", l, []string{"tpl-toast"}, data)
}

func (s *Site) projectData(ctx context.Context, p auth.Principal, pr projects.Project) (projectData, error) {
	data := projectData{Name: pr.Name, Title: pr.Title, Created: Day(pr.CreatedAt), Base: projectHref(pr.Name), HasChats: s.chats != nil,
		Retention: retentionView{LinesMaxAge: durationText(pr.Retention.LinesMaxAge), LinesMaxBytes: units.ByteSize(pr.Retention.LinesMaxBytes).String(),
			MessagesMaxAge: durationText(pr.Retention.MessagesMaxAge), BlobsMaxBytes: units.ByteSize(pr.Retention.BlobsMaxBytes).String()}}
	if s.chats != nil {
		chans, err := s.chats.svc.Channels(ctx, p, true)
		if err != nil {
			return data, err
		}
		for _, c := range chans {
			if c.Project() != pr.Name {
				continue
			}
			local := strings.TrimPrefix(c.Name, pr.Name+"/")
			data.ChannelNames = append(data.ChannelNames, local)
			data.Channels = append(data.Channels, projectChannelView{Name: local, Href: view.ChatHref(c.Stream()), Title: c.Title,
				Topic: c.Topic, Bindings: strings.Join(c.Patterns, ", ")})
		}
	}
	sinks, err := s.d.Projects.Sinks(ctx, pr.Name)
	if err != nil {
		return data, err
	}
	tokens, err := s.d.Tokens.ListTokens(ctx, "")
	if err != nil {
		return data, err
	}
	for _, k := range sinks {
		data.Sinks = append(data.Sinks, sinkViewOf(k, tokens))
	}
	data.NewSink = sinkView{Action: data.Base + "/sinks", SerialKind: "none"}
	grants, err := s.d.Projects.Grants(ctx, pr.Name, "")
	if err != nil {
		return data, err
	}
	principals, err := s.d.Tokens.Principals(ctx, "")
	if err != nil {
		return data, err
	}
	kinds := map[string]string{}
	for _, pp := range principals {
		kinds[pp.Name] = pp.Kind
		if pp.Name == auth.AdminName {
			continue
		}
		data.Principals = append(data.Principals, pp.Name)
		if pp.Kind == auth.KindHuman {
			data.Humans = append(data.Humans, pp.Name)
		}
	}
	for _, g := range grants {
		target := "the whole project"
		if g.Channel != "" {
			target = "#" + g.Channel
		}
		data.Grants = append(data.Grants, grantView{ID: g.ID, Principal: g.Principal, Kind: kinds[g.Principal], Target: target,
			Channel: g.Channel, WholeProject: g.Channel == "", Scopes: strings.Join(g.Scopes.Strings(), ", "),
			DeleteAction: data.Base + "/grants/" + strconv.FormatInt(g.ID, 10) + "/delete"})
	}
	for _, sc := range projects.GrantScopes {
		data.Scopes = append(data.Scopes, scopeOption{Name: string(sc), Checked: sc == auth.ScopeRead || sc == auth.ScopeMessage})
	}
	pending, err := s.pairings.Pending(ctx)
	if err != nil {
		return data, err
	}
	base := s.d.Server.BaseURL()
	for _, row := range pending {
		if !grantsTouch(row.Grants, pr.Name) {
			continue
		}
		code, _, _ := s.pairings.Code(ctx, row.ID)
		dp := view.Pairing(base, row, code)
		data.Pending = append(data.Pending, pendingView{ID: row.ID, Name: row.Name(), URL: dp.URL, QR: dp.QRURL,
			ExpiresISO: row.ExpiresAt.Format(time.RFC3339), ExpiresText: row.ExpiresAt.UTC().Format("15:04:05"),
			Grants: strings.Join(dp.Grants, ", ")})
	}
	return data, nil
}

// grantsTouch reports whether stored grant specs name project.
func grantsTouch(specs, project string) bool {
	gs, err := projects.ParseGrants(specs)
	return err == nil && slices.ContainsFunc(gs, func(g projects.Grant) bool { return g.Project == project })
}

func sinkViewOf(k projects.Sink, tokens []auth.TokenRow) sinkView {
	v := sinkView{Name: k.Name, Title: k.Title, Action: projectHref(k.Project) + "/sinks/" + url.PathEscape(k.Name),
		LiveHref: "/live?" + url.Values{"streams": {"*:" + k.Ref()}}.Encode(), SerialKind: "none",
		DefaultTokenPrincipal: tokenPrincipal(k)}
	var srcs []string
	for _, p := range k.Sources {
		srcs = append(srcs, sourceText(p.String()))
	}
	v.Sources, v.SourcesText = strings.Join(srcs, "\n"), strings.Join(srcs, ", ")
	if port := k.Serial; port != nil {
		v.SerialKind = map[string]string{projects.TransportLocal: "device", projects.TransportRFC2217: "rfc2217", projects.TransportTCP: "tcp"}[port.Transport()]
		v.SerialAddress, v.Writable, v.Parity = port.Address(), port.Writable, port.Parity
		if port.Baud != 0 {
			v.Baud = strconv.Itoa(port.Baud)
		}
		if port.DataBits != 0 {
			v.DataBits = strconv.Itoa(port.DataBits)
		}
		if port.StopBits != 0 {
			v.StopBits = strconv.FormatFloat(port.StopBits, 'f', -1, 64)
		}
		v.SerialText = port.Transport() + " " + port.Address()
		if f := port.Framing(); f != "" {
			v.SerialText += ", " + f
		}
		if port.Writable {
			v.SerialText += ", writable"
		}
	}
	for _, t := range tokens {
		if t.Sink == k.Ref() {
			lu := "never"
			if t.LastUsed != nil {
				lu = Day(*t.LastUsed)
			}
			v.Tokens = append(v.Tokens, sinkTokenView{ID: t.ID, Label: t.Label, Principal: t.Principal, Created: Day(t.CreatedAt), LastUsed: lu})
		}
	}
	return v
}

// tokenPrincipal is the device a new sink token is for unless the admin
// names another: "<project>-<sink>", cut to the name limit.
func tokenPrincipal(k projects.Sink) string {
	name := k.Project + "-" + k.Name
	if len(name) > 40 {
		name = strings.TrimRight(name[:40], "-._")
	}
	return name
}

// sourceText is a prefix as typed: a bare address for a single host.
func sourceText(prefix string) string {
	if a, ok := strings.CutSuffix(prefix, "/32"); ok && !strings.Contains(a, ":") {
		return a
	}
	if a, ok := strings.CutSuffix(prefix, "/128"); ok && strings.Contains(a, ":") {
		return a
	}
	return prefix
}

// durationText is a duration as the config writes it: "336h", "90m", "0".
func durationText(d time.Duration) string {
	if d == 0 {
		return "0"
	}
	s := d.String()
	s = strings.TrimSuffix(s, "0s")
	s = strings.TrimSuffix(s, "0m")
	return s
}

// projectFail answers a refused form: the project page with the server's
// sentence, or the error page for anything unexpected.
func (s *Site) projectFail(w http.ResponseWriter, r *http.Request, p auth.Principal, err error) {
	var apiErr *dto.APIError
	switch {
	case errors.As(err, &apiErr):
		s.renderProject(w, r, p, projectPage{status: dto.HTTPStatus(apiErr.Code), errText: apiErr.Message})
	case errors.Is(err, projects.ErrNotFound), errors.Is(err, auth.ErrNotFound):
		s.renderProject(w, r, p, projectPage{status: http.StatusNotFound, errText: sentence(err.Error())})
	case errors.Is(err, auth.ErrConflict), errors.Is(err, auth.ErrBadPairing):
		s.renderProject(w, r, p, projectPage{status: http.StatusBadRequest, errText: sentence(err.Error())})
	default:
		s.Oops(w, r, &p, err)
	}
}

func badForm(msg string) error { return dto.Errorf(dto.CodeBadRequest, "%s", msg) }

func (s *Site) saveRetention(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var l store.Limits
	durations := []struct {
		field string
		to    *time.Duration
	}{{"lines_max_age", &l.LinesMaxAge}, {"messages_max_age", &l.MessagesMaxAge}}
	for _, f := range durations {
		d, err := time.ParseDuration(strings.TrimSpace(r.PostFormValue(f.field)))
		if err != nil || d < 0 {
			s.projectFail(w, r, p, badForm(f.field+" is not a duration such as 336h, 90m or 0."))
			return
		}
		*f.to = d
	}
	sizes := []struct {
		field string
		to    *int64
	}{{"lines_max_bytes", &l.LinesMaxBytes}, {"blobs_max_bytes", &l.BlobsMaxBytes}}
	for _, f := range sizes {
		var b units.ByteSize
		if err := b.UnmarshalText([]byte(r.PostFormValue(f.field))); err != nil {
			s.projectFail(w, r, p, badForm(f.field+" is not a size such as 2GB, 512MiB or 0."))
			return
		}
		*f.to = int64(b)
	}
	if err := s.d.Projects.SetRetention(r.Context(), r.PathValue("project"), l); err != nil {
		s.projectFail(w, r, p, err)
		return
	}
	http.Redirect(w, r, projectHref(r.PathValue("project"))+"?done=retention", http.StatusSeeOther)
}

func (s *Site) createProjectChannel(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	if s.chats == nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	req := dto.ChannelCreate{Name: strings.TrimSpace(r.PostFormValue("name")), Project: r.PathValue("project"),
		Title: r.PostFormValue("title"), Topic: r.PostFormValue("topic"), Patterns: strings.Fields(r.PostFormValue("patterns"))}
	if _, err := s.chats.svc.CreateChannel(r.Context(), p, req); err != nil {
		s.projectFail(w, r, p, err)
		return
	}
	http.Redirect(w, r, projectHref(req.Project)+"?done=channel", http.StatusSeeOther)
}

// sinkFromForm reads the sink form: the name comes from the path when it
// edits a sink.
func sinkFromForm(r *http.Request) (projects.Sink, error) {
	k := projects.Sink{Project: r.PathValue("project"), Name: r.PathValue("sink"), Title: r.PostFormValue("title")}
	if k.Name == "" {
		k.Name = strings.TrimSpace(r.PostFormValue("name"))
	}
	var err error
	if k.Sources, err = projects.ParseSources(r.PostFormValue("sources")); err != nil {
		return k, badForm(sentence(err.Error()))
	}
	kind := r.PostFormValue("serial_kind")
	if kind == "" || kind == "none" {
		return k, nil
	}
	addr := strings.TrimSpace(r.PostFormValue("serial_address"))
	port := projects.SerialPort{Writable: r.PostFormValue("writable") != "", Parity: r.PostFormValue("parity")}
	switch kind {
	case "device":
		port.Device = addr
	case "rfc2217":
		port.RFC2217 = addr
	case "tcp":
		port.TCP = addr
		port.Parity = ""
	default:
		return k, badForm("The serial port kind must be none, device, rfc2217 or tcp.")
	}
	if kind != "tcp" {
		ints := []struct {
			field string
			to    *int
		}{{"baud", &port.Baud}, {"data_bits", &port.DataBits}}
		for _, f := range ints {
			if v := strings.TrimSpace(r.PostFormValue(f.field)); v != "" {
				n, err := strconv.Atoi(v)
				if err != nil {
					return k, badForm(f.field + " must be a number.")
				}
				*f.to = n
			}
		}
		if v := strings.TrimSpace(r.PostFormValue("stop_bits")); v != "" {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return k, badForm("stop_bits must be 1, 1.5 or 2.")
			}
			port.StopBits = n
		}
	}
	k.Serial = &port
	return k, nil
}

func (s *Site) saveSink(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	k, err := sinkFromForm(r)
	if err == nil {
		_, err = s.d.Projects.SaveSink(r.Context(), k)
	}
	if err != nil {
		s.projectFail(w, r, p, err)
		return
	}
	http.Redirect(w, r, projectHref(k.Project)+"?done=sink", http.StatusSeeOther)
}

func (s *Site) deleteSink(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	project := r.PathValue("project")
	if err := s.d.Projects.DeleteSink(r.Context(), project, r.PathValue("sink")); err != nil {
		s.projectFail(w, r, p, err)
		return
	}
	http.Redirect(w, r, projectHref(project)+"?done=sink-deleted", http.StatusSeeOther)
}

// createSinkToken answers with the project page showing the token once.
func (s *Site) createSinkToken(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	ctx := r.Context()
	k, err := s.d.Projects.Sink(ctx, r.PathValue("project"), r.PathValue("sink"))
	if err != nil {
		s.projectFail(w, r, p, err)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("principal"))
	if name == "" {
		name = tokenPrincipal(k)
	}
	if row, err := s.d.Tokens.Principal(ctx, name); err == nil && row.Kind != auth.KindDevice {
		s.projectFail(w, r, p, badForm(name+" is a "+row.Kind+"; a sink token belongs to a device."))
		return
	}
	if _, err := s.d.Tokens.EnsurePrincipal(ctx, name, auth.KindDevice); err != nil {
		s.projectFail(w, r, p, badForm(sentence(err.Error())))
		return
	}
	_, secret, err := s.d.Tokens.CreateSinkToken(ctx, name, strings.TrimSpace(r.PostFormValue("label")), k.ID, k.Ref())
	if err != nil {
		s.projectFail(w, r, p, err)
		return
	}
	created := &createdView{Principal: name, Secret: secret, Done: projectHref(k.Project),
		Snippets: view.TokenSnippets(s.d.Server.BaseURL(), k.Name, secret, auth.NewScopeSet(auth.ScopeIngest))}
	s.renderProject(w, r, p, projectPage{status: http.StatusCreated, created: created,
		notice: "Token for sink " + k.Ref() + ": it writes there only, whatever stream it names, and reads nothing."})
}

func (s *Site) saveGrant(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		s.projectFail(w, r, p, badForm("The form could not be read."))
		return
	}
	ctx := r.Context()
	name := strings.TrimSpace(r.PostForm.Get("principal"))
	kind := r.PostForm.Get("kind")
	if kind == "" {
		kind = auth.KindAgent
	}
	if name == auth.AdminName {
		s.projectFail(w, r, p, badForm("The admin reaches every project already."))
		return
	}
	if _, err := s.d.Tokens.EnsurePrincipal(ctx, name, kind); err != nil {
		s.projectFail(w, r, p, badForm(sentence(err.Error())))
		return
	}
	var scopes []auth.Scope
	for _, sc := range r.PostForm["scopes"] {
		scopes = append(scopes, auth.Scope(sc))
	}
	g := projects.Grant{Principal: name, Project: r.PathValue("project"), Channel: r.PostForm.Get("channel"), Scopes: auth.NewScopeSet(scopes...)}
	if err := s.d.Projects.SetGrant(ctx, g); err != nil {
		s.projectFail(w, r, p, err)
		return
	}
	http.Redirect(w, r, projectHref(g.Project)+"?done=grant", http.StatusSeeOther)
}

func (s *Site) deleteGrant(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	project := r.PathValue("project")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.d.Projects.DeleteGrant(r.Context(), project, id); err != nil {
		s.projectFail(w, r, p, err)
		return
	}
	http.Redirect(w, r, projectHref(project)+"?done=grant-deleted", http.StatusSeeOther)
}

// createProjectPairing makes a sign-in link whose person gets a grant on
// this project, or on one of its channels, and shows its QR.
func (s *Site) createProjectPairing(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		s.projectFail(w, r, p, badForm("The form could not be read."))
		return
	}
	var scopes []auth.Scope
	for _, sc := range r.PostForm["scopes"] {
		scopes = append(scopes, auth.Scope(sc))
	}
	g := projects.Grant{Project: r.PathValue("project"), Channel: r.PostForm.Get("channel"), Scopes: auth.NewScopeSet(scopes...)}
	if len(g.Scopes) == 0 {
		s.projectFail(w, r, p, badForm("Choose what the person may do."))
		return
	}
	principal, newName := strings.TrimSpace(r.PostForm.Get("principal")), strings.TrimSpace(r.PostForm.Get("new_name"))
	if _, _, err := s.pairings.Create(r.Context(), principal, newName, nil, g.Spec(), p.Name, 0); err != nil {
		s.projectFail(w, r, p, err)
		return
	}
	s.renderProject(w, r, p, projectPage{status: http.StatusCreated, notice: "Sign-in link made: scan the code below with the phone within 5 minutes."})
}
