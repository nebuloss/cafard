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

M0 (skeleton and pipeline) is done. Next is M1: store, writer, hub, tokens,
`POST /api/log`, `cafard push` from stdin, and the MCP log tools.

What exists: `cmd/cafard` (subcommands; `serve`, `config` and `version` work,
the others say which milestone brings them), `internal/config`,
`internal/web/assets` (embed, `bundleBuilt`, minimal `layout.html`),
`src/core` with tests, `src/pages/PageRegistry.ts`, `src/App.ts`, CI and
release workflows, `scripts/remote.sh`, `scripts/check-sinks.sh`.

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
- **`scripts/check-sinks.sh` greps comments and strings too.** Do not name
  the forbidden sinks anywhere in `src/`, tests included.

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
