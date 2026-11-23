#!/usr/bin/env bash
# Activate the built module on the device WITHOUT rebooting.
#
# Why this exists: rebooting is slow, costs a minute or two each time, and is
# almost never the thing under test. A daemon-side change (Go code, helper jar,
# APK, config, WebUI) only needs the old binary stopped and the new module tree
# copied into place — the same code path `service.sh` runs at boot, minus the boot.
#
# What genuinely needs a reboot is only the boot path itself: KernelSU reading the
# module at startup, `post-fs-data.sh`, and the ordering between them. Use
# `install-device.sh` (which reboots once) for that, and for a clean-install
# verification. Everything else goes through here.
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
SERIAL=${SERIAL:-}
MOD=chopin-core
CLI=${CLI:-$ROOT/apps/pc-cli/dist/chopin-linux-amd64}
ZIP="$ROOT/modules/$MOD.zip"

say()  { printf '\n== %s ==\n' "$1"; }
info() { printf '   %s\n' "$*"; }
die()  { printf '\nERROR: %s\n' "$*" >&2; exit 1; }
[ -n "$SERIAL" ] || die "set SERIAL to the adb serial (host:port)"

D()  { adb -s "$SERIAL" shell "$@" 2>&1 | tr -d '\r'; }
SU() { adb -s "$SERIAL" shell "su -c '$1'" 2>&1 | tr -d '\r'; }

[ -f "$ZIP" ] || die "missing $ZIP (run tools/scripts/build-module.sh modules/$MOD)"
adb -s "$SERIAL" get-state >/dev/null 2>&1 || die "device not reachable at $SERIAL"

say "install the zip (stages into modules_update)"
adb -s "$SERIAL" push "$ZIP" /data/local/tmp/$MOD.zip 2>&1 | tail -1 | sed 's/^/   /'
out=$(SU "ksud module install /data/local/tmp/$MOD.zip")
printf '%s\n' "$out" | tail -2 | sed 's/^/   /'
case "$out" in *"Done"*) info "staged" ;; *) die "module install failed" ;; esac

say "stop the daemon (its binary is about to be replaced)"
# Wait for the old daemon to actually exit, not for a guessed sleep: it takes up
# to 10s to shut down gracefully and it holds the lock until it does, so starting
# the new one too early fails with "another daemon already holds the lock" and the
# device ends up with no daemon at all. pgrep -x matches comm exactly, so this
# cannot match the su/shell that is running the check (pkill -f could, and did).
SU 'if [ -f /data/adb/chopin-core/state/engine.pid ]; then kill "$(cat /data/adb/chopin-core/state/engine.pid)" 2>/dev/null; fi; i=0; while [ $i -lt 150 ] && pgrep -x chopin-core >/dev/null 2>&1; do sleep 0.2; i=$((i+1)); done; pgrep -x chopin-core >/dev/null 2>&1 && pkill -9 -x chopin-core; pkill -f HelperMain 2>/dev/null; true' >/dev/null
info "stopped"

say "copy the staged tree over the live module"
SU "cp -a /data/adb/modules_update/$MOD/. /data/adb/modules/$MOD/ && rm -rf /data/adb/modules_update/$MOD && echo copied" | sed 's/^/   /'

say "start the daemon the same way service.sh does"
SU "setsid /data/adb/modules/$MOD/bin/chopin-core daemon --config /data/adb/chopin-core/config/core.json </dev/null >>/data/adb/chopin-core/log/core.log 2>&1 &" >/dev/null

say "wait for it to answer"
ready=0
for _ in $(seq 1 40); do
    if "$CLI" health 2>/dev/null | grep -q '"ok"'; then ready=1; break; fi
    sleep 2
done
[ "$ready" = 1 ] || die "the daemon did not answer; see adb -s $SERIAL shell su -c 'tail -30 /data/adb/chopin-core/log/core.log'"
info "daemon answering"
"$CLI" status 2>/dev/null | python3 -c 'import json,sys; d=json.load(sys.stdin); print("   a11y", d["app"]["a11y"]["state"], "| bg_ok", d["app"]["bg"]["ok"])' 2>/dev/null || true

say "install the client APK if it changed"
# The APK is NOT part of the module: it lives in /data/app as com.chopin.pilot, so
# copying the module tree does nothing for it. This script claimed to cover "APK
# changes" and did not — measured: the installed base.apk's sha256 differed from
# every build produced for two hours, and a fixed overlay behaviour never appeared on
# the device while `journey` kept passing. Comparing the hash is cheap; skipping it
# costs an afternoon of testing the wrong binary.
APK=$(ls -1t "$ROOT"/apps/pilot/dist/*.apk 2>/dev/null | head -1)
if [ -n "$APK" ]; then
    want=$(sha256sum "$APK" | cut -d' ' -f1)
    have=$(SU "sha256sum /data/app/*/com.chopin.pilot*/base.apk 2>/dev/null" | head -1 | awk '{print $1}')
    if [ "$want" = "$have" ]; then
        info "APK already current ($(basename "$APK"))"
    else
        adb -s "$SERIAL" install -r "$APK" 2>&1 | tail -1 | sed 's/^/   /'
        # The install lands in a NEW /data/app/~~.../ directory, so the glob has to be
        # re-evaluated against the new path; and the package manager finishes moving
        # the file slightly after `install` returns. Both were reasons this check
        # reported a false mismatch on an install that had in fact worked.
        # The remote side of SU() is single-quoted, so the command must not contain a
        # single quote of its own (an earlier version used cut -d' ' and the device
        # received garbage). awk with a double-quoted program is quote-safe here.
        have2=""
        for _ in $(seq 1 10); do
            have2=$(SU "sha256sum \$(ls -d /data/app/*/com.chopin.pilot*/base.apk 2>/dev/null | tail -1)" \
                    | awk '{print $1}')
            [ "$want" = "$have2" ] && break
            sleep 1
        done
        if [ "$want" = "$have2" ]; then
            info "APK installed and matches the build"
        else
            printf '   WARNING: APK hash still differs after install (want %s, have %s)\n' "$want" "$have2" >&2
        fi
        # A reinstalled package's accessibility service is unbound until the system
        # rebinds it; the daemon's keepalive does that within a few seconds.
        info "waiting for the accessibility service to rebind"
        for _ in $(seq 1 30); do
            if "$CLI" status 2>/dev/null | grep -q '"state": *"OK"'; then break; fi
            sleep 1
        done
    fi
else
    info "no APK built; skipping"
fi

say "done (no reboot)"
echo "   boot-path changes (service.sh / post-fs-data.sh / clean install) still need install-device.sh"
