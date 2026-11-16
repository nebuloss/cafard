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

## Agents (MCP)

cafard serves MCP over streamable HTTP at `/mcp`, with a bearer token:

```sh
claude mcp add --transport http cafard http://10.0.50.2:8790/mcp \
  --header "Authorization: Bearer cfd_..."
```

A token only sees the tools its scopes allow. Requests that carry an
`Origin` header must come from cafard's own origin, `public_url`, or
`mcp.allowed_origins`.

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
