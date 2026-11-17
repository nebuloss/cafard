package pages

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/web/view"
)

func (s *Site) mountAgents(mux *http.ServeMux) {
	mux.HandleFunc("GET /agents", s.Auth(auth.ScopeAdmin, s.agents))
	mux.Handle("POST /tokens", s.Write(auth.ScopeAdmin, s.createToken))
	mux.Handle("POST /tokens/{id}/revoke", s.Write(auth.ScopeAdmin, s.revokeToken))
}

type tokenRow struct {
	ID                               int64
	Label, Scopes, Created, LastUsed string
}

type principalRow struct {
	Name, Kind, LastSeen string
	Tokens               []tokenRow
}

type scopeOption struct {
	Name    string
	Checked bool
}

type agentsData struct {
	Principals  []principalRow
	Scopes      []scopeOption
	Created     *createdView
	MinPassword int
}

type createdView struct {
	Principal string
	Secret    string
	Snippets  []dto.Snippet
}

func (s *Site) agentsData(r *http.Request) (agentsData, error) {
	ctx := r.Context()
	principals, err := s.d.Tokens.Principals(ctx, "")
	if err != nil {
		return agentsData{}, err
	}
	tokens, err := s.d.Tokens.ListTokens(ctx, "")
	if err != nil {
		return agentsData{}, err
	}
	byPrincipal := map[string][]tokenRow{}
	for _, t := range tokens {
		lu := "never"
		if t.LastUsed != nil {
			lu = Day(*t.LastUsed)
		}
		label := t.Label
		if label == "" {
			label = "(no label)"
		}
		byPrincipal[t.Principal] = append(byPrincipal[t.Principal], tokenRow{ID: t.ID, Label: label,
			Scopes: strings.Join(t.Scopes.Strings(), ", "), Created: Day(t.CreatedAt), LastUsed: lu})
	}
	data := agentsData{MinPassword: auth.MinPasswordLength}
	for _, p := range principals {
		data.Principals = append(data.Principals, principalRow{Name: p.Name, Kind: p.Kind, LastSeen: lastSeen(p.LastSeen), Tokens: byPrincipal[p.Name]})
	}
	for _, sc := range auth.GrantableScopes {
		data.Scopes = append(data.Scopes, scopeOption{Name: string(sc), Checked: sc == auth.ScopeRead || sc == auth.ScopeMessage})
	}
	return data, nil
}

func (s *Site) renderAgents(w http.ResponseWriter, r *http.Request, p auth.Principal, status int, created *createdView, errText string) {
	data, err := s.agentsData(r)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	data.Created = created
	l := s.Shell(r, &p, "Agents", "agents", "agents")
	l.Error = errText
	if r.URL.Query().Get("revoked") == "1" {
		l.Notice = "Token revoked."
	}
	s.Render(w, status, "agents", l, []string{"tpl-toast"}, data)
}

func (s *Site) agents(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	s.renderAgents(w, r, p, http.StatusOK, nil, "")
}

// createToken is the no-JS token form. The answer is the Agents page with
// the token shown once; it is never stored or shown again.
func (s *Site) createToken(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		s.renderAgents(w, r, p, http.StatusBadRequest, nil, "The form could not be read.")
		return
	}
	req := dto.TokenCreate{Principal: r.PostForm.Get("principal"), Kind: dto.PrincipalKind(r.PostForm.Get("kind")), Label: r.PostForm.Get("label")}
	for _, sc := range r.PostForm["scopes"] {
		req.Scopes = append(req.Scopes, dto.Scope(sc))
	}
	created, apiErr := view.CreateToken(r.Context(), s.d.Tokens, s.d.Server.BaseURL(), req)
	if apiErr != nil {
		s.renderAgents(w, r, p, dto.HTTPStatus(apiErr.Code), nil, apiErr.Message)
		return
	}
	s.renderAgents(w, r, p, http.StatusCreated, &createdView{Principal: created.Principal, Secret: created.Secret, Snippets: created.Snippets}, "")
}

func (s *Site) revokeToken(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		err = s.d.Tokens.RevokeToken(r.Context(), id)
	}
	if err != nil {
		if errors.Is(err, auth.ErrNotFound) || errors.Is(err, strconv.ErrSyntax) {
			s.Fail(w, r, &p, http.StatusNotFound, "Not found", "There is no such token; it may already be revoked.")
			return
		}
		s.Oops(w, r, &p, err)
		return
	}
	http.Redirect(w, r, "/agents?revoked=1", http.StatusSeeOther)
}
