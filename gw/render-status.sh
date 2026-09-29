#!/usr/bin/env bash
# gw/html/pre-status.html 을 재생성한다 — 현재 어떤 OSiris 플랫폼 컴포넌트가
# 떠 있는지 보여주는 최소 정적 진단 페이지.
#
# I5(2차 수정, 사용자 지시): 이 페이지는 더 이상 `/`(루트)가 아니라
# `/pre-status`에서 서빙된다 — `/`는 이제 콘솔 SPA가 차지한다(이미지 레이어에
# 구워진 `/usr/share/nginx/console`, gw/Dockerfile 참고). 콘솔이 서기 전까지는
# `gw/html/index.html`의 "콘솔 준비 중" 랜딩이 그 자리를 대신했으나, 콘솔 배선이
# 끝나면서 서빙되지 않는 고아 파일이 되어 삭제했다.
# `gw/conf.d/default.conf`가 두 경로를 각각 라우팅한다.
#
# 이 페이지는 정적이다: osiris-gw는 격리 요구(§2.6) 때문에 osiris-data/
# osiris-agents 네트워크에 붙어 있지 않으므로, 브라우저에서 매번 각 서비스의
# 헬스를 실시간으로 조회할 수 없다(그렇게 하려면 osiris-gw를 이중 홈 이상으로
# 넓혀야 하고, 이는 DMZ 패턴을 어기는 것이다). 대신 "호스트에서 docker를 볼 수
# 있는" osiris-up/이 스크립트가 스냅샷을 찍어 정적 HTML로 굽는다.
#
# 콘솔 SPA가 `/`를 채운 뒤에도 이 진단 페이지는 `/pre-status`에 남겨 뒀다 —
# osiris-gw의 정적 파일이라 osiris-api·콘솔·다른 어떤 컨테이너가 죽어 있어도
# 응답한다. 즉 "콘솔이 안 뜬다"는 상황에서 원인을 볼 수 있는 유일한 화면이라
# 운영 진단용으로 계속 쓸모가 있다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/gw/html/pre-status.html"

status_of() {
  local name="$1"
  if ! docker inspect "$name" >/dev/null 2>&1; then
    echo "not-created"
    return
  fi
  local health
  health="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$name" 2>/dev/null || echo unknown)"
  echo "$health"
}

badge() {
  case "$1" in
    healthy|running) echo "up" ;;
    starting) echo "starting" ;;
    not-created) echo "pending" ;;
    *) echo "down" ;;
  esac
}

row() {
  local label="$2" name="$1" note="$3"
  local st badgecls
  st="$(status_of "$name")"
  badgecls="$(badge "$st")"
  printf '<tr><td>%s</td><td><code>%s</code></td><td><span class="pill %s">%s</span></td><td>%s</td></tr>\n' \
    "$label" "$name" "$badgecls" "$st" "$note"
}

NOW="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

{
cat <<HTML
<!doctype html>
<html lang="ko">
<head>
<meta charset="utf-8">
<title>OSiris — platform status</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<!-- 브랜드 자산: brand/ 가 원본이고 gw/html/ 로 복사되어 서빙된다.
     재생성은 brand/build-brand.py (brand/README.md 참고). -->
<link rel="icon" href="/favicon.ico" sizes="16x16 32x32 48x48">
<link rel="icon" type="image/svg+xml" href="/favicon.svg">
<link rel="apple-touch-icon" href="/favicon-180.png">
<meta name="theme-color" content="#0F1419">
<style>
  :root { color-scheme: light dark; }
  body { font-family: -apple-system, Segoe UI, Helvetica, Arial, sans-serif;
         max-width: 860px; margin: 3rem auto; padding: 0 1.25rem;
         color: #1a1a1a; background: #fafafa; }
  @media (prefers-color-scheme: dark) {
    body { color: #e8e8e8; background: #111214; }
    table { background: #1a1b1e; }
    th, td { border-color: #333 !important; }
    code { background: #26282c !important; color: #e8e8e8 !important; }
  }
  h1 { font-size: 1.4rem; margin-bottom: .2rem; }
  .sub { color: #888; font-size: .9rem; margin-bottom: 1.5rem; }
  table { width: 100%; border-collapse: collapse; background: #fff; }
  th, td { text-align: left; padding: .5rem .7rem; border-bottom: 1px solid #ddd; font-size: .92rem; }
  th { font-size: .78rem; text-transform: uppercase; letter-spacing: .04em; color: #888; }
  code { background: #f0f0f0; padding: .1rem .35rem; border-radius: 4px; font-size: .85rem; }
  .pill { display: inline-block; padding: .12rem .55rem; border-radius: 999px; font-size: .78rem; font-weight: 600; }
  .pill.up       { background: #d9f2df; color: #196f34; }
  .pill.starting { background: #fff3cd; color: #8a6400; }
  .pill.pending  { background: #e6e6e6; color: #666; }
  .pill.down     { background: #fbdada; color: #a1231f; }
  .note { color: #888; }
  footer { margin-top: 2rem; font-size: .8rem; color: #999; }
</style>
</head>
<body>
<h1>OSiris — platform status</h1>
<div class="sub">진단용 정적 스냅샷 — <code>/pre-status</code>에서 서빙된다(콘솔이 서게 될
<code>/</code> 자리는 별도의 "콘솔 준비 중" 랜딩이 차지한다). 다음 워커가
콘솔을 구현하면 <code>gw/conf.d/default.conf</code>의 <code>location /</code>을
실제 콘솔 SPA 프록시로 되돌린다 — 이 진단 페이지는 남겨도, 지워도 된다.
스냅샷 생성 시각: ${NOW}</div>
<p><a href="/omni/">→ omniroute 관리 UI (osiris-omniroute)</a></p>

<table>
<thead><tr><th>컴포넌트</th><th>컨테이너</th><th>상태</th><th>비고</th></tr></thead>
<tbody>
$(row osiris-gw            "osiris-gw"            "인바운드 이음새(nginx). 이 페이지를 서빙 중")
$(row osiris-neo4j         "osiris-neo4j"         "온톨로지 — 조직 의도의 유일한 진실")
$(row osiris-nats          "osiris-nats"          "JetStream — 인박스/이벤트/트랜스크립트/비용")
$(row osiris-egress        "osiris-egress"        "HTTP CONNECT 프록시, 도메인 허용목록")
$(row osiris-omniroute     "osiris-omniroute"     "전용 LLM 게이트웨이(I5). /omni/ 에서 접근, 구성은 온보딩 시점")
$(row osiris-api           "osiris-api"           "콘솔 REST/SSE, 온보딩 0단계(/_api/onboarding/*), D19 SSE 재개")
$(row osiris-core          "osiris-core"          "graph-write 단일 관문 (배정 엔진은 아직 다음 워커)")
$(row osiris-orchestrator  "osiris-orchestrator"  "에이전트 생명주기(Docker Engine API), 수렴 루프")
</tbody>
</table>

<footer>OSiris 1단계(docker-compose) · docs/design/10-core-architecture.md 기준 구현</footer>
</body>
</html>
HTML
} > "$OUT"

echo "[render-status] wrote $OUT"
