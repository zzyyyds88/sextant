#!/usr/bin/env bash
# Cross-artifact contract checks.
#
# Everything asserted here is a string that appears in more than one artifact and
# has silently drifted before: the package name, the accessibility component, the
# IME service vs provider authority, the runtime paths, and the status schema keys
# that the daemon, the APK and the PC CLI all have to agree on.
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
MOD="$ROOT/modules/sextant-core"
CFG="$MOD/config/core.json"
APKSRC="$ROOT/apps/pilot/app/src/com/sextant/pilot"
APKROOT="$ROOT/apps/pilot/app"
CLI="$ROOT/apps/pc-cli"
STATUSGO="$MOD/src/internal/status/status.go"

PASS=0; FAIL=0
ok()  { PASS=$((PASS+1)); printf '  ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL+1)); printf '  FAIL %s\n' "$1"; [ -n "${2:-}" ] && printf '       %s\n' "$2"; }
require() { if grep -qF -- "$2" "$3" 2>/dev/null; then ok "$1"; else bad "$1" "not found in ${3#$ROOT/}: $2"; fi; }
require_absent() { if grep -qF -- "$2" "$3" 2>/dev/null; then bad "$1" "must not appear in ${3#$ROOT/}: $2"; else ok "$1"; fi; }

echo "== package identity =="
require "config names the app package" '"package": "com.sextant.pilot"' "$CFG"
require "APK EnginePaths names the same package" 'PKG = "com.sextant.pilot"' "$APKSRC/EnginePaths.java"
# The CLI does not need the package name; what it must share is the daemon port.
require "CLI targets the daemon port 8443" '8443' "$CLI/cmd/sextant/main.go"

echo "== accessibility component =="
require "config declares the a11y component" 'com.sextant.pilot/.hud.RemoteA11yService' "$CFG"
require "config declares the a11y compat service" 'com.google.android.accessibility.selecttospeak.SelectToSpeakService' "$CFG"
require "APK class exists" 'class RemoteA11yService' "$APKSRC/hud/RemoteA11yService.kt"
require "CLI ships the user-like acceptance matrix" 'journey' "$ROOT/apps/pc-cli/cmd/sextant/commands.go"
require "the journey matrix reads the audit log over the API" 'files/download' "$ROOT/apps/pc-cli/cmd/sextant/journey.go"
require_absent "no old module path in a11y layer" '/data/adb/sextant-remote' "$MOD/src/internal/a11y/a11y.go"

echo "== IME: service, provider authority and package are three different strings =="
require "config has ime_service"   '"ime_service": "com.sextant.pilot/.ime.RemoteImeService"' "$CFG"
require "config has ime_provider"  '"ime_provider": "com.sextant.pilot.ime"' "$CFG"
require "APK manifest declares the provider authority" 'com.sextant.pilot.ime' "$ROOT/apps/pilot/app/AndroidManifest.xml"
require "daemon rejects equal service/provider" 'ime_service must not equal app.ime_provider' "$MOD/src/internal/config/config.go"

echo "== runtime paths (one tree) =="
require "config data_dir"  '"data_dir": "/data/adb/sextant-core"' "$CFG"
require "APK reads from its own private dir" 'APP_DIR = "/data/user/0/com.sextant.pilot/files"' "$APKSRC/EnginePaths.java"
require "APK status file is <app dir>/status.json" 'STATUS = APP_DIR + "/status.json"' "$APKSRC/EnginePaths.java"
require "APK calls the module CLI" '"/data/adb/ksu/bin/sextant-core"' "$APKSRC/EnginePaths.java"
require_absent "daemon never uses the old tree" '/data/adb/sextant-remote' "$MOD/src/internal/engine/engine.go"
require_absent "no old harness tree in a11y" '/data/adb/sextant-harness' "$MOD/src/internal/a11y/a11y.go"
require_absent "daemon never uses the old harness tree" '/data/adb/sextant-harness' "$MOD/src/internal/engine/engine.go"

echo "== status schema keys (daemon <-> APK <-> CLI) =="
for key in '"schema"' '"updated_at"' '"daemon"' '"app"' '"unlock"' '"sessions"' '"errors"'; do
    require "status.go declares $key" "$key" "$STATUSGO"
done
for key in 'SCHEMA = 1' 'status.json' 'unlockCredential' 'daemonRunning'; do
    require "APK PilotStatus uses $key" "$key" "$APKSRC/PilotStatus.java"
done
require "CLI parses the status object" '/v1/status' "$CLI/cmd/sextant/main.go"

echo "== the removed layer must stay removed =="
require_absent "no sextant-harnessctl"      'sextant-harnessctl' "$MOD/service.sh"
require_absent "no allowlist tool"         'allowlist-tool'    "$MOD/service.sh"
require_absent "no watch worker"           'watch_worker'      "$MOD/service.sh"
require_absent "no targets.tsv manifest"   'targets.tsv'       "$MOD/service.sh"
if ls "$MOD"/lib/*.sh >/dev/null 2>&1; then
    bad "the shell semantic layer is back" "$(ls "$MOD"/lib/*.sh)"
else
    ok "no shell semantic layer under lib/"
fi

echo "== OpenAPI document covers the wired routes =="
ROUTES="$MOD/src/internal/engine/routes.go"
DOC="$MOD/src/internal/engine/doc.go"
# Routes are registered as "<METHOD> <path>", so match the path without a leading
# quote; the document map has the bare quoted path.
for path in /doc /v1/status /v1/device /v1/device/lock-state /v1/device/unlock-local \
            /v1/input/tap /v1/input/text /v1/ui/screenshot /v1/ui/observe /v1/ui/act \
            /v1/ui/list /v1/ui/options /v1/ui/readback /v1/ui/wait_stable /v1/ui/page /v1/ui/tree \
            /v1/ui/diff \
            /v1/ui/ocr /v1/ui/find /v1/ui/wait /v1/ui/chain /v1/ui/events \
            /v1/sessions /v1/roster /v1/guard/reap \
            /v1/notifications /v1/notifications/events /v1/sms/messages /v1/location /v1/watchdog \
            /v1/metrics \
            /v1/apps /v1/files /v1/uploads /v1/device/reboot \
            /v1/displays /v1/stream/webrtc/offer /v1/stream/stats \
            /v1/ui/color_probe /v1/ui/pixel_stats /v1/ui/panel_read /v1/ui/wait_result \
            /v1/input/global /v1/device/screen /v1/device/rotation \
            /v1/hud /v1/hud/heartbeat /v1/hud/pause /v1/hud/stop /v1/hud/ack /v1/hud/events \
            /v1/device/sim-unlock \
            /v1/triggers /v1/triggers/runs /v1/triggers/events \
            /v1/input/ime /v1/input/ime/text /v1/input/ime/key \
            /v1/settings/daily /v1/audio /v1/audio/mic /v1/audio/route \
            /v1/audio/listen /v1/audio/speak \
            /v1/ui/challenge /v1/ui/challenge/solve /v1/challenge/risk \
            /v1/device/handover /v1/device/handover/return; do
    require "routes wires $path"  "$path\""         "$ROUTES"
    require "doc documents $path" "\"$path\":"      "$DOC"
done

echo "== CLI covers the D29 primitives =="
CLI_DIR="$ROOT/apps/pc-cli/cmd/sextant"
# A route with no CLI caller is the exact gap that let wait/chain sit unused for
# a whole release. These are the primitives an agent is expected to drive.
for path in /v1/ui/act /v1/ui/list /v1/ui/options /v1/ui/readback /v1/ui/page \
            /v1/ui/diff \
            /v1/ui/wait /v1/ui/chain /v1/ui/wait_result /v1/ui/wait_stable /v1/ui/observe \
            /v1/ui/challenge /v1/ui/challenge/solve /v1/challenge/risk \
            /v1/device/handover /v1/device/handover/return; do
    if grep -Rqs -- "\"$path\"" "$CLI_DIR"; then
        ok "CLI calls $path"
    else
        bad "CLI calls $path" "not found under apps/pc-cli/cmd/sextant"
    fi
done
# The daemon's injection bodies name the field `display`; a CLI that sends
# `display_id` silently drives display 0 instead of the one it was asked for.
require_absent "CLI injection bodies use display" '"display_id": displayOf(a)' "$CLI_DIR/handlers.go"

echo "== the web UI branches only on codes the daemon emits =="
# A client that switches on a code the server never sends has a silently dead
# error path; the canonical set lives in internal/httpx.
WEBUI="$MOD/src/internal/engine/webui/app.js"
for code in session_conflict hud_unavailable too_large; do
    require_absent "the web UI does not branch on the dead code \"$code\"" "\"$code\"" "$WEBUI"
done

echo "== every scope a route demands can be granted =="
# /v1/audio/* requires "device"; while it was absent from the
# closed scope set, those endpoints answered 403 for every token that could exist.
ROUTES_ALL="$MOD/src/internal/engine/routes.go"
for f in "$MOD"/src/internal/engine/routes*.go; do
    grep -ho 'auth.Require(\[\]string{[^}]*}' "$f" 2>/dev/null
done | grep -o '"[a-z]*"' | tr -d '"' | sort -u > /tmp/sextant-route-scopes.txt
SCOPEGO="$MOD/src/internal/token/token.go"
while read -r sc; do
    [ -z "$sc" ] && continue
    require "scope \"$sc\" is issuable" "\"$sc\": true" "$SCOPEGO"
done < /tmp/sextant-route-scopes.txt
# And the default PC token must carry the ones the CLI advertises.
require "the default scope list includes device" '"device",' "$SCOPEGO"

echo "== the web UI keeps the elements its script drives =="
# app.js binds these by id. A redesign that renames one leaves a silent no-op:
# the page renders, the button does nothing, and nothing in the logs says why.
WEBHTML="$MOD/src/internal/engine/webui/index.html"
for id in loginView appView tokenInput loginBtn loginError version statusBadge \
          displaySelect logoutBtn banner bannerText retryStreamBtn stage video \
          snapshot stageHint sessionInfo claimBtn releaseBtn textInput \
          sendTextBtn clearTextBtn inputHint activity activityWhat activityMeta \
          dDaemon dA11y dBg dSessions dUptime; do
    require "the web UI has #$id" "id=\"$id\"" "$WEBHTML"
done
# The rail and the activity line read two endpoints; a typo there is a cell that
# stays on the em-dash forever, which looks like a broken device.
require "the web UI polls /v1/status" "\"/v1/status\"" "$WEBUI"
require "the web UI polls /v1/hud" "\"/v1/hud\"" "$WEBUI"
# i18n keys the rail and the activity line use, so no literal falls through to
# showing the key itself.
I18N="$MOD/src/internal/engine/webui/i18n.js"
for key in railDaemon railA11y railBg railSessions railUptime \
           activityIdle activityLive activityPaused activityStopped; do
    require "i18n defines $key" "$key:" "$I18N"
done

echo "== the phone page and the web page show the same four readings =="
# Two views of one product: a person who has seen the browser should not have to
# learn the phone's page. The four labels are the contract between them.
PHONE="$APKSRC/MainActivity.java"
for label in 守护 无障碍 后台放行 会话; do
    require "the phone page shows $label" "\"$label\"" "$PHONE"
done
require "the phone page has one status chip" "chipState" "$PHONE"
require "the phone page keeps its palette in UiKit" "UiKit" "$PHONE"
# The APK shipped with no icon at all (`aapt2 dump badging` printed icon=''). An app
# with no icon is not "minimal"; it is unfinished, and on a phone it shows up as a
# blank square in the launcher.
require "the manifest names a launcher icon" 'android:icon="@mipmap/ic_launcher"' \
    "$APKROOT/AndroidManifest.xml"
for d in mdpi hdpi xhdpi xxhdpi xxxhdpi; do
    # require() greps; a PNG is not text, so existence is checked directly.
    if [ -f "$APKROOT/res/mipmap-$d/ic_launcher.png" ]; then
        ok "the $d launcher icon exists"
    else
        bad "the $d launcher icon exists" "missing res/mipmap-$d/ic_launcher.png"
    fi
done
# A ScrollView child with the default FrameLayout params resolves to MATCH_PARENT
# height and collapses the whole page: measured on the device, 42 nodes present and
# one line visible. Both halves of the fix are asserted so a refactor cannot undo it.
require "the phone page opts out of fillViewport" "setFillViewport(false)" "$PHONE"
require "the scroll child is measured with wrap_content" \
    "ViewGroup.LayoutParams.WRAP_CONTENT));" "$PHONE"
# Reachability: the port was a bare literal in three places and shown nowhere.
require "the port is one constant" "static final int PORT = 8443" "$APKSRC/EnginePaths.java"
require "the phone page shows the WebUI address" "EnginePaths.WEBUI_PATH" "$PHONE"
require "the web UI shows its own address" 'id="accessUrl"' \
    "$MOD/src/internal/engine/webui/index.html"
UK="$APKSRC/UiKit.java"
require "UiKit resolves a dark palette from the system" "UI_MODE_NIGHT_MASK" "$UK"
require "UiKit keeps body text at 14sp or larger" "setTextSize(14)" "$UK"

echo "== the audio bridge stays reachable =="
# Every audio method the daemon calls must exist in the helper's dispatch table.
# A missing one is not a compile error anywhere — it is a 503 at request time.
AB="$MOD/helper/src/com/sextant/core/helper/AndroidBridge.java"
for m in audio_state audio_mic_mute audio_speakerphone audio_mode audio_listen audio_speak; do
    require "the helper dispatches $m" "\"$m\".equals(method)" "$AB"
done
require "the audio bridge reports silence, not just success" \
    '"silent", rms < 8' "$MOD/helper/src/com/sextant/core/helper/AudioBridge.java"
# Playback still has to say where its track really went: AudioAttributes describe
# what was asked for, and the policy is free to route it elsewhere.
ABRIDGE="$MOD/helper/src/com/sextant/core/helper/AudioBridge.java"
require "the helper reports where the track really went" 'routedDeviceName' "$ABRIDGE"

echo "== the helper keeps the accessibility flag =="
# Losing this flag silently unbinds every third-party accessibility service while
# settings still look correct (DESIGN §10.1 #6), so it is asserted here too.
HELPER="$MOD/helper/src/com/sextant/core/helper/AndroidBridge.java"
require "helper sets FLAG_DONT_SUPPRESS_ACCESSIBILITY_SERVICES" \
    'FLAG_DONT_SUPPRESS_ACCESSIBILITY_SERVICES' "$HELPER"
require "daemon ships the helper jar" 'lib/helper.jar' "$MOD/customize.sh"

echo
if [ "$FAIL" -eq 0 ]; then
    echo "check-contracts: OK ($PASS passed)"
    exit 0
fi
echo "check-contracts: FAILED ($PASS passed, $FAIL failed)"
exit 1
