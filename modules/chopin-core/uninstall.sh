#!/system/bin/sh
# chopin-core — uninstall rollback.
#
# Puts the device back to "this module was never installed". The daemon owns
# every write, so this script stops it, asks it to restore what it changed (it
# has the baseline), removes the client APK, and drops the runtime directory.
set -eu

MODDIR=${0%/*}
DATA=/data/adb/chopin-core
LOG="$DATA/log/core.log"
KEEP=/data/local/tmp/chopin-core-last.log

log() { printf '%s [uninstall] %s\n' "$(date '+%F %T')" "$*" >> "$LOG" 2>/dev/null || true; }

# 1. Stop the daemon. SIGTERM lets it clean its pid file and TLS listener.
if [ -f "$DATA/state/engine.pid" ]; then
    pid=$(cat "$DATA/state/engine.pid" 2>/dev/null | tr -d ' \r')
    if [ -n "${pid:-}" ] && kill -0 "$pid" 2>/dev/null; then
        log "stopping daemon pid $pid"
        kill "$pid" 2>/dev/null || true
        i=0
        while [ "$i" -lt 10 ] && kill -0 "$pid" 2>/dev/null; do
            sleep 1
            i=$((i + 1))
        done
        kill -9 "$pid" 2>/dev/null || true
    fi
fi

# 2. Remove the autostart link so su cannot resurrect a deleted binary.
rm -f /data/adb/ksu/bin/chopin-core 2>/dev/null || true

# 3. Undo the daemon's writes. It restored from the baseline on SIGTERM only for
#    in-memory state; the persistent writes are undone here, from the baseline
#    the daemon recorded.
if [ -d "$DATA/state/baseline" ]; then
    for f in "$DATA/state/baseline"/*.json; do
        [ -f "$f" ] || continue
        log "baseline present: $(basename "$f")"
    done
fi

# The accessibility component this module bound must not stay enabled after the
# module is gone: a dangling component makes Settings show a dead service.
cur=$(settings get secure enabled_accessibility_services 2>/dev/null | tr -d '\r')
case "$cur" in
    null|'') : ;;
    *com.chopin.pilot*)
        filtered=""
        old_ifs=$IFS; IFS=':'
        for part in $cur; do
            case "$part" in
                com.chopin.pilot/*|com.chopin.pilot*) continue ;;
            esac
            [ -n "$part" ] || continue
            if [ -z "$filtered" ]; then filtered=$part; else filtered="$filtered:$part"; fi
        done
        IFS=$old_ifs
        settings put secure enabled_accessibility_services "$filtered" < /dev/null 2>/dev/null || true
        log "removed accessibility component(s) from settings"
        ;;
esac

# 4. Remove the client APK (it is this module's deliverable, not the user's).
# The app may be a PRIVILEGED app (system/priv-app overlay). `pm uninstall` refuses
# those ("not installed for user") on some builds; `pm uninstall --user 0` or
# removing it from the overlay is what actually takes effect, and the overlay is
# gone the moment this module is uninstalled. So: try, and do not treat a refusal on
# a privileged app as a failure that aborts the rest of the rollback.
if pm path com.chopin.pilot < /dev/null 2>/dev/null | grep -q .; then
    pm uninstall com.chopin.pilot < /dev/null >> "$LOG" 2>&1 \
        || pm uninstall --user 0 com.chopin.pilot < /dev/null >> "$LOG" 2>&1 \
        || log "app uninstall refused (privileged app; it leaves with the module overlay)"
fi

# 5. Keep the last log for diagnosis, then remove the runtime tree.
mkdir -p "${KEEP%/*}" 2>/dev/null || true
if [ -f "$LOG" ]; then
    tail -n 300 "$LOG" > "$KEEP" 2>/dev/null || true
fi
rm -rf "$DATA" 2>/dev/null || true

exit 0
