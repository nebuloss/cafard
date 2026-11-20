#!/bin/sh
pkill -f '[c]afard-shots/cafard serve'
sleep 1
sh ~/cafard-shots/setup.sh >/dev/null
