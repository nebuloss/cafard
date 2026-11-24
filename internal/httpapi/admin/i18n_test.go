package admin_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// callIn is call with the language the admin UI chose, in its cookie.
func (r *rig) callIn(lang, method, path, body string) (int, map[string]any) {
	r.t.Helper()
	req, _ := http.NewRequest(method, r.adminSrv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(&http.Cookie{Name: "cafard_lang", Value: lang})
	resp, err := r.client.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// The admin API answers in the language the UI chose: its own sentences,
// the refusals service/access makes, and the notices. The codes stay what
// a client switches on.
func TestAnswersInFrench(t *testing.T) {
	r := newRig(t)
	code, out := r.callIn("fr", "POST", "/admin/api/signin", `{"password":"not the password"}`)
	if code != 403 || out["error"] != "wrong" || out["message"] != "Ce n'est pas le mot de passe admin." {
		t.Errorf("a wrong password, in French: %d %v", code, out)
	}
	r.signIn()
	code, out = r.callIn("fr", "POST", "/admin/api/projects", `{"name":"api"}`)
	if code != 400 || out["error"] != "bad_name" ||
		out["message"] != "\"api\" est un mot réservé aux adresses de cafard\u00a0: choisissez un autre nom de projet." {
		t.Errorf("a refusal from service/access, in French: %d %v", code, out)
	}
	code, out = r.callIn("en", "POST", "/admin/api/projects", `{"name":"api"}`)
	if code != 400 || out["message"] != "\"api\" is a word cafard's addresses use: choose another project name." {
		t.Errorf("the same in English: %d %v", code, out)
	}

	r.call("POST", "/admin/api/projects", map[string]any{"name": "lonely"})
	_, out = r.callIn("fr", "GET", "/admin/api/notices", "")
	found := false
	for _, n := range out["notices"].([]any) {
		if n := n.(map[string]any); n["id"] == "empty:lonely" {
			found = n["title"] == "lonely n'a aucun membre"
		}
	}
	if !found {
		t.Errorf("the empty project's notice, in French: %v", out)
	}
}
