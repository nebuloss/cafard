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
  `passwd` (the admin password), `pair` (a sign-in link), `push`, `term`,
  `mcp`, `send` (`--section` for a gallery's batch).
- `internal/store` (migrations, entry queries, retention janitor),
  `writer`, `hub`, `status`, `textclean`, `auth` (tokens, sessions,
  pairings, the admin password and its in-memory sessions, bearer auth,
  same-origin check, exposure guard), `ratelimit`, `deps`, `dto` with fixtures in
  `internal/web/testdata/api`, `units`, `clock`.
- `internal/ingest/httpapi` (`POST /api/log`), `internal/push`.
- `internal/mcpserver`: streamable HTTP at `/mcp` with `whoami`,
  `list_streams`, `read`, `tail`, `wait_for`, `context`, `mark`.
- `internal/web/assets` (embed, `bundleBuilt`, minimal `layout.html`),
  `src/core` with tests, CI and release workflows, `scripts/remote.sh`.
- **Projects** (PLAN section 4, "Projects, sinks and grants"; DECISIONS
  2026-11-19): `internal/projects` owns projects, sinks, grants and the
  in-memory sink routes, and computes each principal's `Access`. Every
  channel and log stream is in a project; logs reach a project only
  through its sinks; nothing is visible without a grant but to the admin
  session. The admin runs projects at `/projects` and each one's
  `/projects/<p>/settings` (admin interface); `cafard token create
  --grant|--sink` and `cafard pair --grant` do it from a shell.
- **Projects first** (PLAN 9.6.3; DECISIONS 2026-11-19, "projects
  first"): home is the projects; every page of a project lives under
  `/projects/<p>` (`/c/<chat>`, `/g/...`, `/logs[/<sink>]`, `/settings`)
  and shares the side list `partials/side_nav.html` (`pages/side.go`,
  `src/components/SideNav.ts`); `/dm` lists the direct conversations and
  `/admin` is the admin's menu. The old `/c/...` and `/g/...` redirect.
- **Galleries** (PLAN section 6.3; DECISIONS 2026-11-19, "gallery
  channels"): a channel's `kind` is `chat` or `gallery`;
  `internal/messaging/gallery.go` reads items and sections, and
  `internal/web/pages/gallery.go` serves `/projects/<project>/g/<gallery>`
  and each item's page, `src/pages/GalleryPage.ts` the viewer and live tiles.
  Chat messages point at gallery items with refs (PLAN 6.4;
  `internal/messaging/refs.go`), shown as cards and picked from the
  composer (`GalleryPicker`).

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
- **Build the mux once** (`build` in `cmd/cafard/serve.go`). Registering
  the same pattern twice panics at start, not at compile time. There are
  two muxes, each built once: the public one every `Register` gets, and
  the admin one (`web.Register`'s second argument) for the admin-only
  routes, which hands every other path to the public one. A new admin-only
  route goes on the admin mux, or it is published on `http_listen`; add it
  to `adminRoutes` in `cmd/cafard/listeners_test.go`.
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
- **Network ingest (track B):** `config.Default()` binds syslog on
  `:5514` and netconsole on `:6666`. Tests that start a server use
  `testConfig()` (`cmd/cafard/ingest_network_test.go`), which turns them
  off; tests that need them bind loopback ports of their own. Listeners
  start after the HTTP server reports ready, so wait for the port
  (`waitBound`) before sending.
- **OTLP JSON ids are hex, protojson wants base64.** `traceId`/`spanId`
  are lifted out of the JSON before `protojson` sees it. Never import
  `go.opentelemetry.io/proto/otlp/collector/...`, even in a test: it puts
  gRPC in `go.mod`. The response types are encoded by hand and checked
  against `dynamicpb` copies of the reference messages.
- **Stream names from the network are untrusted.** Pass hostnames and
  service names through `ingestutil.StreamName`, or the writer refuses
  the entry (glob characters, spaces, 200 bytes).
- **`deploy/cafard.yaml.example` is tested** (`cmd/cafard/deploy_example_test.go`):
  it must load, equal the defaults, and mention every key `cafard config`
  prints. When your feature adds a config section, replace its commented
  "available once ... lands" block in the example with the real keys, or
  the test fails after the merge.
- **The installer sets the admin password, not the first `serve`**, so it
  never lands in the journal (`cafard passwd --if-unset` from a pipe; it
  parses the "Admin password set" line, so keep that wording). There is no
  admin token: `Tokens.Bootstrap` is gone, and tokens, sessions and
  pairings refuse the `admin` scope. Only an admin session has it.
- **Admin sessions are in memory** (`auth.Admin`; a restart signs the admin
  out) and their cookie `cafard_admin` counts only on requests that went
  through `auth.AdminInterface`. A test that needs the admin uses
  `webtest.Env.Admin()` (a session without the 0.3 s scrypt derivation) and
  `Do`, which serves the admin mux; `DoPublic` is the public interface
  alone. In `cmd/cafard`, `startServer` reads the printed password
  (`s.password`), `s.session()` signs in with it, and `s.admin` is a token
  of the `admin` person without the admin scope.
- **The systemd unit allows only ttyUSB, ttyACM and ttyS devices** and only
  `/var/lib/cafard` for writes. A feature that opens another device class
  or writes elsewhere must change `deploy/cafard.service`.
- **Serial (track E1):** `net.Pipe` is synchronous, so a fake Telnet peer
  must answer from another goroutine, never from inside its read loop, or
  both ends block. Telnet BINARY can be switched on in the middle of a
  read, so the RFC 2217 parser rereads the mode after every option command.
  A value-receiver method such as `serial.Port.Stream()` copies the whole
  struct, which races with a field another goroutine writes under a lock;
  `Handle` keeps immutable copies instead. A config list section must be a
  struct with its own `UnmarshalYAML` (see `serial.Config`), or `Validate`
  is never called. `cafard push` takes the spool location from the
  injected `getenv`, so tests never touch the real `~/.cache`.
- **Track A: build messaging before `mcpserver.New`.** `New` copies
  `deps.Deps`, so `d.Messaging` and `d.Blobs` must be set first or the
  log tools never see `channel=` (`cmd/cafard/serve.go`).
- **Track A: the send rate limit (burst 10) runs on the injected clock.**
  A test that sends more than 10 messages as one principal must advance
  its fake clock (2 s per message) or it gets `rate_limited`.
- **Track A: `textclean.Format` collapses identical lines,** so a test
  that wants a big log snapshot must make its lines differ.
- **Track A: an empty `hub.Filter.Streams` is every log stream; `*` is
  not** (a glob stops at `/`). Message streams match only `chan:*/*` and
  `dm:*` patterns.
- **Track A: MCP resource templates use `{+id}`,** or a stream id with
  `:` does not match the template.
- **Track A: MCP SDK servers are per principal** (not per scope set), so
  a resource notification such as "your inbox changed" reaches only that
  principal's sessions.

- **Track E2: a tool hidden by scope is a protocol error, not a tool
  error.** Calling `serial_write` with a token that lacks the scope makes
  `CallTool` return an error (unknown tool), so `callFails` cannot test
  it; check `ListTools` instead.
- **Track E2: write markers go through `Writer.Commit` before the
  bytes,** so the port's echo is ordered after them. Gates run before the
  marker; keep it that way or a refused call leaves a false trace.

- **Templates cannot use functions.** `assets_test` parses every
  template with an empty FuncMap, so a `{{myFunc}}` anywhere breaks it.
  Build strings in Go (`internal/web/pages/views.go`) and pass fields or
  methods. A page file's body is its content (`pages.New` defines
  `content` per page); do not `{{define "content"}}` in a page.
- **A new `<template>` twin is declared in Go** with `Site.AddTwin(id,
  partial, zero value)` and listed by the page's `Render` call; the
  template test (`internal/web/web_test.go`, `twinSlots` and
  `pageTwins`) checks its slots. Add your pages and twins to those maps.
- **Rendered pages are fixtures.** `go test ./internal/web -update`
  rewrites `internal/web/testdata/pages/*.html`, which the TypeScript
  renderer tests load; `go test ./internal/web/view -update` rewrites the
  ANSI vectors both parsers are checked against. Commit both.
- **TS tests load pages without HTML sinks**: `src/testing/pages.ts` parses
  the file in a separate happy-dom page (`page.content = ...`) and imports
  its nodes. Do not reach for the forbidden setters to build fixtures.
- **Message visibility in SSE is checked twice**: by the hub's viewer
  filter and by `Messaging.Visible` in the handler. Keep both; a test
  runs the hub wide open to pin the second.

- **Track D: the web tests run the real messaging service.** `newEnv`
  in `internal/web/web_test.go` starts it and mounts `RegisterMessages`;
  `seed` creates `#dut1`, a photo, a log slice, a question and a DM, and
  the fixture pages under `testdata/pages` come from that. A test env
  with a fake `deps.Messaging` gets no chat pages or chat API at all.
- **Track D: chat pages ack on render only without a bundle.** With
  `main.js` built they carry a `<noscript>` beacon instead, so tests that
  count unread branch on `assets.BundleBuilt()`; run `go test ./internal/web`
  both before and after `npm run build` when touching read state.
- **Track D: a `<dialog>` must not have the class `dialog`:** track C's
  `.dialog` is a fixed overlay that shows even when the dialog is closed.
  Use `modal`, `sheet` or `image-viewer`.
- **Track D: `.only-scripted` forces `display: inline-flex !important`**
  once scripts run, so a column menu that carries it needs
  `flex-direction: column`.
- **Track D: session posts need `Sec-Fetch-Site: same-origin`** (or no
  browser header) on `POST /api/messages` now; a Go client following the
  303 of a bearer post lands on `/signin?next=/c/...`, since chat pages
  take sessions only.
- **Track D: new chat mutations must emit.** A messaging change a page
  should see live calls `emit`/`emitChat` (`internal/messaging/events.go`),
  or the SSE `chat`/`seen`/`answered` events and the badges miss it.
- **Track D: `Services.uploads` is required.** A test that builds a
  `Services` literal needs an `UploadQueue` (`src/testing/services.ts`
  has `fakeServices`).

- **Track G: the FTS triggers call a Go function** (`cafard_fts_text`,
  registered by `internal/store` in `init`). A `sqlite3` shell or any
  program without it cannot insert into or delete from `entries`. Do not
  run `INSERT INTO entries_fts(entries_fts) VALUES('rebuild')`: it would
  index the raw text again; refill with `delete-all` and the function, as
  migration 0050 does.
- **Track G: `LogView.enqueue` paints on the next animation frame.**
  Tests pass a `FakeFrames` (`src/testing/FakeFrames.ts`) and call
  `run()`; a paused view keeps new entries out of the DOM until
  `resume()`, so a test that pauses and then counts rows sees none of
  them.
- **Track G: `cafard mcp` must not forward `_meta` keys of the protocol**
  (`io.modelcontextprotocol/...`): the remote refuses the client's
  protocol version. `ownMeta` strips them; keep it when adding a
  forwarded request.
- **`cafard mcp` arguments of its own must be removed before forwarding.**
  The server decodes tool arguments strictly (unknown fields are a
  `bad_request`), so `image_paths` is rewritten into `images` and deleted
  in the proxy (`internal/mcpproxy/images.go`); a new proxy-only argument
  needs the same, and its schema entry added in `build`.
- **Track G: a nil `*io.PipeWriter` in an `io.Writer` is not nil.** The
  term session converts it explicitly; copy that pattern.

- **Projects: a glob star stops at `/`, and every stream id has one.**
  `chan:*` matches nothing any more: every channel is `chan:*/*`, one
  project's channels `chan:lab/*` and its logs `*:lab/*`. Build ids with
  `projects.Sink.Stream(kind)` and `store.ProjectOf`, not by hand.
- **Projects: tests need grants.** `writertest.Stack.Member` and
  `webtest.Env.Principal`/`Session` grant the whole of `lab`;
  `Token`/`SessionOnly` grant nothing. In `cmd/cafard`, `s.token` grants
  its scopes on `lab`, `s.tokenOnly` does not, and `startServer` makes
  `lab` with the sinks of `testSinks` before the server starts (sink
  routes are in the server's memory, so a sink written by the test's own
  `projects.Service` is not seen). Add a sink there before a test writes
  logs to it: `POST /api/log` lands in `push:lab/<sink>` whatever kind the
  stream name has, and a name with no sink is dropped, not refused.
- **Projects: `Access` is cached for 5 s** per principal and scope set.
  A grant written by another process (a test's own service, `cafard token
  create --grant`) applies within 5 s to a principal the server has
  already served; write grants before the principal's first request.
- **Projects: ingesters need `deps.Projects`.** `ingesttest.Deps` has
  none; use `ingesttest.NewSinks(t, "lab/dut1=10.0.50.42")` and its
  `Member`. Their database lives outside `t.TempDir()`, whose name has a
  `#` for fuzz seeds, which a database path may not.
- **Projects: the serial tools are always registered** and the manager
  follows `projects.OnSinksChanged` (`Manager.Reconcile`); a sink change
  stops and starts ports synchronously inside the request that saved it.
- **Projects: the migration rewrites `entries.stream`** (`0071_projects_import`,
  Go, in the migration transaction, before the writer starts). That and
  the FTS rebuild of 0050 are the only writes to `entries` outside the
  writer; do not add others.
- **Projects: the old `serial:` list and `netconsole.names` load only to
  be imported** (once per item, recorded in `config_imports`) into the
  project `imported`. Do not read them anywhere else.

- **Galleries: an item is a message with an image** (`galleryImage` in
  `internal/messaging/gallery.go`, the first image, the text's first line
  as the title). Its section is `meta.section`, so a new field that
  writes `meta` on messages must keep it (`SendMessage` marshals the
  whole `meta` struct). The gallery fixtures come from `seedGallery`,
  which only `TestEveryPageCarriesItsTwins` and the gallery tests call,
  so the other web tests keep their chat counts.
- **Gallery refs: never put `meta.refs` in a DTO.** `BaseDTO` drops it
  and strips the ref text a card replaces; a new path that hands an
  entry's `Meta` or `Text` to a browser must go through it, or it names
  galleries to readers without a grant. The per-reader resolution is
  `refDTOs`; a ref the reader cannot see is `{state: "unavailable"}` and
  must stay that bare.

- **Project view: a page of a project puts the side list first.** Build
  it with `Site.projectSide` (or `directSide`), wrap the page in
  `<div class="project-view">` after `partials/side_nav.html`, set
  `Layout.Side` and `Layout.Kicker`, and list `tpl-chat-row` and
  `tpl-stream-dot` among its twins, or the bundle's side list cannot add
  rows. `mountShell` mounts SideNav and its ChatList on any page that has
  them: a page class must not mount a second ChatList on it.
- **Project view: links go through the builders.** `view.ChatHref`,
  `view.ProjectHref`, `view.LogsHref`, `view.SettingsHref`,
  `messaging.GalleryHref`/`ItemHref` and their twins in
  `src/render/format.ts`; the old `/c/` and `/g/` forms only redirect,
  and `GalleryRefRenderer` drops any card `href` that is not
  `/projects/<p>/g/...`. A new address form a person may paste needs
  `galleryLinkRE` too.
- **Project view: the drawer is `:target` without the bundle.** The menu
  button links to `#side` and the close links to `#main`; SideNav
  intercepts them. Do not give the side list another id, and keep
  `body.shell main:has(> .project-view)` as written: `main:has(...)`
  alone loses to `body.shell main` and the page keeps its padding.
- **Project view: the web fixtures are written with the bundle built.**
  CI runs the Go tests after `npm run build`; without `main.js` a chat
  page acks what it renders, and the pages rendered after it (the side
  lists' and home's badges) differ from the committed ones. `seed` makes
  the sinks `lab/dut1` and `lab/bench-pi`, which the side lists and the
  logs pages show.
- **Project view: the admin's `GET /projects/<p>` is gone;** it is the
  public project view. The settings page is `/projects/<p>/settings` on
  the admin mux (in `adminRoutes`), and its form posts kept their
  addresses.

## Rules that are easy to break

- **No `innerHTML` and its relatives in `src/`.** Log lines and messages come
  from anyone on the LAN. CI greps for these sinks.
- **No frontend framework and no CSS framework.** Plain DOM in TypeScript
  classes, as in grafit. `PLAN.md` section 9.4 lists the allowed patterns and
  the problem each one solves; don't add others.
- **The Chats pages, the Sources (`/streams`) page and a project's logs
  (`/projects/<p>/logs`) must work without the bundle,** the side list
  and its drawer included. A test pins this.
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
