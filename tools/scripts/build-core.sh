#!/usr/bin/env bash
# Cross-compile the device-side binary: daemon + ctl in one ELF (DESIGN D22).
#
# Target is linux/arm64 on purpose: the daemon runs as a normal root process
# exec'd by the module, not as an Android app, so it does not need the Android
# C library and must not link against it.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
SRC="$ROOT/modules/chopin-core/src"
OUT="$ROOT/modules/chopin-core/bin/chopin-core"
GO=${GO:-$ROOT/tools/go/bin/go}
VERSION=${VERSION:-3.0.0}

[ -x "$GO" ] || { echo "go not found: $GO" >&2; exit 1; }
[ -d "$SRC" ] || { echo "no source: $SRC" >&2; exit 1; }

echo "building $OUT (GOOS=linux GOARCH=arm64, version $VERSION)"
mkdir -p "$(dirname "$OUT")"
( cd "$SRC" && \
  GOPROXY=${GOPROXY:-off} CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  "$GO" build -trimpath -ldflags="-s -w -X main.version=$VERSION" \
      -o "$OUT" ./cmd/chopin-core )
chmod 0755 "$OUT"
ls -l "$OUT"
file "$OUT" 2>/dev/null || true
