#!/usr/bin/env bash
# Install sextant-core onto the phone.
#
# The module IS the product (DESIGN D20): it starts the daemon as root at boot, so
# there is no allowlist to stage, no second reboot to catch up, and no root grant
# for the APK to wait on. The order is simply: zip → module install → reboot →
# wait for the daemon to answer.
#
#   tools/scripts/install-device.sh --check     # verify the inputs, change nothing
#   tools/scripts/install-device.sh             # install and reboot
#   tools/scripts/install-device.sh --no-reboot # install and activate without rebooting
#   tools/scripts/install-device.sh --rollback  # remove module and APK
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
SERIAL=${SERIAL:-}
PKG=${PKG:-com.sextant.pilot}
ZIP="$ROOT/modules/sextant-core.zip"
CLI=${CLI:-$ROOT/apps/pc-cli/dist/sextant-linux-amd64}

MODE=install
REBOOT=1
case "${1:-}" in
    --check)     MODE=check ;;
    --no-reboot) REBOOT=0 ;;
    --rollback)  MODE=rollback ;;
    --help|-h)
        awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"
        exit 0 ;;
    "") ;;
    *) echo "unknown option: $1 (try --help)" >&2; exit 2 ;;
esac

say()  { printf '\n== %s ==\n' "$1"; }
info() { printf '   %s\n' "$*"; }
die()  { printf '\nERROR: %s\n' "$*" >&2; exit 1; }
[ -n "$SERIAL" ] || die "set SERIAL to the adb serial (host:port)"

D()  { adb -s "$SERIAL" shell "$@" 2>&1 | tr -d '\r'; }
SU() { adb -s "$SERIAL" shell "su -c '$1'" 2>&1 | tr -d '\r'; }

say "preflight"
command -v adb >/dev/null || die "adb not found in PATH"
[ "$MODE" = rollback ] || [ -f "$ZIP" ] || die "missing $ZIP (run tools/scripts/build-module.sh modules/sextant-core)"
[ "$MODE" = rollback ] || [ -x "$CLI" ] || die "missing PC CLI at $CLI (run apps/pc-cli/build.sh)"

adb connect "$SERIAL" >/dev/null 2>&1 || true
state=$(adb -s "$SERIAL" get-state 2>&1 | tr -d '\r')
[ "$state" = device ] || die "device not reachable ($state). Check: phone on? same wifi? wireless debugging enabled?"

info "device: $(D getprop ro.product.model)  android $(D getprop ro.build.version.sdk)"
uid=$(SU 'id -u')
[ "$uid" = 0 ] || die "no root on the device (su -c 'id -u' => $uid); KernelSU must work before the module can be installed"
info "root: ok"

if [ "$MODE" = rollback ]; then
    say "rollback"
    SU 'ksud module uninstall sextant-core' | sed 's/^/   /'
    adb -s "$SERIAL" uninstall "$PKG" 2>&1 | sed 's/^/   /'
    say "reboot (uninstall.sh restores the accessibility setting on the way out)"
    adb -s "$SERIAL" reboot
    exit 0
fi

say "install module"
adb -s "$SERIAL" push "$ZIP" /data/local/tmp/sextant-core.zip 2>&1 | tail -1 | sed 's/^/   /'
out=$(SU 'ksud module install /data/local/tmp/sextant-core.zip')
printf '%s\n' "$out" | tail -3 | sed 's/^/   /'
case "$out" in
    *"Done"*) info "module installed" ;;
    *) die "module install failed:
$out" ;;
esac

if [ "$MODE" = check ]; then
    say "dry run complete"
    info "inputs present, device reachable, root working"
    exit 0
fi

if [ "$REBOOT" = 0 ]; then
    # Activation without a reboot: KernelSU stages into modules_update, and the
    # daemon has to be stopped before its binary can be replaced.
    say "activate without rebooting"
    SU '/data/local/tmp/stop-hx.sh >/dev/null 2>&1 || pkill -f "sextant-core daemon" || true' >/dev/null
    SU 'cp -a /data/adb/modules_update/sextant-core/. /data/adb/modules/sextant-core/ && rm -rf /data/adb/modules_update/sextant-core && echo activated' | sed 's/^/   /'
    SU 'setsid /data/adb/modules/sextant-core/bin/sextant-core daemon --config /data/adb/sextant-core/config/core.json </dev/null >/data/adb/sextant-core/log/daemon.out 2>&1 &' >/dev/null
else
    say "reboot"
    echo "   service.sh starts the daemon as root after sys.boot_completed; nothing else to do."
    adb -s "$SERIAL" reboot
    adb -s "$SERIAL" wait-for-device >/dev/null 2>&1
    echo "   waiting for the daemon to answer…"
fi

say "wait for the daemon"
ready=0
for _ in $(seq 1 60); do
    if "$CLI" health 2>/dev/null | grep -q '"ok"'; then ready=1; break; fi
    sleep 3
done
[ "$ready" = 1 ] || die "the daemon did not answer within ~3 minutes. Check: adb -s $SERIAL shell su -c 'cat /data/adb/sextant-core/log/core.log'"
info "daemon answering"

say "pair the PC (idempotent)"
"$CLI" pair --serial "$SERIAL" 2>&1 | sed 's/^/   /'

say "verify"
"$CLI" status | sed 's/^/   /'
say "done"
echo "   next: sextant acceptance"
