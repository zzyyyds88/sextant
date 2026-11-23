#!/system/bin/sh
# sextant-core — installer (sourced by the KernelSU/Magisk installer).
#
# Non-interactive and idempotent. It grants file modes, seeds the runtime config
# on first install, and records nothing else: there is no allowlist baseline to
# capture any more (DESIGN D21 deleted that layer).
#
# Deliberately NO `set -eu`: this file is SOURCED by the KernelSU installer, so
# `-e` would make any benign non-zero (a missing optional file, the installer's
# own `$1` checks) abort the whole installation, and `-u` breaks the installer's
# unset positional parameters.

SKIPUNZIP=0

set_perm_recursive "$MODPATH" 0 0 0755 0644
set_perm "$MODPATH/customize.sh"     0 0 0755
set_perm "$MODPATH/post-fs-data.sh"  0 0 0755
set_perm "$MODPATH/service.sh"       0 0 0755
set_perm "$MODPATH/uninstall.sh"     0 0 0755
set_perm "$MODPATH/bin/sextant-core" 0 0 0755
set_perm "$MODPATH/lib/helper.jar"       0 0 0644
set_perm "$MODPATH/lib/scrcpy-server.jar" 0 0 0644
set_perm "$MODPATH/lib/app.apk"          0 0 0644
if [ -f "$MODPATH/config/unlock-secret" ]; then
    set_perm "$MODPATH/config/unlock-secret" 0 0 0600
fi
set_perm "$MODPATH/config/core.json" 0 0 0600

# The privileged app overlay: KernelSU bind-mounts this tree over /system at boot,
# so the app ends up in /system/priv-app and gets the permissions listed in
# system/etc/permissions/privapp-permissions-sextant-pilot.xml at install time.
#
# 0755/0644 and root:root are required — the package manager scans /system/priv-app
# as the system user and skips anything it cannot read, silently: the app then just
# does not appear, with no error anywhere a person would look.
if [ -d "$MODPATH/system" ]; then
    set_perm_recursive "$MODPATH/system" 0 0 0755 0644
    set_perm "$MODPATH/system/etc/permissions/privapp-permissions-sextant-pilot.xml" 0 0 0644
fi

DATA=/data/adb/sextant-core
mkdir -p "$DATA/config" "$DATA/state" "$DATA/log" "$DATA/app" "$DATA/pki" "$DATA/tmp" 2>/dev/null || true
chmod 0700 "$DATA" 2>/dev/null || true

# Runtime config is authoritative once it exists: an upgrade must not clobber a
# user's edits. The shipped copy is only the seed.
if [ ! -f "$DATA/config/core.json" ]; then
    cp -f "$MODPATH/config/core.json" "$DATA/config/core.json"
    chmod 0600 "$DATA/config/core.json"
fi
if [ ! -f "$DATA/config/unlock-secret" ] && [ -f "$MODPATH/config/unlock-secret" ]; then
    cp -f "$MODPATH/config/unlock-secret" "$DATA/config/unlock-secret"
    chmod 0600 "$DATA/config/unlock-secret"
fi

ui_print "- Sextant Core 4.0.0 installed"
ui_print "- runtime dir: $DATA"
ui_print "- after reboot the daemon starts as root; no allowlist step is needed"
ui_print "- pair a PC with: adb shell su -c 'sextant-core ctl pair new --agent pc'"
ui_print "- uninstall removes the client APK (com.sextant.pilot) and the runtime dir"
