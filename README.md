# cafard

> *cafard* (French): a snitch, and a cockroach.

cafard is a self-hosted server for a lab network. It collects logs and photos
from devices and people, serves them to AI agents over MCP, and passes messages
between agents and the humans they work with. See `PLAN.md` for the whole
design.

**Status: milestone M1.** Logs arrive over HTTP (`POST /api/log`, or
`cafard push`), and agents read them over MCP: `read`, `tail`, `wait_for`,
`context`, `mark`, `list_streams`, `whoami`. Messaging, the web UI and the
other sources come in the next milestones.

## Build

The frontend is a TypeScript bundle that the Go binary embeds, so build it
first:

```sh
npm ci
npm run typecheck && npm test && npm run build
go build ./cmd/cafard
```

A binary built without the bundle still works; it just has no live updates.
Go 1.26 and no C compiler are needed (`CGO_ENABLED=0` is how the releases are
built).

## Run

```sh
cafard version
cafard config                      # validate and print the effective configuration
cafard serve -http-listen 127.0.0.1:8790 -data-dir ./data
curl http://127.0.0.1:8790/healthz # ok dev
```

The configuration is read from `/etc/cafard.yaml` (or `-config`, or
`CAFARD_CONFIG`). Every key can also be set by a flag or an environment
variable, which take precedence in that order over the file:

| File key | Flag | Environment | Default |
|---|---|---|---|
| `data_dir` | `-data-dir` | `CAFARD_DATA_DIR` | `/var/lib/cafard` |
| `http_listen` | `-http-listen` | `CAFARD_HTTP_LISTEN` | `0.0.0.0:8790` |
| `public_url` | `-public-url` | `CAFARD_PUBLIC_URL` | none |
| `secure` | `-secure` | `CAFARD_SECURE` | `false` |
| `tls_cert`, `tls_key` | `-tls-cert`, `-tls-key` | `CAFARD_TLS_CERT`, `CAFARD_TLS_KEY` | none |
| `log_level` | `-log-level` | `CAFARD_LOG_LEVEL` | `info` |

Unknown keys in the file are an error, so a typo is caught by `cafard config`.

## First start

On an empty database `cafard serve` creates the `admin` principal and an
admin token, and prints the token once. Only its hash is stored. Agents get
tokens of their own:

```sh
cafard token create --name claude-laptop --kind agent --scopes read,message
cafard token list
cafard token revoke 3
```

Scopes are `ingest`, `read`, `message`, `serial_write` and `admin`. On a
routable address cafard refuses to start unless an admin token exists.

## Sending logs

```sh
journalctl -f | cafard push --server http://10.0.50.2:8790 --token cfd_... --stream laptop
echo "hello" | curl -H "Authorization: Bearer cfd_..." --data-binary @- \
  "http://10.0.50.2:8790/api/log?stream=laptop&severity=info"
```

`--server` and `--token` may also come from `CAFARD_SERVER` and
`CAFARD_TOKEN`. The body is plain text (one entry per line) or
`application/x-ndjson` with `{text, ts?, severity?, host?, app?, stream?}`
per line. A stream name without a kind becomes `push:<name>`.

## Network sources: syslog, netconsole, OTLP

**Anyone on the LAN can write syslog and netconsole lines.** Both protocols
are unauthenticated by nature. `syslog.allow` narrows who may send syslog;
nothing can stop a host from claiming another host's name.

```yaml
syslog:
  udp: ":5514"                 # "" disables a listener
  tcp: ":5514"
  allow: ["10.0.50.0/24"]      # optional, CIDRs or addresses; file-only
netconsole:
  udp: ":6666"
  names: { "10.0.50.42": dut1 } # file-only
otlp:
  enabled: true
  max_body_mib: 8
```

### Syslog

RFC 5424 and RFC 3164 over UDP and TCP, port 5514 by default (514 needs
privileges). TCP takes RFC 6587 octet counting or newline-delimited
messages, picked from the first byte of each connection. A line that does
not parse is kept whole at severity 6. Lines land in
`syslog:<hostname in the message, else the source IP>`, with the facility,
proc id, msg id and structured data in the entry's `meta`.

```sh
logger -n 10.0.50.2 -P 5514 -T hello                # TCP
logger -n 10.0.50.2 -P 5514 -T --octet-count hello  # TCP, RFC 6587 octet counting
logger -n 10.0.50.2 -P 5514 -d hello                # UDP
```

rsyslog forwarding: `*.* @@10.0.50.2:5514` (TCP) or `*.* @10.0.50.2:5514`
(UDP). A TCP stream shows as down once its last connection closes (each
`logger -T` run is one connection); UDP streams are only ever idle. UDP
lines are dropped, and counted on the stream, when the server cannot keep up.

### Netconsole

The kernel log over UDP port 6666, in the plain and the extended format
(`<level>,<seq>,<ts>,<flags>;msg` with its ` KEY=value` lines, which go to
`meta`). Lines land in `netconsole:<name from netconsole.names, else the
source IP>`. On the device's kernel command line:

```
netconsole=@/,6666@<server-ip>/      # plain
netconsole=+@/,6666@<server-ip>/     # extended: severity and sequence numbers
```

or at run time: `modprobe netconsole netconsole=+@/eth0,6666@10.0.50.2/`.
Add `loglevel=7` (or `dmesg -n 8`) to get debug messages too.

### OpenTelemetry logs (OTLP/HTTP)

`POST /v1/logs`, protobuf or JSON, optionally gzip, with a bearer token
that has the `ingest` scope (`cafard token create --name collector --kind
device --scopes ingest`). Records land in `otlp:<service.name, else
host.name, else unknown>`. The severity number maps to syslog levels
(the text is used when the number is unset), `time_unix_nano` (else the
observed time) becomes the source time, and attributes, trace and span ids
go to `meta`. Bodies are limited to `otlp.max_body_mib` after
decompression; a busy server answers 503 with `Retry-After`. gRPC (4317),
traces and metrics are not accepted.

OpenTelemetry Collector:

```yaml
exporters:
  otlphttp/cafard:
    logs_endpoint: http://10.0.50.2:8790/v1/logs
    compression: gzip
    encoding: proto          # or json
    headers:
      Authorization: "Bearer cfd_..."
service:
  pipelines:
    logs:
      receivers: [filelog]
      exporters: [otlphttp/cafard]
```

Vector (0.51 or later, for the `otlp` codec; events must have the OTLP
shape, from an `opentelemetry` source or a `remap`):

```yaml
sinks:
  cafard:
    type: opentelemetry
    inputs: [otel.logs]
    protocol:
      type: http
      uri: http://10.0.50.2:8790/v1/logs
      method: post
      compression: gzip
      encoding:
        codec: otlp
      request:
        headers:
          Authorization: "Bearer cfd_..."
```

With the older `codec: json`, Vector sends newline-delimited objects;
cafard accepts those too.

Fluent Bit:

```
[OUTPUT]
    Name      opentelemetry
    Match     *
    Host      10.0.50.2
    Port      8790
    Logs_uri  /v1/logs
    Header    Authorization Bearer cfd_...
    Compress  gzip
```

OpenTelemetry SDKs: `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://10.0.50.2:8790/v1/logs`,
`OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/protobuf` and
`OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer cfd_..."`.
## Serial consoles

List the ports in `/etc/cafard.yaml` (the `serial:` list is file-only).
Each one becomes the stream `serial:<name>`; lines end on `\n`, `\r\n` or
a lone `\r`, and a prompt with no newline is sent after 500 ms.

```yaml
serial:
  - name: dut1                        # lowercase letters, digits, . _ -
    device: /dev/serial/by-id/usb-FTDI_FT232R_A1B2C3-if00-port0
    baud: 115200                      # default 115200
    data_bits: 8                      # default 8
    parity: none                      # none | even | odd | mark | space
    stop_bits: 1                      # 1 | 1.5 | 2
    writable: false                   # agents may write and drive lines only if true
  - name: dut2
    rfc2217: 10.0.50.30:7001          # ser2net with telnet(rfc2217): line settings come from here
    baud: 115200
  - name: dut3
    tcp: 10.0.50.30:7002              # raw TCP: line settings are fixed on the far end
```

Set exactly one of `device`, `rfc2217` and `tcp`. Raw `tcp` ports take no
`baud`, `data_bits`, `parity` or `stop_bits`, since cafard cannot set them.

- **Use `/dev/serial/by-id/...` paths**, not `/dev/ttyUSB0`: the numbers
  change when adapters are replugged in another order, the by-id names do
  not. `ls -l /dev/serial/by-id/` lists them. The user cafard runs as
  needs the `dialout` group (and, in a container, the device passed in).
- **Unplugging is fine.** cafard writes a marker (`serial: disconnected
  from ...`), shows the stream as down, tries again every 1 s to 30 s, and
  writes `serial: reconnected after 12s: ...` when the port is back.
  Agents see the gap.
- **RFC 2217** is the way to reach a port on another machine: cafard sets
  the baud rate and framing, and can drive DTR, RTS and break (to reset a
  board). If the server refuses the option, cafard reads it as plain Telnet
  and the connect marker says why. Carrier detect changes reported by the
  server become markers.

A ser2net 4 configuration (`/etc/ser2net.yaml`) for the two network ports
above:

```yaml
connection: &dut2
  accepter: telnet(rfc2217),tcp,7001
  connector: serialdev,/dev/serial/by-id/usb-FTDI_FT232R_A1B2C3-if00-port0,115200n81,local
  options:
    kickolduser: true

connection: &dut3
  accepter: tcp,7002
  connector: serialdev,/dev/serial/by-id/usb-Prolific_PL2303-if00-port0,115200n81,local
```

`telnet(rfc2217)` is what lets cafard change the line settings (ser2net 3
calls it `remctl`). Without it, use `tcp:` in cafard's config.

### Pushing from a Windows PC

A board on a laptop's COM port needs no server of its own: `cafard push`
reads the port and sends the lines.

```bat
set CAFARD_SERVER=http://10.0.50.2:8790
set CAFARD_TOKEN=cfd_...
cafard.exe push --stream bench --serial COM5 --baud 115200 --echo
```

The port is read as 8N1. Windows lets one program open a COM port at a
time, so close PuTTY first. The port must open at start; if it disappears
later (adapter unplugged), push reopens it when it is back. Ctrl-C stops.

If PuTTY must keep the port, let it write a session log (Session >
Logging, "All session output", "Always overwrite") and follow the file:

```bat
cafard.exe push --stream bench --file C:\logs\putty.log --follow
```

`--follow` starts at the end of the file (`--from-start` also sends what it
already holds) and carries on when the file is truncated (a new PuTTY
session) or replaced by a new file (rotation). `--file` without `--follow`
sends the file and exits. The same flags work on Linux:
`cafard push --stream dut --serial /dev/ttyACM0`.

While the server is unreachable, push keeps the lines in a spool file and
sends them in order, each with the time it was read, when the server is
back or on the next push to the same server and stream. The spool lives in
the user cache directory (`~/.cache/cafard/spool` on Linux,
`%LocalAppData%\cafard\spool` on Windows), holds 100 MiB at most (newer
lines are dropped beyond that, and a line in the stream says how many),
and is deleted once delivered. `--spool PATH` puts it elsewhere; `--spool
off` keeps lines in memory only.

## Agents (MCP)

cafard serves MCP over streamable HTTP at `/mcp`, with a bearer token:

```sh
claude mcp add --transport http cafard http://10.0.50.2:8790/mcp \
  --header "Authorization: Bearer cfd_..."
```

A token only sees the tools its scopes allow. Requests that carry an
`Origin` header must come from cafard's own origin, `public_url`, or
`mcp.allowed_origins`.

## Messages, channels and claims (track A, M2)

Agents and humans talk through cafard. Over MCP, an agent can:

- `send` a message to a principal (a direct conversation only the two of
  them see), to `@humans` in a channel, or to a whole channel (default
  `general`), with up to 6 images and 5 log attachments (slices of a
  stream: `{stream or channel, from_seq, to_seq, grep, min_severity}`);
- read its `inbox`, `get_message` (images come back as image content,
  log attachments as their first 200 lines), `get_log_attachment`, `ack`,
  `wait_message`, and `ask_human` a question with choice buttons;
- `create_channel` bound to device streams (`run-42` bound to
  `serial:dut1`, or a glob such as `syslog:dut1*`), then pass
  `channel=run-42` to `read`, `tail`, `wait_for`, `list_streams` and
  `list_claims` instead of listing streams; `channel_timeline` shows the
  chat with the bound streams' markers (or errors);
- `claim` a stream before driving the device, `release` it after;
  `list_streams` shows who holds what. Every claim, release, expiry and
  binding change leaves a marker.

Resources `cafard://streams`, `cafard://stream/{id}`, `cafard://inbox`,
`cafard://message/{seq}` and `cafard://channel/{name}` mirror the tools;
`cafard://inbox` and `cafard://stream/{id}` accept subscriptions.

Scripts can post too, with a `message` token:

```sh
curl -H "Authorization: Bearer cfd_..." -H "X-Cafard: 1" \
  -F chat=chan:general -F text="bench photo" -F "files[]=@bench.jpg" \
  http://10.0.50.2:8790/api/messages
```

`GET /api/messages/{seq}`, `GET /api/messages/{seq}/attachments/{i}` and
`GET /blob/{sha}?v=thumb|agent` answer only for messages the caller can
see. Settings:

```yaml
messaging:
  rate_per_minute: 30       # per principal, burst 10
  burst: 10
  channel_create_per_hour: 10
  archive_after: 336h       # idle runtime channels are archived
images:
  max_file_mib: 20
  max_files: 10
  max_megapixels: 50
channels:                   # reapplied at every start
  - name: dut1
    title: DUT 1
    streams: [serial:dut1, "syslog:dut1*"]
    surface: markers        # none | markers | errors
```
## Web UI (track C)

Open `public_url` (or `http://<host>:8790`) in a browser:

- **Sign in** at `/signin` by pasting the admin token printed on first
  start, or any token of a person (`--kind human`). It opens a session in
  that browser; the token is not stored there.
- **Pair a phone** at `/pair` (admin): choose a person or type a new name,
  and scan the QR code with the phone within 5 minutes. Each code works
  once. The page also lists signed-in browsers, each with a revoke button.
- **Sources** (`/streams`) shows every source with its status, kind and
  transport, rate, last activity, last line and drops, and updates live.
- **Live** (`/live`) shows the last 500 lines (or the range in
  `?from_seq=&to_seq=`) with ANSI colours, filters in the URL (`streams`,
  `sev`, `re`, `kinds`), and follows new lines; scroll up to pause, `f` to
  toggle.
- **Agents** (`/agents`, admin) lists principals and tokens, creates a
  token (shown once, with ready-to-paste Claude Code, Codex, MCP JSON,
  OpenTelemetry and `cafard push` snippets) and revokes them.
- The theme (system, light, dark) and sign out are under "More".

Pages work without JavaScript; live updates need it. Set `secure: true`
behind HTTPS so the session cookie is HTTPS-only. Without the bundle
(`npm run build` before `go build`), the pages still render.

## Chats (track D, M3)

Home (`/`) is the chat list, with **Needs you** on top: questions agents
asked you (`ask_human`, or addressed to you by name), each answerable
right there with one button per choice or a free reply, then direct
conversations with unread messages. Below come the channels (pinned
first, then by last activity), the direct conversations, and **Show all**
(channels you unfollowed) and **Archived**. On a wide screen the overview
beside the list shows what needs you and the latest activity.

- **A chat** is `/c/<channel>` or `/dm/<principal>`: the latest 50
  messages, with **Older** for the ones before, filter chips (All,
  Questions, Events, Mine, With images), markers of the bound streams as
  one-line event rows (and error lines with the `errors` surface), and the
  composer at the bottom: text, **photos** or the **camera**, paste and
  drag and drop on a desktop, replies, and Ctrl+Enter to send. Photos are
  resized to 2560 px JPEG in the browser (tick "Keep original" to send
  them as they are), uploaded with progress, and queued in the browser
  while offline, then sent when the network is back.
- **Logs of a bound channel**: the "Logs" toggle (`?view=logs`) shows its
  streams merged and live; on a screen 1200 px wide or more they sit in
  a pane beside the chat, whose width you can drag. Tap a line for copy,
  context, a marker, "attach this line", "attach from here on" or "select
  a range"; shift-click selects a range on a desktop. **Attach** puts the
  range in the composer as a chip, with the severity and regexp filters
  you were looking at, so the agent reads exactly those lines. The `+`
  menu of the composer attaches the last 200 lines of a bound stream, or
  the stream from now on.
- **Channel settings** (the gear): title, topic, bound streams (globs are
  previewed as you type), what the chat shows from them, follow, pin,
  mute, and for the creator or an admin rename, archive and delete.
- **Quick drop**: the camera button on home opens a sheet with a chat
  picker (it remembers the last chat), the camera and the photo picker:
  a photo of a screen is two taps away. Without JavaScript it is the
  `/drop` page.
- **Share from Android**: install cafard as an app (the browser's "Install
  app" or "Add to home screen", over HTTPS), then "Share -> cafard" from
  any app opens the quick drop with the shared photos. iOS has no share
  target; post to `/api/messages` from a Shortcut instead (multipart
  `chat=chan:general`, `text`, `files[]`, with your token as a bearer).
- Unread counts, question badges, the document title (`(2) cafard`) and
  the app icon badge update live; a message counts as read once it has
  been on screen for a second. **Keys** on a desktop: `/` focus, `g c`,
  `g l`, `g s` to go to Chats, Live and Sources, `?` for the list, Esc.

Without JavaScript the chats still work: answering, sending text and
photos, older pages, filters and the logs view are links and plain
forms, and a page you open counts as read.

## Retention

| Key | Default |
|---|---|
| `retention.lines_max_age` | `336h` (14 days) |
| `retention.lines_max_bytes` | `2GB` |
| `retention.messages_max_age` | `2160h` (90 days) |
| `retention.blobs_max_bytes` | `5GB` |
| `retention.streams` | per-stream `lines_max_age` overrides, `{pattern, lines_max_age}`, first match wins |

The janitor runs every 10 minutes.

## Install as a service

```sh
curl -fsSL https://raw.githubusercontent.com/nebuloss/cafard/main/deploy/install.sh | sudo sh
```

On Debian (systemd) or Alpine (OpenRC), from a release (checked against
`SHA256SUMS`) or, run as `sudo deploy/install.sh`, from a checkout. It
creates the `cafard` user (in `dialout`), `/etc/cafard.yaml` from
`deploy/cafard.yaml.example`, the data directory, and the admin token,
printed once to your terminal and never to a log. Re-run it to upgrade.
`deploy/README.md` covers reverse proxies (SSE and long MCP calls), TLS,
serial ports in a Proxmox container, the Windows push client and updates.
`scripts/devdeploy.sh` pushes a development build to a box already
installed.

## Releases

A `v*` tag builds `cafard-linux-{amd64,arm64,armv7}` and
`cafard-windows-{amd64,arm64}.exe`, each with the frontend embedded, plus
`SHA256SUMS`.
