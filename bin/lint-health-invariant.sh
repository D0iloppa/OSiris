#!/usr/bin/env bash
# lint-health-invariant.sh — CI guard for the two §5.4 invariants that are
# "구현 시 반드시 지킬 규칙 (어기면 위 설계가 무너진다)".
#
# docs/design/10-core-architecture.md §5.4:
#   1) "헬스 핸들러는 atomic 변수만 읽는다. 자식 프로세스 실행 경로가 잡는 어떤
#       mutex도 잡지 않는다. ... 금지한다."
#   2) "어떤 probe도 LLM을 호출하지 않는다. 불변식으로 못 박는다:
#       /livez·/readyz 처리 경로에서 claude 프로세스를 생성하는 코드는 존재하지
#       않는다."
#
# Both are enforced STRUCTURALLY in the source (agent/internal/health is a
# package whose every field is a sync/atomic type and which imports neither
# os/exec nor any blocking primitive), and this script is what stops that
# structure from quietly eroding. It is deliberately crude and textual: a
# reviewer can verify the script itself in thirty seconds, which is the point.
#
# Exit 0 = PASS. Exit 1 = FAIL. No arguments.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HEALTH_DIR="$ROOT/agent/internal/health"
FAIL=0

say()  { echo "$*"; }
fail() { echo "FAIL: $*" >&2; FAIL=1; }

[ -d "$HEALTH_DIR" ] || { echo "ERROR: $HEALTH_DIR not found" >&2; exit 1; }

FILES=$(find "$HEALTH_DIR" -name '*.go' -type f)
[ -n "$FILES" ] || { echo "ERROR: no .go files in $HEALTH_DIR" >&2; exit 1; }

say "--- lint-health-invariant: $(echo "$FILES" | wc -l) file(s) in agent/internal/health ---"

# The invariant is about CODE, and this package's whole job is to explain the
# invariant in prose — so the forbidden identifiers necessarily appear in its
# comments. Strip line comments first (this package uses no block comments and no
# "//" inside string literals; both facts are themselves asserted below), keeping
# line numbers intact so failures stay locatable.
CODE_ONLY=$(mktemp)
trap 'rm -f "$CODE_ONLY"' EXIT
for f in $FILES; do
  # Blank out everything from the first // to end of line, keep the line.
  sed -e 's|//.*$||' "$f" | nl -ba -w1 -s: | sed "s|^|${f}:|"
done > "$CODE_ONLY"

if grep -q '/\*' $FILES; then
  fail "agent/internal/health uses a /* */ block comment; this lint only strips line comments, so update it before adding one"
fi

# ---------------------------------------------------------------------------
# 1. No lock, no channel, no waitgroup ANYWHERE in the health package.
#    A channel counts: a send/receive on a channel whose other end is served by
#    the child-exec path blocks exactly like a mutex would.
# ---------------------------------------------------------------------------
for pat in 'sync\.Mutex' 'sync\.RWMutex' 'sync\.WaitGroup' 'sync\.Once' '\.Lock\(\)' '\.RLock\(\)' 'chan '; do
  if hits=$(grep -E "$pat" "$CODE_ONLY" 2>/dev/null); then
    fail "agent/internal/health contains a blocking primitive matching /$pat/ — §5.4 forbids the health handler from touching anything a long exec path can hold:"
    echo "$hits" >&2
  fi
done

# ---------------------------------------------------------------------------
# 2. No subprocess creation, no LLM, no network client, no file I/O in the
#    health package. os/exec is the one that matters most (invariant 2), but a
#    net/http *client* or an os.ReadFile in a handler would reintroduce the same
#    "probe blocks on something slow" failure by another route.
# ---------------------------------------------------------------------------
for pat in '"os/exec"' '"os"' 'exec\.Command' 'http\.Get' 'http\.Client' 'ReadFile' 'WriteFile'; do
  if hits=$(grep -E "$pat" "$CODE_ONLY" 2>/dev/null); then
    fail "agent/internal/health references /$pat/ — a probe must do no I/O and must never create a process:"
    echo "$hits" >&2
  fi
done

# ---------------------------------------------------------------------------
# 3. The health package must not import the supervisor package (which DOES hold
#    mutexes and DOES exec claude). If that import ever appears, invariants 1
#    and 2 become reachable in one hop.
# ---------------------------------------------------------------------------
if hits=$(grep -E 'osiris-agent/internal/supervisor' "$CODE_ONLY" 2>/dev/null); then
  fail "agent/internal/health imports internal/supervisor — that package execs claude and holds mutexes:"
  echo "$hits" >&2
fi

# ---------------------------------------------------------------------------
# 4. Positive check: every field of health.State must be a sync/atomic type
#    (or the one immutable string). This is what makes "atomic 변수만 읽는다"
#    true by construction rather than by discipline.
# ---------------------------------------------------------------------------
STATE_BLOCK=$(awk '/^type State struct \{/,/^\}/' "$HEALTH_DIR/state.go")
if [ -z "$STATE_BLOCK" ]; then
  fail "could not find 'type State struct' in $HEALTH_DIR/state.go"
else
  BAD=$(echo "$STATE_BLOCK" \
    | grep -vE '^\s*(//|$|type State struct \{|\})' \
    | grep -vE 'atomic\.(Int32|Int64|Uint32|Uint64|Bool|Value|Pointer)' \
    | grep -vE '^\s*employeeID\s+string')
  if [ -n "$BAD" ]; then
    fail "health.State has non-atomic field(s) — the health handler would then read unsynchronized or locked state:"
    echo "$BAD" >&2
  fi
fi

# ---------------------------------------------------------------------------
# 5. The health HTTP handlers are only registered in this package. If another
#    package registers /livez or /readyz on the agent, this lint would not be
#    checking the real handler any more.
# ---------------------------------------------------------------------------
OTHER=$(grep -rn '"/livez"\|"/readyz"' "$ROOT/agent" --include='*.go' 2>/dev/null | grep -v "internal/health/" || true)
if [ -n "$OTHER" ]; then
  fail "/livez or /readyz is registered outside agent/internal/health:"
  echo "$OTHER" >&2
fi

# ---------------------------------------------------------------------------
# 6. No `claude` process creation anywhere reachable from a handler: the only
#    exec.Command calls in the agent module must live in files that a handler
#    cannot call into. Practically: assert that every exec.Command in the agent
#    module is in internal/supervisor (task.go/probe.go) or cmd/, never in
#    internal/health.
# ---------------------------------------------------------------------------
EXECS=$(grep -rn 'exec\.Command' "$ROOT/agent" --include='*.go' 2>/dev/null | cut -d: -f1 | sort -u || true)
for f in $EXECS; do
  case "$f" in
    */internal/health/*) fail "exec.Command found in the health package: $f" ;;
  esac
done
say "    exec.Command appears only in: $(echo "$EXECS" | sed "s|$ROOT/||" | tr '\n' ' ')"

if [ "$FAIL" -eq 0 ]; then
  say "PASS: health handlers read only atomics; no mutex, no channel, no I/O, no subprocess in the probe path (§5.4)"
  exit 0
fi
exit 1
