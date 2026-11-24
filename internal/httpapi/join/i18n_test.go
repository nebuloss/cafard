package join

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func getIn(t *testing.T, u, lang string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone)")
	req.Header.Set("Accept-Language", lang)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// The join pages are in the browser's language, and say which it is.
func TestJoinPagesInFrench(t *testing.T) {
	r := newRig(t)
	code, body := getIn(t, r.link(t, r.camille), "fr-FR,fr;q=0.9")
	for _, want := range []string{`<html lang="fr">`, "Se connecter en tant que Camille", "Sur cet appareil, en tant que",
		">Se connecter</button>", "Valable une fois, jusqu&#39;au "} {
		if code != 200 || !strings.Contains(body, want) {
			t.Errorf("a person's link in French lacks %q: %d\n%s", want, code, body)
		}
	}
	code, body = getIn(t, r.link(t, r.devCode), "fr")
	for _, want := range []string{`<html lang="fr">`, "Installer dev-code", "Linux / macOS", `aria-current="true">Claude Code</a>`} {
		if code != 200 || !strings.Contains(body, want) {
			t.Errorf("an agent's link in French lacks %q: %d\n%s", want, code, body)
		}
	}
	code, body = getIn(t, r.srv.URL+"/join/nonsense", "fr")
	for _, want := range []string{"Ce lien ne fonctionne plus", "Demandez-en un nouveau à un admin."} {
		if code != http.StatusGone || !strings.Contains(body, want) {
			t.Errorf("a spent link in French lacks %q: %d\n%s", want, code, body)
		}
	}
	if _, body := getIn(t, r.link(t, r.camille), "de"); !strings.Contains(body, `<html lang="en">`) || !strings.Contains(body, "Sign in as Camille") {
		t.Errorf("a language cafard does not have is English:\n%s", body)
	}
}
