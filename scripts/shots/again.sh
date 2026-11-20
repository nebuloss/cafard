#!/bin/sh
# Rebuild, reset, start in the background, seed and shoot.
set -e
D=~/cafard-shots
sh $D/reset.sh
setsid sh $D/start.sh > $D/server.log 2>&1 < /dev/null &
sleep 3
sh $D/run.sh
