#!/usr/bin/env bash
# Build cafard on dev-build and put it straight onto the appliance, without
# GitHub.
#
#   PVE=root@pve-host CT=405 scripts/devdeploy.sh   # Proxmox container, via pct
#   TARGET=user@host scripts/devdeploy.sh           # any host, via ssh and scp
#
# A release takes minutes (tag, CI, download); this takes seconds, which is
# what "does this fix work on the box" needs. It replaces ONE binary and
# restarts the service. It never copies the database or cafard.yaml, and it
# does not touch the unit or the configuration: that is install.sh's job, and
# the box must have been installed with it first. Like install.sh, it sets a
# random admin password when none is set and shows it here once, so the
# restarted server never generates one into its log.
#
# The binary goes the long way round (dev-build, here, then the appliance)
# because only this workstation can reach both.
#
# Other knobs: BUILD_HOST (default guillaume@10.0.50.21), REMOTE_DIR (the
# build tree on dev-build, default cafard-deploy), GOARCH (amd64) and GOARM
# (7, for GOARCH=arm).
set -euo pipefail

BUILD_HOST=${BUILD_HOST:-guillaume@10.0.50.21}
REMOTE_DIR=${REMOTE_DIR:-cafard-deploy}
GOARCH=${GOARCH:-amd64}
GOARM=${GOARM:-7}
PVE=${PVE:-}
CT=${CT:-}
TARGET=${TARGET:-}

say() { printf '> %s\n' "$*"; }
die() { printf 'devdeploy: %s\n' "$*" >&2; exit 1; }

# No default target on purpose: guessing which box to restart is how the
# wrong one gets restarted.
if [ -n "$PVE" ] || [ -n "$CT" ]; then
	[ -n "$PVE" ] && [ -n "$CT" ] || die "PVE and CT go together: PVE=root@host CT=<id>"
	[ -z "$TARGET" ] || die "set PVE and CT, or TARGET, not both"
elif [ -z "$TARGET" ]; then
	die "say where to deploy: PVE=root@host CT=<id> (Proxmox container) or TARGET=user@host"
fi

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

# Stamped so `cafard version` on the appliance is never mistaken for a release.
version="dev-$(git rev-parse --short HEAD)$(git diff --quiet HEAD || echo '-dirty')"

say "syncing to $BUILD_HOST:$REMOTE_DIR"
# scripts/rsync-excludes, shared with remote.sh: the database and
# cafard.yaml never leave the machine they live on.
rsync -az --delete --exclude-from=scripts/rsync-excludes \
	./ "$BUILD_HOST:$REMOTE_DIR/"

say "building $version for linux/$GOARCH (frontend included)"
# The bundle is embedded, so it is built first, on dev-build, from the synced
# sources. npm ci only when node_modules is missing or older than the lock.
# shellcheck disable=SC2029
ssh "$BUILD_HOST" "cd $REMOTE_DIR && set -e
	if [ ! -d node_modules ] || [ package-lock.json -nt node_modules/.package-lock.json ]; then npm ci --silent; fi
	npm run build --silent >/dev/null
	test -s internal/web/assets/static/app/main.js
	CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH GOARM=$GOARM go build -trimpath \
		-ldflags='-s -w -X main.version=$version' -o /tmp/cafard-devdeploy ./cmd/cafard"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
say "fetching the binary"
rsync -az "$BUILD_HOST:/tmp/cafard-devdeploy" "$tmp/cafard"
# shellcheck disable=SC2029
ssh "$BUILD_HOST" "rm -f /tmp/cafard-devdeploy"

# Run on the appliance. Renamed rather than overwritten: replacing a running
# binary in place is "Text file busy". No single quotes in here: it travels
# inside them. Expanded on the appliance, not here.
# shellcheck disable=SC2016
post='set -e
mv -f /usr/local/bin/cafard.new /usr/local/bin/cafard
password=$(od -An -N18 -tx1 /dev/urandom | tr -d " \n")
out=$(printf "%s\n" "$password" | /usr/local/bin/cafard passwd -config /etc/cafard.yaml --if-unset)
case "$out" in "Admin password set"*) printf "\nadmin password, shown once: %s\n\n" "$password" ;; esac
unset password
data_dir=$(/usr/local/bin/cafard config -config /etc/cafard.yaml 2>/dev/null | sed -n "s/^data_dir: *//p")
owner=$(stat -c %U "${data_dir:-/var/lib/cafard}")
chown -R "$owner:$owner" "${data_dir:-/var/lib/cafard}"
rc-service cafard restart >/dev/null 2>&1 || systemctl restart cafard
sleep 2
/usr/local/bin/cafard version
listen=$(/usr/local/bin/cafard config -config /etc/cafard.yaml 2>/dev/null | sed -n "s/^http_listen: *//p")
port=${listen##*:}
get() { if command -v curl >/dev/null 2>&1; then curl -fsSk -m 5 "$1"; else wget -qO- -T 5 "$1"; fi; }
printf "health : "
get "http://127.0.0.1:${port:-8790}/healthz" 2>/dev/null || get "https://127.0.0.1:${port:-8790}/healthz" 2>/dev/null || echo FAILED'

if [ -n "$PVE" ]; then
	say "pushing to container $CT on $PVE"
	# pct push reads from the Proxmox host's filesystem, not from here.
	ssh "$PVE" 'cat > /tmp/cafard-devdeploy' <"$tmp/cafard"
	# shellcheck disable=SC2029
	ssh "$PVE" "pct push $CT /tmp/cafard-devdeploy /usr/local/bin/cafard.new --perms 755 && rm -f /tmp/cafard-devdeploy"
	say "restarting and checking"
	# shellcheck disable=SC2029
	ssh "$PVE" "pct exec $CT -- sh -c '$post'"
else
	say "pushing to $TARGET"
	sudo=sudo
	[ "${TARGET%%@*}" != root ] || sudo=
	scp -q "$tmp/cafard" "$TARGET:/tmp/cafard-devdeploy"
	# shellcheck disable=SC2029
	ssh "$TARGET" "$sudo install -m 0755 /tmp/cafard-devdeploy /usr/local/bin/cafard.new && rm -f /tmp/cafard-devdeploy"
	say "restarting and checking"
	# shellcheck disable=SC2029
	ssh "$TARGET" "$sudo sh -c '$post'"
fi
