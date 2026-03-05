#!/usr/bin/env bash
set -euo pipefail

WEB_CHAT_PORT="${WEB_CHAT_PORT:-18080}"
PIDS="$(lsof -ti tcp:${WEB_CHAT_PORT} -sTCP:LISTEN || true)"

if [ -z "$PIDS" ]; then
  echo "web chat server not running on port ${WEB_CHAT_PORT}"
  exit 0
fi

for pid in $PIDS; do
  kill "$pid" || true
done

echo "stopped web chat server on port ${WEB_CHAT_PORT}"
