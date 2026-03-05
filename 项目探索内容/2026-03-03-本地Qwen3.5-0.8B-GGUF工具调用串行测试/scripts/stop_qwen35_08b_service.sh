#!/usr/bin/env bash
set -euo pipefail

CONTAINER_NAME="${CONTAINER_NAME:-qwen35-08b-service}"

if docker ps --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
  docker stop "$CONTAINER_NAME" >/dev/null
  echo "stopped: $CONTAINER_NAME"
else
  echo "service not running: $CONTAINER_NAME"
fi
