#!/usr/bin/env bash
# bin/omni-pw-change.sh — osiris-omniroute 대시보드 비밀번호를 바꾼다.
#
#   bin/omni-pw-change.sh <새-비밀번호>
#   printf '%s' '<새-비밀번호>' | bin/omni-pw-change.sh --stdin
#
# ---------------------------------------------------------------------------
# 왜 존재하는가 (2026-09-11 사건)
# ---------------------------------------------------------------------------
# omniroute 대시보드 비밀번호를 분실했다. `INITIAL_PASSWORD` env는 **저장된
# 해시가 없을 때만** 부트스트랩에 쓰이고(src/lib/auth/managementPassword.ts —
# `storedPassword || getInitialPasswordValue(...)`), 한 번 해시가 생기면 env를
# 무엇으로 바꿔도 무시된다. 그래서 Secret에 적힌 값도 호스트 .env에 적힌 값도
# 전부 401이었다 — 최초 기동 때 실제로 쓰인 값이 어디에도 남지 않았다.
#
# ---------------------------------------------------------------------------
# 왜 벤더가 안내하는 방법을 그대로 쓰지 않는가 — 둘 다 이 배포에 안 맞는다
# ---------------------------------------------------------------------------
# omniroute의 비밀번호 재설정 안내 화면은 두 가지를 제시한다.
#
# **방법 1 (`npx omniroute reset-password`) — 이 이미지에서 깨져 있다.**
# 컨테이너 안에서 실행하면(호스트에서 실행하면 엉뚱한 로컬 설치를 건드리므로
# 컨테이너 안이 맞다) 이렇게 죽는다 — 2026-09-11 실측:
#
#     Error [ERR_MODULE_NOT_FOUND]: Cannot find package 'bcryptjs'
#       imported from /app/bin/cli/settings-store.mjs
#
# 확인해 보니 `bcryptjs`가 이미지 어디에도 없다(`find /app -iname "*bcrypt*"`
# 0건, /app/node_modules 75개 중 없음). 서버 자신은 비밀번호를 검증하므로 bcrypt
# 를 쓰지만, 그건 Next 빌드 산출물 안에 번들링되어 있어 **패키지로 resolve되지
# 않는다**. 즉 CLI는 런타임 이미지에서 의존성이 빠진 채 배포된 벤더 패키징
# 결함이고, 우리가 고칠 수 있는 것이 아니다. (향후 이미지가 bcryptjs를 포함하면
# 방법 1이 되살아난다 — 그때 이 스크립트를 CLI 호출로 되돌려도 된다.)
#
# **방법 2 (`.env` 수정 + `data/settings.json` 삭제) — 파일이 그 모양이 아니다.**
# 이 배포의 설정은 JSON 파일이 아니라 SQLite에 있다:
#     /app/data/storage.sqlite  (WAL)
#     테이블 key_value(namespace='settings', key, value)  ← password 행 하나
#
# ---------------------------------------------------------------------------
# 그래서 이 스크립트가 하는 것: **방법 2를 SQLite에 맞게 옮긴 것**이다.
# ---------------------------------------------------------------------------
# 해싱을 우리가 하지 않는다는 점이 핵심이다 — bcrypt가 필요한 쪽은 서버이고,
# 서버는 자기 번들에 그것을 갖고 있다. 우리는 "해시를 지우고 새 평문을 env로
# 건네주는" 일만 하고, 해싱·저장은 서버가 자기 코드로 한다.
#
#   1. 새 비밀번호를 INITIAL_PASSWORD의 출처에 쓴다
#        k8s     → Secret osiris-secrets 의 OSIRIS_OMNIROUTE_ADMIN_PASSWORD
#        compose → .env 의 OSIRIS_OMNIROUTE_ADMIN_PASSWORD
#      (omniroute 컨테이너는 그 값을 INITIAL_PASSWORD로 받는다 —
#       omniroute-statefulset.yaml / docker-compose.yml 참고)
#   2. omniroute를 재시작한다 — **여기서 새 값이 프로세스 env에 들어온다.**
#      이 순서를 뒤집으면 안 된다(아래 "순서가 중요한 이유" 참고).
#   3. 저장된 해시 행을 지운다 (node 내장 node:sqlite, 외부 패키지 0개).
#   4. 새 비밀번호로 로그인을 왕복시킨다. 첫 로그인에서
#      `ensurePersistentManagementPasswordHash`(login/route.ts:102)가 해시가
#      없음을 보고 INITIAL_PASSWORD를 자기 bcrypt로 해싱해 영속화한다.
#      즉 4번은 검증인 동시에 **적용**이다.
#
# 순서가 중요한 이유: 2번(재시작)이 3번(해시 삭제)보다 **먼저**여야 한다.
# 반대로 하면, 해시가 지워진 상태로 아직 옛 INITIAL_PASSWORD를 들고 있는 구
# 프로세스가 살아 있고, 그 사이 로그인 시도 하나가 **옛 비밀번호를 다시
# 영속화**해 버린다. 그러면 조용히 실패한다 — 이번 사건이 정확히 "바뀐 줄
# 알았는데 아니었다"였으므로 그 실패 양식을 구조적으로 막는다.
#
# 보안 창이 열리지 않는 이유: 3번과 4번 사이에 저장된 해시가 없는 순간이
# 존재하지만, 그때도 `hasManagementPasswordConfigured`는 INITIAL_PASSWORD가
# 설정되어 있으므로 true이고, 해시도 env도 없으면 로그인 라우트는 403
# `needsSetup`을 돌려준다(login/route.ts:108~). 누구나 들어올 수 있는 순간은
# 생기지 않는다.
#
# ---------------------------------------------------------------------------
# ★ OSiris는 이 비밀번호를 쓰지 않는다. 온보딩은 omniroute **루트 키**
#   (OSIRIS_OMNIROUTE_ROOT_KEY — deploy/osiris/templates/secrets.yaml 주석)로만
#   붙는다. 이 비밀번호는 오직 "사람이 omniroute UI를 볼 때"를 위한 것이고,
#   또 분실해도 시스템은 계속 돈다. 그것이 루트 키를 둔 이유다.
#
# 시크릿 취급: 비밀번호는 stdin으로만 컨테이너/파이프에 들어간다(셸 보간 없음 —
#   따옴표가 섞인 값도 안전하고 컨테이너 안 `ps`에도 안 보인다). 로그에 쓰지
#   않고 출력에도 찍지 않는다.
#   ⚠ 다만 **인수로 넘기는 사용법은 값이 셸 히스토리와 호스트 `ps`에 남는다** —
#   이 스크립트가 없앨 수 없는 성질이다. 신경 쓰이면 `--stdin`을 쓸 것.
# ---------------------------------------------------------------------------
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
K8S_NS="${OSIRIS_K8S_NAMESPACE:-osiris-system}"
K8S_WORKLOAD="statefulset/osiris-omniroute"
K8S_POD="osiris-omniroute-0"
COMPOSE_CONTAINER="osiris-omniroute"
ENV_KEY="OSIRIS_OMNIROUTE_ADMIN_PASSWORD"
DB_PATH="/app/data/storage.sqlite"
MIN_LEN=8   # omniroute 자신의 MIN_PASSWORD_LENGTH와 같은 값

DRY_RUN=0
FROM_STDIN=0
PASSWORD=""

usage() {
  cat >&2 <<'EOF'
사용법:
  bin/omni-pw-change.sh <새-비밀번호> [옵션]
  printf '%s' '<새-비밀번호>' | bin/omni-pw-change.sh --stdin [옵션]

옵션:
  --stdin      비밀번호를 인수가 아니라 stdin에서 읽는다(셸 히스토리에 안 남는다).
  --dry-run    실행하지 않고 수행할 단계만 출력한다.
  -h, --help   이 도움말.

환경변수:
  OSIRIS_K8S_NAMESPACE   k8s 네임스페이스(기본 osiris-system).
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --stdin)   FROM_STDIN=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    -*)        echo "알 수 없는 옵션: $1" >&2; usage; exit 2 ;;
    *)
      if [ -n "$PASSWORD" ]; then
        echo "비밀번호 인수는 하나만 받습니다." >&2; usage; exit 2
      fi
      PASSWORD="$1"; shift ;;
  esac
done

if [ "$FROM_STDIN" -eq 1 ]; then
  [ -n "$PASSWORD" ] && { echo "--stdin과 비밀번호 인수를 함께 쓸 수 없습니다." >&2; exit 2; }
  PASSWORD="$(cat)"
fi
[ -z "$PASSWORD" ] && { echo "새 비밀번호가 필요합니다." >&2; usage; exit 2; }
if [ "${#PASSWORD}" -lt "$MIN_LEN" ]; then
  # 길이는 값을 노출하지 않는 정보다. 컨테이너까지 갔다 와서 실패하는 것보다
  # 여기서 먼저 말해 주는 편이 낫다.
  echo "비밀번호가 너무 짧습니다: ${#PASSWORD}자 (최소 ${MIN_LEN}자)" >&2
  exit 2
fi
case "$PASSWORD" in
  *$'\n'*) echo "비밀번호에 줄바꿈은 쓸 수 없습니다(.env 한 줄 형식과 충돌)." >&2; exit 2 ;;
esac

# --- 배포 형태 감지 ---------------------------------------------------------
# bin/show-master-key.sh와 같은 규율 — 이 저장소는 compose와 k8s(k3d)를 둘 다
# 지원하고, 2026-09-11에 "호스트 경로만 보고 상태를 단정"해서 오진한 전례가
# 있다. 추정하지 말고 실제로 확인한다.
MODE=""
if command -v kubectl >/dev/null 2>&1 && kubectl -n "$K8S_NS" get "$K8S_WORKLOAD" >/dev/null 2>&1; then
  MODE="k8s"
elif command -v docker >/dev/null 2>&1 && docker ps --format '{{.Names}}' 2>/dev/null | grep -qx "$COMPOSE_CONTAINER"; then
  MODE="compose"
else
  echo "osiris-omniroute를 찾을 수 없습니다." >&2
  echo "  k8s:     네임스페이스 '${K8S_NS}'에 ${K8S_WORKLOAD}가 없습니다" >&2
  echo "           (다른 네임스페이스면 OSIRIS_K8S_NAMESPACE=<ns> 로 지정)" >&2
  echo "  compose: '${COMPOSE_CONTAINER}' 컨테이너가 실행 중이지 않습니다" >&2
  exit 1
fi
echo "배포 형태: ${MODE}"

if [ "$DRY_RUN" -eq 1 ]; then
  cat <<EOF

[dry-run] 수행할 단계 (비밀번호는 stdin으로만 흐르므로 아래에 나타나지 않습니다):
  1. ${ENV_KEY} 갱신 → $([ "$MODE" = k8s ] && echo "secret/osiris-secrets (ns ${K8S_NS})" || echo "${ROOT}/.env")
  2. omniroute 재시작 → $([ "$MODE" = k8s ] && echo "kubectl rollout restart ${K8S_WORKLOAD}" || echo "docker restart ${COMPOSE_CONTAINER}")
  3. 저장된 해시 삭제 → node:sqlite 로 ${DB_PATH} 의 key_value(namespace='settings', key='password') 행 DELETE
  4. 새 비밀번호로 로그인 왕복 (첫 로그인이 곧 적용)
EOF
  exit 0
fi

# --- 1. INITIAL_PASSWORD 출처 갱신 ------------------------------------------
echo "1/4 ${ENV_KEY} 갱신 중..."
if [ "$MODE" = "k8s" ]; then
  # base64는 python3로 만든다 — 비밀번호가 인수로 노출되지 않도록 stdin으로만
  # 받고, kubectl에 넘길 JSON도 같은 프로세스 안에서 조립한다.
  printf '%s' "$PASSWORD" | python3 -c '
import base64, json, subprocess, sys
pw, ns, key = sys.stdin.read(), sys.argv[1], sys.argv[2]
patch = json.dumps({"data": {key: base64.b64encode(pw.encode()).decode()}})
sys.exit(subprocess.call(["kubectl","-n",ns,"patch","secret","osiris-secrets","--type=merge","-p",patch],
                         stdout=subprocess.DEVNULL))
' "$K8S_NS" "$ENV_KEY"
else
  ENV_FILE="$ROOT/.env"
  [ -f "$ENV_FILE" ] || { echo "  .env가 없습니다: $ENV_FILE" >&2; exit 1; }
  printf '%s' "$PASSWORD" | python3 -c '
import re, sys
pw, path, key = sys.stdin.read(), sys.argv[1], sys.argv[2]
lines = open(path, encoding="utf-8").read().splitlines(keepends=True)
out, seen = [], False
for ln in lines:
    if re.match(rf"^{re.escape(key)}=", ln):
        out.append(f"{key}={pw}\n"); seen = True
    else:
        out.append(ln)
if not seen:
    if out and not out[-1].endswith("\n"): out.append("\n")
    out.append(f"{key}={pw}\n")
open(path, "w", encoding="utf-8").write("".join(out))
' "$ENV_FILE" "$ENV_KEY"
fi

# --- 2. 재시작 (해시 삭제보다 반드시 먼저) ----------------------------------
echo "2/4 omniroute 재시작 중 (새 값을 프로세스 env에 넣기 위함)..."
if [ "$MODE" = "k8s" ]; then
  kubectl -n "$K8S_NS" rollout restart "$K8S_WORKLOAD" >/dev/null
  kubectl -n "$K8S_NS" rollout status "$K8S_WORKLOAD" --timeout=180s >/dev/null
else
  # compose는 env 변경을 반영하려면 재생성이 필요하다(restart로는 부족).
  (cd "$ROOT" && docker compose up -d --force-recreate "$COMPOSE_CONTAINER" >/dev/null)
fi

# --- 3. 저장된 해시 삭제 ----------------------------------------------------
# node:sqlite는 Node 22+ 내장이다 — 이 이미지에 bcryptjs가 없어서 벤더 CLI가
# 죽는 것과 달리, 여기엔 외부 패키지 의존이 하나도 없다.
echo "3/4 저장된 비밀번호 해시 삭제 중..."
DELETE_JS='
const { DatabaseSync } = require("node:sqlite");
const db = new DatabaseSync(process.argv[1]);
const before = db.prepare("SELECT COUNT(*) n FROM key_value WHERE namespace=? AND key=?").get("settings","password").n;
db.prepare("DELETE FROM key_value WHERE namespace=? AND key=?").run("settings","password");
const after = db.prepare("SELECT COUNT(*) n FROM key_value WHERE namespace=? AND key=?").get("settings","password").n;
db.close();
console.log(`DELETED before=${before} after=${after}`);
if (after !== 0) process.exit(1);
'
if [ "$MODE" = "k8s" ]; then
  kubectl -n "$K8S_NS" exec "$K8S_POD" -- node -e "$DELETE_JS" "$DB_PATH"
else
  docker exec "$COMPOSE_CONTAINER" node -e "$DELETE_JS" "$DB_PATH"
fi

# --- 4. 로그인 왕복 = 검증이자 적용 ------------------------------------------
# 컨테이너 안에서 루프백으로 친다 — 공개 도메인을 알 필요가 없다(이 프레임워크가
# 스스로에 대해 확실히 아는 유일한 주소). 비밀번호는 여기서도 stdin으로만.
echo "4/4 새 비밀번호로 로그인 검증 중 (첫 로그인이 곧 적용)..."
VERIFY_JS='
let pw = ""; process.stdin.setEncoding("utf8");
process.stdin.on("data", (c) => (pw += c));
process.stdin.on("end", async () => {
  let last = "";
  for (let i = 0; i < 20; i++) {
    try {
      const r = await fetch("http://127.0.0.1:20128/omni/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password: pw }),
      });
      if (r.status === 200) { console.log("LOGIN_OK"); process.exit(0); }
      last = `HTTP ${r.status}`;
      // 403 needsSetup / 401 은 설정이 아직 안 읽혔을 수 있다(읽기 캐시 TTL 5s).
    } catch (e) { last = e.message; }
    await new Promise((s) => setTimeout(s, 1500));
  }
  console.log("LOGIN_FAILED " + last); process.exit(1);
});
'
VERIFY_OUT=""
if [ "$MODE" = "k8s" ]; then
  VERIFY_OUT="$(printf '%s' "$PASSWORD" | kubectl -n "$K8S_NS" exec -i "$K8S_POD" -- node -e "$VERIFY_JS" 2>&1 || true)"
else
  VERIFY_OUT="$(printf '%s' "$PASSWORD" | docker exec -i "$COMPOSE_CONTAINER" node -e "$VERIFY_JS" 2>&1 || true)"
fi

case "$VERIFY_OUT" in
  *LOGIN_OK*)
    echo "    ✅ 새 비밀번호로 로그인 성공 — 서버가 해시를 영속화했습니다."
    ;;
  *)
    echo "    ❌ 로그인이 되지 않습니다:" >&2
    echo "$VERIFY_OUT" | tail -5 >&2
    echo >&2
    echo "    해시는 지워졌고 새 값은 ${ENV_KEY}에 들어가 있습니다 — 재시도(이 스크립트를" >&2
    echo "    같은 인수로 다시 실행)가 안전합니다." >&2
    exit 1
    ;;
esac

echo
if [ "$MODE" = "k8s" ]; then
  echo "재조회: kubectl -n ${K8S_NS} get secret osiris-secrets -o jsonpath='{.data.${ENV_KEY}}' | base64 -d; echo"
else
  echo "재조회: grep '^${ENV_KEY}=' ${ROOT}/.env"
fi
echo "omniroute UI: 상위 게이트웨이의 /omni/login"
echo "(OSiris 온보딩은 이 비밀번호를 쓰지 않습니다 — 루트 키로만 붙습니다.)"
