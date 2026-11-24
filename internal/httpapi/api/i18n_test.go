package api

import (
	"io"
	"net/http"
	"testing"
)

// A refusal is said in the asker's language; curl, which names none, is
// answered in English. The code is the same either way.
func TestRefusalsInTheAskersLanguage(t *testing.T) {
	r := newRig(t)
	ask := func(lang string) (int, map[string]string) {
		req, _ := http.NewRequest("GET", r.srv.URL+"/api/v1/projects/be98/conversations", nil)
		req.Header.Set("Authorization", "Bearer "+r.outsider)
		if lang != "" {
			req.Header.Set("Accept-Language", lang)
		}
		resp, err := r.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var out map[string]string
		decode(t, b, &out)
		return resp.StatusCode, out
	}
	code, out := ask("fr-FR,fr;q=0.9,en;q=0.8")
	if code != http.StatusForbidden || out["error"] != "not_member" ||
		out["message"] != "lab-bench n'est pas membre de be98. Demandez à une personne du projet de l'ajouter." {
		t.Errorf("in French: %d %v", code, out)
	}
	code, out = ask("")
	if code != http.StatusForbidden || out["error"] != "not_member" ||
		out["message"] != "lab-bench is not a member of be98. Ask a person in the project to add it." {
		t.Errorf("for curl: %d %v", code, out)
	}
}
