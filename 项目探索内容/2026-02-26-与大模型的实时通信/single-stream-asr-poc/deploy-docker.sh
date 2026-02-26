#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

WEB_PORT="${WEB_PORT:-18188}"
ICE_UDP_PORT="${ICE_UDP_PORT:-19188}"
ICE_PUBLIC_IP="${ICE_PUBLIC_IP:-127.0.0.1}"

if lsof -nP -iTCP:"$WEB_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "Error: WEB_PORT=$WEB_PORT is already in use."
  echo "Set another fixed port and retry, e.g. WEB_PORT=18189 ./deploy-docker.sh"
  exit 1
fi
if lsof -nP -iUDP:"$ICE_UDP_PORT" >/dev/null 2>&1 || lsof -nP -iTCP:"$ICE_UDP_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "Error: ICE_UDP_PORT=$ICE_UDP_PORT is already in use."
  echo "Set another fixed port and retry, e.g. ICE_UDP_PORT=19189 ./deploy-docker.sh"
  exit 1
fi

ASR_MODE="${ASR_MODE:-bridge}"
STRICT_MODEL="${STRICT_MODEL:-1}"

# Host-level proxy for docker daemon/client.
HTTP_PROXY="${HTTP_PROXY:-http://127.0.0.1:7897}"
HTTPS_PROXY="${HTTPS_PROXY:-${HTTP_PROXY}}"
ALL_PROXY="${ALL_PROXY:-socks5://127.0.0.1:7897}"

# Build/runtime proxy from container view.
BUILD_HTTP_PROXY="${BUILD_HTTP_PROXY:-http://host.docker.internal:7897}"
BUILD_HTTPS_PROXY="${BUILD_HTTPS_PROXY:-${BUILD_HTTP_PROXY}}"
BUILD_ALL_PROXY="${BUILD_ALL_PROXY:-socks5://host.docker.internal:7897}"
RUNTIME_HTTP_PROXY="${RUNTIME_HTTP_PROXY:-${BUILD_HTTP_PROXY}}"
RUNTIME_HTTPS_PROXY="${RUNTIME_HTTPS_PROXY:-${BUILD_HTTPS_PROXY}}"
RUNTIME_ALL_PROXY="${RUNTIME_ALL_PROXY:-${BUILD_ALL_PROXY}}"
RUNTIME_NO_PROXY="${RUNTIME_NO_PROXY:-127.0.0.1,localhost,asr-bridge,go-server}"

export WEB_PORT ICE_UDP_PORT ICE_PUBLIC_IP ASR_MODE STRICT_MODEL
export HTTP_PROXY HTTPS_PROXY ALL_PROXY
export BUILD_HTTP_PROXY BUILD_HTTPS_PROXY BUILD_ALL_PROXY
export RUNTIME_HTTP_PROXY RUNTIME_HTTPS_PROXY RUNTIME_ALL_PROXY RUNTIME_NO_PROXY

echo "Deploying with WEB_PORT=$WEB_PORT ICE_UDP_PORT=$ICE_UDP_PORT ICE_PUBLIC_IP=$ICE_PUBLIC_IP ASR_MODE=$ASR_MODE STRICT_MODEL=$STRICT_MODEL"
echo "Proxy(host)=$HTTP_PROXY Proxy(container)=$BUILD_HTTP_PROXY"
docker compose up -d --build

echo
echo "Deployment done."
echo "Open: http://127.0.0.1:${WEB_PORT}"
echo "Check: docker compose ps"
echo "Logs : docker compose logs -f go-server asr-bridge"
