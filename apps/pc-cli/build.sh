#!/usr/bin/env bash
# Build the computer-side CLI.
#
# Default target is the host that runs it: this project's workstation is Linux,
# and the binary is also cross-compiled for Windows because the user's laptop is
# Windows. Both are static (CGO_ENABLED=0) so they run without a toolchain.
set -euo pipefail

APP=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$APP/../.." && pwd)
GO=${GO:-$ROOT/tools/go/bin/go}
DIST="$APP/dist"

[ -x "$GO" ] || { echo "go not found: $GO" >&2; exit 1; }
mkdir -p "$DIST"

build() {
    local goos=$1 goarch=$2 ext=$3
    local out="$DIST/sextant-$goos-$goarch$ext"
    echo "building $out"
    ( cd "$APP" && \
      GOPROXY=${GOPROXY:-off} CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch \
      "$GO" build -trimpath -ldflags '-s -w' -o "$out" ./cmd/sextant )
    ls -l "$out"
}

build linux amd64 ""
build windows amd64 ".exe"
