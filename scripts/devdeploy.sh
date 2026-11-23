#!/usr/bin/env bash
# Build the pushed branch on dev-build and put it in a Proxmox container, to
# try a change on a running cafard in seconds rather than through a release.
#
#   PVE=root@10.0.0.2 CT=302 scripts/devdeploy.sh
#
# What it does, in order:
#   1. refuses unless the local branch is pushed: dev-build builds from git,
#      never from this working tree;
#   2. on dev-build, checks out that branch and runs `make build`;
#   3. streams the binary from dev-build to the Proxmox host without writing
#      it here (dev-code keeps source and git only), then `pct push`es it and
#      the OpenRC script into the container;
#   4. in the container: creates the cafard account and, the first time only,
#      /etc/cafard/config.yaml; swaps the binary; restarts; checks /healthz.
#
# It never touches the state directory and never rewrites an existing
# config.yaml: those belong to the box. The first config it writes listens on
# LISTEN (default 0.0.0.0:8790) and ADMIN_LISTEN (default 0.0.0.0:8791), with
# PUBLIC_URL (default http://<container address>:8790). Both on every
# interface suits a container on the internal network; one in the DMZ gets an
# admin-listen on its internal address instead, by editing the file.
#
# Knobs: BUILD_HOST (default guillaume@10.0.50.21), BUILD_DIR (the clone on
# dev-build, default src/cafard-next), BRANCH (default: the current branch).
set -euo pipefail

BUILD_HOST=${BUILD_HOST:-guillaume@10.0.50.21}
BUILD_DIR=${BUILD_DIR:-src/cafard-next}
PVE=${PVE:-}
CT=${CT:-}
LISTEN=${LISTEN:-0.0.0.0:8790}
ADMIN_LISTEN=${ADMIN_LISTEN:-0.0.0.0:8791}
PUBLIC_URL=${PUBLIC_URL:-}

say() { printf '> %s\n' "$*"; }
die() { printf 'devdeploy: %s\n' "$*" >&2; exit 1; }

# No default target on purpose: guessing which box to restart is how the
# wrong one gets restarted.
[ -n "$PVE" ] && [ -n "$CT" ] || die "say where to deploy: PVE=root@<proxmox host> CT=<container id>"

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
BRANCH=${BRANCH:-$(git rev-parse --abbrev-ref HEAD)}
git fetch -q origin "$BRANCH"
local_sha=$(git rev-parse HEAD)
remote_sha=$(git rev-parse "origin/$BRANCH")
[ "$local_sha" = "$remote_sha" ] || die "HEAD ($local_sha) is not origin/$BRANCH ($remote_sha): push first, dev-build builds what is pushed"
git diff --quiet HEAD || say "note: uncommitted changes here are not deployed"

short=$(git rev-parse --short HEAD)
version="dev-$short"

say "building $BRANCH at $short on $BUILD_HOST"
# shellcheck disable=SC2029
ssh "$BUILD_HOST" "set -e; cd $BUILD_DIR
	git fetch -q origin
	git checkout -q -B $BRANCH origin/$BRANCH
	make build VERSION=$version >/dev/null
	./dist/cafard version"

say "copying to container $CT on $PVE"
# Streamed host to host: the binary never lands on this machine.
# shellcheck disable=SC2029
ssh "$BUILD_HOST" "cat $BUILD_DIR/dist/cafard" | ssh "$PVE" 'cat > /tmp/cafard-devdeploy'
ssh "$PVE" 'cat > /tmp/cafard-devdeploy.openrc' <deploy/cafard.openrc
# shellcheck disable=SC2029
ssh "$PVE" "pct push $CT /tmp/cafard-devdeploy /usr/local/bin/cafard.new --perms 755 &&
	pct push $CT /tmp/cafard-devdeploy.openrc /etc/init.d/cafard --perms 755 &&
	rm -f /tmp/cafard-devdeploy /tmp/cafard-devdeploy.openrc"

say "installing and restarting"
# Runs in the container. No single quotes inside: it travels inside them.
# shellcheck disable=SC2016
post='set -e
id cafard >/dev/null 2>&1 || { addgroup -S cafard; adduser -S -D -H -h /var/lib/cafard -s /sbin/nologin -G cafard cafard; }
mkdir -p /etc/cafard
if [ ! -f /etc/cafard/config.yaml ]; then
	addr=$(ip -4 -o addr show scope global | awk "{print \$4}" | cut -d/ -f1 | head -1)
	url=${PUBLIC_URL:-http://$addr:${LISTEN##*:}}
	printf "listen: \"%s\"\nadmin-listen: \"%s\"\nstate-dir: /var/lib/cafard\npublic-url: \"%s\"\nlog:\n  level: info\n  format: text\n" "$LISTEN" "$ADMIN_LISTEN" "$url" >/etc/cafard/config.yaml
	chown root:cafard /etc/cafard/config.yaml
	chmod 0640 /etc/cafard/config.yaml
	echo "wrote /etc/cafard/config.yaml"
fi
/usr/local/bin/cafard.new config -config /etc/cafard/config.yaml >/dev/null
mv -f /usr/local/bin/cafard.new /usr/local/bin/cafard
rc-update add cafard default >/dev/null 2>&1 || true
rc-service cafard restart >/dev/null
sleep 2
/usr/local/bin/cafard version
port=$(/usr/local/bin/cafard config -config /etc/cafard/config.yaml | sed -n "s/^listen: *//p" | sed "s/.*://")
admin=$(/usr/local/bin/cafard config -config /etc/cafard/config.yaml | sed -n "s/^admin-listen: *//p" | sed "s/.*://")
get() { if command -v curl >/dev/null 2>&1; then curl -fsS -m 5 "$1"; else wget -qO- -T 5 "$1"; fi; }
printf "public %s: " "$port"; get "http://127.0.0.1:$port/healthz" || echo FAILED
printf "admin  %s: " "$admin"; get "http://127.0.0.1:$admin/healthz" || echo FAILED'
# shellcheck disable=SC2029
ssh "$PVE" "pct exec $CT -- env LISTEN=$LISTEN ADMIN_LISTEN=$ADMIN_LISTEN PUBLIC_URL=$PUBLIC_URL sh -c '$post'"
