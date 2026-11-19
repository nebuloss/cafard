package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/web/view"
)

// Mount registers the account, stream, entry and session routes on
// public, and the token and pairing routes on admin, the admin interface.
func (a *API) Mount(public, admin *http.ServeMux) {
	public.Handle("GET /api/me", a.Read(auth.ScopeRead, a.me))
	public.Handle("GET /api/projects", a.Read(auth.ScopeRead, a.projects))
	public.Handle("GET /api/principals", a.Read(auth.ScopeRead, a.principals))
	public.Handle("GET /api/streams", a.Read(auth.ScopeRead, a.streams))
	public.Handle("GET /api/streams/match", a.Read(auth.ScopeRead, a.streamsMatch))
	public.Handle("GET /api/entries", a.Read(auth.ScopeRead, a.entries))
	public.Handle("GET /api/entries/{seq}/context", a.Read(auth.ScopeRead, a.context))
	public.Handle("POST /api/marks", a.Write(auth.ScopeMessage, a.mark))
	public.Handle("GET /api/sessions", a.Read("", a.listSessions))
	public.Handle("DELETE /api/sessions/{id}", a.Write("", a.revokeSession))
	admin.Handle("GET /api/tokens", a.Read(auth.ScopeAdmin, a.tokens))
	admin.Handle("POST /api/tokens", a.Write(auth.ScopeAdmin, a.createToken))
	admin.Handle("DELETE /api/tokens/{id}", a.Write(auth.ScopeAdmin, a.revokeToken))
	admin.Handle("POST /api/pairings", a.Write(auth.ScopeAdmin, a.createPairing))
}

func (a *API) me(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	dto.WriteJSON(w, http.StatusOK, dto.Me{Name: p.Name, Kind: dto.PrincipalKind(p.Kind), Scopes: view.Scopes(p.Scopes),
		SessionID: p.SessionID, Theme: view.ThemeOf(r)})
}

// projectLister is what messaging offers for GET /api/projects.
type projectLister interface {
	Projects(ctx context.Context, p auth.Principal) ([]dto.Project, error)
}

func (a *API) projects(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	out := []dto.Project{}
	if lister, ok := a.d.Messaging.(projectLister); ok {
		var err error
		if out, err = lister.Projects(r.Context(), p); err != nil {
			a.Fail(w, r, err)
			return
		}
	}
	dto.WriteJSON(w, http.StatusOK, map[string][]dto.Project{"projects": out})
}

func (a *API) principals(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	kind := r.URL.Query().Get("kind")
	if kind != "" && !auth.ValidKind(kind) {
		dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "kind is agent, human or device, not %q.", kind))
		return
	}
	rows, err := a.d.Tokens.Principals(r.Context(), kind)
	if err == nil {
		rows, err = a.d.Projects.Reachable(r.Context(), p, rows)
	}
	if err != nil {
		a.Fail(w, r, err)
		return
	}
	out := make([]dto.Principal, 0, len(rows))
	for _, p := range rows {
		out = append(out, view.Principal(p))
	}
	dto.WriteJSON(w, http.StatusOK, map[string][]dto.Principal{"principals": out})
}

func (a *API) streams(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	list, err := view.VisibleStreams(r.Context(), a.d, p)
	if err != nil {
		a.Fail(w, r, err)
		return
	}
	if ch := r.URL.Query().Get("channel"); ch != "" {
		patterns, apiErr := view.LogFilter{Channel: ch}.Patterns(r.Context(), a.d, p)
		if apiErr != nil {
			dto.WriteError(w, apiErr)
			return
		}
		list = slices.DeleteFunc(list, func(s dto.Stream) bool { return !store.StreamMatches(s.ID, patterns) || len(patterns) == 0 })
	}
	if project := r.URL.Query().Get("project"); project != "" {
		list = slices.DeleteFunc(list, func(s dto.Stream) bool { return s.Project != project })
	}
	dto.WriteJSON(w, http.StatusOK, map[string][]dto.Stream{"streams": nonNil(list)})
}

// streamsMatch previews a glob: which streams a binding would show now.
func (a *API) streamsMatch(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	pattern := strings.TrimSpace(r.URL.Query().Get("pattern"))
	if err := store.ValidatePattern(pattern); err != nil {
		dto.WriteError(w, dto.Errorf(dto.CodeInvalidPattern, "%v.", err))
		return
	}
	list, err := view.VisibleStreams(r.Context(), a.d, p)
	if err != nil {
		a.Fail(w, r, err)
		return
	}
	list = slices.DeleteFunc(list, func(s dto.Stream) bool { return !store.StreamMatches(s.ID, []string{pattern}) })
	dto.WriteJSON(w, http.StatusOK, map[string][]dto.Stream{"streams": nonNil(list)})
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

const (
	entriesDefault = 200
	entriesMax     = 1000
)

// entries reads log lines and markers. Never messages: chan:* and dm:*
// patterns are dropped, and kinds=message is refused.
func (a *API) entries(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	f, apiErr := view.ParseLogFilter(r.URL.Query())
	if apiErr == nil && f.Messages {
		apiErr = dto.Errorf(dto.CodeBadRequest, "/api/entries returns lines and markers; read messages through the chat API.")
	}
	if apiErr != nil {
		dto.WriteError(w, apiErr)
		return
	}
	patterns, apiErr := f.Patterns(r.Context(), a.d, p)
	if apiErr != nil {
		dto.WriteError(w, apiErr)
		return
	}
	ids, err := view.StreamIDs(r.Context(), a.d.Store, a.d.Projects.Access(r.Context(), p), patterns)
	if err != nil {
		a.Fail(w, r, err)
		return
	}
	limit := entriesDefault
	if f.Limit > 0 {
		limit = min(f.Limit, entriesMax)
	}
	pg, err := view.ReadLogs(r.Context(), a.d.Store, f, ids, limit)
	if err != nil {
		a.Fail(w, r, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.Page[dto.Entry]{Items: view.Entries(r.Context(), pg.Entries, a.kinds),
		HasMore: pg.HasMore, NextSeq: pg.NextSeq, NextBeforeSeq: pg.NextBeforeSeq})
}

func intParam(r *http.Request, name string, def, limit int) (int, *dto.APIError) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, dto.Errorf(dto.CodeBadRequest, "%s must be a positive integer.", name)
	}
	return min(n, limit), nil
}

func (a *API) context(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	seq, err := strconv.ParseInt(r.PathValue("seq"), 10, 64)
	notFound := dto.Errorf(dto.CodeNotFound, "There is no entry %s; retention may have removed it.", r.PathValue("seq"))
	if err != nil {
		dto.WriteError(w, notFound)
		return
	}
	before, apiErr := intParam(r, "before", 20, 200)
	if apiErr != nil {
		dto.WriteError(w, apiErr)
		return
	}
	after, apiErr := intParam(r, "after", 20, 200)
	if apiErr != nil {
		dto.WriteError(w, apiErr)
		return
	}
	acc := a.d.Projects.Access(r.Context(), p)
	focus, err := a.d.Store.Get(r.Context(), seq)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (store.IsMessageStream(focus.Stream) || !acc.Can(focus.Stream, auth.ScopeRead))) {
		dto.WriteError(w, notFound)
		return
	}
	if err != nil {
		a.Fail(w, r, err)
		return
	}
	around, err := a.d.Store.Around(r.Context(), seq, before, after)
	if err != nil {
		a.Fail(w, r, err)
		return
	}
	page := dto.Page[dto.Entry]{Items: view.Entries(r.Context(), around, a.kinds), FocusSeq: seq}
	if n := len(around); n > 0 {
		page.NextSeq = around[n-1].Seq + 1
		if around[0].Seq < seq || before == 0 {
			page.NextBeforeSeq = around[0].Seq
		}
	}
	dto.WriteJSON(w, http.StatusOK, page)
}

const maxLabel = 1000

// mark writes a marker into a log stream, as the MCP mark tool does.
func (a *API) mark(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var req dto.MarkCreate
	if !Decode(w, r, &req) {
		return
	}
	label := strings.TrimSpace(req.Label)
	switch {
	case label == "":
		dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "A marker needs a label."))
		return
	case len(label) > maxLabel:
		dto.WriteError(w, dto.Errorf(dto.CodeTooLarge, "A marker label is at most %d bytes.", maxLabel))
		return
	case store.IsMessageStream(req.Stream):
		dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "Markers go into log streams; post in the chat instead."))
		return
	}
	ids, err := a.d.Store.MatchStreams(r.Context(), []string{req.Stream})
	if err != nil {
		a.Fail(w, r, err)
		return
	}
	acc := a.d.Projects.Access(r.Context(), p)
	if !slices.Contains(ids, req.Stream) || !acc.Can(req.Stream, auth.ScopeRead) {
		dto.WriteError(w, dto.Errorf(dto.CodeNotFound, "There is no stream %q.", req.Stream))
		return
	}
	if !acc.Can(req.Stream, auth.ScopeMessage) {
		dto.WriteError(w, dto.Errorf(dto.CodeForbidden, "Marking %s needs message on it; ask the admin for a grant.", req.Stream))
		return
	}
	seq, err := a.d.Writer.Commit(r.Context(), store.Entry{Stream: req.Stream, Kind: store.KindMarker, Severity: store.DefaultSeverity,
		Text: p.Name + ": " + label, Sender: p.Name})
	if err != nil {
		a.Fail(w, r, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, dto.MarkCreated{Seq: seq})
}

func (a *API) tokens(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	rows, err := a.d.Tokens.ListTokens(r.Context(), "")
	if err != nil {
		a.Fail(w, r, err)
		return
	}
	out := make([]dto.Token, 0, len(rows))
	for _, t := range rows {
		out = append(out, view.Token(t))
	}
	dto.WriteJSON(w, http.StatusOK, map[string][]dto.Token{"tokens": out})
}

func (a *API) createToken(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	var req dto.TokenCreate
	if !Decode(w, r, &req) {
		return
	}
	created, apiErr := view.CreateToken(r.Context(), a.d.Tokens, a.d.Server.BaseURL(), req)
	if apiErr != nil {
		dto.WriteError(w, apiErr)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, created)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

func (a *API) revokeToken(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	id, ok := pathID(r)
	notFound := dto.Errorf(dto.CodeNotFound, "There is no token %s; it may already be revoked.", r.PathValue("id"))
	if !ok {
		dto.WriteError(w, notFound)
		return
	}
	if err := a.d.Tokens.RevokeToken(r.Context(), id); err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			dto.WriteError(w, notFound)
			return
		}
		a.Fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listSessions: every session for an admin, one's own for anyone else.
func (a *API) listSessions(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	who := p.Name
	if p.Has(auth.ScopeAdmin) {
		who = ""
	}
	rows, err := a.sessions.List(r.Context(), who)
	if err != nil {
		a.Fail(w, r, err)
		return
	}
	out := make([]dto.Session, 0, len(rows))
	for _, s := range rows {
		out = append(out, view.Session(s, p.SessionID))
	}
	dto.WriteJSON(w, http.StatusOK, map[string][]dto.Session{"sessions": out})
}

func (a *API) revokeSession(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	id, ok := pathID(r)
	notFound := dto.Errorf(dto.CodeNotFound, "There is no session %s.", r.PathValue("id"))
	if !ok {
		dto.WriteError(w, notFound)
		return
	}
	if err := view.RevokeSession(r.Context(), a.sessions, p, id); errors.Is(err, auth.ErrNotFound) {
		dto.WriteError(w, notFound)
		return
	} else if err != nil {
		a.Fail(w, r, err)
		return
	}
	if id == p.SessionID {
		auth.ClearSessionCookie(w, a.d.Server.Secure)
	}
	w.WriteHeader(http.StatusNoContent)
}

// createPairing makes a QR code. A pairing cannot grant a scope the admin
// does not hold, nor admin itself (auth.Pairings.Create refuses it).
func (a *API) createPairing(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var req dto.PairingCreate
	if !Decode(w, r, &req) {
		return
	}
	scopes, err := view.ParseScopes(req.Scopes)
	if err != nil {
		dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "%v.", err))
		return
	}
	for _, sc := range scopes {
		if !p.Has(sc) {
			dto.WriteError(w, auth.MissingScope(sc))
			return
		}
	}
	row, code, err := a.pairings.Create(r.Context(), strings.TrimSpace(req.Principal), strings.TrimSpace(req.NewName), scopes,
		strings.Join(req.Grants, " "), p.Name, 0)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrNotFound):
			dto.WriteError(w, dto.Errorf(dto.CodeNotFound, "There is no person %q.", req.Principal))
		case errors.Is(err, auth.ErrConflict):
			dto.WriteError(w, dto.Errorf(dto.CodeConflict, "%s.", strings.TrimPrefix(err.Error(), "conflict: ")))
		case errors.Is(err, auth.ErrBadPairing):
			dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "%s.", strings.TrimPrefix(err.Error(), "bad pairing: ")))
		default:
			a.Fail(w, r, err)
		}
		return
	}
	dto.WriteJSON(w, http.StatusCreated, view.Pairing(a.d.Server.BaseURL(), row, code))
}
