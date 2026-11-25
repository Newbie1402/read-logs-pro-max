#!/bin/sh
set -eu

BUNDLE_ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
APP_ROOT="$(CDPATH= cd -- "$BUNDLE_ROOT/.." && pwd)"

# Dock launches do not provide a useful working directory. Keep the existing
# .env beside the app when possible; use Application Support if the app lives
# in a read-only location such as /Applications.
if [ ! -w "$APP_ROOT" ]; then
	APP_ROOT="$HOME/Library/Application Support/Log Viewer"
	mkdir -p "$APP_ROOT"
fi
cd "$APP_ROOT"
if [ -z "${CACHE_DIR:-}" ] && ! grep -q '^[[:space:]]*CACHE_DIR=' "$APP_ROOT/.env" 2>/dev/null; then
	export CACHE_DIR="$APP_ROOT/log-cache"
fi

exec "$BUNDLE_ROOT/Contents/MacOS/LogViewer.bin" "$@"
