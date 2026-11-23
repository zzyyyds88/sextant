#!/usr/bin/env bash
# Read and set the daily settings on a device.
#
# Every setter goes through the service that OWNS the setting, not the Settings
# database, and reads the result back. The difference is not cosmetic: measured on
# this device, writing `airplane_mode_on=1` left Wi-Fi running, because a radio is
# switched by ConnectivityManager reacting to a broadcast and not by the row.
#
# Usage:
#   settings.sh list
#   settings.sh set <name> <value>
#
# Requires the CLI at apps/pc-cli/dist/sextant-linux-amd64 and a reachable device.
set -uo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
CLI=${CLI:-$ROOT/apps/pc-cli/dist/sextant-linux-amd64}
export SEXTANT_ADB_HOME=${SEXTANT_ADB_HOME:-$ROOT/adbhome}

case "${1:-list}" in
    list|ls)
        "$CLI" settings
        ;;
    set)
        [ $# -ge 3 ] || { echo "usage: $0 set <name> <value>" >&2; exit 2; }
        shift
        "$CLI" settings "$@"
        ;;
    *)
        echo "usage: $0 [list|set <name> <value>]" >&2
        exit 2
        ;;
esac
