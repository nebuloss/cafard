#!/bin/sh
# Build a commit on the build machine and install it on a cafard host, as
# claudication's scripts/deploy.sh does.
#
# Why this exists: devdeploy.sh copied a binary in and restarted it, which is
# how claudication's gateway was left down on 2026-09-24. scripts/install.sh
# does an update safely (the new binary runs first, the database is backed
# up, both listeners must answer with the new version or the old one is put
# back); this is the one way to get a build to it, so nobody improvises the
# rest.
#
#   scripts/deploy.sh                 deploy HEAD
#   scripts/deploy.sh v0.3.0          deploy a tag, or any pushed ref
#   scripts/deploy.sh wip/feature     a branch under test
#
# The commit is built from the remote repository, not from this working tree,
# so it has to be pushed first: what runs is then something anyone can check
# out and rebuild to the same bytes (make repro).
#
# Overridable:
#   BUILD_HOST   user@host that builds            (guillaume@10.0.50.21)
#   BUILD_DIR    clone on the build host          (src/cafard-deploy)
#   TARGET       user@host that runs cafard       (root@10.0.0.2)
#   PCT          Proxmox container id on TARGET, empty for TARGET itself (409)
#   SKIP_CHECK   1 to skip `make check` before building
#   BUILD_WRAP   command prefixed to make on the build host (rtk: it keeps
#                the build's output to errors and warnings)

set -eu

BUILD_HOST="${BUILD_HOST:-guillaume@10.0.50.21}"
BUILD_DIR="${BUILD_DIR:-src/cafard-deploy}"
TARGET="${TARGET:-root@10.0.0.2}"
PCT="${PCT-409}"
SKIP_CHECK="${SKIP_CHECK:-0}"
BUILD_WRAP="${BUILD_WRAP-rtk}"

# shellcheck disable=SC1007  # clearing CDPATH for this one command, not assigning
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
ref="${1:-HEAD}"
git -C "$root" fetch -q --tags origin
# A branch is what was pushed, origin/<branch>, not a local branch of the
# same name: a checkout's main can lag behind, and deploying it once built a
# commit from before the rebuild. HEAD, a tag or a hash is taken as given.
if git -C "$root" rev-parse --verify -q "refs/remotes/origin/$ref" >/dev/null; then
  commit=$(git -C "$root" rev-parse "refs/remotes/origin/$ref^{commit}")
else
  commit=$(git -C "$root" rev-parse --verify "$ref^{commit}")
fi
repo=$(git -C "$root" remote get-url origin)

# Pushed, or the build machine cannot fetch it.
if ! git -C "$root" branch -r --contains "$commit" | grep -q . &&
   ! git -C "$root" tag --contains "$commit" | grep -q .; then
  echo "deploy: $commit is not on any remote branch: push it first" >&2
  exit 1
fi

echo "== build $commit on $BUILD_HOST"
if [ "$SKIP_CHECK" = 1 ]; then targets="build"; else targets="check build"; fi
# shellcheck disable=SC2029  # expanded here on purpose
ssh "$BUILD_HOST" "set -e; . ~/.profile 2>/dev/null || true
  [ -d '$BUILD_DIR/.git' ] || git clone -q '$repo' '$BUILD_DIR'
  cd '$BUILD_DIR'
  git fetch -q --tags origin
  git checkout -q --detach '$commit'
  git clean -qfdx -e web/node_modules
  $BUILD_WRAP make $targets"

# shellcheck disable=SC2029
sum=$(ssh "$BUILD_HOST" "cd '$BUILD_DIR' && sha256sum dist/cafard" | awk '{print $1}')
version=$(ssh "$BUILD_HOST" "cd '$BUILD_DIR' && ./dist/cafard version")
echo "== built $version"
echo "   sha256 $sum"

# Streamed straight from the build host to the target: the binary never lands
# on the machine running this script.
tmp="/tmp/cafard-deploy-$commit"
echo "== copy to $TARGET${PCT:+ (container $PCT)}"
# shellcheck disable=SC2029
ssh "$BUILD_HOST" "cd '$BUILD_DIR' && tar -c dist/cafard scripts/install.sh" |
  ssh "$TARGET" "rm -rf '$tmp' && mkdir -p '$tmp' && tar -x -C '$tmp'"

install="BINARY_FILE=/tmp/cafard-deploy/dist/cafard BINARY_SHA256=$sum QUIET=1 sh /tmp/cafard-deploy/scripts/install.sh"
echo "== install"
if [ -n "$PCT" ]; then
  # shellcheck disable=SC2029
  ssh "$TARGET" "set -e
    pct exec $PCT -- mkdir -p /tmp/cafard-deploy/dist /tmp/cafard-deploy/scripts
    pct push $PCT '$tmp/dist/cafard' /tmp/cafard-deploy/dist/cafard --perms 0755
    pct push $PCT '$tmp/scripts/install.sh' /tmp/cafard-deploy/scripts/install.sh
    rm -rf '$tmp'
    status=0
    pct exec $PCT -- sh -c '$install' || status=\$?
    pct exec $PCT -- rm -rf /tmp/cafard-deploy
    exit \$status"
else
  # shellcheck disable=SC2029
  ssh "$TARGET" "rm -rf /tmp/cafard-deploy && mv '$tmp' /tmp/cafard-deploy
    status=0; $install || status=\$?
    rm -rf /tmp/cafard-deploy; exit \$status"
fi
echo "== deployed $version"
