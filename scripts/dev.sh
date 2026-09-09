#!/usr/bin/env bash
set -euo pipefail
TRIBUNA_ROOT=$(cd "$(dirname "$0")/.." && pwd)
if [ -f "$TRIBUNA_ROOT/.env" ]; then set -a; . "$TRIBUNA_ROOT/.env"; set +a; fi
export DATA_DIR=${DATA_DIR:-$TRIBUNA_ROOT/data}
export FRONTEND_DIR=${FRONTEND_DIR:-$TRIBUNA_ROOT/frontend}
export TEMPLATE_DIR="$TRIBUNA_ROOT/backend/templates"
export ADMIN_DB_PATH="$DATA_DIR/ingester.db"
export ADMIN_UPLOADS_DIR="$FRONTEND_DIR/uploads/ads"
export PORT=${PORT:-8080} ADMIN_PORT=${ADMIN_PORT:-8081} INGESTER_PORT=${INGESTER_PORT:-8082}
export ADMIN_URL="http://127.0.0.1:$ADMIN_PORT"
export BIND_ADDR=127.0.0.1
# This development script binds only to loopback; TLS cookies need HTTPS in deployment.
export ADMIN_SECURE_COOKIES=false
"$TRIBUNA_ROOT/scripts/build.sh"
cd "$TRIBUNA_ROOT/backend"
pids=()
cleanup() { for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done; wait || true; }
trap cleanup EXIT
trap 'exit 130' INT TERM
"$TRIBUNA_ROOT/bin/trbn-ingester" & pids+=("$!")
"$TRIBUNA_ROOT/bin/trbn-admin" & pids+=("$!")
"$TRIBUNA_ROOT/bin/trbn-backend" & pids+=("$!")
printf 'Трибуна: http://127.0.0.1:%s\nАдминка: http://127.0.0.1:%s/admin/\nCtrl+C — остановить\n' "$PORT" "$PORT"
# Detect a stopped child without terminating unrelated local processes.
while true; do
 for pid in "${pids[@]}"; do
  if ! kill -0 "$pid" 2>/dev/null; then wait "$pid"; exit 1; fi
 done
 sleep 2
done
