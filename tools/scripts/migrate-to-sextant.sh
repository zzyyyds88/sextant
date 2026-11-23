#!/usr/bin/env bash
# Migrate an installed chopin-core / com.chopin.pilot device to Sextant.
#
# This is destructive by design:
#   - the old APK is uninstalled and its private data is lost;
#   - the old KernelSU module is removed;
#   - only the daemon credentials/tokens/baseline are copied to the new state root.
#
# Required:
#   SERIAL=<adb serial> CONFIRM=1 tools/scripts/migrate-to-sextant.sh
#
# Optional:
#   SEXTANT_ADB_HOME=<adb HOME>   use a dedicated adb key directory
#   RUN_ACCEPTANCE=1              run `sextant acceptance` and `sextant journey` after health
#
# A backup is written before anything is removed:
#   device: /data/adb/sextant-migration-<ts>/  (root-only)
#   PC:     out/migration-<ts>/                 (old ~/.chopin, 0700)
#
# The old APK data is not recoverable. The old module/state backup is the rollback
# source; reinstall the old module zip and APK only if you accept that data loss.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
SERIAL=${SERIAL:-}
CONFIRM=${CONFIRM:-0}
RUN_ACCEPTANCE=${RUN_ACCEPTANCE:-0}
ZIP="$ROOT/modules/sextant-core.zip"
CLI="$ROOT/apps/pc-cli/dist/sextant-linux-amd64"
APK=$(ls -1t "$ROOT"/apps/pilot/dist/sextant-pilot-*.apk 2>/dev/null | head -1 || true)
OLD_DATA=/data/adb/chopin-core
OLD_MOD=/data/adb/modules/chopin-core
OLD_BIN=/data/adb/ksu/bin/chopin-core
OLD_PKG=com.chopin.pilot
NEW_DATA=/data/adb/sextant-core
NEW_MOD=/data/adb/modules/sextant-core
NEW_BIN=/data/adb/ksu/bin/sextant-core
NEW_PKG=com.sextant.pilot
TS=$(date +%Y%m%d-%H%M%S)
REMOTE_BACKUP="/data/adb/sextant-migration-$TS"
LOCAL_BACKUP="$ROOT/out/migration-$TS"

say()  { printf '\n== %s ==\n' "$1"; }
info() { printf '   %s\n' "$*"; }
die()  { printf '\nERROR: %s\n' "$*" >&2; exit 1; }

[ -n "$SERIAL" ] || die "set SERIAL to the adb serial (host:port)"
[ "$CONFIRM" = "1" ] || die "refusing to run without CONFIRM=1 (this uninstalls the old APK and module)"
export SEXTANT_SERIAL="$SERIAL"
[ -f "$ZIP" ] || die "missing $ZIP (run tools/scripts/build-module.sh modules/sextant-core)"
[ -f "$CLI" ] || die "missing $CLI (run apps/pc-cli/build.sh)"
[ -n "$APK" ] && [ -f "$APK" ] || die "missing apps/pilot/dist/sextant-pilot-*.apk (run apps/pilot/build.sh)"

adb_cmd() {
    if [ -n "${SEXTANT_ADB_HOME:-}" ]; then
        HOME="$SEXTANT_ADB_HOME" adb "$@"
    else
        adb "$@"
    fi
}
D()  { adb_cmd -s "$SERIAL" shell "$@" < /dev/null 2>&1 | tr -d '\r'; }
SU() { adb_cmd -s "$SERIAL" shell "su -c '$*'" < /dev/null 2>&1 | tr -d '\r'; }

adb_cmd -s "$SERIAL" get-state >/dev/null 2>&1 || die "device not reachable at $SERIAL"
[ "$(SU 'test -d /data/adb/modules/chopin-core && echo yes || echo no')" = "yes" ] \
    || die "chopin-core module not found; use tools/scripts/install-device.sh for a clean Sextant install"

say "migration plan"
info "old: $OLD_MOD / $OLD_PKG / $OLD_DATA"
info "new: $NEW_MOD / $NEW_PKG / $NEW_DATA"
info "device backup: $REMOTE_BACKUP"
info "PC backup:     $LOCAL_BACKUP"
info "old APK data will be deleted"
info "new TLS certificate will be generated; the CLI will be re-paired"

say "back up old module, state and PC config"
SU "mkdir -p $REMOTE_BACKUP" || die "cannot create $REMOTE_BACKUP"
SU "chmod 0700 $REMOTE_BACKUP"
if [ "$(SU "test -d $OLD_DATA && echo yes || echo no")" = "yes" ]; then
    SU "cp -a $OLD_DATA $REMOTE_BACKUP/chopin-core" || die "backup of $OLD_DATA failed"
else
    info "no $OLD_DATA; continuing with module/APK only"
fi
SU "cp -a $OLD_MOD $REMOTE_BACKUP/module" || die "backup of $OLD_MOD failed"
mkdir -p "$LOCAL_BACKUP"
chmod 0700 "$LOCAL_BACKUP"
REAL_HOME=${HOME:-/root}
if [ -d "$REAL_HOME/.chopin" ]; then
    cp -a "$REAL_HOME/.chopin" "$LOCAL_BACKUP/chopin-cli" || die "backup of ~/.chopin failed"
    chmod -R go-rwx "$LOCAL_BACKUP/chopin-cli"
fi

say "stop the old daemon"
pids=$(SU "pidof chopin-core 2>/dev/null || true")
if [ -n "$pids" ]; then
    info "killing old pid(s): $pids"
    SU "kill $pids" || true
    i=0
    while [ "$i" -lt 20 ]; do
        [ -z "$(SU "pidof chopin-core 2>/dev/null || true")" ] && break
        i=$((i + 1))
        sleep 1
    done
    if [ -n "$(SU "pidof chopin-core 2>/dev/null || true")" ]; then
        info "old daemon did not exit; sending SIGKILL"
        SU "kill -9 $pids" || true
    fi
else
    info "no old daemon pid"
fi

say "remove the old APK (data loss) and module"
if [ -n "$(D "pm path $OLD_PKG" || true)" ]; then
    SU "pm uninstall $OLD_PKG" || true
fi
old_uninstall=$(SU "ksud module uninstall chopin-core" || true)
printf '%s\n' "$old_uninstall" | tail -4 | sed 's/^/   /'
SU "rm -f $OLD_BIN" || true

say "migrate durable daemon state"
SU "mkdir -p $NEW_DATA/config $NEW_DATA/state" || die "cannot create $NEW_DATA"
SU "chmod 0700 $NEW_DATA $NEW_DATA/config $NEW_DATA/state"
for f in unlock-secret sim-pin; do
    if [ "$(SU "test -f $REMOTE_BACKUP/chopin-core/config/$f && echo yes || echo no")" = "yes" ]; then
        SU "cp -a $REMOTE_BACKUP/chopin-core/config/$f $NEW_DATA/config/$f"
        SU "chmod 0600 $NEW_DATA/config/$f"
    fi
done
if [ "$(SU "test -d $REMOTE_BACKUP/chopin-core/state && echo yes || echo no")" = "yes" ]; then
    SU "cp -a $REMOTE_BACKUP/chopin-core/state/. $NEW_DATA/state/" || true
    SU "rm -f $NEW_DATA/state/engine.pid $NEW_DATA/state/engine.lock $NEW_DATA/state/sessions.json $NEW_DATA/state/pairing.json"
fi
# core.json and pki are intentionally not copied: the new module seeds its own
# config (new package/data paths) and generates a new TLS certificate.

say "install sextant-core"
adb_cmd -s "$SERIAL" push "$ZIP" /data/local/tmp/sextant-core.zip < /dev/null 2>&1 | tail -1 | sed 's/^/   /'
out=$(SU "ksud module install /data/local/tmp/sextant-core.zip")
printf '%s\n' "$out" | tail -4 | sed 's/^/   /'
case "$out" in *"Done"*) info "staged" ;; *) die "module install failed; backup: $REMOTE_BACKUP" ;; esac

say "reboot into sextant-core"
adb_cmd -s "$SERIAL" reboot >/dev/null 2>&1 || true
adb_cmd -s "$SERIAL" wait-for-device >/dev/null 2>&1 || true
i=0
ready=0
while [ "$i" -lt 180 ]; do
    if [ "$(SU "test -x $NEW_MOD/bin/sextant-core && echo yes || echo no")" = "yes" ]; then
        if SU "$NEW_MOD/bin/sextant-core ctl status >/dev/null 2>&1 && echo ok || echo no" | grep -q ok; then
            ready=1
            break
        fi
    fi
    i=$((i + 1))
    sleep 2
done
[ "$ready" = "1" ] || die "sextant daemon did not become ready; backup: $REMOTE_BACKUP"

say "install the new APK and pair"
adb_cmd -s "$SERIAL" install -r "$APK" < /dev/null 2>&1 | tail -3 | sed 's/^/   /' || true
"$CLI" pair --serial "$SERIAL" 2>&1 | sed 's/^/   /'
"$CLI" health 2>&1 | sed 's/^/   /'
"$CLI" doctor 2>&1 | sed 's/^/   /' || info "doctor reported a failure; inspect before accepting the migration"

say "verify old identity is gone"
old_pkg=$(D "pm path $OLD_PKG" || true)
[ -z "$old_pkg" ] && info "old package absent" || info "WARNING: old package still present: $old_pkg"
old_mod=$(SU "test -d $OLD_MOD && echo yes || echo no")
[ "$old_mod" = "no" ] && info "old module absent" || info "WARNING: old module directory still present"
old_proc=$(SU "pidof chopin-core 2>/dev/null || true")
[ -z "$old_proc" ] && info "old daemon absent" || info "WARNING: old daemon still running: $old_proc"
new_pkg=$(D "pm path $NEW_PKG" || true)
[ -n "$new_pkg" ] && info "new package: $new_pkg" || info "WARNING: new package not installed yet; next boot retries"

if [ "$RUN_ACCEPTANCE" = "1" ]; then
    say "run acceptance"
    "$CLI" acceptance 2>&1 | tail -20 | sed 's/^/   /'
    "$CLI" journey 2>&1 | tail -20 | sed 's/^/   /'
fi

say "done"
info "device backup: $REMOTE_BACKUP"
info "PC backup:     $LOCAL_BACKUP"
info "old CLI config remains at $REAL_HOME/.chopin; new CLI uses $REAL_HOME/.sextant"
info "rollback: reinstall the old module zip/APK, accepting that old APK data is already gone"
