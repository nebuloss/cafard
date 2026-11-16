package api

import (
	"errors"
	"net/http"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/blob"
)

// blob serves /blob/{sha}?v=thumb|agent to whoever can see a message that
// references it; anyone else gets 404, not 403, so ids leak nothing.
func (h *messagesAPI) blob(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	sha := r.PathValue("sha")
	variant := r.URL.Query().Get("v")
	if variant != blob.VariantOriginal && variant != blob.VariantAgent && variant != blob.VariantThumb {
		http.NotFound(w, r)
		return
	}
	ok, err := h.svc.Blobs().VisibleTo(r.Context(), sha, p)
	if err != nil {
		h.d.Log.Error("blob visibility failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, row, err := h.svc.Blobs().Open(r.Context(), sha, variant)
	if errors.Is(err, blob.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.d.Log.Error("blob open failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	hd := w.Header()
	hd.Set("Content-Type", row.Mime)
	hd.Set("Content-Disposition", "inline")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	hd.Set("Cache-Control", "private, max-age=3600")
	hd.Set("ETag", `"`+row.SHA256+`"`)
	http.ServeContent(w, r, "", row.CreatedAt.Time, f)
}
