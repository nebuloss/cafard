# AGENTS.md: cafard

cafard collects logs and images from devices and people (phone web page,
syslog, netconsole, serial ports, a push client that runs on Windows). It
serves them to AI agents over MCP and passes messages between agents and
humans.

**Read `PLAN.md`, then `CONTRACTS.md`.** `PLAN.md` is the specification:
data model, MCP tools, frontend architecture, build, CI, deployment and
milestones. `CONTRACTS.md` holds the interfaces between parts and wins on
any contract the two disagree on. Record every deviation, with the reason,
in `DECISIONS.md`.

## Status

M0 and M1 are done. Next is M2 (track A: messaging, channels, claims,
images, log attachments), which may run beside B, C, E1 and F now that git
and the remote exist (CONTRACTS.md section 7).

What exists:
- `cmd/cafard`: `serve`, `config`, `version`, `token create|list|revoke`,
  `push` from stdin. `term` and `mcp` say which milestone brings them.
- `internal/store` (migrations, entry queries, retention janitor),
  `writer`, `hub`, `status`, `textclean`, `auth` (tokens, bearer auth,
  same-origin check, exposure guard), `deps`, `dto` with fixtures in
  `internal/web/testdata/api`, `units`, `clock`.
- `internal/ingest/httpapi` (`POST /api/log`), `internal/push`.
- `internal/mcpserver`: streamable HTTP at `/mcp` with `whoami`,
  `list_streams`, `read`, `tail`, `wait_for`, `context`, `mark`.
- `internal/web/assets` (embed, `bundleBuilt`, minimal `layout.html`),
  `src/core` with tests, CI and release workflows, `scripts/remote.sh`.

## Conventions come from the sibling projects

This project follows the user's other Go projects in `~/misc`. **grafit**
covers Go + TypeScript classes, the embedded Vite bundle, the hardened
systemd unit and the installer. **piCVert** covers OpenRC, the LXC container
drop-in and `devdeploy.sh`. `PLAN.md` section 13 lists the exact files to
copy from. When unsure, do what grafit does.

## Build and test

Two halves: a Go server, and a TypeScript bundle that Go embeds. Build the
bundle first, or the binary ships without it.

```sh
npm ci
npm run typecheck        # tsc; vite does not typecheck
npm test                 # vitest: pure logic, renderers under happy-dom
npm run build            # src/ -> internal/web/assets/static/app/main.js
scripts/check-sinks.sh   # the CI grep for HTML sinks in src/

scripts/remote.sh        # sync to dev-build: gofmt -l, build, vet, test -race
scripts/remote.sh <cmd>  # run anything in the remote tree
scripts/devdeploy.sh     # (M6) build on dev-build, push the binary to the appliance
```

`remote.sh` rsyncs the working tree, including the built `main.js`, so run
`npm run build` first when the Go tests should see the bundle.

- **Go checks run on dev-build** through `scripts/remote.sh`. The local Go is
  older than the `go 1.26.0` in `go.mod`. A pass on dev-build is the only pass
  that counts.
- **Binaries that ship** (including `cafard.exe` for Windows) come from CI on
  a `v*` tag, or from dev-build through `devdeploy.sh` for testing. Never
  build them on the editing machine.
- **No cgo, ever.** cafard cross-compiles to linux amd64/arm64/armv7 and
  windows amd64/arm64. A dependency that quietly needs cgo breaks the
  deployment targets without breaking any test. (`go test -race` does use
  cgo for the test binary; that is why it runs on dev-build and in CI.)

## Gotchas that will cost you an afternoon

- **Templates and static files are under `internal/web/assets/`,** not
  `internal/web/`: `go:embed` cannot reach a parent directory.
- **Adding a config key:** add a field (with a `yaml` tag) to your package's
  config struct, and one field of that type to `config.Config`. Its flag and
  environment variable appear on their own; lists and maps are file-only.
  Unknown keys in the file are errors, so the example config must not list
  keys whose feature has not landed.
- **Build the mux once** (`newHandler` in `cmd/cafard/serve.go`). Registering
  the same pattern twice panics at start, not at compile time.
- **Severity 0 is emergency.** `store.Entry{}` has severity 0, so every
  producer sets `store.DefaultSeverity` (6) unless it knows better, and
  `store.Query.MinSeverity` is a pointer for the same reason.
- **A feature cannot import `internal/config`** (config imports the
  features). Take your section as a `Register` argument; read root settings
  from `deps.Server`.
- **Add MCP tools inside `Register`, before `mcpserver.Register` mounts
  `/mcp`.** `AddTool` panics afterwards: the per-scope SDK servers are
  already built.
- **Fixtures:** `go test ./internal/dto -update` rewrites
  `internal/web/testdata/api/*.json`; CI fails if they are stale. A new DTO
  gets a fixture in the same change.
- **`go test -race ./cmd/cafard` takes about 100 s** on dev-build: the M1
  "Done when" test pushes 100 000 lines through a real server.
- **`scripts/check-sinks.sh` greps comments and strings too.** Do not name
  the forbidden sinks anywhere in `src/`, tests included.

- **`deploy/cafard.yaml.example` is tested** (`cmd/cafard/deploy_example_test.go`):
  it must load, equal the defaults, and mention every key `cafard config`
  prints. When your feature adds a config section, replace its commented
  "available once ... lands" block in the example with the real keys, or
  the test fails after the merge.
- **The installer creates the admin token, not the first `serve`**, so it
  never lands in the journal (DECISIONS, track F). Changing the name or
  scopes in `Tokens.Bootstrap` means changing `deploy/install.sh` too.
- **The systemd unit allows only ttyUSB, ttyACM and ttyS devices** and only
  `/var/lib/cafard` for writes. A feature that opens another device class
  or writes elsewhere must change `deploy/cafard.service`.

## Rules that are easy to break

- **No `innerHTML` and its relatives in `src/`.** Log lines and messages come
  from anyone on the LAN. CI greps for these sinks.
- **No frontend framework and no CSS framework.** Plain DOM in TypeScript
  classes, as in grafit. `PLAN.md` section 9.4 lists the allowed patterns and
  the problem each one solves; don't add others.
- **The Chats pages and the Sources (`/streams`) page must work without
  the bundle.** A test pins this.
- **Only the writer goroutine writes `entries`** (and `entries_fts`).
  Everything else sends entries to it. Other tables are written directly
  in their own short transactions, and any marker they produce goes
  through the writer. The order of `seq` is the order of commits, and
  agents' cursors depend on that.
- **`wait_for` must not lose an entry that arrives between "read the store"
  and "subscribe to the hub".** A test pins this.
- **A message's sender comes from the authenticated token or session, never
  from the request.**
- **Never log tokens, session ids, message text or image contents.** Never
  rsync the database or `cafard.yaml` to another machine.
- **Use standard protocols** (`PLAN.md` section 2.1): MCP, OTLP/HTTP, syslog,
  netconsole, RFC 2217, SSE. Never import the OTLP collector packages: they
  pull in gRPC. Use the message types only.
- **Do not use em dashes in source code.**
- **Hardware checks are manual.** Real serial ports, a phone and Windows COM
  ports are checks the user runs. Automated tests use fakes behind
  interfaces.
