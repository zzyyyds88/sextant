#!/system/bin/sh
# chopin-core — late-start stage.
#
# This script is the ONLY thing that starts the daemon, and KernelSU runs module
# scripts as root (uid 0, u:r:su:s0). That is why the daemon gets its privileges
# here instead of from a KernelSU allowlist grant to the app: there is no APK
# root, no "reboot to take effect", and no second privilege path to reason about.
#
# It does not wait for the unlock: health/status/device/input must work on a
# locked device, and the daemon unlocks the screen itself once boot settles.
set -eu

MODDIR=${0%/*}
DATA=/data/adb/chopin-core
LOG="$DATA/log/core.log"

mkdir -p "$DATA/state" "$DATA/log" "$DATA/app" "$DATA/config" "$DATA/pki" "$DATA/tmp" \
         "$DATA/state/baseline" 2>/dev/null || true
chmod 0700 "$DATA" "$DATA/state" "$DATA/app" "$DATA/pki" "$DATA/tmp" 2>/dev/null || true
chmod 0600 "$LOG" 2>/dev/null || true

log() { printf '%s [service] %s\n' "$(date '+%F %T')" "$*" >> "$LOG" 2>/dev/null || true; }

# Wait for the framework, not for the unlock. Without boot_completed the package
# manager and settings provider are not usable yet.
i=0
while [ "$(getprop sys.boot_completed 2>/dev/null | tr -d '\r')" != "1" ]; do
    i=$((i + 1))
    [ "$i" -gt 900 ] && { log "boot_completed timeout; not starting the daemon"; exit 0; }
    sleep 1
done

# Seed the runtime config on first boot / first upgrade. Never overwrite a
# user-modified file: the runtime copy is authoritative once it exists.
if [ ! -f "$DATA/config/core.json" ] && [ -f "$MODDIR/config/core.json" ]; then
    cp -f "$MODDIR/config/core.json" "$DATA/config/core.json"
    chmod 0600 "$DATA/config/core.json"
    log "seeded runtime config"
fi

# Install or upgrade the client APK this module ships.
#
# The comparison is on the file hash, not on a version string: a rebuilt APK with
# the same versionCode must still replace the installed one, and that is exactly
# the case during development. A signature mismatch cannot be fixed silently, so
# the failure is logged and the install is retried on the next boot.
#
# The priv-app overlay alone is NOT enough on this ROM. It gives the package its
# privileged permissions, but app processes cannot read the APK through
# /system/priv-app (measured: ResourcesManager/ziparchive ENOENT, then
# ContextImpl Resources.getImpl() NPE, then the a11y service never binds). The old
# install worked only because an earlier pm install had left a /data/app update
# copy behind. Keep that copy deliberately: pm install -r over the privileged
# package yields PRIVILEGED + UPDATED_SYSTEM_APP, and pm path then points at the
# readable data copy.
PM_PATH=$(pm path com.chopin.pilot 2>/dev/null | sed -n 's/^package://p' | head -1)
case "$PM_PATH" in
    /system/priv-app/*|/system/app/*|/product/priv-app/*)
        log "app is privileged ($PM_PATH); a data-app update copy is required"
        PRIVILEGED_APP=1
        ;;
    *) PRIVILEGED_APP=0 ;;
esac

if [ -f "$MODDIR/lib/app.apk" ]; then
    need_install=0
    if [ "$PRIVILEGED_APP" = 1 ]; then
        need_install=1
    fi

    shipped_hash=$(md5sum "$MODDIR/lib/app.apk" 2>/dev/null | cut -d' ' -f1)
    installed_path=$(pm path com.chopin.pilot 2>/dev/null | sed -n 's/^package://p' | head -1)
    installed_hash=""
    if [ -n "$installed_path" ] && [ -f "$installed_path" ]; then
        installed_hash=$(md5sum "$installed_path" 2>/dev/null | cut -d' ' -f1)
    fi
    if [ "$shipped_hash" != "$installed_hash" ]; then
        need_install=1
    fi

    if [ "$need_install" = 1 ]; then
        # The package manager is not ready the instant boot_completed flips: on this
        # ROM `pm install` right after boot fails with
        # "Failure calling service package: Failed transaction (2147483646)" while the
        # same command succeeds a few seconds later. Retrying is the fix; treating
        # that as a permanent failure left the APK a whole version behind for hours
        # (its status page then reads an old status path and looks broken).
        installed_now=1
        try=0
        while [ "$try" -lt 6 ]; do
            try=$((try + 1))
            log "installing client APK ($shipped_hash vs ${installed_hash:-none}) attempt $try"
            # Capture first, write afterwards.
            #
            # `pm install ... >> "$LOG"` fails on this ROM with
            # "Failure calling service package: Failed transaction (2147483646)"
            # even though the identical command with stdout going to a pipe, to
            # /dev/null or to /data/local/tmp succeeds (measured: /data/... => exit 2,
            # /data/local/tmp/... => exit 0, five repeats, no exceptions). The exact
            # cause was not identified; capturing into a variable and appending
            # afterwards is the form that works, and the failure mode it avoids is
            # expensive (the APK silently stays one build behind).
            pm_out=$(pm install -r "$MODDIR/lib/app.apk" < /dev/null 2>&1)
            pm_rc=$?
            printf '%s\n' "$pm_out" >> "$LOG"
            if [ "$pm_rc" -eq 0 ]; then
                installed_now=0
                break
            fi
            # A signature mismatch cannot be repaired by -r. Only a data-app install
            # may be removed; do not uninstall the privileged system package: on a
            # fresh 3.0 install that would take the overlay app away and leave the
            # device with no client until the next boot.
            if [ "$try" -eq 3 ] && [ "$PRIVILEGED_APP" -eq 0 ]; then
                log "app install keeps failing; removing the old package and retrying"
                pm uninstall com.chopin.pilot < /dev/null >> "$LOG" 2>&1 || true
            fi
            sleep 5
        done
        if [ "$installed_now" -ne 0 ]; then
            log "app install still failed after $try attempts; the next boot will retry"
        fi
    fi
else
    log "lib/app.apk missing; cannot install the client"
fi

# Keep the CLI reachable from 'su -c': KernelSU puts /data/adb/ksu/bin on the su
# PATH, and the APK needs exactly one entry point (ctl secret set-unlock).
if [ -d /data/adb/ksu/bin ]; then
    ln -sf "$MODDIR/bin/chopin-core" /data/adb/ksu/bin/chopin-core 2>/dev/null || true
fi

# Single instance.
#
# The pid file alone is not enough: after a reboot the recorded pid can belong to
# an unrelated process that reused it, and `kill -0` then says "still running"
# while no daemon exists — the device boots with nothing listening (observed:
# boot log said "already running as pid 1578" with no daemon and a refused port).
# So the pid is only trusted when its command line is actually ours; otherwise the
# daemon is started and the flock inside it arbitrates.
if [ -f "$DATA/state/engine.pid" ]; then
    pid=$(cat "$DATA/state/engine.pid" 2>/dev/null | tr -d ' \r')
    if [ -n "${pid:-}" ] && kill -0 "$pid" 2>/dev/null; then
        cmdline=$(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null || true)
        case "$cmdline" in
            *chopin-core*daemon*)
                log "daemon already running as pid $pid"
                exit 0 ;;
            *)
                log "stale pid $pid belongs to another process; starting the daemon" ;;
        esac
    fi
fi

log "starting daemon"
exec "$MODDIR/bin/chopin-core" daemon --config "$DATA/config/core.json" >> "$LOG" 2>&1
