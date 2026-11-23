#!/usr/bin/env bash
# One command for the whole static gate.
#
# DESIGN §11.2: this layer only covers "it compiles and the contracts agree".
# Capability correctness is proven on the device, not here, and the total number
# of assertions is capped at 60 so the suite cannot silently grow back into a
# substitute for real verification.
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
GO=${GO:-$ROOT/tools/go/bin/go}
GOFMT=${GOFMT:-$ROOT/tools/go/bin/gofmt}
FAIL=0
TOTAL=0
step() { printf '\n=== %s ===\n' "$1"; }
fail() { FAIL=$((FAIL+1)); printf '  >>> FAILED: %s\n' "$1"; }
pass() { TOTAL=$((TOTAL+1)); printf '  ok   %s\n' "$1"; }

# 1. Go formatting ------------------------------------------------------------
step "1. gofmt (device daemon, PC CLI)"
bad_fmt=$({ "$GOFMT" -l "$ROOT/modules/sextant-core/src" "$ROOT/apps/pc-cli" 2>/dev/null; } || true)
if [ -z "$bad_fmt" ]; then pass "gofmt clean"; else fail "gofmt: $bad_fmt"; fi

# 2. Go vet + build -----------------------------------------------------------
step "2. go vet + go build (device daemon)"
( cd "$ROOT/modules/sextant-core/src" && GOPROXY=off "$GO" vet ./... 2>&1 ) && pass "daemon vet" || fail "daemon vet"
( cd "$ROOT/modules/sextant-core/src" && GOPROXY=off GOOS=linux GOARCH=arm64 CGO_ENABLED=0 "$GO" build ./... 2>&1 ) \
    && pass "daemon cross-builds for linux/arm64" || fail "daemon cross-build"

step "2b. go vet + go build (PC CLI)"
( cd "$ROOT/apps/pc-cli" && GOPROXY=off "$GO" vet ./... 2>&1 ) && pass "cli vet" || fail "cli vet"
( cd "$ROOT/apps/pc-cli" && GOPROXY=off CGO_ENABLED=0 "$GO" build ./... 2>&1 ) && pass "cli builds" || fail "cli build"

# 3. Go unit tests ------------------------------------------------------------
step "3. go test (offline logic only: parsers, state machines, sessions)"
( cd "$ROOT/modules/sextant-core/src" && GOPROXY=off "$GO" test ./... 2>&1 | tail -12 )
if ( cd "$ROOT/modules/sextant-core/src" && GOPROXY=off "$GO" test ./... >/dev/null 2>&1 ); then
    pass "daemon unit tests"
else
    fail "daemon unit tests"
fi
if ( cd "$ROOT/apps/pc-cli" && GOPROXY=off "$GO" test ./... >/dev/null 2>&1 ); then
    pass "cli unit tests"
else
    fail "cli unit tests"
fi

# 4. Shell syntax -------------------------------------------------------------
step "4. sh -n (the five lifecycle scripts + build scripts)"
for f in "$ROOT"/modules/sextant-core/customize.sh \
         "$ROOT"/modules/sextant-core/post-fs-data.sh \
         "$ROOT"/modules/sextant-core/service.sh \
         "$ROOT"/modules/sextant-core/uninstall.sh; do
    if sh -n "$f" 2>/dev/null; then pass "sh -n $(basename "$f")"; else fail "sh -n $f"; fi
done
for f in "$ROOT"/tools/scripts/build-*.sh; do
    bash -n "$f" 2>/dev/null || fail "bash -n $f"
done
pass "build scripts parse"

# 5. Cross-artifact contracts -------------------------------------------------
step "5. contracts (config / APK / CLI / doc must agree)"
if bash "$ROOT/tools/scripts/check-contracts.sh"; then pass "check-contracts"; else fail "check-contracts"; fi

# 6. APK ---------------------------------------------------------------------
step "6. APK (zero-root client)"
if [ -f "$(ls -1t "$ROOT"/apps/pilot/dist/*.apk 2>/dev/null | head -1)" ]; then
    if bash "$ROOT/apps/pilot/tools/verify-apk.sh"; then pass "verify-apk"; else fail "verify-apk"; fi
else
    fail "no APK built (run apps/pilot/build.sh)"
fi

# 7. Docs --------------------------------------------------------------------
step "7. docs index"
if bash "$ROOT/tools/scripts/check-docs.sh"; then pass "check-docs"; else fail "check-docs"; fi

# 8. Module zip contents ------------------------------------------------------
step "8. module zip (must ship runtime only)"
if [ -f "$ROOT/modules/sextant-core.zip" ]; then
    listing=$(unzip -l "$ROOT/modules/sextant-core.zip")
    for want in bin/sextant-core lib/helper.jar lib/scrcpy-server.jar lib/app.apk \
                config/core.json config/unlock-secret service.sh post-fs-data.sh; do
        case "$listing" in *"$want"*) pass "zip has $want" ;; *) fail "zip missing $want" ;; esac
    done
    case "$listing" in *"/src/"*) fail "zip must not ship src/" ;; *) pass "zip excludes src/" ;; esac
    # The privileged app overlay. Asserted on the ZIP, not on the source tree: an
    # overlay that exists in the repo but is not packaged changes nothing on a device.
    case "$listing" in
        *"system/priv-app/SextantPilot/"*) pass "zip has the privileged APK" ;;
        *) fail "zip missing system/priv-app/SextantPilot/*.apk" ;;
    esac
    case "$listing" in
        *"system/etc/permissions/privapp-permissions-sextant-pilot.xml"*) pass "zip has the privapp whitelist" ;;
        *) fail "zip missing system/etc/permissions/privapp-permissions-sextant-pilot.xml" ;;
    esac
    case "$listing" in *"/tests/"*) fail "zip must not ship tests/" ;; *) pass "zip excludes tests/" ;; esac
    if printf '%s\n' "$listing" | awk '{print $4}' | grep -qi 'chopin'; then
        fail "zip contains an old chopin path"
    else
        pass "zip has no old chopin path"
    fi
else
    fail "no module zip (run tools/scripts/build-module.sh modules/sextant-core)"
fi

# Summary ---------------------------------------------------------------------
printf '\n=== summary ===\n'
printf 'assertions: %d, failures: %d\n' "$TOTAL" "$FAIL"
if [ "$TOTAL" -gt 60 ]; then
    printf '>>> the static gate grew past its 60-assertion budget\n'
    FAIL=$((FAIL+1))
fi
if [ "$FAIL" -eq 0 ]; then
    echo "run-all-static: OK"
    exit 0
fi
echo "run-all-static: FAILED ($FAIL)"
exit 1
