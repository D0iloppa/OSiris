#!/usr/bin/env bash
# lint-identity-authn.sh — CI guard for the machine-principal invariants of
# docs/design/60-identity-authn.md §13.
#
# This is the THIRD lint in this repository's family (bin/lint-driver-boundary.sh,
# bin/lint-health-invariant.sh) and follows their shape deliberately: crude,
# textual, reviewable in under a minute. A reviewer who cannot verify the lint
# itself will not trust what it claims to prove.
#
# 60 §13 specifies twenty invariants HI1~HI20. This build implements §19
# (verification-only machine principals) only, so this script implements the
# subset that §19 makes checkable TODAY:
#
#   HI15  mutating routes reject machine principals — STATIC route-table check
#   HI14  machine principals are runtime issuances, and their scope is
#         read-only — ① nothing in the install/boot path creates one,
#                     ② the issue-time allowlist is org.read.* and nothing else
#   HI13  a machine principal never becomes a session
#   HI11  this design added zero new .env variables
#   HI9   only osiris-api is granted $KV.AUTHN_* rights
#
# [2026-07-28] THE RBAC AND H10 CHECKS LANDED. The paragraph that used to sit
# here said HI16~HI18 "police code that does not exist yet ... A lint that
# checks an empty set passes for the wrong reason." That code exists now
# (authz.CheckDelegation, internal/people, internal/masterkey), so the empty-set
# argument no longer holds and these were added:
#
#   HI16  the delegation decision exists in exactly ONE function, and every
#         C10 action route reaches it
#   HI17  no role/profile id or title literal appears in the authorization path
#   HI18  DG3 (self-target) and DG4 (last credentialed admin) are present, and
#         the handlers that can strip org.admin compute RemovesTargetAdmin
#   H10   the master key is prefix-routed, its usability is re-evaluated at
#         every acceptance point, the legacy setup_token field is gone, and only
#         internal/masterkey deletes the key file
#   AUD   every secevents subject prefix is granted in the NATS ACL generator
#   GEN   the genesis principal's scope is an allowlist and excludes commit (F14)
#
# STILL NOT IMPLEMENTED, and honestly so: HI1~HI8, HI10, HI12, HI19~HI20.
#
# AUD IS NOT IN §13's LIST. It is here because it caught a real defect during
# the H10 build rather than because a document asked for it: a new
# security.masterkey.* subject was published without a matching NATS grant, and
# since the audit publish waits for a JetStream ack, the missing grant did not
# quietly drop an event — it stalled the credential-redemption request that
# produced it for over 100 seconds. A lint is the right place for that class of
# mistake because the two files involved are owned by different services.
#
# EVERY CHECK BELOW HAS BEEN FAULT-INJECTED — a deliberate violation was
# introduced, this script was confirmed to FAIL, and the violation was
# reverted. See docs/design/IMPLEMENTATION-NOTES.md for the injection log.
# Without that step a lint that checks nothing passes silently, which is worse
# than no lint at all.
#
# Exit 0 = PASS. Exit 1 = FAIL. No arguments.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SELF="$ROOT/bin/lint-identity-authn.sh"
FAIL=0
CHECKS=0

say()  { echo "$*"; }
ok()   { echo "  ok    $*"; }
fail() { echo "  FAIL  $*" >&2; FAIL=1; }
note() { echo "  --    $*"; }

SERVER_GO="$ROOT/api/internal/api/server.go"
AUTHZ_GO="$ROOT/api/internal/authz/authz.go"
MACHINE_DIR="$ROOT/api/internal/machineauth"
NATSAUTH_GO="$ROOT/orchestrator/internal/natsauth/natsauth.go"

say "--- lint-identity-authn (60-identity-authn.md §13, §19 subset) ---"

# ===========================================================================
# HI15 — "mutating 라우트는 기계 주체를 거부한다"
#
#   §13's method: "라우터 등록부 정적 검사: GET/HEAD 이외의 모든 라우트와
#   /api/auth/* 전체가 personOnly 미들웨어를 통과하는지. HI12와 같은 형태
#   (핸들러 내부 개별 검사는 새 핸들러가 빠뜨린다)."
#
# THIS CHECK ONLY BECAME POSSIBLE IN THIS BUILD. Before it, routes were
# registered as bare paths and the method was tested inside the handler, so
# "non-GET route" was not a property of the route table and there was nothing
# static to inspect. Go 1.22 method patterns ("POST /api/x") put the method
# back in the table. The first assertion below is therefore the load-bearing
# one: a route WITHOUT a method prefix fails, because it would silently opt
# out of every other rule here.
# ===========================================================================
say
say "HI15  mutating routes are person-only, checked against the route table"
CHECKS=$((CHECKS+1))

if [ ! -f "$SERVER_GO" ]; then
  fail "HI15: $SERVER_GO not found"
else
  # Only APIMux's body. HealthMux's /livez//readyz live on a separate listener
  # and are intentionally unauthenticated (§5.4).
  # ── [2026-07-28] MULTI-LINE REGISTRATIONS ARE JOINED FIRST ───────────────
  #
  # This check reads one route per LINE, which was true when it was written and
  # stopped being true when the org-role routes arrived: those stack two
  # middlewares and wrap across three lines. The per-line reader then extracted
  # an EMPTY handler expression and reported "not wrapped in PersonOnly" for six
  # routes that are, in fact, wrapped — a FALSE FAILURE, which is the more
  # dangerous direction for a lint nobody can trust.
  #
  # So the router body is normalized before it is read: a registration is
  # accumulated until its parentheses balance. Parens inside string literals are
  # skipped, because a route pattern may legitimately contain one.
  BODY="$(awk '/^func \(s \*Server\) APIMux\(\)/,/^}/' "$SERVER_GO" | awk '
    function bal(s,   i,c,q,ch,d) {
      d = 0; q = 0
      for (i = 1; i <= length(s); i++) {
        ch = substr(s, i, 1)
        if (ch == "\"") { q = !q; continue }
        if (q) continue
        if (ch == "(") d++
        else if (ch == ")") d--
      }
      return d
    }
    /^[[:space:]]*mux\.(HandleFunc|Handle)\(/ {
      acc = $0; depth = bal($0)
      while (depth > 0 && (getline nxt) > 0) {
        sub(/^[[:space:]]+/, "", nxt)
        acc = acc " " nxt
        depth += bal(nxt)
      }
      print acc; next
    }
    { print }
  ')"
  if [ -z "$BODY" ]; then
    fail "HI15: could not locate APIMux() in $SERVER_GO — the lint must be updated before the router is restructured"
  fi

  ROUTES="$(printf '%s\n' "$BODY" | grep -E '^[[:space:]]*mux\.(HandleFunc|Handle)\(' )"
  if [ -z "$ROUTES" ]; then
    fail "HI15: APIMux() registers no routes — refusing to pass a check with an empty subject"
  fi

  n_routes=0
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    n_routes=$((n_routes+1))
    pattern="$(printf '%s' "$line" | sed -n 's/.*mux\.Handle\(Func\)\?("\([^"]*\)".*/\2/p')"
    handler="$(printf '%s' "$line" | sed -n 's/.*mux\.Handle\(Func\)\?("[^"]*",[[:space:]]*\(.*\))[[:space:]]*$/\2/p')"

    method="$(printf '%s' "$pattern" | sed -n 's/^\([A-Z][A-Z]*\) .*/\1/p')"
    path="$(printf '%s' "$pattern" | sed 's/^[A-Z][A-Z]* //')"

    if [ -z "$method" ]; then
      fail "HI15: route \"$pattern\" has no HTTP method in its pattern."
      fail "      Without a method pattern the router cannot tell a mutating route from a read,"
      fail "      and this entire invariant becomes unenforceable. Register it as e.g. \"POST $pattern\"."
      continue
    fi

    case "$method" in
      GET|HEAD)
        case "$path" in
          /api/auth/*)
            # §19.6 row ②: the whole /api/auth/* subtree is closed to machine
            # principals regardless of method.
            case "$handler" in
              *PersonOnly*) : ;;
              *) fail "HI15: $method $path is under /api/auth/* but is not wrapped in a PersonOnly middleware: $handler" ;;
            esac
            ;;
          *) : ;;  # a read route may be RequirePerm(...) — that is the point of (c)
        esac
        ;;
      *)
        case "$path" in
          /api/infra/proposals)
            # [2026-09-11, C-1] docs/design/90-infra-topology.md §11 IN18 —
            # THE ONE DELIBERATE, NAMED HI15 EXCEPTION IN THIS CODEBASE.
            # "기계주체는 POST /_api/infra/proposals(제안 생성)만 할 수
            # 있다... 제안은 inert — 사람이 승인하기 전엔 아무 능력도
            # 부여하지 않으므로, 기계의 제안 생성은 권한 경계가 아니다."
            # This carve-out is scoped to EXACTLY this one path and method —
            # not a wildcard, not "any /api/infra/* POST" — so a future
            # mutating infra route does not silently inherit the exception.
            case "$handler" in
              *RequirePerm\(authz.PermOrgReadOps*) : ;;
              *) fail "HI15(IN18): POST /api/infra/proposals must be RequirePerm(authz.PermOrgReadOps, ...) — got: $handler" ;;
            esac
            ;;
          *)
            case "$handler" in
              *PersonOnly*) : ;;
              *)
                fail "HI15: $method $path is a mutating route but is not wrapped in a PersonOnly middleware."
                fail "      Handler expression: $handler"
                fail "      A machine principal must be refused by the ROUTER, not by a check inside the handler"
                fail "      (60 §19.6 row ①; a handler-internal check is what a new handler forgets)."
                ;;
            esac
            ;;
        esac
        ;;
    esac
  done <<< "$ROUTES"

  if [ "$FAIL" -eq 0 ]; then
    ok "HI15: $n_routes routes, every non-GET one and all of /api/auth/* go through PersonOnly"
  fi

  # ---------------------------------------------------------------------
  # HI15b — the long-lived route re-checks its credential.
  #
  # Authentication at connect time is not authentication for the life of a
  # connection. Before this build, a session that had been logged out kept
  # receiving SSE events until the client hung up (MEASURED — see
  # IMPLEMENTATION-NOTES.md §104), which made §8.4's server-side invalidation
  # and §19.8 MP5's "즉시 리보크" both silently false for /api/events.
  #
  # GET /api/events is the only long-lived response this service serves, so
  # this pins that one route to the middleware that carries the watchdog. If
  # a second streaming route is ever added, add it here — a lint that names
  # its subject explicitly is better than one that guesses which handlers
  # block.
  # ---------------------------------------------------------------------
  SSE_LINE="$(printf '%s\n' "$ROUTES" | grep -F '"GET /api/events"')"
  if [ -z "$SSE_LINE" ]; then
    note "HI15b: no GET /api/events route registered, skipped"
  elif printf '%s' "$SSE_LINE" | grep -q 'RequirePermStreaming('; then
    ok "HI15b: GET /api/events revalidates its credential while the stream is open"
  else
    fail "HI15b: GET /api/events is not wrapped in RequirePermStreaming."
    fail "       A long-lived response authenticated only at connect time keeps serving a"
    fail "       revoked credential for as long as the client holds the socket (60 §19.8 MP5)."
  fi
fi

# ===========================================================================
# HI14 ① — machine principals are RUNTIME issuances, never install-time.
#
#   §13: "osiris-up·docker-compose*.yml·bin/**·부팅 경로에서 기계 주체를
#   생성하는 코드가 발견되면 실패 (설치 시점 굽기 금지 — 4회 재발 패턴)."
#
#   §19.5's table states the rule positively: the default state is ZERO
#   machine principals, and that is also the normal state. Nothing about the
#   stack presumes one exists.
# ===========================================================================
say
say "HI14① machine principals are never created by the install or boot path"
CHECKS=$((CHECKS+1))

BOOT_TARGETS=()
[ -f "$ROOT/osiris-up" ] && BOOT_TARGETS+=("$ROOT/osiris-up")
while IFS= read -r f; do BOOT_TARGETS+=("$f"); done < <(find "$ROOT" -maxdepth 1 -name 'docker-compose*.yml' -type f)
while IFS= read -r f; do
  [ "$f" = "$SELF" ] && continue
  BOOT_TARGETS+=("$f")
done < <(find "$ROOT/bin" -type f 2>/dev/null)
# The service's own boot path: process startup may WIRE the store, but must
# never ISSUE.
[ -f "$ROOT/api/cmd/osiris-api/main.go" ] && BOOT_TARGETS+=("$ROOT/api/cmd/osiris-api/main.go")

# strip_comments blanks line comments while preserving line numbering, so a
# doc comment that merely NAMES the endpoint (main.go's wiring comment does,
# on purpose) is not mistaken for a call to it.
#
# The `://` carve-out matters: a naive `s|//.*||` would turn a real violation
# like `curl http://host/_api/machine-principals` into `curl http:` and hide
# it. Same discipline as bin/lint-health-invariant.sh's comment stripper, with
# that one extra case handled rather than assumed away.
strip_comments() {
  case "$1" in
    *.go)  sed -e 's|^[[:space:]]*//.*$||' -e 's|\([^:]\)//.*$|\1|' "$1" ;;
    *)     sed -e 's|^[[:space:]]*#.*$||' -e 's|[[:space:]]#.*$||' "$1" ;;
  esac
}

if [ "${#BOOT_TARGETS[@]}" -eq 0 ]; then
  fail "HI14①: no install/boot files found to check — refusing to pass vacuously"
else
  hits=""
  for f in "${BOOT_TARGETS[@]}"; do
    # "Creating a machine principal" has exactly three textual forms:
    #   - calling the issue endpoint      (/machine-principals with a POST)
    #   - calling the issue function      (.Issue( on the store)
    #   - carrying a minted token literal (osm_)
    h="$(strip_comments "$f" | grep -nE '/machine-principals|\.Issue\(|osm_')"
    if [ -n "$h" ]; then
      hits="$hits$(printf '%s\n' "$h" | sed "s|^|${f#"$ROOT"/}:|")"$'\n'
    fi
  done
  if [ -n "${hits// /}" ]; then
    fail "HI14①: the install/boot path creates or carries a machine principal — this is the"
    fail "       'bake it at install time' pattern that has recurred FOUR times in this project"
    fail "       (60 §19.5). A machine principal must only ever be created by a human calling"
    fail "       POST /api/machine-principals at runtime."
    printf '%s' "$hits" >&2
  else
    ok "HI14①: ${#BOOT_TARGETS[@]} install/boot file(s) checked, no issuance and no token literal"
  fi
fi

# ===========================================================================
# HI14 ② — the issue-time scope allowlist is org.read.* and nothing else.
#
#   §13: "발급 핸들러의 스코프 허용목록 상수가 org.read. 접두 외의 코드를
#   포함하면 실패." (= MP3, the capability ceiling.)
#
# Checked structurally rather than by listing the allowed codes here: the
# allowlist is DERIVED (authz.MachineGrantableCodes filters authz.Catalog
# through the prefix test), so the thing to protect is the derivation, not a
# duplicated list that could drift from it.
# ===========================================================================
say
say "HI14② the machine scope allowlist is org.read.* only, enforced at issue time"
CHECKS=$((CHECKS+1))

if [ ! -f "$AUTHZ_GO" ]; then
  fail "HI14②: $AUTHZ_GO not found"
else
  if ! grep -q 'ReadPrefix = "org.read."' "$AUTHZ_GO"; then
    fail "HI14②: authz.ReadPrefix is not the literal \"org.read.\" (60 §19.6 / MP3)"
  else
    ok "HI14②: authz.ReadPrefix == \"org.read.\""
  fi

  # MachineGrantable must test the prefix. Extract its body and require it.
  MG="$(awk '/^func MachineGrantable\(/,/^}/' "$AUTHZ_GO")"
  if [ -z "$MG" ]; then
    fail "HI14②: authz.MachineGrantable not found"
  elif ! printf '%s' "$MG" | grep -q 'strings.HasPrefix(string(c), ReadPrefix)'; then
    fail "HI14②: authz.MachineGrantable does not test the org.read. prefix — the capability"
    fail "        ceiling (MP3) would then be decided somewhere else, or nowhere"
  else
    ok "HI14②: authz.MachineGrantable tests the org.read. prefix"
  fi

  # The issue path must actually call it. Enforcing only at request time would
  # let a malformed record reach AUTHN_MACHINE.
  ISSUE="$(awk '/^func \(s \*Store\) Issue\(/,/^}/' "$MACHINE_DIR/store.go" 2>/dev/null)"
  if [ -z "$ISSUE" ]; then
    fail "HI14②: machineauth.Store.Issue not found"
  elif ! printf '%s' "$ISSUE" | grep -q 'authz.MachineGrantable('; then
    fail "HI14②: machineauth.Store.Issue does not call authz.MachineGrantable — the allowlist"
    fail "        must be applied AT ISSUE TIME, not only when the token is later presented"
  else
    ok "HI14②: machineauth.Store.Issue applies the allowlist before persisting"
  fi
fi

# ===========================================================================
# HI13 — "기계 주체는 세션이 되지 않는다" (§19.3, §3.1.1's 승격 row).
#
#   §13: "기계 주체 검증 함수는 Set-Cookie를 쓰는 패키지를 import하지 않는다."
# ===========================================================================
say
say "HI13  a machine principal can never become a session"
CHECKS=$((CHECKS+1))

if [ ! -d "$MACHINE_DIR" ]; then
  fail "HI13: $MACHINE_DIR not found"
else
  h="$(grep -rnE '"osiris-api/internal/session"|http\.SetCookie|Set-Cookie' "$MACHINE_DIR" 2>/dev/null)"
  if [ -n "$h" ]; then
    fail "HI13: internal/machineauth touches the session/cookie machinery:"
    printf '%s\n' "$h" >&2
  else
    ok "HI13: internal/machineauth imports no session package and sets no cookie"
  fi
fi

# ===========================================================================
# HI11 — ".env.example 신규 변수 0건" (§11.2, §13).
#
#   Nothing this design introduces may become an install-time environment
#   variable. The bucket name, TTLs, the rate limit and the token prefix are
#   all compile-time constants for exactly this reason.
# ===========================================================================
say
say "HI11  this design added zero new .env variables"
CHECKS=$((CHECKS+1))

ENV_EXAMPLE="$ROOT/.env.example"
if [ ! -f "$ENV_EXAMPLE" ]; then
  note "HI11: no .env.example — nothing to check"
else
  h="$(grep -nEi '^[[:space:]]*(AUTHN_|MACHINE_PRINCIPAL|OSIRIS_MACHINE|OSM_)' "$ENV_EXAMPLE" 2>/dev/null)"
  if [ -n "$h" ]; then
    fail "HI11: .env.example gained a variable from the machine-principal design:"
    printf '%s\n' "$h" >&2
  else
    ok "HI11: .env.example has no AUTHN_/MACHINE_PRINCIPAL/OSM_ variable"
  fi
fi

# ===========================================================================
# HI9 — "인증 저장소는 osiris-api만 접근한다" (§13).
#
#   Checked on BOTH the generator (orchestrator/internal/natsauth) and the
#   artifact it produces (nats/generated/authorization.conf), because the
#   artifact is regenerated on every reconcile tick and the generator is what
#   a future edit would change.
# ===========================================================================
say
say "HI9   only osiris-api is granted \$KV.AUTHN_* rights"
CHECKS=$((CHECKS+1))

# The generated conf is a `users: [ { user: "X", ... permissions: { publish:
# ... } } ]` list where a user's grants sit on LATER lines than its name, so
# a per-line test is wrong. Track the enclosing user instead.
CONF="$ROOT/nats/generated/authorization.conf"
if [ ! -f "$CONF" ]; then
  note "HI9: nats/generated/authorization.conf not present, skipped"
else
  offenders="$(awk '
    /\{[[:space:]]*user:[[:space:]]*"/ {
      match($0, /user:[[:space:]]*"[^"]*"/)
      cur = substr($0, RSTART, RLENGTH)
      sub(/user:[[:space:]]*"/, "", cur); sub(/"$/, "", cur)
    }
    /\$KV\.AUTHN_/ { if (cur != "osiris-api") print NR ": user=" cur }
  ' "$CONF")"
  grants="$(grep -c '\$KV\.AUTHN_' "$CONF")"
  if [ -n "$offenders" ]; then
    printf '%s\n' "$offenders" >&2
    fail "HI9: nats/generated/authorization.conf grants \$KV.AUTHN_* to a user other than osiris-api"
  elif [ "$grants" -eq 0 ]; then
    note "HI9: nats/generated/authorization.conf has no \$KV.AUTHN_ grant yet (osiris-orchestrator has not reconciled since the grant was added)"
  else
    ok "HI9: nats/generated/authorization.conf — all \$KV.AUTHN_ grants belong to osiris-api"
  fi
fi

# For the generator, verify the grant sits in the osiris-api writeUser call.
# Comments are stripped first: this file DOCUMENTS the invariant in prose and
# necessarily names the subject there (same situation lint-health-invariant.sh
# handles for agent/internal/health).
if [ -f "$NATSAUTH_GO" ]; then
  NA_CODE="$(strip_comments "$NATSAUTH_GO")"
  API_BLOCK="$(printf '%s\n' "$NA_CODE" | awk '/Username: "osiris-api"/,/^\t\t\}\)/')"
  total="$(printf '%s\n' "$NA_CODE" | grep -c '\$KV\.AUTHN_')"
  inblock="$(printf '%s\n' "$API_BLOCK" | grep -c '\$KV\.AUTHN_')"
  if [ "$total" -eq 0 ]; then
    fail "HI9: natsauth.go grants no \$KV.AUTHN_ rights at all — osiris-api cannot own AUTHN_MACHINE"
  elif [ "$total" != "$inblock" ]; then
    fail "HI9: natsauth.go has $total \$KV.AUTHN_ grants but only $inblock inside the osiris-api user block"
  else
    ok "HI9: natsauth.go — all $total \$KV.AUTHN_ grant(s) are inside the osiris-api user block"
  fi
fi

# ===========================================================================
# HI16 — "위임 판정이 단 하나의 함수에만 존재한다" (§21.3)
#
#   H31: "아래 다섯 규칙은 인가 판정 단일 함수(authz.CheckDelegation)에 있고,
#   역할 변경·계정 상태 변경·자격증명 조작(등록 티켓 발급/재발급)의 모든 경로가
#   그 함수를 지난다(HI16이 검사)."
#
# TWO HALVES, AND BOTH ARE NEEDED. Checking only "the function is called" would
# pass a build that grew a SECOND delegation function beside it; checking only
# "there is one function" would pass a handler that forgot to call it. §21.4-2's
# lesson is the reason the second half enumerates the deny/profile handlers too:
# leaving DENIES manipulation outside the rules bypasses DG1~DG4 through one edge.
# ===========================================================================
say
say "HI16  the delegation decision lives in exactly one function, and every C10 route reaches it"
CHECKS=$((CHECKS+1))

DELEG_GO="$ROOT/api/internal/authz/delegation.go"
PEOPLE_DIR="$ROOT/api/internal/people"

if [ ! -f "$DELEG_GO" ]; then
  fail "HI16: api/internal/authz/delegation.go is missing — the single delegation decision has no home"
else
  # (a) exactly one definition, and it is in delegation.go.
  defs="$(grep -rn '^func CheckDelegation(' --include='*.go' "$ROOT/api" | wc -l | tr -d ' ')"
  if [ "$defs" != "1" ]; then
    grep -rn '^func CheckDelegation(' --include='*.go' "$ROOT/api" >&2
    fail "HI16: expected exactly 1 definition of CheckDelegation, found $defs"
  else
    ok "HI16(a): CheckDelegation is defined exactly once"
  fi

  # (b) no parallel implementation. Any OTHER top-level func whose name mentions
  # delegation is the shape H31 forbids — a second opinion on the same question.
  rogue="$(grep -rn '^func .*[Dd]elegation' --include='*.go' "$ROOT/api" \
            | grep -v '/authz/delegation.go:' || true)"
  if [ -n "$rogue" ]; then
    printf '%s\n' "$rogue" >&2
    fail "HI16: a delegation-named function exists outside authz/delegation.go"
  else
    ok "HI16(b): no delegation function outside authz/delegation.go"
  fi

  # (c) every C10 action handler reaches the decision. In this codebase a
  # handler reaches it either directly or through Service.prepare, which is the
  # only other function permitted to call it.
  for fn in AssignRole RevokeRole Deactivate IssueTicket SetAdditiveProfile SetDeny; do
    body="$(awk -v f="func (s *Service) $fn(" 'index($0,f){p=1} p{print} p&&/^}$/{exit}' \
             "$PEOPLE_DIR/handlers.go")"
    if [ -z "$body" ]; then
      fail "HI16(c): handler $fn not found in internal/people/handlers.go"
    elif printf '%s\n' "$body" | grep -q 's\.prepare(\|authz\.CheckDelegation('; then
      ok "HI16(c): $fn reaches CheckDelegation"
    else
      fail "HI16(c): $fn changes a role/account/grant but never reaches CheckDelegation"
    fi
  done
fi

# ===========================================================================
# HI17 — "인가 경로에 역할·프로파일 이름/id 리터럴이 없다" (§21.3, H30)
#
#   §20.3: "인가 판정에 역할 id·title·프로파일 id를 쓰지 않는다. 오직
#   :Permission.code만 쓴다 ... 누가 role-org-manager의 title을 '관리자'로
#   바꾸거나, org-admin 프로파일의 id를 재사용하면 판정이 뒤집힌다."
#
# The seeded ids necessarily appear in core's catalog SEEDER — that file's job is
# to create them — so the check is scoped to osiris-api's authorization path,
# which is where a literal would become a decision. people/me.go's adminRoleID
# is the positive example: it finds the admin role BY GRANTS, not by id.
# ===========================================================================
say
say "HI17  no role/profile id literal in the authorization path"
CHECKS=$((CHECKS+1))

AUTHZ_PATH_DIRS="authz authn people orgstate machineauth masterkey"
hits=""
for d in $AUTHZ_PATH_DIRS; do
  [ -d "$ROOT/api/internal/$d" ] || continue
  for f in "$ROOT/api/internal/$d"/*.go; do
    [ -f "$f" ] || continue
    found="$(strip_comments "$f" | grep -n '"\(role-org-\|pp-org-\)' || true)"
    [ -n "$found" ] && hits="$hits
${f#$ROOT/}: $found"
  done
done
if [ -n "$hits" ]; then
  printf '%s\n' "$hits" >&2
  fail "HI17: a role/profile id literal appears in the authorization path — decide on :Permission.code instead (H30)"
else
  ok "HI17: no role-org-* / pp-org-* literal in api/internal/{$(echo $AUTHZ_PATH_DIRS | tr ' ' ',')}"
fi

# ===========================================================================
# HI18 — "DG3(자기 대상 금지)와 DG4(최소 1 관리자) 검사가 해당 핸들러에 존재한다"
#
# §21.3 marks HI18 "린트 + 통합테스트". This is the lint half: the two rules are
# PRESENT and wired to real inputs. It cannot prove they are correct — §21.2's
# four questions are behavioural claims — so it checks the three things a
# regression would break first: DG3 refuses, DG4 refuses on the credentialed
# set, and the handlers that can strip org.admin actually compute that fact.
# ===========================================================================
say
say "HI18  DG3 and DG4 are present and wired"
CHECKS=$((CHECKS+1))

if [ -f "$DELEG_GO" ]; then
  DG_CODE="$(strip_comments "$DELEG_GO")"
  printf '%s\n' "$DG_CODE" | grep -q 'refuse("DG3"' \
    && ok "HI18: DG3 (self-target) refusal present" \
    || fail "HI18: no DG3 refusal in delegation.go — self-promotion is unguarded"
  printf '%s\n' "$DG_CODE" | grep -q 'refuse("DG4"' \
    && ok "HI18: DG4 (last credentialed admin) refusal present" \
    || fail "HI18: no DG4 refusal in delegation.go — the organization can be locked out"
  printf '%s\n' "$DG_CODE" | grep -q 'CredentialedAdminIDs' \
    && ok "HI18: DG4 reads the credentialed-admin set (the API-layer half §21.1 requires)" \
    || fail "HI18: DG4 does not consult CredentialedAdminIDs — only the graph half of the rule exists"

  # The post-state input DG4 needs. A handler that can remove org.admin but
  # always reports RemovesTargetAdmin=false would silently disable DG4.
  if grep -q 'RemovesTargetAdmin' "$PEOPLE_DIR/people.go"; then
    ok "HI18: internal/people supplies RemovesTargetAdmin to the decision"
  else
    fail "HI18: internal/people never sets RemovesTargetAdmin — DG4 can never fire"
  fi
fi

# ===========================================================================
# H10 — the master key (§4.0/H22, §4.3, §4.5/H23)
#
# Not one of §13's HI numbers; these are the invariants THIS build introduced,
# and they are checked here because every one of them is a single line that a
# later edit could quietly drop.
# ===========================================================================
say
say "H10   the master key is prefix-routed, re-evaluated, and owned by one package"
CHECKS=$((CHECKS+1))

MK_GO="$ROOT/api/internal/masterkey/masterkey.go"
AUTHN_GO="$ROOT/api/internal/authn/authn.go"

if [ ! -f "$MK_GO" ]; then
  fail "H10: api/internal/masterkey/masterkey.go is missing"
else
  grep -q 'KeyPrefix = "osk_"' "$MK_GO" \
    && ok "H10(a): the master key prefix is osk_ (§19.3)" \
    || fail "H10(a): masterkey.KeyPrefix is not \"osk_\" — §19.3's prefix routing is broken"

  # Valid must require the prefix, or a legacy unprefixed file would still
  # authenticate and §4.0's migration would be cosmetic.
  awk '/^func \(g \*Guard\) Valid\(/,/^}/' "$MK_GO" | grep -q 'HasKeyPrefix' \
    && ok "H10(b): Valid requires the prefix, so a legacy setup_token cannot authenticate" \
    || fail "H10(b): Valid does not require the osk_ prefix"

  # Only this package may delete the key file. §4.5's two dimensions stay
  # independent only if the data dimension has exactly one owner.
  purgers="$(grep -rn 'os\.Remove' --include='*.go' "$ROOT/api" | grep -v '/masterkey/' || true)"
  if [ -n "$purgers" ]; then
    printf '%s\n' "$purgers" >&2
    fail "H10(c): a file removal outside internal/masterkey — the key file must have one owner"
  else
    ok "H10(c): only internal/masterkey removes files"
  fi
fi

if [ -f "$AUTHN_GO" ]; then
  AUTHN_CODE="$(strip_comments "$AUTHN_GO")"

  # The retired JSON field. §4.0 renames the body key; an alias would keep the
  # misleading name alive at the one place a human types it.
  legacy="$(printf '%s\n' "$AUTHN_CODE" | grep -n 'setupToken' || true)"
  if [ -n "$legacy" ]; then
    printf '%s\n' "$legacy" >&2
    fail "H10(d): authn.go still references a setupToken field — §4.0 replaced it with masterKey"
  else
    ok "H10(d): the setupToken request field is gone (§4.0)"
  fi

  # EVERY acceptance point re-evaluates H10, checked PER FUNCTION.
  #
  # A COUNTING VERSION OF THIS CHECK WAS WRITTEN FIRST AND FAILED ITS OWN FAULT
  # INJECTION. It compared "how many operator principals are constructed" against
  # "how many times the predicate is consulted", and because the predicate is
  # consulted more often than principals are built, deleting one real check still
  # left the totals passing. Totals cannot express "each of these, individually".
  #
  # So: find every function that constructs an operator principal, and require
  # that same function to consult the predicate. This is the invariant that
  # matters — a master-key credential must never be turned into a principal by
  # code that did not just ask whether the window is still open.
  offenders="$(printf '%s\n' "$AUTHN_CODE" | awk '
    /^func / { name = $0; body = "" }
    { body = body "\n" $0 }
    /^}$/ {
      if (name != "" && index(body, "OperatorScope()") > 0 && index(body, "masterKeyUsable(") == 0) {
        sub(/ \{$/, "", name); print name
      }
      name = ""; body = ""
    }
  ')"
  built="$(printf '%s\n' "$AUTHN_CODE" | grep -c 'OperatorScope()')"
  if [ "$built" -eq 0 ]; then
    fail "H10(e): authn.go constructs no operator principal — the master-key branch is gone"
  elif [ -n "$offenders" ]; then
    printf '%s\n' "$offenders" >&2
    fail "H10(e): the function(s) above build a master-key principal without consulting masterKeyUsable — an acceptance point skips H10"
  else
    ok "H10(e): every function that builds a master-key principal consults masterKeyUsable first"
  fi

  # The predicate itself must NOT be reimplemented here (§4.3's four sections,
  # one predicate — orgstate owns it).
  if printf '%s\n' "$AUTHN_CODE" | grep -q 'CurrentOrgRevision\|CountHoldersWithCredential'; then
    fail "H10(f): authn.go computes the H10 conditions itself — orgstate owns that predicate"
  else
    ok "H10(f): authn.go asks orgstate for the predicate instead of recomputing it"
  fi
fi

# ===========================================================================
# AUD — every security event subject is grantable
#
# See this file's header for why this exists: a subject published without a
# matching NATS grant does not lose an event, it stalls the request, because the
# publish waits for a JetStream ack that a permissions violation never sends.
# ===========================================================================
say
say "AUD   every secevents subject prefix is granted in the NATS ACL generator"
CHECKS=$((CHECKS+1))

SECEV_GO="$ROOT/api/internal/secevents/secevents.go"
if [ ! -f "$SECEV_GO" ] || [ ! -f "$NATSAUTH_GO" ]; then
  note "AUD: secevents.go or natsauth.go not present, skipped"
else
  # Prefix constants are declared as `XPrefix = "security.<area>."`.
  areas="$(strip_comments "$SECEV_GO" \
            | grep -o '"security\.[a-z]*\.' \
            | sed -e 's|^"||' -e 's|\.$||' | sort -u)"
  if [ -z "$areas" ]; then
    fail "AUD: no security.* subject prefix found in secevents.go — refusing to pass vacuously"
  else
    NA_CODE2="$(strip_comments "$NATSAUTH_GO")"
    for a in $areas; do
      if printf '%s\n' "$NA_CODE2" | grep -q "\"$a\.>\""; then
        ok "AUD: $a.> is granted"
      else
        fail "AUD: secevents publishes on $a.* but natsauth grants no \"$a.>\" — the publish will stall on a permissions violation"
      fi
    done
  fi
fi

# ===========================================================================
# GEN — the genesis principal's scope is an allowlist, and `commit` is not in it
#
#   10-core-architecture.md §8.5.1 (F14): "core의 genesis 스코프 검사는
#   허용목록(allowlist)이어야 하며 commit은 그 목록에 없다. 금지 목록이 아니라
#   허용 목록인 이유는 새 엔드포인트가 추가될 때 기본이 거부여야 하기 때문이다.
#   이 성질은 린트로 강제한다."
#
# THIS IS THE ONLY MECHANICAL GUARD ON F14. Everything else about that amendment
# is prose in two design documents; this check is what makes it survive an edit.
# ===========================================================================
say
say "GEN   the genesis scope is an allowlist without commit"
CHECKS=$((CHECKS+1))

GEN_GO="$ROOT/core/internal/genesis/genesis.go"
CORE_SERVER_GO="$ROOT/core/internal/api/server.go"

if [ ! -f "$GEN_GO" ]; then
  note "GEN: core/internal/genesis/genesis.go not present, skipped"
else
  GEN_CODE="$(strip_comments "$GEN_GO")"

  # An ENTRY is a line carrying both a Method and a Path. Matching the entry
  # SHAPE rather than an awk line range is deliberate: the first version of this
  # check used /AllowedRoutes = \[\]Route\{/,/^\}/ and FAILED ITS OWN FAULT
  # INJECTION — collapsing the literal to one line (`[]Route{}`) left the range
  # with no terminator, so it swallowed the rest of the file and counted `Path:`
  # from the struct definition. A range that can silently run to EOF is the wrong
  # tool for asserting a list is non-empty.
  ENTRIES="$(printf '%s\n' "$GEN_CODE" | grep -E 'Method:[[:space:]]*http\.Method[A-Za-z]+,[[:space:]]*Path:[[:space:]]*"' || true)"
  entry_count="$(printf '%s' "$ENTRIES" | grep -c 'Path:' || true)"
  [ -z "$ENTRIES" ] && entry_count=0

  # (a) the list exists and is non-empty — refuse to pass vacuously.
  if [ "$entry_count" -lt 1 ]; then
    fail "GEN(a): genesis.AllowedRoutes is empty or unparsable — refusing to pass a check with no subject"
  else
    ok "GEN(a): genesis.AllowedRoutes declares $entry_count route(s)"
  fi

  # (b) ★ commit is not granted. Checked against the ENTRIES only — the package
  # doc necessarily names the endpoint it excludes.
  if printf '%s\n' "$ENTRIES" | grep -q 'commit'; then
    printf '%s\n' "$ENTRIES" | grep -n 'commit' >&2
    fail "GEN(b): the genesis allowlist contains a commit route — F14 removed it (10 §8.5.1, 30 §3.4)"
  else
    ok "GEN(b): no commit route in the genesis allowlist"
  fi

  # (c) default deny. Allows() must fall through to false; a version that
  # returned true on no match would make the list decorative.
  allows_body="$(printf '%s\n' "$GEN_CODE" | awk '/^func Allows\(/,/^\}/')"
  if [ -z "$allows_body" ]; then
    fail "GEN(c): genesis.Allows not found"
  elif printf '%s\n' "$allows_body" | tail -3 | grep -q 'return false'; then
    ok "GEN(c): Allows falls through to deny"
  else
    printf '%s\n' "$allows_body" >&2
    fail "GEN(c): genesis.Allows does not end in 'return false' — the allowlist would not be default-deny"
  fi

  # (d) no graph write is reachable from the genesis surface. core's write
  # operations all live on graphdb methods that the onboarding handlers must not
  # call; the draft validator is a pure function over a catalog snapshot.
  if [ -f "$ROOT/core/internal/onboarding/draft.go" ]; then
    if strip_comments "$ROOT/core/internal/onboarding/draft.go" | grep -qE 'MERGE|CREATE |SET |neo4j\.|NewSession'; then
      fail "GEN(d): internal/onboarding touches the graph — the draft path must write nothing (O2, §8.5.1)"
    else
      ok "GEN(d): internal/onboarding contains no graph access at all"
    fi
  fi
fi

# (e) the router must not expose a genesis-authenticated commit route, and the
# three allowlisted paths must actually be registered with genesisAuth. A path
# in the list with no route is a scope grant for something that does not exist;
# a genesisAuth route absent from the list is caught at runtime by (c) but is
# worth failing here, where it is cheap to notice.
if [ -f "$CORE_SERVER_GO" ]; then
  CS_CODE="$(strip_comments "$CORE_SERVER_GO")"
  if printf '%s\n' "$CS_CODE" | grep -q 'onboarding/commit'; then
    printf '%s\n' "$CS_CODE" | grep -n 'onboarding/commit' >&2
    fail "GEN(e): core's router registers an onboarding/commit route — F14 says the commit is called by a human's session through the ordinary write routes"
  else
    ok "GEN(e): core's router has no onboarding/commit route"
  fi

  for p in /v1/onboarding/draft /v1/onboarding/status /v1/onboarding/catalog; do
    if printf '%s\n' "$CS_CODE" | grep -q "\"$p\", s.genesisAuth("; then
      ok "GEN(e): $p is registered with genesisAuth"
    else
      fail "GEN(e): $p is in the allowlist but not registered with genesisAuth"
    fi
  done

  # ===========================================================================
  # GEN(f) — [2026-07-28 K7 REWRITTEN]. THE ORIGINAL CHECK OUTLIVED ITS SUBJECT.
  #
  # The version this replaces asserted that /v1/onboarding/genesis-token and
  # .../revoke were "gated by s.auth (the platform token)", on the reasoning that
  # "a genesis subject that could issue its own credential would have no closure
  # condition at all". That reasoning was correct while OSiris MINTED the genesis
  # token (§8.5, the compose build).
  #
  # K7 removed minting entirely: K8s issues the token, osiris-core verifies it
  # with TokenReview, and OSiris creates zero secrets. There is no issuance
  # instant left to gate. But the old check still demanded those two routes
  # EXIST — so the K7 implementation had to keep them as 410 Gone stubs purely to
  # satisfy a lint. A LINT THAT PINS A CORPSE IN PLACE IS WORSE THAN NO LINT: it
  # converts "we deleted a mechanism" into "we cannot delete a mechanism".
  #
  # The invariant worth checking under K7 is the inverse of the old one — not
  # "issuance is properly gated" but "issuance does not exist":
  #
  #   f1  the genesis package MINTS NOTHING (no Issue, no token prefix, no Store)
  #   f2  verification goes through a TokenReview seam
  #   f3  the identity check exists — a valid token for the WRONG ServiceAccount
  #       must be refused, which is K7's entire authorization boundary
  #
  # f3 is the one that matters most. Without it, K7 would degrade from "the
  # bootstrap-master Pod may onboard" to "any authenticated Pod in the cluster may
  # onboard", which is strictly worse than what §8.5 had.
  # ===========================================================================
  GEN_PKG="$ROOT/core/internal/genesis"
  if [ ! -d "$GEN_PKG" ]; then
    note "GEN(f): core/internal/genesis not present, skipped"
  else
    GEN_ALL="$(for f in "$GEN_PKG"/*.go; do [ -f "$f" ] && [ "${f%_test.go}" = "$f" ] && strip_comments "$f"; done)"

    # f1 — no minting. These three names ARE the minting mechanism §8.5 had.
    minted="$(printf '%s\n' "$GEN_ALL" | grep -nE 'func .*\) Issue\(|TokenPrefix|type Store struct' || true)"
    if [ -n "$minted" ]; then
      printf '%s\n' "$minted" >&2
      fail "GEN(f1): the genesis package still mints tokens — K7 says K8s issues them and OSiris creates zero secrets"
    else
      ok "GEN(f1): the genesis package mints nothing (no Issue / TokenPrefix / Store)"
    fi

    # f2 — verification goes through a reviewable seam.
    if printf '%s\n' "$GEN_ALL" | grep -q 'TokenReview\|TokenReviewer'; then
      ok "GEN(f2): verification goes through a TokenReview seam"
    else
      fail "GEN(f2): no TokenReview seam in the genesis package — K7 requires K8s to be the identity authority"
    fi

    # f3 — ★ the authorization boundary. Both halves of the identity must be
    # compared: namespace AND service account. Comparing only the name would let
    # a same-named SA in another namespace through.
    if printf '%s\n' "$GEN_ALL" | grep -q 'ErrWrongIdentity'; then
      ok "GEN(f3): a wrong-identity refusal exists"
    else
      fail "GEN(f3): no wrong-identity refusal — a valid token for ANY ServiceAccount would be accepted"
    fi
    if printf '%s\n' "$GEN_ALL" | grep -qE 'Namespace *!=|!= .*\.Namespace' && \
       printf '%s\n' "$GEN_ALL" | grep -qE 'ServiceAccount *!=|!= .*\.ServiceAccount'; then
      ok "GEN(f3): both namespace and service account are compared"
    else
      fail "GEN(f3): the identity check does not compare BOTH namespace and service account"
    fi

    # The vestigial routes must NOT be resurrected.
    if printf '%s\n' "$CS_CODE" | grep -q 'onboarding/genesis-token'; then
      printf '%s\n' "$CS_CODE" | grep -n 'onboarding/genesis-token' >&2
      fail "GEN(f4): core still registers a genesis-token issue/revoke route — K7 removed minting, so the route has nothing to do"
    else
      ok "GEN(f4): no vestigial genesis-token issue/revoke route"
    fi
  fi
fi

# ===========================================================================
say
say "--- lint-identity-authn: $CHECKS invariant group(s) checked ---"
if [ "$FAIL" -eq 0 ]; then
  echo "PASS: HI9, HI11, HI13, HI14①, HI14②, HI15, HI16, HI17, HI18, H10, AUD, GEN hold"
else
  echo "FAIL: see above. Spec: docs/design/60-identity-authn.md §13, §19, §21." >&2
fi
exit $FAIL
