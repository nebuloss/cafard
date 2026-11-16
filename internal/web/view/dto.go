package view

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
)

// Principal is the DTO of a principal row.
func Principal(p auth.PrincipalRow) dto.Principal {
	out := dto.Principal{Name: p.Name, Kind: dto.PrincipalKind(p.Kind)}
	if p.LastSeen != nil {
		out.LastSeen = dto.TimePtr(*p.LastSeen)
	}
	return out
}

// Scopes converts a scope set.
func Scopes(s auth.ScopeSet) []dto.Scope {
	out := make([]dto.Scope, len(s))
	for i, sc := range s {
		out[i] = dto.Scope(sc)
	}
	return out
}

// Token is the DTO of a token row, never with its secret.
func Token(t auth.TokenRow) dto.Token {
	out := dto.Token{ID: t.ID, Label: t.Label, Principal: t.Principal, Kind: dto.PrincipalKind(t.Kind),
		Scopes: Scopes(t.Scopes), CreatedAt: dto.TimeOf(t.CreatedAt)}
	if t.LastUsed != nil {
		out.LastUsed = dto.TimePtr(*t.LastUsed)
	}
	return out
}

// Session is the DTO of a session row; current marks the caller's own.
func Session(s auth.SessionRow, current int64) dto.Session {
	return dto.Session{ID: s.ID, Label: s.Label, Principal: s.Principal, UserAgent: s.UserAgent,
		CreatedAt: dto.TimeOf(s.CreatedAt), ExpiresAt: dto.TimeOf(s.ExpiresAt), Current: s.ID == current}
}

// PairURL is the address a pairing QR holds.
func PairURL(base, code string) string { return base + "/p/" + code }

// PairQRURL is where the QR image of a pairing is served.
func PairQRURL(id int64) string { return "/pair/" + strconv.FormatInt(id, 10) + "/qr.svg" }

// Pairing is the DTO of a pending pairing. code may be "" when this process
// no longer holds it (after a restart); url is then empty too.
func Pairing(base string, p auth.PairingRow, code string) dto.Pairing {
	out := dto.Pairing{ID: p.ID, QRURL: PairQRURL(p.ID), ExpiresAt: dto.TimeOf(p.ExpiresAt), Principal: p.Principal, NewName: p.NewName}
	if code != "" {
		out.URL = PairURL(base, code)
	}
	return out
}

// TokenSnippets are the ready-to-paste client configurations for a new
// token, with the server address and the token filled in. Which ones are
// offered follows the token's scopes.
func TokenSnippets(base, principal, secret string, scopes auth.ScopeSet) []dto.Snippet {
	var out []dto.Snippet
	mcpURL := base + "/mcp"
	if scopes.Has(auth.ScopeRead) {
		out = append(out,
			dto.Snippet{Label: "Claude Code", Language: dto.LangShell,
				Text: fmt.Sprintf("claude mcp add --transport http cafard %s --header \"Authorization: Bearer %s\"", mcpURL, secret)},
			dto.Snippet{Label: "Codex (~/.codex/config.toml)", Language: dto.LangTOML,
				Text: fmt.Sprintf("# export CAFARD_TOKEN=%s in the shell that runs codex\n[mcp_servers.cafard]\nurl = %q\nbearer_token_env_var = \"CAFARD_TOKEN\"", secret, mcpURL)},
			dto.Snippet{Label: "Other MCP clients (JSON)", Language: dto.LangJSON,
				Text: fmt.Sprintf("{\n  \"mcpServers\": {\n    \"cafard\": {\n      \"type\": \"http\",\n      \"url\": %q,\n      \"headers\": { \"Authorization\": \"Bearer %s\" }\n    }\n  }\n}", mcpURL, secret)},
			// Track G: for clients that only start local commands.
			dto.Snippet{Label: "Clients that speak stdio only (cafard mcp)", Language: dto.LangJSON,
				Text: fmt.Sprintf("{\n  \"mcpServers\": {\n    \"cafard\": {\n      \"command\": \"cafard\",\n      \"args\": [\"mcp\", \"--server\", %q],\n      \"env\": { \"CAFARD_TOKEN\": \"%s\" }\n    }\n  }\n}", base, secret)},
		)
	}
	if scopes.Has(auth.ScopeIngest) {
		out = append(out,
			dto.Snippet{Label: "OpenTelemetry exporter", Language: dto.LangEnv, Text: otelEnv(base, secret)},
			dto.Snippet{Label: "cafard push (Linux)", Language: dto.LangShell,
				Text: fmt.Sprintf("journalctl -f | cafard push --server %s --token %s --stream %s", base, secret, principal)},
			dto.Snippet{Label: "cafard push (Windows PowerShell)", Language: dto.LangShell,
				Text: fmt.Sprintf("Get-Content C:\\logs\\app.log -Wait | .\\cafard.exe push --server %s --token %s --stream %s", base, secret, principal)},
		)
	}
	out = append(out, dto.Snippet{Label: "Environment", Language: dto.LangEnv,
		Text: fmt.Sprintf("CAFARD_SERVER=%s\nCAFARD_TOKEN=%s", base, secret)})
	return out
}

func otelEnv(base, secret string) string {
	return fmt.Sprintf("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=%s/v1/logs\nOTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/protobuf\nOTEL_EXPORTER_OTLP_LOGS_HEADERS=Authorization=Bearer%%20%s",
		base, secret)
}

// Host is the host part of base, for commands that take a bare host.
func Host(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" {
		return "localhost"
	}
	return u.Hostname()
}

// SourceCommands are the empty-state commands of Live and Sources (PLAN
// 9.6.6): ways to send a first line, with public_url filled in. The token
// is a placeholder; /agents makes real ones.
func SourceCommands(base string) []dto.Snippet {
	host := Host(base)
	const tok = "cfd_YOUR_TOKEN"
	return []dto.Snippet{
		{Label: "Syslog", Language: dto.LangShell, Text: fmt.Sprintf("logger -n %s -P 5514 -T hello", host)},
		{Label: "Netconsole (kernel command line)", Language: dto.LangShell, Text: fmt.Sprintf("netconsole=@/,6666@%s/", host)},
		{Label: "OpenTelemetry exporter", Language: dto.LangEnv, Text: otelEnv(base, tok)},
		{Label: "cafard push (Linux)", Language: dto.LangShell, Text: fmt.Sprintf("journalctl -f | cafard push --server %s --token %s --stream laptop", base, tok)},
		{Label: "cafard push (Windows PowerShell)", Language: dto.LangShell, Text: fmt.Sprintf("Get-Content C:\\logs\\app.log -Wait | .\\cafard.exe push --server %s --token %s --stream laptop", base, tok)},
		{Label: "curl", Language: dto.LangShell, Text: fmt.Sprintf("echo hello | curl -H \"Authorization: Bearer %s\" --data-binary @- \"%s/api/log?stream=laptop\"", tok, base)},
	}
}

// MCPCommand is the empty-state snippet that connects an agent.
func MCPCommand(base string) string {
	return fmt.Sprintf("claude mcp add --transport http cafard %s/mcp --header \"Authorization: Bearer cfd_YOUR_TOKEN\"", base)
}

// Join is strings.Join for templates that cannot call functions.
func Join(s []string) string { return strings.Join(s, ", ") }
