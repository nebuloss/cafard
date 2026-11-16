# Decisions

Deviations from `PLAN.md` and answers to its open questions, newest first.
Each entry gives the date, the decision and the reason.

## 2026-11-16: track E2 serial control

- **`serial_write` and `serial_control` are added in `serial.Register`**
  (scope `serial_write`), only when at least one port is configured: with
  no port they could only fail. No shared file changed for them.
- **The claim gate asserts `deps.Messaging` to `serial.ClaimChecker`**
  (`Holds`), and to `mcpserver.ClaimSource` (`ActiveClaims`) to name the
  holder; serial never imports messaging and `internal/deps` is unchanged.
  Without messaging every write is refused.
- **Gate order and codes:** unknown stream `not_found` (lists the
  ports), not `writable` `forbidden` (names the config key), claim held by
  someone else `conflict` (names the holder and the expiry), no claim at
  all `forbidden` ("claim it first"). The scope is checked by `AddTool`
  and hides the tools. All three gates apply to every call, even an empty
  `serial_control` that only reads the line state, as PLAN 7 says "same
  three gates". Then: port down `busy` with `retry_after_s` 5; raw `tcp`
  or plain Telnet control `bad_request` with E1's reason; invalid
  arguments `bad_request`; a port that fails mid-way `busy`.
- **The marker is written before the bytes or the control action,**
  with `Writer.Commit`, so the board's echo and answer always come after
  it in seq order; the tools return its seq (`marker_seq`) as a cursor. If
  the action then fails, a second marker says so (`serial: write by
  agent-a failed: ...`, severity 4). Refusals (gates, unsupported
  transport, down port, bad arguments) are checked before the marker and
  leave none. Markers carry `sender` = the principal and
  `meta.serial` = `write` | `control` (`write_failed`, `control_failed`).
- **Marker texts:** a write is `<principal>> <data>` with the newline
  included, `\r \n \t \\` and other control bytes, invalid UTF-8,
  C1 and bidi controls escaped, cut after about 300 bytes with
  `... (N bytes in all)`. A control action is `serial: <principal>
  set 57600 baud, set DTR off, set RTS on, sent a 250 ms break, pulsed
  DTR for 100 ms` (the steps asked, in the order they run).
- **One `serial_write` sends at most 16 KiB**, newline included
  (`too_large` beyond). `newline` is a pointer so `""` (no newline) and
  absent (`\r`) differ. `pulse.line` is case-insensitive.
- **No test hook in the serial package:** the end-to-end test configures
  real ports against loopback TCP fakes (an RFC 2217 peer and raw TCP
  peers), so the server's own `Open` runs.

## 2026-11-16: merging tracks A, B, C, E1, F

- **Other tracks add DTO fixtures from their own test file** by appending
  to `trackFixtures` in an `init` (see `internal/dto/fixtures_c_test.go`),
  so `fixtures_test.go` is not edited per track and its stale-file check
  still knows every fixture. Request bodies (`*_create`) are skipped by
  the TypeScript parse test: the client sends them, it never parses them.
- **`deps.Messaging.ResolveChannel` returns binding patterns,** as track A
  decided; the comment in `internal/deps` now says so.

## 2026-11-16: track C web foundation

- **Sessions and pairings** are migration `0020_c_sessions.sql`. Sessions
  are hashed like tokens (`cfs_` prefix, so a pasted cookie is not taken
  for a token), copy the scopes of the token or pairing that made them,
  and last 30 days, renewed when used with less than 15 days left. The
  cookie itself is HttpOnly, SameSite=Lax, `Secure` with `secure`, and
  kept 400 days; the server decides when a session ends. `pairings` has
  an `id` (for `/pair/{id}/qr.svg`) and a `created_by` beside the
  columns of PLAN 4.
- **The clear code of a pending pairing is kept in memory** for its 5
  minutes, so `/pair` can draw the QR again on reload; only its hash is
  stored. After a restart the page says so and the admin makes a new one.
  Redemption is one `UPDATE ... RETURNING`, so two phones scanning the same
  code cannot both win.
- **Signing in needs a token of a human principal** (the admin token is
  one); an agent or device token is refused with a sentence saying why.
- **`web.Register` builds its own `auth.Sessions`** (it is stateless but
  for a last-seen throttle), so the Register signature stays the
  contract's. `serve.go` builds another for `TokenAuthenticator.Sessions`
  and for the exposure guard counter (`Sessions.CountAdmin`, live admin
  sessions only).
- **`assets.Layout` gained the shell's fields** (theme, active nav item,
  user, badges, rendered twins, notice, error, back, path), and
  `assets.Register` serves `/manifest.webmanifest`, `/sw.js` and
  `/icons.svg` from `static/` when the file exists (CONTRACTS 2.2). Both
  are track 0's file; the shell is track C's, and needs them.
- **No template functions.** `assets_test` parses every template with an
  empty FuncMap, so templates only use fields and methods; wording and
  formatting are built in Go (`pages/views.go`). Each page file's body is
  its content: `pages.New` clones the set per page and defines `content`
  as that file. Twins are declared with `Site.AddTwin(id, partial, zero)`;
  track D declares its own the same way.
- **ANSI colours are rendered on the server too** (`view.ANSI`), into
  the same fixed classes as `render/AnsiParser.ts`, so no-JS and live
  lines look alike. Both are checked against
  `internal/web/testdata/ansi/vectors.json` (`go test ./internal/web/view
  -update`). Stream tag tones are FNV-1a mod 8 on both sides.
- **Log times are shown in UTC** with milliseconds, on the server and in
  the bundle, as agents see them; the Sources page says so.
- **Palette:** no Material tool runs offline, so `scripts/palette.py`
  ports the needed part of material-color-utilities (CAM16, HCT by tone,
  tonal spot, 2023 role tones). Its error role is M3's `#ba1a1a`. It also
  picks the ANSI and stream tag colours by contrast and writes the ratios.
- **Messaging extension points for track A** (optional interfaces, checked
  with a type assertion on `deps.Messaging`): `view.Counter` fills the chat
  badges of `counts`; `view.Presenter` turns a message into its full DTO
  for a viewer. Without them SSE sends `sources_down` only, and a message
  as one text segment with `chat = ChatOf(e)`. SSE checks
  `Messaging.Visible` on every message itself, whatever the hub lets
  through, and sends no message when `Messaging` is nil.
- **SSE details:** `scope=home` carries every message visible to the
  viewer until follows exist; `chat=` needs messaging (404 before);
  `since_seq` wins over `Last-Event-ID`; a replay past 5000 entries, or a
  lagged subscriber, gets `reset` and the stream ends. The bundle's
  `LiveFeed` replaces EventSource's own retry with a backoff (1 s to 30 s)
  and resumes with `since_seq`, since EventSource cannot set
  `Last-Event-ID` on a new connection.
- **New request DTOs without fixtures** (`dto.TokenCreate`,
  `PairingCreate`, `MarkCreate`, `MarkCreated` in `internal/dto/web_c.go`):
  the fixture test's stale-file check only knows `fixtures()`, so a
  second fixture list in a new test file would be reported stale.
  A one-line hook in `fixtures_test.go` would fix it; that file is track
  0's. The pairing fixture's `url`/`qr_url` are examples; real values are
  `<base>/p/<code>` and `/pair/<id>/qr.svg`.
- **`POST /api/marks` answers 201** with `{seq}`.
- **Pages carry a strict CSP** (`default-src 'self'`, no inline script or
  style) and `Cache-Control: no-store`.
- **New dependency:** `rsc.io/qr` v0.2.0, named in PLAN 2, pure Go.

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

## 2026-11-16: track E1 serial ingest and push

- **The `serial:` section is `serial.Config`, a struct holding `Ports
  []Port`, not a bare `[]serial.Port`.** The config walk only calls
  `SetDefaults` and `Validate` on struct fields, so a bare list would never
  be checked by `cafard config`. The struct reads and writes the plain YAML
  list (`UnmarshalYAML`/`MarshalYAML`); since a custom unmarshaler loses
  the decoder's unknown-key check, it checks the keys of each port itself.
- **Port defaults are 115200 8N1** for `device` and `rfc2217`. Raw `tcp`
  ports refuse `baud`, `data_bits`, `parity` and `stop_bits` rather than
  ignore them, so nobody believes they were applied. Names match
  `^[a-z0-9][a-z0-9._-]{0,62}$`.
- **Markers:** `serial: connected: <how>` on the first open too (its text
  says RFC 2217 with the framing, raw TCP, or plain Telnet and why), one
  `cannot open` marker per streak of failed opens, `disconnected from
  <addr>: <reason>` and `reconnected after <gap>: <how>`. Severity 4 for
  failures and drops, 6 for connects; `meta` is `{"serial": "<event>"}`.
  The backoff (1 s doubling to 30 s) resets only after a connection that
  lasted longer than 30 s, so a flapping adapter does not write a marker
  pair every second.
- **RFC 2217 details:** no answer to COM-PORT-OPTION within 5 s counts as
  a refusal (plain Telnet). Line settings are sent without waiting;
  SET-CONTROL and SET-BAUDRATE wait up to 2 s for the server's answer and
  a silent server is assumed to have obeyed; a different answer is an
  error. The modem and line state masks are left at the server's default.
  If the peer refuses BINARY, NVT rules apply (CR NUL). Carrier detect
  markers come from NOTIFY-MODEMSTATE only: local ports read CTS and DCD
  when a control request runs, they are not polled.
- **Line control API for track E2** (`serial.Manager.Lookup(stream)` then
  `Handle.Write` / `Handle.Control`): steps run in the order baud, dtr,
  rts, break, pulse; a pulse flips the line from its current state (taken
  as asserted when unknown, since ports open with DTR and RTS up) and puts
  it back, even if the context ends; `break_ms` and `pulse.ms` are at most
  10 s; a baud change is kept across reconnects until restart. The
  package enforces the config gate (`writable`) itself; scope and claim
  are E2's. An empty request only reads the line state. Errors wrap
  `ErrUnknownStream`, `ErrNotWritable`, `ErrDisconnected`, `ErrBadRequest`
  or `ErrUnsupported` (`*UnsupportedError` with the reason: raw TCP, or a
  plain Telnet peer). `Register` adds no tools; E2 adds them in
  `serial.Register`, where the Manager is built.
- **`cafard push` spool:** one file per server and stream under the user
  cache directory (`~/.cache/cafard/spool`, `%LocalAppData%\cafard\spool`,
  `~/Library/Caches/cafard/spool`), NDJSON as sent, the delivered offset in
  a `.pos` file beside it. It is created only when a line needs it and
  deleted once drained, so a healthy push leaves nothing behind. Over the
  100 MiB cap the newest lines are dropped and a line saying how many goes
  into the stream. While the spool holds anything, new lines go behind it,
  which keeps the order. A replayed batch is at most 1000 lines or 4 MiB.
  `--spool PATH` and `--spool off`; with no cache directory push warns and
  keeps lines in memory as before.
- **`push --serial` must open the port at start** (a mistyped COM5 fails at
  once) and reopens it with backoff after that. `--follow` starts at the
  end of the file like `tail -f`; the new `--from-start` sends what it
  already holds. Ctrl-C ends a serial or follow push with status 0.
  Disconnects of a pushed port are logged on stderr only: `/api/log`
  takes lines, not markers.
- **New dependency `go.bug.st/serial` v1.8.0** (named in PLAN section 2),
  pure Go; the five `CGO_ENABLED=0` cross-builds pass with it.

## 2026-11-16: track B network ingest (M4)

- **Three config sections and three `Register` lines** (`syslog`,
  `netconsole`, `otlp`), one per package of CONTRACTS.md 1.1, instead of
  the one-field / one-line rule of 7.3, which assumes one package per
  track. Each type has its own `SetDefaults` and `Validate`.
- **Shared helpers in `internal/ingest/ingestutil`** (stream names from
  untrusted text, capped meta, allowlists, declare-once) and fakes in
  `internal/ingest/ingestutil/ingesttest`. Both are track B packages.
- **Dependencies:** `go.opentelemetry.io/proto/otlp` v1.11.1 (only
  `logs/v1`, `common/v1`, `resource/v1` are imported) and
  `google.golang.org/protobuf` v1.36.12 (`proto`, `protojson`,
  `protowire`; tests also use `dynamicpb`, `protodesc`, `descriptorpb`).
  **`go mod graph` does list `google.golang.org/grpc` and grpc-gateway,
  as requirements of the otlp module's own go.mod** (the module also holds
  the collector packages). Nothing pulls them into the build: they are not
  in our `go.mod` or `go.sum`, and `go list -deps -test ./...` has no
  grpc, genproto or collector package. Every otlp version has those
  requirements, so this is the best available.
- **Reference types for the response test** are `dynamicpb` messages
  built from descriptors copied from `logs_service.proto` and
  `google/rpc/status.proto` (without `details`): the generated collector
  package cannot be imported, even in a test, without putting gRPC in
  `go.mod`. Protobuf bytes are compared exactly; JSON is compared as
  values, because `protojson` output is deliberately unstable.
- **OTLP errors are `google.rpc.Status` bodies** in the request's
  encoding (as the OTLP/HTTP spec says), with gRPC codes 3, 8 or 14; 415
  for an unknown Content-Type or Content-Encoding (JSON body then). The
  401/403 from `auth.Require` keep the cafard error format.
- **OTLP queues with `Submit` instead of `CommitBatch`:** a lossy submit
  first, then, if the queue is full, a blocking one bounded by 2 s per
  entry. If nothing could be queued the answer is 503 with
  `Retry-After: 5`; if the wait ran out part-way, the request succeeds
  with `partial_success` counting the records not stored (a 503 then
  would make the client resend records already stored). Records the
  writer refuses also count as rejected. The response comes before the
  commit, which the writer does within 50 ms and flushes on shutdown.
- **OTLP JSON bodies may hold several requests in a row** (Vector's
  legacy `json` codec sends newline-delimited objects); their resources
  are concatenated. Invalid or all-zero hex ids are dropped, not refused.
- **OTLP bodies become one entry per line**, as everywhere else; every
  line of a record shares its severity, `src_ts` and meta. A record with
  no body uses `event_name` as text.
- **Meta is capped at 4 KiB by dropping, not cutting:** scalar fields
  first, then object fields (`sd`, `attributes`, `resource`) keep the keys
  that fit, sorted, and `"truncated": true` marks the loss.
- **Syslog TCP streams are down when their last connection closes**
  (counted per stream across connections). `logger -T` opens one
  connection per run, so its stream shows down between runs. UDP streams
  are never marked connected or down.
- **RFC 3164 timestamps have no zone or year:** read in the server's
  local zone, current year, or last year when that lands more than two
  days in the future. RFC 3339 stamps (rsyslog high precision) and a
  trailing year or fraction are also read. A hostname is only taken from
  a 3164 message that has a timestamp.
- **RFC 6587:** octet counting is chosen when the first non-blank byte
  of a connection is 1-9; a frame that stops making sense switches the
  connection to newlines for good. Frames over 256 KiB keep their first
  256 KiB. Newline framing goes through `textclean.Splitter` (500 ms
  partial-line flush). At most 512 TCP connections at once.
- **Netconsole:** plain datagrams go through one `Splitter` per source IP,
  so `pr_cont` pieces join into one line and a prompt is flushed after
  500 ms. Extended messages split by the kernel (`ncfrag=`) are
  reassembled, or stored as received after 2 s. The boot-relative
  timestamp goes to `meta.ts_usec`, not `src_ts`, which has no absolute
  time to give. Severity is the low 3 bits of the level field.
- **`syslog.allow` applies to syslog only,** as in PLAN.md 11; refused UDP
  datagrams are ignored and refused TCP peers disconnected at once.
- **Shared test file edits:** `startServer` in `cmd/cafard/e2e_test.go`
  takes optional config tweaks and starts from `testConfig()`, and
  `TestServeAnswersHealthzAndShutsDownGracefully` uses it too, so no test
  binds the default ports 5514 and 6666 on a shared machine.

## 2026-11-16: track F deployment

- **The installer, not the service, does the first start.** `cafard serve`
  prints the admin token on stdout on an empty database, and under systemd
  or OpenRC stdout is the journal or `/var/log/cafard.log`: the token would
  be kept in a log. So `install.sh`, when the data directory has no
  `cafard.db`, runs `cafard token create --name admin --kind human --scopes
  ingest,read,message,admin --label "first start"` (what `Tokens.Bootstrap`
  does), prints the token to the terminal only, and chowns the data
  directory to `cafard`. The server then finds a principal and prints
  nothing. If `Bootstrap`'s name or scopes change, change the installer too.
- **The installer sets `public_url`** in a NEW `/etc/cafard.yaml` to
  `http://<first address>:8790` (or `CAFARD_INSTALL_PUBLIC_URL`), because
  pairing QR codes pointing at `localhost` are useless to a phone. An
  existing file is never edited.
- **Installer variables are `CAFARD_INSTALL_*`** (`VERSION`, `REPO`,
  `PUBLIC_URL`), not `CAFARD_*`, which cafard itself reads as config keys.
- **Paths:** binary `/usr/local/bin/cafard` (as grafit), config
  `/etc/cafard.yaml`, data `/var/lib/cafard`, OpenRC log
  `/var/log/cafard.log`.
- **SHA256SUMS fails closed in every case:** missing file, asset not
  listed, mismatch, and also no `sha256sum` on the machine (grafit only
  warned on the last).
- **The container drop-in is installed on exit status 226/NAMESPACE, and
  also 219/CGROUP** (what a refused device cgroup policy gives). Any other
  failure stops the installer with the journal, so a bad configuration is
  not mistaken for a container. The drop-in also resets `DevicePolicy` to
  `auto`: in an LXC container the host's device list (`dev0:`) is the
  allowlist.
- **No systemd test in containers:** dev-build has neither docker nor
  podman. `install.sh` was tested from a checkout in chroots of the
  Debian trixie and Alpine 3.24 LXC images (namespaced, no network), and
  the hardened unit on dev-build's own systemd; see the track report.
- **CI checks the unit with `systemd-analyze verify`** and the OpenRC script
  with shellcheck. The release smoke test now also starts the binary and
  checks `/healthz` reports exactly the tag, that `/app/main.js` is served
  (the bundle is embedded), and that the example config loads.
- **`scripts/devdeploy.sh` builds in its own tree on dev-build**
  (`REMOTE_DIR=cafard-deploy`), keeping its `node_modules` there, and takes
  `GOARCH` for arm appliances.

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
