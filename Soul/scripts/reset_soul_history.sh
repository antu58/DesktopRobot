#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${DB_DSN:-}" ]]; then
  echo "DB_DSN is required"
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
psql "${DB_DSN}" -v ON_ERROR_STOP=1 -f "${SCRIPT_DIR}/reset_soul_history.sql"
echo "Soul history reset completed."
