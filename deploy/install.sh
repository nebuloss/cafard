#!/bin/sh
# Install or upgrade cafard as a system service.
#
#   curl -fsSL https://raw.githubusercontent.com/nebuloss/cafard/main/deploy/install.sh | sudo sh
#
# or, from a checkout:
#
#   sudo deploy/install.sh
#
# Piped from curl it downloads the release binary for this machine and checks
# it against the published SHA256SUMS, refusing to install anything it cannot
# verify. From a checkout it builds from source instead, so what gets
# installed is what you are looking at.
#
# IDEMPOTENT: run it again to upgrade. It never overwrites /etc/cafard.yaml or
# the data directory once they exist.
#
# POSIX sh on purpose: a fresh Alpine container has busybox, not bash.
set -eu

REPO=${CAFARD_INSTALL_REPO:-nebuloss/cafard}
VERSION=${CAFARD_INSTALL_VERSION:-latest}
# Only used when writing a new /etc/cafard.yaml; otherwise the file wins.
PUBLIC_URL=${CAFARD_INSTALL_PUBLIC_URL:-}

BINARY=/usr/local/bin/cafard
CONFIG=/etc/cafard.yaml
UNIT=/etc/systemd/system/cafard.service
DROPIN_DIR=/etc/systemd/system/cafard.service.d
OPENRC=/etc/init.d/cafard
SERVICE_USER=cafard
SERIAL_GROUP=dialout

say() { printf '> %s\n' "$*"; }
warn() { printf '! %s\n' "$*" >&2; }
die() { printf 'install: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run this as root, or through sudo"
[ "$(uname -s)" = Linux ] || die "this installer is for Linux"

# --- where are we? -----------------------------------------------------------

# A checkout is recognised by its own files rather than by $0, which is not a
# usable path when the script arrives on a pipe.
repo_root=""
case "$0" in
	*/*) candidate=$(cd "$(dirname "$0")/.." 2>/dev/null && pwd || true) ;;
	*) candidate=$(pwd) ;;
esac
if [ -n "$candidate" ] && [ -f "$candidate/go.mod" ] && [ -d "$candidate/cmd/cafard" ]; then
	repo_root=$candidate
fi

# The release publishes one binary per architecture; a wrong guess would be
# an "Exec format error" from the init system, which explains nothing.
case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	armv7* | armhf) arch=armv7 ;;
	*) [ -n "$repo_root" ] || die "no release build for $(uname -m); build from a checkout instead" ;;
esac

# systemd, OpenRC, or neither (a container with no init). Detected, because
# an Alpine container has no systemd and deserves a working service anyway.
INIT=none
if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
	INIT=systemd
elif command -v rc-update >/dev/null 2>&1 && [ -d /etc/init.d ]; then
	INIT=openrc
fi

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
	fetch_stdout() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO "$2" "$1"; }
	fetch_stdout() { wget -qO- "$1"; }
else
	fetch() { die "neither curl nor wget is installed, so $1 cannot be downloaded"; }
	fetch_stdout() { fetch "$1" -; }
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

# deploy_file NAME DEST copies deploy/NAME from the checkout, or downloads it
# from the release tag (falling back to main), so the unit matches the binary.
deploy_file() {
	if [ -n "$repo_root" ] && [ -f "$repo_root/deploy/$1" ]; then
		cp "$repo_root/deploy/$1" "$2"
	else
		fetch "https://raw.githubusercontent.com/$REPO/$VERSION/deploy/$1" "$2" 2>/dev/null ||
			fetch "https://raw.githubusercontent.com/$REPO/main/deploy/$1" "$2" ||
			die "cannot fetch deploy/$1"
	fi
}

# --- the binary --------------------------------------------------------------

install_from_source() {
	command -v go >/dev/null 2>&1 || die "go is not installed; run without a checkout to download a release"

	# The bundle is embedded into the binary, so it has to be built first. A
	# binary without it still works (pages are server-rendered) but has no
	# live updates.
	bundle="$repo_root/internal/web/assets/static/app/main.js"
	if command -v npm >/dev/null 2>&1; then
		say "building the frontend"
		(
			cd "$repo_root"
			# npm ci deletes node_modules, which may be a symlink to a shared
			# tree; only install when there is none.
			[ -e node_modules ] || npm ci --silent
			npm run build --silent
		) || die "the frontend build failed"
		# Give the build output back to whoever ran sudo, so their next
		# `npm run build` can overwrite it.
		if [ -n "${SUDO_UID:-}" ] && [ -n "${SUDO_GID:-}" ]; then
			chown -R "$SUDO_UID:$SUDO_GID" "$repo_root/internal/web/assets/static/app" "$repo_root/node_modules" 2>/dev/null || true
		fi
	elif [ -s "$bundle" ]; then
		say "npm is not installed; using the frontend bundle already built"
	else
		warn "npm is not installed, so the binary will not include the frontend bundle."
		warn "Pages still work, without live updates. Install node and re-run to add it,"
		warn "or install without a checkout to get a release binary, which has it."
	fi

	# dev-<sha>[-dirty], so it is never mistaken for a release. safe.directory
	# because root is not the owner of the checkout.
	git_c() { git -c safe.directory="$repo_root" -C "$repo_root" "$@"; }
	stamp=dev
	if command -v git >/dev/null 2>&1 && sha=$(git_c rev-parse --short HEAD 2>/dev/null); then
		stamp="dev-$sha"
		git_c diff --quiet HEAD 2>/dev/null || stamp="$stamp-dirty"
	fi

	say "building cafard $stamp"
	# Pure Go, no cgo, so this needs no C toolchain.
	(cd "$repo_root" && CGO_ENABLED=0 go build -trimpath \
		-ldflags="-s -w -X main.version=$stamp" -o "$TMP/cafard" ./cmd/cafard) ||
		die "the build failed"
}

install_from_release() {
	if [ "$VERSION" = latest ]; then
		say "asking GitHub for the latest release"
		VERSION=$(fetch_stdout "https://api.github.com/repos/$REPO/releases/latest" |
			sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
		[ -n "$VERSION" ] || die "cannot work out the latest version; set CAFARD_INSTALL_VERSION=vX.Y.Z"
	fi
	base="https://github.com/$REPO/releases/download/$VERSION"
	asset="cafard-linux-$arch"

	say "downloading $asset ($VERSION)"
	fetch "$base/$asset" "$TMP/cafard" || die "could not download $base/$asset"

	# This executable will run as a service, so what arrived has to be what
	# was built. Every way of not knowing that is a refusal.
	fetch "$base/SHA256SUMS" "$TMP/SHA256SUMS" ||
		die "could not fetch SHA256SUMS, so the download cannot be verified"
	command -v sha256sum >/dev/null 2>&1 ||
		die "sha256sum is not installed, so the download cannot be verified"
	want=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1; exit }' "$TMP/SHA256SUMS")
	[ -n "$want" ] || die "SHA256SUMS does not list $asset, so the download cannot be verified"
	got=$(sha256sum "$TMP/cafard" | cut -d' ' -f1)
	[ "$got" = "$want" ] || die "checksum mismatch, refusing to install
  expected $want
  got      $got"
	say "checksum verified"
}

if [ -n "$repo_root" ]; then
	say "installing from the checkout at $repo_root"
	install_from_source
else
	say "installing the published release for linux/$arch"
	install_from_release
fi

chmod 0755 "$TMP/cafard"
# Run it before installing it: a truncated download or a binary for the wrong
# architecture fails here, not as a service that will not start.
"$TMP/cafard" version >/dev/null 2>&1 || die "the new binary will not run on this machine"

# Written beside the old one and renamed: overwriting a running binary in
# place is "Text file busy", and a failed copy must leave the old one alone.
cp "$TMP/cafard" "$BINARY.new"
chmod 0755 "$BINARY.new"
mv -f "$BINARY.new" "$BINARY"
say "installed $BINARY ($("$BINARY" version))"

# --- account -----------------------------------------------------------------

# dialout owns the serial ports on Debian and Alpine; a minimal image may lack
# the group, and the unit names it.
if ! getent group "$SERIAL_GROUP" >/dev/null 2>&1; then
	if command -v groupadd >/dev/null 2>&1; then
		groupadd --system "$SERIAL_GROUP"
	else
		addgroup -S "$SERIAL_GROUP"
	fi
fi

if ! id "$SERVICE_USER" >/dev/null 2>&1; then
	say "creating the $SERVICE_USER user"
	if command -v useradd >/dev/null 2>&1; then
		useradd --system --user-group --home-dir /var/lib/cafard --no-create-home \
			--shell /usr/sbin/nologin "$SERVICE_USER"
	elif command -v adduser >/dev/null 2>&1; then
		# busybox: the group first and named, or `adduser -S` puts the
		# account in nogroup and cafard:cafard does not exist.
		addgroup -S "$SERVICE_USER" 2>/dev/null || true
		adduser -S -D -H -h /var/lib/cafard -s /sbin/nologin -G "$SERVICE_USER" "$SERVICE_USER"
	else
		die "cannot create the $SERVICE_USER user: no useradd or adduser"
	fi
fi
# Repairs an account made by hand without its group, on every run.
if ! getent group "$SERVICE_USER" >/dev/null 2>&1; then
	if command -v groupadd >/dev/null 2>&1; then
		groupadd --system "$SERVICE_USER"
		usermod -g "$SERVICE_USER" "$SERVICE_USER"
	else
		addgroup -S "$SERVICE_USER"
		addgroup "$SERVICE_USER" "$SERVICE_USER"
	fi
fi
if ! id -nG "$SERVICE_USER" | tr ' ' '\n' | grep -qx "$SERIAL_GROUP"; then
	if command -v usermod >/dev/null 2>&1; then
		usermod -aG "$SERIAL_GROUP" "$SERVICE_USER"
	else
		addgroup "$SERVICE_USER" "$SERIAL_GROUP"
	fi
fi

# --- configuration -----------------------------------------------------------

host_ip=$(hostname -I 2>/dev/null | cut -d' ' -f1 || true)
if [ -z "$host_ip" ] && command -v ip >/dev/null 2>&1; then
	host_ip=$(ip -4 -o addr show scope global 2>/dev/null | awk '{ sub("/.*", "", $4); print $4; exit }')
fi

new_config=no
if [ -f "$CONFIG" ]; then
	say "$CONFIG exists, left alone"
else
	say "writing $CONFIG"
	deploy_file cafard.yaml.example "$TMP/cafard.yaml"
	# Pairing QR codes need an address the phone can reach, and nothing in a
	# request says what it is behind a proxy. This machine's first address
	# is a better guess than localhost; the file says how to change it.
	[ -n "$PUBLIC_URL" ] || { [ -z "$host_ip" ] || PUBLIC_URL="http://$host_ip:8790"; }
	if [ -n "$PUBLIC_URL" ]; then
		sed "s|^# public_url:.*|public_url: $PUBLIC_URL|" "$TMP/cafard.yaml" >"$TMP/cafard.yaml.set"
		mv "$TMP/cafard.yaml.set" "$TMP/cafard.yaml"
	fi
	cp "$TMP/cafard.yaml" "$CONFIG.new"
	chmod 0640 "$CONFIG.new"
	mv "$CONFIG.new" "$CONFIG"
	new_config=yes
fi
# Re-applied on every run, so a file left root:root 0640 (unreadable by the
# service) is repaired by installing again.
chown "root:$SERVICE_USER" "$CONFIG"
chmod 0640 "$CONFIG"

# The same check the service runs before starting: fail here, with the reason,
# rather than as a service that keeps restarting.
if ! effective=$("$BINARY" config -config "$CONFIG" 2>&1); then
	printf '%s\n' "$effective" | sed 's/^/  /' >&2
	die "$CONFIG is not usable (the reason is above); fix it and run this again"
fi
cfg_value() { printf '%s\n' "$effective" | sed -n "s/^$1: *//p" | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'\$/\1/" | head -n 1; }
data_dir=$(cfg_value data_dir)
http_listen=$(cfg_value http_listen)
admin_listen=$(cfg_value admin_listen)
public_url=$(cfg_value public_url)
tls_cert=$(cfg_value tls_cert)

# 0700: the database holds messages, photos and token hashes.
install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0700 "$data_dir"
[ "$data_dir" = /var/lib/cafard ] ||
	warn "data_dir is $data_dir: the systemd unit only allows writes under /var/lib/cafard (ReadWritePaths)"

# --- first start -------------------------------------------------------------

# With no admin password, the server makes one at start and prints it on its
# stdout, which under a service manager is the journal or /var/log/cafard.log:
# a secret kept in a log. So the installer sets a random one first, on a new
# install and on an upgrade from a version without passwords, and shows it
# only on this terminal. The server then finds a password and prints nothing.
# --if-unset leaves one that is set alone.
admin_password=""
password=$(od -An -N18 -tx1 /dev/urandom | tr -d ' \n')
[ ${#password} -eq 36 ] || die "could not read 18 random bytes from /dev/urandom"
if ! passwd_out=$(printf '%s\n' "$password" | "$BINARY" passwd -config "$CONFIG" --if-unset 2>"$TMP/passwd.err"); then
	sed 's/^/  /' "$TMP/passwd.err" >&2
	die "could not set the admin password"
fi
case "$passwd_out" in
"Admin password set"*)
	say "set a new admin password"
	admin_password=$password
	;;
esac
unset password
# Run as root; the service must own its database.
chown -R "$SERVICE_USER:$SERVICE_USER" "$data_dir"

# --- the service -------------------------------------------------------------

scheme=http
[ -z "$tls_cert" ] || scheme=https
port=${http_listen##*:}
listen_host=${http_listen%:*}
case "$listen_host" in
	"" | 0.0.0.0 | "[::]" | "::") probe_host=127.0.0.1 ;;
	*) probe_host=$listen_host ;;
esac
health_url="$scheme://$probe_host:$port/healthz"

healthy() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSk --max-time 2 "$health_url" >/dev/null 2>&1
	elif command -v wget >/dev/null 2>&1; then
		wget -qO /dev/null -T 2 --no-check-certificate "$health_url" >/dev/null 2>&1 ||
			wget -qO /dev/null -T 2 "$health_url" >/dev/null 2>&1
	else
		return 1
	fi
}

# wait_healthy SECONDS polls /healthz once a second.
wait_healthy() {
	n=0
	while [ "$n" -lt "$1" ]; do
		healthy && return 0
		sleep 1
		n=$((n + 1))
	done
	return 1
}

started=no
case "$INIT" in
systemd)
	say "installing the systemd unit"
	deploy_file cafard.service "$TMP/cafard.service"
	install -m 0644 "$TMP/cafard.service" "$UNIT"
	systemctl daemon-reload
	systemctl enable cafard >/dev/null 2>&1
	systemctl restart cafard || true

	if ! wait_healthy 15; then
		# An unprivileged container cannot grant the mount namespaces the
		# unit asks for, and systemd fails it with 226/NAMESPACE (219/CGROUP
		# for the device policy). Strict first, relaxed only on that, so a
		# machine that can take the hardening keeps it.
		status=$(systemctl show -p ExecMainStatus --value cafard 2>/dev/null || true)
		if [ "$status" = 226 ] || [ "$status" = 219 ]; then
			say "the hardened unit cannot start here ($status): installing the container drop-in"
			mkdir -p "$DROPIN_DIR"
			deploy_file cafard-container.conf "$TMP/container.conf"
			install -m 0644 "$TMP/container.conf" "$DROPIN_DIR/container.conf"
			systemctl daemon-reload
			systemctl restart cafard || true
			wait_healthy 15 || true
		fi
	fi
	if healthy; then
		started=yes
		[ ! -f "$DROPIN_DIR/container.conf" ] ||
			warn "some kernel-level hardening is off here: see $DROPIN_DIR/container.conf"
	else
		journalctl -u cafard --no-pager --lines=25 2>/dev/null | sed 's/^/  /' >&2
		die "the service did not answer $health_url (its log is above)"
	fi
	;;
openrc)
	say "installing the OpenRC service"
	deploy_file cafard.openrc "$TMP/cafard.openrc"
	install -m 0755 "$TMP/cafard.openrc" "$OPENRC"
	rc-update add cafard default >/dev/null 2>&1 || true
	if [ -e /run/openrc/softlevel ]; then
		rc-service cafard restart >/dev/null 2>&1 || rc-service cafard start >/dev/null 2>&1 || true
		if wait_healthy 15; then
			started=yes
		else
			tail -n 25 /var/log/cafard.log 2>/dev/null | sed 's/^/  /' >&2
			die "the service did not answer $health_url (its log is above)"
		fi
	else
		warn "OpenRC is installed but did not boot this system, so the service was not started."
	fi
	;;
*)
	warn "no init system here, so nothing was set up to start cafard automatically."
	;;
esac

# --- what next ---------------------------------------------------------------

if [ -z "$public_url" ]; then
	public_url="$scheme://${host_ip:-$probe_host}:$port"
fi
public_url=${public_url%/}

printf '\n'
if [ "$started" = yes ]; then
	say "$("$BINARY" version) is running: $health_url answers."
else
	say "Start it by hand, as the $SERVICE_USER user:"
	say "  $BINARY serve -config $CONFIG"
fi

admin_url=$public_url
if [ -n "$admin_listen" ]; then
	admin_port=${admin_listen##*:}
	admin_host=${admin_listen%:*}
	case "$admin_host" in
	"" | 0.0.0.0 | "[::]" | "::") admin_host=${host_ip:-127.0.0.1} ;;
	esac
	admin_url="$scheme://$admin_host:$admin_port"
fi

if [ -n "$admin_password" ]; then
	cat <<EOF

+-- THE ADMIN PASSWORD -----------------------------------------------------
|
|   $admin_password
|
|  Shown once: only its hash is stored, and it is in no log.
|  Sign in with it at $admin_url/signin
|  Change it on the Agents page, or: sudo -u $SERVICE_USER $BINARY passwd -config $CONFIG
+--------------------------------------------------------------------------
EOF
fi

if [ "$new_config" = yes ] || [ -n "$admin_password" ]; then
	cat <<EOF

Next:

  1. Check public_url in $CONFIG ($public_url): sign-in links and QR
     codes point there. Then: $BINARY config -config $CONFIG
  2. Sign in at $admin_url/signin with the password above, then pair your
     phone at $admin_url/pair, or print a sign-in link for it:
       sudo -u $SERVICE_USER $BINARY pair -config $CONFIG --name my-phone
  3. Give each agent its own token and point it at $public_url/mcp:
       sudo -u $SERVICE_USER $BINARY token create -config $CONFIG \\
         --name claude-laptop --kind agent --scopes read,message
  4. Send logs: journalctl -f | cafard push --server $public_url --token cfd_... --stream laptop

Behind a reverse proxy, for TLS, or to keep the admin pages off the public
address (admin_listen), see deploy/README.md in the repository.
EOF
else
	say "Upgraded. $CONFIG and $data_dir were left alone."
fi

case "$INIT" in
systemd) say "systemctl status cafard / journalctl -u cafard -f" ;;
openrc) say "rc-service cafard status / tail -f /var/log/cafard.log" ;;
esac
