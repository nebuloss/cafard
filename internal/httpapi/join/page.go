package join

import (
	"html/template"
	"net/http"
	"time"
)

type pageData struct {
	Title    string
	Name     string
	Username string
	Expires  time.Time
	Error    string
	Spent    bool
	// Agent is an agent's link opened in a browser: the commands that run
	// it, by shell.
	Agent   bool
	Sh, PS1 string
}

func page(w http.ResponseWriter, status int, d pageData) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	// The page needs nothing from anywhere: its style is inline and it has
	// no script.
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
	w.WriteHeader(status)
	pageTmpl.Execute(w, d)
}

// The page uses claudication's MD3 tokens, as the admin UI and the chat app
// do, so a person meets the same look on their phone before they have
// signed in.
var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="robots" content="noindex">
<title>{{.Title}} · cafard</title>
<style>
:root { --primary:#8f4c33; --on-primary:#fff; --surface:#faf9f7; --on-surface:#1c1b19; --on-surface-variant:#4a4643;
  --surface-lowest:#fff; --outline-variant:#cbc5bf; --error:#ba1a1a; --error-container:#ffdad6; --success-container:#bcf0b4; --on-success-container:#002105; color-scheme: light; }
@media (prefers-color-scheme: dark) { :root { --primary:#ffb59d; --on-primary:#55200a; --surface:#141312; --on-surface:#e9e5e1;
  --on-surface-variant:#c9c4be; --surface-lowest:#0f0e0d; --outline-variant:#4c4743; --error:#ffb4ab; --error-container:#93000a;
  --success-container:#235024; --on-success-container:#bcf0b4; color-scheme: dark; } }
* { box-sizing: border-box; }
body { margin:0; min-height:100vh; display:grid; place-items:center; background:var(--surface); color:var(--on-surface);
  font:15px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif; padding:24px max(16px, env(safe-area-inset-left)); }
main { width:min(400px,100%); display:flex; flex-direction:column; gap:16px; }
.brand { display:flex; align-items:center; gap:8px; font-weight:700; font-size:18px; color:var(--primary); }
.brand svg { width:28px; height:28px; }
h1 { margin:0; font-size:22px; text-wrap:balance; }
p { margin:0; color:var(--on-surface-variant); }
button { width:100%; font:inherit; font-weight:600; min-height:46px; border:0; border-radius:999px; background:var(--primary); color:var(--on-primary); cursor:pointer; }
.err { padding:10px 14px; border-radius:12px; background:var(--error-container); color:var(--on-surface); }
.user { font-weight:700; color:var(--on-surface); }
small { color:var(--on-surface-variant); }
.cmd { display:flex; flex-direction:column; gap:6px; }
.cmd span { font-weight:600; font-size:13px; }
pre { margin:0; padding:10px 12px; border-radius:12px; border:1px solid var(--outline-variant); background:var(--surface-lowest);
  font:13px/1.45 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace; white-space:pre-wrap; word-break:break-all; user-select:all; }
</style>
</head>
<body>
<main>
  <div class="brand"><svg viewBox="0 0 32 32" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><ellipse cx="16" cy="19" rx="7" ry="9.5" fill="currentColor" fill-opacity=".18"/><path d="M16 10v18"/><path d="M13 9.5C11 6 8 4 5 4M19 9.5C21 6 24 4 27 4"/><path d="M9.5 16 5 14M9.5 21 4.5 22M22.5 16 27 14M22.5 21l5 1"/></svg>cafard</div>
{{- if .Spent}}
  <h1>{{.Title}}</h1>
  <div class="err">{{.Error}}</div>
{{- else if .Agent}}
  <h1>Set up {{.Name}}</h1>
  <p>Run on its machine, as the user its agents run as.</p>
  <div class="cmd"><span>Linux / macOS</span><pre>{{.Sh}}</pre></div>
  <div class="cmd"><span>Windows (PowerShell)</span><pre>{{.PS1}}</pre></div>
  <small>Works once, until {{.Expires.Format "2 Jan 15:04"}}.</small>
{{- else}}
  <h1>Sign in as {{.Name}}</h1>
  <p>On this device, as <span class="user">{{.Username}}</span>.</p>
  <form method="post"><button type="submit" autofocus>Sign in</button></form>
  <small>Works once, until {{.Expires.Format "2 Jan 15:04"}}.</small>
{{- end}}
</main>
</body>
</html>
`))
