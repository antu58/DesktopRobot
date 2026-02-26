#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

COSY_WEB_PORT="${COSY_WEB_PORT:-18388}"
COSY_MODEL_DIR="${COSY_MODEL_DIR:-iic/CosyVoice-300M-SFT}"
FORCE_REBUILD="${FORCE_REBUILD:-0}"

if lsof -nP -iTCP:"$COSY_WEB_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  if docker compose ps --status running --services 2>/dev/null | grep -qx "cosyvoice-tts"; then
    echo "COSY_WEB_PORT=$COSY_WEB_PORT is already used by current cosyvoice-tts service, continue."
  else
    echo "Error: COSY_WEB_PORT=$COSY_WEB_PORT is already in use."
    echo "Retry with another fixed port, e.g. COSY_WEB_PORT=18389 ./deploy.sh"
    exit 1
  fi
fi

HTTP_PROXY="${HTTP_PROXY:-http://127.0.0.1:7897}"
HTTPS_PROXY="${HTTPS_PROXY:-${HTTP_PROXY}}"
ALL_PROXY="${ALL_PROXY:-socks5://127.0.0.1:7897}"

BUILD_HTTP_PROXY="${BUILD_HTTP_PROXY:-http://host.docker.internal:7897}"
BUILD_HTTPS_PROXY="${BUILD_HTTPS_PROXY:-${BUILD_HTTP_PROXY}}"
BUILD_ALL_PROXY="${BUILD_ALL_PROXY:-socks5://host.docker.internal:7897}"
RUNTIME_HTTP_PROXY="${RUNTIME_HTTP_PROXY:-${BUILD_HTTP_PROXY}}"
RUNTIME_HTTPS_PROXY="${RUNTIME_HTTPS_PROXY:-${BUILD_HTTPS_PROXY}}"
RUNTIME_ALL_PROXY="${RUNTIME_ALL_PROXY:-${BUILD_ALL_PROXY}}"
RUNTIME_NO_PROXY="${RUNTIME_NO_PROXY:-127.0.0.1,localhost}"

export COSY_WEB_PORT COSY_MODEL_DIR
export HTTP_PROXY HTTPS_PROXY ALL_PROXY
export BUILD_HTTP_PROXY BUILD_HTTPS_PROXY BUILD_ALL_PROXY
export RUNTIME_HTTP_PROXY RUNTIME_HTTPS_PROXY RUNTIME_ALL_PROXY RUNTIME_NO_PROXY

echo "Deploying CosyVoice TTS on port ${COSY_WEB_PORT}"
echo "Model: ${COSY_MODEL_DIR}"
echo "Proxy(host)=${HTTP_PROXY} Proxy(container)=${BUILD_HTTP_PROXY}"
echo "Force rebuild: ${FORCE_REBUILD}"

if [ "${FORCE_REBUILD}" = "1" ]; then
  docker compose up -d --build
else
  docker compose up -d
fi

echo
echo "Deployment done."
echo "Open: http://127.0.0.1:${COSY_WEB_PORT}"
echo "Check: docker compose ps"
echo "Logs : docker compose logs -f cosyvoice-tts"
