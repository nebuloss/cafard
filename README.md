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

## Retention

| Key | Default |
|---|---|
| `retention.lines_max_age` | `336h` (14 days) |
| `retention.lines_max_bytes` | `2GB` |
| `retention.messages_max_age` | `2160h` (90 days) |
| `retention.blobs_max_bytes` | `5GB` |
| `retention.streams` | per-stream `lines_max_age` overrides, `{pattern, lines_max_age}`, first match wins |

The janitor runs every 10 minutes.

## Releases

A `v*` tag builds `cafard-linux-{amd64,arm64,armv7}` and
`cafard-windows-{amd64,arm64}.exe`, each with the frontend embedded, plus
`SHA256SUMS`.
