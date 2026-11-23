#!/usr/bin/env bash
# Build the root helper dex the daemon loads with app_process.
#
# The helper is the mechanism for everything that needs the framework from a root
# process: UiAutomation, notification listener, SMS, location, capture. It is
# shipped as lib/helper.jar in the module.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
SRC="$ROOT/modules/sextant-core/helper/src"
OUT="$ROOT/modules/sextant-core/lib/helper.jar"
BUILD="$ROOT/modules/sextant-core/helper/out"
SDK=${ANDROID_SDK_ROOT:-"$ROOT/tools/android-sdk"}
BT=${ANDROID_BUILD_TOOLS:-"$SDK/build-tools/android-35"}
PLATFORM=${ANDROID_PLATFORM:-"$SDK/platforms/android-34/android.jar"}

[ -f "$PLATFORM" ] || { echo "missing android.jar: $PLATFORM" >&2; exit 1; }
[ -d "$SRC" ] || { echo "no helper source: $SRC" >&2; exit 1; }

rm -rf "$BUILD"
mkdir -p "$BUILD/classes" "$BUILD/dex"
mapfile -t SRC_FILES < <(find "$SRC" -name '*.java' | sort)
echo "compiling ${#SRC_FILES[@]} helper source(s)"
javac -encoding UTF-8 --release 8 -nowarn -classpath "$PLATFORM" -d "$BUILD/classes" "${SRC_FILES[@]}"
jar cf "$BUILD/helper-classes.jar" -C "$BUILD/classes" .
"$BT/d8" --release --min-api 29 --lib "$PLATFORM" \
  --output "$BUILD/dex" "$BUILD/helper-classes.jar"
mkdir -p "$(dirname "$OUT")"
( cd "$BUILD/dex" && zip -q -j "$OUT" classes.dex )
chmod 0644 "$OUT"
ls -l "$OUT"
