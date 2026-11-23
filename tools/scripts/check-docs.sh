#!/usr/bin/env bash
# Documentation drift checks.
#
# Only the current sources of truth are checked: DESIGN, TASKS, ACCEPTANCE, the
# READMEs and AGENTS. The workspace is remote-control only: old modules, the old
# design archive, and ROM/research trees must not come back into the repo.
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
PASS=0; FAIL=0
ok()  { PASS=$((PASS+1)); printf '  ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL+1)); printf '  FAIL %s\n' "$1"; [ -n "${2:-}" ] && printf '       %s\n' "$2"; }
file_has() { if grep -qF -- "$2" "$1" 2>/dev/null; then ok "$3"; else bad "$3" "missing in ${1#$ROOT/}: $2"; fi; }
file_lacks() { if grep -qF -- "$2" "$1" 2>/dev/null; then bad "$3" "must not appear in ${1#$ROOT/}: $2"; else ok "$3"; fi; }
dir_absent() { if [ -e "$ROOT/$1" ]; then bad "$2" "must not exist: $1"; else ok "$2"; fi; }

echo "== the current sources of truth exist =="
for f in docs/DESIGN.md docs/TASKS.md docs/ACCEPTANCE.md README.md AGENTS.md \
         modules/sextant-core/README.md apps/pilot/README.md apps/pc-cli/README.md; do
    if [ -f "$ROOT/$f" ]; then ok "$f"; else bad "$f is missing"; fi
done

echo "== the workspace is remote-control only =="
dir_absent "modules/sextant-compat" "the old compat module is gone"
dir_absent "modules/miui14-agent-tune" "the old tune module is gone"
dir_absent "modules/sextant-harness" "the old harness module directory is gone"
dir_absent "apps/remote" "the old remote app directory is gone"
dir_absent "docs/archive" "the old design archive is gone"
dir_absent "decompiled" "decompiled trees are external"
dir_absent "research" "research material is external"
dir_absent "audit" "audit captures are external"
dir_absent "downloads" "ROM downloads are external"
dir_absent "extract" "extracted images are external"
dir_absent "old" "old builds are external"
file_has "$ROOT/README.md" '/root/chopin-research/' "README points at the external research root"
file_has "$ROOT/WORKSPACE.md" '/root/chopin-research/' "WORKSPACE points at the external research root"
file_has "$ROOT/AGENTS.md" '/root/chopin-research/' "AGENTS points at the external research root"

echo "== the acceptance process states its own rules =="
file_has "$ROOT/docs/ACCEPTANCE.md" '不许把跳过算通过' 'ACCEPTANCE forbids counting SKIP as a pass'
file_has "$ROOT/docs/ACCEPTANCE.md" '用另一条通道复核' 'ACCEPTANCE requires an independent channel'
file_has "$ROOT/docs/ACCEPTANCE.md" '读审计日志' 'ACCEPTANCE requires log confirmation'
file_has "$ROOT/docs/ACCEPTANCE.md" 'sextant journey' 'ACCEPTANCE documents the user-like matrix'
file_has "$ROOT/docs/ACCEPTANCE.md" '已知未覆盖' 'ACCEPTANCE admits what is not covered'

echo "== DESIGN carries the current decision set =="
for d in D20 D21 D22 D23 D24 D25 D26 D27; do
    file_has "$ROOT/docs/DESIGN.md" "$d" "DESIGN has $d"
done
file_has "$ROOT/docs/DESIGN.md" '设备端单制品' 'DESIGN states the single-artifact decision'
file_has "$ROOT/docs/DESIGN.md" '只负责远控' 'DESIGN states the remote-control-only scope'
file_has "$ROOT/docs/DESIGN.md" '/root/chopin-research/' 'DESIGN points at the external research root'

echo "== AGENTS agrees with the current architecture =="
file_lacks "$ROOT/AGENTS.md" 'sextant-harnessctl' 'AGENTS has no old dead CLI name'
file_lacks "$ROOT/AGENTS.md" 'sextant-corectl' 'AGENTS has no new dead CLI name'
file_lacks "$ROOT/AGENTS.md" 'docs/HARNESS-SPEC.md' 'AGENTS has no dead spec path'
file_has "$ROOT/AGENTS.md" '只负责远控' 'AGENTS states the remote-control-only scope'
file_has "$ROOT/AGENTS.md" '≤ 60' 'AGENTS states the static-gate budget'

echo "== documented ctl subcommands exist =="
CTL_DOC="$ROOT/modules/sextant-core/README.md"
CTL_SRC="$ROOT/modules/sextant-core/src/internal/ctl/ctl.go"
for sub in version status "pair new" "token list" "token revoke" "token rotate" \
           "secret set-unlock" "secret clear-unlock" "secret set-sim" "secret clear-sim" \
           "secret status" "animation off" "animation restore" "animation status" log; do
    file_has "$CTL_DOC" "sextant-core ctl $sub" "ctl $sub is documented"
done
file_has "$CTL_SRC" "secret set-unlock" "ctl usage lists the same subcommands"
file_has "$CTL_SRC" "secret set-sim" "ctl usage lists set-sim"

echo "== README index points at real files =="
python3 - "$ROOT" <<'PY'
import os, re, sys
root = sys.argv[1]
text = open(os.path.join(root, "README.md"), encoding="utf-8").read()
missing = []
for m in re.finditer(r"\[[^\]]*\]\(([^)]+)\)", text):
    target = m.group(1).split("#")[0].strip()
    if not target or target.startswith(("http://", "https://", "mailto:")):
        continue
    if not os.path.exists(os.path.join(root, target)):
        missing.append(target)
if missing:
    print("  FAIL README links to missing paths: " + ", ".join(sorted(set(missing))))
    sys.exit(1)
print("  ok   every README link resolves")
PY
if [ $? -eq 0 ]; then PASS=$((PASS+1)); else FAIL=$((FAIL+1)); fi

echo
if [ "$FAIL" -eq 0 ]; then
    echo "check-docs: OK ($PASS passed)"
    exit 0
fi
echo "check-docs: FAILED ($PASS passed, $FAIL failed)"
exit 1
