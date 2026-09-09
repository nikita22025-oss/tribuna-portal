#!/usr/bin/env bash
set -euo pipefail
TRIBUNA_ROOT=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$TRIBUNA_ROOT/dist"
export GOOS=linux GOARCH=${GOARCH:-amd64} CGO_ENABLED=0
"$TRIBUNA_ROOT/scripts/build.sh"
COPYFILE_DISABLE=1 tar -czf "$TRIBUNA_ROOT/dist/tribuna-linux-$GOARCH.tar.gz" --exclude='._*' --exclude='.DS_Store' --exclude='uploads/news/*' --exclude='uploads/ads/*' -C "$TRIBUNA_ROOT" bin backend/cmd backend/internal backend/templates backend/go.mod backend/go.sum frontend data/bundle.fixture.json data/sources.json scripts deploy licenses README.md TESTING.md .env.example
printf 'Архив: %s/dist/tribuna-linux-%s.tar.gz\n' "$TRIBUNA_ROOT" "$GOARCH"
