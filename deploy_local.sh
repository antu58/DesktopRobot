#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOUL_DIR="${ROOT_DIR}/Soul"
ACTION="${1:-up}"

log() {
  echo "[deploy] $*"
}

fail() {
  echo "[deploy][error] $*" >&2
  exit 1
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    fail "missing command: $1"
  fi
}

get_env() {
  local key="$1"
  if [[ ! -f ".env" ]]; then
    return 0
  fi
  awk -F= -v key="$key" '$1==key {print substr($0, index($0, "=")+1)}' .env | tail -n1
}

wait_http() {
  local name="$1"
  local url="$2"
  local retries=30
  local interval=2

  if ! command -v curl >/dev/null 2>&1; then
    log "curl not found, skip health check for ${name}"
    return 0
  fi

  for ((i=1; i<=retries; i++)); do
    if curl -fsS "${url}" >/dev/null 2>&1; then
      log "${name} is healthy: ${url}"
      return 0
    fi
    sleep "${interval}"
  done

  return 1
}

validate_env() {
  local llm_provider openai_api_key
  llm_provider="$(get_env LLM_PROVIDER)"
  llm_provider="${llm_provider:-openai}"

  openai_api_key="$(get_env OPENAI_API_KEY)"
  if [[ "${llm_provider}" == "openai" ]]; then
    if [[ -z "${openai_api_key}" || "${openai_api_key}" == "replace_with_real_key" ]]; then
      fail "OPENAI_API_KEY is invalid in ${SOUL_DIR}/.env"
    fi
  fi
}

ensure_prerequisites() {
  require_cmd docker
  docker compose version >/dev/null 2>&1 || fail "docker compose is not available"
  docker info >/dev/null 2>&1 || fail "docker daemon is not running"

  [[ -d "${SOUL_DIR}" ]] || fail "Soul directory not found: ${SOUL_DIR}"
  cd "${SOUL_DIR}"

  if [[ ! -f ".env" ]]; then
    [[ -f ".env.example" ]] || fail ".env.example not found in ${SOUL_DIR}"
    cp .env.example .env
    log "created ${SOUL_DIR}/.env from .env.example"
    log "please update keys in .env and run again"
    exit 1
  fi
}

compose_up() {
  validate_env
  log "starting Docker services in ${SOUL_DIR}"
  docker compose up -d --build --remove-orphans

  local soul_port terminal_port mem0_port
  soul_port="$(get_env SOUL_HTTP_PORT)"
  terminal_port="$(get_env TERMINAL_WEB_HTTP_PORT)"
  mem0_port="$(get_env MEM0_HTTP_PORT)"
  soul_port="${soul_port:-9010}"
  terminal_port="${terminal_port:-9011}"
  mem0_port="${mem0_port:-18000}"

  if ! wait_http "mem0 (optional)" "http://localhost:${mem0_port}/docs"; then
    log "mem0 optional check skipped; continue with soul-server and terminal-web"
  fi
  wait_http "soul-server" "http://localhost:${soul_port}/healthz" || fail "soul-server health check failed"
  wait_http "terminal-web" "http://localhost:${terminal_port}/healthz" || fail "terminal-web health check failed"

  log "deployment completed"
  log "mem0: http://localhost:${mem0_port}"
  log "soul-server: http://localhost:${soul_port}"
  log "terminal-web: http://localhost:${terminal_port}"
}

compose_down() {
  log "stopping Docker services in ${SOUL_DIR}"
  docker compose down
}

compose_status() {
  docker compose ps
}

compose_logs() {
  docker compose logs -f --tail=200
}

usage() {
  cat <<EOF
Usage: $(basename "$0") [up|down|restart|status|logs]

Commands:
  up       Build and start all services (default)
  down     Stop and remove services
  restart  Restart services
  status   Show compose service status
  logs     Follow compose logs
EOF
}

main() {
  case "${ACTION}" in
    -h|--help|help)
      usage
      exit 0
      ;;
  esac

  ensure_prerequisites

  case "${ACTION}" in
    up)
      compose_up
      ;;
    down)
      compose_down
      ;;
    restart)
      compose_down
      compose_up
      ;;
    status)
      compose_status
      ;;
    logs)
      compose_logs
      ;;
    *)
      usage
      fail "unknown action: ${ACTION}"
      ;;
  esac
}

main "$@"
