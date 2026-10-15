#!/bin/sh
set -eu

mkdir -p /data /data/log-cache
chown -R logviewer:logviewer /data

exec su-exec logviewer:logviewer /app/logviewer -no-open
