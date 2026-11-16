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
