#!/usr/bin/env bash
# Static acceptance for the client APK.
#
# The APK is a zero-root client (docs/DESIGN.md D20/D21): no engine, no helper, no
# scrcpy payload, no execute-a-root-process code. So this script checks the
# opposite of what the old one checked — it asserts those things are ABSENT.
set -uo pipefail

APP=$(cd "$(dirname "$0")/.." && pwd)
ROOT=$(cd "$APP/../.." && pwd)
SDK=${ANDROID_SDK_ROOT:-"$ROOT/tools/android-sdk"}
BT=${ANDROID_BUILD_TOOLS:-"$SDK/build-tools/android-35"}
AAPT2="$BT/aapt2"
APKSIGNER="$BT/apksigner"
APK=${1:-$(ls -1t "$APP"/dist/*.apk 2>/dev/null | head -1)}

[ -f "$APK" ] || { echo "FAIL: no APK (run ./build.sh first)" >&2; exit 2; }

PASS=0; FAIL=0
ok()  { PASS=$((PASS+1)); printf '  ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL+1)); printf '  FAIL %s\n' "$1"; [ -n "${2:-}" ] && printf '       %s\n' "$2"; }
has() { case "$2" in *"$3"*) ok "$1" ;; *) bad "$1" "missing [$3]" ;; esac; }
hasnt() { case "$2" in *"$3"*) bad "$1" "must not contain [$3]" ;; *) ok "$1" ;; esac; }

WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT

echo "== manifest =="
BADGING=$("$AAPT2" dump badging "$APK" 2>/dev/null)
has  "package is com.sextant.pilot" "$BADGING" "package: name='com.sextant.pilot'"
has  "minSdk 29"                    "$BADGING" "minSdkVersion:'29'"
has  "targetSdk 33"                 "$BADGING" "targetSdkVersion:'33'"
has  "has a launcher activity"      "$BADGING" "launchable-activity: name='com.sextant.pilot.MainActivity'"

echo "== components =="
MANIFEST=$("$AAPT2" dump xmltree --file AndroidManifest.xml "$APK" 2>/dev/null)
has "accessibility service declared" "$MANIFEST" "RemoteA11yService"
has "IME service declared"           "$MANIFEST" "RemoteImeService"
has "IME provider declared"          "$MANIFEST" "com.sextant.pilot.ime"
has "boot receiver declared"         "$MANIFEST" "BootReceiver"

echo "== zero root (the whole point of D21) =="
unzip -q -o "$APK" -d "$WORK" 2>/dev/null
BINARIES=$(find "$WORK" -name '*.so' -o -name 'assets' -type d 2>/dev/null)
if [ -z "$BINARIES" ]; then
    ok "no native payload in the APK"
else
    so=$(unzip -l "$APK" | grep -c '\.so$' || true)
    if [ "${so:-0}" -eq 0 ]; then ok "no .so payload in the APK"; else bad "APK ships $so native libraries"; fi
fi
if unzip -l "$APK" | grep -qE 'assets/(helper\.jar|scrcpy-server\.jar|config\.json)'; then
    bad "APK must not ship the engine payload"
else
    ok "no engine/helper/scrcpy payload"
fi
DEXSTR=$(strings "$WORK"/classes*.dex 2>/dev/null | sort -u)
# `su` itself is present on purpose: the one allowed privileged call is writing the
# unlock credential. What must be gone is every path that used to run an engine.
has    "the one su call is the ctl secret write" "$DEXSTR" "ctl secret set-unlock"
hasnt "no engine staging in dex"       "$DEXSTR" "libremote_engine"
hasnt "no old harness CLI in dex"      "$DEXSTR" "sextant-harnessctl"
hasnt "no old module paths in dex"     "$DEXSTR" "/data/adb/sextant-remote"
hasnt "no old harness module paths in dex" "$DEXSTR" "/data/adb/sextant-harness"
has    "reads the published status"    "$DEXSTR" "/data/user/0/com.sextant.pilot/files/status.json"

echo "== signature =="
"$APKSIGNER" verify "$APK" >/dev/null 2>&1 && ok "apksigner verify" || bad "apksigner verify"
CERT=$("$APKSIGNER" verify --print-certs "$APK" 2>/dev/null | head -1)
# The signing key is deliberately reused from the pre-3.0 app, so its DN is the
# historical CN=com.sextant.remote. Do not tie the assertion to the new package
# name: what matters is that the APK is signed and the local keystore is reused.
has "APK is signed with a certificate" "$CERT" "Signer #1 certificate DN:"

echo "== source hygiene =="
# The single privileged call is documented in apps/pilot/README.md: writing the
# unlock credential through `su` + `sextant-core ctl secret set-unlock`.
offenders=$(grep -rln 'ProcessBuilder("su"' "$APP/app/src" 2>/dev/null | grep -v 'Shell.java' || true)
if [ -z "$offenders" ]; then ok "only Shell.java runs su"; else bad "privileged shell outside Shell.java" "$offenders"; fi
grep -q 'ctl secret set-unlock' "$APP/app/src/com/sextant/pilot/Shell.java" 2>/dev/null \
    && ok "Shell.java uses the documented ctl subcommand" \
    || bad "Shell.java no longer uses ctl secret set-unlock"

echo "== summary =="
if [ "$FAIL" -eq 0 ]; then
    echo "verify-apk: OK ($PASS passed)"
    exit 0
fi
echo "verify-apk: FAILED ($PASS passed, $FAIL failed)"
exit 1
