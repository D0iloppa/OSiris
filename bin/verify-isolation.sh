#!/usr/bin/env bash
# bin/verify-isolation.sh — 격리(§2.6 DMZ 패턴, §8.3)가 실제로 성립하는지
# 확인한다. docker-compose가 떠 있는 상태에서 실행한다.
#
# I5 2차 수정(이식성): dev-net은 이 프레임워크의 기본 설치에 없을 수 있다
# (docker-compose.dev-net.yml은 선택적 오버레이). dev-net이 없는 호스트에서는
# 그것에 의존하는 검사(아래 B, C1의 절반)를 **명시적으로 SKIP**하고 나머지는
# 그대로 수행한다 — 조용히 통과시키지 않는다.
#
# 검증 항목:
#   A. osiris-agents 네트워크에만 붙은 임시 컨테이너에서
#      - Neo4j(osiris-data 전용) 도달 불가
#      - dev-net 대상(상위 게이트웨이, 있다면) 도달 불가
#      - nats(이중 홈: osiris-data+osiris-agents) 도달 가능
#      - osiris-egress(osiris-agents 멤버)의 프록시 포트 자체는 TCP로 도달
#        가능하지만, 허용목록 밖 도메인은 CONNECT가 거부되어야 함
#      - osiris-egress를 통해 허용목록 도메인(github.com)은 CONNECT 성립
#   B. dev-net에만 붙은(= osiris 소유가 아닌 척하는) 임시 컨테이너에서
#      - osiris-egress를 프록시로 쓰려 해도 거부되어야 함(I5 2차 수정 이후
#        osiris-egress가 dev-net 멤버가 아니게 되어 애초에 라우팅이 없다 —
#        A10 핀홀 자체가 osiris-egress 쪽에서는 소멸했다. dev-net 오버레이가
#        붙는 것은 이제 osiris-gw 하나뿐)
#
# 실패 시 exit 1.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [ -f "$ROOT/.env" ]; then
  # shellcheck disable=SC1091
  set -a; source "$ROOT/.env"; set +a
fi

IMG="curlimages/curl:8.10.1"
AGENT_CT="osiris-verify-agents"
DEVNET_CT="osiris-verify-devnet"

# compose는 네트워크 이름을 <project>_<network>로 만든다 (COMPOSE_PROJECT_NAME,
# .env.example에서 "osiris"로 고정해 둠). 하드코딩하지 않고 실제로 존재하는
# 네트워크를 찾아 쓴다 — 프로젝트 이름이 바뀌어도 이 스크립트는 그대로 동작한다.
find_net() {
  local suffix="$1"
  docker network ls --format '{{.Name}}' | grep -E "(^|_)${suffix}\$" | head -1
}
NET_AGENTS="$(find_net osiris-agents)"
NET_DEV="${DEV_NET_NAME:-dev-net}"
[ -n "$NET_AGENTS" ] || { echo "ERROR: osiris-agents 네트워크를 찾지 못했습니다 (docker compose up -d를 먼저 실행하세요)" >&2; exit 1; }

# I5 2차 수정(이식성): 외부 네트워크(dev-net류)는 기본 설치에 없을 수 있다 —
# 그 경우 dev-net에 의존하는 검사(B1, 합성 dev-net 소스 컨테이너를 쓰는 C1의
# 절반)는 "통과"가 아니라 명시적으로 건너뛴다. 조용히 넘어가지 않는다.
if docker network inspect "$NET_DEV" >/dev/null 2>&1; then
  DEV_NET_AVAILABLE=1
else
  DEV_NET_AVAILABLE=0
fi
echo "네트워크: agents=$NET_AGENTS dev-net=$NET_DEV (available=$DEV_NET_AVAILABLE)"

fail=0
pass_count=0
fail_count=0

cleanup() {
  docker rm -f "$AGENT_CT" "$DEVNET_CT" >/dev/null 2>&1 || true
}
trap cleanup EXIT

report() {
  local desc="$1" expect="$2" got="$3"
  if [ "$expect" = "$got" ]; then
    echo "PASS: $desc"
    pass_count=$((pass_count + 1))
  else
    echo "FAIL: $desc (expected=$expect got=$got)"
    fail_count=$((fail_count + 1))
    fail=1
  fi
}

# curl exit codes that mean "TCP-level unreachable" (no route / refused / timeout / DNS fail)
is_unreachable() {
  case "$1" in
    6|7|28|56|35) return 0 ;;   # could not resolve / connect / timeout / conn reset / SSL connect
    *) return 1 ;;
  esac
}

echo "=== OSiris isolation verification ==="
docker rm -f "$AGENT_CT" "$DEVNET_CT" >/dev/null 2>&1 || true

docker run -d --rm --name "$AGENT_CT" --network "$NET_AGENTS" "$IMG" sleep 300 >/dev/null
if [ "$DEV_NET_AVAILABLE" = "1" ]; then
  docker run -d --rm --name "$DEVNET_CT" --network "$NET_DEV" "$IMG" sleep 300 >/dev/null
else
  echo "SKIP: dev-net('$NET_DEV') 네트워크가 없습니다 — 이 호스트는 표준(이식형) 설치입니다. dev-net 소스 검사(B1) 및 그 절반의 C1을 건너뜁니다."
fi

# --- A1: agents-only -> Neo4j (osiris-data only) : must be UNREACHABLE ---
docker exec "$AGENT_CT" curl -s -o /dev/null --max-time 4 http://neo4j:7474/ >/dev/null 2>&1
rc=$?
if is_unreachable "$rc"; then got=unreachable; else got=reachable; fi
report "osiris-agents -> neo4j:7474 (no route expected)" "unreachable" "$got"

# --- A2: agents-only -> upstream reverse proxy on dev-net IP : must be UNREACHABLE ---
# UPSTREAM_PROXY_CONTAINER: dev-net 위 상위 리버스 프록시 컨테이너 이름(없으면 SKIP)
UPSTREAM_PROXY_IP="$([ -n "${UPSTREAM_PROXY_CONTAINER:-}" ] && docker inspect "$UPSTREAM_PROXY_CONTAINER" --format '{{(index .NetworkSettings.Networks "'"${DEV_NET_NAME:-dev-net}"'").IPAddress}}' 2>/dev/null || true)"
if [ -n "${UPSTREAM_PROXY_IP:-}" ]; then
  docker exec "$AGENT_CT" curl -s -o /dev/null --max-time 4 "http://${UPSTREAM_PROXY_IP}:80/" >/dev/null 2>&1
  rc=$?
  if is_unreachable "$rc"; then got=unreachable; else got=reachable; fi
  report "osiris-agents -> upstream-proxy(${UPSTREAM_PROXY_IP}) dev-net (no route expected)" "unreachable" "$got"
else
  echo "SKIP: upstream proxy dev-net IP not found (UPSTREAM_PROXY_CONTAINER unset or not running?)"
fi

# --- A3: agents-only -> nats (dual-homed) : must be REACHABLE ---
docker exec "$AGENT_CT" curl -s -o /dev/null --max-time 4 http://nats:8222/healthz >/dev/null 2>&1
rc=$?
if is_unreachable "$rc"; then got=unreachable; else got=reachable; fi
report "osiris-agents -> nats:8222/healthz (reachable expected, dual-homed)" "reachable" "$got"

# --- A4: agents-only -> osiris-egress:3128 TCP reachable (it's on this network) ---
docker exec "$AGENT_CT" curl -s -o /dev/null --max-time 4 http://osiris-egress:3128/ >/dev/null 2>&1
rc=$?
if is_unreachable "$rc"; then got=unreachable; else got=reachable; fi
report "osiris-agents -> osiris-egress:3128 TCP (reachable expected)" "reachable" "$got"

# CONNECT를 통해 프록시가 허용/거부하는지 판정한다. Squid가 CONNECT 자체를
# 403으로 거부하면 curl은 터널을 못 열어 실제 %{http_code}는 "000"(목적지 응답
# 없음)이 되고 curl 자신은 실패 종료한다(exitcode!=0, 보통 56) — 이것이
# "거부"의 신호다. 터널이 서면 실제 목적지의 HTTP 응답 코드(2xx/3xx/…)가 잡히고
# curl은 exitcode=0으로 끝난다 — 이것이 "허용"의 신호다.
via_egress() {
  local ct="$1" url="$2"
  docker exec "$ct" curl -s -o /dev/null -w '%{http_code} %{exitcode}' --max-time 6 \
    -x http://osiris-egress:3128 "$url" 2>/dev/null
}
judge_egress() {
  local out="$1" code ec
  code="$(echo "$out" | awk '{print $1}')"
  ec="$(echo "$out" | awk '{print $2}')"
  if [ "$ec" = "0" ] && [ "$code" != "000" ]; then
    echo allowed
  else
    echo denied
  fi
}

# --- A5: agents-only, via egress proxy, to ALLOWlisted domain -> must be ALLOWED ---
OUT="$(via_egress "$AGENT_CT" https://github.com/)"
report "osiris-agents --[egress]--> github.com (allowlisted, raw='$OUT')" "allowed" "$(judge_egress "$OUT")"

# --- A6: agents-only, via egress proxy, to NON-allowlisted domain -> must be DENIED ---
OUT="$(via_egress "$AGENT_CT" https://example.com/)"
report "osiris-agents --[egress]--> example.com (NOT allowlisted, raw='$OUT')" "denied" "$(judge_egress "$OUT")"

# --- B1: dev-net container abusing egress as an open proxy -> must be DENIED regardless of domain ---
# I5 2차 수정: osiris-egress는 이제 dev-net 멤버가 아니다(자체 osiris-outbound
# 브리지로 인터넷에 나간다) — dev-net 컨테이너는 osiris-egress를 이름으로
# 찾을 라우팅 자체가 없어(더 강한 격리) "denied"가 나온다. 여전히 squid ACL
# (agents_net만 허용)도 별도 방어선으로 남아 있다.
if [ "$DEV_NET_AVAILABLE" = "1" ]; then
  OUT="$(via_egress "$DEVNET_CT" https://github.com/)"
  report "dev-net --[egress]--> github.com (no route + source ACL, raw='$OUT')" "denied" "$(judge_egress "$OUT")"
else
  echo "SKIP: dev-net 없음 — B1 건너뜀"
fi

# ---------------------------------------------------------------------------
# A7/A8 (I5): osiris-omniroute — reachable ONLY via the egress proxy, never
# directly. osiris-omniroute sits on osiris-llm, a network osiris-agents is
# NOT a member of, so this must hold with no new hole in §8.3's three-name
# reachable set (nats/osiris-core:8090/osiris-egress:3128 — osiris-omniroute
# is not a fourth name agents can reach directly).
# ---------------------------------------------------------------------------
OMNIROUTE_CT_NAME="$(docker ps --format '{{.Names}}' | grep -E '(^|_)osiris-omniroute$' | head -1)"
if [ -n "$OMNIROUTE_CT_NAME" ]; then
  # A7: agents-only, via egress proxy, to osiris-omniroute:20128 -> must be ALLOWED
  OUT="$(via_egress "$AGENT_CT" http://osiris-omniroute:20128/omni/v1/models)"
  report "osiris-agents --[egress]--> osiris-omniroute:20128/v1/models (allowlisted, raw='$OUT')" "allowed" "$(judge_egress "$OUT")"

  # A8: agents-only, DIRECT (no proxy) to osiris-omniroute:20128 -> must be UNREACHABLE
  # (proves the agent network contract is unchanged — no new direct pinhole)
  docker exec "$AGENT_CT" curl -s -o /dev/null --max-time 4 http://osiris-omniroute:20128/omni/v1/models >/dev/null 2>&1
  rc=$?
  if is_unreachable "$rc"; then got=unreachable; else got=reachable; fi
  report "osiris-agents -> osiris-omniroute:20128 DIRECT, no proxy (no route expected — §8.3 unchanged)" "unreachable" "$got"
else
  echo "SKIP: osiris-omniroute container not found (not deployed yet?)"
fi

# ---------------------------------------------------------------------------
# C. osiris-core (2단계 워커가 구현) — 네트워크 배치 검증.
#
# NOTE ON INTENT: docs/design/10-core-architecture.md §2.6의 네트워크 배치표와
# §8.3("도달 가능한 것은 nats, osiris-core:8090, osiris-egress:3128 셋뿐")은
# osiris-core가 osiris-data **와** osiris-agents 양쪽에 이중 홈으로 붙어,
# 에이전트가 :8090(그래프-쓰기 API, 토큰 인증)에 **도달 가능**해야 한다고
# 명시한다 — 이것이 "에이전트 대면 포트"의 정의 그 자체다. 그래서 아래는
# "osiris-core가 agents 망에서 전면 도달 불가"를 검증하지 않는다(설계와
# 모순되므로). 대신 실제로 지켜야 하는 경계 세 가지를 검증한다:
#   C1. osiris-core는 dev-net에는 전혀 발을 걸치지 않는다 (dev-net에서 도달 불가)
#   C2. osiris-agents에서 osiris-core:8090 자체는 도달 가능하다(설계대로)
#   C3. 그러나 토큰 없이는 그 API가 아무 데이터도 내주지 않는다(401) —
#       "도달 가능 = 무엇이든 쓸 수 있다"가 아님을 확인한다
#   C4. Neo4j는 여전히 osiris-agents에서 도달 불가하다(A5의 실제 보루 —
#       에이전트가 core를 우회해 그래프에 직접 쓸 방법이 없다는 것)
# ---------------------------------------------------------------------------
CORE_CT_NAME="$(docker ps --format '{{.Names}}' | grep -E '(^|_)osiris-core$' | head -1)"
if [ -n "$CORE_CT_NAME" ]; then
  # C1: dev-net -> osiris-core : must be UNREACHABLE (core has no dev-net interface at all)
  if [ "$DEV_NET_AVAILABLE" = "1" ]; then
    docker exec "$DEVNET_CT" curl -s -o /dev/null --max-time 4 http://osiris-core:8090/v1/revision >/dev/null 2>&1
    rc=$?
    if is_unreachable "$rc"; then got=unreachable; else got=reachable; fi
    report "dev-net -> osiris-core (no interface on dev-net, no route expected)" "unreachable" "$got"
  else
    echo "SKIP: dev-net 없음 — C1 건너뜀"
  fi

  # C2: osiris-agents -> osiris-core:8090 : must be REACHABLE (agent-facing graph-write API, by design)
  docker exec "$AGENT_CT" curl -s -o /dev/null --max-time 4 http://osiris-core:8090/v1/revision >/dev/null 2>&1
  rc=$?
  if is_unreachable "$rc"; then got=unreachable; else got=reachable; fi
  report "osiris-agents -> osiris-core:8090 (reachable expected, agent-facing API by design §8.3)" "reachable" "$got"

  # C3: same call, no bearer token -> must be 401 (reachable != authorized)
  CODE="$(docker exec "$AGENT_CT" curl -s -o /dev/null -w '%{http_code}' --max-time 4 http://osiris-core:8090/v1/revision 2>/dev/null || echo "000")"
  report "osiris-agents -> osiris-core:8090/v1/revision without token (401 expected, raw=$CODE)" "401" "$CODE"

  # C4: osiris-agents -> neo4j : must remain UNREACHABLE (the real A5 backstop —
  # agents cannot bypass core's validation by writing to Neo4j directly, even
  # though they can reach core's own API surface)
  docker exec "$AGENT_CT" curl -s -o /dev/null --max-time 4 http://neo4j:7474/ >/dev/null 2>&1
  rc=$?
  if is_unreachable "$rc"; then got=unreachable; else got=reachable; fi
  report "osiris-agents -> neo4j:7474 (still no route expected — core is the only writer, A5)" "unreachable" "$got"
else
  echo "SKIP: osiris-core container not found (not deployed yet?)"
fi

# ---------------------------------------------------------------------------
# D. 실제 에이전트 컨테이너(osiris-orchestrator가 생성한 것) — 합성
# 테스트 컨테이너(A절)가 아니라 진짜 에이전트 워크로드 자체의 네트워크
# 네임스페이스를 그대로 공유해(`--network container:<id>`) 검증한다. 이것이
# A절보다 더 엄밀하다: 에이전트 이미지에 셸이 있더라도 `docker exec`로
# 셸을 띄울 수 없으므로, 같은 netns를 공유하는 별도 컨테이너로 "이 에이전트
# 컨테이너가 실제로 무엇에 도달할 수 있는가"를 그대로 관찰한다.
#
# 완료조건 7: Neo4j·orchestrator·dev-net 도달 실패, nats·osiris-core:8090·
# osiris-egress:3128 만 도달 성공.
# ---------------------------------------------------------------------------
AGENT_REAL_ID="$(docker ps -q --filter "label=osiris.dev/managed-by=osiris-orchestrator" | head -1)"
if [ -n "$AGENT_REAL_ID" ]; then
  AGENT_REAL_NAME="$(docker inspect "$AGENT_REAL_ID" --format '{{.Name}}' | sed 's#^/##')"
  echo "실제 에이전트 컨테이너로 검증: $AGENT_REAL_NAME ($AGENT_REAL_ID)"

  netcheck() {
    local desc="$1" url="$2" expect="$3"
    docker run --rm --network "container:$AGENT_REAL_ID" "$IMG" \
      curl -s -o /dev/null --max-time 4 "$url" >/dev/null 2>&1
    local rc=$?
    if is_unreachable "$rc"; then got=unreachable; else got=reachable; fi
    report "$desc" "$expect" "$got"
  }

  # D1: neo4j — must be UNREACHABLE (§8.3: "Neo4j·orchestrator·dev-net에는 라우팅이 없다")
  netcheck "[실agent] -> neo4j:7474 (no route expected)" "http://neo4j:7474/" "unreachable"

  # D2: osiris-orchestrator — must be UNREACHABLE (orchestrator는 osiris-data에만 있음, §2.6)
  netcheck "[실agent] -> osiris-orchestrator:8081 (no route expected)" "http://osiris-orchestrator:8081/livez" "unreachable"

  # D3: dev-net (upstream reverse proxy) — must be UNREACHABLE
  if [ -n "${UPSTREAM_PROXY_IP:-}" ]; then
    docker run --rm --network "container:$AGENT_REAL_ID" "$IMG" \
      curl -s -o /dev/null --max-time 4 "http://${UPSTREAM_PROXY_IP}:80/" >/dev/null 2>&1
    rc=$?
    if is_unreachable "$rc"; then got=unreachable; else got=reachable; fi
    report "[실agent] -> upstream-proxy(${UPSTREAM_PROXY_IP}) dev-net (no route expected)" "unreachable" "$got"
  else
    echo "SKIP: upstream proxy dev-net IP not found"
  fi

  # D4: nats:4222/8222 — must be REACHABLE
  netcheck "[실agent] -> nats:8222/healthz (reachable expected)" "http://nats:8222/healthz" "reachable"

  # D5: osiris-core:8090 — must be REACHABLE (agent-facing graph-write API)
  netcheck "[실agent] -> osiris-core:8090 (reachable expected)" "http://osiris-core:8090/v1/revision" "reachable"

  # D6: osiris-egress:3128 — must be REACHABLE (TCP; allowlist/deny logic already covered by A5/A6)
  netcheck "[실agent] -> osiris-egress:3128 (reachable expected)" "http://osiris-egress:3128/" "reachable"

  # D7 (I5): osiris-omniroute DIRECT (no proxy) — must be UNREACHABLE. This is
  # THE completion-condition-4 check: the real agent workload's netns must not
  # gain a fourth directly-reachable name beyond nats/osiris-core:8090/
  # osiris-egress:3128 just because osiris-omniroute exists.
  netcheck "[실agent] -> osiris-omniroute:20128 DIRECT, no proxy (no route expected — §8.3 unchanged)" "http://osiris-omniroute:20128/omni/v1/models" "unreachable"

  # D8 (I5): osiris-omniroute via the egress proxy — must be ALLOWED (this is
  # completion-condition-3: the real agent reaches the new gateway only
  # through osiris-egress, exactly like the LLM path always worked).
  D8_OUT="$(docker run --rm --network "container:$AGENT_REAL_ID" "$IMG" \
    curl -s -o /dev/null -w '%{http_code} %{exitcode}' --max-time 6 \
    -x http://osiris-egress:3128 http://osiris-omniroute:20128/omni/v1/models 2>/dev/null)"
  report "[실agent] --[egress]--> osiris-omniroute:20128/v1/models (allowlisted, raw='$D8_OUT')" "allowed" "$(judge_egress "$D8_OUT")"
else
  echo "SKIP: 실행 중인 실제 에이전트 컨테이너가 없습니다 (osiris.dev/managed-by=osiris-orchestrator 라벨 없음) — osiris-orchestrator가 최소 1개 에이전트를 띄운 상태에서 재실행하세요"
fi

# ---------------------------------------------------------------------------
# E (osiris-api, 6단계 워커): osiris-api는 osiris-edge + osiris-data에만
# 붙는다 — osiris-agents 멤버가 아니므로 에이전트가 도달할 수 있는 이름이
# 하나도 늘지 않는다(§8.3의 "nats·osiris-core:8090·osiris-egress:3128
# 셋뿐"은 osiris-api 도입 이후에도 문자 그대로 유지되어야 한다). 또한
# osiris-api 컨테이너 자체에 docker.sock이 마운트되어 있지 않아야 한다
# (D4/G8 — 컨테이너 생성 권한은 osiris-orchestrator에만).
# ---------------------------------------------------------------------------
API_CT_NAME="$(docker ps --format '{{.Names}}' | grep -E '(^|_)osiris-api$' | head -1)"
if [ -n "$API_CT_NAME" ]; then
  # E1: osiris-agents -> osiris-api : must be UNREACHABLE (no interface on osiris-agents at all)
  docker exec "$AGENT_CT" curl -s -o /dev/null --max-time 4 http://osiris-api:8443/api/onboarding/status >/dev/null 2>&1
  rc=$?
  if is_unreachable "$rc"; then got=unreachable; else got=reachable; fi
  report "osiris-agents -> osiris-api:8443 (no interface on osiris-agents, no route expected)" "unreachable" "$got"

  # E2: docker.sock must NOT be mounted into osiris-api (D4/G8)
  SOCK_MOUNTED="$(docker inspect "$API_CT_NAME" --format '{{range .Mounts}}{{if eq .Source "/var/run/docker.sock"}}yes{{end}}{{end}}')"
  if [ "$SOCK_MOUNTED" = "yes" ]; then got=mounted; else got=absent; fi
  report "osiris-api container has NO docker.sock mount (D4/G8)" "absent" "$got"

  # E3: real agent workload netns -> osiris-api : must be UNREACHABLE too (D7-style check, reuses whichever real agent D-section found, if any)
  if [ -n "${AGENT_REAL_ID:-}" ]; then
    docker run --rm --network "container:$AGENT_REAL_ID" "$IMG" \
      curl -s -o /dev/null --max-time 4 http://osiris-api:8443/api/onboarding/status >/dev/null 2>&1
    rc=$?
    if is_unreachable "$rc"; then got=unreachable; else got=reachable; fi
    report "[실agent] -> osiris-api:8443 (no route expected — not one of the 3 names agents know, §8.3)" "unreachable" "$got"
  fi
else
  echo "SKIP: osiris-api container not found (not deployed yet?)"
fi

echo "=== $pass_count passed, $fail_count failed ==="
exit $fail
