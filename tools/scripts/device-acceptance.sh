#!/usr/bin/env bash
# Device acceptance.
#
# This used to drive the old shell layer with a fake adb and a transcript parser.
# The acceptance suite now lives in the PC CLI, where it talks to the daemon over
# the same HTTP API an agent uses — so this script only forwards to it. Keeping two
# implementations of "is the phone OK" was how they drifted apart.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
CLI=${CLI:-$ROOT/apps/pc-cli/dist/chopin-linux-amd64}

if [ ! -x "$CLI" ]; then
    echo "no PC CLI at $CLI — run apps/pc-cli/build.sh first" >&2
    exit 1
fi

exec "$CLI" acceptance "$@"
