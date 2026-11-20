#!/bin/sh
# Seeds a fresh server (already started by start.sh) and shoots it.
set -e
D=~/cafard-shots
node $D/seed.mjs http://127.0.0.1:18790 shots-password-1 $D/img
rm -rf $D/out && mkdir -p $D/out
node $D/shoot.mjs http://127.0.0.1:18790 shots-password-1 $D/out
ls $D/out
