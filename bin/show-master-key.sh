#!/usr/bin/env bash
# bin/show-master-key.sh — OSiris **마스터 키**를 조작자가 원할 때 다시
# 보여준다. (구 bin/show-setup-token.sh의 후신 — 60-identity-authn.md §4.0/H22)
#
# 왜 이름이 바뀌었는가: 이 값은 처음부터 "사람이 콘솔 화면에 붙여넣어 세션을
# 얻는 유일한 진입점"이었다 — 즉 **행동상 이미 로그인 자격증명**이었고,
# "설정 토큰"이라는 이름이 그 사실을 가리고 있었다(§4.0). 이름을 바꾸면서
# 규율도 함께 붙었다: 이 값은 **초기 관리자가 비밀번호를 세우는 순간 죽는다**
# (H23). 그래서 이 스크립트는 "값이 없다"를 에러로만 처리하지 않는다 —
# 인수인계가 끝난 뒤에는 파일이 없는 것이 **정상**이고, 그 두 경우를 구별해서
# 말해주는 것이 이 스크립트의 실질적인 일이다.
#
# 왜 존재하는가: `osiris-up`은 이 값을 **최초 생성 시 1회만** stdout에 찍는다
# (반복 출력하면 셸 히스토리/스크롤백에 시크릿이 계속 쌓이므로). 그 순간을
# 놓쳤을 때 "호스트 파일을 직접 cat하라"는 안내는 그 자체가 마찰이다(감독 실측
# 피드백, 2026-07-27). 이 스크립트가 그 명시적·의도적 재조회 경로다.
#
# 시크릿 취급: 유일한 출력 대상은 조작자가 직접 실행한 이 커맨드의 stdout뿐
# 이다 — 로그 파일에 쓰지 않고, 에러 메시지에 값을 넣지 않는다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KEY_PATH="$ROOT/state/creds/admin/master_key"
LEGACY_PATH="$ROOT/state/creds/admin/setup_token"

GW_PORT="18280"
if [ -f "$ROOT/.env" ]; then
  ENV_PORT="$(grep -E '^OSIRIS_GW_HOST_PORT=' "$ROOT/.env" | head -1 | cut -d= -f2- || true)"
  [ -n "$ENV_PORT" ] && GW_PORT="$ENV_PORT"
fi
CONSOLE_URL="http://127.0.0.1:${GW_PORT}/"

# ---------------------------------------------------------------------------
# [2026-09-11] 배포 형태를 먼저 확인한다 — 이 스크립트가 실제로 오진했기 때문에
# 추가된 블록이다.
#
# 사건: 조작자가 이 스크립트를 돌렸더니 "(1) 인수인계가 끝났다 — email +
# 비밀번호로 로그인하세요"가 출력됐다. 실제로는 인수인계가 끝나기는커녕
# :Person 노드가 0개였고(즉 초기 관리자가 아예 없었고), 마스터 키는 멀쩡히
# 살아 있었다. 이 스크립트가 **compose 배포의 호스트 경로**($KEY_PATH) 하나만
# 보는데, 그 배포는 k8s(k3d)로 옮겨가 있었고 마스터 키는 PVC 안에 있었던 것뿐
# 이다. 파일이 없다는 사실에서 "인수인계 완료"를 **추론**한 것이 오진의 전부다.
#
# 교훈이자 이 블록의 규율: 이 스크립트는 "파일이 없다"에서 상태를 추론해서는
# 안 되고, **자기가 보지 않은 배포가 있는지 먼저 확인**해야 한다. 마스터 키가
# 어디에 있든 그 저장 위치를 아는 것은 osiris-api이지 이 스크립트가 아니므로,
# 호스트 경로에 없으면 k8s 배포를 찾아 같은 경로(/state/creds/admin/master_key)
# 를 파드 안에서 읽는다.
#
# 시크릿 취급은 호스트 경로와 동일하다 — 값은 이 커맨드의 stdout으로만 나가고,
# kubectl 호출의 stderr는 값을 담지 않는다.
# ---------------------------------------------------------------------------
K8S_NS="${OSIRIS_K8S_NAMESPACE:-osiris-system}"
K8S_KEY_PATH="/state/creds/admin/master_key"

# k8s 배포가 "있다"의 정의: kubectl이 있고, 그 네임스페이스에 osiris-api
# 디플로이먼트가 실재한다. 클러스터가 없거나 kubeconfig가 없으면 조용히 넘어가
# compose 경로만 쓰는 설치를 방해하지 않는다.
k8s_deployment_present() {
  command -v kubectl >/dev/null 2>&1 || return 1
  kubectl -n "$K8S_NS" get deploy/osiris-api >/dev/null 2>&1
}

# 파드 안의 키를 읽는다. 파일이 없으면(인수인계 완료) kubectl이 비-0으로 끝나고
# 여기서도 비-0을 돌려준다 — "없음"과 "못 읽음"을 호출부가 구분하지 않아도
# 되도록, 아래 호출부는 성공했을 때만 값을 쓴다.
k8s_read_master_key() {
  kubectl -n "$K8S_NS" exec deploy/osiris-api -c api -- cat "$K8S_KEY_PATH" 2>/dev/null
}

if [ ! -f "$KEY_PATH" ] && k8s_deployment_present; then
  K8S_KEY="$(k8s_read_master_key || true)"
  if [ -n "${K8S_KEY:-}" ]; then
    echo "배포 형태: kubernetes (네임스페이스 ${K8S_NS}) — 호스트 경로가 아니라 파드의 PVC에서 읽었습니다."
    echo "  (compose 경로 $KEY_PATH 는 이 배포에서 쓰이지 않습니다 — 비어 있는 것이 정상입니다.)"
    echo
    echo "콘솔: 이 배포의 공개 주소는 상위 게이트웨이가 정하므로 이 스크립트가 알 수 없습니다."
    echo "마스터 키: ${K8S_KEY}"
    echo
    echo "이 값으로 로그인하면 조직을 만들고, 그 다음 본인의 비밀번호를 설정합니다."
    echo "비밀번호를 설정하는 순간 이 마스터 키는 영구히 폐기됩니다(H23)."
    exit 0
  fi

  # 파드는 있는데 키 파일이 없다 — 이때만 "인수인계 완료"를 말할 자격이 있다.
  echo "마스터 키 파일이 파드 안에도 없습니다: ${K8S_NS}/osiris-api:${K8S_KEY_PATH}" >&2
  echo >&2
  echo "인수인계가 끝난 상태입니다 — 초기 관리자가 비밀번호를 설정하면 마스터 키는" >&2
  echo "영구 폐기됩니다(H23). 로그인은 관리자 본인의 email + 비밀번호로 합니다." >&2
  echo "관리자 자격증명을 분실했다면 새 마스터 키가 아니라 break-glass 복구 경로를" >&2
  echo "써야 합니다(docs/design/60-identity-authn.md §9)." >&2
  exit 1
fi

if [ ! -f "$KEY_PATH" ]; then
  # 구 파일이 남아 있으면 그것부터 짚는다 — 그 값은 `osk_` 접두가 없어서
  # 가드가 아예 경로를 고를 수 없으므로(§19.3) 붙여넣어도 401이다. "왜 파일은
  # 있는데 안 되는가"를 조작자가 혼자 추측하게 두지 않는다.
  if [ -f "$LEGACY_PATH" ]; then
    echo "구 설정 토큰 파일이 남아 있습니다: $LEGACY_PATH" >&2
    echo "이 값은 더 이상 사용되지 않습니다(H22: 마스터 키는 osk_ 접두가 필요합니다)." >&2
    echo "osiris-api가 다음 기동 시 조직 리비전을 보고 이 파일을 정리하고, 조직이 아직 없으면 새 마스터 키를 발급합니다." >&2
    exit 1
  fi

  # 여기가 이 스크립트의 핵심 분기다. 파일이 없는 것은 두 가지 뜻일 수 있고,
  # 하나는 정상이며 하나는 아니다.
  # 여기에 도달했다는 것은 (a) 호스트 경로에 키가 없고, (b) 위 블록이 k8s
  # 배포를 찾지 못했다는 뜻이다. (b)는 "k8s 배포가 없다"와 "있는데 이 셸에서
  # 못 봤다"(kubectl 없음/kubeconfig 없음/다른 네임스페이스)를 구분하지 못한다
  # — 그래서 아래 세 경우를 모두 제시하고, 어느 하나를 단정하지 않는다.
  # 단정했다가 오진한 것이 정확히 2026-09-11 사건이었다(위 블록 주석).
  echo "마스터 키 파일이 없습니다: $KEY_PATH" >&2
  echo >&2
  echo "세 경우 중 하나입니다:" >&2
  echo "  (1) 이 설치는 compose가 아니라 kubernetes 배포다 — 키는 호스트가 아니라" >&2
  echo "      파드의 PVC에 있습니다. 이 스크립트는 네임스페이스 '${K8S_NS}'의" >&2
  echo "      deploy/osiris-api를 찾아봤지만 보이지 않았습니다(kubectl 미설치," >&2
  echo "      kubeconfig 없음, 또는 다른 네임스페이스). 네임스페이스가 다르면:" >&2
  echo "        OSIRIS_K8S_NAMESPACE=<네임스페이스> $0" >&2
  echo "  (2) 인수인계가 끝났다 — 초기 관리자가 비밀번호를 설정하면 마스터 키는" >&2
  echo "      영구 폐기됩니다(H23). 이것이 정상 운영 상태이며, 로그인은 관리자" >&2
  echo "      본인의 email + 비밀번호로 합니다: ${CONSOLE_URL}" >&2
  echo "      관리자 자격증명을 분실했다면 새 마스터 키가 아니라 break-glass" >&2
  echo "      복구 경로를 써야 합니다(docs/design/60-identity-authn.md §9)." >&2
  echo "  (3) osiris-api가 한 번도 기동하지 않았다 — './osiris-up' 또는" >&2
  echo "      'docker compose up -d osiris-api'를 먼저 실행하세요." >&2
  exit 1
fi

# 상위 게이트웨이(도메인)가 있는 배포에서도 그 도메인은 이 스크립트가 알 방법이
# 없다(배포 시점 정보, 온보딩이 결정할 항목 — gw/conf.d/default.conf 헤더 주석과
# 같은 원칙) — 그래서 항상 루프백 URL만 찍는다. 이것이 이 프레임워크가 스스로에
# 대해 확실히 아는 유일한 주소다.
echo "콘솔(루프백, 프레임워크가 아는 유일한 확실한 주소):  ${CONSOLE_URL}"
echo "마스터 키:                                          $(cat "$KEY_PATH")"
echo
echo "이 값으로 로그인하면 조직을 만들고, 그 다음 본인의 비밀번호를 설정합니다."
echo "비밀번호를 설정하는 순간 이 마스터 키는 영구히 폐기됩니다(H23)."
