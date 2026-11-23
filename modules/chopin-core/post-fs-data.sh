#!/system/bin/sh
# chopin-core — post-fs-data stage.
#
# Runs early, before the framework exists. Only filesystem work is allowed here:
# settings/dumpsys/appops are all unavailable and calling them would just log
# failures. Everything semantic belongs to the daemon (DESIGN D22).
set -eu

MODDIR=${0%/*}
DATA=/data/adb/chopin-core

mkdir -p "$DATA/state" "$DATA/log" "$DATA/app" "$DATA/config" "$DATA/pki" "$DATA/tmp" \
         "$DATA/state/baseline" 2>/dev/null || true

# The manifest names packages and the config holds the unlock credential. Keep
# the tree root-only; the app gets one 0700 directory it can traverse.
chmod 0700 "$DATA" "$DATA/state" "$DATA/app" "$DATA/pki" "$DATA/tmp" "$DATA/state/baseline" 2>/dev/null || true
chmod 0600 "$DATA/config/core.json" "$DATA/config/unlock-secret" 2>/dev/null || true

# Trim the log once per boot so a chatty daemon cannot fill /data.
LOG="$DATA/log/core.log"
if [ -f "$LOG" ]; then
    size=$(wc -c < "$LOG" 2>/dev/null || echo 0)
    if [ "${size:-0}" -gt 1048576 ] 2>/dev/null; then
        tail -n 500 "$LOG" > "$LOG.tmp" 2>/dev/null && mv -f "$LOG.tmp" "$LOG"
    fi
fi

exit 0
