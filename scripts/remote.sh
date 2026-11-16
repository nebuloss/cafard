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

# scripts/rsync-excludes keeps the database and cafard.yaml here.
rsync -a --delete --exclude-from="$repo_root/scripts/rsync-excludes" \
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
