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
6. on a new install, creates the `admin` principal and its token and prints
   the token, once;
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

### The admin token

Started on an empty database, `cafard serve` creates the `admin` principal
and prints its token on stdout. Under systemd or OpenRC, stdout is the
journal or `/var/log/cafard.log`, so the token would sit in a log. The
installer therefore creates the principal and the token itself, before the
first start (`cafard token create --name admin --kind human --scopes
ingest,read,message,admin`), and prints the token only to your terminal. The
service then finds a principal and prints nothing.

Lost it? Make another, as the service user so the database stays its own:

```sh
sudo -u cafard cafard token create -config /etc/cafard.yaml \
  --name admin --kind human --scopes ingest,read,message,admin
cafard token list -config /etc/cafard.yaml
```

On a routable address cafard refuses to start while no admin token exists.

## Configuration

Edit `/etc/cafard.yaml`, check it, restart:

```sh
sudo cafard config -config /etc/cafard.yaml
sudo systemctl restart cafard        # or: rc-service cafard restart
```

Unknown keys are errors. The sections at the end of the example (syslog,
netconsole, OTLP, messaging, images, channels, serial) are commented out
until the version you run has the feature; uncommenting one too early stops
cafard from starting, and `cafard config` says which key.

`public_url` is the address phones and agents use: it goes into pairing QR
codes and links. Behind a reverse proxy it is the proxy's address.

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
Passthrough.

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
