#!/usr/bin/env bash
# bin/lint-driver-boundary.sh — CI enforcement of G3
# (docs/design/10-core-architecture.md §1.7 / §6.2 D3 / §13):
#
#   "Docker API 타입이 WorkloadDriver 밖으로 유출되면 안 된다.
#    ... github.com/docker/docker import는 driver/docker 패키지에서만
#    허용(CI 린트로 강제)."
#
# Generalized to this repo's actual layout: NO Go source file may import
# "osiris-orchestrator/internal/driver/<anything>" EXCEPT
#   (a) files that already live inside orchestrator/internal/driver/<x>/
#       themselves (a driver package's own files may import its own
#       sibling files — that's not a boundary crossing), and
#   (b) orchestrator/cmd/osiris-orchestrator/main.go, which is the single
#       approved wiring point where a concrete driver is constructed and
#       handed to reconcile.NewLoop as the workload.WorkloadDriver
#       interface (see internal/reconcile/reconcile.go's package doc).
#
# This exists because the violation it catches ALREADY HAPPENED ONCE in
# this codebase: internal/reconcile/reconcile.go imported
# internal/driver/docker directly and typed its Loop.driver field as
# *docker.Driver instead of workload.WorkloadDriver. A supervisor code
# review caught it after the fact; this script is what should have caught
# it automatically, before review.
#
# Exit 0 = clean. Exit 1 = at least one forbidden import found (path:line
# printed for each).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ORCH_MODULE="osiris-orchestrator"
ORCH_DIR="$ROOT/orchestrator"

if [ ! -d "$ORCH_DIR" ]; then
  echo "SKIP: $ORCH_DIR not found"
  exit 0
fi

APPROVED_WIRING_FILE="$ORCH_DIR/cmd/osiris-orchestrator/main.go"

fail=0
count_checked=0

while IFS= read -r -d '' file; do
  # (a) a driver package's own files may import their own tree —
  # e.g. internal/driver/docker/driver.go referencing
  # "osiris-orchestrator/internal/driver/docker" (itself) or any future
  # internal/driver/docker/<subpkg> is fine; only files OUTSIDE
  # internal/driver/ are being policed here.
  case "$file" in
    "$ORCH_DIR"/internal/driver/*) continue ;;
  esac
  # (b) the one approved wiring point.
  if [ "$file" = "$APPROVED_WIRING_FILE" ]; then
    continue
  fi

  count_checked=$((count_checked + 1))
  # Match an import path "osiris-orchestrator/internal/driver/..." —
  # whether on its own line (goimports style) or inside a parenthesized
  # import block. Deliberately matches the quoted import path, not just
  # the bare word "driver", so a comment mentioning "the driver" or a
  # field named "driver" doesn't false-positive.
  hits="$(grep -n "\"${ORCH_MODULE}/internal/driver/" "$file" 2>/dev/null || true)"
  if [ -n "$hits" ]; then
    fail=1
    while IFS= read -r line; do
      rel="${file#"$ROOT"/}"
      echo "FAIL: $rel:${line%%:*} imports a concrete driver package outside the approved boundary (G3)"
      echo "      ${line#*:}"
    done <<< "$hits"
  fi
done < <(find "$ORCH_DIR" -name '*.go' -print0)

echo "--- lint-driver-boundary: checked $count_checked file(s) outside internal/driver/ and outside $APPROVED_WIRING_FILE ---"
if [ "$fail" -eq 0 ]; then
  echo "PASS: no file outside internal/driver/ or cmd/osiris-orchestrator/main.go imports a concrete driver package"
else
  echo "FAIL: driver boundary violated — see above. Fix: depend on workload.WorkloadDriver instead (internal/workload/spec.go)."
fi
exit $fail
