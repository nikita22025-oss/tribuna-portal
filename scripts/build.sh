#!/usr/bin/env bash
set -euo pipefail
TRIBUNA_ROOT=$(cd "$(dirname "$0")/.." && pwd)
GO_BIN=${GO_BIN:-go}
mkdir -p "$TRIBUNA_ROOT/bin"
cd "$TRIBUNA_ROOT/backend"
export CGO_ENABLED=0
for target in server ingester admin; do
  name=$target
  if [ "$target" = server ]; then name=backend; fi
  "$GO_BIN" build -trimpath -ldflags='-s -w' -o "$TRIBUNA_ROOT/bin/trbn-$name" "./cmd/$target"
done
