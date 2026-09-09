#!/usr/bin/env bash
set -euo pipefail
TRIBUNA_ROOT=$(cd "$(dirname "$0")/.." && pwd)
if [ -f "$TRIBUNA_ROOT/.env" ]; then set -a; . "$TRIBUNA_ROOT/.env"; set +a; fi
export DATA_DIR=${DATA_DIR:-$TRIBUNA_ROOT/data}
export ADMIN_DB_PATH=${ADMIN_DB_PATH:-$DATA_DIR/ingester.db}
if [ ! -x "$TRIBUNA_ROOT/bin/trbn-admin" ]; then "$TRIBUNA_ROOT/scripts/build.sh"; fi
read -r -p 'Email администратора: ' TRIBUNA_ADMIN_EMAIL
read -r -s -p 'Пароль (не менее 12 символов): ' TRIBUNA_ADMIN_PASSWORD
printf '\n'
export TRIBUNA_ADMIN_PASSWORD
"$TRIBUNA_ROOT/bin/trbn-admin" create-user --email "$TRIBUNA_ADMIN_EMAIL"
unset TRIBUNA_ADMIN_PASSWORD
