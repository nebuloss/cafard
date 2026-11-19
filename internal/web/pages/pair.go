package pages

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rsc.io/qr"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/web/view"
)

func (s *Site) mountPair(mux *http.ServeMux) {
	mux.HandleFunc("GET /pair", s.Auth(auth.ScopeAdmin, s.pair))
	mux.Handle("POST /pair", s.Write(auth.ScopeAdmin, s.createPairing))
	mux.HandleFunc("GET /pair/{id}/qr.svg", s.Auth(auth.ScopeAdmin, s.pairQR))
}

type pendingView struct {
	ID                                     int64
	Name, URL, QR, ExpiresISO, ExpiresText string
	Grants                                 string
}

type sessionView struct {
	ID                                 int64
	Label, Principal, Created, Expires string
	Current                            bool
}

type pairData struct {
	Pending  []pendingView
	Humans   []string
	Sessions []sessionView
}

func (s *Site) renderPair(w http.ResponseWriter, r *http.Request, p auth.Principal, status int, errText string) {
	ctx := r.Context()
	pending, err := s.pairings.Pending(ctx)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	data := pairData{}
	base := s.d.Server.BaseURL()
	for _, row := range pending {
		code, _, _ := s.pairings.Code(ctx, row.ID)
		dp := view.Pairing(base, row, code)
		data.Pending = append(data.Pending, pendingView{ID: row.ID, Name: row.Name(), URL: dp.URL, QR: dp.QRURL,
			ExpiresISO: row.ExpiresAt.Format(time.RFC3339), ExpiresText: row.ExpiresAt.UTC().Format("15:04:05"),
			Grants: strings.Join(dp.Grants, ", ")})
	}
	humans, err := s.d.Tokens.Principals(ctx, auth.KindHuman)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	for _, h := range humans {
		data.Humans = append(data.Humans, h.Name)
	}
	sessions, err := s.sessions.List(ctx, "")
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	for _, se := range sessions {
		label := se.Label
		if label == "" {
			label = "Browser"
		}
		data.Sessions = append(data.Sessions, sessionView{ID: se.ID, Label: label, Principal: se.Principal,
			Created: Day(se.CreatedAt), Expires: Day(se.ExpiresAt), Current: se.ID == p.SessionID})
	}
	l := s.Shell(r, &p, "Pair a phone", "pair", "pair")
	l.Error = errText
	s.Render(w, status, "pair", l, []string{"tpl-toast"}, data)
}

func (s *Site) pair(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	s.renderPair(w, r, p, http.StatusOK, "")
}

func (s *Site) createPairing(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	principal := strings.TrimSpace(r.PostFormValue("principal"))
	newName := strings.TrimSpace(r.PostFormValue("new_name"))
	if principal != "" && newName != "" {
		s.renderPair(w, r, p, http.StatusBadRequest, "Choose an existing person or type a new name, not both.")
		return
	}
	if principal == "" && newName == "" {
		s.renderPair(w, r, p, http.StatusBadRequest, "Choose who the phone signs in as.")
		return
	}
	if _, _, err := s.pairings.Create(r.Context(), principal, newName, nil, r.PostFormValue("grants"), p.Name, 0); err != nil {
		if errors.Is(err, auth.ErrConflict) || errors.Is(err, auth.ErrNotFound) || errors.Is(err, auth.ErrBadPairing) {
			s.renderPair(w, r, p, http.StatusBadRequest, sentence(err.Error()))
			return
		}
		s.Oops(w, r, &p, err)
		return
	}
	http.Redirect(w, r, "/pair", http.StatusSeeOther)
}

func sentence(s string) string {
	for _, prefix := range []string{"conflict: ", "not found: ", "bad pairing: "} {
		s = strings.TrimPrefix(s, prefix)
	}
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// pairQR draws the QR of a pending pairing as SVG: crisp at any size, and
// always dark on white with its quiet zone, which is what cameras expect.
func (s *Site) pairQR(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	code, _, ok := s.pairings.Code(r.Context(), id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	svg, err := QRSVG(view.PairURL(s.d.Server.BaseURL(), code))
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(svg)
}

// QRSVG encodes text as an SVG QR code with a 4-module quiet zone.
func QRSVG(text string) ([]byte, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	const quiet = 4
	n := c.Size + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return []byte(b.String()), nil
}
