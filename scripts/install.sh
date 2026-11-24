#!/bin/sh
# cafard — install / update (the same command does both)
#
# Installs the message hub as a service and starts it. One static binary, no
# runtime, no dependencies behind it.
#
# Usage, as root:
#   curl -fsSL https://raw.githubusercontent.com/nebuloss/cafard/main/scripts/install.sh | sh
#
# A minimal Alpine has wget but not curl, so there:
#   wget -qO- https://raw.githubusercontent.com/nebuloss/cafard/main/scripts/install.sh | sh
#
# Pin a version, or bind somewhere else:
#   curl -fsSL .../install.sh | VERSION=v0.2.0 LISTEN=10.0.60.9:8790 sh
#
# Install a binary you built yourself instead of a release (scripts/deploy.sh
# does this for you):
#   BINARY_FILE=/tmp/cafard sh install.sh
#
# An update never leaves cafard down: the new binary has to run, and accept
# the configuration, before anything is touched; the database is copied
# first; and if the new version does not come up answering on both
# listeners, the previous one is put back, with the copied database if it
# cannot open the one the new version left.
#
# An update is a stop and a start: for the few seconds between them both
# listeners refuse connections, and the chat's event streams reconnect. The
# handover claudication does (the running process serves until the new one
# answers) comes with internal/handover, and with it a reload path here.
#
# Systems: Alpine Linux (OpenRC), Debian/Ubuntu (systemd)
#
# Layout: settings, output helpers, a platform layer holding every
# Alpine/Debian difference, the install steps, and main() at the bottom —
# which reads as the list of things this script does.

set -eu

PATH="/usr/local/bin:$PATH"
export PATH


# ═══ Settings, all overridable from the environment ═══════════════════════════

BIN_DIR="${BIN_DIR:-/usr/local/bin}"
SERVICE_NAME="${SERVICE_NAME:-cafard}"
SERVICE_USER="${SERVICE_USER:-cafard}"
GH_REPO="${GH_REPO:-nebuloss/cafard}"
GH_API="${GH_API:-https://api.github.com}"
GH_DL="${GH_DL:-https://github.com}"
VERSION="${VERSION:-}"                # install this tag instead of the newest
CONFIG_DIR="${CONFIG_DIR:-/etc/cafard}"
CONFIG_FILE="${CONFIG_FILE:-$CONFIG_DIR/config.yaml}"

# The settings below go into the config file the first time it is written.
# After that the file is the operator's, and an update changes a setting in
# it only when this run names that setting. Whether it did is captured before
# the defaults are applied, because after them "not mentioned" and
# "deliberately set to the default" look the same, and an update that
# silently put back a default would undo a decision nobody is re-making.
LISTEN_GIVEN="${LISTEN+yes}"
# The public listener: the chat app, /api/v1, /mcp and join links. The one
# published in the DMZ.
LISTEN="${LISTEN:-0.0.0.0:8790}"
ADMIN_LISTEN_GIVEN="${ADMIN_LISTEN+yes}"
# The admin UI and API: whoever reaches it can create people, agents and
# projects. It must differ from LISTEN (cafard refuses to start otherwise)
# and stay on the internal network.
ADMIN_LISTEN="${ADMIN_LISTEN:-0.0.0.0:8791}"
# Where join links and the MCP config the join script writes point. Empty
# picks http://<this machine's first global IPv4>:<LISTEN's port>, which is
# right until a proxy fronts cafard; then set the proxy's https:// address.
PUBLIC_URL_GIVEN="${PUBLIC_URL+yes}"
PUBLIC_URL="${PUBLIC_URL:-}"
# Comma-separated IPs or CIDRs the *proxy* in front connects from. Without
# them every client behind it shares the proxy's address, and one rate limit.
TRUSTED_PROXIES_GIVEN="${TRUSTED_PROXIES+yes}"
TRUSTED_PROXIES="${TRUSTED_PROXIES:-}"
# Only for a fresh config. On an update the state directory is whatever the
# config says, and moving it is not something an update does.
STATE_DIR_GIVEN="${STATE_DIR+yes}"
STATE_DIR="${STATE_DIR:-/var/lib/cafard}"

BOOT_TIMEOUT="${BOOT_TIMEOUT:-30}"    # seconds to wait for the first response
# Seconds to wait for the old process to let go of its listeners. Longer than
# the 10 s it gives its servers to shut down (cmd/cafard runServer), or the
# wait is pointless.
STOP_TIMEOUT="${STOP_TIMEOUT:-30}"
SET_PASSWORD="${SET_PASSWORD:-1}"     # 0 to leave the admin UI without a password
# A binary already on this machine, installed instead of downloading a release.
# For a build that has no release: a branch under test, a local fix.
BINARY_FILE="${BINARY_FILE:-}"
# Its expected digest, when the file came from somewhere you do not control.
BINARY_SHA256="${BINARY_SHA256:-}"
# Pre-update copies of the database, newest kept, in $STATE_DIR/backups.
# 0 skips the copy, and with it the rollback's way back from a migration.
BACKUPS_KEPT="${BACKUPS_KEPT:-5}"
QUIET="${QUIET:-0}"                   # 1 to drop the progress dots

BINARY="$BIN_DIR/cafard"
LOG_FILE="/var/log/${SERVICE_NAME}.log"
# What store.Open names it, and the two files SQLite keeps beside it in WAL
# mode. files/ is not copied: it is large and append-only, and a rollback
# never needs an older copy of it.
DB_FILES="cafard.db cafard.db-wal cafard.db-shm"


# ═══ Output ═══════════════════════════════════════════════════════════════════

# Colour only when something is there to read it. Piped into a provisioning
# log, escape codes are noise that outlives the install.
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'
  BOLD='\033[1m'; DIM='\033[2m'; NC='\033[0m'
else
  GREEN=''; YELLOW=''; RED=''; BOLD=''; DIM=''; NC=''
fi

info() { printf "${GREEN}[+]${NC} %s\n" "$*"; }
warn() { printf "${YELLOW}[!]${NC} %s\n" "$*"; }
fail() { printf "${RED}[x]${NC} %s\n" "$*"; }
step() { printf "${DIM}    %s${NC}\n" "$*"; }
die()  { fail "$*"; exit 1; }

tick()    { [ "$QUIET" = "1" ] || printf '.'; }
endline() { [ "$QUIET" = "1" ] || printf '\n'; }

dump_logs() {
  printf '\n'
  warn "last log lines:"
  service_tail
}


# ═══ Platform ═════════════════════════════════════════════════════════════════
#
# The only place Alpine and Debian differ. Every step below goes through these,
# so supporting another distribution means editing this section and nothing
# else.

detect_platform() {
  if [ -f /etc/alpine-release ]; then
    OS=alpine
  elif [ -f /etc/debian_version ]; then
    OS=debian
  else
    die "unsupported system (needs Alpine or Debian/Ubuntu)"
  fi
}

# Which build to fetch, from the machine's own architecture.
#
# Getting this wrong is not a friendly failure: the wrong binary runs and the
# kernel says "Exec format error", which says nothing about why.
detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  ASSET=cafard-linux-amd64 ;;
    aarch64|arm64) ASSET=cafard-linux-arm64 ;;
    *) die "no release for $(uname -m): cafard is released for amd64 and arm64 only (build one and install it with BINARY_FILE=…)" ;;
  esac
}

# The package manager's own output is a page of progress and locale
# warnings on a fresh Debian container: it goes to a file, shown only when
# the install fails.
pkg_install() {
  pkg_log=$(mktemp)
  case "$OS" in
    alpine)
      apk update -q >/dev/null 2>&1 || true
      apk add --no-cache "$@" >"$pkg_log" 2>&1 || { cat "$pkg_log"; warn "apk reported an error"; }
      ;;
    debian)
      export DEBIAN_FRONTEND=noninteractive
      apt-get update -qq >/dev/null 2>&1 || true
      LC_ALL=C apt-get install -y -qq "$@" >"$pkg_log" 2>&1 || { cat "$pkg_log"; warn "apt reported an error"; }
      ;;
  esac
  rm -f "$pkg_log"
}

# An account and a group of the same name, with no shell, at home in the
# state directory: the config file is root's and readable by the group, so
# the service can read it and not change it.
create_system_user() {
  case "$OS" in
    alpine)
      grep -q "^$SERVICE_USER:" /etc/group || addgroup -S "$SERVICE_USER" >/dev/null 2>&1 || true
      adduser -S -D -H -h "$STATE_DIR" -s /sbin/nologin -G "$SERVICE_USER" \
        "$SERVICE_USER" >/dev/null 2>&1 || true
      ;;
    debian)
      grep -q "^$SERVICE_USER:" /etc/group || groupadd --system "$SERVICE_USER" >/dev/null 2>&1 || true
      useradd --system --gid "$SERVICE_USER" --home-dir "$STATE_DIR" --no-create-home \
        --shell /usr/sbin/nologin "$SERVICE_USER" >/dev/null 2>&1 || true
      ;;
  esac
}

service_stop() {
  case "$OS" in
    alpine) rc-service "$SERVICE_NAME" stop >/dev/null 2>&1 || true ;;
    debian) systemctl stop "$SERVICE_NAME" 2>/dev/null || true ;;
  esac
}

service_running() {
  case "$OS" in
    alpine) rc-service "$SERVICE_NAME" status >/dev/null 2>&1 ;;
    debian) systemctl is-active --quiet "$SERVICE_NAME" ;;
  esac
}

service_start() {
  info "Starting $SERVICE_NAME"
  case "$OS" in
    alpine) rc-service "$SERVICE_NAME" start >/dev/null 2>&1 ;;
    # A rollback starts right after a version that crashed in a loop, which
    # may have used up the unit's start limit; that refusal says nothing
    # about the version being started now.
    debian) systemctl reset-failed "$SERVICE_NAME" 2>/dev/null || true
            systemctl start "$SERVICE_NAME" ;;
  esac
}

service_tail() {
  case "$OS" in
    alpine) tail -25 "$LOG_FILE" 2>/dev/null || true ;;
    debian) journalctl -u "$SERVICE_NAME" -n 25 --no-pager 2>/dev/null || true ;;
  esac
}

restart_hint() {
  case "$OS" in
    alpine) printf 'rc-service %s restart' "$SERVICE_NAME" ;;
    *)      printf 'systemctl restart %s' "$SERVICE_NAME" ;;
  esac
}

service_hints() {
  case "$OS" in
    alpine) step "logs:    tail -f $LOG_FILE" ;;
    debian) step "logs:    journalctl -u $SERVICE_NAME -f" ;;
  esac
  step "restart: $(restart_hint)   (a stop: refuses until it is back)"
}

service_install() {
  case "$OS" in
    alpine) write_openrc_service ;;
    debian) write_systemd_service ;;
  esac
}

# Nothing else rotates this file.
#
# systemd hands its unit's output to the journal, which has its own limits; the
# OpenRC path writes to a plain file that grows for as long as the service runs.
# cafard logs events (a sign-in, a redeemed link), not every request, so a
# week is usually small; the size ceiling is for the week it is not — debug
# left on, or an error repeating — since the database is on the same disk.
write_logrotate() {
  [ -d /etc/logrotate.d ] || return 0
  # maxsize rather than size: `size` replaces the time trigger outright, which
  # would leave a quiet log unrotated indefinitely. maxsize adds a ceiling to
  # the weekly schedule instead of replacing it, so the total is bounded at
  # roughly 8 x 32 MB regardless of traffic.
  cat > "/etc/logrotate.d/$SERVICE_NAME" <<EOF
$LOG_FILE {
  weekly
  maxsize 32M
  rotate 8
  compress
  delaycompress
  missingok
  notifempty
  copytruncate
  su root root
  create 640 $SERVICE_USER $SERVICE_USER
}
EOF
  # copytruncate rather than a restart: supervise-daemon holds the file open,
  # and rotating it out from under a running cafard would send every
  # subsequent line to a deleted inode.
  info "Log rotation configured (/etc/logrotate.d/$SERVICE_NAME)"
}

write_openrc_service() {
  info "Installing the OpenRC service"

  # The service opens the log after dropping privileges, and /var/log is
  # root-owned 0755 — so the file has to exist and be writable beforehand, or
  # the service dies before producing any output to explain why.
  #
  # Created only if absent: this script is also the update path, and `: >` would
  # throw away the log every time, taking with it whatever the operator was
  # about to read to find out why they were updating.
  [ -f "$LOG_FILE" ] || : > "$LOG_FILE"
  chown "$SERVICE_USER:$SERVICE_USER" "$LOG_FILE"
  chmod 640 "$LOG_FILE"
  write_logrotate

  openrc_script > "/etc/init.d/$SERVICE_NAME"
  chmod +x "/etc/init.d/$SERVICE_NAME"
  rc-update add "$SERVICE_NAME" default >/dev/null 2>&1 || true
}

# The OpenRC script, and the only copy of it: an install writes it, so there
# is no file in the repository to drift from what a box runs.
openrc_script() {
  cat <<EOF
#!/sbin/openrc-run
# cafard under OpenRC, written by scripts/install.sh: rerun it rather than
# editing this file, which the next update replaces.
# Variables are read by openrc-run, not by this file.
# shellcheck shell=sh disable=SC2034
#
# An Alpine container is a reasonable place to run one static binary, and it
# has no systemd. The hardening the systemd unit asks for has no OpenRC
# equivalent; what is kept is what OpenRC can express: an unprivileged account
# and a state directory only it can read. The rest is the container's job.

name="$SERVICE_NAME"
description="messages, photos and logs between people, agents and machines"

config="$CONFIG_FILE"
command="$BINARY"
command_args="serve -config \$config"
command_user="$SERVICE_USER:$SERVICE_USER"
pidfile="/run/\$RC_SVCNAME.pid"
umask=077

output_log="$LOG_FILE"
error_log="$LOG_FILE"

# Supervised, so a crash is a five-second gap rather than an outage.
supervisor="supervise-daemon"
respawn_delay=5
respawn_max=10
respawn_period=1800

# Wait out the shutdown before calling it stopped, as TimeoutStopSec does for
# systemd: longer than the 10 s cafard gives its servers, so a restart does
# not start the new process while the old one still holds the ports.
retry="TERM/30/KILL/5"

depend() {
    need net
    after firewall
}

start_pre() {
    checkpath --file --owner $SERVICE_USER:$SERVICE_USER --mode 0640 "\$output_log"

    # Refuse to start rather than start wrong: \`cafard config\` fails on an
    # unknown key or on listeners that overlap, and says which. Under
    # supervise-daemon a refusal here is one clear line in the log, where a
    # binary that exits at once would be respawned into the same error.
    if ! "\$command" config -config "\$config" >/dev/null 2>&1; then
        eerror "the configuration in \$config is not usable:"
        "\$command" config -config "\$config" 2>&1 | head -12
        return 1
    fi

    state_dir=\$("\$command" config -config "\$config" 2>/dev/null |
        sed -n 's/^state-dir: *"\\{0,1\\}\\([^"]*\\)"\\{0,1\\}\$/\\1/p')
    checkpath --directory --owner $SERVICE_USER:$SERVICE_USER --mode 0700 "\${state_dir:-/var/lib/cafard}"
}
EOF
}

write_systemd_service() {
  info "Installing the systemd service"
  systemd_unit > "/etc/systemd/system/$SERVICE_NAME.service"
  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME" >/dev/null 2>&1
}

# The systemd unit, and the only copy of it: deploy/cafard.service is this
# with the default paths, printed by sourcing this script
# (INSTALL_SH_SOURCED=1, then systemd_unit), so a unit a reader finds in the
# repository is the one an install gets.
systemd_unit() {
  cat <<EOF
[Unit]
Description=cafard - messages between people, agents and machines
Documentation=https://github.com/$GH_REPO
After=network-online.target
Wants=network-online.target

[Service]
Type=exec
User=$SERVICE_USER
Group=$SERVICE_USER
# Everything is in the config file, the state directory included: an
# environment variable that silently overrode it would make half a
# configuration, which is worse than either.
ExecStart=$BINARY serve -config $CONFIG_FILE
Restart=on-failure
RestartSec=2s

# cafard gives its servers 10 s to finish what they are answering, then
# stops; give systemd more patience than that. A stop and a start is the
# whole of an update until internal/handover, and with it a reload, exists.
KillSignal=SIGTERM
TimeoutStopSec=30s

# The database and the files are the service's alone, as the state directory
# itself is (0700): nothing it writes there is for another account to read.
UMask=0077

NoNewPrivileges=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectSystem=strict
ProtectHome=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_INET AF_INET6
RestrictNamespaces=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service
ReadWritePaths=$STATE_DIR

[Install]
WantedBy=multi-user.target
EOF
}


# ═══ Configuration ════════════════════════════════════════════════════════════
#
# The config file is the source of truth. It is written once, on the first
# install, commented like configs/config.example.yaml; after that it is the
# operator's, with their comments in it, and an update changes only the lines
# for the settings this run names. Every version of it is checked by the new
# binary's `cafard config` before it replaces the file, so a typo in LISTEN
# fails here, with nothing touched, rather than at the start that follows.

# The first global IPv4 of this machine, or empty: what a person on the same
# network types to reach it. hostname -I is Debian's; busybox has only `ip`.
first_ipv4() {
  ip -4 -o addr show scope global 2>/dev/null | awk '{ sub(/\/.*/, "", $4); print $4; exit }' |
    grep . || hostname -I 2>/dev/null | awk '{print $1}' || true
}

# A listener's address as something to connect to: a wildcard host is
# reachable on loopback, any other on itself.
connect_host() {
  host="${1%:*}"
  host="${host#[}"; host="${host%]}"
  case "$host" in
    ""|0.0.0.0|::) printf '127.0.0.1' ;;
    *:*)           printf '[%s]' "$host" ;;
    *)             printf '%s' "$host" ;;
  esac
}

# The address people reach a listener at, for the summary and the default
# public-url: a wildcard is this machine's address, not loopback.
reach_host() {
  case "${1%:*}" in
    ""|0.0.0.0|::|"[::]") ip="$(first_ipv4)"; printf '%s' "${ip:-127.0.0.1}" ;;
    *)                    connect_host "$1" ;;
  esac
}

default_public_url() {
  printf 'http://%s:%s' "$(reach_host "$LISTEN")" "${LISTEN##*:}"
}

# The values go between double quotes in YAML, unescaped; none of the
# settings has a reason to hold a quote or a backslash, so refuse them rather
# than escape them.
check_value() {
  case "$2" in
    *\"*|*\\*) die "$1 contains a quote or a backslash: refusing to write it into $CONFIG_FILE" ;;
  esac
}

# trusted-proxies as YAML list lines, from the comma-separated setting.
proxy_lines() {
  printf '%s\n' "$TRUSTED_PROXIES" | tr ',' '\n' | while read -r cidr; do
    cidr="$(printf '%s' "$cidr" | tr -d ' ')"
    [ -n "$cidr" ] || continue
    printf '  - "%s"\n' "$cidr"
  done
}

fresh_config() {
  cat <<EOF
# cafard's configuration: where it listens, where its state lives, and the
# admin password's hash.
# People, agents, projects and every setting the admin UI changes are in the
# database, not here.
#
# Written once by the installer, which afterwards changes only a setting it
# is given again (LISTEN=… sh install.sh), so your comments survive. An
# unknown key is an error. Restart the service after editing:
#     $(restart_hint)
# The annotated example is at
# https://github.com/$GH_REPO/blob/main/configs/config.example.yaml

# The public listener, published in the DMZ: the chat app, the HTTP API,
# the MCP server at /mcp, and join links.
listen: "$LISTEN"

# The admin UI and API. Required, and never the same address as \`listen\`:
# nothing reachable from the DMZ can create a person, an agent or a project.
# Keep it on the internal network: do not publish this port.
admin-listen: "$ADMIN_LISTEN"

# The database and the files people and agents send. It is as sensitive as
# everything sent to cafard.
state-dir: "$STATE_DIR"

# The address people and agents reach \`listen\` at, as it appears in join
# links and in the MCP config the join script writes. Empty means
# http://<listen>. Set the proxy's https:// address once one fronts cafard.
# The admin UI's settings can set another over it, without a restart.
public-url: "$PUBLIC_URL"

EOF
  if [ -n "$TRUSTED_PROXIES" ]; then
    echo "# The proxies in front of cafard, by address or CIDR: their"
    echo "# X-Forwarded-For and X-Forwarded-Proto are believed."
    echo "trusted-proxies:"
    proxy_lines
  else
    echo "# SET THIS if a proxy fronts cafard, or every client behind it shares"
    echo "# its address, and one rate limit. The value is the address the PROXY"
    echo "# connects from, by address or CIDR."
    echo "trusted-proxies: []"
    echo "#   - \"10.0.60.2\""
  fi
  cat <<EOF

# The hash of the admin password, which opens the admin UI. Written by
# \`cafard passwd -config $CONFIG_FILE\`; restart cafard after.
# admin-password-hash: "\$argon2id\$v=19\$m=19456,t=2,p=1\$…"

log:
  level: info    # debug, info, warn, error
  format: json   # json or text
EOF
}

# A top-level scalar replaced in place, or appended when the file has none.
# Only that line changes; a comment at its end goes with it.
set_scalar() {
  awk -v key="$1" -v val="$2" '
    !done && index($0, key ":") == 1 { print key ": \"" val "\""; done = 1; next }
    { print }
    END { if (!done) print key ": \"" val "\"" }'
}

# trusted-proxies replaced in place, with the lines under it that belong to
# it (its items, and comments indented with them), or appended.
#
# The lines go through the environment: awk -v would read escapes in them, and
# not every awk takes a newline there.
set_proxies() {
  PROXY_LINES="$(proxy_lines)" awk '
    function block() {
      if (ENVIRON["PROXY_LINES"] == "") print "trusted-proxies: []"
      else { print "trusted-proxies:"; print ENVIRON["PROXY_LINES"] }
    }
    skipping && /^([ \t]|- )/ { next }
    { skipping = 0 }
    !done && index($0, "trusted-proxies:") == 1 { block(); done = 1; skipping = 1; next }
    { print }
    END { if (!done) block() }'
}

# Put a new version of the config in place: root's and the service group's,
# readable by nobody else (it holds the admin password's hash), and only once
# the new binary accepts it — and the installed one too, on an update, or a
# rollback would come back to a file it refuses.
replace_config() {
  tmp="$1"
  chown "root:$SERVICE_USER" "$tmp"
  chmod 0640 "$tmp"
  if ! err="$("$NEW_BIN" config -config "$tmp" 2>&1 >/dev/null)"; then
    rm -f "$tmp"; rm -rf "$NEW_DIR"
    die "$NEW_VERSION refuses the configuration — nothing was changed: $err"
  fi
  if [ -n "$BEFORE" ] && [ -f "$CONFIG_FILE" ] &&
     ! err="$("$BINARY" config -config "$tmp" 2>&1 >/dev/null)"; then
    rm -f "$tmp"; rm -rf "$NEW_DIR"
    die "$BEFORE, which a rollback would return to, refuses the new configuration — nothing was changed: $err"
  fi
  mv -f "$tmp" "$CONFIG_FILE"
}

# Under umask 077, so no file on the way holds the admin password's hash
# readable by everyone before replace_config narrows it to the group.
write_config_file() {
  old_umask="$(umask)"
  umask 077
  write_config
  umask "$old_umask"
}

write_config() {
  mkdir -p "$CONFIG_DIR"
  chmod 0755 "$CONFIG_DIR"
  for v in LISTEN ADMIN_LISTEN PUBLIC_URL TRUSTED_PROXIES STATE_DIR; do
    eval "check_value $v \"\$$v\""
  done

  if [ ! -f "$CONFIG_FILE" ]; then
    info "Writing $CONFIG_FILE"
    [ -n "$PUBLIC_URL_GIVEN" ] || PUBLIC_URL="$(default_public_url)"
    fresh_config > "$CONFIG_FILE.new"
    replace_config "$CONFIG_FILE.new"
    CONFIG_WRITTEN=1
    return
  fi

  # The settings this run names, and nothing else.
  cp -p "$CONFIG_FILE" "$CONFIG_FILE.new"
  changed=""
  if [ -n "$LISTEN_GIVEN" ]; then
    set_scalar listen "$LISTEN" < "$CONFIG_FILE" > "$CONFIG_FILE.new"
    changed="$changed listen"
  fi
  if [ -n "$ADMIN_LISTEN_GIVEN" ]; then
    set_scalar admin-listen "$ADMIN_LISTEN" < "$CONFIG_FILE.new" > "$CONFIG_FILE.tmp"
    mv -f "$CONFIG_FILE.tmp" "$CONFIG_FILE.new"
    changed="$changed admin-listen"
  fi
  if [ -n "$PUBLIC_URL_GIVEN" ]; then
    set_scalar public-url "$PUBLIC_URL" < "$CONFIG_FILE.new" > "$CONFIG_FILE.tmp"
    mv -f "$CONFIG_FILE.tmp" "$CONFIG_FILE.new"
    changed="$changed public-url"
  fi
  if [ -n "$TRUSTED_PROXIES_GIVEN" ]; then
    set_proxies < "$CONFIG_FILE.new" > "$CONFIG_FILE.tmp"
    mv -f "$CONFIG_FILE.tmp" "$CONFIG_FILE.new"
    changed="$changed trusted-proxies"
  fi

  if [ -z "$changed" ] || cmp -s "$CONFIG_FILE" "$CONFIG_FILE.new"; then
    rm -f "$CONFIG_FILE.new"
    info "Keeping $CONFIG_FILE"
    # Still checked: a release can drop a key, and the start would refuse it.
    replace_check_only
    return
  fi
  info "Updating$changed in $CONFIG_FILE"
  replace_config "$CONFIG_FILE.new"
}

replace_check_only() {
  if ! err="$("$NEW_BIN" config -config "$CONFIG_FILE" 2>&1 >/dev/null)"; then
    rm -rf "$NEW_DIR"
    die "$NEW_VERSION refuses $CONFIG_FILE — nothing was changed: $err"
  fi
}

# The configuration as cafard reads it, defaults included, kept for the rest
# of the run: the ports to wait on, the state directory, the URLs to print.
# Read through the binary rather than the file, which may spell a value any
# way YAML allows.
load_config() {
  EFFECTIVE="$("$NEW_BIN" config -config "$CONFIG_FILE")" ||
    die "could not read $CONFIG_FILE back"
  dir="$(config_value state-dir)"
  if [ -n "$STATE_DIR_GIVEN" ] && [ "$dir" != "$STATE_DIR" ]; then
    rm -rf "$NEW_DIR"
    die "$CONFIG_FILE keeps the state in $dir, not STATE_DIR=$STATE_DIR — an update does not move it"
  fi
  STATE_DIR="$dir"
}

# A top-level scalar of the effective configuration, or empty.
config_value() {
  printf '%s\n' "$EFFECTIVE" |
    sed -n "s/^$1:[[:space:]]*['\"]\{0,1\}\([^'\"]*\)['\"]\{0,1\}[[:space:]]*$/\1/p" | head -1
}


# ═══ Steps ════════════════════════════════════════════════════════════════════

require_root() {
  [ "$(id -u)" -eq 0 ] || die "must be run as root"
  detect_platform
  info "System detected: $OS"
}

missing_tools() {
  out=""
  for tool in curl; do
    command -v "$tool" >/dev/null 2>&1 || out="$out $tool"
  done
  printf '%s' "$out"
}

ensure_tools() {
  missing="$(missing_tools)"
  [ -n "$missing" ] || return 0

  info "Installing:$missing"
  # shellcheck disable=SC2086
  pkg_install ca-certificates $missing
  still="$(missing_tools)"
  [ -z "$still" ] || die "still missing after install:$still"
}

resolve_release_tag() {
  if [ -n "$VERSION" ]; then
    printf '%s' "$VERSION"
    return 0
  fi
  curl -fsSL "${GH_API}/repos/${GH_REPO}/releases/latest" |
    sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
    head -1
}

# Check a download against the release's published digest.
#
# This installs an executable that then runs as a service, so "it downloaded"
# is not the same as "it is what was built". A missing SHA256SUMS is not fatal
# — a release predating it should still install — but a mismatch is.
verify_download() {
  file="$1"; name="$2"; tag="$3"

  if ! command -v sha256sum >/dev/null 2>&1; then
    warn "sha256sum not available: cannot verify $name"
    return 0
  fi

  sums="$(curl -fsSL "${GH_DL}/${GH_REPO}/releases/download/${tag}/SHA256SUMS" 2>/dev/null)" || sums=""
  if [ -z "$sums" ]; then
    warn "no SHA256SUMS in $tag: cannot verify $name"
    return 0
  fi

  want="$(printf '%s\n' "$sums" | awk -v n="$name" '$2 == n || $2 == "*"n {print $1; exit}')"
  if [ -z "$want" ]; then
    warn "$name is not listed in SHA256SUMS: cannot verify it"
    return 0
  fi

  got="$(sha256sum "$file" | awk '{print $1}')"
  if [ "$got" != "$want" ]; then
    rm -f "$file"
    die "$name does not match its published checksum — refusing to install it"
  fi
  info "Checksum verified"
}

# The version a binary reports, or empty. Also the pre-flight check: a binary
# that cannot print its version will not serve either — wrong architecture, a
# truncated copy, a file that is not cafard at all. `cafard version` prints
# "cafard v0.1.0 (commit …, built …)".
binary_version() {
  [ -x "$1" ] || return 0
  "$1" version 2>/dev/null | awk '$1 == "cafard" {print $2; exit}'
}

installed_version() { binary_version "$BINARY"; }

# Put the new binary in a temporary file and prove it runs, touching nothing
# that is serving. Sets NEW_BIN, NEW_VERSION and BEFORE.
fetch_binary() {
  BEFORE="$(installed_version)"
  NEW_DIR="$(mktemp -d)"
  NEW_BIN="$NEW_DIR/cafard"

  if [ -n "$BINARY_FILE" ]; then
    [ -f "$BINARY_FILE" ] || die "BINARY_FILE=$BINARY_FILE does not exist"
    cp "$BINARY_FILE" "$NEW_BIN"
    if [ -n "$BINARY_SHA256" ]; then
      got="$(sha256sum "$NEW_BIN" | awk '{print $1}')"
      [ "$got" = "$BINARY_SHA256" ] || { rm -rf "$NEW_DIR"; die "$BINARY_FILE does not match BINARY_SHA256 — refusing to install it"; }
      info "Checksum verified"
    fi
    label="$BINARY_FILE"
  else
    tag="$(resolve_release_tag)"
    [ -n "$tag" ] || die "could not resolve the latest release of $GH_REPO"
    step "$(uname -m) -> $ASSET"
    url="${GH_DL}/${GH_REPO}/releases/download/${tag}/${ASSET}"
    curl -fsSL "$url" -o "$NEW_BIN" || { rm -rf "$NEW_DIR"; die "download failed: $url"; }
    verify_download "$NEW_BIN" "$ASSET" "$tag"
    label="$tag"
  fi

  chmod 755 "$NEW_BIN"
  NEW_VERSION="$(binary_version "$NEW_BIN")"
  if [ -z "$NEW_VERSION" ]; then
    rm -rf "$NEW_DIR"
    die "the new binary ($label) does not run on this machine — nothing was changed"
  fi

  if [ -n "$BEFORE" ]; then
    info "Updating $BEFORE -> $NEW_VERSION"
  else
    info "Installing $NEW_VERSION"
  fi
}

ensure_user() {
  if id "$SERVICE_USER" >/dev/null 2>&1 && grep -q "^$SERVICE_USER:" /etc/group; then
    return 0
  fi
  info "Creating user $SERVICE_USER"
  create_system_user
  id "$SERVICE_USER" >/dev/null 2>&1 || die "could not create the user $SERVICE_USER"
}

ensure_dirs() {
  mkdir -p "$STATE_DIR"
  chown -R "$SERVICE_USER:$SERVICE_USER" "$STATE_DIR"
  # It holds everything sent to cafard and the tokens that open it, so it is
  # the service user's and nobody else's.
  chmod 700 "$STATE_DIR"
}

# Copy the database before a new version can migrate it.
#
# With the service stopped, so the three files are one consistent state: a
# copy of a live WAL database can catch the -wal half-written. A migration
# that goes wrong is otherwise unrecoverable, because a release refuses a
# database newer than itself (internal/store), so the old binary cannot open
# what the new one changed. Sets BACKUP_DIR.
backup_state() {
  BACKUP_DIR=""
  [ -f "$STATE_DIR/cafard.db" ] || return 0
  if [ "$BACKUPS_KEPT" = 0 ]; then
    warn "BACKUPS_KEPT=0: no copy of the database before updating"
    return 0
  fi

  dir="$STATE_DIR/backups"
  mkdir -p "$dir"
  chown "$SERVICE_USER:$SERVICE_USER" "$dir"
  chmod 700 "$dir"
  label="$(printf '%s' "${BEFORE:-unknown}" | tr -c 'A-Za-z0-9._-' '_')"
  BACKUP_DIR="$dir/pre-$label-$(date -u +%Y%m%d-%H%M%S)"
  mkdir "$BACKUP_DIR"
  for f in $DB_FILES; do
    [ -f "$STATE_DIR/$f" ] || continue
    if ! cp -p "$STATE_DIR/$f" "$BACKUP_DIR/$f"; then
      rm -rf "$BACKUP_DIR"
      return 1
    fi
  done
  chown -R "$SERVICE_USER:$SERVICE_USER" "$BACKUP_DIR"
  chmod 700 "$BACKUP_DIR"
  info "Database backed up: $BACKUP_DIR"

  # Newest first; everything past the limit goes. The names are ours and carry
  # no whitespace, so ls is safe here.
  # shellcheck disable=SC2012
  ls -1dt "$dir"/pre-*/ 2>/dev/null | tail -n +$((BACKUPS_KEPT + 1)) | while read -r old; do
    rm -rf "$old"
  done
}

# Put the checked binary in place, keeping the one it replaces beside it.
#
# Copied beside it and renamed over it, because $NEW_DIR is usually on another
# filesystem, and mv across one writes into the file in place — which a
# running executable refuses ("Text file busy"), and a half-written one is
# what the next start would run.
swap_binary() {
  mkdir -p "$BIN_DIR"
  if [ -x "$BINARY" ]; then cp -p "$BINARY" "$BINARY.prev"; fi
  cp "$NEW_BIN" "$BINARY.new"
  chmod 755 "$BINARY.new"
  mv -f "$BINARY.new" "$BINARY"
  rm -rf "$NEW_DIR"
}

# Both listeners, as address lines: the public one and the admin one.
listeners() {
  config_value listen
  config_value admin-listen
}

# Whether anything is listening on a TCP port, read from the kernel rather than
# by connecting: a process shutting down answers nothing and still holds the
# port.
port_listening() {
  hex="$(printf '%04X' "$1")"
  cat /proc/net/tcp /proc/net/tcp6 2>/dev/null |
    awk -v p="$hex" 'NR > 1 { n = split($2, a, ":"); if (a[n] == p && $4 == "0A") f = 1 } END { exit !f }'
}

any_port_held() {
  for addr in $(listeners); do
    port_listening "${addr##*:}" && return 0
  done
  return 1
}

# Wait for the old process to let go of both listeners.
#
# Until both are free the new process cannot bind, and on Alpine
# supervise-daemon respawns it into that failure until respawn_max and then
# gives up for good. Waiting on one alone is how claudication's 2026-09-24
# upgrade went wrong: its relay port was free while the admin one was still
# held.
#
# Bounded, so a wedged process delays an update rather than blocking it for
# ever. Reaching the bound is worth saying out loud.
wait_released() {
  i=0
  while any_port_held; do
    i=$((i + 1))
    if [ "$i" -gt "$STOP_TIMEOUT" ]; then
      endline
      warn "a port is still held after ${STOP_TIMEOUT}s: $(listeners | tr '\n' ' ')"
      return 1
    fi
    [ $((i % 2)) -eq 0 ] && tick
    sleep 1
  done
  [ "$i" -gt 0 ] && endline
  return 0
}

stop_and_release() {
  info "Stopping $SERVICE_NAME"
  service_stop
  wait_released || true
}

# The update: stop, copy the database, swap, start. cafard cannot hand over
# yet (internal/handover is still to come from claudication), so both
# listeners refuse connections from the stop until the new version answers.
stop_and_start() {
  if [ -n "$BEFORE" ] || service_running; then
    stop_and_release
  fi
  if ! backup_state; then
    # The old binary is still in place, so starting it is the whole undo.
    [ -z "$BEFORE" ] || service_start >/dev/null 2>&1 || true
    rm -rf "$NEW_DIR"
    die "could not copy the database in $STATE_DIR before updating — nothing was changed (BACKUPS_KEPT=0 skips this)"
  fi
  swap_binary
  set_admin_password
  service_start
  wait_ready
}

# A fresh install's admin UI has no password, so nobody can open it until
# someone runs `cafard passwd` on the box. Set one here and hand it back once,
# so the box is usable from the summary below. Only on a first install with
# no hash in the config: an update never touches the password, which is the
# operator's from the day it was printed.
set_admin_password() {
  ADMIN_PASSWORD=""
  [ "$SET_PASSWORD" = "1" ] || return 0
  [ -z "$BEFORE" ] || return 0
  [ -z "$(config_value admin-password-hash)" ] || return 0

  # 36 hex characters, 144 bits: nothing to mistype on a phone keyboard.
  pw="$(head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  if printf '%s\n' "$pw" | "$BINARY" passwd -config "$CONFIG_FILE" >/dev/null 2>&1; then
    ADMIN_PASSWORD="$pw"
    info "Admin password set"
  else
    warn "could not set the admin password: run 'cafard passwd -config $CONFIG_FILE', then restart"
  fi
}

# What /healthz on one listener says the serving process is, or empty. It
# answers "ok <version>".
serving_version() {
  curl -fsS -m 2 "http://$(connect_host "$1"):${1##*:}/healthz" 2>/dev/null |
    awk '$1 == "ok" {print $2; exit}'
}

# Ready means the NEW version answers on both listeners. Either alone is not
# enough: a process that came up on the public port and failed on the admin
# one would leave the box unmanageable, and look served.
ready() {
  for addr in $(listeners); do
    [ "$(serving_version "$addr")" = "$NEW_VERSION" ] || return 1
  done
  return 0
}

wait_ready() {
  i=0
  while [ "$i" -lt "$BOOT_TIMEOUT" ]; do
    if ready; then
      endline
      return 0
    fi
    # Only trust a "not running" reading once it has had a moment to start.
    if [ "$i" -gt 3 ] && ! service_running; then
      endline
      fail "the service stopped unexpectedly"
      dump_logs
      return 1
    fi
    i=$((i + 1))
    [ $((i % 2)) -eq 0 ] && tick
    sleep 1
  done

  endline
  got=""
  for addr in $(listeners); do
    got="$got $addr=$(serving_version "$addr")"
  done
  fail "$NEW_VERSION not serving on both listeners after ${BOOT_TIMEOUT}s (healthz:$got)"
  dump_logs
  return 1
}

# The update did not come up: put the previous binary back and serve that.
#
# First on the database as the failed version left it, which keeps anything
# written meanwhile. If the previous version will not open it — the new one
# migrated it, and a release refuses a schema newer than its own — the copy
# from before the update goes back in, and what the failed version left is
# moved aside, not deleted.
rollback() {
  [ -n "$BEFORE" ] && [ -x "$BINARY.prev" ] || return 1
  failed="$NEW_VERSION"
  warn "rolling back to $BEFORE"
  stop_and_release
  mv -f "$BINARY.prev" "$BINARY"
  NEW_VERSION="$BEFORE"
  service_start || true
  if wait_ready; then
    warn "$BEFORE is serving again; the failed version's log lines are above"
    return 0
  fi

  if [ -z "${BACKUP_DIR:-}" ]; then
    fail "$BEFORE did not come back either, and there is no copy of the database to return to"
    return 1
  fi
  warn "$BEFORE does not start on the database $failed left: putting back $BACKUP_DIR"
  stop_and_release
  label="$(printf '%s' "$failed" | tr -c 'A-Za-z0-9._-' '_')"
  aside="$STATE_DIR/backups/failed-$label-$(date -u +%Y%m%d-%H%M%S)"
  mkdir -p "$aside"
  for f in $DB_FILES; do
    [ -f "$STATE_DIR/$f" ] && mv -f "$STATE_DIR/$f" "$aside/$f"
  done
  for f in $DB_FILES; do
    [ -f "$BACKUP_DIR/$f" ] && cp -p "$BACKUP_DIR/$f" "$STATE_DIR/$f"
  done
  chown -R "$SERVICE_USER:$SERVICE_USER" "$aside" "$STATE_DIR"/cafard.db*
  service_start || true
  if wait_ready; then
    warn "$BEFORE is serving again, on the database from before the update"
    step "what $failed wrote is kept in $aside"
    return 0
  fi
  fail "$BEFORE did not come back either"
  step "the database $failed left is in $aside, the one from before it in $BACKUP_DIR"
  return 1
}

print_summary() {
  pub="$(config_value listen)"
  adm="$(config_value admin-listen)"
  url="$(config_value public-url)"
  [ -n "$url" ] || url="http://$(reach_host "$pub"):${pub##*:}"

  printf '\n'
  info "cafard $(installed_version) is running"
  step "chat:     $url/"
  step "admin UI: http://$(reach_host "$adm"):${adm##*:}/"
  service_hints

  if [ -n "${ADMIN_PASSWORD:-}" ]; then
    printf '\n'
    printf "  ${BOLD}admin password: %s${NC}\n" "$ADMIN_PASSWORD"
    step "shown once — nothing can recover it, but 'cafard passwd' sets a new one"
  fi

  if [ "${CONFIG_WRITTEN:-0}" = 1 ]; then
    printf '\n'
    step "next: open the admin UI, or 'cafard admin create person NAME' for a join link"
    warn "the admin listener ($adm) must not be published: keep port ${adm##*:} on the internal network, publish only ${pub##*:}"
    case "$url" in
      http://*) warn "served over plain HTTP: put a TLS proxy in front of ${pub##*:}, then set PUBLIC_URL and TRUSTED_PROXIES" ;;
    esac
  fi
}


# ═══ Run ══════════════════════════════════════════════════════════════════════

main() {
  require_root
  [ -n "$BINARY_FILE" ] || detect_arch
  ensure_tools

  # Nothing that is serving is touched until the new binary has run here and
  # accepted the configuration.
  fetch_binary
  ensure_user
  CONFIG_WRITTEN=0
  write_config_file
  load_config
  ensure_dirs

  # The unit before the stop, so the stop already runs under its patience.
  service_install

  ready=0
  stop_and_start && ready=1

  if [ "$ready" = 1 ]; then
    print_summary
  else
    rollback || true
  fi

  # A non-zero exit matters when this is piped into a provisioning tool, and a
  # rolled-back update is a failed one.
  [ "$ready" = "1" ]
}

# Sourced to print the systemd unit into deploy/cafard.service:
#   env -i PATH="$PATH" INSTALL_SH_SOURCED=1 sh -c '. scripts/install.sh; systemd_unit'
[ "${INSTALL_SH_SOURCED:-0}" = 1 ] || main "$@"
