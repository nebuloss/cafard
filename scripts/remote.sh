#!/usr/bin/env bash
# Sync the working tree to dev-build and run a command there.
#
#   scripts/remote.sh                 # gofmt -l, build, vet, test -race
#   scripts/remote.sh <command ...>   # run <command> in the remote tree
#
# dev-build is authoritative: a pass there is the only pass that counts.
set -euo pipefail

REMOTE_HOST=${REMOTE_HOST:-guillaume@10.0.50.21}
REMOTE_DIR=${REMOTE_DIR:-cafard}

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# The database holds messages and the photos people dropped, and cafard.yaml
# may hold addresses and secrets. Neither leaves the machine it lives on.
# Patterns are anchored to the tree root where they name a directory, so
# they cannot match a same-named directory deeper down.
rsync -a --delete \
	--exclude '/.git/' \
	--exclude '/node_modules/' \
	--exclude '/data/' \
	--exclude '*.db' \
	--exclude '*.db-wal' \
	--exclude '*.db-shm' \
	--exclude '/cafard.yaml' \
	"$repo_root/" "$REMOTE_HOST:$REMOTE_DIR/"

if [ "$#" -eq 0 ]; then
	# gofmt -l exits 0 whatever it finds, so its output is what fails the run.
	# Single quotes on purpose: this expands on dev-build, not here.
	# shellcheck disable=SC2016
	set -- 'out=$(gofmt -l .); if [ -n "$out" ]; then echo "not gofmt-ed:"; echo "$out"; exit 1; fi;' \
		'go build ./... && go vet ./... && go test -race ./...'
fi

# shellcheck disable=SC2029
ssh "$REMOTE_HOST" "cd $REMOTE_DIR && $*"
