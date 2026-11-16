# Decisions

Deviations from `PLAN.md` and answers to its open questions, newest first.
Each entry gives the date, the decision and the reason.

## 2026-11-16: M0 skeleton

- **Templates and static files live under `internal/web/assets/`**
  (`templates/`, `static/`, `static/app/`). `go:embed` cannot reach a parent
  directory, and CONTRACTS.md 1.1 gives the embed to package
  `internal/web/assets`; CONTRACTS.md 7.3 already said
  `internal/web/assets/static/app.css`. PLAN.md and AGENTS.md paths fixed.
- **The bundle is served at `/app/main.js`** (CONTRACTS.md 2.2), with a
  content ETag and `Cache-Control: no-cache`. `/static/app/` is not served,
  so the bundle has one URL.
- **Every scalar config key gets a flag and an environment variable for
  free**, derived from its yaml path: `otlp.max_body_mib` is
  `-otlp-max-body-mib` and `CAFARD_OTLP_MAX_BODY_MIB`. Lists and maps get
  neither, which is the file-only rule of PLAN.md 11. A feature adds its
  sub-struct to `config.Config` and nothing else; a `Validate() error`
  method on it is called after loading.
- **Unknown keys in `cafard.yaml` are an error,** so a typo fails at
  `cafard config` rather than being silently ignored. Fields tagged
  `secret:"true"` print as `<redacted>`.
- **New root key `log_level`** (`debug | info | warn | error`, default
  `info`), for cafard's own slog output.
- **The HTTP server has no read or write timeout,** only a 10 s header
  timeout and a 2 min idle timeout: SSE and `wait_for` stay open for
  minutes, and phone uploads are slow. Request contexts end on shutdown.
- **The exposure guard lands with tokens in M1;** in M0 the server only
  serves `/healthz` and static files.
- **`golang.org/x/sync/errgroup` (named in CONTRACTS.md 1.2) is added in
  M1,** when there is more than one background task to run.
- **TypeScript test fakes live in `src/testing/`** (`FakeClock`). vitest
  runs every test under happy-dom. The `Clock` interface is `now()` plus
  `setTimeout()` returning a `Disposable`.
- **`scripts/check-sinks.sh`** is the sink grep, run in CI and by hand. It
  also forbids `createContextualFragment`, `setHTMLUnsafe`,
  `parseHTMLUnsafe` and `DOMParser`. `h()` refuses `on*` attributes,
  `style`, `srcdoc`, and URLs that are neither same-origin paths nor
  http(s).
- **Release binaries report the tag without its `v`** (`cafard 1.2.3`), as
  grafit's do.
- **`go test -race` needs cgo for the test binary,** so it runs on dev-build
  and in CI, which have a C compiler. Shipped binaries stay
  `CGO_ENABLED=0`.

## 2026-11-16: review fixes

- **`CONTRACTS.md` wins on contracts.** Routes, DTOs, SSE events, Go
  interfaces, templates and MCP scopes are written once there, so agents
  working in parallel agree; PLAN.md points to it.
- **One stream per direct conversation,** `dm:<a>~<b>` (names sorted).
  Visibility is per stream, and a DM is visible only to its two parties,
  admin included.
- **The writer owns `entries` only.** Other tables are written in short
  transactions of their own; their markers still go through the writer,
  so `seq` stays commit order without funnelling every update.
- **Claims move to M2,** so channels and markers are complete before the
  UI; `serial_write` and `serial_control` come with serial in M5.
- **`happy-dom` is the second frontend dev dependency,** as the vitest
  environment for renderer tests against the Go fixture page.
- **Stream status is `connected | idle | down`.** Only serial ports and
  TCP peers can be down; UDP, HTTP and OTLP sources are only idle.
- Logs views are called the "logs pane" everywhere (`--pane-logs`).
## 2026-11-16: log attachments

- **A message can attach slices of streams** (stream or channel, seq range or
  open-ended, optional grep and severity). They are references, and closed
  ranges within 2000 lines and 256 KiB are also snapshotted as text blobs,
  because log retention (14 days) is shorter than message retention (90
  days). Humans attach by selecting lines in a logs view. Agents attach
  through `send(logs=...)` and read with `get_message` and
  `get_log_attachment`.

## 2026-11-16: home is the chat list

- `/` is the chat list with a "Needs you" section on top: open questions
  you can answer in place, and unread direct messages. On a desktop the list
  sits beside an overview pane, and no chat opens automatically. A
  quick-drop button and the Android share target send a photo to any chat
  without opening it.

## 2026-11-16: chats as workspaces

- **Channels can be bound to streams** (ids or globs). The user works per
  device or per task. A channel then groups the conversation and the logs:
  - The web UI shows the bound logs beside the chat, in a pane on large
    screens or behind a Chat/Logs toggle on a phone.
  - Markers, and optionally error lines, appear as event rows in the chat.
  - Agents pass `channel=` instead of listing streams.
- **The web UI is organised by chats.** The main page is "Chats" (`/`,
  `/c/{name}`, `/dm/{principal}`), and Live stays the global view across
  all sources.
- **The channel timeline is a union computed on read, never copies,** so
  changing a channel's bindings also changes its history view.
- **Follows decide which broadcasts reach an inbox,** so agents are not
  flooded by every channel. Following is automatic on create, send and
  read, and humans follow all channels by default. It is not access
  control.

## 2026-11-16: web UI layout

- **Material 3 look in plain CSS, copied from grafit's stylesheet,** with a
  palette generated from a cafard seed (chestnut `#8b5a2b`). The window size
  classes are compact, medium, expanded and large: bottom navigation bar
  below 600 px, navigation rail above, two panes from 840 px, a logs pane
  from 1200 px.
- **Markup is defined once, in Go templates.** The bundle clones
  server-rendered `<template>` twins of each partial and fills `data-slot`
  parts. The no-JS page and the live page cannot drift apart, and there is
  no HTML parsing in the browser.
- **Message text is parsed into safe segments on the server** (text, code,
  link). There is no Markdown parser on either side.

## 2026-11-16: standard protocols

- **OTLP/HTTP logs ingest** (`POST /v1/logs`, protobuf and JSON). The
  standard way log pipelines export, so Vector, Fluent Bit, the OTel
  Collector and OTel SDKs work unchanged. `POST /api/log` stays for `curl`.
  Only message types from `go.opentelemetry.io/proto/otlp` are used; the
  collector service package is avoided because it pulls in gRPC.
- **RFC 2217 serial client.** Raw TCP leaves baud rate and line control to
  the far end; RFC 2217 lets cafard set them and toggle DTR/RTS/break, which
  is how boards get reset remotely. Raw TCP stays as a fallback. New MCP tool
  `serial_control`, gated like `serial_write`.
- **Static bearer tokens now, OAuth 2.1 later (M8, optional).** The MCP spec
  defines OAuth 2.1 for HTTP, but the target clients accept a fixed header,
  which suits a lab network. Auth sits behind an interface so OAuth can be
  added without touching handlers.
- **No A2A at the core.** A2A expects every agent to be a callable server;
  terminal agents are MCP clients and cannot be called. A shared mailbox
  over MCP is the only design they can use. An A2A gateway stays a possible
  extension.

## 2026-11-16: decided with the user while planning

- **Agents can message each other and the human.** The user asked for it.
  Messages are entries in the same timeline as the logs. Read state is kept
  per reader (acks). Questions to the human use `ask_human`.
- **Go for the server, TypeScript classes for the frontend.** The frontend
  is plain DOM, with no framework, as in grafit. The user asked for an
  object-oriented design and good use of design patterns; PLAN section 9.4
  lists the allowed patterns.
- **Deployment follows the other Go projects in `~/misc`:**
  - grafit: CI, release, installer, hardened systemd unit, `remote.sh`;
  - piCVert: OpenRC, the LXC container drop-in, `devdeploy.sh`, YAML config.
- **Module path is `github.com/nebuloss/cafard`,** matching the GitHub
  remote the other projects use. grafit's module path does not match its
  remote; that mismatch is not copied.
- **The Vite bundle is not committed,** as in grafit; piCVert commits its
  bundle. A committed `README.txt` placeholder keeps the embed working on a
  clean checkout.
