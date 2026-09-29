#!/usr/bin/env bash
# bin/verify-isolation-k8s.sh — bin/verify-isolation.sh의 k8s 후속판.
#
# `docs/design/70-kubernetes-deployment.md` §3.4(K11 실측)이 이 스크립트의
# 존재 이유를 정확히 적어 둔다:
#
#   "verify-isolation.sh의 k8s 후속판이 kubectl exec로 도달 가능성을
#    찍고, 그 결과가 15개 검사에 대응하는 목록으로 나오면 [K11이] 닫힌다."
#
# ★ 정직하게 밝힌다: bin/verify-isolation.sh는 지금 실제로 15개가 아니라
# **24개**의 개별 report() 판정(A1~A8, B1, C1~C4, D1~D8, E1~E3)을 수행한다
# — I5(omniroute 도입) 이후 여러 차례 늘어났고, 70 §3.4/§6.2가 인용한
# "15개"는 그 이전 버전(`IMPLEMENTATION-NOTES.md` §104.5 완료조건 8 시점)
# 의 개수로 보인다. 이 스크립트는 "15개"라는 숫자에 맞추기 위해 검사를
# 임의로 합치거나 빼지 않는다 — **원본에 지금 실제로 있는 24개 판정을
# 전부 하나씩 대응**시킨다(조용히 빠뜨리지 않는다, 작업 지시 원칙 그대로).
#
# 대응 원칙:
#   - docker 네트워크 토폴로지 검사(agents/dev-net 컨테이너를 붙이고
#     끊는 것)는 kubectl exec/kubectl debug로 실제 Pod netns에서
#     재현한다.
#   - `dev-net`(compose 전용, k8s 클러스터 밖의 도커 브리지) 자체가
#     클러스터 안에 없으므로, dev-net 관련 검사는 "대응 없음"으로 명시
#     하거나(A2/D3처럼 순수 외부망 개념일 때) "다른 네임스페이스에서의
#     접근 시도"로 대체한다(B1/C1처럼 "OSiris 소유가 아닌 제3자가 우리
#     프록시/코어를 오용할 수 있는가"라는 취지가 네임스페이스 경계로도
#     재현되는 경우).
#   - docker.sock 마운트 검사(E2)는 k8s에 docker.sock 개념이 없으므로
#     "대응 없음"이 아니라 **k8s 대응물로 치환**한다 — G8("core/api는
#     워크로드를 만들 수 없다")의 k8s 표현은 RBAC이므로, ServiceAccount가
#     pods/deployments를 create할 권한이 없는지 `kubectl auth can-i`로
#     검사한다.
#
# 아직 클러스터가 없으므로 이 스크립트는 **실행하지 않는다**(작업 지시) —
# `kubectl`이 없거나 클러스터에 붙을 수 없으면 각 검사를 명시적으로
# SKIP 보고한다(조용히 통과 처리하지 않는다 — bin/verify-isolation.sh의
# dev-net 부재 시 SKIP 규율과 동일).
set -uo pipefail

NS_SYSTEM="osiris-system"
NS_AGENTS="osiris-agents"
NS_OUTSIDER="default"   # dev-net의 "OSiris 소유가 아닌 제3자" 취지를
                          # 재현하는 데 쓰는, osiris 네임스페이스가 아닌
                          # 제3의 네임스페이스. 클러스터에 이미 워크로드가
                          # 있을 수 있으므로 여기 만드는 테스트 Pod은 항상
                          # 정리한다.
IMG="curlimages/curl:8.10.1"
AGENT_TEST_POD="osiris-verify-agents"
OUTSIDER_TEST_POD="osiris-verify-outsider"

pass_count=0
fail_count=0
fail=0
skip_count=0

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

skip() {
  echo "SKIP: $1"
  skip_count=$((skip_count + 1))
}

no_equivalent() {
  # 대응 없음을 report()와 나란히 눈에 띄게 남긴다 — PASS/FAIL 어느 쪽도
  # 아니지만, 조용히 빠뜨리는 것과는 다르다는 것을 출력에서 구별한다.
  echo "N/A: $1 — $2"
}

# ---------------------------------------------------------------------------
# 0. 전제 확인 — kubectl 존재 여부와 클러스터 접속 가능 여부.
#    둘 중 하나라도 실패하면 **이 스크립트의 모든 개별 검사를 SKIP으로
#    보고**하고 종료한다(조용히 통과시키지 않는다 — bin/verify-isolation.sh
#    의 dev-net SKIP 규율과 동일한 정신).
# ---------------------------------------------------------------------------
CLUSTER_AVAILABLE=1
if ! command -v kubectl >/dev/null 2>&1; then
  echo "SKIP-ALL: kubectl이 설치되어 있지 않습니다. 이 스크립트는 클러스터가 있을 때만 의미가 있습니다."
  CLUSTER_AVAILABLE=0
elif ! kubectl cluster-info >/dev/null 2>&1; then
  echo "SKIP-ALL: kubectl은 있으나 클러스터에 접속할 수 없습니다(kubeconfig 미설정 또는 클러스터 미기동)."
  CLUSTER_AVAILABLE=0
fi

if [ "$CLUSTER_AVAILABLE" = "0" ]; then
  # 24개 각 검사를 개별적으로 SKIP 표시한다 — "스크립트가 아무것도 안
  # 했다"가 아니라 "24개 중 24개를 확인하지 못했다"는 것을 명시한다.
  for id in A1 A2 A3 A4 A5 A6 B1 A7 A8 C1 C2 C3 C4 \
            D1 D2 D3 D4 D5 D6 D7 D8 E1 E2 E3; do
    skip "[$id] 클러스터 미접속으로 확인 불가"
  done
  echo "=== 0 passed, 0 failed, $skip_count skipped (클러스터 없음) ==="
  exit 0
fi

cleanup() {
  kubectl delete pod "$AGENT_TEST_POD" -n "$NS_AGENTS" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete pod "$OUTSIDER_TEST_POD" -n "$NS_OUTSIDER" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

kube_run() {
  # $1=namespace $2=podname — 이미 있으면 지우고 새로 띄운다.
  local ns="$1" name="$2"
  kubectl delete pod "$name" -n "$ns" --ignore-not-found --wait=true >/dev/null 2>&1 || true
  kubectl run "$name" -n "$ns" --image="$IMG" --restart=Never \
    --command -- sleep 600 >/dev/null
  kubectl wait --for=condition=Ready "pod/$name" -n "$ns" --timeout=60s >/dev/null 2>&1
}

# curl exit code 판정 — bin/verify-isolation.sh의 is_unreachable과 동일한
# 코드 집합(could not resolve/connect/timeout/reset/SSL connect).
classify_rc() {
  case "$1" in
    6|7|28|56|35) echo unreachable ;;
    *) echo reachable ;;
  esac
}

curl_from() {
  # $1=namespace $2=podname $3...=curl args
  local ns="$1" pod="$2"; shift 2
  kubectl exec "$pod" -n "$ns" -- curl -s -o /dev/null --max-time 4 "$@" >/dev/null 2>&1
  echo $?
}

judge_egress() {
  # bin/verify-isolation.sh의 judge_egress와 동일 판정(exitcode=0 &&
  # http_code!=000 => allowed).
  local ns="$1" pod="$2" url="$3"
  local out
  out="$(kubectl exec "$pod" -n "$ns" -- curl -s -o /dev/null -w '%{http_code} %{exitcode}' \
    --max-time 6 -x "http://osiris-egress.${NS_SYSTEM}:3128" "$url" 2>/dev/null)"   # [2026-09-29] 테스트 파드는 다른 네임스페이스라 짧은 이름이 안 풀려 A5/A7이 늘 denied(A6/B1은 거짓 PASS)였다
  local code ec
  code="$(echo "$out" | awk '{print $1}')"
  ec="$(echo "$out" | awk '{print $2}')"
  if [ "$ec" = "0" ] && [ "$code" != "000" ]; then echo allowed; else echo denied; fi
}

echo "=== OSiris k8s isolation verification ==="
kube_run "$NS_AGENTS" "$AGENT_TEST_POD"
kube_run "$NS_OUTSIDER" "$OUTSIDER_TEST_POD"

# ===========================================================================
# A절 대응 — 합성 osiris-agents 테스트 Pod에서의 도달 가능성
# ===========================================================================

# [A1] agents -> neo4j:7474 : unreachable
rc="$(curl_from "$NS_AGENTS" "$AGENT_TEST_POD" "http://neo4j.${NS_SYSTEM}.svc.cluster.local:7474/")"
report "[A1] osiris-agents -> neo4j:7474 (no route expected, NetworkPolicy default-deny)" "unreachable" "$(classify_rc "$rc")"

# [A2] agents -> dev-net(상위 리버스 프록시) : 대응 없음 — dev-net은 compose 전용
# 도커 브리지이고 K3가 osiris-gw 자체를 폐지한다. 클러스터 안에 이 개념이
# 존재하지 않는다. 취지("에이전트가 OSiris 소유가 아닌 임의 외부/인접
# 네트워크에 도달할 수 없다")는 egress default-deny 정책이 프록시를 거치지
# 않는 임의 외부 목적지를 전부 막는다는 사실로 대체 확인한다.
no_equivalent "[A2] osiris-agents -> upstream-proxy(dev-net)" "dev-net은 클러스터 밖 docker 개념, K3로 osiris-gw 폐지. 취지는 아래 임의 외부 IP 직접 접속 검사(A2')로 대체"
rc="$(curl_from "$NS_AGENTS" "$AGENT_TEST_POD" "http://1.1.1.1:80/")"
report "[A2'] osiris-agents -> 임의 외부 IP 직접 접속(프록시 우회, no route expected)" "unreachable" "$(classify_rc "$rc")"

# [A3] agents -> nats:8222/healthz : reachable
rc="$(curl_from "$NS_AGENTS" "$AGENT_TEST_POD" "http://nats.${NS_SYSTEM}.svc.cluster.local:8222/healthz")"
report "[A3] osiris-agents -> nats:8222/healthz (reachable expected, 이중 홈)" "reachable" "$(classify_rc "$rc")"

# [A4] agents -> osiris-egress:3128 TCP : reachable
rc="$(curl_from "$NS_AGENTS" "$AGENT_TEST_POD" "http://osiris-egress.${NS_SYSTEM}.svc.cluster.local:3128/")"
report "[A4] osiris-agents -> osiris-egress:3128 TCP (reachable expected)" "reachable" "$(classify_rc "$rc")"

# [A5] agents --[egress]--> github.com (allowlisted) : allowed
got="$(judge_egress "$NS_AGENTS" "$AGENT_TEST_POD" "https://github.com/")"
report "[A5] osiris-agents --[egress]--> github.com (allowlisted)" "allowed" "$got"

# [A6] agents --[egress]--> example.com (NOT allowlisted) : denied
got="$(judge_egress "$NS_AGENTS" "$AGENT_TEST_POD" "https://example.com/")"
report "[A6] osiris-agents --[egress]--> example.com (NOT allowlisted)" "denied" "$got"

# [B1] dev-net(제3자)이 osiris-egress를 오픈 프록시로 오용 : denied
# 대응: dev-net 자체는 없지만 "OSiris 소유가 아닌 네임스페이스"라는 취지는
# `default` 네임스페이스의 테스트 Pod으로 재현된다 — networkpolicy-system
# .yaml의 osiris-egress-allow가 osiris-agents 네임스페이스와 omniroute
# 컴포넌트만 ingress를 허용하므로, `default` 네임스페이스에서의 시도는
# NetworkPolicy 단계에서부터 막혀야 한다(squid ACL까지 갈 필요도 없이).
got="$(judge_egress "$NS_OUTSIDER" "$OUTSIDER_TEST_POD" "https://github.com/")"
report "[B1→outsider-ns] default 네임스페이스 --[egress]--> github.com (제3자 오용, denied 기대: NetworkPolicy가 osiris-agents 네임스페이스만 허용)" "denied" "$got"

# [A7] agents --[egress]--> osiris-omniroute:20128 : allowed
got="$(judge_egress "$NS_AGENTS" "$AGENT_TEST_POD" "http://osiris-omniroute.${NS_SYSTEM}.svc.cluster.local:20128/omni/v1/models")"
report "[A7] osiris-agents --[egress]--> osiris-omniroute:20128 (allowlisted)" "allowed" "$got"

# [A8] agents -> osiris-omniroute:20128 DIRECT(프록시 없이) : unreachable
rc="$(curl_from "$NS_AGENTS" "$AGENT_TEST_POD" "http://osiris-omniroute.${NS_SYSTEM}.svc.cluster.local:20128/omni/v1/models")"
report "[A8] osiris-agents -> osiris-omniroute:20128 DIRECT (no route expected, §9.0 불변)" "unreachable" "$(classify_rc "$rc")"

# ===========================================================================
# C절 대응 — osiris-core 배치 경계
# ===========================================================================

# [C1] dev-net -> osiris-core : 대응 없음(dev-net 부재) → outsider 네임스페이스로 대체
no_equivalent "[C1] dev-net -> osiris-core" "dev-net 부재. outsider 네임스페이스 검사(C1')로 대체"
rc="$(curl_from "$NS_OUTSIDER" "$OUTSIDER_TEST_POD" "http://osiris-core.${NS_SYSTEM}.svc.cluster.local:8090/v1/revision")"
report "[C1'] default 네임스페이스 -> osiris-core:8090 (no route expected, osiris-core-allow가 api/orchestrator/osiris-agents만 허용)" "unreachable" "$(classify_rc "$rc")"

# [C2] agents -> osiris-core:8090 : reachable (에이전트 대면 API, 설계대로)
rc="$(curl_from "$NS_AGENTS" "$AGENT_TEST_POD" "http://osiris-core.${NS_SYSTEM}.svc.cluster.local:8090/v1/revision")"
report "[C2] osiris-agents -> osiris-core:8090 (reachable expected, agent-facing API by design)" "reachable" "$(classify_rc "$rc")"

# [C3] 같은 호출, 토큰 없이 : 401 (도달 가능 != 인가됨)
CODE="$(kubectl exec "$AGENT_TEST_POD" -n "$NS_AGENTS" -- curl -s -o /dev/null -w '%{http_code}' \
  --max-time 4 "http://osiris-core.${NS_SYSTEM}.svc.cluster.local:8090/v1/revision" 2>/dev/null || echo "000")"
report "[C3] osiris-agents -> osiris-core:8090/v1/revision 토큰 없이 (401 기대, raw=$CODE)" "401" "$CODE"

# [C4] agents -> neo4j : 여전히 unreachable (core가 유일한 writer, A5/A1과
# 동일한 k8s 검사 — 원본에서도 A1과 문자 그대로 같은 호출을 반복해
# "우회 불가"를 재확인하는 검사였다. 여기서도 동일 호출을 재사용한다.
rc="$(curl_from "$NS_AGENTS" "$AGENT_TEST_POD" "http://neo4j.${NS_SYSTEM}.svc.cluster.local:7474/")"
report "[C4] osiris-agents -> neo4j:7474 (여전히 no route — core가 유일한 writer, A1과 동일 검사 재확인)" "unreachable" "$(classify_rc "$rc")"

# ===========================================================================
# D절 대응 — 실제 에이전트 워크로드(operator가 만든 진짜 Pod)의 netns.
#
# compose 원본은 `docker run --network container:<id>`로 실제 에이전트
# 컨테이너와 네트워크 네임스페이스를 공유하는 별도 컨테이너를 띄웠다.
# k8s 대응물은 `kubectl debug <pod> --image=... `(ephemeral container) —
# ephemeral container는 항상 대상 Pod의 네트워크 네임스페이스를 공유한다.
#
# ★ TODO(감독 판단 필요): Agent CRD/operator(K9/K10)는 C-3 범위라 아직
# 없다 — "실제 에이전트 Pod을 어떤 라벨로 찾는가"가 아직 정해지지 않았다.
# compose 시절 라벨(`osiris.dev/managed-by=osiris-orchestrator`)을 그대로
# 가정해 뒀지만, operator가 실제로 이 라벨을 붙이는지는 C-3 구현을
# 봐야 확인된다 — 이 스크립트는 그 라벨이 없으면(=Agent 0개 또는 라벨
# 불일치) 아래 D1~D8을 전부 SKIP으로 보고한다(조용히 통과 아님).
# ===========================================================================
REAL_AGENT_POD="$(kubectl get pods -n "$NS_AGENTS" \
  -l osiris.dev/managed-by=osiris-orchestrator \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"

if [ -n "${REAL_AGENT_POD:-}" ]; then
  echo "실제 에이전트 Pod로 검증: $REAL_AGENT_POD (namespace=$NS_AGENTS)"

  debug_curl() {
    # ephemeral debug container로 실제 에이전트 Pod의 netns를 공유해 curl.
    local url="$1"
    kubectl debug "$REAL_AGENT_POD" -n "$NS_AGENTS" \
      --image="$IMG" --quiet -- \
      curl -s -o /dev/null --max-time 4 "$url" >/dev/null 2>&1
    echo $?
  }
  debug_egress_judge() {
    local url="$1"
    local out
    out="$(kubectl debug "$REAL_AGENT_POD" -n "$NS_AGENTS" \
      --image="$IMG" --quiet -- \
      curl -s -o /dev/null -w '%{http_code} %{exitcode}' --max-time 6 \
      -x http://osiris-egress:3128 "$url" 2>/dev/null)"
    local code ec
    code="$(echo "$out" | awk '{print $1}')"; ec="$(echo "$out" | awk '{print $2}')"
    if [ "$ec" = "0" ] && [ "$code" != "000" ]; then echo allowed; else echo denied; fi
  }

  # [D1] real-agent -> neo4j : unreachable
  rc="$(debug_curl "http://neo4j.${NS_SYSTEM}.svc.cluster.local:7474/")"
  report "[D1] [실agent] -> neo4j:7474 (no route expected)" "unreachable" "$(classify_rc "$rc")"

  # [D2] real-agent -> osiris-orchestrator : unreachable (§2.6, 에이전트 도달 불가)
  rc="$(debug_curl "http://osiris-orchestrator.${NS_SYSTEM}.svc.cluster.local:8081/livez")"
  report "[D2] [실agent] -> osiris-orchestrator:8081 (no route expected)" "unreachable" "$(classify_rc "$rc")"

  # [D3] real-agent -> dev-net(상위 리버스 프록시) : 대응 없음(dev-net 부재) → 임의 외부 IP로 대체
  no_equivalent "[D3] [실agent] -> upstream-proxy(dev-net)" "dev-net 부재. 임의 외부 IP 직접 접속(D3')으로 대체"
  rc="$(debug_curl "http://1.1.1.1:80/")"
  report "[D3'] [실agent] -> 임의 외부 IP 직접 접속(프록시 우회, no route expected)" "unreachable" "$(classify_rc "$rc")"

  # [D4] real-agent -> nats : reachable
  rc="$(debug_curl "http://nats.${NS_SYSTEM}.svc.cluster.local:8222/healthz")"
  report "[D4] [실agent] -> nats:8222/healthz (reachable expected)" "reachable" "$(classify_rc "$rc")"

  # [D5] real-agent -> osiris-core:8090 : reachable
  rc="$(debug_curl "http://osiris-core.${NS_SYSTEM}.svc.cluster.local:8090/v1/revision")"
  report "[D5] [실agent] -> osiris-core:8090 (reachable expected)" "reachable" "$(classify_rc "$rc")"

  # [D6] real-agent -> osiris-egress:3128 : reachable
  rc="$(debug_curl "http://osiris-egress.${NS_SYSTEM}.svc.cluster.local:3128/")"
  report "[D6] [실agent] -> osiris-egress:3128 (reachable expected)" "reachable" "$(classify_rc "$rc")"

  # [D7] real-agent -> osiris-omniroute DIRECT(프록시 없이) : unreachable
  rc="$(debug_curl "http://osiris-omniroute.${NS_SYSTEM}.svc.cluster.local:20128/omni/v1/models")"
  report "[D7] [실agent] -> osiris-omniroute:20128 DIRECT (no route expected)" "unreachable" "$(classify_rc "$rc")"

  # [D8] real-agent --[egress]--> osiris-omniroute : allowed
  got="$(debug_egress_judge "http://osiris-omniroute.${NS_SYSTEM}.svc.cluster.local:20128/omni/v1/models")"
  report "[D8] [실agent] --[egress]--> osiris-omniroute:20128 (allowlisted)" "allowed" "$got"
else
  skip "[D1..D8] 실행 중인 실제 에이전트 Pod을 찾지 못했습니다 (라벨 osiris.dev/managed-by=osiris-orchestrator 없음 — Agent CRD/operator가 C-3에서 구현되기 전까지는 항상 이 상태입니다. TODO 참고)"
fi

# ===========================================================================
# E절 대응 — osiris-api 배치 경계 + docker.sock 검사의 k8s 대응물(RBAC)
# ===========================================================================

# [E1] agents -> osiris-api : unreachable (osiris-api는 osiris-agents 멤버가 아님)
rc="$(curl_from "$NS_AGENTS" "$AGENT_TEST_POD" "http://osiris-api.${NS_SYSTEM}.svc.cluster.local:8443/api/onboarding/status")"
report "[E1] osiris-agents -> osiris-api:8443 (no route expected)" "unreachable" "$(classify_rc "$rc")"

# [E2] docker.sock 마운트 검사의 k8s 대응물 — **대응 없음이 아니라 치환**.
# G8("core/api는 워크로드를 만들지 못한다")의 k8s 표현은 docker.sock의
# 부재가 아니라 RBAC이다: osiris-api Pod의 ServiceAccount가 pods/
# deployments를 create할 권한이 없어야 한다.
#
# [2026-09-11 개정] osiris-api는 이제 "권한 0"이 아니다 —
# docs/design/90-infra-topology.md §4.3에 따라 osiris-api-infra-read
# ClusterRole(nodes/namespaces/pods get·list·watch, metrics.k8s.io
# nodes/pods get·list)을 갖는다(rbac-api.yaml). 이 검사가 여전히 유효한
# 이유: 그 개정은 읽기(get/list/watch)만 다루고, 이 검사는 **쓰기**
# (create)만 묻는다 — 두 축은 서로 다르고, D4/G8이 실제로 지키는 것은
# "워크로드를 만들지 못한다"는 쪽이다. read RBAC가 생겼다고 이 검사의
# 기대값(cannot-create)이 바뀔 이유는 없다. secrets는 어떤 verb로도
# 어떤 스코프에서도 여전히 주어지지 않는다(§4.3(2)).
API_SA="$(kubectl get pods -n "$NS_SYSTEM" -l app.kubernetes.io/component=api \
  -o jsonpath='{.items[0].spec.serviceAccountName}' 2>/dev/null || true)"
if [ -n "${API_SA:-}" ]; then
  if kubectl auth can-i create pods --as="system:serviceaccount:${NS_SYSTEM}:${API_SA}" -n "$NS_SYSTEM" 2>/dev/null | grep -q '^yes$'; then
    got="can-create"
  else
    got="cannot-create"
  fi
  report "[E2→RBAC] osiris-api ServiceAccount($API_SA)는 pods를 create할 수 없다 (D4/G8의 k8s 대응 — docker.sock 부재 대신 RBAC 부재)" "cannot-create" "$got"
else
  skip "[E2→RBAC] osiris-api Pod을 찾지 못해 ServiceAccount를 확인할 수 없습니다"
fi

# [E3] real-agent -> osiris-api : unreachable (D-절 real agent pod을 재사용)
if [ -n "${REAL_AGENT_POD:-}" ]; then
  rc="$(kubectl debug "$REAL_AGENT_POD" -n "$NS_AGENTS" --image="$IMG" --quiet -- \
    curl -s -o /dev/null --max-time 4 "http://osiris-api.${NS_SYSTEM}.svc.cluster.local:8443/api/onboarding/status" 2>/dev/null; echo $?)"
  report "[E3] [실agent] -> osiris-api:8443 (no route expected)" "unreachable" "$(classify_rc "$rc")"
else
  skip "[E3] 실제 에이전트 Pod이 없어 확인 불가 (D1..D8과 동일한 이유)"
fi

echo "=== $pass_count passed, $fail_count failed, $skip_count skipped ==="
exit $fail
