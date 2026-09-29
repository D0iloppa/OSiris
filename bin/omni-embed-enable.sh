#!/usr/bin/env bash
# bin/omni-embed-enable.sh — omniroute UI를 OSiris 콘솔 안에 iframe으로
# 임베드할 수 있게 게이트웨이에서 프레임 정책을 연다.
#
#   bin/omni-embed-enable.sh            # 적용
#   bin/omni-embed-enable.sh --dry-run  # 수행할 것만 출력
#   bin/omni-embed-enable.sh --revert   # 원래대로(임베드 차단) 되돌린다
#
# ---------------------------------------------------------------------------
# 무엇을 여는가 — 그리고 무엇을 열지 않는가
# ---------------------------------------------------------------------------
# omniroute는 모든 응답에 다음 두 헤더를 보낸다(next.config.mjs의
# securityHeaders, **하드코딩이라 env 노브가 없다**):
#
#     X-Frame-Options: DENY
#     Content-Security-Policy: ... frame-ancestors 'none' ...
#
# 이 스크립트는 그 **두 헤더만** 게이트웨이에서 걷어내고, CSP를 원문 그대로
# 다시 붙이되 `frame-ancestors`만 `'self'`로 바꾼다. 나머지 지시어
# (default-src·script-src·connect-src 등)와 나머지 보안 헤더
# (X-Content-Type-Options, Referrer-Policy, Permissions-Policy, HSTS)는
# **건드리지 않는다** — 그것들은 프록시를 그대로 통과한다.
#
# `'self'`가 충분한 이유: omniroute는 `/omni` **서브 URL**로 서빙되므로
# 콘솔과 **같은 오리진**이다(감독 확정: "서브도메인을 매번 발행하는 것은
# 적절하지 않음"). 그래서 "우리 자신 안에서만 임베드 허용"으로 끝나고,
# 외부 사이트가 omniroute를 프레임에 넣는 것은 여전히 막힌다. 서브도메인
# 배포였다면 `'self'`로는 부족해 출처를 명시적으로 나열해야 했을 것이다.
#
# 이 완화는 omniroute 자신이 이미 쓰는 패턴이다 — 벤더도 자기 대시보드에
# 9Router UI를 끼워 넣으려고 특정 경로에만 `frame-ancestors 'self'` 예외를
# 준다(next.config.mjs의 headers(), NinerouterEmbedFrame.tsx). 우리가
# 발명한 우회가 아니라 같은 수단을, 소스를 고치지 않고 게이트웨이에서 쓴다
# (omniroute 소스는 한 줄도 고치지 않는다 — 이 저장소의 규율).
#
# ---------------------------------------------------------------------------
# 대가 두 가지 (알고 켜야 한다)
# ---------------------------------------------------------------------------
# (1) **컨트롤러 전역 설정을 하나 켠다.** ingress-nginx 1.9+ 는
#     `allow-snippet-annotations`를 기본 false로 두는데, 헤더를 걷어내는
#     annotation(`proxy_hide_header`)이 없어 snippet 없이는 방법이 없다.
#     이 값을 켜면 **그 클러스터의 다른 Ingress도** nginx 설정을 주입할 수
#     있게 된다. 클러스터에 Ingress를 만들 수 있는 사람 = 이 설정을 신뢰할
#     수 있는 사람인 배포에서만 켤 것. (deploy/osiris/values.yaml의
#     `ingress.useConfigurationSnippet` 주석이 같은 사정을 이미 설명한다.)
#
# (2) **CSP 원문을 복제한다.** nginx는 헤더 하나에서 지시어 하나만 바꾸지
#     못한다 — 통째로 지우고 다시 붙여야 한다. 그래서 아래 CSP는 omniroute의
#     next.config.mjs에서 **그대로 베낀 것**이고 `frame-ancestors`만 다르다.
#     omniroute를 업그레이드해 벤더가 CSP를 바꾸면 이 복제본이 낡는다.
#     `--check`로 원문과 대조할 수 있게 해 뒀다 — 업그레이드 후 한 번 돌릴 것.
# ---------------------------------------------------------------------------
set -euo pipefail

K8S_NS="${OSIRIS_K8S_NAMESPACE:-osiris-system}"
CTRL_NS="${OSIRIS_INGRESS_NAMESPACE:-ingress-nginx}"
CTRL_CM="ingress-nginx-controller"
CTRL_DEPLOY="deploy/ingress-nginx-controller"
INGRESSES=(osiris-omniroute osiris-omniroute-catchall)
OMNI_POD="osiris-omniroute-0"

# omniroute next.config.mjs의 contentSecurityPolicy 원문 — `frame-ancestors`만
# 'none' → 'self'. 순서·공백까지 원문을 따른다(대조를 쉽게 하기 위해).
CSP="default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'self'; form-action 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval' blob:; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com data:; img-src 'self' data: blob: https:; media-src 'self' data: blob:; connect-src 'self' http://localhost:* http://127.0.0.1:* ws://localhost:* ws://127.0.0.1:* https: ws: wss:; worker-src 'self' blob:; manifest-src 'self'"

MODE="apply"
case "${1:-}" in
  --dry-run) MODE="dry-run" ;;
  --revert)  MODE="revert" ;;
  --check)   MODE="check" ;;
  -h|--help)
    sed -n '2,10p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
    echo
    echo "  --check    실행 중인 omniroute의 CSP 원문과 이 스크립트의 복제본을 대조한다."
    exit 0 ;;
  "") ;;
  *) echo "알 수 없는 옵션: $1" >&2; exit 2 ;;
esac

command -v kubectl >/dev/null 2>&1 || { echo "kubectl이 필요합니다." >&2; exit 1; }
kubectl -n "$K8S_NS" get ingress "${INGRESSES[0]}" >/dev/null 2>&1 || {
  echo "네임스페이스 '${K8S_NS}'에 ${INGRESSES[0]} Ingress가 없습니다." >&2
  echo "(다른 네임스페이스면 OSIRIS_K8S_NAMESPACE=<ns> 로 지정)" >&2
  exit 1
}

# --- --check: 벤더 CSP가 우리 복제본과 어긋났는지 대조 ----------------------
if [ "$MODE" = "check" ]; then
  LIVE="$(kubectl -n "$K8S_NS" exec "$OMNI_POD" -- node -e '
    const fs=require("fs"); const s=fs.readFileSync("/app/next.config.mjs","utf8");
    const m=s.match(/const contentSecurityPolicy = \[([\s\S]*?)\]\.join\("; "\)/);
    if(!m){console.log("PARSE_FAIL");process.exit(0);}
    const parts=[...m[1].matchAll(/"([^"]+)"/g)].map(x=>x[1]);
    console.log(parts.join("; "));
  ' 2>/dev/null || true)"
  if [ -z "$LIVE" ] || [ "$LIVE" = "PARSE_FAIL" ]; then
    echo "벤더 CSP를 읽지 못했습니다(파일 구조가 바뀌었을 수 있음) — 수동 대조가 필요합니다." >&2
    exit 1
  fi
  # scriptSrc는 변수로 삽입되므로 위 파서가 못 잡는다. frame-ancestors만 바꾼
  # 우리 복제본에서 같은 방식으로 지시어를 골라 비교한다.
  EXPECT="$(printf '%s' "$LIVE" | sed "s/frame-ancestors 'none'/frame-ancestors 'self'/")"
  MINE_SUBSET="$(printf '%s' "$CSP" | tr ';' '\n' | sed 's/^ *//' | grep -vE "^script-src" | sort)"
  LIVE_SUBSET="$(printf '%s' "$EXPECT" | tr ';' '\n' | sed 's/^ *//' | grep -vE "^script-src|^\$\{?scriptSrc" | sort)"
  if [ "$MINE_SUBSET" = "$LIVE_SUBSET" ]; then
    echo "✅ CSP 복제본이 실행 중인 omniroute 원문과 일치합니다(script-src 제외 — 원문이 변수 삽입)."
    exit 0
  fi
  echo "⚠ CSP 복제본이 원문과 다릅니다. diff(좌: 이 스크립트 / 우: omniroute):" >&2
  diff <(printf '%s\n' "$MINE_SUBSET") <(printf '%s\n' "$LIVE_SUBSET") >&2 || true
  echo >&2
  echo "이 스크립트 상단의 CSP 변수를 원문에 맞춰 갱신한 뒤 다시 적용하세요." >&2
  exit 1
fi

# --- --revert ---------------------------------------------------------------
if [ "$MODE" = "revert" ]; then
  echo "임베드 허용을 되돌립니다(omniroute의 원래 DENY / frame-ancestors 'none'으로 복귀)."
  for ing in "${INGRESSES[@]}"; do
    kubectl -n "$K8S_NS" annotate ingress "$ing" \
      nginx.ingress.kubernetes.io/configuration-snippet- >/dev/null 2>&1 || true
    echo "  ${ing}: snippet 제거"
  done
  echo
  echo "컨트롤러의 allow-snippet-annotations는 **건드리지 않았습니다** — 다른 Ingress가"
  echo "쓰고 있을 수 있어 이 스크립트가 임의로 끄지 않습니다. 끄려면:"
  echo "  kubectl -n ${CTRL_NS} patch cm ${CTRL_CM} --type=merge -p '{\"data\":{\"allow-snippet-annotations\":\"false\"}}'"
  exit 0
fi

SNIPPET="proxy_hide_header X-Frame-Options;
proxy_hide_header Content-Security-Policy;
add_header Content-Security-Policy \"${CSP}\" always;
"

if [ "$MODE" = "dry-run" ]; then
  cat <<EOF

[dry-run] 수행할 것:
  1. kubectl -n ${CTRL_NS} patch cm ${CTRL_CM}  (allow-snippet-annotations=true)
  2. kubectl -n ${CTRL_NS} rollout restart ${CTRL_DEPLOY}
  3. 아래 Ingress에 configuration-snippet annotation 추가:
       ${INGRESSES[*]}
  4. 응답 헤더 검증 (X-Frame-Options 사라짐 + frame-ancestors 'self')

  주입할 snippet:
$(printf '%s' "$SNIPPET" | sed 's/^/      /')
EOF
  exit 0
fi

# --- 1. 컨트롤러에서 snippet 허용 -------------------------------------------
CURRENT="$(kubectl -n "$CTRL_NS" get cm "$CTRL_CM" -o jsonpath='{.data.allow-snippet-annotations}' 2>/dev/null || true)"
if [ "$CURRENT" = "true" ]; then
  echo "1/4 allow-snippet-annotations 이미 켜져 있음 — 건너뜀"
else
  echo "1/4 allow-snippet-annotations 켜는 중 (컨트롤러 전역 설정 — 위 주석의 대가 (1) 참고)..."
  kubectl -n "$CTRL_NS" patch cm "$CTRL_CM" --type=merge \
    -p '{"data":{"allow-snippet-annotations":"true"}}' >/dev/null
  echo "2/4 ingress 컨트롤러 재시작 중..."
  kubectl -n "$CTRL_NS" rollout restart "$CTRL_DEPLOY" >/dev/null
  kubectl -n "$CTRL_NS" rollout status "$CTRL_DEPLOY" --timeout=180s >/dev/null
fi
[ "$CURRENT" = "true" ] && echo "2/4 컨트롤러 재시작 불필요 — 건너뜀"

# --- 3. Ingress에 snippet 주입 ----------------------------------------------
echo "3/4 Ingress에 헤더 덮어쓰기 주입 중..."
PATCH_FILE="$(mktemp)"
trap 'rm -f "$PATCH_FILE"' EXIT
python3 - "$SNIPPET" >"$PATCH_FILE" <<'PY'
import json, sys
print(json.dumps({"metadata":{"annotations":{
    "nginx.ingress.kubernetes.io/configuration-snippet": sys.argv[1]}}}))
PY
for ing in "${INGRESSES[@]}"; do
  kubectl -n "$K8S_NS" patch ingress "$ing" --type=merge --patch-file "$PATCH_FILE" >/dev/null
  echo "    ${ing}: 적용"
done

# --- 4. 검증 ----------------------------------------------------------------
# 게이트웨이를 실제로 통과시켜 확인한다. 공개 도메인은 이 스크립트가 알 수
# 없으므로(배포 시점 정보) 클러스터 안에서 ingress 컨트롤러에 직접 친다.
echo "4/4 응답 헤더 검증 중..."
HDRS=""
for i in $(seq 1 12); do
  HDRS="$(kubectl -n "$CTRL_NS" exec "$CTRL_DEPLOY" -- \
    curl -s -D - -o /dev/null -H 'Host: localhost' http://127.0.0.1:80/omni/login 2>/dev/null || true)"
  printf '%s' "$HDRS" | grep -qi "content-security-policy" && break
  sleep 2
done

XFO="$(printf '%s' "$HDRS" | grep -ci '^x-frame-options' || true)"
FA="$(printf '%s' "$HDRS" | grep -i 'content-security-policy' | grep -c "frame-ancestors 'self'" || true)"

if [ "$XFO" = "0" ] && [ "$FA" != "0" ]; then
  echo "    ✅ X-Frame-Options 제거됨 + frame-ancestors 'self' 적용됨"
  echo
  echo "콘솔의 설정 → omniroute 설정 화면에서 임베드가 보입니다."
  echo "되돌리려면: $0 --revert"
else
  echo "    ❌ 아직 반영되지 않았습니다 (x-frame-options 헤더 ${XFO}개, frame-ancestors 'self' ${FA}개)" >&2
  echo "    수 초 뒤 다시 확인하거나, 컨트롤러 로그에서 snippet 거부 메시지를 확인하세요:" >&2
  echo "      kubectl -n ${CTRL_NS} logs ${CTRL_DEPLOY} --tail=50 | grep -i snippet" >&2
  exit 1
fi
