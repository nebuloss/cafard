package join

import (
	"net/http"
	"text/template"

	"cafard/internal/store"
)

// installScript connects a machine as an agent: `curl -fsSL …/join/{code} | sh`.
//
// It exchanges the code for the token, keeps the token in a file only its
// user can read, and prints what to run in each MCP client. It does not yet
// register cafard in those clients itself: the MCP server is step 5 of the
// build order, and a client told about a server that answers 404 would show
// it as broken. Step 5 makes the script run `claude mcp add` itself.
//
// POSIX sh, curl only, and no secret in the script: the token exists from
// the moment the script posts the code.
var installScript = template.Must(template.New("install").Funcs(template.FuncMap{"q": shellQuote}).Parse(`#!/bin/sh
# cafard: connect this machine as the agent {{.Name}}.
set -eu

url={{.URL | q}}
code={{.Code | q}}

command -v curl >/dev/null 2>&1 || { echo "cafard: this needs curl" >&2; exit 1; }

dir="${XDG_CONFIG_HOME:-$HOME/.config}/cafard"
umask 077
mkdir -p "$dir"
token=$(curl -fsS -X POST "$url/join/$code/token") || {
	echo "cafard: the link no longer works: ask an admin for a new one" >&2
	exit 1
}
printf '%s\n' "$token" > "$dir/token"
printf 'url=%s\nagent=%s\n' "$url" {{.Name | q}} > "$dir/config"

echo "cafard: this machine is now the agent {{.Name}}."
echo "        token saved in $dir/token (readable by $(id -un) only)"
echo
echo "Check it works:"
echo "  curl -H \"Authorization: Bearer \$(cat $dir/token)\" $url/api/v1/me"
echo
echo "When cafard's MCP server is in place, connect Claude Code with:"
echo "  claude mcp add --scope user --transport http cafard $url/mcp \\"
echo "    --header \"Authorization: Bearer \$(cat $dir/token)\""
`))

type scriptData struct {
	URL, Code, Name string
}

func (j *Join) script(w http.ResponseWriter, code string, id store.Identity) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	installScript.Execute(w, scriptData{URL: j.publicURL, Code: code, Name: id.Name})
}
