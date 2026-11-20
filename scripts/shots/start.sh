#!/bin/sh
D=~/cafard-shots
exec $D/cafard serve -data-dir $D/data -http-listen 127.0.0.1:18790 -syslog-udp '' -syslog-tcp '' -netconsole-udp '' -messaging-burst 200 -messaging-rate-per-minute 600 -log-level warn
