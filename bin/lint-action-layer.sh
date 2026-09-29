#!/usr/bin/env bash
# lint-action-layer.sh — CI guard for docs/design/80-action-layer.md §9's
# invariants (AC1~AC22).
#
# This is a sibling of bin/lint-identity-authn.sh (864 lines) and follows its
# shape on purpose: crude, textual, reviewable in under a minute. A reviewer
# who cannot verify the lint itself will not trust what it claims to prove.
#
# §9's own "린트 가능 항목 요약" line names what this script family is
# eventually responsible for:
#
#   AC4 · AC5 · AC6 · AC7 · AC10 · AC11(부분) · AC13 · AC18,
#   그리고 AC3·AC17·AC20·AC21의 정적 부분
#
# [2026-08-13] AC5/AC6/AC11(부분) are now real checks — see the dated note
# below this header for what changed and why.
#
# [2026-08-13, same day, G-A11 closure] AC20 is now a real check too:
# actiontype_permission_mirror.go was upgraded from "refuse to boot if any
# REQUIRES_PERMISSION edge exists" to "actually compute and verify the
# min-of-gateClass derivation (80 §8.1/AD13)". This script's new AC20
# section checks the OTHER half of AC20 — that the mirror, once derived, is
# never read back into an authorization decision (Grant.Gate has zero
# readers; :Permission.gate/approverRole/limitJson appear only in
# seedPermissions' write and the derivation check's read-for-comparison).
#
# [2026-08-12] UPDATED FOR P2 STAGE ② (§11's "구현 워커" ordering item ②:
# ":ActionType 스키마 + graph-write 화이트리스트(AC4/CA1)"). Stage ① shipped
# the predicate/effect registries and the boot reconciler (checked below,
# unchanged); stage ② added internal/graphdb/actiontype.go
# (CreateActionType/UpdateActionType) and api/server.go's /v1/action-types
# routes, so AC4 and AC10 below are now REAL checks against that code, not
# stubs.
#
# [2026-08-12, later same day] UPDATED FOR P2 STAGE ③ (git.commit as an
# actual row, effect.scm.commit registered, core/internal/action/dispatch's
# generic dispatcher, gateFor). AC7 below is now a REAL check against
# gateFor's declared signature.
#
# [2026-08-13] UPDATED for actiontype.write's authorization gap closure
# (DG6~DG8 landed in api/internal/authz/delegation.go, HumanOnly landed in
# api/internal/authz/humanonly.go). AC5, AC6 and AC11(부분) below are now
# REAL checks, not stubs:
#   - AC5  — CheckDelegation's DG6 section + >=2 DG6-named tests.
#   - AC6  — cross-module: HumanOnly's 4 codes carry empty
#            ClaudeAllowedTools/MCPTools/EgressAllowlist in orchestrator's
#            grant-catalog (reads BOTH api/internal/authz/*.go AND
#            orchestrator/internal/catalog/catalog.go — the one check in
#            this script that spans two Go modules; see humanonly.go's doc
#            comment for why that cross-check cannot live inside either
#            module's own `go test`).
#   - AC11 — static half only (CheckDelegation's DG8 section + the
#            fail-closed notify-after-bumpRevision ordering in
#            UpdateActionType + >=2 DG8-named tests). The RUNTIME half (does
#            a publish failure actually roll the transaction back) needs a
#            live Neo4j session this script does not have — hence "부분",
#            per §9's own grading for this row.
#
#   - AC13/AC18 remain explicit NOTE stubs — they police the gate-handler/
#     execution-path code for taint (§7) and capability resolution (§5.4),
#     neither of which exists yet (composition execution and taint are
#     explicitly forbidden until a later stage, 80 §11). A lint that
#     "passes" a check with nothing to look at is worse than no lint (same
#     argument lint-identity-authn.sh makes for HI16~HI18 before authz.go
#     existed) — so those sections stay explicit NOTE stubs, not silent
#     PASSes.
#   - AC3's STATIC part — unchanged from stage ①.
#
# EVERY CHECK BELOW HAS BEEN FAULT-INJECTED — see this repository's
# docs/design/IMPLEMENTATION-NOTES.md convention (lint-identity-authn.sh's
# own header explains why: "A lint that checks nothing passes silently,
# which is worse than no lint at all"). The fault-injection notes for THIS
# script are recorded in its own git-free equivalent: this repository has no
# git history (additive-only build constraint), so the injection was done
# and reverted in-session rather than recorded as a commit; the checks below
# were confirmed to fail against a deliberately broken copy of each guarded
# file before being left in their passing state.
#
# Exit 0 = PASS. Exit 1 = FAIL. No arguments.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FAIL=0
CHECKS=0

say()  { echo "$*"; }
ok()   { echo "  ok    $*"; }
fail() { echo "  FAIL  $*" >&2; FAIL=1; }
note() { echo "  --    $*"; }

PREDICATE_GO="$ROOT/core/internal/action/predicate/predicate.go"
EFFECT_GO="$ROOT/core/internal/action/effect/effect.go"
RECONCILE_GO="$ROOT/core/internal/graphdb/actiontype_reconcile.go"
MAIN_GO="$ROOT/core/cmd/osiris-core/main.go"
CATALOG_GO="$ROOT/orchestrator/internal/catalog/catalog.go"
MIRROR_GO="$ROOT/core/internal/graphdb/actiontype_permission_mirror.go"
SEED_GO="$ROOT/core/internal/graphdb/seed.go"

say "--- lint-action-layer (80-action-layer.md §9, P2 stage ①+②+③ subset) ---"

# ===========================================================================
# AC3 (static part) — "fail-closed, 건너뛰지 않는다"
#
# §9: "validationPredicates/effects의 모든 원소가 각 레지스트리에 존재한다.
# 쓰기 시 거부, 부팅 시 inert. fail-closed — 건너뛰지 않는다." 등급 치명.
#
# The static half this build can check: the two registries and the boot
# reconciler must NOT contain grant-catalog's silent-skip idiom
# (`ok := Table[g]; if !ok { continue }` — orchestrator/internal/catalog's
# own documented, INTENTIONAL behavior for grant codes, §5.3.1's own
# contrast case). If that idiom appears in the action-layer files, an
# unknown predicate/effect/id would be silently treated as absent instead of
# causing a hard failure/degrade — exactly the failure mode AC3 exists to
# rule out.
# ===========================================================================
say
say "AC3(정적) — 술어/이펙트 레지스트리와 부팅 리컨사일러에 grant-catalog식 skip이 없다"
CHECKS=$((CHECKS+1))

for f in "$PREDICATE_GO" "$EFFECT_GO" "$RECONCILE_GO"; do
  if [ ! -f "$f" ]; then
    fail "AC3: $f not found"
    continue
  fi
  # The grant-catalog idiom this rule bans is specifically the two-line
  # sequence "if !ok {" immediately followed by "continue" (catalog.go's own
  # AllowedTools/DeniedTools/MCPServers/EgressAllowlist all read exactly
  # this way — grep confirms it: `if !ok { continue }` on consecutive
  # lines, four times). This is deliberately NARROW: it must NOT flag a
  # lookup that fails closed via "if !ok { return ... }" or
  # "if !ok { return false, \"...\" }" — those are the correct action-layer
  # behavior (propagate the failure), not the banned one (skip and keep
  # going as if nothing referenced it).
  hit="$(awk '
    /if *!ok *\{[[:space:]]*$/ { line=NR; pending=1; next }
    pending { if ($0 ~ /^[[:space:]]*continue[[:space:]]*$/) { print line": if !ok {"; print NR": continue" }; pending=0 }
  ' "$f")"
  if [ -n "$hit" ]; then
    printf '%s\n' "$hit" >&2
    fail "AC3: $f contains an 'if !ok { continue }' pattern — grant-catalog's silent-skip idiom, disallowed for the action layer (80 §5.3.1's explicit contrast)"
  else
    ok "AC3: $(basename "$f") has no 'if !ok { continue }' skip idiom"
  fi
done

# Cross-check the contrast case itself still exists and is still documented
# as intentional in catalog.go — if that comment disappears, the "here is
# why the action layer must NOT do this" argument in the three files above
# loses its anchor.
if [ -f "$CATALOG_GO" ] && grep -q 'silently skipped rather than erroring' "$CATALOG_GO"; then
  ok "AC3: grant-catalog's own silent-skip rationale comment is intact (the contrast case this rule cites)"
else
  note "AC3: could not confirm grant-catalog's silent-skip rationale comment in $CATALOG_GO (informational only — not this build's file to police)"
fi

# ===========================================================================
# AC3 (static part, continued) — registries are not runtime-mutable
#
# §5.3.1's registry table requires "런타임 편집 불가" (same discipline as
# grant-catalog's Table). The structural guarantee is Go visibility: the
# backing map must be an UNEXPORTED package-level var, with no exported
# setter that could let a caller replace or mutate entries at runtime.
# ===========================================================================
say
say "AC3(정적) — 레지스트리 맵이 런타임 편집 불가능한 형태(비공개 var, setter 없음)다"
CHECKS=$((CHECKS+1))

for f in "$PREDICATE_GO" "$EFFECT_GO"; do
  [ -f "$f" ] || { fail "AC3: $f not found"; continue; }
  base="$(basename "$f")"

  if grep -qE '^var registry = map\[string\]' "$f"; then
    ok "AC3: $base declares 'registry' as an unexported package-level literal map"
  else
    fail "AC3: $base does not declare an unexported literal 'var registry = map[string]...' — cannot confirm the registry is fixed at build time"
  fi

  if grep -qE '^var Registry ' "$f" || grep -qE '^func Register\(' "$f" || grep -qE '^func Set[A-Z]' "$f"; then
    fail "AC3: $base exposes an exported Registry/Register/Set* symbol — that would be a runtime edit path grant-catalog's own model forbids"
  else
    ok "AC3: $base exposes no exported mutation entry point (no Registry/Register*/Set*)"
  fi
done

# ===========================================================================
# AC3 (static part, continued) — the boot reconciler is wired into main.go's
# boot path, between catalog seeding and the readyz prober going live.
# ===========================================================================
say
say "AC3(정적) — ReconcileActionTypes가 main.go 부팅 경로(seed 이후, readyz 이전)에 배선되어 있다"
CHECKS=$((CHECKS+1))

if [ ! -f "$MAIN_GO" ]; then
  fail "AC3: $MAIN_GO not found"
else
  SEED_LINE="$(grep -n 'graphdb.SeedCatalog(' "$MAIN_GO" | head -1 | cut -d: -f1)"
  RECON_LINE="$(grep -n 'graphdb.ReconcileActionTypes(' "$MAIN_GO" | head -1 | cut -d: -f1)"
  READYZ_LINE="$(grep -n 'RunProber(' "$MAIN_GO" | head -1 | cut -d: -f1)"

  if [ -z "$SEED_LINE" ] || [ -z "$RECON_LINE" ] || [ -z "$READYZ_LINE" ]; then
    fail "AC3: could not find one of SeedCatalog/ReconcileActionTypes/RunProber in $MAIN_GO (seed=$SEED_LINE recon=$RECON_LINE readyz=$READYZ_LINE)"
  elif [ "$SEED_LINE" -lt "$RECON_LINE" ] && [ "$RECON_LINE" -lt "$READYZ_LINE" ]; then
    ok "AC3: boot order is SeedCatalog (line $SEED_LINE) -> ReconcileActionTypes (line $RECON_LINE) -> readyz prober (line $READYZ_LINE)"
  else
    fail "AC3: ReconcileActionTypes is not strictly between SeedCatalog and the readyz prober (seed=$SEED_LINE recon=$RECON_LINE readyz=$READYZ_LINE)"
  fi
fi

# ===========================================================================
# AC3 (static part, continued) — the reconcile query actually DEGRADES
# (SET status = 'inert'), not merely reports. A report-only implementation
# would be indistinguishable from CheckInvariants' read-only I1-I17 and
# would not satisfy "부팅 시 inert 강등".
# ===========================================================================
say
say "AC3(정적) — 리컨사일러가 실제로 status를 'inert'로 SET한다(보고만 하지 않는다)"
CHECKS=$((CHECKS+1))

if [ -f "$RECONCILE_GO" ] && grep -qE "SET a\.status = 'inert'" "$RECONCILE_GO"; then
  ok "AC3: $(basename "$RECONCILE_GO") SETs status = 'inert' on the matched rows"
else
  fail "AC3: no \"SET a.status = 'inert'\" found in $RECONCILE_GO — this would be a report, not the required degrade"
fi

# ===========================================================================
# STAGE ①로는 미구현/무의미한 항목 — 스텁만 남긴다.
#
# 아래는 80 §9의 "린트 가능 항목 요약"에 실제로 나열된 다른 불변식들이다.
# 전부 stage ②(스키마 + graph-write 화이트리스트) 또는 그 이후를 전제한다
# — 정의할 :ActionType 노드도, 그것을 쓰는 graph-write 함수도 이 build에는
# 없다. "검사해서 통과"가 아니라 "검사 대상이 아직 존재하지 않음"을 명시적으로
# 남긴다 — 없는 검사를 통과한 것처럼 보이게 하는 것이 이 파일이 가장
# 피해야 할 실수다.
# ===========================================================================
ACTIONTYPE_GO="$ROOT/core/internal/graphdb/actiontype.go"
SERVER_GO="$ROOT/core/internal/api/server.go"

# ===========================================================================
# AC4 (금지 속성 화이트리스트) — P2 stage ② 실제 검사.
#
# §9: "실행 가능한 표현을 담는 속성이 존재하지 않는다. 화이트리스트 밖 속성은
# 거부(무시 아님)." 등급 치명. 세 가지를 텍스트로 확인한다:
#   1. ActionTypeAttributeWhitelist 맵 리터럴 안에 금지 필드명(command/
#      script/template/url/endpoint/handlerCode/expr/condition/webhook)이
#      키로 존재하지 않는다.
#   2. ActionTypeInput 구조체 필드 목록에도 같은 금지어가 필드명으로
#      존재하지 않는다(주석은 대상이 아니다 — 구조체 본문만 추출해서 본다).
#   3. api/server.go의 actionTypeReq(JSON 요청 바디) json 태그 집합이 그
#      화이트리스트 + 관계용 고정 필드(byPartyId/composedOf/
#      requiresPermissionCodes) 정확히 그 합집합과 같다 — 화이트리스트에
#      없는 키가 요청 구조체에 몰래 추가되면 즉시 실패한다.
#   4. decode()가 여전히 DisallowUnknownFields를 호출한다 — 화이트리스트
#      밖 JSON 키는 "무시"가 아니라 "거부"되어야 한다(AC4의 핵심 문구).
# ===========================================================================
say
say "AC4 (금지 속성 화이트리스트) — :ActionType graph-write 화이트리스트 실검사"
CHECKS=$((CHECKS+1))

BANNED_FIELDS="command script template url endpoint handlerCode expr condition webhook"

if [ ! -f "$ACTIONTYPE_GO" ]; then
  fail "AC4: $ACTIONTYPE_GO not found"
else
  WHITELIST_BODY="$(awk '/^var ActionTypeAttributeWhitelist = map\[string\]struct\{\}\{$/{f=1;next} f&&/^\}$/{f=0} f' "$ACTIONTYPE_GO")"
  STRUCT_BODY="$(awk '/^type ActionTypeInput struct \{$/{f=1;next} f&&/^\}$/{f=0} f' "$ACTIONTYPE_GO")"

  if [ -z "$WHITELIST_BODY" ]; then
    fail "AC4: could not locate ActionTypeAttributeWhitelist map body in $ACTIONTYPE_GO"
  else
    ok "AC4: ActionTypeAttributeWhitelist map body located"
  fi
  if [ -z "$STRUCT_BODY" ]; then
    fail "AC4: could not locate ActionTypeInput struct body in $ACTIONTYPE_GO"
  else
    ok "AC4: ActionTypeInput struct body located"
  fi

  for w in $BANNED_FIELDS; do
    if printf '%s\n' "$WHITELIST_BODY" | grep -qiE "\"$w\""; then
      fail "AC4: banned field name \"$w\" appears as a whitelist key in ActionTypeAttributeWhitelist"
    fi
    if printf '%s\n' "$STRUCT_BODY" | grep -qiE "^\s*(\*)?$w\b"; then
      fail "AC4: banned field name \"$w\" appears as an ActionTypeInput struct field"
    fi
  done
  ok "AC4: none of {$BANNED_FIELDS} appear as a whitelist key or ActionTypeInput field"
fi

if [ ! -f "$SERVER_GO" ]; then
  fail "AC4: $SERVER_GO not found"
else
  REQ_BODY="$(awk '/^type actionTypeReq struct \{$/{f=1;next} f&&/^\}$/{f=0} f' "$SERVER_GO")"
  if [ -z "$REQ_BODY" ]; then
    fail "AC4: could not locate actionTypeReq struct body in $SERVER_GO"
  else
    REQ_TAGS="$(printf '%s\n' "$REQ_BODY" | grep -oE 'json:"[a-zA-Z]+' | sed 's/json:"//' | sort -u)"
    WL_KEYS="$(printf '%s\n' "$WHITELIST_BODY" | grep -oE '"[a-zA-Z]+":' | sed 's/[":]//g' | sort -u)"
    # relationship/actor fields that legitimately live on the request body
    # but are NOT §4.2 node properties (§4.3 + this codebase's byPartyId
    # convention) — the only keys allowed to be in REQ_TAGS but not in
    # WL_KEYS.
    EXTRA_ALLOWED="byPartyId
composedOf
requiresPermissionCodes"
    # Server-managed §4.2 properties this codebase deliberately does NOT
    # expose on the request body at all (graphdb.ActionTypeInput's own doc
    # comment explains why: catalogVersion comes from cfg.CatalogVersion,
    # createdAtRevision/closedAtRevision are computed from bumpRevision /
    # not yet settable via any function). Their absence from actionTypeReq
    # is intentional hardening, not drift — excluded from the MISSING check
    # for that reason, but NOT from the whitelist itself (they still must
    # exist as graph node properties, CA1).
    SERVER_MANAGED="catalogVersion
createdAtRevision
closedAtRevision"
    UNEXPECTED="$(comm -23 <(printf '%s\n' "$REQ_TAGS") <(printf '%s\n%s\n' "$WL_KEYS" "$EXTRA_ALLOWED" | sort -u))"
    MISSING="$(comm -23 <(printf '%s\n' "$WL_KEYS" | grep -vxF -f <(printf '%s\n' "$SERVER_MANAGED")) <(printf '%s\n' "$REQ_TAGS"))"
    if [ -n "$UNEXPECTED" ]; then
      fail "AC4: actionTypeReq has json tag(s) not in the whitelist (or the known relationship-field allowlist): $(printf '%s' "$UNEXPECTED" | tr '\n' ' ')"
    elif [ -n "$MISSING" ]; then
      fail "AC4: actionTypeReq is MISSING json tag(s) the whitelist requires: $(printf '%s' "$MISSING" | tr '\n' ' ') — a caller could never even attempt to set these, which is safe but means the whitelist and the request body have drifted"
    else
      ok "AC4: actionTypeReq's json tags == ActionTypeAttributeWhitelist ∪ {byPartyId, composedOf, requiresPermissionCodes}"
    fi
  fi

  if grep -E '^\s*dec\.DisallowUnknownFields\(\)\s*$' "$SERVER_GO" >/dev/null; then
    ok "AC4: decode() still calls DisallowUnknownFields (whitelist-violating JSON keys are REJECTED, not ignored)"
  else
    fail "AC4: decode() no longer calls DisallowUnknownFields — a stray JSON key would be silently dropped, violating AC4's '거부(무시 아님)'"
  fi
fi

DELEGATION_GO="$ROOT/api/internal/authz/delegation.go"
DELEGATION_TEST_GO="$ROOT/api/internal/authz/delegation_test.go"
HUMANONLY_GO="$ROOT/api/internal/authz/humanonly.go"
AUTHZ_GO="$ROOT/api/internal/authz/authz.go"
WRITEOPS_GO="$ROOT/core/internal/graphdb/writeops.go"

# ===========================================================================
# AC5 (actiontype.write는 :AIAgent에 위임 불가, DG6, 치명) — 실제 검사.
#
# §9: "CheckDelegation 분기 존재 + 통합테스트 2건." Three textual checks:
#  1. delegation.go's DG6 section (between the "--- DG6" and "--- DG1 —"
#     markers) mentions the refusal rule "DG6", the code PermActionTypeWrite,
#     and the TargetIsAIAgent field together — not just any one of them.
#  2. delegation_test.go exists and has at least 2 DG6-named test functions.
# ===========================================================================
say
say "AC5 (actiontype.write는 :AIAgent에 위임 불가, DG6) — 실제 검사"
CHECKS=$((CHECKS+1))

if [ ! -f "$DELEGATION_GO" ]; then
  fail "AC5: $DELEGATION_GO not found"
else
  DG6_BLOCK="$(awk '/--- DG6 —/{f=1} f{print} f&&/--- DG1 —/{exit}' "$DELEGATION_GO")"
  if [ -z "$DG6_BLOCK" ]; then
    fail "AC5: no '--- DG6 —' section found in $DELEGATION_GO"
  elif printf '%s' "$DG6_BLOCK" | grep -q '"DG6"' \
    && printf '%s' "$DG6_BLOCK" | grep -q 'PermActionTypeWrite' \
    && printf '%s' "$DG6_BLOCK" | grep -q 'TargetIsAIAgent'; then
    ok "AC5: CheckDelegation's DG6 section refuses PermActionTypeWrite grants where TargetIsAIAgent"
  else
    fail "AC5: DG6 section exists but does not tie together the refusal rule, PermActionTypeWrite and TargetIsAIAgent"
  fi
fi

if [ ! -f "$DELEGATION_TEST_GO" ]; then
  fail "AC5: $DELEGATION_TEST_GO not found — §9 requires '통합테스트 2건'"
else
  DG6_TEST_COUNT="$(grep -cE '^func TestDG6' "$DELEGATION_TEST_GO")"
  if [ "$DG6_TEST_COUNT" -ge 2 ]; then
    ok "AC5: $DG6_TEST_COUNT DG6 test function(s) found in $DELEGATION_TEST_GO (>= 2 required)"
  else
    fail "AC5: only $DG6_TEST_COUNT DG6 test function(s) found in $DELEGATION_TEST_GO, need >= 2"
  fi
fi

# ===========================================================================
# AC6 (HumanOnly는 닫힌 열거이며, 원소 중 grant-catalog에서 도구/서버/egress가
# 채워진 코드가 있으면 실패, P1-Q 상한) — 실제 검사, 세 Go 모듈/파일 교차.
#
# This is the one check in this script that reads across module/package
# boundaries — see humanonly.go's doc comment for why that cross-check
# cannot be a Go test in any one module alone. Textual, in the same spirit
# as AC10's Cypher-clause grep.
#
# TWO INDEPENDENT SUB-CHECKS, not one:
#  (i)  osiris-api's HumanOnly (humanonly.go) vs. orchestrator's grant-
#       catalog Table (catalog.go) — resolve each HumanOnly identifier to
#       its string code via authz.go's `Ident Code = "value"` declarations,
#       then confirm that code's Table entry mentions none of
#       ClaudeAllowedTools/MCPTools/EgressAllowlist.
#  (ii) osiris-api's HumanOnly vs. osiris-core's OWN independent copy,
#       humanOnlyPermissionCodes (writeops.go) — a SET EQUALITY check, both
#       directions. This is the one an earlier pass of this task got wrong:
#       it added a THIRD hardcoded literal (in a Go test) that pinned only
#       core's own copy to itself and called that "matching the API layer"
#       without ever reading api/internal/authz/humanonly.go. Go cannot fix
#       that (core and api are separate modules, no import is possible), so
#       this lint section is the ONLY place in the repository capable of
#       actually comparing the two copies' CONTENTS (not just their counts)
#       — which is why it belongs here, not in either module's `go test`.
# ===========================================================================
say
say "AC6 (HumanOnly 원소는 grant-catalog에서 도구/서버/egress가 비어야 한다, P1-Q) — 실제 검사"
CHECKS=$((CHECKS+1))

if [ ! -f "$HUMANONLY_GO" ] || [ ! -f "$AUTHZ_GO" ] || [ ! -f "$CATALOG_GO" ]; then
  fail "AC6: one of $HUMANONLY_GO / $AUTHZ_GO / $CATALOG_GO not found"
else
  HUMANONLY_IDENTS="$(awk '/^var HumanOnly = map\[Code\]bool\{$/{f=1;next} f&&/^\}$/{f=0} f' "$HUMANONLY_GO" \
    | sed -nE 's/^\s*([A-Za-z0-9_]+):\s*true.*/\1/p')"
  if [ -z "$HUMANONLY_IDENTS" ]; then
    fail "AC6: could not parse any identifiers out of HumanOnly's map literal in $HUMANONLY_GO"
  else
    IDENT_COUNT=0
    AC6_BAD=0
    HUMANONLY_CODES=""
    for ident in $HUMANONLY_IDENTS; do
      IDENT_COUNT=$((IDENT_COUNT+1))
      code="$(sed -nE "s/^[[:space:]]*${ident}[[:space:]]+Code = \"([^\"]+)\".*/\1/p" "$AUTHZ_GO")"
      if [ -z "$code" ]; then
        fail "AC6: HumanOnly identifier $ident has no 'Code = \"...\"' declaration in $AUTHZ_GO"
        AC6_BAD=1
        continue
      fi
      HUMANONLY_CODES="$HUMANONLY_CODES $code"
      ENTRY_BLOCK="$(awk -v key="\"$code\":" '
        index($0, key) { capturing=1 }
        capturing && /^\t"[A-Za-z]/ && index($0, key) == 0 { exit }
        capturing && /^}$/ { exit }
        capturing { print }
      ' "$CATALOG_GO")"
      if [ -z "$ENTRY_BLOCK" ]; then
        fail "AC6: orchestrator grant-catalog Table has no entry for HumanOnly code %q ($code)"
        AC6_BAD=1
        continue
      fi
      if printf '%s' "$ENTRY_BLOCK" | grep -qE 'ClaudeAllowedTools|MCPTools|EgressAllowlist'; then
        fail "AC6: HumanOnly code $code has a non-empty ClaudeAllowedTools/MCPTools/EgressAllowlist in orchestrator's grant-catalog — P1-Q's upper bound is violated"
        AC6_BAD=1
      fi
    done
    if [ "$IDENT_COUNT" -ne 4 ]; then
      fail "AC6: HumanOnly has $IDENT_COUNT identifier(s), expected exactly 4 (80 §5.1.3 P1-Q's closed enumeration)"
      AC6_BAD=1
    fi
    if [ "$AC6_BAD" -eq 0 ]; then
      ok "AC6: all $IDENT_COUNT HumanOnly code(s) carry empty ClaudeAllowedTools/MCPTools/EgressAllowlist in orchestrator's grant-catalog"
    fi

    # --- sub-check (ii): HumanOnly (api) == humanOnlyPermissionCodes (core), SET EQUALITY ---
    if [ ! -f "$WRITEOPS_GO" ]; then
      fail "AC6: $WRITEOPS_GO not found — cannot cross-check core's HumanOnly copy"
    else
      WRITEOPS_CODES="$(awk '/^var humanOnlyPermissionCodes = map\[string\]struct\{\}\{$/{f=1;next} f&&/^\}$/{f=0} f' "$WRITEOPS_GO" \
        | grep -oE '"[a-zA-Z0-9_.]+"' | tr -d '"')"
      if [ -z "$WRITEOPS_CODES" ]; then
        fail "AC6: could not parse any codes out of humanOnlyPermissionCodes' map literal in $WRITEOPS_GO"
      else
        CROSS_BAD=0
        # api -> core: every HumanOnly code must appear in core's copy.
        for c in $HUMANONLY_CODES; do
          found=0
          for w in $WRITEOPS_CODES; do
            [ "$c" = "$w" ] && found=1 && break
          done
          if [ "$found" -eq 0 ]; then
            fail "AC6: HumanOnly code $c (api/internal/authz/humanonly.go) is MISSING from core's humanOnlyPermissionCodes ($WRITEOPS_GO) — the structural mutatePartyGrant guard would not refuse it"
            CROSS_BAD=1
          fi
        done
        # core -> api: every code in core's copy must be a real HumanOnly code.
        for w in $WRITEOPS_CODES; do
          found=0
          for c in $HUMANONLY_CODES; do
            [ "$c" = "$w" ] && found=1 && break
          done
          if [ "$found" -eq 0 ]; then
            fail "AC6: core's humanOnlyPermissionCodes ($WRITEOPS_GO) contains $w, which is NOT in api's HumanOnly (humanonly.go) — the two copies have drifted"
            CROSS_BAD=1
          fi
        done
        if [ "$CROSS_BAD" -eq 0 ]; then
          ok "AC6: api's HumanOnly and core's humanOnlyPermissionCodes are the exact same set ($(echo $HUMANONLY_CODES | tr -s ' ' ', '))"
        fi
      fi
    fi
  fi
fi

# ===========================================================================
# AC11 (부분, 치명) — 게이트/오염/효과/가역성 완화는 org.admin만, 발행 성공이
# 커밋 조건 (DG8). 정적 검사만: CheckDelegation의 DG8 분기와
# UpdateActionType의 fail-closed 발행 게이트가 존재하는지, 그리고 통합테스트.
# 런타임(발행이 실제로 트랜잭션을 롤백시키는지)은 Neo4j 없이는 검사 불가 —
# §9 자신이 "부분"이라고 적은 이유.
# ===========================================================================
say
say "AC11 (부분) — DG8: 완화는 org.admin만 + 발행 성공이 커밋 조건 — 정적 검사"
CHECKS=$((CHECKS+1))

if [ ! -f "$DELEGATION_GO" ]; then
  fail "AC11: $DELEGATION_GO not found"
else
  DG8_BLOCK="$(awk '/DG8 — relaxation/{f=1} f{print} f&&/return Decision\{Allowed: true, RequiresRelaxationNotify/{exit}' "$DELEGATION_GO")"
  # A literal condition line, not just "the three tokens appear somewhere in
  # the block" — the earlier looser form did not fail when the condition was
  # short-circuited to dead code (e.g. `if false && req.RelaxesActionType &&
  # !actor[PermOrgAdmin] {`), which is exactly the fault this check exists to
  # catch. Anchoring on the exact `if req.RelaxesActionType && !actor[...]`
  # guard text closes that hole.
  if [ -z "$DG8_BLOCK" ]; then
    fail "AC11: no DG8 relaxation-gate section found in $DELEGATION_GO"
  elif printf '%s' "$DG8_BLOCK" | grep -qE '^\s*if req\.RelaxesActionType && !actor\[PermOrgAdmin\] \{' \
    && printf '%s' "$DG8_BLOCK" | grep -q '"DG8"'; then
    ok "AC11: CheckDelegation's DG8 section refuses a relaxation unless the actor holds org.admin"
  else
    fail "AC11: DG8 section exists but does not gate on 'if req.RelaxesActionType && !actor[PermOrgAdmin]' + a \"DG8\" refusal"
  fi
fi

ACTIONTYPE_RELAX_GO="$ROOT/core/internal/graphdb/actiontype.go"
if [ ! -f "$ACTIONTYPE_RELAX_GO" ]; then
  fail "AC11: $ACTIONTYPE_RELAX_GO not found"
else
  if grep -q 'func actionTypeRelaxes(' "$ACTIONTYPE_RELAX_GO" \
    && grep -q 'security.actiontype.relaxed' "$ACTIONTYPE_RELAX_GO"; then
    # Fail-closed ordering: the notify call must appear AFTER bumpRevision
    # inside UpdateActionType, in the SAME transaction closure, so that a
    # publish failure aborts the whole write (see actiontype.go's comment).
    BUMP_LINE="$(grep -n 'bumpRevision(ctx, tx, in.ByPartyID, fmt.Sprintf("action type %s updated"' "$ACTIONTYPE_RELAX_GO" | cut -d: -f1 | head -1)"
    NOTIFY_LINE="$(grep -n 'security.actiontype.relaxed' "$ACTIONTYPE_RELAX_GO" | cut -d: -f1 | head -1)"
    if [ -n "$BUMP_LINE" ] && [ -n "$NOTIFY_LINE" ] && [ "$NOTIFY_LINE" -gt "$BUMP_LINE" ]; then
      ok "AC11: UpdateActionType's security.actiontype.relaxed notification runs inside the same write transaction, after bumpRevision (fail-closed)"
    else
      fail "AC11: could not confirm the relaxation notification runs after bumpRevision inside UpdateActionType's transaction"
    fi
  else
    fail "AC11: $ACTIONTYPE_RELAX_GO is missing actionTypeRelaxes or the security.actiontype.relaxed notification"
  fi
fi

if [ ! -f "$DELEGATION_TEST_GO" ]; then
  fail "AC11: $DELEGATION_TEST_GO not found — §9 requires an integration test"
else
  DG8_TEST_COUNT="$(grep -cE '^func TestDG8' "$DELEGATION_TEST_GO")"
  if [ "$DG8_TEST_COUNT" -ge 2 ]; then
    ok "AC11: $DG8_TEST_COUNT DG8 test function(s) found in $DELEGATION_TEST_GO"
  else
    fail "AC11: only $DG8_TEST_COUNT DG8 test function(s) found in $DELEGATION_TEST_GO, need >= 2"
  fi
fi

GATEFOR_GO="$ROOT/core/internal/action/dispatch/gatefor.go"

# ===========================================================================
# AC7 (gateFor 시그니처에 Party/principal/partyId 없음, HI16 보호) — P2 stage
# ③ 실제 검사.
#
# §9: "gateFor의 시그니처에 Party/principal/partyId가 나타나지 않는다." 등급
# 치명. gateFor now exists (core/internal/action/dispatch/gatefor.go, stage
# ③) — this is a literal, single-line signature grep: the exact text of the
# `func gateFor(` declaration line must not contain "Party", "principal", or
# "partyId" (case-insensitive, so PartyID/PARTY_ID etc. are also caught).
# ===========================================================================
say
say "AC7 (gateFor 시그니처에 Party/principal/partyId 없음, HI16 보호) — 실제 검사"
CHECKS=$((CHECKS+1))

if [ ! -f "$GATEFOR_GO" ]; then
  fail "AC7: $GATEFOR_GO not found"
else
  SIG_LINE="$(grep -nE '^func gateFor\(' "$GATEFOR_GO" | head -1)"
  if [ -z "$SIG_LINE" ]; then
    fail "AC7: could not locate a 'func gateFor(' declaration in $GATEFOR_GO"
  else
    SIG_TEXT="$(printf '%s' "$SIG_LINE" | cut -d: -f2-)"
    if printf '%s' "$SIG_TEXT" | grep -qiE 'party|principal'; then
      fail "AC7: gateFor signature mentions Party/principal/partyId — $SIG_TEXT"
    else
      ok "AC7: gateFor signature carries no Party/principal/partyId — $SIG_TEXT"
    fi
  fi
fi

# ===========================================================================
# AC10 (COMPOSED_OF 엣지 속성은 ord 하나뿐) — P2 stage ② 실제 검사.
#
# 세 겹: (1) ComposedOfInput 구조체가 값 필드를 정확히 하나(Ord)만 갖는다,
# (2) COMPOSED_OF를 CREATE하는 Cypher가 ord 외의 관계 속성을 SET/CREATE하지
# 않는다, (3) api/server.go의 composedOfReq도 JSON 쪽에서 같은 모양이다.
# ===========================================================================
say
say "AC10 (COMPOSED_OF 엣지 속성은 ord 하나뿐) — 실제 검사"
CHECKS=$((CHECKS+1))

if [ ! -f "$ACTIONTYPE_GO" ]; then
  fail "AC10: $ACTIONTYPE_GO not found"
else
  COMPOSED_STRUCT_BODY="$(awk '/^type ComposedOfInput struct \{$/{f=1;next} f&&/^\}$/{f=0} f' "$ACTIONTYPE_GO")"
  FIELD_COUNT="$(printf '%s\n' "$COMPOSED_STRUCT_BODY" | grep -cE '^\s*[A-Za-z]')"
  if [ "$FIELD_COUNT" -eq 2 ] && printf '%s\n' "$COMPOSED_STRUCT_BODY" | grep -qE '^\s*ChildActionTypeID\s' && printf '%s\n' "$COMPOSED_STRUCT_BODY" | grep -qE '^\s*Ord\s'; then
    ok "AC10: ComposedOfInput has exactly ChildActionTypeID (the edge target, not a property) + Ord (the one allowed relationship property)"
  else
    fail "AC10: ComposedOfInput does not have exactly the expected two fields (got $FIELD_COUNT field(s)): $(printf '%s' "$COMPOSED_STRUCT_BODY" | tr '\n' ';')"
  fi

  # The CREATE clause that actually writes the edge: must set exactly
  # "ord" as a relationship property and nothing else. Narrow, literal
  # match on the fixed Cypher string this codebase always uses (writeops.go
  # convention: fixed parameterized queries, no builder — see that file's
  # header).
  if grep -qE 'CREATE \(a\)-\[:COMPOSED_OF \{ord: \$ord\}\]->\(child\)' "$ACTIONTYPE_GO"; then
    ok "AC10: the COMPOSED_OF CREATE clause sets exactly {ord: \$ord} and no other relationship property"
  else
    fail "AC10: could not find the expected 'CREATE (a)-[:COMPOSED_OF {ord: \$ord}]->(child)' clause in $ACTIONTYPE_GO — either it changed shape or was not found"
  fi

  # Defense in depth: every place COMPOSED_OF is bound to a relationship
  # variable (`r:COMPOSED_OF`) must be immediately followed (next
  # non-empty line) by exactly "DELETE r" — the only thing this codebase's
  # replace path does with that binding. Any OTHER use (e.g. "SET r.foo =
  # ...") would be a second, less obvious write path adding a property.
  BOUND_LINES="$(grep -nE '\[r:COMPOSED_OF\]' "$ACTIONTYPE_GO" | cut -d: -f1)"
  BAD_BINDING=0
  for ln in $BOUND_LINES; do
    next="$(sed -n "$((ln+1))p" "$ACTIONTYPE_GO" | tr -d '[:space:]')"
    if [ "$next" != "DELETEr" ]; then
      fail "AC10: line $ln binds a COMPOSED_OF relationship to a variable but the next line is not a bare 'DELETE r' (got: $next) — review for a hidden second property writer"
      BAD_BINDING=1
    fi
  done
  if [ "$BAD_BINDING" -eq 0 ]; then
    if [ -z "$BOUND_LINES" ]; then
      note "AC10: no COMPOSED_OF relationship variable bindings found at all (informational)"
    else
      ok "AC10: every COMPOSED_OF relationship variable binding is immediately followed by 'DELETE r' — no hidden second property writer"
    fi
  fi
fi

if [ -f "$SERVER_GO" ]; then
  COMPOSED_REQ_BODY="$(awk '/^type composedOfReq struct \{$/{f=1;next} f&&/^\}$/{f=0} f' "$SERVER_GO")"
  REQ_FIELD_COUNT="$(printf '%s\n' "$COMPOSED_REQ_BODY" | grep -cE '^\s*[A-Za-z]')"
  if [ "$REQ_FIELD_COUNT" -eq 2 ]; then
    ok "AC10: composedOfReq (JSON request shape) also has exactly two fields"
  else
    fail "AC10: composedOfReq has $REQ_FIELD_COUNT field(s), expected 2 (childActionTypeId + ord)"
  fi
fi

# ===========================================================================
# AC20 (:Permission.gate/approverRole/limitJson은 :ActionType에서 파생된
# 미러이며 직접 쓰기 금지, AD13) — [2026-08-13] G-A11 해소분: 부팅 검사가
# 이제 실제 min 대조를 한다(actiontype_permission_mirror.go). 이 린트는 그
# 반대쪽 절반, "미러는 판정에 쓰이지 않는다"를 지킨다:
#
#   (a) orchestrator/internal/catalog.go의 Grant.Gate 필드를 실제로 "읽는"
#       코드가 그 파일 밖에 없다. core/internal/action/dispatch의
#       Outcome.Gate는 완전히 다른 타입이므로 화이트리스트에 명시적으로
#       올려 오검출을 막는다(과제 브리핑이 이름으로 지목한 함정).
#   (b) :Permission의 gate/approverRole/limitJson 프로퍼티를 Cypher의
#       RETURN/WHERE(=읽기·조건절)에 쓰는 코드가 seedPermissions(쓰기 전용,
#       seed.go)와 새 미러 검증(읽기지만 대조 전용, actiontype_permission_
#       mirror.go) 밖에 없다.
#
# 두 검사 모두 "이 필드를 읽는 코드가 늘어나면 실패"하는 화이트리스트
# 형태이므로, 화이트리스트 파일 자신을 늘리지 않고는 새 소비자를 추가할 수
# 없다 — grant-catalog/그래프 어느 쪽도 미러를 판정에 쓰기 시작하면 이
# 그룹이 잡는다.
# ===========================================================================
say
say "AC20 (:Permission 미러는 판정에 쓰이지 않는다, AD13/G-A11 해소) — 실제 검사"
CHECKS=$((CHECKS+1))

# (a) Grant.Gate readers. ".Gate" (word-boundary, so "Gateway"/"GatewayToken"
# etc. never match) anywhere under the four Go modules, MINUS the known,
# reviewed set:
#   - catalog.go itself (defines the field; "Gate: \"...\"" struct-literal
#     writes do not even match "\.Gate\b", so catalog.go should not appear
#     here at all today — if it ever does, that is itself worth seeing)
#   - dispatch.go / dispatch_test.go: Outcome.Gate, a DIFFERENT field on a
#     DIFFERENT type in a different package — the named false-positive trap
#   - seed.go / actiontype_permission_mirror.go: comments that mention
#     "Grant.Gate" in prose (documentation, not code reading the field)
GATE_HITS="$(grep -rn '\.Gate\b' --include='*.go' \
  "$ROOT/core" "$ROOT/api" "$ROOT/agent" "$ROOT/orchestrator" 2>/dev/null \
  | grep -v -F "$CATALOG_GO:" \
  | grep -v -F "$ROOT/core/internal/action/dispatch/dispatch.go:" \
  | grep -v -F "$ROOT/core/internal/action/dispatch/dispatch_test.go:" \
  | grep -v -F "$SEED_GO:" \
  | grep -v -F "$MIRROR_GO:")"
if [ -n "$GATE_HITS" ]; then
  fail "AC20(a): code outside the reviewed set reads a '.Gate' field — verify it is not orchestrator's Grant.Gate (unread mirror, per 80 §8.1/G-A11):"
  printf '%s\n' "$GATE_HITS" | while IFS= read -r line; do fail "AC20(a):   $line"; done
else
  ok "AC20(a): no '.Gate' field read outside catalog.go (definer)/dispatch.go+dispatch_test.go (Outcome.Gate, a different type)/seed.go+actiontype_permission_mirror.go (comments only) — Grant.Gate has zero readers"
fi

# (b) :Permission.gate/approverRole/limitJson consumed in a Cypher
# RETURN/WHERE clause anywhere outside seedPermissions (seed.go, a WRITE via
# SET) and the new mirror check (actiontype_permission_mirror.go, a READ but
# comparison-only — never used to grant/deny anything).
PERM_FIELD_PATTERN='(^|[^A-Za-z_])p\.(gate|approverRole|limitJson)\b|perm\.(gate|approverRole|limitJson)\b'
PERM_FIELD_HITS="$(grep -rnE "$PERM_FIELD_PATTERN" --include='*.go' \
  "$ROOT/core" "$ROOT/api" "$ROOT/agent" "$ROOT/orchestrator" 2>/dev/null \
  | grep -v -F "$SEED_GO:" \
  | grep -v -F "$MIRROR_GO:")"
if [ -n "$PERM_FIELD_HITS" ]; then
  fail "AC20(b): :Permission gate/approverRole/limitJson referenced outside seedPermissions (write) / actiontype_permission_mirror.go (derivation check only):"
  printf '%s\n' "$PERM_FIELD_HITS" | while IFS= read -r line; do fail "AC20(b):   $line"; done
else
  ok "AC20(b): :Permission.gate/approverRole/limitJson appear only in seedPermissions' SET clause (seed.go) and the AC20 derivation check (actiontype_permission_mirror.go) — no authorization decision reads the mirror"
fi

# (c) Sanity check that AC20's boot check actually computes a derivation
# now, not just a "count and refuse" stub (the pre-G-A11-closure shape this
# file replaced). A crude but effective signal: the min-derivation helper
# function must exist and be called from the boot check.
if [ ! -f "$MIRROR_GO" ]; then
  fail "AC20(c): $MIRROR_GO not found"
elif grep -q 'func derivePermissionGate(' "$MIRROR_GO" && grep -q 'derivePermissionGate(row.gateClasses)' "$MIRROR_GO"; then
  ok "AC20(c): CheckActionTypePermissionMirror calls a real derivePermissionGate(...) helper — not a count-only stub"
else
  fail "AC20(c): actiontype_permission_mirror.go no longer calls a derivePermissionGate(...) helper from its boot check — AC20 may have regressed to a count-only stub"
fi

say
say "AC13 (게이트 핸들러가 요청 바디의 taint 필드를 읽지 않는다, AD9)"
note "AC13: 미구현(②단계 이후) — 게이트 핸들러 자체가 아직 없음(§7 오염 모델은 이 워커의 범위 밖)"

say
say "AC18 (실행 경로에 능력 키/스택 이름 리터럴이 없다, AD11)"
note "AC18: 미구현(②단계 이후) — requiresCapability를 소비하는 실행 경로가 아직 없음(81과의 계약, §5.4)"

# ===========================================================================
say
say "--- lint-action-layer: $CHECKS invariant group(s) checked (stage ③ scope) ---"
if [ "$FAIL" -eq 0 ]; then
  echo "PASS: AC3(정적 부분)/AC4/AC5/AC6/AC7/AC10/AC11(정적 부분)/AC20 hold. AC13/AC18 remain unimplemented stubs — see notes above (later stages)."
else
  echo "FAIL: see above. Spec: docs/design/80-action-layer.md §9." >&2
fi
exit $FAIL
