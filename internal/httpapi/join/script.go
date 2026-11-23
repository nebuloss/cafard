package join

import (
	"net/http"
	"text/template"

	"cafard/internal/store"
)

// installScript connects a machine as an agent: `curl -fsSL …/join/{code} | sh`.
//
// It exchanges the code for the token, keeps the token in a file only its
// user can read, and registers cafard's MCP server in Claude Code when it is
// installed, once per machine, at user scope: every session on the machine
// then has it, and select_project says which project each works in
// (docs/design.md, "One agent per machine"). A registration left by an
// earlier link is replaced, so running a new link is how a token is changed.
// Other MCP clients get the URL and the header to set.
//
// Claude Code keeps the header in its own configuration, ~/.claude.json,
// which is its user's alone like the token file.
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
if command -v claude >/dev/null 2>&1; then
	claude mcp remove --scope user cafard >/dev/null 2>&1 || true
	if claude mcp add --scope user --transport http cafard "$url/mcp" \
		--header "Authorization: Bearer $token" >/dev/null; then
		echo "Claude Code: cafard is registered for every session of $(id -un)."
		echo "  Start a session and say \"check cafard\", or name the project: \"check cafard, project <name>\"."
	else
		echo "Claude Code: registering cafard failed. Run:"
		echo "  claude mcp add --scope user --transport http cafard $url/mcp \\"
		echo "    --header \"Authorization: Bearer \$(cat $dir/token)\""
	fi
else
	echo "Claude Code is not installed here. Once it is, run:"
	echo "  claude mcp add --scope user --transport http cafard $url/mcp \\"
	echo "    --header \"Authorization: Bearer \$(cat $dir/token)\""
fi
echo
echo "Other MCP clients: Streamable HTTP at $url/mcp, with the header"
echo "  Authorization: Bearer <the token in $dir/token>"
`))

type scriptData struct {
	URL, Code, Name string
}

func (j *Join) script(w http.ResponseWriter, code string, id store.Identity) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	installScript.Execute(w, scriptData{URL: j.publicURL, Code: code, Name: id.Name})
}
