#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../../.." && pwd)"
MODEL_PATH="${MODEL_PATH:-$ROOT_DIR/models/Qwen3.5-0.8B-Q4_K_M.gguf}"
MODEL_BASENAME="$(basename "$MODEL_PATH")"
HOST="${HOST:-127.0.0.1}"
PORT="${PORT:-8000}"
CTX_SIZE="${CTX_SIZE:-8192}"
GPU_LAYERS="${GPU_LAYERS:-99}"
CONTAINER_NAME="${CONTAINER_NAME:-qwen35-08b-service}"
IMAGE="${IMAGE:-ghcr.io/ggml-org/llama.cpp:server}"
# Current image tag is amd64-only in this environment.
DOCKER_PLATFORM="${DOCKER_PLATFORM:-linux/amd64}"

if [ ! -f "$MODEL_PATH" ]; then
  echo "model file not found: $MODEL_PATH"
  exit 1
fi

if docker ps --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
  echo "service already running: $CONTAINER_NAME"
else
  if docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
    docker rm -f "$CONTAINER_NAME" >/dev/null
  fi

  run_args=(
    -d
    --name "$CONTAINER_NAME"
    -p "${HOST}:${PORT}:8000"
    -v "${ROOT_DIR}/models:/models:ro"
  )

  if [ -n "$DOCKER_PLATFORM" ]; then
    run_args+=(--platform "$DOCKER_PLATFORM")
  fi

  docker run "${run_args[@]}" \
    "$IMAGE" \
    -m "/models/${MODEL_BASENAME}" \
    --host 0.0.0.0 \
    --port 8000 \
    -c "$CTX_SIZE" \
    -ngl "$GPU_LAYERS" \
    --jinja >/dev/null

  echo "service container started: $CONTAINER_NAME"
fi

echo "waiting for model API at http://${HOST}:${PORT}/v1/models ..."
for _ in $(seq 1 60); do
  if curl -fsS "http://${HOST}:${PORT}/v1/models" >/dev/null 2>&1; then
    echo "ready: http://${HOST}:${PORT}/v1/models"
    echo "model: ${MODEL_BASENAME}"
    exit 0
  fi
  sleep 1
done

echo "model API not ready in 60s, check logs:"
echo "  docker logs --tail=120 ${CONTAINER_NAME}"
exit 1
