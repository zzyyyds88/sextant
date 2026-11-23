#!/usr/bin/env bash
# Build the client APK: UI + accessibility HUD + IME. No engine, no helper.
#
# DESIGN D20/D21: the APK is a zero-root client. The Go daemon, the root helper
# and scrcpy-server all ship inside the KernelSU module, which also installs this
# APK. That is why nothing here compiles a Go binary or stages a native library:
# a client that could start a privileged process is exactly what this redesign
# removed.
set -euo pipefail

APP=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$APP/../.." && pwd)
SDK=${ANDROID_SDK_ROOT:-"$ROOT/tools/android-sdk"}
BT=${ANDROID_BUILD_TOOLS:-"$SDK/build-tools/android-35"}
PLATFORM=${ANDROID_PLATFORM:-"$SDK/platforms/android-34/android.jar"}
PKG=${PKG:-com.sextant.pilot}
NAME=${NAME:-sextant-pilot}
VERSION_NAME=${VERSION_NAME:-4.0.0}
VERSION_CODE=${VERSION_CODE:-400}
DIST="$APP/dist"
STAGE="$APP/build/apk"
OUT="$DIST/$NAME-$VERSION_NAME.apk"

[ -f "$PLATFORM" ] || { echo "missing android.jar: $PLATFORM" >&2; exit 1; }

echo "[1/3] resources + dex + assemble"
rm -rf "$STAGE"
mkdir -p "$STAGE/assets" "$DIST"
"$APP/assemble.sh" "$APP/app" "$OUT" "$PKG" "$VERSION_NAME" "$VERSION_CODE" "$STAGE/assets" "$STAGE/native"

echo "[2/3] verify"
# `sed -n` reads the whole stream; `head -4` closes the pipe early and, with
# `set -o pipefail`, turns aapt2's SIGPIPE into a failed build.
"$BT/aapt2" dump badging "$OUT" | sed -n '1,4p'
"$BT/apksigner" verify --print-certs "$OUT" | sed -n '1,3p'

echo "[3/3] static checks that must hold for a zero-root client"
if grep -rn "ProcessBuilder(\"su\"\|RootShell\|KernelSU allowlist" "$APP/app/src" >/dev/null 2>&1; then
    # One intentional exception: writing the unlock credential (DESIGN §9.2).
    offenders=$(grep -rln "ProcessBuilder(\"su\"" "$APP/app/src" | grep -v "Shell.java" || true)
    if [ -n "$offenders" ]; then
        echo "ERROR: privileged shell call outside Shell.java:" >&2
        echo "$offenders" >&2
        exit 1
    fi
fi
echo "ok: the only privileged call is Shell.storePattern/clearPattern"
if [ "${SEXTANT_RELEASE:-0}" = "1" ]; then
    echo "[release] certificate and checksum"
    "$BT/apksigner" verify --print-certs "$OUT" | sed -n '1,6p'
    ( cd "$DIST" && sha256sum "$(basename "$OUT")" > "$(basename "$OUT").sha256" )
    cat "$DIST/$(basename "$OUT").sha256"
fi
ls -l "$OUT"
