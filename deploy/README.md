# Hosting cafard

| File | What it is |
|---|---|
| `install.sh` | installs or upgrades the service, from a release or from a checkout |
| `cafard.service` | the hardened systemd unit |
| `cafard-container.conf` | a drop-in for unprivileged LXC containers, installed only when needed |
| `cafard.openrc` | the OpenRC script, for Alpine |
| `cafard.yaml.example` | every configuration key, commented; copied to `/etc/cafard.yaml` |

## Quick install

```sh
curl -fsSL https://raw.githubusercontent.com/nebuloss/cafard/main/deploy/install.sh | sudo sh
```

It downloads `cafard-linux-{amd64,arm64,armv7}` for this machine from the
latest release, and refuses to install it unless it matches the published
`SHA256SUMS`. Then it:

1. installs `/usr/local/bin/cafard` (written as `cafard.new`, then renamed, so
   a failed update leaves the running version alone);
2. creates the `cafard` system user, in the `dialout` group for serial ports;
3. writes `/etc/cafard.yaml` from the example, only if it does not exist
   (mode 0640, `root:cafard`), with `public_url` set to this machine's first
   address;
4. runs `cafard config` and stops on any error, with the reason;
5. creates the data directory (`/var/lib/cafard`, mode 0700);
6. when no admin password is set (a new install, or an upgrade from a
   version without one), sets a random one and prints it, once;
7. installs and starts the systemd unit, or the OpenRC script on Alpine, and
   waits for `GET /healthz`.

From a checkout it builds from source instead (Go 1.26; the frontend too if
`npm` is installed), so what gets installed is what you are looking at:

```sh
sudo deploy/install.sh
```

| Variable | Default | |
|---|---|---|
| `CAFARD_INSTALL_VERSION` | `latest` | a tag such as `v0.6.0` |
| `CAFARD_INSTALL_REPO` | `nebuloss/cafard` | for a fork |
| `CAFARD_INSTALL_PUBLIC_URL` | `http://<first address>:8790` | `public_url` for a new `/etc/cafard.yaml` |

With `curl | sudo sh`, set them after `sudo`: `curl ... | sudo
CAFARD_INSTALL_VERSION=v0.6.0 sh`.

The installer is idempotent. Run it again to upgrade: it never overwrites
`/etc/cafard.yaml` or the data directory.

### The admin password

There is one admin account. Its password opens an admin session (12 hours,
kept in memory, so a restart signs the admin out) in which the admin acts as
the `admin` person in the chats and manages tokens and pairing. No token
carries the admin scope; the password is the only way in.

Started with no password set, `cafard serve` makes a random one and prints
it on stdout. Under systemd or OpenRC, stdout is the journal or
`/var/log/cafard.log`, so the password would sit in a log. The installer
therefore sets one itself before the service starts (`cafard passwd
--if-unset`, fed from a pipe) and prints it only on your terminal. The
service then finds a password and prints nothing.

Forgot it, or want your own? Set it from a shell on the server, without the
old one, as the service user so the database stays its own:

```sh
sudo -u cafard cafard passwd -config /etc/cafard.yaml
```

It asks twice on a terminal, or reads one line from a pipe. Admin sessions
already open end when the service restarts. Signed in, the Agents page can
also change it (it asks for the current one, and signs the admin out
everywhere).

On a routable address cafard refuses to start while no admin password is
set.

### Signing people in

People other than the admin never type a password: the admin gives them a
one-time sign-in link, which signs their browser in for 30 days (renewed
while used). On the admin interface, the Pair page shows it as a QR code
for a phone (valid 5 minutes). From a shell, `cafard pair` prints one, for
up to 24 hours, to send by other means:

```sh
sudo -u cafard cafard pair -config /etc/cafard.yaml --name alice-phone --grant lab=read,message
sudo -u cafard cafard pair -config /etc/cafard.yaml --name alice --ttl 2h --scopes read,message --grant lab:dut1=read,message
```

The link starts with `public_url`. It opens on the public interface, so it
works from wherever people reach cafard. Scopes default to `read`,
`message` and `ingest`, and never include `admin`. They are a cap: the
person sees only what their grants cover, so give one with `--grant`, on
the project page (which makes such links with their QR code too), or
nothing but the admin's direct messages is visible.

### Upgrading from a version with an admin token

The upgrade removes the `admin` scope from every token and browser session
(they keep their other scopes), and the `/signin` page no longer takes a
token. Re-running the installer sets a random admin password and prints it;
upgrading another way (`scripts/devdeploy.sh`, a copied binary) lets the
server make one at its next start and print it to its log, so set your own
right after:

```sh
sudo -u cafard cafard passwd -config /etc/cafard.yaml
sudo rc-service cafard restart        # or: systemctl restart cafard
```

The old first-start token still works for `cafard push` and MCP, without
admin; revoke it on the Agents page if nothing uses it.

### Upgrading to projects

The first start of a version with projects moves what the database holds
into a project named `imported`, once, before it listens. On a large
database this rewrites the stream index of every log line and can take a
few minutes; let it finish (`rc-service cafard status`, or the journal,
says when it listens). Nothing is lost:

- every channel becomes `#imported/<name>`, with its messages, bindings,
  follows and read state; every log stream `serial:dut1` becomes
  `serial:imported/dut1` (the same for `syslog:`, `netconsole:`, `push:`,
  `otlp:`), with its lines, claims and the log attachments pointing at it;
- every old stream name that is a valid sink name becomes a sink of
  `imported`; a syslog or netconsole stream named by an IP address gets
  that address as the sink's source, so those senders keep landing in it;
- every principal but `admin` gets a grant on the whole of `imported`
  with the scopes its tokens and sessions had (people with neither get
  `read` and `message`), so agents, phones and push clients keep working:
  `cafard push --stream laptop` lands in `push:imported/laptop`, and an
  OTLP exporter whose `service.name` was a stream name in the sink of that
  name;
- then, at every start, each port of the `serial:` list and each
  `netconsole.names` entry not imported yet becomes the port, or a source
  address, of the sink of its name in `imported`, and the log warns that
  the keys can go. They still load in this version; remove them once the
  project page shows the sinks. Each item is recorded when it is
  imported, so a sink or port deleted on the project page does not come
  back from the file.

What needs the admin afterwards:

- **Syslog from hosts that named themselves** (`syslog:dut1` from the
  hostname in the message): the sink `imported/dut1` exists, but cafard
  never recorded the sender's address, so its new lines are dropped until
  the address is added to the sink on the project page. Sources shows how
  many syslog lines no sink took.
- A client that pushed with an explicit kind (`--stream serial:dut1`) now
  lands in the sink's push stream, `push:imported/dut1`.
- New agents and people see nothing until they get a grant (`cafard token
  create --grant`, `cafard pair --grant`, or the project page).

## Configuration

Edit `/etc/cafard.yaml`, check it, restart:

```sh
sudo cafard config -config /etc/cafard.yaml
sudo systemctl restart cafard        # or: rc-service cafard restart
```

Unknown keys are errors, and `cafard config` says which key. Projects,
their sinks (sender addresses, serial ports, tokens), their retention and
their members are set on the admin interface and kept in the database, not
in this file; the `serial:` list and `netconsole.names` still load, only to
be imported once into the project `imported` (see "Upgrading to
projects").

`public_url` is the address phones and agents use: it goes into pairing QR
codes and links. Behind a reverse proxy it is the proxy's address.

## DMZ: split interfaces

By default `http_listen` serves everything. To put cafard where the public
side faces a DMZ or the internet, give the admin pages an address of their
own:

```yaml
http_listen: 127.0.0.1:8790        # behind the reverse proxy and its TLS
public_url: https://cafard.example.org
secure: true
admin_listen: 10.0.50.2:8791       # the internal network only
```

- **`http_listen`, the public interface**: `/healthz`, the static files,
  `/mcp` (bearer tokens), ingest (`/api/log`, `/v1/logs`), the pages and the
  API of signed-in people (chats, live, sources, photos, their own
  sessions), and the sign-in links (`/p/...`). Its `/signin` page only says
  to ask the administrator for a link or QR code.
- **`admin_listen`, the admin interface**: the password sign-in, the Agents
  page and `/api/tokens`, the Pair page and `/api/pairings`, the Projects
  pages (`/projects/...`), the password change, and everything the public interface serves, so the admin can chat
  and watch logs from there too. Only this interface reads the admin
  session cookie.

On the public interface the admin routes answer 404, whatever the proxy in
front does, and an admin cookie that reaches it (cookies do not tell ports
apart) opens nothing. Both addresses are bound before anything is served;
if either fails, cafard does not start. They must differ.

Without an internal network, bind the admin interface to loopback and reach
it through SSH:

```yaml
admin_listen: 127.0.0.1:8791
```

```sh
ssh -L 8791:127.0.0.1:8791 cafard-host     # then open http://localhost:8791/signin
```

Do not route the admin interface through the public reverse proxy: that
would publish exactly what the split keeps private. If cafard terminates
TLS itself (`tls_cert`), both interfaces use it.

## The systemd unit

`cafard.service` runs as `cafard` with `ProtectSystem=strict`, no
capabilities, a `@system-service` system call filter,
`MemoryDenyWriteExecute`, `UMask=0077`, and `/var/lib/cafard` as the only
writable path. If you move `data_dir`, change `ReadWritePaths` too.

Two differences from a plain hardened web service:

- **Serial ports need `/dev`.** `PrivateDevices=yes` would give an empty
  `/dev`, so the unit keeps the real one and closes it with the device
  cgroup instead: `DevicePolicy=closed` with `DeviceAllow` for `ttyUSB*`,
  `ttyACM*` and `ttyS*` only, and `SupplementaryGroups=dialout`.
- **No `IPAddressDeny`.** cafard listens on the LAN (HTTP, later syslog and
  netconsole) and connects to RFC 2217 serial servers on the LAN.

For syslog on port 514, uncomment the two `CAP_NET_BIND_SERVICE` lines.

### In an unprivileged LXC container

The kernel refuses some of the unit's mount namespaces there, and systemd
fails the service with `226/NAMESPACE`. The installer tries the strict unit
first, and only on that failure installs `cafard-container.conf` as
`/etc/systemd/system/cafard.service.d/container.conf`. It turns off what the
container cannot grant and keeps `ProtectSystem`, `ProtectHome`,
`NoNewPrivileges`, the system call filter and the empty capability set.

## Serial ports in a Proxmox container

The container sees only the devices the host passes through. In
`/etc/pve/lxc/<id>.conf` on the Proxmox host:

```
dev0: /dev/ttyUSB0,gid=20
```

`gid` is the `dialout` group *inside* the container (20 on Debian and
Alpine; check with `getent group dialout`). Restart the container, then
check `ls -l /dev/ttyUSB0` inside it. Add `dev1:`, `dev2:` for more ports.
USB adapters can change number when replugged; on the host,
`ls -l /dev/serial/by-id/` shows which `ttyUSB` is which board.

The same works from the web UI: Container, Resources, Add, Device
Passthrough. Then give the port to a sink on its project's page (kind
"local device", the `/dev/serial/by-id/...` path); it opens as soon as the
sink is saved.

## Behind a reverse proxy

cafard streams: the web UI uses Server-Sent Events and MCP calls such as
`wait_for` stay open for minutes. The proxy must not buffer responses and
must allow long reads. Photos are uploaded in the request body.

nginx:

```nginx
server {
    listen 443 ssl;
    server_name cafard.lab.example;
    # ssl_certificate ...;

    client_max_body_size 256m;          # photos, and 16 MiB log batches

    location / {
        proxy_pass http://127.0.0.1:8790;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;

        proxy_buffering off;            # SSE and streamed MCP responses
        proxy_request_buffering off;    # uploads go straight through
        proxy_read_timeout 1h;          # wait_for and idle SSE streams
        proxy_send_timeout 1h;
    }
}
```

Caddy:

```
cafard.lab.example {
    reverse_proxy 127.0.0.1:8790 {
        flush_interval -1
        transport http {
            read_timeout 1h
        }
    }
}
```

Then, in `/etc/cafard.yaml`:

```yaml
http_listen: 127.0.0.1:8790
public_url: https://cafard.lab.example
secure: true
```

`secure: true` marks session cookies HTTPS-only. Clients that send an
`Origin` header to `/mcp` must match cafard's origin, `public_url` or
`mcp.allowed_origins`.

## TLS

Either a reverse proxy terminates it (above), or cafard does:

```yaml
tls_cert: /etc/cafard/tls.crt
tls_key: /etc/cafard/tls.key
secure: true
public_url: https://10.0.50.2:8790
```

The files must be readable by the `cafard` user (`chgrp cafard`, mode 0640).
The phone's installable app (PWA) and its share target need HTTPS, or
`localhost`; plain HTTP on the LAN works for everything else.

## The Windows push client

Each release has `cafard-windows-amd64.exe` and `cafard-windows-arm64.exe`
(the same program as the server). Give the machine a device token, on the
server:

```sh
sudo -u cafard cafard token create -config /etc/cafard.yaml \
  --name lab-pc --kind device --scopes ingest
```

then, in PowerShell on Windows:

```powershell
$env:CAFARD_SERVER = "http://10.0.50.2:8790"
$env:CAFARD_TOKEN  = "cfd_..."
Get-Content -Wait C:\logs\build.log | .\cafard.exe push --stream lab-pc
```

`cafard push` retries while the server is unreachable. Reading a COM port
directly (`--serial COM3`) and following a file (`--file`) come with serial
ingest.

## Running it

```sh
systemctl status cafard          # rc-service cafard status
journalctl -u cafard -f          # tail -f /var/log/cafard.log
curl http://127.0.0.1:8790/healthz
```

## Updating

Re-run the installer, from curl or from a checkout. It writes the new binary
beside the old one, moves it into place only once the download or build
succeeded, and restarts the service. Database migrations run at start and
are idempotent; there is no separate step. Pin a version with
`CAFARD_INSTALL_VERSION=v0.6.0`. Going back to an older version after a
migration is not supported.

For development builds there is `scripts/devdeploy.sh` in the repository: it
builds on the build host, pushes only the binary (never the database or
`cafard.yaml`) and restarts the service. Its version is `dev-<sha>`, never a
release number.

```sh
PVE=root@pve-host CT=405 scripts/devdeploy.sh    # a Proxmox container, via pct
TARGET=admin@10.0.50.2 scripts/devdeploy.sh      # any host, via ssh
```

## Uninstalling

```sh
sudo systemctl disable --now cafard     # or: rc-service cafard stop; rc-update del cafard
sudo rm -rf /etc/systemd/system/cafard.service /etc/systemd/system/cafard.service.d \
  /etc/init.d/cafard /usr/local/bin/cafard
# The data, which holds messages and photos, and the configuration:
sudo rm -rf /var/lib/cafard /etc/cafard.yaml
```
