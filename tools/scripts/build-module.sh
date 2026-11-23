#!/usr/bin/env bash
# Build a KernelSU/Magisk module zip from a module source directory.
# Usage: build-module.sh modules/sextant-core [output.zip]
set -euo pipefail

SRC=${1:?usage: $0 <module-dir> [output.zip]}
SRC=${SRC%/}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
[ -f "$SRC/module.prop" ] || { echo "not a module dir: $SRC" >&2; exit 2; }
NAME=$(sed -n 's/^id=//p' "$SRC/module.prop" | head -1)
# D26: the unlock pattern is a local build input, never tracked. Failing here is
# deliberate: a module zip without it installs a device that cannot auto-unlock.
if [ "$NAME" = "sextant-core" ] && [ ! -s "$SRC/config/unlock-secret" ]; then
    echo "error: $SRC/config/unlock-secret is missing or empty" >&2
    echo "       it is a local build input (0600) and intentionally not tracked" >&2
    exit 2
fi
OUT=${2:-"$(dirname "$SRC")/$NAME.zip"}
OUT=$(realpath -m "$OUT")
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
cp -a "$SRC/." "$TMP/"
# A module that ships an APK must ship the one that was just built.
#
# lib/app.apk is a COPY of the client build, and nothing kept the two in sync:
# measured, the shipped copy was a build from hours earlier while apps/pilot/dist
# held the current one. activate-device.sh installs from dist, so development looked
# fine — but a fresh module install (or service.sh's own upgrade path, which compares
# this file's hash) would put the OLD APK on the device. Silent, and it looks exactly
# like "my APK change did nothing".
if [ -f "$TMP/lib/app.apk" ]; then
    newest=$(ls -1t "$ROOT"/apps/pilot/dist/*.apk 2>/dev/null | head -1)
    if [ -n "$newest" ]; then
        if ! cmp -s "$newest" "$TMP/lib/app.apk"; then
            cp -f "$newest" "$TMP/lib/app.apk"
            echo "refreshed lib/app.apk from $(basename "$newest")"
        fi
        # The privileged copy under system/priv-app is the one that actually installs
        # on a fresh boot, so it must be the same bytes as lib/app.apk. Two copies of
        # an APK that drift apart produce a device running a version nobody built.
        if [ -d "$TMP/system/priv-app" ]; then
            found=0
            while IFS= read -r target; do
                found=1
                if ! cmp -s "$newest" "$target"; then
                    cp -f "$newest" "$target"
                    echo "refreshed $(realpath --relative-to="$TMP" "$target")"
                fi
            done < <(find "$TMP/system/priv-app" -name '*.apk' -type f)
            if [ "$found" = 0 ] && [ -f "$TMP/lib/app.apk" ]; then
                appdir=$(find "$TMP/system/priv-app" -mindepth 1 -maxdepth 1 -type d | head -1)
                if [ -n "$appdir" ]; then
                    appname=$(basename "$appdir")
                    target="$appdir/$appname.apk"
                    cp -f "$TMP/lib/app.apk" "$target"
                    found=1
                    echo "created $(realpath --relative-to="$TMP" "$target")"
                fi
            fi
            [ "$found" = 1 ] || echo "WARN: system/priv-app exists but ships no APK"
        fi
    fi
fi
# Runtime dirs, VCS state and host-side sources never belong in a flashable zip.
rm -rf "$TMP/state" "$TMP/log" "$TMP/.git" "$TMP/src" 2>/dev/null || true
find "$TMP" -name .gitkeep -delete 2>/dev/null || true
# Honour an optional .moduleignore (one glob-ish path per line).
if [ -f "$SRC/.moduleignore" ]; then
    while IFS= read -r pat; do
        case "$pat" in ''|'#'*) continue ;; esac
        rm -rf "$TMP/$pat" 2>/dev/null || true
    done < "$SRC/.moduleignore"
    rm -f "$TMP/.moduleignore"
fi
rm -f "$OUT"          # zip would otherwise APPEND to a stale archive
( cd "$TMP" && zip -qr "$OUT" . )
echo "built $OUT"
unzip -t "$OUT" >/dev/null
