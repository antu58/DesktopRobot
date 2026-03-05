#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
WEB_CHAT_HOST="${WEB_CHAT_HOST:-127.0.0.1}"
WEB_CHAT_PORT="${WEB_CHAT_PORT:-18080}"
MODEL_BASE_URL="${MODEL_BASE_URL:-http://127.0.0.1:8000/v1}"
MODEL_NAME="${MODEL_NAME:-Qwen3.5-0.8B-Q4_K_M.gguf}"

export WEB_CHAT_HOST WEB_CHAT_PORT MODEL_BASE_URL MODEL_NAME
exec node "${ROOT_DIR}/web_chat/server.js"
