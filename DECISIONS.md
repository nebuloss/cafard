# Decisions

Deviations from `PLAN.md` and answers to its open questions, newest first.
Each entry gives the date, the decision and the reason.

## 2026-11-16: track A messaging, channels, claims, images

- **`ResolveChannel` returns the channel's binding patterns** (ids and
  globs), not the ids they match now. The log tools and the hub match
  patterns themselves, so a stream that appears during a `wait_for`
  (a glob binding) is still seen. `Service.BoundStreams` gives the
  matched ids; log attachments in channel form store those. CONTRACTS
  1.9 comment updated; the comment in `internal/deps` (track 0) still
  says "match now".
- **`follows` has a `followed` column** (migration 0010). A row with
  `followed = 0` is an explicit unfollow, so humans can follow every
  channel by default without a row per channel, and pin/mute survive an
  unfollow. Agents auto-follow (create, send, `channel=` read) only when
  no row exists, so an explicit unfollow sticks.
- **Three config sections** (`messaging:`, `images:`, `channels:`), as the
  task asked. `channels:` is `messaging.Channels`, a struct around the
  list with its own YAML (un)marshalling, because the loader only calls
  `Validate` on structs; unknown keys inside it are still errors.
- **One Register line, `api.RegisterMessages(mux, mcp, d, msg)`,** in
  `internal/web/api` (allowed to import messaging). It adds the MCP tools
  and resources, the janitor tasks and the routes, and returns the
  service's `Start` (auto-archive every hour, resource notifications).
  `messaging.New` runs before `mcpserver.New`, makes sure `general`
  exists (source `config`, so it is never renamed or deleted) and
  reapplies config channels; it writes no entry, as the writer is not
  running yet. A config channel dropped from the file becomes a runtime
  one.
- **`internal/mcpserver` extensions,** none changing an existing tool:
  a tool output implementing `ExtraContent` adds content after the text
  (the images of `get_message`); the principal is also put in the
  handler's context (`auth.WithPrincipal`); `ClaimSource` lets
  `list_streams` show claim holders; `AddResource`, `AddResourceTemplate`,
  `SetSubscriptions` and `ResourceUpdated` add resources. **SDK servers
  are now per principal** (key: name + scopes) instead of per scope set:
  `ResourceUpdated` on a shared server would tell every principal's
  sessions that "the inbox" changed, which leaks when someone else gets a
  direct message.
- **Subscriptions** work on `cafard://inbox` and `cafard://stream/{id}`,
  throttled to one notification per 2 s per URI and principal, with a
  trailing one. Templates use `{+id}` so ids with `:` match.
- **Renaming a channel moves its messages** to `chan:<new>` (an
  `UPDATE entries` run through `writer.W.Exec`, like deletes), and moves
  follows. Deleting runs through the writer too. Rename markers are
  written like binding markers.
- **Log attachments:** "expired" means every line of the reference is
  gone (no entry of its streams left in its seq range up to its send-time
  `last_ts`); partial loss is not flagged. A snapshot is read back by
  parsing the `Format` text into entries, so collapsed repeats come back
  as one line. If the blob size limit removed a snapshot, reads fall back
  to the stream.
- **Questions** carry `meta.choices` (an empty array for a free-text
  `ask_human`); that key is what makes a message a question. A reply's
  `choice` must be one of them; a choice-only reply gets the choice as its
  text.
- **`wait_message` acks what it returns** (`ack: false` to keep it), or
  a second call would return the same message forever. It returns stored
  unacked matches first, then waits.
- **Claims:** default TTL 15 min, max 24 h; claiming again renews (and
  writes a "renewed" marker). An admin may release someone else's claim.
  Expiry is found by the janitor or lazily by the next claim/release on
  that resource, whichever comes first; `DELETE ... RETURNING` makes sure
  only one writes the "expired" marker.
- **Blobs:** any unreferenced blob is kept for 1 hour (uploads in flight,
  staged shares), then deleted; the size limit deletes the oldest
  originals with their variants. Uploaded files are always sniffed and
  must be png, jpeg, webp, gif or heic. `/blob` also sends
  `Content-Security-Policy: default-src 'none'; sandbox`. Decoding runs
  in a pool of `NumCPU/2` workers (1 to 4).
- **Hooks for later tracks:** `Service.SetStagedLookup` (the share
  target's `staged` ids, track D), `Service.OnAck` (SSE `seen`, track C),
  `Service.Holds` (`serial_write`, track E2), `Service.EntryDTO`,
  `ChannelChat`, `DirectChat`, `Timeline`/`TimelineDTO`, `SetPrefs`,
  `AckUpTo` for the chat JSON API.
- **`POST /api/messages`** is bearer-only for now; the handler reads its
  principal from the context, answers JSON with `X-Cafard: 1` or
  `Accept: application/json`, else 303 to the chat (or back with
  `?error=<code>`). It also takes a urlencoded form. Files are streamed
  part by part into the blob store, not buffered by `ParseMultipartForm`.
- **New dependency:** `golang.org/x/image` v0.46.0 (scaling, webp), pure
  Go, named in the task.

## 2026-11-16: M1 store, ingest and core MCP

- **`Deps` carries root settings (`deps.Server`), not `*config.Config`,
  and a feature's config section is a `Register` argument.**
  `internal/config` imports each feature for its section type, so a
  feature importing `internal/config` through `Deps` was an import cycle.
  CONTRACTS.md 1.2 updated. `Deps` also gained `Tokens`, `Janitor` and
  `Version`.
- **Feature defaults:** a section type with `SetDefaults()` gets it called
  by `config.Default()`, as `Validate()` is called after loading.
- **Migrations are recorded by file name** in `schema_migrations(name,
  applied_at)`, replacing the single `schema_version` number, because
  numbers are reserved per track and may land out of order.
- **The janitor deletes entries through the writer** (`writer.W.Exec`, a
  function run in the writer goroutine between batches, in its own
  transaction), in chunks of 5000 rows. "Only the writer writes entries"
  covers deletes too. Size retention counts text bytes plus 64 per row.
- **`store.Query.MinSeverity` is a `*int`** (nil keeps everything): 0 is a
  real severity, so a zero-value default would have meant "emergencies
  only". `store.Entry.Severity` has the same trap; producers set
  `store.DefaultSeverity` (6) explicitly. `hub.Filter.MinSeverity` keeps
  the contract's `int` with 7 for "all".
- **Stream globs never match message streams** unless the pattern itself
  starts with `chan:` or `dm:`, so `streams=["*"]` cannot leak a direct
  message. Log tools also drop `chan:*`/`dm:*` patterns outright.
- **`wait_for` takes "now" before subscribing,** then subscribes, then
  reads the store, then drains; an entry committed at any point after
  "now" is seen exactly once. A test commits entries at random moments
  around the subscription to pin it.
- **MCP tools are registered through `mcpserver.AddTool`,** which infers
  the schemas with `github.com/google/jsonschema-go` (already required by
  the SDK, now a direct dependency) and returns errors as `isError`
  results whose structured content is `{code, message}`. One SDK server is
  built per scope set, so `tools/list` hides what a token cannot call.
  Sessions are bound to the principal (`TokenInfo.UserID`), so a token
  cannot take over another principal's session.
- **`/mcp` accepts a token only if it has `read`.** Every MCP tool needs
  at least `read`, so an ingest-only device token gets 401 there.
- **The SDK's auth middleware answers 401 on `/mcp`** in its own format
  (with `WWW-Authenticate`), not the cafard error DTO: MCP clients expect
  it.
- **New dependencies:** `github.com/modelcontextprotocol/go-sdk` v1.8.0
  (brings `golang.org/x/oauth2`, `golang.org/x/time`,
  `github.com/segmentio/encoding`, all pure Go), `modernc.org/sqlite`,
  `golang.org/x/sync` (errgroup). The cross-builds stay `CGO_ENABLED=0`.
- **First start prints the admin token to stdout,** never to the log. The
  admin token gets `ingest read message admin`; scopes are independent, so
  admin does not imply `serial_write`.
- **`POST /api/log` also accepts `Content-Encoding: gzip`;** `cafard push`
  sends gzip. Lines get the request's receive time in `ts`, and NDJSON
  `ts` becomes `src_ts`. Body limit 16 MiB after decompression.
- **`cafard push` retries forever with backoff (1 s to 30 s, or the
  server's `Retry-After`)** while the server is unreachable or busy, and
  stops at once on a refusal (4xx), printing the server's sentence. The
  disk spool comes with M5.
- **`token` subcommands:** `cafard token create|list|revoke`, opening the
  database directly; SQLite WAL lets it run beside a live server.

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
