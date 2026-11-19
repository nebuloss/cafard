# cafard: contracts between parts

`PLAN.md` says what cafard does. This file pins the **interfaces between its
parts**, so several agents can build them at the same time without guessing
at each other's work:

1. Go interfaces and which package owns what.
2. Every HTTP route, with its method, auth and scope.
3. The JSON API: DTOs and the error format.
4. SSE events.
5. MCP tool scopes and errors.
6. Templates: page ids, partials, `tpl-*` ids and slots.
7. The split of work into parallel tracks.

**Rules for this file:**

- When `PLAN.md` and this file disagree on a contract, this file wins. Fix
  the other one in the same change.
- Changing a contract is a change to this file first, with a line in
  `DECISIONS.md`.
- Additions that break nothing (a new optional field, a new route) can be
  made by the track that needs them. Renaming or removing anything needs the
  user's agreement.
- JSON uses `snake_case`, the same spelling as the MCP arguments.
- Timestamps are RFC 3339 strings with nanoseconds, in UTC.
- Sequence numbers are JSON numbers (int64, safe below 2^53).

---

## 1. Packages and Go interfaces

### 1.1 Ownership

| Package | Owns | Track (section 7) |
|---|---|---|
| `cmd/cafard` | subcommands, wiring; one `Register` call per feature | 0 |
| `internal/config` | the root config struct; each feature adds **one** field holding its own sub-struct, defined in its own package | 0 |
| `internal/deps` | the `Deps` struct below (breaks import cycles) | 0 |
| `internal/dto` | **every JSON type in section 3**, shared by the web API, SSE, MCP structured output and the fixture generator | 0, append-only after |
| `internal/store` | the SQLite handle, migrations runner, entry queries (read by seq, ranges, FTS) | 0 |
| `internal/writer` | the single entry writer, seq assignment, publishing to the hub | 0 |
| `internal/hub` | in-memory pub/sub of committed entries | 0 |
| `internal/status` | in-memory source status (connected, rates, drops, line state) | 0 |
| `internal/textclean` | line splitting, ANSI stripping, dedupe, truncation, the agent output format | 0 |
| `internal/auth` | tokens, sessions, pairings, scopes, the `Authenticator`, CSRF check, exposure guard | 0 (tokens), C (sessions, pairing) |
| `internal/projects` | projects, sinks (source addresses, serial ports), grants, the per-principal `Access`, sink routing, the one-time import of the old config keys | projects |
| `internal/mcpserver` | the MCP server, transports, log tools | 0; tools added by A, E |
| `internal/ingest/httpapi` | `POST /api/log` | 0 |
| `internal/ingest/{syslog,netconsole,otlp}` | listeners and parsers | B |
| `internal/ingest/serial` | ports, RFC 2217, reconnect, writes and line control | E |
| `internal/push` | the `cafard push` client | E |
| `internal/messaging` | messages, channels, bindings, follows, acks, questions, claims, log attachments, timeline union, rate limits | A |
| `internal/blob` | content-addressed files, image variants, worker pool | A |
| `internal/web/assets` | `embed` of templates and static files, `bundleBuilt` | 0 |
| `internal/web/pages` | server-rendered pages and form posts | C, D |
| `internal/web/api` | JSON API under `/api/` (except `/api/log` and `/api/events`) | A (messages), C, D |
| `internal/web/sse` | `GET /api/events` | C |
| `src/` | the TypeScript bundle | C, D |
| `deploy/`, `scripts/`, `.github/` | install, units, CI, release | 0 (CI, remote.sh), F |

A package may import the ones above it in this table, but never one owned
by a later track, except through an interface defined in `internal/deps`.

### 1.2 Wiring

```go
// internal/deps
type Deps struct {
	Server    Server           // root settings: public_url, http_listen, admin_listen, secure, tls, data_dir
	Store     *store.Store
	Writer    writer.Writer
	Hub       hub.Hub
	Status    status.Registry
	Auth      auth.Authenticator
	Tokens    *auth.Tokens
	Admin     *auth.Admin      // the admin password and in-memory admin sessions
	Janitor   *store.Janitor   // Add(store.Task) for acks, blobs, claims, sessions
	Projects  *projects.Service // projects, sinks, grants and Access (section 1.10)
	Blobs     BlobStore        // nil until track A; an interface, see below
	Messaging Messaging        // nil until track A; an interface, see below
	Clock     clock.Clock      // interface { Now() time.Time; After(d) <-chan time.Time }
	Log       *slog.Logger
	Version   string
}

// Every feature package (cfg only when the feature has a config section):
func Register(mux *http.ServeMux, mcp *mcpserver.Server, d deps.Deps[, cfg *Config]) (start func(ctx context.Context) error, err error)
```

- `start` runs the feature's background work (listeners, janitor, port
  readers) until the context is cancelled. It is `nil` when there is none.
- `main` builds the mux once (a grafit gotcha), calls every `Register`,
  starts everything in an `errgroup`, and shuts down on SIGINT and SIGTERM.
  `mux` is the public mux. `web.Register` alone also takes the admin mux
  (`Register(public, admin, mcp, d)`) for the admin-only routes; the admin
  mux hands every other path to the public one. Each is built once, so no
  pattern is registered twice on one mux. With `admin_listen` empty,
  `http_listen` serves the admin mux; set, it serves the public mux and
  `admin_listen` the admin one.
- A feature gets its own config section as an argument, not through
  `Deps`: `internal/config` imports every feature package for its section
  type, so a feature cannot import `internal/config`.
- MCP tools are added with `mcpserver.AddTool(s, scope, tool, handler)`
  inside `Register`, before `/mcp` serves. A handler returns the agent's
  text, the structured output, and a `*dto.APIError` for errors the agent
  should read.

### 1.3 Entries

```go
// internal/store
type Kind string // "line" | "marker" | "message"

type Entry struct {
	Seq         int64            // 0 until committed
	TS          time.Time        // receive time, set by the writer if zero
	SrcTS       *time.Time
	Stream      string           // "serial:dut1", "chan:dut1", "dm:alice~codex-build"
	Kind        Kind
	Severity    int              // 0..7, default 6
	Host, App   string
	Text        string
	Attachments []Attachment     // images and log slices, messages only
	Sender      string           // principal name, messages and agent markers
	Recipient   string           // "", "@humans" or a principal name
	ReplyTo     int64
	Meta        json.RawMessage  // <= 4 KiB
}
```

### 1.4 Writer

```go
// internal/writer
type Writer interface {
	// Submit queues an entry and returns at once. With lossy=true it never
	// blocks: when the queue is full it returns ErrFull and the caller
	// counts a drop in status. With lossy=false it blocks until queued or ctx ends.
	Submit(ctx context.Context, e store.Entry, lossy bool) error
	// Commit queues an entry and waits for its commit. It returns the seq.
	Commit(ctx context.Context, e store.Entry) (int64, error)
	// CommitBatch is Commit for several entries in one transaction (OTLP, NDJSON).
	CommitBatch(ctx context.Context, es []store.Entry) (last int64, err error)
}
var ErrFull = errors.New("writer queue full")
```

- Only the writer writes `entries` and `entries_fts`.
- Other tables are written directly by their owner in short transactions.
- A marker those actions produce goes through `Commit`.
- The writer calls `Status.Seen(stream, ts)` for every committed entry, so
  ingesters do not have to.

### 1.5 Hub

```go
// internal/hub
type Filter struct {
	Streams     []string        // exact ids or path.Match globs; empty = all non-message streams
	Kinds       []store.Kind    // empty = all
	MinSeverity int             // keep entries with Severity <= MinSeverity; 7 = all
	Pattern     *regexp.Regexp  // on Text, nil = all
	Viewer      *auth.Principal // non-nil: message entries pass only if visible to Viewer
}

type Hub interface {
	Subscribe(f Filter, buffer int) Subscription
}

type Subscription interface {
	C() <-chan store.Entry
	// Lagged is closed if the subscriber fell behind and was dropped.
	// The subscriber must re-read from the store from its last seq.
	Lagged() <-chan struct{}
	Close()
}
```

- `Publish` is not part of the interface: only the writer publishes, after
  the commit.
- Waiting tools follow one order: subscribe first, then read the store from
  `since_seq`, then drain the subscription, skipping seqs already seen. This
  order is what makes "no entry lost between read and wait" hold.

### 1.6 Status registry

```go
// internal/status
type State string // "connected" | "idle" | "down"

type Source struct {
	Stream    string
	Transport string // "local" | "rfc2217" | "tcp" | "udp" | "http" | "otlp" | "push" | "internal"
}

type Registry interface {
	Declare(s Source)                       // at start or on first entry
	Seen(stream string, ts time.Time)       // called by the writer
	SetConnected(stream string, up bool, reason string)  // serial and TCP only
	Dropped(stream string, n int)
	SetLineState(stream string, ls LineState)  // DTR, RTS, CTS, DCD when known
	Snapshot() []dto.Stream                 // claim is filled in by the caller
	Watch(buffer int) (<-chan dto.Stream, func())  // changes, throttled to 1 per 2 s per stream
}
```

- `idle` means no entry for 5 minutes.
- `down` means `SetConnected(false)`. It only applies to serial and TCP
  sources.
- Rates are lines per minute over a sliding 5-minute window.

### 1.7 Text cleaning and agent output

```go
// internal/textclean
type Splitter struct{ /* ... */ }
func NewSplitter(flushAfter time.Duration, maxLine int, emit func(line string, at time.Time)) *Splitter
func (s *Splitter) Write(p []byte, at time.Time)   // handles \n, \r\n, lone \r, partial lines
func (s *Splitter) Tick(now time.Time)             // flushes a partial line older than flushAfter
func (s *Splitter) Close()

func StripANSI(s string) string                    // and control characters except \t

type Limits struct{ MaxLines, MaxBytes, MaxLineLen int }   // defaults 400, 60 KB, 2000
var AgentDefaults = Limits{400, 60 << 10, 2000}

type Formatted struct {
	Text    string // header line(s) + one line per entry, PLAN section 7 format
	Shown   int
	NextSeq int64
	HasMore bool
}
func Format(entries []store.Entry, lim Limits, loc *time.Location) Formatted
```

`Format` is used everywhere agent-facing text is produced:

- the MCP tools;
- `channel_timeline`;
- the log part of `get_message`;
- log attachment snapshots, stored as the `Text` of a `Formatted` with
  unlimited `MaxLines`, capped by the snapshot thresholds.

### 1.8 Auth

```go
// internal/auth
type Scope string // "ingest" | "read" | "message" | "serial_write" | "admin"

type Principal struct {
	Name      string
	Kind      string   // "agent" | "human" | "device"
	Scopes    ScopeSet // a cap: grants decide where they apply (1.10)
	TokenID   int64    // 0 for a session
	SessionID int64    // 0 for a bearer token
	Sink      string   // "project/sink" for a sink token, else ""
}

type Authenticator interface {
	// FromRequest reads "Authorization: Bearer cfd_..." or the session cookie.
	// Bearer wins when both are present. It returns ErrNoCredentials or ErrInvalid.
	FromRequest(r *http.Request) (Principal, error)
}

func Require(a Authenticator, scope Scope, next http.Handler) http.Handler  // 401/403 per section 3.2
func RequireSameOrigin(next http.Handler) http.Handler                      // Sec-Fetch-Site, Origin fallback
func PrincipalFrom(ctx context.Context) (Principal, bool)
```

- `/mcp` accepts bearer tokens only. It wraps `FromRequest` and rejects a
  request authenticated by cookie.
- OAuth (M8, optional) will be another `Authenticator`.

### 1.9 Interfaces filled in by track A

```go
// internal/deps
type BlobStore interface {
	Put(ctx context.Context, r io.Reader, mime string) (dto.Blob, error)
	Open(ctx context.Context, sha string, variant string) (io.ReadSeekCloser, dto.Blob, error) // "", "agent", "thumb"
	VisibleTo(ctx context.Context, sha string, p auth.Principal) (bool, error)
}

type Messaging interface {
	Send(ctx context.Context, from auth.Principal, m dto.SendRequest) (dto.Entry, error)
	Visible(ctx context.Context, p auth.Principal, e store.Entry) bool
	ResolveChannel(ctx context.Context, p auth.Principal, name string) (chat string, patterns []string, err error) // chan:<project>/<name>, binding patterns (ids, globs)
	ChatOf(e store.Entry) string                                                   // "chan:x" | "dm:a~b" | ""
}
```

Tracks B, C and E code against these interfaces, and use fakes until A
lands.

`Visible` applies grants (1.10) to every entry, logs included: a log
entry is visible when the viewer may read its stream, a channel message
when it may read the channel, a direct message to its two parties only.
`ResolveChannel` takes `project/name`, or a bare `name` that only one
project visible to `p` has, and returns its bindings, which reading the
channel lets `p` read.

### 1.10 Projects and access

```go
// internal/projects
type Project struct {
	Name, Title string
	CreatedAt   time.Time
	Retention   Limits // the four limits; 0 = forever or no limit
}

type Sink struct {
	ID            int64
	Project, Name string           // stream ids end in project + "/" + name
	Title         string
	Sources       []netip.Prefix   // syslog and netconsole senders
	Serial        *SerialPort      // at most one port; serial.Port is this type, Name is project/name
}

type Grant struct {
	ID                 int64
	Principal, Project string
	Channel            string        // "" = the whole project
	Scopes             auth.ScopeSet // read, message, ingest (project only), serial_write
}

// Access is what one principal may reach, from its grants capped by its
// credential's scopes. The admin session reaches everything.
type Access struct{ /* ... */ }
func (s *Service) Access(ctx context.Context, p auth.Principal) *Access
func (a *Access) All() bool                          // the admin
func (a *Access) Projects() []string                 // projects with any grant
func (a *Access) Can(stream string, sc auth.Scope) bool // chan:, dm: and log streams
func (a *Access) LogPatterns() []string              // globs of readable log streams; nil = all
func (a *Access) ChannelPatterns() []string          // chan: globs of readable channels; nil = all
func (a *Access) ProjectScopes(project string) auth.ScopeSet // project grants only

// Routing: what ingest asks.
func (s *Service) SinkForAddr(ip netip.Addr) (Sink, bool)           // syslog, netconsole
func (s *Service) SinkForIngest(ctx context.Context, p auth.Principal, name string) (Sink, bool) // HTTP, OTLP
func (s *Service) SerialPorts() []serial.Port      // what the serial manager runs
func (s *Service) OnSinksChanged(f func())         // after a sink is created, changed or deleted

// internal/store
func ProjectOf(stream string) string               // "lab" for serial:lab/dut1 and chan:lab/x; "" for dm: and unprefixed ids
func LocalName(stream string) string               // "dut1" for serial:lab/dut1
```

- Grants are cached per principal and credential; every write through the
  service drops the cache, and entries also expire after 5 s so grants
  added from the CLI (another process) apply without a restart.
- `store.ProjectOf` is the only parser of stream ids into projects;
  everything that checks access goes through `Access.Can`.

---

## 2. Routes

**Auth** column:

- `public`: no authentication.
- `session`: browser cookie only (on the admin interface, the admin
  session's `cafard_admin` cookie counts too).
- `bearer`: token only.
- `any`: bearer or session.

**Scope** is the scope required on top of that. Only an admin session has
`admin`; no token or browser session can carry it.

**Admin interface.** Routes marked *(admin interface)* are on the admin mux
only: with `admin_listen` set they answer 404 on `http_listen`. The admin
mux serves every other route too, by handing it to the public mux.

**CSRF** column:

- `SO`: `RequireSameOrigin` applies.
- `-`: not applicable, because the route is a safe method or bearer-only.

### 2.1 Pages (HTML, `internal/web/pages`)

| Method | Path | Auth | Scope | CSRF | Notes |
|---|---|---|---|---|---|
| GET | `/` | session | read | - | home: chat list, overview on wide screens. No session: 303 to `/signin` |
| GET | `/c/{project}/{name}` | session | read (grant) | - | channel; `?view=logs`, `?f=`, `?before_seq=` |
| GET | `/dm/{peer}` | session | read | - | direct conversation with `peer` |
| GET | `/drop` | session | message | - | quick-drop form; `?share=<id>` preloads shared files |
| GET | `/live` | session | read | - | query `streams, channel, from_seq, to_seq, sev, re, kinds` |
| GET | `/streams` | session | read | - | sources |
| GET | `/agents` | session | admin | - | principals, tokens, password change *(admin interface)* |
| GET | `/pair` | session | admin | - | pairing *(admin interface)* |
| GET | `/pair/{id}/qr.svg` | session | admin | - | QR image of a pending pairing *(admin interface)* |
| GET | `/projects` | session | admin | - | projects and the create form *(admin interface)* |
| POST | `/projects` | session | admin | SO | form `name, title`; 303 to `/projects/{name}` *(admin interface)* |
| GET | `/projects/{project}` | session | admin | - | channels, sinks, retention, members, sign-in links *(admin interface)* |
| POST | `/projects/{project}/retention` | session | admin | SO | form `lines_max_age, lines_max_bytes, messages_max_age, blobs_max_bytes` (durations and sizes as in the config; 0 = none) *(admin interface)* |
| POST | `/projects/{project}/channels` | session | admin | SO | form `name, title, topic, patterns` (one per line) *(admin interface)* |
| POST | `/projects/{project}/sinks` | session | admin | SO | form `name, title, sources` (one IP or CIDR per line), `serial_kind` (`none\|device\|rfc2217\|tcp`), `serial_address, baud, data_bits, parity, stop_bits, writable` *(admin interface)* |
| POST | `/projects/{project}/sinks/{sink}` | session | admin | SO | same form, edits the sink *(admin interface)* |
| POST | `/projects/{project}/sinks/{sink}/delete` | session | admin | SO | stops its port, revokes its tokens, keeps its history *(admin interface)* |
| POST | `/projects/{project}/sinks/{sink}/tokens` | session | admin | SO | form `label`; renders the project page with the sink token shown once *(admin interface)* |
| POST | `/projects/{project}/grants` | session | admin | SO | form `principal, kind, channel` (empty = whole project), `scopes` (repeated) *(admin interface)* |
| POST | `/projects/{project}/grants/{id}/delete` | session | admin | SO | *(admin interface)* |
| POST | `/projects/{project}/pairings` | session | admin | SO | form `principal` or `new_name`, `channel`, `scopes`; renders the project page with the link and its QR *(admin interface)* |
| GET | `/signin` | public | - | - | admin interface: the password form; public: "ask the administrator for a sign-in link" |
| POST | `/signin` | public | - | SO | form `password`; opens an admin session (cookie `cafard_admin`); 303 to `next` or `/`; 10 a minute per address *(admin interface; 404 on the public one)* |
| POST | `/password` | session | admin | SO | form `current, new, repeat`; ends every admin session; 303 to `/signin?changed=1` *(admin interface)* |
| POST | `/signout` | session | - | SO | ends the admin session or the browser session; 303 to `/signin` |
| GET | `/p/{code}` | public | - | - | redeems a pairing code, sets the cookie, 303 to `/` |
| POST | `/theme` | session | - | SO | form `theme=system\|light\|dark`, cookie, 303 back |
| POST | `/pair` | session | admin | SO | form `principal` or `new_name`, `grants` (`project[:channel]=scopes`, one per line); 303 to `/pair` *(admin interface)* |
| POST | `/share` | session | message | SO | Web Share Target, multipart `text, title, url, files[]`; see 2.4 |
| GET | `/seen` | session | message | same-origin `Sec-Fetch-Site` only | `?seqs=1,2`: the no-JS read beacon in `<noscript>` (a 1x1 GIF); acks those messages |
| POST | `/tokens` | session | admin | SO | no-JS token creation; renders `/agents` showing the token once *(admin interface)* |
| POST | `/tokens/{id}/revoke` | session | admin | SO | 303 to `/agents` *(admin interface)* |
| POST | `/sessions/{id}/revoke` | session | admin, or own session | SO | 303 back |

Revoking a token or a session **deletes its row**. There is no
`revoked_at` column. The exposure guard checks that an admin password is
set.

### 2.2 Data and ingest

| Method | Path | Auth | Scope | Package |
|---|---|---|---|---|
| POST | `/api/log` | bearer | ingest + a sink (token or grant) | `ingest/httpapi`; `{accepted, last_seq, dropped}` |
| POST | `/v1/logs` | bearer | ingest + a sink (token or grant) | `ingest/otlp`; unrouted records are rejected in `partial_success` |
| POST | `/v1/traces`, `/v1/metrics` | - | - | 404 |
| GET, POST, DELETE | `/mcp` | bearer | read (tools check their own, section 5) | `mcpserver` |
| GET | `/api/events` | any | read | `web/sse` (section 4) |
| GET | `/blob/{sha}` | any | read + `BlobStore.VisibleTo` | `web/api`; `?v=thumb\|agent`; 404 when not visible (a log snapshot also needs every stream it covers readable) |
| GET | `/healthz` | public | - | `200 ok <version>` |
| GET | `/static/*`, `/app/main.js`, `/manifest.webmanifest`, `/sw.js`, `/icons.svg` | public | - | `web/assets`; `sw.js` at the root for scope `/` |

### 2.3 JSON API (`internal/web/api`)

All routes take `any` auth. Writes need `SO` when called with a session.
The DTOs are in section 3.

| Method | Path | Scope | Request | Response |
|---|---|---|---|---|
| GET | `/api/me` | read | | `Me` |
| GET | `/api/principals` | read | `?kind=` | `{principals: Principal[]}` |
| GET | `/api/projects` | read | | `{projects: Project[]}`, the visible ones |
| GET | `/api/chats` | read | `?archived=1&all=1&project=` | `{chats: Chat[]}`, in list order |
| GET | `/api/overview` | read | | `Overview` |
| GET | `/api/chats/{chat}/timeline` | read | `?before_seq=&since_seq=&limit=50&f=` | `Page<Entry>` (messages + event rows per `surface`, `f`) |
| PUT | `/api/chats/{chat}/prefs` | message | `{pinned?, muted?, followed?}` | `Chat` |
| POST | `/api/chats/{chat}/read` | message | `{up_to_seq}` | `{acked: n}` |
| POST | `/api/acks` | message | `{seqs: []}` | `{acked: n}` |
| POST | `/api/channels` | message (project grant) | `ChannelCreate` | `Chat` (201) |
| PATCH | `/api/channels/{name}` | message (project grant; rename, archive: creator or admin) | `ChannelUpdate` | `Chat` |
| DELETE | `/api/channels/{name}` | message (project grant; creator or admin) | `?confirm={name}` | 204 |
| POST | `/api/messages` | message | multipart, section 3.4 | `Entry` (201), or 303 for a form post |
| GET | `/api/messages/{seq}` | read | | `Entry` |
| GET | `/api/messages/{seq}/attachments/{index}` | read | `?since_seq=&limit=` | `Page<Entry>` (lines of a log attachment) |
| GET | `/api/shares/{id}` | message | | `Share` (staged files from `/share`) |
| GET | `/api/streams` | read | `?channel=&project=` | `{streams: Stream[], unrouted?: {transport: count}}` (`unrouted`: admin only) |
| GET | `/api/streams/match` | read | `?pattern=` | `{streams: Stream[]}` (glob preview) |
| GET | `/api/entries` | read | `?streams=&channel=&since_seq=&before_seq=&from_seq=&to_seq=&sev=&re=&kinds=&limit=` | `Page<Entry>` (never `chan:*`/`dm:*`) |
| GET | `/api/entries/{seq}/context` | read | `?before=20&after=20` | `Page<Entry>` with `focus_seq` |
| POST | `/api/marks` | message | `{stream, label}` | `{seq}` |
| GET | `/api/tokens` | admin | | `{tokens: Token[]}` *(admin interface)* |
| POST | `/api/tokens` | admin | `{principal, kind?, label, scopes[]}` (principal created if new; `admin` refused) | `TokenCreated` *(admin interface)* |
| DELETE | `/api/tokens/{id}` | admin | | 204 *(admin interface)* |
| GET | `/api/sessions` | admin (others: own only) | | `{sessions: Session[]}` |
| DELETE | `/api/sessions/{id}` | admin, or own | | 204 |
| POST | `/api/pairings` | admin | `{principal?, new_name?, scopes?, grants?}` (`admin` refused; grants `project[:channel]=scopes`, added when redeemed) | `Pairing` *(admin interface)* |

`{chat}` is a chat id: `chan:<project>/<name>` or `dm:<peer>`, where peer
is the other principal. The server builds `dm:<a>~<b>` itself, so the
client never sorts names. `{chat}` and `{name}` (`<project>/<name>`) carry
a `/`, so clients escape them (`%2F`); the mux matches the escaped segment.

Every route above also applies grants (PLAN section 4, "Projects, sinks
and grants"): what the caller may not read answers 404 or is left out of
lists; a write without the grant answers 403 `forbidden`, naming it.

### 2.4 Share target

- `POST /share` stores the files as blobs, keeps a **staged share** in
  memory for 1 hour (`{id, principal, text, blobs[]}`), and answers 303 to
  `/drop?share=<id>`.
- With the bundle, the quick-drop sheet opens on `/?share=<id>` instead. The
  service worker rewrites the redirect target, and does nothing else.
- Sending uses `POST /api/messages` with `staged=<id>`.
- Staged blobs are protected from the janitor for 1 hour.
- A restart loses staged shares. That is acceptable.

---

## 3. JSON API

### 3.1 Conventions

- Responses are `application/json; charset=utf-8`. Unknown fields must be
  ignored by clients, which allows additive changes.
- `Page<T>`:
  `{items: T[], has_more: bool, next_seq?: int, next_before_seq?: int, focus_seq?: int}`.
- **`X-Cafard: 1`** is sent by the bundle's `HttpClient` on every request.
  It is **not** a security control (CSRF is `Sec-Fetch-Site`). It tells
  `POST /api/messages` and the form routes to answer JSON or an error DTO,
  instead of a 303 or an HTML error page.

### 3.2 Errors

```json
{ "error": { "code": "rate_limited", "message": "You sent 30 messages in the last minute; wait 12 s.", "retry_after_s": 12 } }
```

| HTTP | `code` |
|---|---|
| 400 | `bad_request`, `invalid_pattern`, `unknown_channel` (message lists the existing ones), `too_large` |
| 401 | `unauthenticated` |
| 403 | `forbidden` (missing scope: the message names it), `cross_origin` |
| 404 | `not_found` (also when something exists but is not visible to the caller) |
| 409 | `conflict` (name taken, claim held: the message names the holder) |
| 413 | `too_large` |
| 429 | `rate_limited` (+ `Retry-After` header) |
| 503 | `busy` (+ `Retry-After`) |

`message` is a full sentence written for a person ("the server owns the
wording"). The bundle shows it as is.

### 3.3 DTOs

TypeScript notation, with `?` marking an optional field. Go types with the
same JSON live in `internal/dto`.

```ts
type Me = { name: string; kind: PrincipalKind; scopes: Scope[]; session_id?: number; theme: "system"|"light"|"dark" };
type PrincipalKind = "agent" | "human" | "device";
type Scope = "ingest" | "read" | "message" | "serial_write" | "admin";
type Principal = { name: string; kind: PrincipalKind; last_seen?: string };

type Entry = {
  seq: number; ts: string; src_ts?: string;
  stream: string; kind: "line" | "marker" | "message";
  severity: number; host?: string; app?: string;
  text: string;
  meta?: Record<string, unknown>;
  // messages only:
  chat?: string;                     // "chan:dut1" | "dm:codex-build" (peer from the viewer's side)
  sender?: { name: string; kind: PrincipalKind };
  recipient?: string;                // "@humans" | principal | absent
  reply_to?: number;
  segments?: Segment[];              // parsed text, render these, not `text`
  attachments?: Attachment[];
  seen_by?: string[];
  question?: { choices: string[]; open: boolean; answer?: { by: string; choice?: string; seq: number } };
  // event rows in a timeline:
  event?: boolean;                   // a line or marker from a bound stream, shown as an event row
};

type Segment =
  | { t: "text"; v: string }
  | { t: "code"; v: string }
  | { t: "codeblock"; v: string; lang?: string }
  | { t: "link"; v: string; href: string };      // href is http(s) only, validated by the server

type Attachment = ImageAttachment | LogAttachment;
type ImageAttachment = {
  type: "image"; sha256: string; mime: string; width?: number; height?: number;
  url: string; thumb_url?: string; preview: boolean;   // preview=false for HEIC
};
type LogAttachment = {
  type: "log";
  stream?: string; channel?: string;   // as sent
  streams: string[];                   // resolved at send time
  from_seq: number; to_seq: number | null;
  grep?: string; min_severity?: number; title?: string;
  lines: number; first_ts?: string; last_ts?: string;
  snapshot: boolean; expired: boolean;
  hidden?: boolean;                    // the viewer may not read every stream: no lines, no preview, no snapshot
  preview: Entry[];                    // first 5 lines
};

type Project = {
  name: string; title?: string; created_at: string;
  scopes: Scope[];                     // the viewer's project-wide scopes (capped); every scope for the admin
  channels: string[];                  // visible chat ids, "chan:lab/dut1"
  sinks: string[];                     // sink names, when the viewer may read the project's logs
  retention: { lines_max_age_s: number; lines_max_bytes: number; messages_max_age_s: number; blobs_max_bytes: number }; // 0 = none
};

type Chat = {
  id: string;                          // "chan:lab/dut1" | "dm:codex-build"
  kind: "channel" | "direct";
  project?: string;                    // channels only
  project_title?: string;
  name: string; title?: string; topic?: string;
  peer?: Principal;                    // direct only
  patterns: string[];                  // channel bindings
  streams: StreamRef[];                // streams the patterns match now
  surface: "none" | "markers" | "errors";
  followed: boolean; pinned: boolean; muted: boolean; archived: boolean;
  source: "runtime" | "config"; created_by?: string;
  can_manage: boolean;                 // viewer may rename, archive, delete
  unread: number; open_questions: number;
  last_activity?: string;
  preview?: { seq: number; sender?: string; text: string; event: boolean };
  // preview.text of a message with images and no text is "[1 image]" or "[N images]"
};
type StreamRef = { id: string; status: StreamState };
type StreamState = "connected" | "idle" | "down";

type Stream = {
  id: string; kind: string; transport: string; labels: Record<string, string>;
  project?: string; sink?: string;     // "lab", "dut1" for serial:lab/dut1
  status: StreamState; last_seq: number; last_activity?: string;
  rate_per_min: number; dropped: number;
  claim?: { principal: string; note?: string; expires_at: string };
  line_state?: { dtr?: boolean; rts?: boolean; cts?: boolean; dcd?: boolean };
  last_line?: string;
};

type Overview = {
  questions: Entry[];                  // open, addressed to the viewer (or @humans), newest first
  direct: Chat[];                      // direct chats with unread > 0
  recent: Entry[];                     // latest 30 messages and event rows across followed chats
};

type Counts = {
  open_questions: number; unread_direct: number; sources_down: number;
  chats: { id: string; unread: number; open_questions: number }[];  // only chats with a change or non-zero
};

type ChannelCreate = { name: string; project?: string; title?: string; topic?: string; patterns?: string[]; surface?: Chat["surface"] };
type ChannelUpdate = {
  rename?: string; title?: string; topic?: string;
  add_patterns?: string[]; remove_patterns?: string[];
  surface?: Chat["surface"]; archived?: boolean;
};

type Token = { id: number; label: string; principal: string; kind: PrincipalKind; scopes: Scope[]; created_at: string; last_used?: string };
type TokenCreated = Token & {
  token: string;                        // shown once
  snippets: { label: string; language: "shell" | "toml" | "json" | "env"; text: string }[];
};
type Session = { id: number; label: string; principal: string; user_agent?: string; created_at: string; expires_at: string; current: boolean };
type Pairing = { id: number; url: string; qr_url: string; expires_at: string; principal?: string; new_name?: string; grants?: string[] }; // "lab:dut1=read,message"
type LogAccepted = { accepted: number; last_seq: number; dropped?: number };   // POST /api/log
type Share = { id: string; text?: string; files: ImageAttachment[]; expires_at: string };
```

### 3.4 `POST /api/messages`

Multipart form fields:

| Field | Meaning |
|---|---|
| `chat` | target chat id (`chan:lab/dut1`, `dm:codex-build`). The bundle always sends this |
| `channel`, `to` | the MCP-style alternative to `chat` (scripts, iOS Shortcut). `to=<principal>` means a DM; `to=@humans` addresses humans in `channel` (`project/name`, or a name only one of the sender's projects has; default `general`, resolved the same way) |
| `text` | at most 32 KiB |
| `reply_to` | seq |
| `choice` | the chosen answer when replying to a question (must be one of its choices) |
| `logs` | JSON array of `{stream \| channel, from_seq, to_seq?, grep?, min_severity?, title?}`, at most 5 |
| `files[]` | images, 10 files of 20 MiB at most |
| `staged` | a share id from `/share`; its files are attached |
| `back` | a same-origin path to return to with `?error=` (only its `share` parameter is kept), for forms not on the chat page (`/drop`, home) |
| `next` | a same-origin path to go to once sent, instead of the chat (home's question cards) |

- With `X-Cafard: 1` or `Accept: application/json`, the response is 201
  with the `Entry`, or an error DTO.
- Otherwise it is a 303 to the chat, or a 303 back with `?error=<code>`,
  which the page renders as a banner.

### 3.5 Fixtures

A Go test (`internal/dto/fixtures_test.go`, track 0) writes one example of
every DTO, plus each error code, to `internal/web/testdata/api/*.json`:

- With `-update` it rewrites the files.
- Without it, it fails if they differ, so CI notices drift.

The TypeScript `model/parse.ts` tests read these files. A new DTO in Go
gets a fixture in the same change.

---

## 4. SSE (`GET /api/events`)

There is one connection per page. The query picks the scope:

| Page | Query | `entry` events carry |
|---|---|---|
| home | `?scope=home` | messages visible to the viewer in followed chats (for previews and Overview) |
| chat | `?chat=chan:lab/dut1` | the chat's messages, and every entry of its bound streams the viewer may read (the client sends lines to the logs pane and event rows to the timeline, per `surface`) |
| chat (direct) | `?chat=dm:codex-build` | the conversation's messages |
| live | `?streams=&channel=&kinds=&sev=&re=` | matching non-message entries of streams the viewer may read (`kinds=message` adds messages visible to the viewer; with `channel=`, that channel's messages only) |
| streams | `?scope=status` | none |
| any | `&since_seq=N` or the `Last-Event-ID` header | replay after N first (at most 5000 entries; beyond that a `reset`) |

Every connection also receives `counts` and `stream_status`.

| Event | `id:` | `data:` | When |
|---|---|---|---|
| `entry` | seq | `Entry` | each matching committed entry |
| `seen` | - | `{seq, by}` | someone acked a message the page shows (receipts) |
| `answered` | - | `{seq, answer: {by, choice?, seq}}` | a question on the page was answered |
| `chat` | - | `Chat` | a chat's metadata, bindings, prefs or archive state changed. On a chat page whose bindings changed, the client reconnects with its last seq |
| `counts` | - | `Counts` | on connect, then on change, throttled to 1 per second |
| `stream_status` | - | `Stream` | a source changed (state, drops, line state, or a new entry), throttled to 1 per 2 s per stream. It carries `last_line`, `last_seq`, `labels` and `claim` (absent: nobody holds it) |
| `reset` | - | `{reason}` | the subscriber lagged, or the replay was too long. The client reloads through the JSON API, then reconnects |

- A heartbeat comment `: ping` is sent every 20 s.
- Messages always pass `Messaging.Visible` for the viewer, and so do log
  entries (grants): the hub filter is built from `Access.LogPatterns`,
  and the handler checks each entry again.

---

## 5. MCP tool scopes and errors

| Scope | Tools |
|---|---|
| read | `whoami`, `list_projects`, `list_principals`, `list_streams`, `read`, `tail`, `wait_for`, `search`, `context`, `list_channels`, `channel_timeline`, `inbox`, `get_message`, `get_log_attachment`, `wait_message`, `list_claims` |
| message | `send`, `ask_human`, `ack`, `mark`, `follow`, `unfollow`, `create_channel`, `update_channel`, `delete_channel`, `claim`, `release` |
| serial_write (+ claim held, + stream `writable`) | `serial_write`, `serial_control` |

- **Tool list.** A tool the token lacks the scope for is left out of
  `tools/list`, so agents are not offered tools that will fail.
- **Grants.** Every tool applies them on top of the scope (PLAN section 4,
  "Projects, sinks and grants"): a stream, channel or message the caller
  may not read is unknown to it (`not_found`, `unknown_channel`, or left
  out of a list), and a write without the grant is `forbidden`.
- **Projects.** `list_projects` returns `{projects: Project[]}`.
  `list_streams`, `list_channels`, `inbox` and `search` take an optional
  `project`. A `channel` argument is `project/name`, or a bare `name` when
  only one visible project has it; `send` and `ask_human` default to
  `general` resolved the same way. Channel ids in results are
  `project/name`.
- **Errors.** A tool error is an MCP result with `isError: true`. The text
  content is the same sentence as `error.message` in section 3.2.
  Structured content is `{code, message, retry_after_s?}`, with the codes
  of section 3.2.
- **`cafard mcp` adds `image_paths` to `send`** (a list of local file
  paths, in its `tools/list` and its calls only). It reads each file,
  sniffs a png, jpeg, webp or gif, and forwards `images: [{data, mime}]`
  in its place: at most 6 images in all, 10 MiB each. A refused file is a
  tool error in the shape above (`not_found`, `bad_request`, `too_large`)
  and nothing is forwarded. The server's `/mcp` refuses `image_paths`
  (`bad_request`, an unknown argument).
- **Structured output.** Every tool that returns entries also returns
  `{next_seq, has_more, shown}` as structured content. The `Entry` DTO is
  used for any entry list in structured content.

---

## 6. Templates

### 6.1 Pages

`<body data-page="...">` picks the TypeScript `Page`:

| `data-page` | Template | Page class | Track |
|---|---|---|---|
| `home` | `pages/home.html` | `HomePage` | D |
| `chat` | `pages/chat.html` (channel and direct) | `ChatPage` | D |
| `drop` | `pages/drop.html` | `DropPage` | D |
| `live` | `pages/live.html` | `LivePage` | C (LogView in M3), page in M5 |
| `streams` | `pages/streams.html` | `StreamsPage` | C |
| `agents` | `pages/agents.html` | `AgentsPage` | C |
| `projects` | `pages/projects.html` | `PairPage` (the shell only) | projects |
| `project` | `pages/project.html` | `PairPage` (the shell, and the countdown of its sign-in codes) | projects |
| `pair` | `pages/pair.html` | `PairPage` | C |
| `signin` | `pages/signin.html` | none | C |

`layout.html` holds the shell (top bar, nav rail and bar, `main`, toast
region, dialog root) and includes `partials/nav.html`.

### 6.2 Partials with a `<template>` twin

Each partial below is rendered twice:

- with data, where it appears on the page;
- with zero values inside `<template id="tpl-...">` at the end of `<body>`,
  on every page that lists it.

| Template id | Partial | `data-slot` names | Attributes the client may set | Pages |
|---|---|---|---|---|
| `tpl-chat-row` | `partials/chat_row.html` | `name`, `preview`, `time`, `unread`, `questions`, `dots` | `href`, `data-chat`, `data-pinned`, `data-muted`, `data-archived` | home, chat |
| `tpl-stream-dot` | `partials/stream_dot.html` | none | `data-status`, `title` | home, chat, streams |
| `tpl-message` | `partials/message.html` | `sender`, `sender-kind`, `time`, `body`, `reply-quote`, `attachments`, `seen-by`, `menu` | `data-seq`, `data-mine`, `data-sender-kind`, `id` (`m<seq>`) | chat, home (overview) |
| `tpl-question` | `partials/question.html` | `sender`, `time`, `body`, `choices`, `reply-form`, `answer` | `data-seq`, `data-open`, `id` (`m<seq>`); the hidden inputs (`data-field`) get their `value` | chat, home |
| `tpl-choice` | inside `question.html` (`partials/choice`) | `label` | `data-choice`, `disabled`, `value` (the submit button's choice) | chat, home |
| `tpl-event-row` | `partials/event_row.html` | `stream`, `time`, `text`, `open` | `data-seq`, `data-severity`, `data-kind`, `href` on `open` | chat, home |
| `tpl-line` | `partials/line.html` | `time`, `stream`, `sev`, `text` | `data-seq`, `data-sev`, `data-tone` (0-7), `data-selected`, `data-kind` (`message` for a message row in Live) | chat, live, home (log attachment previews) |
| `tpl-marker` | `partials/marker.html` | `time`, `text` | `data-seq` | chat, live |
| `tpl-image` | `partials/image.html` | `img`, `more` | `src` (only `/blob/...`), `alt`, `width`, `height`, `data-sha`, `href` on the root (only `/blob/...`) | chat, home, drop |
| `tpl-log-attachment` | `partials/log_attachment.html` | `streams`, `title`, `lines`, `span`, `preview`, `open`, `state` | `href` on `open` (only `/live?...` or `#`), `data-snapshot`, `data-expired`, `data-open-ended` | chat, home |
| `tpl-attachment-chip` | `partials/attachment_chip.html` | `label`, `remove` | `data-kind`, `data-index` | chat, home, drop, live |
| `tpl-upload-status` | `partials/upload_status.html` | `label`, `progress`, `retry` | `data-state`, `value` on progress | all with a composer |
| `tpl-toast` | `partials/toast.html` | `text`, `action` | `data-tone` | all |
| `tpl-stream-card` | `partials/stream_card.html` | `name`, `status`, `transport`, `rate`, `last`, `preview`, `dropped`, `claim`, `line-state`, `open` | `data-stream`, `data-status`, `href` on `open` | streams |
| `tpl-needs-you-question` | `partials/needs_you.html` (`partials/needs_you_question`) | as `tpl-question`, plus `chat` (link to the question in its chat) | as `tpl-question`, plus `href` on `chat` (same-origin) | home |

The chat list groups its channel rows in one `<section class="project-chats"
data-project="<name>">` per project, each with its `data-section="channels"`
(and, when listed, `unfollowed` and `archived`) rows carrying the same
`data-project`; direct chats are in the one `data-section="direct"`. The
bundle puts a new row in the section of its state and `Chat.project`, and
leaves it to the next load when the page has no such section. Sources group
stream cards in `.stream-group[data-project]`, by `Stream.project` (`""`
for streams outside projects, listed last); the bundle makes a group for a
project the page did not have.

### 6.3 Server-only partials (no twin)

These are on the page from the start, and the bundle only attaches
behaviour to them:

- `nav`, `channel_header`, `channel_dialog`, `composer`, `quick_drop_sheet`
- `image_viewer`, `range_bar`, `filter_bar`, `logs_pane`, `line_menu`, `overview`
- `token_dialog`, `pair_panel`, `shortcuts_dialog`, `empty_*`
- `sink_form`, `grant_fields` (the project page)

Their mount points are `data-component="<ComponentName>"`.

### 6.4 Filling rules

- **Text slots** are filled with `textContent`.
- **Container slots** (`dots`, `attachments`, `choices`, `preview`, `body`)
  receive child elements made from other templates or by `TextSegments`.
- **Attributes** come only from the table in 6.2. URLs are built from DTO
  fields with an allowlist:
  - `src`: `/blob/` only;
  - `href`: same-origin paths, or the `href` of a `link` segment, which the
    server has already validated as http(s);
  - anything else is dropped.
- **`TextSegments`** renders `text` as a text node, `code` as `<code>`,
  `codeblock` as `<pre><code>`, and `link` as
  `<a rel="noopener noreferrer" target="_blank">`.
- **Tests.** A Go test renders every page and checks that each `tpl-*` it
  lists exists and carries the slots in 6.2. It writes the rendered pages
  to `internal/web/testdata/pages/*.html`. The TS renderer tests load those
  files with happy-dom.

---

## 7. Work split for parallel agents

### 7.1 Prerequisite

The repository must be under git, with the GitHub remote of `PLAN.md`
section 3, before more than one agent works. **The user does this.** Until
then, one agent works at a time.

- Every track works on its own branch (`track/<letter>-<topic>`) and
  rebases on `main` before merging.
- Every merge into `main` passes `scripts/remote.sh` and the frontend
  checks.

### 7.2 Tracks

| Track | Scope (milestones of PLAN section 14) | Starts after | Can run with |
|---|---|---|---|
| **0, Foundation** | all of M0 and M1: config, store and the migration runner, writer, hub, status, textclean, auth (tokens, `Authenticator`, exposure guard), `deps`, `dto` with fixtures, `web/assets` with layout and `bundleBuilt`, `POST /api/log`, `cafard push` from stdin, the MCP server with the log tools, CI, `remote.sh` | nothing | nothing (alone) |
| **A, Messaging** | M2: `internal/messaging` (messages, channels, follows, acks, questions, claims, log attachments), `internal/blob`, MCP message, channel and claim tools, `POST /api/messages` with bearer auth, `GET /blob` | 0 | B, C, E1, F |
| **B, Network ingest** | M4: syslog, netconsole, OTLP | 0 | A, C, E1, F |
| **C, Web foundation** | the parts of M3 that do not need messaging: sessions, sign-in, pairing, CSRF, theme, the shell and nav, Material 3 CSS and the palette, `src/core`, `net`, `model`, `render` (AnsiParser, SlotTemplate, LineRenderer, MarkerRenderer), LogView, FilterBar, ConnectionBadge, Toast, `web/sse`, `/streams` (minimal), `/agents`, `/pair`, `/signin`, and the `live` LogView mount | 0 | A, B, E1, F |
| **D, Chat UI** | the rest of M3: home, chat, drop, ChatList, ConversationView, Composer, UploadQueue, the image strategies, QuestionCard, log attachment UI and range selection, the logs pane, the channel dialog, the chat JSON API, PWA, the share target | A and C | B, E, F |
| **E1, Serial ingest** | M5 server side: local and RFC 2217 and TCP ports, reconnect markers, line state into `status`; `cafard push --serial` and `--file --follow`, the spool, Windows builds in CI | 0 | A, B, C, F |
| **E2, Serial control** | `serial_write` and `serial_control` tools with claims | A and E1 | D, F |
| **F, Deployment** | `deploy/` (install.sh, systemd unit, container drop-in, OpenRC, example config, README), `scripts/devdeploy.sh`, release workflow | 0 | everything |
| **G, Polish** | M5 `/live` page and full Sources, M6 (`cafard term`, `search`, stdio proxy), M7 and M8 if asked | D and E1 | F |

Up to five agents can work at once, once track 0 has merged: A, B, C, E1
and F.

### 7.3 Rules for shared files

| File | Rule |
|---|---|
| `cmd/cafard/main.go` | add one `Register` call and one subcommand at most; nothing else |
| `internal/config/config.go` | add one field typed by your package's config struct; defaults and validation live in your package |
| `internal/dto/*.go` | add files, never edit another track's types; changing a type means changing this file first |
| `internal/store/migrations/` | one file per migration, `NNNN_<track>_<topic>.sql`. **Numbers are reserved by track:** 0001-0009 for 0, 0010-0019 for A, 0020-0029 for C, 0030-0039 for B, 0040-0049 for E, 0050+ for G. The runner applies them in order and records each one. A merged migration is never edited |
| `src/App.ts`, `src/pages/PageRegistry.ts` | add one registration line per page |
| `internal/web/assets/static/app.css` | track C owns the tokens and the shell. Other tracks add a section at the end, `/* --- <component> (track X) --- */`, using only the tokens |
| `internal/web/assets/templates/layout.html`, `partials/nav.html` | track C only; others ask through `DECISIONS.md` |
| `PLAN.md`, `CONTRACTS.md` | contract changes per the rules at the top; any track may fix a typo |
| `DECISIONS.md` | append only, newest first |

### 7.4 Done for a track

A track is done when all of the following hold:

- The "Done when" checks of its milestones pass.
- `scripts/remote.sh` passes, and so do `npm run typecheck`, `npm test` and
  `npm run build`.
- The fixtures are up to date.
- `AGENTS.md` gained any "gotcha that will cost you an afternoon" found on
  the way.
- `README.md` documents what a user can now do.
