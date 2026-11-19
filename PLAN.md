# cafard: implementation plan

> *cafard* (French): a snitch, and a cockroach. Devices snitch on themselves,
> agents gossip with each other, and a human drops photos in from a phone.

This is the full brief for the agent implementing cafard. Read it end to end
before writing code.

- **Decided**: not up for debate.
- **Default**: may change for a concrete reason, written in `DECISIONS.md`.

`CONTRACTS.md` holds the interfaces between parts (Go interfaces, routes,
JSON, SSE, templates, MCP scopes) and the work split. When PLAN.md and
CONTRACTS.md disagree on a contract, CONTRACTS.md wins; fix the other.

The conventions (layout, build, CI, release, deployment, frontend style) are
taken from the user's other Go projects in `~/misc`, mainly **grafit** (Go +
TypeScript classes, embedded bundle, systemd installer) and **piCVert**
(OpenRC, LXC container drop-in, `devdeploy.sh`). When this plan is silent on a
convention, open those repositories and do what they do. Section 13 lists the
exact files to copy from.

---

## 1. What it is

One self-hosted server that:

1. **Collects** logs and images from devices and humans: a phone-first web
   page, syslog, netconsole, OpenTelemetry (OTLP/HTTP), serial ports (local
   or RFC 2217), and a push client that also runs on Windows.
2. **Serves** them to AI agents over MCP: one timeline, cursors, search, and a
   "wait until this pattern appears" tool.
3. **Relays messages** between agents, and between agents and the human:
   an agent can tell another agent what it found, hand over an image, or ask
   the human to press the reset button and wait for the answer.

```
 producers                          cafard                              consumers
 ---------               ---------------------------------             ---------
 phone / browser --HTTP->|                                 |<--MCP---- agent A
 syslog UDP/TCP  ------->| ingest -> writer -> SQLite      |<--MCP---- agent B
 netconsole UDP  ------->|                 \-> blob store  |
 serial / RFC 2217 ----->|                 \-> hub (live)  |--SSE----> browser (live, messages)
 OTLP/HTTP exporters --->|                                 |
 cafard push (Win/Linux)>|                                 |
                         |   messages: agent <-> agent <-> human       |
```

### Typical uses

- **Photo of a panic.** A developer photographs a kernel panic on a phone
  and drops it in. An agent sees it with `inbox` then `get_message`.
- **Wait on a boot.** A board boots on a serial port. An agent calls
  `wait_for(pattern="login:|Kernel panic")` and reacts to whichever line
  appears first.
- **Ask the human.** An agent needs a power cycle. It calls
  `ask_human("Power-cycle dut1 please", choices=["done","can't"])`. The
  question appears on the phone, and the tool returns the human's answer.
- **Hand over to another agent.** Agent A runs the firmware build and agent B
  runs the boot test. A sends B `"image v42 is at http://..., go"` and B
  replies with the result.
- **Windows serial port.** A Windows laptop forwards COM5 with
  `cafard.exe push --serial COM5`.

### Why build it

No existing project covers this whole (surveyed 2026-11):

- `mikezio/agent-drop` has a drop page and MCP, but works on localhost only
  and has no logs.
- `qarnet/serial-mcp` reads serial ports, but only on the machine that hosts
  it.
- Syslog MCP servers (`crunchtools/mcp-syslog`, the Loki and Graylog ones)
  only query a store that already exists.
- `KI7MT/agent-inbox` passes messages between agents, but has no humans, logs
  or images.

Gluing these together loses what matters most: one timeline with shared
cursors, `wait_for`, and messages that sit in the same timeline as the logs.
That last point lets an agent read "what happened on the serial port between
my message and the reply".

### Non-goals

- Not a log management platform: no dashboards, alerting or multi-tenancy.
- Not exposed to the internet. It runs on a trusted lab network.
- No horizontal scaling: one process and one SQLite file.
- Not a general chat application. Messages exist to coordinate work on
  devices.

---

## 2. Technology (Decided)

| Concern | Choice | Notes |
|---|---|---|
| Server language | Go, `go 1.26.0` in `go.mod` (same as grafit and piCVert) | |
| cgo | **Disabled everywhere** (`CGO_ENABLED=0`) | Cross-compiles to linux amd64/arm64/armv7 and windows amd64/arm64 from a machine with no C toolchain |
| MCP | `github.com/modelcontextprotocol/go-sdk` (v1.8.x when this was written) | Official SDK, streamable HTTP and stdio. Read its README and `go doc` before use; the API has moved between versions |
| Database | `modernc.org/sqlite` (pure Go, as grafit), WAL, FTS5, foreign keys on in the DSN | |
| Serial | `go.bug.st/serial` | Pure Go on Linux, macOS, Windows |
| Serial over network | RFC 2217 client written in-house (`internal/ingest/serial/rfc2217`) | About 300 lines of Telnet option handling; no library worth the dependency |
| OTLP logs | `go.opentelemetry.io/proto/otlp/logs/v1` + `google.golang.org/protobuf` (`proto`, `protojson`) | Message types only. Do **not** import `collector/logs/v1`: it pulls in gRPC and grpc-gateway (see 5.8) |
| Images | `image/*`, `golang.org/x/image/draw`, `golang.org/x/image/webp` | Decode, downscale, re-encode JPEG |
| QR codes | `rsc.io/qr` (already used by grafit) | Phone pairing |
| Config | YAML, `gopkg.in/yaml.v3` (as piCVert), plus flags and env | |
| HTTP | `net/http` with Go 1.22+ patterns, no router library | Build the mux once in `New` (see the grafit gotcha) |
| Own logging | `log/slog` | |
| Frontend | TypeScript (strict), **plain DOM in classes, no framework**, bundled by Vite into `internal/web/assets/static/app/main.js` and embedded by Go | Same setup as grafit |
| Frontend tests | `vitest` and `happy-dom` (two extra dev dependencies) | Pure logic, plus renderer tests in vitest's `happy-dom` environment |
| CSS | Hand-written, custom properties, no CSS framework | Same as grafit |

Ask in `DECISIONS.md` before adding any other dependency, on either side.

### 2.1 Protocols (Decided)

Every link uses a published protocol where one exists. The only in-house
formats are the convenience endpoints, and each has a standard alternative.

| Link | Protocol | Notes |
|---|---|---|
| Agents to cafard | **MCP** over streamable HTTP and stdio | Official Go SDK |
| Log ingest, pipelines | **OTLP/HTTP logs** (`POST /v1/logs`, protobuf and JSON) | Vector, Fluent Bit, OTel Collector, OTel SDKs send to it unchanged |
| Log ingest, devices | **Syslog**: RFC 5424, RFC 3164, RFC 6587 framing over TCP | |
| Kernel log | **netconsole**, plain and extended format | Linux kernel documentation, not an RFC |
| Serial over network | **RFC 2217** (Telnet COM Port Control); raw TCP as fallback | Remote baud rate, DTR/RTS, break |
| Uploads | HTTP `multipart/form-data` (RFC 7578) | |
| Live browser feed | **Server-Sent Events** (WHATWG HTML) | |
| Phone share menu | **Web Share Target** (W3C) | Android Chrome only |
| Phone notifications (M7) | **Web Push** (RFC 8030) with **VAPID** (RFC 8292) | |
| Auth | Static bearer tokens (RFC 6750 header) now; **OAuth 2.1** as in the MCP authorization spec later (M8, optional) | See section 10 |
| Convenience | `POST /api/log` (text or NDJSON) | For `curl` and shell scripts; OTLP is the standard path |

**Agent-to-agent messaging is not A2A, on purpose.** A2A (Agent2Agent,
Linux Foundation) assumes each agent runs a server that others call. The
agents cafard serves (Claude Code, Codex, other terminal clients) are MCP
*clients* and cannot be called, so the only design open to them today is a
shared mailbox reached through MCP tools. Message content is plain text plus
images, which ties nothing to cafard. An A2A gateway that exposes cafard as an
A2A agent is a possible extension, out of scope until asked for.

---

## 3. Repository layout

```
cafard/
  cmd/cafard/main.go            subcommands: serve, push, term, mcp, token, config, version
  internal/config/              YAML + env + flags, validation (`cafard config` prints/validates)
  internal/deps/                the Deps struct and cross-track interfaces (CONTRACTS.md 1.2, 1.9)
  internal/dto/                 every JSON type, shared by web API, SSE, MCP, fixtures
  internal/status/              in-memory source status registry (CONTRACTS.md 1.6)
  internal/store/               SQLite schema, migrations, queries, retention janitor
  internal/blob/                content-addressed files, image variants
  internal/hub/                 in-memory fan-out of committed entries (wait_for, SSE)
  internal/writer/              single writer goroutine, batching, seq assignment
  internal/textclean/           line splitting, ANSI strip, dedupe, truncation, the agent
                                output formatter (CONTRACTS.md section 1)
  internal/ingest/httpapi/      POST /api/log
  internal/ingest/syslog/       UDP + TCP, RFC 3164 / 5424 / 6587
  internal/ingest/netconsole/   UDP, extended headers
  internal/ingest/otlp/         POST /v1/logs, protobuf + JSON, gzip
  internal/ingest/serial/       local tty, RFC 2217, raw TCP (ser2net, hub4com), reconnect
  internal/ingest/serial/rfc2217/  Telnet COM Port Control client
  internal/messaging/           messages, channels, follows, acks, claims, timeline union,
                                attachments, ask/reply, rate limits
  internal/mcpserver/           MCP tools, resources, instructions
  internal/auth/                principals, tokens, sessions, pairing codes, CSRF (Sec-Fetch-Site)
  internal/push/                client side of `cafard push` (batching, spool, file follow)
  internal/web/pages/           html/template page handlers
  internal/web/api/             JSON API, POST /api/messages (multipart)
  internal/web/sse/             GET /api/events
  internal/web/assets/          embed of templates and static
  internal/web/assets/templates/ *.html (server-rendered shells)
  internal/web/assets/static/   app.css, manifest.webmanifest, sw.js, icons/
  internal/web/assets/static/app/ Vite output (main.js ignored, README.txt committed)
  src/                          TypeScript sources (section 9)
  deploy/                       install.sh, cafard.service, cafard-container.conf,
                                cafard.openrc, cafard.yaml.example, README.md
  scripts/remote.sh             rsync to dev-build, run checks there
  scripts/devdeploy.sh          build on dev-build, push binary to the appliance
  .github/workflows/ci.yml      frontend -> artifact -> Go checks, cross-build, shellcheck
  .github/workflows/release.yml tag v* -> binaries + SHA256SUMS + GitHub release
  package.json  tsconfig.json  vite.config.ts  vitest.config.ts
  go.mod  .gitignore  README.md  AGENTS.md  PLAN.md  CONTRACTS.md  DECISIONS.md
```

Every feature package exposes `Register(mux, mcp, deps)` (CONTRACTS.md 1.2)
(or the MCP equivalent) and owns its config sub-struct, so adding a
feature adds one line to `main.go` and one to `internal/config`. Package
ownership and the Go interfaces between packages are in CONTRACTS.md
section 1.

The Go module path is `github.com/nebuloss/cafard` (Default). The remote is
`git@github.com:nebuloss/cafard.git`, as for the other projects. grafit's
module path does not match its remote. Do not copy that mismatch.

---

## 4. Data model

### Concepts

- **Principal**: whoever talks to cafard. Every token belongs to one
  principal: `name` (unique slug), `kind` (`agent` | `human` | `device`),
  `created_at`, `last_seen`. Scopes live on tokens, and are copied into
  `sessions.scopes` when a session is created (sign-in or pairing). One
  token per agent instance (`claude-laptop`, `codex-build`) is how agents
  get distinct identities.
- **Stream**: one source of entries. Its id is `kind:name`, and every
  stream but a direct conversation belongs to a project (section 4,
  "Projects, sinks and grants"):
  - `serial:<project>/<sink>`, `syslog:<project>/<sink>`,
    `netconsole:<project>/<sink>` for devices.
  - `push:<project>/<sink>` for the push client, `otlp:<project>/<sink>`
    for OTLP.
  - `chan:<project>/<name>` for message channels; every project has
    `chan:<project>/general`.
  - `dm:<a>~<b>` for one direct conversation: the two principal names,
    sorted, joined by `~` (`dm:claude-laptop~guillaume`).

  A stream also has `labels` (JSON, per stream only), `last_seq`, and an
  optional retention override. Per-entry data goes in the entry's `meta`.
  Stream status is `connected | idle | down`: idle after 5 minutes with no
  entry; down when a serial port is disconnected or a TCP peer is lost.
  UDP, HTTP and OTLP sources are never down, only idle.
- **Channel**: a chat, and the main unit of work (section 6.1). It
  belongs to a project and has a name, a title, a topic, and **bound
  streams**: the device logs of its project it is about, given as stream
  ids or globs (`serial:lab/dut1`, `syslog:lab/dut1*`). A channel's
  messages live in stream `chan:<project>/<name>`. Its bindings decide which
  logs its log pane shows, which markers show up in the chat, and which
  streams agents read by default when they pass `channel`.
- **Follow**: a principal following a chat (`chan:<name>` or
  `dm:<a>~<b>`), with its pin and mute state. Broadcasts reach the inbox
  only from followed channels (section 6).
- **Entry**: one item in the timeline.
  - `seq`: global, strictly increasing, assigned by the writer. It is the
    single cursor agents use across every stream.
  - `ts` (receive time, UTC ns), `src_ts` (time claimed by the source).
  - `stream`, `kind`, `severity` (0-7 as in syslog; default 6), `host`, `app`,
    `text`, `attachments` (JSON array of typed attachments, section 6.2):
    `{"type":"image","sha256":...}` or
    `{"type":"log","stream":...,"from_seq":...,"to_seq":...,...}`.
  - Message-only fields: `sender` (principal name), `recipient` (principal
    name, `@humans`, or NULL for everyone on the channel), `reply_to` (seq).
  - `meta` (JSON, optional, at most 4 KiB): `{"choices":["done","can't"]}`
    for a question, `choice` on a reply, the syslog facility, OTLP
    attributes and trace ids. All per-entry data goes here.
  - `kind` is one of:
    - `line`: one log line.
    - `marker`: written by cafard or an agent, e.g. "port reconnected".
    - `message`: from a principal. A human's photo drop is a message too.
- **Ack**: per-principal read state for messages. With several agents,
  "read" cannot be a single column.
- **Claim**: a lease on a stream id (e.g. `serial:dut1`; claim resources
  are stream ids only) held by a principal until a deadline. Agents use claims to avoid driving the same
  board at the same time, and `serial_write` requires one.
- **Blob**: a stored file keyed by sha256, plus derived image variants.

### Schema (end state)

This is the schema once every milestone is done. Each milestone adds what
it needs as a new numbered migration. A migration that has shipped (been
committed at a milestone end) is never edited.

```sql
CREATE TABLE principals (
  name TEXT PRIMARY KEY, kind TEXT NOT NULL,
  created_at INTEGER NOT NULL, last_seen INTEGER
);
CREATE TABLE tokens (
  id INTEGER PRIMARY KEY,
  hash TEXT NOT NULL UNIQUE,                 -- sha256 of the token, never the token
  principal TEXT NOT NULL REFERENCES principals(name) ON DELETE CASCADE,
  label TEXT,
  scopes TEXT NOT NULL,                      -- space separated
  created_at INTEGER NOT NULL, last_used INTEGER
);
CREATE TABLE sessions (                      -- browser sessions, hashed like tokens
  id INTEGER PRIMARY KEY,
  hash TEXT NOT NULL UNIQUE,
  principal TEXT NOT NULL REFERENCES principals(name) ON DELETE CASCADE,
  label TEXT, user_agent TEXT,
  scopes TEXT NOT NULL,                      -- copied from the token or pairing
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL
);
CREATE TABLE pairings (                      -- one-time QR codes (section 10)
  code_hash TEXT PRIMARY KEY,
  principal TEXT,                            -- existing human, or NULL
  new_name TEXT,                             -- name typed by the admin for a new human
  scopes TEXT NOT NULL,
  expires_at INTEGER NOT NULL, used_at INTEGER
);
CREATE TABLE streams (
  id TEXT PRIMARY KEY, kind TEXT NOT NULL, labels TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL, last_seq INTEGER NOT NULL DEFAULT 0,
  retention TEXT
);
CREATE TABLE entries (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL, src_ts INTEGER,
  stream TEXT NOT NULL REFERENCES streams(id), -- chan:<name>, dm:<a>~<b>, or a device
  kind TEXT NOT NULL, severity INTEGER NOT NULL DEFAULT 6,
  host TEXT, app TEXT, text TEXT NOT NULL,
  attachments TEXT,                          -- JSON array, section 6.2
  sender TEXT,
  recipient TEXT,                            -- principal (DM), @humans, or NULL (broadcast)
  reply_to INTEGER, meta TEXT
);
CREATE INDEX entries_stream_seq ON entries(stream, seq);
CREATE INDEX entries_messages ON entries(recipient, seq) WHERE kind = 'message';
CREATE INDEX entries_reply ON entries(reply_to) WHERE reply_to IS NOT NULL;
CREATE VIRTUAL TABLE entries_fts USING fts5(text, content='entries', content_rowid='seq');
-- triggers keep entries_fts in sync on insert and delete
CREATE TABLE acks (
  principal TEXT NOT NULL, seq INTEGER NOT NULL, at INTEGER NOT NULL,
  PRIMARY KEY (principal, seq)
) WITHOUT ROWID;
CREATE TABLE claims (
  resource TEXT PRIMARY KEY,                 -- a stream id
  principal TEXT NOT NULL,
  note TEXT, expires_at INTEGER NOT NULL
);
CREATE TABLE channels (
  name TEXT PRIMARY KEY,                     -- <project>/<slug>; messages in stream chan:<name>
  title TEXT, topic TEXT,
  surface TEXT NOT NULL DEFAULT 'markers',   -- none | markers | errors (section 6.1)
  created_by TEXT NOT NULL, created_at INTEGER NOT NULL,
  last_activity INTEGER NOT NULL, archived_at INTEGER,
  source TEXT NOT NULL DEFAULT 'runtime'     -- runtime | config (config ones cannot be deleted)
);
CREATE TABLE channel_streams (
  channel TEXT NOT NULL REFERENCES channels(name) ON DELETE CASCADE,
  pattern TEXT NOT NULL,                     -- stream id or glob; may match streams not seen yet
  PRIMARY KEY (channel, pattern)
) WITHOUT ROWID;
CREATE TABLE follows (
  principal TEXT NOT NULL REFERENCES principals(name) ON DELETE CASCADE,
  chat TEXT NOT NULL,                        -- chan:<name> or dm:<a>~<b>; no FK, the
                                             -- janitor removes rows of deleted chats
  pinned INTEGER NOT NULL DEFAULT 0, muted INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (principal, chat)
) WITHOUT ROWID;
CREATE TABLE blobs (
  sha256 TEXT PRIMARY KEY, mime TEXT NOT NULL, size INTEGER NOT NULL,
  width INTEGER, height INTEGER, created_at INTEGER NOT NULL,
  agent_sha256 TEXT, thumb_sha256 TEXT
);
CREATE TABLE projects (                      -- section 4, "Projects, sinks and grants"
  name TEXT PRIMARY KEY,                     -- slug, embedded in stream ids, never renamed
  title TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
  lines_max_age INTEGER, lines_max_bytes INTEGER,   -- ns and bytes, 0 = no limit;
  messages_max_age INTEGER, blobs_max_bytes INTEGER -- NULL = take the config value at start
);
CREATE TABLE sinks (
  id INTEGER PRIMARY KEY,
  project TEXT NOT NULL REFERENCES projects(name) ON DELETE CASCADE,
  name TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
  UNIQUE (project, name)
);
CREATE TABLE sink_sources (                  -- syslog and netconsole senders
  sink INTEGER NOT NULL REFERENCES sinks(id) ON DELETE CASCADE,
  prefix TEXT NOT NULL,                      -- an IP or a CIDR, masked
  PRIMARY KEY (sink, prefix)
) WITHOUT ROWID;
CREATE TABLE sink_serial (                   -- at most one port per sink
  sink INTEGER PRIMARY KEY REFERENCES sinks(id) ON DELETE CASCADE,
  device TEXT, rfc2217 TEXT, tcp TEXT,       -- exactly one is set
  baud INTEGER, data_bits INTEGER, parity TEXT, stop_bits REAL,
  writable INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE grants (
  id INTEGER PRIMARY KEY,
  principal TEXT NOT NULL REFERENCES principals(name) ON DELETE CASCADE,
  project TEXT NOT NULL REFERENCES projects(name) ON DELETE CASCADE,
  channel TEXT,                              -- NULL = the whole project, else a channel name
  scopes TEXT NOT NULL                       -- read message ingest serial_write
);
CREATE UNIQUE INDEX grants_target ON grants(principal, project, coalesce(channel, ''));
CREATE TABLE config_imports (key TEXT PRIMARY KEY, at INTEGER NOT NULL); -- yaml imported once
-- tokens gain sink (a sink token writes only there); pairings gain grants.
CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL);
```

Migrations are numbered SQL strings applied in a transaction at startup.
Each one must be idempotent, like grafit's: "no migration step" on upgrade.

The database file is chmodded to 0600 after open, and a test checks the mode
(grafit gotcha: SQLite creates the file with the process umask).

### Write path

- **Single writer for entries.** Only the writer goroutine writes
  `entries` (and `entries_fts`). Ingesters, messaging and MCP send entries
  to it over a buffered channel.
- **Other tables** (`principals`, `tokens`, `sessions`, `pairings`,
  `channels`, `channel_streams`, `follows`, `acks`, `claims`, `blobs`) are
  written directly, each action in its own short transaction. Any marker
  such an action produces (a claim, a binding change) goes through the
  writer.
- **Batching.** The writer commits in batches: every 50 ms or every 500
  entries, whichever comes first.
- **Publish after commit.** Only then does it publish the committed entries,
  with their `seq`, to the hub. So `seq` order is commit order, and a
  subscriber never sees an entry that is not yet in the database.
- **Synchronous writes.** `send` and `mark` must return the new `seq`, so they
  use a request/response variant (an entry plus a reply channel).
- **Back-pressure.** When the channel is full:
  - Lossy sources (syslog UDP, netconsole) drop the entry and count the loss.
  - OTLP waits up to 2 s for the writer, then answers 503 with
    `Retry-After`.
  - Everything else blocks.

  Loss counters appear in `list_streams` and on the Sources page.

### Hub

In-memory publish/subscribe.

- A subscriber registers a filter: streams, kinds, minimum severity, a
  compiled RE2 pattern, and a recipient.
- A subscriber whose buffer fills is dropped and told so. It then re-reads
  from the store by cursor. The writer never blocks on a subscriber.
- `wait_for`, `wait_message`, `ask_human` and SSE all use the hub.

### Retention (Default)

| Limit | Default |
|---|---|
| Log entries, age | 14 days |
| Log entries, total size | 2 GB |
| Messages, age | 90 days (they are small and valuable) |
| Blobs, total size | 5 GB |

- Entries in channel and `dm:*` streams follow message retention. Device
  streams follow line retention, markers included. The limits are per
  project; this table is the default of a new project and the limit of
  what is outside projects (section 4, "Projects, sinks and grants").
- Per-stream overrides are allowed in the config (`retention.streams`,
  section 11).
- A janitor runs every 10 minutes:
  1. Delete by age.
  2. Delete oldest first until under the size limit.
  3. Delete acks of deleted entries.
  4. Delete blobs nothing references any more (images and log snapshots).
  5. Delete expired claims (each writes a "claim expired" marker through
     the writer), expired sessions and pairings, and `follows` rows of
     deleted chats.

### Projects, sinks and grants

cafard is organised like Discord: **projects** hold the chats and the log
**sinks** of one piece of work, and the admin decides who sees what with
**grants**. The point is to keep, per project, the history of the data sent
and of what the agents replied. Decisions and their reasons are in
DECISIONS.md ("Projects, sinks and grants").

- **Project.** A row of `projects`: `name` (a slug with the charset of
  principal names, `[a-z0-9][a-z0-9._-]{0,39}`; stream ids embed it, so it
  never changes), `title`, `created_at`, and its retention:
  `lines_max_age`, `lines_max_bytes`, `messages_max_age`,
  `blobs_max_bytes`, where 0 means forever or no limit. A new project
  copies the `retention:` config values. Every project has a `#general`
  channel, made with it.
- **Stream ids.** `chan:<project>/<channel>` for a channel,
  `<kind>:<project>/<sink>` for a sink's logs, one stream per transport:
  `serial`, `syslog`, `netconsole`, `push` (`POST /api/log`, `cafard
  push`) and `otlp`. `dm:<a>~<b>` stays outside projects. A glob star
  stops at `/`, so `chan:lab/*` is the channels of `lab` and `*:lab/*` its
  log streams, while "every channel" is `chan:*/*`. The source host and
  service stay in each entry's `host` and `app`.
- **Sink.** A named log source owned by a project (`sinks`: project,
  name with the principal charset, title). Logs reach it three ways, all
  managed on the admin interface and stored in the database:
  1. **Tokens.** A sink token (kind `device`, scope `ingest` only) writes
     only into its sink, whatever stream it names: `POST /api/log` and
     `cafard push` land in `push:<project>/<sink>`, OTLP in
     `otlp:<project>/<sink>`. A token whose principal has an `ingest`
     grant on a project may instead name the sink: `stream=<sink>` or
     `<project>/<sink>` (a kind prefix is ignored), and OTLP uses
     `service.name`, else `host.name`, as the sink name.
  2. **Source addresses.** A sink lists IP addresses and CIDRs
     (`sink_sources`). A syslog or netconsole datagram from one of them
     lands in `syslog:<project>/<sink>` or `netconsole:<project>/<sink>`;
     the most specific prefix wins, then the oldest sink. This replaces
     `netconsole.names`; `syslog.allow` still filters first.
  3. **Serial port.** At most one per sink (`sink_serial`: local `device`,
     `rfc2217` or raw `tcp`, line settings, `writable`), read into
     `serial:<project>/<sink>`. The serial manager reconciles its running
     ports whenever a sink is created, changed or deleted.

  **Logs that match no sink are dropped**, never stored: syslog and
  netconsole from unknown addresses, and HTTP or OTLP from tokens with no
  sink and no matching ingest grant. Each drop counts in status per
  transport (shown to the admin on Sources and in `GET /api/streams`),
  with a debug log line at most once a minute per transport. `POST
  /api/log` answers 200 with `accepted: 0` and `dropped`; OTLP answers a
  `partial_success` that rejects them.

  Deleting a sink stops its port, revokes its tokens and keeps its history,
  which ages out by the project's retention.
- **Grants.** `grants(principal, project, channel, scopes)`: `channel` NULL
  grants the whole project, else one of its channels. Scopes are `read`,
  `message`, `ingest` (project grants only) and `serial_write`. The rules:
  - A principal with no grant sees nothing: no channel, stream, message,
    blob or other principal. The admin session sees everything.
  - Credentials keep their scopes as a **cap**: what a token, a browser
    session or a pairing may do anywhere is the intersection of its scopes
    and the grant of the thing it touches. `tools/list` still follows the
    token's scopes.
  - A project grant covers every channel and log stream of the project.
  - A channel grant covers that channel's messages, and the log streams
    its bindings match (read them, `mark` and `claim` them with
    `message`, `serial_write` on a bound serial stream). Bindings stay
    inside the channel's project; changing them, and creating, renaming,
    archiving or deleting channels, needs `message` on the whole project,
    so a channel grant cannot widen itself.
  - A direct conversation is visible to its two parties only. Sending one
    needs the two principals to share a project (any grant in it), or one
    of them to be the admin. `list_principals` lists the principals that
    share a project with the caller, and `admin`.
  - `@humans` in a channel reaches the humans who can read it.
  - A sink token has no grant and reads nothing.
- **People.** A pairing link carries grants (`project[:channel]=scopes`);
  redeeming it adds them to the person, merging with what they have.
  The project page makes such links for itself.
- **Enforced everywhere data is read or written:** the MCP tools,
  resources and their notifications, `/api/*`, SSE (the hub's viewer
  filter and the handler both check, as for messages), the chat, Live and
  Sources pages, blobs, and log attachments: an attachment's lines, preview
  and snapshot show only when the reader can read every stream it covers;
  otherwise the card says it is hidden.
- **Retention per project.** The janitor applies each project's limits to
  its streams: `messages_max_age` to `chan:<project>/*`, `lines_max_age`
  to its log streams (a `retention.streams` override still wins),
  `lines_max_bytes` to the total of its log streams, and `blobs_max_bytes`
  to the blobs its messages reference. The `retention:` config values are
  the defaults of new projects and the limits of everything outside
  projects (direct messages and their images, and any stream without a
  project).
- **Upgrading an install.** Migration `0070` creates the tables and moves
  every existing channel and log stream into a project `imported`
  (`serial:dut1` becomes `serial:imported/dut1`, `chan:dut1`
  `chan:imported/dut1`, with bindings, follows, claims and log attachments
  rewritten), makes a sink for each old stream name that is a valid sink
  name (with its address when the name is an IP), and grants every
  existing principal the whole project with the scopes its tokens and
  sessions had, so nothing is lost and nobody is locked out. At start,
  the config's `serial` list and `netconsole.names` are imported once into
  `imported` as sinks, with a warning to remove them from the file.

---

## 5. Producers (ingest)

Every ingester normalises input through `textclean`:

- Decode as UTF-8 and replace invalid bytes with U+FFFD.
- Split lines on `\n`, `\r\n` and a lone `\r`. Serial consoles use `\r` alone.
- Flush a partial line after 500 ms of silence, so a prompt such as
  `login: ` is not stuck in a buffer.
- Store lines raw, ANSI codes included. Stripping happens at read time for
  agents; the browser renders the colours.
- Split lines longer than 16 KiB, with a continuation marker.

### 5.1 HTTP log endpoint

`POST /api/log?stream=<name>&host=&app=&severity=`

- **Body types:**
  - `text/plain`: one line or many.
  - `application/x-ndjson`: one object per line,
    `{text, ts?, severity?, host?, app?, stream?}`.
- **Stream id:** `push:<project>/<sink>`: the token's sink, or the sink
  `stream` names in a project where the principal has an `ingest` grant.
  Lines that reach no sink are dropped and counted.
- **Auth:** a bearer token with the `ingest` scope.
- **Response:** `{"accepted": n, "last_seq": s, "dropped": d}`.

### 5.2 Message upload endpoint

A drop is a message from a `human` principal with optional images. The UI
that posts here is described in section 9.6.3; the rules are in section 6.

`POST /api/messages` (multipart, handled by `internal/web/api`) takes these
fields:

- `text` (at most 32 KiB)
- `channel` (default `general`)
- `to` (optional): a principal for a direct message, or `@humans`, with
  the same rules as MCP `send` (section 6)
- `reply_to` (optional seq)
- `choice` (optional): the answer to a question, with `reply_to` set to
  the question's seq
- `logs` (optional): a JSON string, the same shape as MCP `send.logs`
- `files[]`

Limits: 20 MiB per file, 10 files per message. Accepted types are png, jpeg,
webp, gif and heic. It accepts a bearer token with `message` scope or a
session. Field names, the answer and errors are in CONTRACTS.md section 3.

HEIC cannot be decoded without cgo. It is stored, marked "no preview", and
the agent is told so. The browser normally converts it to JPEG before
upload, so this case is rare.

The same endpoint must also accept a plain HTML form post with no JavaScript
(see section 9.1), and answers it with a 303 redirect back to the chat.

### 5.3 Syslog

- **Listen:** UDP and TCP on `:5514`. Port 514 needs privileges, so it is
  opt-in (section 12.5).
- **Parsing:** RFC 5424 and RFC 3164, best effort. If parsing fails, keep the
  whole line as text with severity 6.
- **TCP framing:** octet counting (RFC 6587) or newline-delimited, detected
  per connection from the first byte.
- **Stream id:** `syslog:<project>/<sink>`, the sink whose source
  addresses hold the sender's IP; nothing is stored from other addresses.
  The hostname goes into `host`, the facility into the entry's `meta`.
- **Allowlist:** optional source CIDRs.

### 5.4 Netconsole

- **Listen:** UDP on `:6666`.
- **Format:** extended netconsole headers (`<level>,<seq>,<ts>,<flags>;msg`)
  are parsed when present.
- **Stream id:** `netconsole:<project>/<sink>`, by source address as for
  syslog. The old `netconsole.names` map is imported into sinks once.
- **README:** document the kernel command line,
  `netconsole=@/,6666@<server-ip>/`.

### 5.5 Serial (server side)

```yaml
serial:
  - name: dut1
    device: /dev/ttyUSB0      # local port
    baud: 115200              # 8N1 unless data_bits / parity / stop_bits are set
    writable: false           # agents may write only if true (section 7)
  - name: dut2
    rfc2217: 10.0.50.30:7001  # ser2net with telnet + RFC 2217: line settings set from here
    baud: 115200
  - name: dut3
    tcp: 10.0.50.30:7002      # raw TCP: line settings fixed on the far end
```

**RFC 2217** is the preferred way to reach a port on another machine:

- On connect, negotiate the Telnet `COM-PORT-OPTION` (44), then send
  `SET-BAUDRATE`, `SET-DATASIZE`, `SET-PARITY` and `SET-STOPSIZE` from the
  config. Also negotiate `BINARY` and `SUPPRESS-GO-AHEAD` in both directions.
- Escape `0xFF` (IAC) in data sent, and unescape it in data received. Strip
  every Telnet command from the data before it reaches `textclean`.
- Support `SET-CONTROL` for DTR on/off, RTS on/off and break on/off. That
  is what `serial_control` (section 7) uses.
- Ignore `NOTIFY-LINESTATE` and `NOTIFY-MODEMSTATE` except for logging
  carrier changes as markers.
- A peer that refuses option 44 is treated as plain Telnet: data still
  flows, `serial_control` is refused, and a marker says why.
- Server side reference: ser2net with `telnet(rfc2217)` in its accepter.
  Document a ser2net YAML example in the README.

What each kind of port supports:

| | local `device` | `rfc2217` | raw `tcp` |
|---|---|---|---|
| read, write | yes | yes | yes |
| baud and framing from cafard config | yes | yes | no, set on the far end |
| DTR, RTS, break | yes (`go.bug.st/serial`) | yes (`SET-CONTROL`) | no |

- When a USB port disappears or a TCP peer drops, reconnect with backoff from
  1 s to 30 s.
- Write a `marker` entry on disconnect and on reconnect, so agents see the
  gap.
- Stream id: `serial:<project>/<sink>`. Ports are defined per sink on the
  admin interface; the `serial:` config list is imported once into the
  project `imported`.
- Prefer stable device paths (`/dev/serial/by-id/...`) and say so in the
  README.

### 5.6 `cafard push` (client, Linux and Windows)

The same binary runs as a client. On Windows it is `cafard.exe`.

```
cafard push --server URL --token T --stream NAME [source]
  (no source)                       read stdin       journalctl -f | cafard push ...
  --serial COM5 --baud 115200       read a serial port directly
  --file C:\logs\putty.log --follow follow a growing file (PuTTY session log)
  --echo                            also print what is read to the terminal
```

- Send NDJSON to `/api/log` in batches: every 200 ms or 1000 lines. Keep the
  local receive time of each line in `ts`.
- If the server is unreachable, spool to a local file (cap 100 MiB) and
  replay it in order when the server is back.
- With `--file --follow`, survive truncation and rotation: notice when the
  size shrinks or the file is replaced, and reopen it.
- `--token` may also come from `CAFARD_TOKEN`, and `--server` from
  `CAFARD_SERVER`.

### 5.7 `cafard term` (Default: milestone 6)

Windows lets only one program open a COM port. `cafard term COM5` replaces
PuTTY:

- It shows the port's output and sends keystrokes to it.
- It pushes everything it receives to the server.
- It uses a raw console. `Ctrl-]` quits.

If raw console handling on Windows turns out to be unreasonable, ship
`push --serial` alone and document com0com + hub4com as the workaround.

### 5.8 OpenTelemetry logs (OTLP/HTTP)

`POST /v1/logs`, as in the OTLP specification, so any OpenTelemetry
exporter can send logs by pointing its endpoint at cafard.

- **Encodings:** `application/x-protobuf` and `application/json`. Accept
  `Content-Encoding: gzip`. Answer in the encoding of the request.
- **Decoding without gRPC:** `ExportLogsServiceRequest` and `LogsData` are
  the same message on the wire (field 1, `repeated ResourceLogs`). Decode
  into `logsv1.LogsData` with `proto.Unmarshal` or `protojson.Unmarshal`,
  and encode the response by hand: an empty message, or one with
  `partial_success` (field 1: `rejected_log_records` int64 = 1,
  `error_message` string = 2) when some records were refused. A test
  checks both against bytes produced by the reference types.
- **JSON caveat:** OTLP JSON writes `traceId` and `spanId` as hex, where
  `protojson` expects base64. Clear those two fields before decoding, or
  pre-process them, and keep them in `meta` in hex.
- **Mapping to entries:**
  - `body` (string, or any other value rendered as compact JSON) becomes
    `text`.
  - `severity_number` maps to syslog severity: 1-4 TRACE and 5-8 DEBUG
    to 7, 9-12 INFO to 6, 13-16 WARN to 4, 17-20 ERROR to 3, 21-24 FATAL
    to 2. If the number is unset, parse `severity_text`.
  - `time_unix_nano` becomes `src_ts`, falling back to
    `observed_time_unix_nano`.
  - Stream id: `otlp:<project>/<sink>`: the token's sink, else the sink
    named by `service.name`, else `host.name`, in a project where the
    principal has an `ingest` grant; records that reach no sink are
    rejected in `partial_success`. `host.name` becomes `host` and `service.name` becomes
    `app`.
  - Resource and record attributes, trace id and span id are kept in the
    entry's `meta`, capped at 4 KiB of JSON.
- **Auth:** bearer token with the `ingest` scope (OTel exporters set
  headers from `OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer cfd_..."`).
- **Limits:** request body 8 MiB after decompression; status 413 above.
  When the writer is saturated, wait up to 2 s, then answer 503 with
  `Retry-After`, as the spec describes for throttling.
- **Out of scope:** OTLP gRPC (port 4317), traces and metrics. Answer 404
  on `/v1/traces` and `/v1/metrics`.

README examples: a Vector sink (`type = "opentelemetry"`), a Fluent Bit
`opentelemetry` output, and the OTel Collector `otlphttp` exporter.

---

## 6. Messaging (agents, humans, and both)

### Principals and addressing

- Every message has a `sender`, which is the authenticated principal. It
  cannot be spoofed: it comes from the token or session, never from the
  request body.
- `recipient` can be:
  - a principal name (direct);
  - `@humans` (every human principal);
  - NULL (everyone reading the channel).
- A channel's messages are in stream `chan:<name>`. Each direct
  conversation is its own stream `dm:<a>~<b>` (the two principal names
  sorted, joined by `~`), visible only to its two parties, admin included.
- `send` and `POST /api/messages` pick the stream from `to`:
  - `to=<principal>`: a direct message in `dm:<me>~<principal>`;
    `channel` is ignored.
  - `to="@humans"`: a message in `channel` (default `general`) with
    recipient `@humans`.
  - no `to`: a broadcast in `channel`.
- `reply_to` links a reply to a message. Threads are flat: a reply to a reply
  points at the root.

### Inbox semantics

The inbox of principal P holds the messages where all of these are true:

- P is the recipient, or the recipient is `@humans` and P is human, or the
  recipient is NULL on a channel P **follows**;
- the sender is not P;
- P has no ack for the message.

Every reader has its own acks, so two agents both see a human's photo.

A channel is readable by the principals with a `read` grant on it or on
its project (section 4, "Projects, sinks and grants"). Following only
decides what reaches the inbox; it is not access control. Following
happens automatically:

- an agent follows a channel when it creates it, sends to it, or reads it
  with `channel=`;
- humans follow every channel by default, and can mute or unfollow one in
  the UI;
- `follow` and `unfollow` tools exist for explicit control.

### 6.1 Channels bound to streams (chats as workspaces)

Work happens per device or per task ("debugging dut1", "test run 42"), so a
channel can carry the logs it is about.

- **Bindings.** A channel has zero or more patterns: stream ids or globs
  (`serial:dut1`, `netconsole:dut1`, `syslog:dut1*`).
  - A glob may match streams that do not exist yet: a device that has never
    logged starts appearing as soon as it does.
  - Patterns are matched with `path.Match` semantics on the stream id.
  - `#general` has no bindings.
- **Timeline.** A channel's chat shows its own messages and, depending on
  `surface`, entries from its bound streams as compact **event rows**:
  - `none`: messages only;
  - `markers` (default): plus markers from the bound streams (claims,
    releases, `serial_write` and `serial_control` actions, `mark`,
    port disconnects and reconnects);
  - `errors`: plus lines of severity error and worse (0-3).

  The union is done when the timeline is read, never by copying entries, so
  bindings can change afterwards and the history follows.
- **Agents** pass `channel` instead of listing streams. One rule for every
  tool:
  - log tools (`read`, `tail`, `wait_for`, `list_streams`, `list_claims`)
    resolve `channel=` to its bound streams only;
  - `search(channel=)` and `channel_timeline` also include the channel's
    own messages.

  An agent working on dut1 needs to know one name.
- **Creation.**
  - From the web UI, with the "+" in the channel list.
  - By agents with `create_channel`, for example one channel per test run
    (`run-42`, bound to the device under test).
  - From the config (`channels:` in section 11). Config channels are
    reapplied at every start and cannot be renamed or deleted; their
    title, topic, bindings and surface can be edited, and the edit lasts
    until the next start. The config may re-declare `general` to set its
    title and topic only.
  - Creating channels is rate limited to 10 per hour per principal.
- **Archiving.** A runtime channel with no new message and no new line on
  its bound streams for 14 days (Default) is archived automatically.
  Archived channels leave the channel list but stay readable and searchable,
  and posting to one unarchives it. Messages still follow the normal
  retention.
- **Rights.** Any principal with the `message` scope can create a channel
  and change its bindings. Renaming (`update_channel(rename=)`), archiving
  and deleting (`delete_channel`) are for the creator or an admin, and for
  runtime channels only. Deleting a channel deletes its messages; the UI
  asks for confirmation first. Every binding change writes a marker into
  the channel (`codex-build bound serial:dut2`), so the history says why
  the logs pane changed.
- **Names.** Lowercase slug, 1 to 40 characters, `[a-z0-9-]`. `dm` and
  `general` are reserved names (`general` exists already).

### 6.3 Gallery channels (screenshots by section)

Agents that test a UI produce screenshots in batches; a conversation buries
them. A channel therefore has a **kind**:

- `chat` (the default): a conversation, as above.
- `gallery`: the same messages, shown as a grid. Every message is one
  **item**: an image (the first of the message) and a **title** (the first
  line of the text, at most 140 characters, possibly empty). A message to a
  gallery without an image is refused with an error that says why and
  points at a chat channel. Markers (a kind change, a binding change) stay
  in the gallery's conversation and are not items.

The kind is set at creation (`create_channel(kind=)`, the project page's
"New channel" form, `POST /api/channels`) and can be changed afterwards
(`update_channel(kind=)`, "Make it a gallery" / "Make it a chat" on the
project page; same rights as a binding change, and a marker records it).
`#general` stays a chat. Changing the kind changes only how the messages
are shown and the one rule above; nothing is rewritten.

- **Sections.** `send(section=)` groups a batch ("login flow v2"). It is
  stored in the message's `meta.section`, trimmed, at most 80 characters,
  without control characters or `[`, `]`, `|`. A section on a message to a
  chat channel or a direct message is refused. Items without a section are
  "Unsorted".
- **The page** `/projects/{project}/g/{gallery}`: section chips (All, then each
  section with its count, the one with the newest item first), and the
  items newest first, grouped under section headings in the same order.
  `?section=NAME` shows one section (`?section=` the unsorted items).
  Paging is by seq (`?before_seq=`, 60 items a page). Each tile is a link
  to the item's own page `/projects/{project}/g/{gallery}/{seq}`: the image (agent
  variant), its title and section, and newer/older within the section, so
  the gallery works without the bundle. With the bundle a tap opens the
  image viewer on the tiles of that section (previous and next stay in
  it), and the newest page follows the channel's feed: a new item goes
  first in its section, which moves to the top. The project's side list
  (section 9.6.3) lists its galleries with their image counts and leads
  to the grid; the grid links to the conversation and the conversation's
  header back to the grid. The older `/g/...` addresses redirect (308).
- **Agents** see the kind in `list_channels` (with the sections, newest
  first, and their counts) and `list_projects` (the galleries and their
  section names), and the server instructions ask them to post
  screenshots to the project's gallery, one image per message with a
  short title, a batch under one section.
- **API.** `GET /api/galleries/{project/name}?section=&before_seq=&limit=`
  returns a `GalleryPage` (CONTRACTS.md section 3).

### 6.4 Gallery artifacts in chat

A chat message can show gallery items without copying them, so a
conversation about a fix points at its screenshots, and the gallery stays
the one place they live (and age out by retention).

- **Stable links.** `/projects/<project>/g/<gallery>/<seq>` is one item;
  `/projects/<project>/g/<gallery>?section=<name>` a section;
  `/projects/<project>/g` the project's galleries (the "Galleries" heading
  of the project's side list). The `/g/<project>/...` addresses they had
  first redirect there (308, query kept), so links already written keep
  working.
- **Ref syntax** for agents and people writing text:
  `[[<project>/<gallery>#<seq>]]` for an item,
  `[[<project>/<gallery>|<section>]]` for a section. A link to an item or a
  section page (relative or with any host, of either address form) counts
  the same. The MCP `send`
  also takes `gallery_refs` (item seqs); so does the `POST /api/messages`
  form (`gallery_refs`, repeated or comma separated).
- **Checks at send.** Refs go in a chat channel's messages only (not a
  gallery's, not a direct message), at most 10 per message, and only to
  galleries of the same project. Each `gallery_refs` seq must be an image
  item of such a gallery that the sender can read, else the send fails
  with one sentence whatever the reason (missing, not an image, another
  project, no grant), so it tells nothing about what the sender cannot
  see. A ref or link in the text that fails the same checks stays plain
  text. The refs are stored in the message's `meta.refs`
  (`{gallery, seq}` or `{gallery, section}`); nothing is copied.
- **Per reader.** Each reader gets the refs resolved for itself
  (`Entry.gallery_refs`): an item with its title, section and thumbnail; a
  section with its first four images and its count. A ref to a gallery the
  reader cannot read is `{state: "unavailable"}` and nothing else; an item
  retention removed is `gone`. `meta.refs` never leaves the server, and
  the ref text that became a card is taken out of the text the web shows.
- **Chat.** A card per ref under the message: the thumbnail, the title and
  "#gallery / section" for an item; a strip and "N images in #gallery"
  for a section; "gallery item unavailable" or "no longer available"
  otherwise. Without the bundle a card is a link to the item or section
  page; with it a tap opens the viewer on the card's images.
- **Composer.** In a chat channel of a project with galleries the reader
  can see, "Attach from gallery" (bundle only) lists the galleries, a
  gallery's sections and images; picking writes the ref in the message,
  picking a section writes the section ref.
- **Agents.** `get_message` lists each ref, marked as a reference, with
  its image while the call is under its 6 images. Previews of a message
  with nothing but refs say "[N gallery images]".

### Questions to the human

`ask_human(question, choices?, channel?, timeout_s)` works like this:

1. It sends a message in `channel` (default `general`) with recipient
   `@humans` and `meta.choices`.
2. It returns `{seq}` immediately if `timeout_s` is 0.
3. Otherwise it waits for the first reply (`reply_to = seq`) from a human, up
   to `timeout_s`.

The web UI shows such messages as a card with one button per choice, plus a
free-text reply box. If the wait times out, the question stays open. The
agent can collect the answer later with `wait_message(reply_to=seq)` or from
`inbox`.

A question is open until the first reply (`reply_to` = its seq) from any
human principal. The reply carries `meta.choice` when a button was used.
The answer closes the question for every human. Later replies are allowed
but do not reopen it.

### Read receipts

Every message carries `seen_by[]`: the principals who acked it, the sender
excluded. Anyone who can see the message can see the list.

### Guards

- **Rate limit.** 30 messages per minute per principal, with a burst of 10
  (token bucket). This stops two agents replying to each other forever.
  Exceeding it returns an error that explains why.
- **Size.** 32 KiB of text per message. Images: 6 per message, 10 MiB each
  for agents.
- **Loop hint.** The MCP server instructions tell agents not to reply to
  acknowledgements, and not to send a message whose only purpose is to
  confirm receipt.

### 6.2 Log attachments (a stream slice attached to a message)

A message can carry **pieces of logs** as attachments, next to images, so
"look at this" points at the exact lines. A human selects lines in a logs
view and sends them to an agent; an agent reports a failure to the human or
to another agent with the lines that prove it.

**Shape:**

```jsonc
{
  "type": "log",
  "stream": "serial:dut1",          // sent by the client, or "channel": "dut1"
  "streams": ["serial:dut1"],       // set by the server: the resolved list
  "from_seq": 18230,
  "to_seq": 18302,                  // null = open-ended: "from here on"
  "grep": "panic|Call Trace",       // optional RE2, applied when read
  "min_severity": 3,                // optional
  "title": "panic on boot 3",       // optional, shown on the card
  "snapshot_sha256": "...",         // set by the server, see below
  "lines": 73, "first_ts": ..., "last_ts": ...   // set by the server
}
```

**Behaviour:**

- **A reference, frozen when small.**
  - The attachment points at a range of `seq` in a stream: nothing is
    copied into the message.
  - Closed ranges within both limits (at most 2000 lines and at most
    256 KiB) are also **snapshotted**
    at send time, as a cleaned text blob (`text/plain`, the section 7
    output format). The lines then outlive the stream's retention, which is
    shorter than the messages'.
  - Bigger ranges and open-ended ones stay references. Once retention has
    eaten the lines, the card says so.
- **Open-ended** (`to_seq: null`) means "from here on". It suits "watch this
  from now": the reader continues with `read(since_seq=from_seq)`.
- **Channel form.** `channel` instead of `stream` takes the channel's bound
  streams at send time. The server resolves them and stores the list in
  `streams`, so a later binding change does not alter what was attached.
- **Limits.**
  - At most 5 log attachments per message.
  - A range may hold at most 100 000 lines.
  - The stream must exist when the message is sent.
  - The server fills in `streams`, `lines`, `first_ts` and `last_ts`; the
    client never sends them.
- **Visibility** follows the message: a log attachment in a direct message
  is only shown to its two parties, even though the stream itself is
  readable by all.

### Claims

`claim(resource, ttl_s, note)`, `release(resource)` and `list_claims()`.
A resource is a stream id.

- A claim is held by one principal until its deadline. The holder may renew
  it. When it expires, a "claim expired" marker is written.
- `serial_write` refuses unless the caller holds the claim on that stream.
- Every claim and release also writes a marker into the resource's stream, so
  the timeline says who drove the board when.

---

## 7. MCP server (agents)

### Transports

- **Streamable HTTP** at `/mcp`. It takes bearer tokens only, never cookies,
  and validates `Origin` as the MCP spec requires (DNS rebinding). This is the
  main transport.
- **stdio**: `cafard mcp --server URL --token T` is a thin stdio MCP server
  that forwards every call to a remote cafard over HTTP. It never opens the
  database. It lets stdio-only clients use a cafard on another machine.
  It adds one argument of its own: `send` takes `image_paths`, local
  files the proxy reads, types by content and turns into `images`
  (at most 6 of 10 MiB), so an agent attaches a screenshot by path
  instead of writing base64. The paths never reach the server, and the
  HTTP `/mcp` refuses them: it cannot read the agent's disk. Every other
  argument passes as it came (`section`, `gallery_refs`, ...); a test
  sends a screenshot by path into a gallery section and refs to it
  through the proxy.
- **Shell**, not MCP: `cafard send [--channel C | --to P] [--reply-to SEQ]
  [--section NAME] [--image FILE]... [TEXT | -]` posts through
  `POST /api/messages` with the token (section 3.4 of CONTRACTS) and prints
  the seq and the message's link in the web UI (a gallery item's own page
  when it has a section), for agents that would rather run a command, and
  for scripts.

### Output rules (every tool that returns log text)

Agents pay for every token, so:

- **Clean:** strip ANSI escape sequences and control characters, except
  `\t`.
- **Collapse:** fold runs of identical lines (leading timestamps ignored)
  into one line plus `[repeated N times]`.
- **Truncate:** cut each line at 2000 characters and add `[...truncated]`.
- **Cap:** stop at 400 lines or 60 KB, whichever comes first. Always say
  whether more is available and give the cursor to continue from.
- **Format:** one compact line per entry,
  `<seq> <HH:MM:SS.mmm> <stream> <SEV> <text>`. Print the date once in a
  header, and again only when it changes.
- **Structured output:** also return structured content (`next_seq`,
  `has_more`, counts), using the SDK's output schemas.

### Tools: logs

| Tool | Arguments | Returns |
|---|---|---|
| `list_streams` | `kind?`, `channel?` | id, kind, labels, last_seq, last activity, lines/min over 5 min, dropped count, status (`connected \| idle \| down`), claim holder |
| `read` | `channel?` (its bound streams), `streams?` (default all log streams), `since_seq?` (default: last 100 lines), `until_seq?`, `limit?`, `min_severity?`, `grep?` (RE2) | lines, `next_seq`, `has_more` |
| `tail` | `channel?` (its bound streams), `streams?`, `lines?` (50) | the last N lines and `next_seq` |
| `wait_for` | `pattern` (RE2), `channel?` (its bound streams), `streams?`, `since_seq?` (default: now), `timeout_s?` (60, max 600), `context_lines?` (20) | on a match: the matching line, the lines before it, `match_seq`. On timeout: `timed_out`, the lines seen (capped), `next_seq` |
| `search` | `query` (FTS5), `channel?` (its messages and bound streams), `streams?`, `from?`, `to?` (RFC 3339 or relative like `-2h`), `since_seq?` (continue), `limit?` | matching lines in seq order, `next_seq`, `has_more` |
| `context` | `seq`, `before?` (20), `after?` (20) | lines around one entry, in its stream; on a message seq, message visibility applies |
| `mark` | `stream`, `label` | creates a marker and returns its `seq`, a cursor for "everything since I started test X" |
| `serial_write` | `stream`, `data`, `newline?` (`\r`) | bytes written. Refused unless all three hold: the stream is `writable` in the config, the token has the `serial_write` scope, and the caller holds the claim. Every write is recorded as a marker `<principal>> data`. |
| `serial_control` | `stream`, `dtr?` (bool), `rts?` (bool), `break_ms?`, `pulse?` (`{line: "dtr"\|"rts", ms}`), `baud?` | the resulting line state. Same three gates as `serial_write`. Works on local and `rfc2217` ports, refused on raw `tcp` with the reason. Used to reset boards or enter a boot mode when DTR/RTS are wired to reset or boot pins. Every action is recorded as a marker. |

### Tools: messages

| Tool | Arguments | Returns |
|---|---|---|
| `whoami` | | principal name, kind, scopes |
| `list_principals` | `kind?` | name, kind, last_seen (who can be messaged) |
| `send` | `text`, `to?` (principal = direct message, `@humans`, or none = broadcast; section 6), `channel?` (`general`, ignored for a direct message), `reply_to?`, `images?` (base64 + mime, at most 6; through `cafard mcp` also `image_paths?`, local files, section 7 Transports), `logs?` (at most 5, section 6.2: `{stream\|channel, from_seq, to_seq?, grep?, min_severity?, title?}`), `section?` (gallery channels only, section 6.3), `gallery_refs?` (item seqs, chat channels only, section 6.4) | `seq`, and per log attachment the resolved line count |
| `list_channels` | `include_archived?` (false) | name, kind (`chat` or `gallery`, with a gallery's sections and their counts), title, topic, bound patterns and the streams they currently match (with status), followed or not, unread count, last activity |
| `create_channel` | `name`, `title?`, `topic?`, `streams?` (patterns), `surface?` (`markers`), `kind?` (`chat`) | the channel; the caller follows it |
| `update_channel` | `name`, `rename?`, `title?`, `topic?`, `add_streams?`, `remove_streams?`, `surface?`, `archived?`, `kind?` | the channel after the change; writes a marker. `rename` is for the creator or an admin, runtime channels only |
| `delete_channel` | `name` | done. Creator or admin, runtime channels only; the channel's messages are deleted with it |
| `follow` / `unfollow` | `channel` | the follow state |
| `channel_timeline` | `channel`, `since_seq?`, `limit?` | messages and event rows as the chat shows them (section 6.1), with the output rules, `next_seq` |
| `inbox` | `channel?`, `limit?` (20), `include_acked?` (false) | messages: seq, time, sender, channel, recipient, text preview, image count, reply_to, open question or not |
| `get_message` | `seq`, `ack?` (true), `images_offset?` | the full text, one **ImageContent** per image (the agent variant, at most 6 per call), and for each log attachment: a header (stream, range, line count, time span), the first 200 lines with the output rules, and the exact `read` arguments to continue |
| `get_log_attachment` | `seq`, `index`, `since_seq?`, `limit?` | the attachment's lines page by page (from the snapshot when there is one, else from the stream), `next_seq`, `has_more`; for an open-ended one, continues live |
| `ack` | `seqs` | count |
| `wait_message` | `from?`, `channel?`, `reply_to?`, `timeout_s?` (60, max 600) | the first matching message for this principal, or `timed_out` |
| `ask_human` | `question`, `choices?`, `channel?` (`general`), `timeout_s?` (300, max 1800) | the reply (text, the chosen choice, who answered) or `timed_out` and the question's `seq` |
| `claim` / `release` / `list_claims` | as in section 6; `list_claims` takes `channel?` (its bound streams) | |

`channel` and `streams` together: the tool reads the union. An unknown
channel is an error that lists the existing ones, so an agent with a typo
does not silently read every stream.

**Log tools never return messages.** `read`, `tail`, `wait_for`, `search`
without `channel`, `context`, `cafard://stream/*` and `list_streams` never
return entries of `chan:*` or `dm:*` streams. Messages are reachable only
through the message tools, `channel_timeline`, `search(channel=)` and SSE,
and all of them apply message visibility.

The scope each tool needs is in CONTRACTS.md section 5.

### Waiting tools

`wait_for`, `wait_message` and `ask_human` share these rules:

- Subscribe to the hub first, then read the store after `since_seq`, then
  drain the subscription, skipping seqs already seen (CONTRACTS.md 1.5).
  Nothing that arrives in between may be lost; a test pins this.
- Honour the request context: a cancelled call releases its subscription
  immediately.
- Send MCP progress notifications every 10 s when the client supplied a
  progress token.

### Resources

- `cafard://streams`
- `cafard://stream/{id}`: the last 200 lines, cleaned.
- `cafard://message/{seq}`
- `cafard://inbox`
- `cafard://channel/{name}`: topic, bindings and the last 50 timeline rows.

Support subscriptions on `cafard://stream/{id}` and `cafard://inbox`, with
notifications throttled to one every 2 s. Many clients ignore resources,
which is why every resource is also a tool.

### Server instructions

Set the MCP `instructions` string to a short guide, roughly:

> Call whoami and list_channels first. When you work on a device or a test,
> use its channel: pass channel= to read, wait_for and search instead of
> listing streams, and post your findings there. Create a channel bound to
> the device for a new task if none fits. Keep the next_seq cursor from every
> read and pass it back as since_seq. Use wait_for instead of polling read.
> Check inbox at the start of a task and whenever you wait: the human drops
> photos and notes there, and other agents send results. Use ask_human when
> you need a physical action. Claim a device before driving it and release
> it after. To share a screenshot, attach it to send: through the cafard mcp
> stdio proxy pass its file path in image_paths; over HTTP pass base64
> images, or run cafard send --image FILE TEXT in a shell. Post screenshots
> to a gallery channel of your project when one exists (list_projects and
> list_channels show them): one image per message, its text a short title,
> and a batch grouped under one section name (send section=...). To show
> gallery images in a chat, pass their seqs in gallery_refs, or write
> [[project/gallery#seq]] in the text; nothing is copied. Do not send
> acknowledgements or thanks to other agents.

`cafard mcp` appends a sentence of its own when it offers `image_paths`.

---

## 8. Images

- Store the original as is in `blobs/<aa>/<sha256>`.
- On ingest, decode it (png, jpeg, gif first frame, webp) and derive two
  variants, each stored as its own blob:
  - **agent variant**: long edge at most 1568 px, JPEG quality 85, EXIF
    orientation applied, EXIF stripped. This is what MCP returns.
  - **thumbnail**: 320 px, for the web UI.
- Decoding runs in a small worker pool, never on the request goroutine. Any
  image with a decoded size above 50 megapixels is refused, to defend against
  decompression bombs.
- The web UI serves originals at `/blob/<sha256>` and variants at
  `/blob/<sha256>?v=thumb`. Both require a session or a bearer token with
  `read`, and are allowed only if the requester can see at least one
  message that references the blob (images, thumbnails, log snapshots).
  Otherwise they answer 404, not 403. Both send
  `Content-Disposition: inline` with the stored mime type and
  `X-Content-Type-Options: nosniff`.

---

## 9. Web frontend

The pages are rendered by Go `html/template`. A TypeScript bundle enhances
them. As in grafit, the bundle is built by Vite into
`internal/web/assets/static/app/main.js` and embedded with `embed`:

- `main.js` is gitignored.
- `internal/web/assets/static/app/README.txt` is committed, so a clean checkout
  compiles.
- `emptyOutDir` is off.
- A compile-time `bundleBuilt` check decides whether the layout emits the
  script tag.

### 9.1 What works without the bundle

- **Chats page.** The compose form is a plain `<form method=post
  enctype=multipart/form-data>` that works without JS. The thread renders
  server-side, the latest 50 messages and event rows. The project's side
  list and the "Logs" toggle are links (`/projects/lab/c/dut1?view=logs`);
  on a phone the side list is a drawer opened by a link to `#side` (CSS
  `:target`).
- **Sources page (`/streams`), and a project's logs
  (`/projects/{project}/logs`).** Fully server-rendered, the sources and
  the lines.
- **Live updates** (the live page, new messages, the logs pane following)
  require the bundle (SSE). Without it, the logs views render the range in
  the query (section 9.6.4), or the last 500 lines when there is none, and
  a notice.

A Go test, like grafit's `TestThePageWorksWithoutTheBundle`, pins the first
two.

### 9.2 Pages

| Path | Purpose |
|---|---|
| `/` | **Projects** (home): a card per project the viewer sees (unread, last activity, counts of chats, galleries and sinks), Direct messages, "Needs you", the overview on wide screens; the admin's New project and Admin (section 9.6.3) |
| `/projects/{project}` | **The project view**: opens on its most recently active chat (303), with the project's side list on every page below (section 9.6.3) |
| `/projects/{project}/c/{channel}`, `/dm/{principal}` | **Chats**: the conversation (human drops, agent messages, open questions, event rows), the composer, and the logs of the channel's bound streams (section 9.6.3) |
| `/dm` | The direct conversations |
| `/projects/{project}/g`, `/projects/{project}/g/{gallery}`, `/projects/{project}/g/{gallery}/{seq}` | **Galleries**: a project's galleries, a gallery channel's screenshots by section, and one item's stable page (sections 6.3 and 6.4) |
| `/projects/{project}/logs`, `/projects/{project}/logs/{sink}` | **A project's logs**: its sources with their status, then the Live view of every sink, or one, with the Live filters but the stream picker |
| `/c/...`, `/g/...` | The addresses before the project view: 308 to their `/projects/...` place |
| `/live` | Live log view: stream picker, severity filter, regexp filter, follow/pause, ANSI colours, tap a line to copy its seq, attach this line, attach from here on, or select a range. In the Admin menu; open to everyone as a link target |
| `/streams` | Sources with status (`connected`, `idle`, `down`), rate, drops, claim holder. In the Admin menu; open to everyone |
| `/admin` | Admin only (admin interface). The admin's menu: Projects, Agents and tokens, sign-in links, the global Sources and Live, unrouted drops |
| `/agents` | Admin only (admin interface). Principals, last seen, tokens (create or revoke; a token is shown once), the admin password |
| `/pair` | Admin only (admin interface). QR code to sign a phone in (section 10) |
| `/projects`, `/projects/{name}/settings` | Admin only (admin interface). Projects and the create form; a project's channels, sinks, retention and members (section 4, "Projects, sinks and grants") |
| `/drop` | Plain quick-drop form with a chat picker (no-JS fallback of the quick drop sheet) |
| `/signin` | The admin password form on the admin interface; on the public one, a note to ask the administrator for a sign-in link (DECISIONS, "Admin password and split interfaces") |

Mobile first:

- Works one-handed on a 375 px screen, with tap targets of at least 44 px.
- Inputs use at least 16 px text, so iOS does not zoom on focus.
- Dark mode follows the system.
- Safe-area insets are respected.
- The composer is pinned to the bottom.

### 9.3 Composer features (bundle)

- Text, with "Add photos" (`accept="image/*" multiple`) and "Camera"
  (`capture="environment"`).
- On desktop: paste from the clipboard, and drag and drop.
- Client-side resize to a long edge of 2560 px, converted to JPEG unless
  "keep original" is ticked. Use `createImageBitmap(file,
  {imageOrientation: "from-image"})` so EXIF rotation is honoured, which also
  turns HEIC into JPEG on iOS.
- Upload with `XMLHttpRequest`, because `fetch` has no upload progress. Show
  progress per message.
- Offline queue persisted in IndexedDB, retried with backoff and visible in
  the UI.
- The last quick-drop chat is remembered in `localStorage`.
- Reply to a specific message. For questions, show one button per choice.
- **Attach logs.** The composer shows log attachments as chips (stream,
  line count, title), removable before sending. They are added from a logs
  view (section 9.6.4) or with "Attach logs" in the composer's `+` menu,
  which offers "the last N lines of a bound stream" and "from now on".
- PWA:
  - `manifest.webmanifest` with a **Web Share Target**
    (`POST /share`, multipart), so "Share -> cafard" works from any Android
    app. The server stages the files (blobs plus a share kept in memory for
    1 hour) and redirects to `/drop?share=<id>`. The service worker only
    rewrites that redirect to `/?share=<id>`, which opens the quick drop
    sheet with the files attached and a chat picker (CONTRACTS.md 2.4).
  - A service worker caches the shell only, never the API.
  - iOS has no share target. The README documents an iOS Shortcut that posts
    to `/api/messages` instead.

### 9.4 TypeScript architecture (Decided principles, Default details)

Object-oriented, small classes, explicit dependencies. The style is grafit's
`src/` (plain DOM in TypeScript classes, `@/` alias, `data-*` attributes to
find mount points), extended for a bigger interface. Use patterns where they
remove a real problem. Each pattern below names the problem it solves; do not
add patterns that are not on this list.

```
src/
  main.ts                        entry: waits for DOM, calls new App(document).start()
  App.ts                         composition root: builds services once, picks the page
  core/
    Disposable.ts                interface Disposable + DisposableBag (cleanup in one place)
    EventBus.ts                  typed Observer: EventBus<EventMap>, on() returns Disposable
    Observable.ts                Observable<T>: value + subscribe, for UI state
    Dom.ts                       typed find/findAll, h() element builder (textContent only)
    Clock.ts                     injectable time source (tests)
    Backoff.ts                   exponential backoff with jitter (SSE, uploads)
    KeyValueStore.ts             typed localStorage wrapper (interface + impl)
  net/
    HttpClient.ts                fetch wrapper: same-origin, X-Cafard header, ApiError
    CafardApi.ts                 Gateway/Facade over every JSON endpoint; pages never call fetch
    LiveFeed.ts                  Adapter over EventSource: reconnect, Last-Event-ID resume,
                                 connection State (connecting | live | retrying | closed)
  model/
    types.ts                     DTOs mirroring the Go JSON (Entry, Message, Stream, Principal)
    parse.ts                     runtime validation of server JSON into DTOs (never trust `as`)
  media/
    ImageProcessor.ts            Strategy interface: process(file) -> Promise<Blob>
    ResizeToJpeg.ts              strategy: createImageBitmap + OffscreenCanvas/canvas
    KeepOriginal.ts              strategy: passthrough (GIF, "keep original", decode failure)
  upload/
    UploadJob.ts                 Command: one message to send, with its own State machine
                                 (queued -> processing -> sending(progress) -> done | failed)
    UploadQueue.ts               runs jobs in order, retries with Backoff, emits events
    JobRepository.ts             interface + IndexedDB impl: persists pending jobs offline
  render/
    AnsiParser.ts                pure: string -> styled segments (SGR subset)
    EntryRenderer.ts             interface render(entry): HTMLElement
    SlotTemplate.ts              clones a server-rendered <template>, fills data-slot parts (9.6.8)
    TextSegments.ts              renders server-parsed message segments (text, code, link)
    renderers/                   LineRenderer, MarkerRenderer, MessageRenderer, QuestionRenderer
                                 (builds the card DOM from tpl-question), LogAttachmentRenderer
    RendererRegistry.ts          Factory/Registry keyed by entry kind (+ meta), fallback renderer
  components/
    Component.ts                 abstract base, Template Method: mount() calls build() then
                                 bind(); unmount() disposes the bag. Subclasses never override mount.
    LogView.ts                   bounded list (cap 5000 nodes, drops oldest), follow/pause
    QuickDropSheet.ts  NeedsYou.ts  Overview.ts
    Composer.ts  AttachmentTray.ts  ConversationView.ts  StreamPicker.ts  FilterBar.ts
    ChatList.ts  ChannelHeader.ts  ChannelDialog.ts  LogsPane.ts  EventRow.ts
    SideNav.ts                   the project view's side list: drawer toggle, sinks, switcher badges
    ProjectCards.ts              home's project cards: badges and latest message
    ConnectionBadge.ts  Toast.ts  UploadStatus.ts
    QuestionCard.ts              binds the choice buttons and the reply form of a rendered card
  pages/
    Page.ts                      interface Page { start(): Disposable }
    PageRegistry.ts              maps <body data-page="..."> to a Page factory
    HomePage.ts  ChatPage.ts  DropPage.ts  LivePage.ts  StreamsPage.ts  AgentsPage.ts  PairPage.ts
```

**Rules**

1. **No `innerHTML`, `outerHTML`, `insertAdjacentHTML` or
   `document.write`.** Log lines and messages come from the network
   (anyone on the LAN can send syslog), so every string reaches the DOM
   through `textContent` or `h()`.
   - CI greps for these and fails the build if it finds one.
   - The ANSI renderer builds `<span>` elements with classes from a fixed
     list. It never passes a colour value through from the input.
2. **Dependency injection through constructors.**
   - `App` is the only place that calls `new` on services.
   - There are no singletons, module-level mutable state, or service
     locators.
   - Components receive what they need, so a test can build a component
     with fakes.
3. **Program to interfaces at the edges.** `HttpClient`, `KeyValueStore`,
   `JobRepository`, `ImageProcessor`, `Clock` and the `EventSource` factory
   are interfaces, each with one real implementation and test fakes.
4. **One-way data flow inside a page.** Services emit events through
   `EventBus` or `Observable`. Components subscribe and render. User actions
   call service methods; components never mutate another component.
5. **Every subscription and listener is registered in a `DisposableBag`.**
   `unmount()` releases everything. Leaked `EventSource` connections are the
   bug to avoid.
6. **Composition over inheritance.** `Component` is the only base class.
   Behaviour shared beyond that goes into collaborators, not deeper
   hierarchies.
7. **Server JSON is validated** by `model/parse.ts` before use. A field that
   fails validation produces an `ApiError`, never an `undefined` deep in the
   UI.
8. **The server owns wording** (a grafit rule). Error sentences and status
   descriptions come from the server. The client does not assemble its own
   phrasing for the same fact.
9. Use **`private readonly`** fields and **`readonly` arrays** in DTOs. Use
   discriminated unions for states (`{state:"sending", progress:number}`),
   never boolean flags.
10. **Accessibility.** The conversation is an `aria-live="polite"` region.
    Every control has a label. Focus moves to new questions, and the ANSI
    colours meet a contrast ratio of at least 4.5:1 in both themes.

`tsconfig.json`: copy grafit's (strict, `noUncheckedIndexedAccess`,
`noImplicitOverride`, `noUnusedLocals` and the rest). Scripts:

```json
"dev": "vite build --watch",
"build": "vite build",
"typecheck": "tsc -p tsconfig.json --noEmit",
"test": "vitest run"
```

Vite does not typecheck, so CI runs `typecheck`, `test` and `build`.

### 9.5 SSE

`GET /api/events?channel=&streams=&kinds=&since_seq=`

`channel` resolves to the channel's messages plus its bound streams, as
`search(channel=)` and `channel_timeline` do. The bundle opens **one SSE
connection per page**: a chat page uses `channel=<name>`, and the Live
page uses `streams=`. The `counts` and `stream_status` events are
multiplexed on that same connection. A binding change is pushed as a
`channel` event, and the page reopens its feed with the new set. Event
names and payloads are in CONTRACTS.md section 4.

- The event id is `seq`. On reconnect, `Last-Event-ID` resumes the feed.
- Send a heartbeat comment every 20 s, so proxies do not close the
  connection.
- Respect the request context, and unsubscribe from the hub on disconnect.
- Messages are filtered by message visibility: a browser never receives a
  DM of a conversation its principal is not a party to.
- A `counts` event is sent on connect and whenever one of the counts
  changes. It feeds the badges (section 9.6.2).

### 9.6 Layout, navigation and look (Default)

#### 9.6.1 Window size classes

The layout follows the Material 3 window size classes, in CSS media queries
only. There is no device sniffing and no layout logic in TypeScript.

| Class | Width | Navigation | Chats page | Live page |
|---|---|---|---|---|
| compact | < 600 px | bottom navigation bar | one column; the side list is a drawer | one column, filters in a bottom sheet |
| medium | 600-839 px | navigation rail | one column, max 720 px; the side list is a drawer | filter bar inline |
| expanded | 840-1199 px | navigation rail | side list + conversation; logs replace the conversation | full filter bar, column layout |
| large | >= 1200 px | navigation rail | side list + conversation + logs pane (bound channels) | same, wider text column |

Four layout widths, as custom properties next to grafit's `--page-narrow` and
`--page-wide`:

- `--pane-list: 280px` (channel list);
- `--pane-reading: 720px` (maximum conversation width, kept for readability);
- `--pane-logs: 420px` (logs pane of a bound channel, resizable);
- `--page-narrow: 560px` (forms: pairing, token creation).

#### 9.6.2 Shell (`layout.html`)

Every page shares one shell:

```
+------------------------------------------------+
| top app bar: title | page actions | badge (live) |
+------+-----------------------------------------+
| rail |  main (page content)                    |
| or   |                                         |
| none |                                         |
+------+-----------------------------------------+
| bottom navigation bar (compact only)           |
+------------------------------------------------+
  + toast region (aria-live)   + dialog / bottom sheet root
```

- **Navigation items.** Every item is a real link, so navigation works
  without the bundle. The template marks the active item from `page.Nav`.

  | Item | Icon | Badge | Compact bar | Rail |
  |---|---|---|---|---|
  | Projects `/` | folder | open questions for you | yes | yes |
  | Direct `/dm` | chat | unread direct messages | yes | yes |
  | Admin `/admin` | key | error dot if a source is down | admin only | admin only |

  Live, Sources, Agents, Pair and Projects are entries of the Admin page;
  inside a project, its logs pages take the place of Live and Sources
  (DECISIONS 2026-11-19, "projects first").

  "More" holds the theme switch (system, light, dark) and sign out.

- **Icons.** Material Symbols are inlined as an SVG sprite
  (`static/icons.svg`, only the glyphs used). They are not loaded from a
  font CDN; cafard makes no outbound requests.
- **Badges.** The server renders them in the nav on page load. The bundle
  updates them from the SSE `counts` event, and also:
  - prefixes the document title, as in `(2) cafard`;
  - calls `navigator.setAppBadge()` when the PWA is installed.
- **Connection badge** (top bar): `live`, `reconnecting` or `offline`,
  driven by the `LiveFeed` state. Pending offline uploads show here as a
  count.
- **Safe areas.** Use `env(safe-area-inset-*)` on the top bar, the bottom
  bar and the composer.

#### 9.6.3 Projects and chats (`/`, `/projects/{project}/...`, `/dm/...`)

The web UI is organised like Discord, **projects first** (DECISIONS
2026-11-19, "projects first"): home lists the projects, and a project's
pages share a side list of its **chats** (channels, section 6.1), its
**galleries** (6.3), its **logs** (its sinks, section 4) and, for the
admin, its **settings**. Direct conversations are outside projects, with
a list of their own. A channel bound to streams is a workspace that holds
the conversation and the device logs together. Every page has its own
URL, so a link to `/projects/lab/c/dut1` can be shared or bookmarked.
`/dm/{principal}` shows the stream `dm:<me>~<principal>`. Routes are in
CONTRACTS.md section 2.

**Home (`/`) is the projects**, with what needs you on top, even when
the viewer sees one project only (no redirect: home is where "Needs you"
and the direct messages are).

Compact (the phone), home:

```
+--------------------------+
| cafard                   |
+--------------------------+
| NEEDS YOU                |  only when non-empty
| ? claude-laptop  #dut1   |  open question for you, answerable here
|   Power-cycle dut1?      |
|   [done] [can't] [open]  |
| PROJECTS                 |
| [L] Lab bench  10:44  3  |  a card: title, last activity, unread
|     #dut1 codex: image.. |  the latest message, with its chat
|     2 chats, 1 gallery, 2 sinks
| [R] Rack 2     09:12     |
| [@] Direct messages   1  |  the direct conversations' list
| [+ New project] [Admin]  |  admin only
|            [cam] [drop]  |  extended FAB: quick drop
+--------------------------+
| Projects  Direct  Admin  |
+--------------------------+
```

A project, compact, with its drawer open (the menu button of the top bar):

```
+------------------+-------+
| Lab bench v   [x]|       |  switcher: All projects, each project, Direct
| CHATS          + |       |  + new channel (bundle)
| # dut1  * ? 3    |  the  |  dots per bound stream, questions, unread
| # general        | page, |
| > Not followed   | under |
| GALLERIES        | a     |
| # shots 12 images|scrim  |
| LOGS             |       |
| > All logs       |       |
| ((o)) dut1 *  10:44      |  dot per stream of the sink, last line time
| ((o)) bench-pi * 09:30   |
| (gear) Settings  |       |  admin only
+------------------+-------+
```

- **Needs you.** Open questions addressed to you (`@humans` or your
  principal), answerable from home without opening the chat, then
  direct conversations with unread messages. It is hidden when empty.
- **Project cards.** One per project the viewer sees: its title, the
  unread messages and open questions of its chats (badges), the time and
  one line of its latest message (`#chat sender: text`), and how many
  chats, galleries and sinks it has. Unread counts follow the SSE
  `counts` event, the line the `entry` events of the home feed.
- **The project view** `/projects/{project}` opens on the project's most
  recently active chat, else `#general` (a 303, so the address is the
  chat's). Its pages: a chat `/c/{channel}`, the galleries `/g` and each
  gallery `/g/{gallery}`, the logs of every sink `/logs` or of one
  `/logs/{sink}`, and for the admin `/settings`.
- **Side list.** From 840 px a sidebar (`--pane-list`), below a drawer
  over the page; without the bundle the menu button is a link to `#side`
  and CSS `:target` opens it, with the bundle `SideNav` toggles it (Esc,
  the scrim and its close button shut it). Its rows are compact: an icon
  (`#`, the sink's sensor), the name, status dots, the badges, a bold
  name when something is unread, and the page shown highlighted.
  - **Chats**: the followed channels, pinned first, then by last
    activity, which counts messages and bound-stream events; "Not
    followed" and "Archived" fold below (`<details>`).
  - **Galleries**: each gallery and its image count.
  - **Logs**: "All logs", then each sink with one status dot per stream
    (`connected`, `idle` or `down`; the icon shows the worst) and the
    time of its last line, live from `stream_status`. Only for viewers
    who may read the project's logs.
  - **Settings**: the admin's project page (channels, sinks, retention,
    members, sign-in links), on the admin interface only.
- **Header.** The top bar shows the project above the page's title (the
  channel's name); the side list's head is the project switcher. On wide
  screens a back arrow returns to home; on a phone the drawer has "All
  projects".
- **A project's logs** list the sources of its sinks (the Sources cards)
  above the Live view on `*:<project>/*` or `*:<project>/<sink>`, with
  Live's filters but the stream picker; "Open in Live" opens the same
  streams on `/live`, which keeps every filter.
- **Rows.** A chat row has the name, one status dot per bound stream,
  the time of last activity, the unread count and, outside the compact
  side list, a preview line (the last message, or the last event row).
  Muted chats are greyed and never counted (see read state below). A long
  press, or right-click on a desktop, offers: pin, mute, follow/unfollow,
  mark read, settings.
- **Quick drop.** The extended FAB opens the composer as a bottom sheet:
  - Its chat picker defaults to the last chat you dropped into.
  - The camera button opens the camera directly.
  - It covers "take a photo of the screen and send it" in two taps without
    leaving home.
  - The Android share target (`/share`) opens the same sheet, prefilled
    with the shared files.
- **Back stack.** Opening a chat pushes its URL; the system back gesture
  returns to the page before.
- **Without the bundle**, every page is the same server-rendered HTML: the
  cards, the side list and the switcher are links and `<details>`, the
  drawer a `:target`. The quick drop is a link to `/drop`, a plain form
  page with the chat picker.

Compact, inside a bound channel:

```
+--------------------------+
| < #dut1      [Chat|Logs*]|  back to the list; toggle, dot = new error
| serial:dut1 * claude-l.. |  bound stream chips (status dot), claim holder
+--------------------------+
| [1 question waiting]     |  sticky banner, tap scrolls to it
| (All)(Questions)(Events) |  filter chips, scroll sideways
|                          |
|  codex-build  10:42      |  agent message, left, sender chip with icon
|  image v42 ready, go     |
|  -- claude-laptop claimed serial:dut1 --   event row (marker)
|  +--------------------+  |
|  | Power-cycle dut1?  |  |  question card (tertiary container)
|  | [done] [can't]     |  |
|  | reply...           |  |
|  +--------------------+  |
|  !! serial:dut1 Kernel panic - not syncing  [logs]   event row (surface=errors)
|             you 10:44    |  human message, right
|        [photo][photo]    |
|   seen by codex-build    |
+--------------------------+
| [+][cam] message...  [>] |  composer, pinned
+--------------------------+
```

Inside a chat on compact screens the bottom navigation bar is hidden, to
give the conversation and the composer the height. The drawer leads to
the other chats and back to home, where the bar is.

"Logs" swaps the conversation for a `LogView` of the bound streams, merged,
with the Live page's filters. The composer stays, so you can answer while
you watch. Each view keeps its scroll position when you toggle.

Expanded (840-1199 px) and large (>= 1200 px), inside a chat:

```
+----+------------+------------------------------+----------------------+
|rail| Lab v      | #dut1  DUT 1 (x86 board)     | logs: bound streams  |
|    | CHATS    + | serial:dut1 * netconsole:dut1| [sev v] [/re/] || [x] |
|    | # dut1 * 3 | ---------------------------- | 10:42:01 dut1 I ...  |
|    | # general  | conversation (max 720 px)    | 10:42:03 dut1 W ...  |
|    | GALLERIES  |                              | -- marker --         |
|    | LOGS       |                              |                      |
|    |  dut1  *   | [composer]                   |       [v 12 new]     |
+----+------------+------------------------------+----------------------+
```

- On **large** screens the logs pane is open by default for a bound
  channel. It is 420 px wide, resizable, and its width is remembered.
- On **expanded** screens the "Logs" toggle shows the pane in place of
  the conversation, as on a phone.
- Unbound channels and direct conversations have no logs pane and no
  toggle.
- On these screens, **home (`/`) shows the projects and an overview
  pane** beside them:
  - "Needs you", as on the phone.
  - Recent activity across followed chats: the latest messages and event
    rows, each linking to its chat.
  - The quick-start snippets from the empty state while nothing exists.

  No chat is opened automatically. A desktop that lands on a chat by itself
  hides what needs you in the other chats.

**Chat rows (all sizes):**

- The side lists and "Needs you" use one component (`ChatList`), fed by
  one server-rendered partial (`partials/chat_row.html`) and its
  `<template>` twin.
- Unread counts and status dots update live from the SSE `counts` and
  `stream_status` events; a new chat lands in the section of its state
  and kind (a gallery under Galleries).
- Pinning and muting are per principal, stored server-side in
  `follows.pinned` and `follows.muted` (section 4), so the phone and the
  desktop agree.

**Read state:**

- A message is acked for the viewing human once it has been visible in the
  conversation for 1 s (bundle, `IntersectionObserver`), or when a no-JS
  page renders it.
- "Mark read" acks everything in the chat.
- Mute removes the chat from unread counts and "Needs you", except
  questions and direct messages addressed to that human by name.

**Channel header:**

- The title and topic.
- Bound stream chips. Tapping one opens its sink's logs in the project
  view (`/projects/<project>/logs/<sink>`).
- The claim holder, if a bound stream is claimed.
- A settings button that opens the channel dialog: title, topic, bindings
  (a stream picker that accepts globs and previews what they match),
  surface mode, mute (do not count unread for me), follow, archive, and,
  for the creator or an admin on a runtime channel, rename and delete
  (delete asks for confirmation and removes the channel's messages).

**Event rows:**

- Markers and, with `surface=errors`, error lines from the bound streams are
  shown as one-line rows between messages. Their style is not a bubble.
- Tapping one opens the logs view at that `seq`, with 20 lines of context.

**Mapping to the Live page:**

- Live stays the global view across all sources (in the Admin menu, and
  the target of log attachments and event rows); inside a project, its
  logs pages are Live on its sinks.
- Its stream picker also accepts a channel, to view that channel's bound
  streams.

- **Message anatomy:**
  - Sender chip: a kind icon (agent, human or device), the name, the time.
  - Body, then attachments, then the reply quote if `reply_to` is set.
  - For human messages, a receipt line ("seen by X, Y") built from
    `seen_by` (section 6).
  - Menu: reply, copy text, copy seq, open in Live (jumps to the entry's
    time across streams).
- **Message text is plain text with three safe enrichments:** fenced code
  blocks and inline code, http(s) autolinks, and preserved line breaks.
  - The **server** parses text into segments
    (`[{t:"text"|"code"|"codeblock"|"link", v, href?}]`).
  - The Go templates render the segments, and the JSON API returns them.
    The browser never parses message text itself ("the server owns the
    wording").
  - Everything else in the text is shown as typed, so no Markdown parser
    is needed on either side.
- **Images.**
  - One image is shown full width. Two to four go in a 2x2 grid. Beyond
    that, the grid shows a `+N` tile.
  - Tapping an image opens a viewer dialog with the original image, native
    pinch zoom, and swipe between images.
  - Without the bundle, the viewer is a plain link to `/blob/<sha>`.
- **Log attachment card.**
  - The header shows the stream chip(s), the title, the line count and the
    time span. A clock icon marks an open-ended attachment, and a pin icon
    marks a snapshotted one.
  - Below it, a preview of the first 5 lines in monospace with ANSI
    colours, then the matching lines when `grep` is set.
  - "Open" shows the whole slice in the logs view (the chat's logs pane on
    large screens), highlighted, with 20 lines of context around it.
  - Once retention has removed the lines and there was no snapshot, the
    card says "lines expired".
  - Without the bundle, "Open" is a link to
    `/live?streams=...&from_seq=...&to_seq=...` (plus `sev` and `re` when
    the attachment has them).
- **Question card.**
  - Tertiary-container surface, one filled tonal button per choice, and a
    free-text reply.
  - Once answered (section 6, first human reply), it shows "answered by
    <name>: <choice>" and disables the buttons for every human.
  - Open questions addressed to you are also listed in the sticky banner.
- **Filter chips.** This is the only list: All, Questions, Events, Mine,
  With images. The URL query is `f=all|questions|events|mine|images`, so
  they work without the bundle. Events is shown only in bound channels.
- **Scrolling.**
  - The page opens at the newest message.
  - Older messages load when you scroll up, through `before_seq`; without
    the bundle, an "Older" link does the same.
  - New messages scroll the view only if it is already at the bottom.
    Otherwise a "N new" pill appears.

#### 9.6.4 Live page (`/live`)

```
+----+------------------------------------------------------------+
|rail| [streams v] [sev >= info v] [/regex/ ] [kinds v]  || pause |
|    +------------------------------------------------------------+
|    | 10:42:01.123  dut1     I  U-Boot 2024.04                   |
|    | 10:42:03.456  dut1     W  ...                              |
|    | ----- claude-laptop claimed serial:dut1 -----              |
|    | 10:42:09.000  syslog   E  ...                              |
|    |                                    [v 120 new]   *live     |
+----+------------------------------------------------------------+
```

- **Columns:** time (ms), stream tag, severity letter, text. The stream tag
  colour is a hash of the stream id into a fixed set of 8 accessible tones.
  On compact screens the time is shortened to `mm:ss` and the line wraps
  under the tag.
- **Severity** uses colour roles: error and above `--error`, warning
  `--tertiary`, debug `--on-surface-variant`. The letter is always shown,
  so meaning never depends on colour alone.
- **Markers** are full-width divider rows. Messages can be included
  through the kinds filter, to correlate a conversation with the logs.
- **Follow.**
  - The view follows the end while it is scrolled to the bottom.
    Scrolling up pauses it.
  - A floating "v N new" button resumes following. `f` toggles it on a
    desktop.
- **Line actions.** Tapping a line, or right-clicking on a desktop, opens
  a bottom sheet or menu with: copy line, copy seq, show context (+-20
  lines in its stream), add marker here (`POST /api/marks`, see
  CONTRACTS.md section 2: scope `message`, written into the stream of the
  line), **attach this line**, **attach from here on**, and **select a
  range**.
- **Range selection.**
  - Start one with "select a range" (or a long press on a phone), then tap
    the last line. On a desktop, click then shift-click.
  - The selected range is highlighted, and a bar shows its line count with
    the actions: attach to a message, copy, cancel.
  - "Attach" opens the composer with the log chip. Inside a chat's logs
    pane or toggle it is that chat's composer; on the Live page it is the
    quick drop sheet with a chat picker.
  - The active filters (`grep`, minimum severity) are carried into the
    attachment, so what the agent reads is what you saw.
- **Filters** live in the URL. The query parameters, named here once, are
  `streams`, `channel`, `from_seq`, `to_seq`, `sev`, `re` and `kinds`.
  Without the bundle the server renders that range, or the last 500 lines
  when there is no range. On compact screens the filters sit behind one
  filter button that opens a bottom sheet.
- **Monospace.** 13 px, with a denser line height than M3's default,
  through a `.dense` modifier.

#### 9.6.5 Sources, Agents, Pair

- **Sources (`/streams`).**
  - Layout: cards on compact screens, a table from medium up.
  - Each source shows: a status dot (`connected`, `idle`, `down`; section
    4), its
    kind and transport (local, rfc2217, tcp, udp, otlp), lines per minute,
    last activity, a preview of the last line, dropped count, claim holder
    chip, and "open in Live".
  - Serial sources also show the last known line state (DTR, RTS) when the
    transport reports it.
- **Agents (`/agents`, admin).**
  - Principals with kind and last seen, and their tokens (name, scopes,
    last used, revoke).
  - "New token" opens a dialog that shows the token **once**, with a copy
    button.
  - The dialog also shows ready-to-paste client configuration with the
    server URL and token filled in:
    - the `claude mcp add --transport http ...` command;
    - the Codex `config.toml` block;
    - a generic MCP JSON entry;
    - OTel exporter environment variables for ingest tokens.
  - The server renders these snippets.
- **Pair (`/pair`, admin).** A large QR code with a countdown of 5
  minutes, a principal picker (an existing human, or a new human whose
  name the admin types), and the list of active sessions, each with
  revoke.
- **Admin (`/admin`, admin).** The admin's menu: Projects, Agents and
  tokens, sign-in links (Pair), the global Sources and Live, and the
  count of logs that reached no sink. Non-admins never see these items.
- **Projects (`/projects`, `/projects/{name}/settings`, admin).** The list
  of projects with a create form (also on home); per project its
  settings, in the project's side list: its channels (create), its
  sinks (create, edit the source addresses and the serial port, delete,
  make a sink token shown once), its retention, and its members: agents
  and people with their grants (add, remove), and a sign-in link with a
  QR code that grants this project or one of its channels.
- **Grouped by project.** Sources and the Live stream picker group
  streams and channels under their project; the project view gives each
  project its own chats and logs. All server-rendered, so the Chats,
  Sources and project logs pages still work without the bundle.

#### 9.6.6 Empty states

Every page has a useful empty state. The server renders it with
`public_url` filled in, so it can be copied as is:

- **Chats:** "No messages yet", with the pairing link and the MCP
  configuration snippet.
- **Live and Sources:** ready-to-paste commands:
  - `logger -n <host> -P 5514 -T hello`;
  - the netconsole kernel argument;
  - an OTel exporter environment example;
  - `cafard push` for Linux and for Windows.

#### 9.6.7 Look

The look is Material 3 in plain CSS, as in grafit. Start from a copy of
`~/misc/grafit/internal/web/static/app.css` and keep its structure:

- M3 colour roles as custom properties (`--primary`, `--surface-container`,
  ... `--outline-variant`).
- The shape scale (`--shape-*`), the elevation levels, state-layer
  opacities, and motion tokens.
- The dark block applied twice: under `prefers-color-scheme: dark` guarded
  by `:not([data-theme="light"])`, and under `[data-theme="dark"]`. The
  theme choice is stored in a cookie, so the server renders the right
  theme without a flash.
- System font stacks only, with no webfont downloads:
  - Text: `system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`.
  - Monospace: `ui-monospace, "SF Mono", Menlo, Consolas, monospace`.

What changes from grafit:

- **Palette.** A tonal palette generated the M3 way from a cafard seed:
  chestnut brown `#8b5a2b`, a cockroach. Generate the values once with
  Material Theme Builder or `material-color-utilities`, commit them, and
  write the seed and the tool in a comment at the top of the file, as
  grafit does for graphite.
- **ANSI colours.** 16 custom properties per theme (`--ansi-0` to
  `--ansi-15`), each checked to reach a contrast of 4.5:1 against
  `--surface` and `--surface-container`. Since a test cannot read CSS, a
  comment lists the measured ratios.
- **Density.** Add the `.dense` modifier for log rows and tables. The M3
  defaults are kept for everything touch-driven (composer, chips, question
  buttons, nav), with targets of at least 48 px on compact screens.

Components used, all M3: top app bar, navigation bar, navigation rail,
filter chips, filled and tonal buttons, FAB (the "N new" pill), cards,
bottom sheets, dialogs, snackbars (toasts), badges, and text fields
(outlined).

#### 9.6.8 Templates and one source of markup

Templates live in `internal/web/assets/templates/`: `layout.html` (the shell),
one partial per repeated piece (message, question, line, stream card, chat
row, needs you, empty states), and one file per page. The list of
partials, the `data-page` values, the `tpl-*` ids and the `data-slot`
names are in CONTRACTS.md section 6.

**Markup is defined once, in Go.** The bundle never builds the structure of
a message or a line on its own:

- Each partial is rendered twice by the same template:
  - with real data, for the initial page;
  - empty, inside a `<template id="tpl-message">` (and `tpl-line`,
    `tpl-question`, ...) at the end of the page.
- Fillable parts carry `data-slot` attributes (`sender`, `body`, ...).
- The TypeScript renderers (`render/renderers/*`) clone the `<template>`,
  fill the slots with `textContent` and `h()`, and set attributes from a
  fixed list. `QuestionRenderer` builds the card DOM from `tpl-question`;
  the `QuestionCard` component then binds its buttons and reply form.
- So the no-JS page and the live page cannot drift apart, and there is
  still no HTML parsing in the browser.
- A Go test checks that every `tpl-*` the bundle asks for is present on the
  pages that load it. A TS test checks the renderers against a fixture page
  produced by that Go test.

#### 9.6.9 Keyboard (desktop)

| Key | Action |
|---|---|
| `/` | focus the filter or the composer |
| `g p` / `g d` / `g l` / `g s` | go to the projects, the direct messages, Live, Sources |
| `f` | toggle follow (Live) |
| `Esc` | close the sheet, dialog or viewer |
| `Ctrl+Enter` | send from the composer |

Shortcuts are ignored while a text field has focus, except `Esc` and
`Ctrl+Enter`. They are listed in a `?` dialog.

---

## 10. Security

The server runs on a trusted lab network. That is no excuse for carelessness.

### Tokens and scopes

Scopes are `ingest`, `read`, `message`, `serial_write` and `admin`.

- `cafard token create --name codex-build --kind agent --scopes read,message`
  prints the token once. Tokens are 32 random bytes in base64url, prefixed
  `cfd_` so secret scanners recognise them.
- Only the sha256 of a token is stored.
- Scopes live on tokens. A session copies the scopes of the token or
  pairing that created it into `sessions.scopes`.
- A paired phone session gets `read message ingest`. `device` principals
  get `ingest` only.
- Scopes are a cap. Reaching a project, a channel or a stream also needs a
  grant (section 4, "Projects, sinks and grants"); a sink token needs none
  and can only write into its sink. `cafard token create --sink
  project/sink` makes one; `--grant project[:channel]=scopes` (repeatable)
  grants the principal, and so does `cafard pair --grant`.
- The scope each route and MCP tool needs is in CONTRACTS.md sections 2
  and 5.

### First start

With no admin password set, `cafard serve` makes a random one, stores its
scrypt hash, and prints it with the admin sign-in URL to stdout once. The
installer sets one before the first start instead (`cafard passwd
--if-unset`), so it never reaches the service's log. `cafard passwd` resets
it from a shell without the old one.

Like grafit, it refuses to bind a routable address (`http_listen` or
`admin_listen`) unless an admin password is set. First start sets one, so
in practice the guard only fires when setting it failed. It fails closed if
the address cannot be parsed or resolved.

### Admin, interfaces, browser sessions and pairing

- **One admin account, one password.** Signing in at `/signin` on the admin
  interface opens an admin session: in memory only (a restart signs the
  admin out), 12 hours, cookie `cafard_admin` (HttpOnly, SameSite=Strict).
  It acts as the human principal `admin` with `ingest read message admin`.
  It is the only credential with the `admin` scope: tokens, browser
  sessions and pairings refuse it.
- **Two interfaces.** `http_listen` is the public one. With `admin_listen`
  set, the admin routes (password sign-in, tokens, pairing, password
  change) are only on that second address, which also serves every public
  route; on `http_listen` they answer 404 and the admin cookie is ignored.
  Without it, one address serves both.

0. Nobody but the admin signs in by typing anything. The admin gives each
   person a one-time sign-in link: a QR code on `/pair`, or `cafard pair
   --name NAME`, which prints `<public_url>/p/<code>` (5 minutes by
   default, `--ttl` up to 24 hours).
1. The admin opens `/pair` and gets a QR code holding a one-time code (valid
   5 minutes, single use). Codes are stored hashed in the `pairings` table,
   with the chosen principal, or the name the admin typed for a new human,
   and the grants the link gives (section 4, "Projects, sinks and grants").
2. Scanning it on the phone (on the public interface) creates that human
   principal, or reuses the one chosen on `/pair`, and sets an HttpOnly,
   SameSite=Lax session cookie, valid 30 days and renewed while used.
3. Session tokens are stored hashed, so a copy of the database cannot mint a
   cookie.

### CSRF and Origin

- Cross-origin writes are refused on `Sec-Fetch-Site`, with an `Origin`
  fallback (grafit's approach, pinned by a test).
- `/share` (the share target) is same-origin by construction and passes that
  check.

### Other rules

- **MCP:** bearer tokens only, and the `Origin` header is validated.
- **Syslog and netconsole:** unauthenticated by nature. The CIDR allowlist is
  optional, and the README says plainly that anyone on the LAN can write
  lines.
- **serial_write and serial_control:** gated four times (the port is
  `writable`, the token's scope, a `serial_write` grant on the stream, and
  the claim).
- **OTLP:** same bearer tokens as the rest of ingest (`ingest` scope).
- **Why static tokens rather than OAuth.** The MCP authorization spec
  defines OAuth 2.1 for the HTTP transport, with protected resource
  metadata (RFC 9728). The clients cafard targets accept a fixed
  `Authorization` header in their configuration, which suits a lab
  network, so static tokens come first. M8 adds OAuth 2.1 if a client turns
  out to require it: authorization code with PKCE, an approval page shown
  to a signed-in human, and tokens mapped to the same principals and
  scopes. Keep `internal/auth` behind an interface (`Authenticator`
  returning a principal and scopes) so OAuth slots in without touching
  handlers.
- **TLS:** optional (`tls_cert`, `tls_key`), or terminated by a reverse
  proxy. Use `-secure` for HTTPS-only cookies, as grafit does. The PWA
  service worker and share target need HTTPS or localhost, and the README
  explains that.
- **Secrets in logs:** never log tokens, session ids, message text or image
  contents in cafard's own log.
- **File modes:** the database 0600, the data directory 0700, and the config
  file 0640 owned by the service user.

---

## 11. Configuration

`/etc/cafard.yaml` (piCVert style). Every key can also be set by a flag
(`-http-listen`) or an environment variable (`CAFARD_HTTP_LISTEN`).
Precedence is flag, then env, then file, then default. List-valued keys
(`serial`, `channels`, `retention.streams`, the allowlists such as
`syslog.allow` and `mcp.allowed_origins`) are file-only: they have no flag
and no environment variable.

`cafard config` loads, validates and prints the effective configuration,
with secrets redacted, and exits non-zero on error. The OpenRC script and
the installer call it before starting, as piCVert's do.

```yaml
data_dir: /var/lib/cafard
http_listen: 0.0.0.0:8790
# admin_listen: 127.0.0.1:8791        # admin routes only here (DMZ setups)
public_url: http://10.0.50.X:8790     # used in QR codes and links
secure: false                         # HTTPS-only cookies (behind TLS)
# tls_cert: /etc/cafard/tls.crt
# tls_key:  /etc/cafard/tls.key

syslog:
  udp: ":5514"
  tcp: ":5514"
  allow: ["10.0.50.0/24"]

netconsole:
  udp: ":6666"
  # names: { "10.0.50.42": dut1 }   # deprecated: imported once into sinks

retention:                          # defaults of new projects, and the limits
                                    # of what is outside projects
  lines_max_age: 336h
  lines_max_bytes: 2GB
  messages_max_age: 2160h
  blobs_max_bytes: 5GB
  streams:                          # per-stream overrides, first match wins
    - pattern: "serial:*"
      lines_max_age: 720h

messaging:
  rate_per_minute: 30
  burst: 10
  channel_create_per_hour: 10
  archive_after: 336h               # runtime channels idle this long are archived

images:
  max_file_mib: 20
  max_files: 10
  max_megapixels: 50

mcp:
  allowed_origins: []               # extra Origin values accepted on /mcp

otlp:
  enabled: true
  max_body_mib: 8

channels:                           # reapplied at every start; a bare name is in
                                    # the project imported, project/name elsewhere
  - name: general                   # may set title and topic only
    topic: anything not about one device
  - name: dut1
    title: DUT 1 (x86 test board)
    streams: [serial:dut1, netconsole:dut1, "syslog:dut1*"]
    surface: markers

# serial:                          # deprecated: imported once into sinks of the
#   - name: dut1                    # project imported; ports are set per sink now
#     device: /dev/serial/by-id/usb-FTDI_FT232R_A1B2C3-if00-port0
#     baud: 115200
```

Port 8790 is a Default; the other projects use 8781 (grafit) and 3000
(piCVert), so pick one that collides with neither.

---

## 12. Build, CI, release, deployment (Decided: same as the other projects)

### 12.1 Where things run

| What | Where | Command |
|---|---|---|
| Typecheck, unit tests, bundle | here or dev-build | `npm ci && npm run typecheck && npm test && npm run build` |
| Quick Go compile of one package | here, if the local Go satisfies `go 1.26.0`; otherwise remote | `go build ./internal/<pkg>/` |
| gofmt, build, vet, `test -race` | **dev-build** | `scripts/remote.sh` |
| Anything that ships | **dev-build** or CI | `scripts/devdeploy.sh`, or a `v*` tag |

The local Go is 1.24 and `go.mod` says 1.26. If `GOTOOLCHAIN` cannot fetch
1.26 here, do every Go check through `scripts/remote.sh`.

### 12.2 `scripts/remote.sh`

Copy grafit's script and adapt it:

- `REMOTE_DIR=cafard`.
- With no arguments, run `gofmt -l . && go build ./... && go vet ./... &&
  go test -race ./...`. `gofmt -l` must print nothing: make the command fail
  if it prints anything.
- Excludes, anchored to the tree root:
  - `/.git/`, `/node_modules/`
  - `/data/`, `*.db`, `*.db-wal`, `*.db-shm`
  - `/cafard.yaml`

  The database holds messages and the photos people dropped; it never
  leaves the machine it runs on.

### 12.3 CI (`.github/workflows/ci.yml`, modelled on grafit's)

1. **Frontend job.** Node 22: `npm ci`, `typecheck`, `test`, `build`. Check
   that `main.js` exists and is not empty. Grep `src/` for forbidden HTML
   sinks. Upload the bundle as an artifact.
2. **Server job.** Download the artifact, then: `gofmt` check, `go mod tidy`
   check, `go vet`, `go build`, `go test -race`.
3. **Cross-build matrix** with `CGO_ENABLED=0`:
   - linux amd64, arm64 and armv7;
   - **windows amd64 and arm64** (cafard.exe for `push` and `term`).
4. **Shell job.** `shellcheck` on `scripts/*.sh` and `deploy/*.sh`, and
   `sh -n deploy/install.sh` (POSIX sh).

### 12.4 Release (`.github/workflows/release.yml`)

A `v*` tag triggers the release:

1. Re-run the frontend checks and `go test -race`.
2. Build each target with `-trimpath -ldflags "-s -w -X main.version=$TAG"`.
3. Smoke-test that the linux amd64 binary reports the tagged version.
4. Write `SHA256SUMS` and publish a GitHub release.

Asset names:

- `cafard-linux-amd64`, `cafard-linux-arm64`, `cafard-linux-armv7`
- `cafard-windows-amd64.exe`, `cafard-windows-arm64.exe`
- `SHA256SUMS`

Each binary embeds the frontend, so each is the whole application.

### 12.5 `deploy/`

The files come from grafit and piCVert:

- **`install.sh`**, POSIX sh, idempotent, merging grafit's and piCVert's:
  - Piped from curl, it downloads the release for this architecture and
    verifies it against `SHA256SUMS`, failing closed if the sums are
    missing. From a checkout, it builds from source, including the bundle if
    `npm` exists.
  - It writes `cafard.new` and then moves it into place, so a failed update
    leaves the running version alone.
  - It creates the `cafard` system user and adds it to the `dialout` group
    for serial ports.
  - It creates `/var/lib/cafard` with mode 0700.
  - It writes `/etc/cafard.yaml` from the example only if the file does not
    exist; it never overwrites the user's config.
  - It installs the systemd unit, or the OpenRC script on Alpine.
  - It tries the strict unit first. If that fails with `226/NAMESPACE`, it
    installs the container drop-in, as piCVert's installer does.
  - It sets a random admin password when none is set, and finally prints
    it (once), the sign-in URL, and the next steps.
- **`cafard.service`**: hardened as grafit's, with these differences:
  - Use `PrivateDevices=no` with `DevicePolicy=closed` and
    `DeviceAllow=char-ttyUSB rw`, `char-ttyACM rw`, `char-ttyS rw`, plus
    `SupplementaryGroups=dialout`. Serial ports need `/dev`. Explain this in
    a comment.
  - Allow `AF_INET AF_INET6 AF_UNIX`, with no `IPAddressDeny` (it listens on
    the LAN and makes no outbound calls except the optional TLS cert
    reload).
  - `StateDirectory=cafard`, `UMask=0077`, `ReadWritePaths=/var/lib/cafard`.
  - A commented `AmbientCapabilities=CAP_NET_BIND_SERVICE`, for users who
    want syslog on port 514.
- **`cafard-container.conf`**: piCVert's drop-in for unprivileged LXC. Note
  that a serial port in an LXC container also needs device passthrough on
  the Proxmox host (`dev0: /dev/ttyUSB0,gid=<dialout gid>`); document it in
  `deploy/README.md`.
- **`cafard.openrc`**: piCVert's, with `start_pre` running `cafard config`
  and refusing to start on a bad configuration.
- **`cafard.yaml.example`**: section 11.
- **`README.md`**: the quick install, reverse-proxy settings, TLS, LXC serial
  passthrough, Windows push client use, and updating.

Health check: `GET /healthz` returns `200 ok` with the version. Install and
devdeploy both call it.

### 12.6 `scripts/devdeploy.sh`

Copy piCVert's script. It builds on dev-build, pulls the binary back here,
and pushes it to the appliance. The binary is pushed as `cafard.new` and
then renamed (otherwise you get "Text file busy"). The script restarts the
service with `rc-service`, falling back to `systemctl`, then prints
`cafard version` and `/healthz`.

The version string is `dev-<sha>[-dirty]`, so it is never mistaken for a
release.

The target is not known yet, so support both modes through environment
variables:

- `PVE=root@host CT=<id>`: push with `pct`, as piCVert does.
- `TARGET=user@host`: plain ssh and scp.

The script refuses to run when neither is set.

---

## 13. Reference files to copy from

| Need | Copy from |
|---|---|
| `remote.sh` | `~/misc/grafit/scripts/remote.sh` |
| `devdeploy.sh` | `~/misc/piCVert/scripts/devdeploy.sh` |
| `install.sh` | `~/misc/grafit/deploy/install.sh` + config handling and container fallback from `~/misc/piCVert/deploy/install.sh` |
| systemd unit | `~/misc/grafit/deploy/grafit.service` |
| container drop-in, OpenRC | `~/misc/piCVert/deploy/picvert-container.conf`, `picvert.openrc` |
| CI, release | `~/misc/grafit/.github/workflows/{ci,release}.yml` (add the Windows targets) |
| Vite, tsconfig, embed placeholder, `bundleBuilt` | `~/misc/grafit/vite.config.ts`, `tsconfig.json`, `internal/web/static/app/README.txt`, `internal/web/server.go` |
| TS class style | `~/misc/grafit/src/` (App, core/Api, components/Dialog) |
| Material 3 CSS (roles, shapes, elevation, dark block) | `~/misc/grafit/internal/web/static/app.css` |
| Layout template, manifest | `~/misc/grafit/internal/web/templates/layout.html`, `static/manifest.webmanifest` |
| `main.go` shape (usage text, `run() error`, version var, graceful shutdown, exposure guard) | `~/misc/grafit/cmd/grafit/main.go` |
| `.gitignore` for the bundle | `~/misc/grafit/.gitignore` |
| AGENTS.md tone ("Gotchas that will cost you an afternoon") | `~/misc/grafit/AGENTS.md` |

---

## 14. Milestones

Each milestone ends with code, tests, an updated `README.md` and
`AGENTS.md`, and the "Done when" check. Commit at the end of each milestone
only if the user has initialised git and asked for commits.

**Parallel work.** The milestones below are grouped into tracks that
several agents can run at once (CONTRACTS.md section 7). Track 0 (M0 and
M1) runs alone first. **The user initialises git and the GitHub remote
before more than one agent works.** Until then, one agent works at a time.

**M0. Skeleton and pipeline**
- `go mod init github.com/nebuloss/cafard`, `cmd/cafard` with its
  subcommands, config loading with `cafard config`, slog, `/healthz`,
  graceful shutdown.
- `package.json`, `tsconfig.json`, Vite and vitest configs, `src/main.ts`,
  `App.ts`, the `core/` classes with tests, the embed placeholder, and
  `bundleBuilt`.
- `scripts/remote.sh`, the CI and release workflows, `.gitignore`.
- *Done when:* `scripts/remote.sh` passes, `npm run typecheck && npm test &&
  npm run build` passes, and `cafard version` prints `dev`.

**M1. Store, HTTP ingest, core MCP**
- Store and migrations, writer, hub, janitor, principals and tokens
  (`cafard token create`), bearer auth.
- `POST /api/log`, and `cafard push` from stdin.
- MCP over streamable HTTP: `whoami`, `list_streams`, `read`, `tail`,
  `wait_for`, `context`, `mark`. Section 7 output rules.
- A Go test writes `internal/web/testdata/api/*.json` fixtures for every
  DTO in CONTRACTS.md section 3.
- *Done when:* `seq 1 100000 | cafard push` lands. An SDK client in an
  integration test is released from `wait_for` both by a line pushed
  afterwards and by one pushed between `since_seq` and the call.

**M2. Messaging, channels and images**
- `internal/messaging`: send, inbox, acks, follows, `wait_message`,
  `ask_human`, rate limits.
- Channels with stream bindings (section 6.1): config channels, globs,
  timeline union by `surface`, auto-archive, binding markers. `channel=`
  resolution in `read`, `tail`, `wait_for`, `list_streams`, `list_claims`.
- Claims (section 6): the table, `claim`, `release`, `list_claims`, the
  claim, release and expiry markers.
- `POST /api/messages` with bearer auth (section 5.2).
- Blob store, image variants, the worker pool.
- Log attachments (section 6.2): validation, snapshots, the MCP
  `get_log_attachment` tool and the log part of `get_message`.
- MCP: `send`, `inbox`, `get_message`, `ack`, `wait_message`, `ask_human`,
  `list_principals`, `list_channels`, `create_channel`, `update_channel`,
  `delete_channel`, `follow`, `unfollow`, `channel_timeline`, resources.
- *Done when:*
  - Agent A's `send` with an image arrives in agent B's `get_message` as
    ImageContent.
  - Both agents see a broadcast, and each acks it independently.
  - A spamming agent is rate limited.
  - An agent creates `run-42` bound to `serial:dut1`, and `wait_for` with
    `channel="run-42"` matches a line pushed to `serial:dut1`.
  - A glob binding picks up a stream created after the binding.
  - Agent A sends agent B a message with a log attachment of 73 lines.
    `get_message` shows them; they survive a retention pass that deleted
    the stream's lines (snapshot); an open-ended attachment keeps
    delivering new lines through `get_log_attachment`.
  - Two agents claim `serial:dut1`: the second is refused until the first
    releases or the claim expires, and each step leaves a marker.

**M3. Web UI: chats, pairing, PWA**
- The shell, navigation and look (section 9.6), the server-rendered pages
  and partials with their `<template>` twins.
- `POST /api/messages` from a session: the bundle's form and the no-JS
  post. `POST /api/marks`.
- Pairing with QR and sessions, CSRF.
- `/agents`: token create and revoke, the client snippets.
- A minimal server-rendered `/streams` (status, rate, last activity).
- The bundle: ChatPage (chat list, conversation, logs pane), Composer,
  UploadQueue with IndexedDB, ResizeToJpeg, LiveFeed, ConversationView,
  QuestionCard, and the logs view parts LogView, AnsiParser and FilterBar.
- Manifest, share target, service worker.
- *Done when:*
  - A photo taken on a phone reaches an agent through `get_message`.
  - An `ask_human` question shows its buttons on the phone, and tapping one
    returns the choice to the waiting agent.
  - The page is usable at 375 px wide and switches to the rail at 600 px
    and to two panes at 840 px.
  - Selecting a range in the logs view and attaching it reaches an agent
    through `get_message` with the same lines, filters included.
  - Home on the phone is the chat list with "Needs you" on top; a question
    can be answered from there, and the quick drop sends a photo in two taps.
  - Badges and the document title update when a question arrives.
  - In a bound channel, the logs pane (large screens) or the "Logs" toggle
    (phone) shows the bound streams live. A marker appears both there and as
    an event row in the chat.
  - `TestThePageWorksWithoutTheBundle` passes.

**M4. Syslog, netconsole, OTLP**
- Listeners, parsers, allowlist, loss counters.
- OTLP/HTTP logs (section 5.8), protobuf and JSON, gzip.
- *Done when:*
  - `logger -n <host> -P 5514 -T hello` and a UDP send both land.
  - A VM's netconsole output lands under its configured name.
  - An OTel Collector with an `otlphttp` exporter, and Vector with an
    `opentelemetry` sink, both deliver logs that appear with the right
    stream, severity and source time.

**M5. Serial, Windows push, live view**
- Server-side serial: local, RFC 2217 and raw TCP, reconnect markers.
- The `serial_write` and `serial_control` MCP tools, for local and
  RFC 2217 ports (claims exist since M2).
- `push --serial`, `--file --follow`, the spool.
- Windows binaries from CI.
- The `/live` page itself (its LogView, AnsiParser and FilterBar exist
  since M3), and the full Sources page (`/streams`, section 9.6.5).
- *Done when:*
  - Unplugging and replugging a USB adapter resumes the stream with markers.
  - A ser2net port in RFC 2217 mode gets its baud rate from cafard's
    config, and a DTR pulse reaches the far end.
  - Two agents contend for `serial:dut1` and only the claim holder can
    write or toggle lines.
  - `cafard.exe push --serial COM5` works; the user tests it on Windows.
  - `/live` follows 1000 lines per second without the tab freezing.

**M6. Control and polish**
- `cafard term`, `search` (FTS5), the `cafard mcp` stdio proxy.
- `deploy/` complete (install, units, OpenRC, container drop-in, README) and
  `scripts/devdeploy.sh`.
- *Done when:*
  - `sudo deploy/install.sh` on a fresh Debian container and on an Alpine
    container both produce a running service that passes `/healthz`.

**M7 (optional). Notifications**
- Web Push (VAPID) to the phone when a question or direct message arrives
  for a human. This needs HTTPS. Do it only if the user asks after M6.

**M8 (optional). OAuth 2.1 for MCP**
- As in the MCP authorization spec: protected resource metadata
  (RFC 9728), authorization server metadata (RFC 8414), authorization code
  with PKCE, dynamic client registration (RFC 7591) if the clients need it.
- Approval page for a signed-in human; issued tokens map to principals and
  scopes exactly like static ones, which keep working.
- Do it only if a client the user runs cannot send a static header.

---

## 15. Testing

### Go

- **Table tests** for:
  - syslog 3164 and 5424 samples, malformed ones included;
  - RFC 6587 framing;
  - netconsole extended headers;
  - OTLP protobuf and JSON requests (gzip and not), severity mapping, hex
    trace ids, the hand-encoded response checked against the reference
    types, partial success;
  - RFC 2217 negotiation, IAC escaping in both directions, `SET-CONTROL`
    sequences, and a peer that refuses option 44;
  - line splitting with `\r` and partial lines;
  - ANSI stripping, dedupe and truncation.
- **Fuzz tests** (`go test -fuzz`) for the syslog parser, the line splitter,
  the NDJSON ingest, the OTLP decoders and the RFC 2217 stream parser.
- **Store tests** on a temporary-directory database:
  - retention with a fake clock;
  - the database file mode is 0600;
  - cascade deletes work (foreign keys are on).
- **Messaging tests:**
  - inbox visibility rules (a DM never appears for a third party, over MCP;
    over SSE, tagged M3);
  - acks are per principal;
  - the rate limiter;
  - the `ask_human` round trip;
  - claim expiry and contention;
  - log attachments: range validation, snapshot threshold, channel form
    frozen at send time, grep and severity carried, "expired" state after
    retention, visibility inside a direct message;
  - channels: glob matching, a binding added later changes the timeline
    retroactively, `surface` modes, `channel=` resolution in every tool
    that takes it (each milestone covers the tools that exist by then),
    an unknown channel is an error that lists the existing ones, follows
    decide broadcast delivery, auto-archive with a fake clock, and config
    channels being reapplied at start.
- **MCP integration tests:** start the server on `127.0.0.1:0`, connect with
  the go-sdk client, and exercise every tool, including cancelling a
  `wait_for`.
- **Web tests:**
  - `TestThePageWorksWithoutTheBundle`;
  - cross-origin writes are refused;
  - pages escape hostile log lines and messages (`<script>`, ANSI injection);
  - a token is never echoed back;
  - the exposure guard refuses a routable bind with no admin password;
  - on the public interface every admin route answers 404, with or without
    the admin cookie.
- **Serial:** the port sits behind an `io.ReadWriteCloser` factory, tested
  with `net.Pipe` and fakes. RFC 2217 is tested against a fake Telnet peer
  in-process. Real hardware and a real ser2net are manual tests the user
  runs.
- `go test -race ./...` on dev-build is the pass that counts.

### TypeScript (vitest: pure logic, and renderers under happy-dom)

- `AnsiParser`.
- `Backoff`.
- The `UploadJob` state machine.
- `UploadQueue` with a fake repository and a fake HTTP client.
- `RendererRegistry` fallback.
- `model/parse` against fixture JSON. The fixtures are written by a Go test
  (from M1) into `internal/web/testdata/api/*.json`, so the DTOs on both
  sides cannot drift. CI checks that the fixtures are up to date.
- `LiveFeed` resume logic with a fake EventSource and a fake Clock.
- The renderers against the fixture page written by the Go template test
  (section 9.6.8), in vitest's `happy-dom` environment.

---

## 16. Open questions (ask the user, do not guess)

1. Which host runs `cafard serve`? It must be reachable by the devices and by
   the phone. This decides `public_url`, TLS, and whether devdeploy uses
   `pct` or plain ssh.
2. Are the serial ports attached to that host, or behind ser2net or Windows?
3. Is the default rate limit (30 messages per minute per principal) right?
4. Are the retention defaults right?

Until they are answered, implement with the defaults written here.
