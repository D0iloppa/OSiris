# osiris-console — K3(70-kubernetes-deployment.md §3.3): "osiris-gw 폐지,
# 콘솔 SPA는 자기 파드(nginx)가 정적 자산만 서빙한다." 경로 분기
# (`/_api/*`·`/omni/*`)·TLS 종단·SSE 패스스루는 Ingress Controller가
# 맡는다 — 그래서 이 이미지는 gw/Dockerfile과 달리 **라우팅 conf를 굽지
# 않는다**. gw/conf.d/default.conf의 proxy_pass 블록(`/_api/`, `/omni/`,
# catch-all)은 옮기지 않았다 — 그건 Ingress의 일이다(K3, 이 phase 범위
# 밖).
#
# 옮긴 것은 gw/conf.d/default.conf의 콘솔 **정적 서빙** 부분뿐이다
# (console/nginx.conf 참고, 각 location에 원문 출처를 주석으로 남겼다):
#   - `location = /` 의 SPA 진입 규칙 — 원문 그대로 `try_files /index.html
#     =404;`. (`try_files $uri $uri/ /index.html` 형태가 아니다 — 콘솔은
#     순수 React state 라우팅이라 `/`(정확히 그 경로) 밖의 URL을 쓰지
#     않는다, gw/conf.d/default.conf "콘솔 SPA 배선 완료" 주석 참고. gw
#     설정에 실제로 있는 표기를 그대로 옮겼다.)
#   - `location /assets/`, `location /brand/` — vite 빌드 산출물이 최상위
#     절대경로로 참조하는 두 접두사(해시 파일명이라 디렉터리 단위 예약).
#   - `location ~ ^/(favicon|osiris-mark)[^/]*$` — 콘솔이 참조하는 브랜드
#     정적 파일. gw/conf.d/default.conf가 브랜드 변형을 파일명으로
#     열거했다가 404 났던 사고(주석에 기록됨)와 같은 함정을 피하려고 같은
#     정규식을 그대로 옮겼다.
#   - **캐시 헤더는 옮기지 않았다** — gw/conf.d/default.conf에 애초에
#     Cache-Control/expires 지시자가 없다. 없는 것을 새로 발명하지 않는다.
#
# BUILD CONTEXT: gw/Dockerfile·api/Dockerfile과 달리 이 이미지는 console/
# 밖의 아무 파일도 필요로 하지 않는다(operations.mjs 같은 repo-root 공유
# 자산이 없음) — 그래서 빌드 컨텍스트는 `console/` 그 자체다:
#   docker build -f console/Dockerfile console/
#
# non-root 실행: api/Dockerfile(`USER node`, base 이미지 내장 non-root
# 사용자를 그대로 쓰고 새 사용자를 만들지 않는 방식)과 같은 원칙. 표준
# `nginx:*-alpine`은 80번 포트 바인딩에 root가 필요하므로, 같은 nginx
# official 계열의 unprivileged 변형(`nginxinc/nginx-unprivileged`, 내장
# `nginx` 사용자 uid 101, 기본 8080에서 non-root로 수신하도록 이미
# 구성됨)을 대신 쓴다 — 이 이미지 자신이 사용자를 만들지 않는다는 점에서
# api/Dockerfile과 같은 패턴이다.

FROM node:22.14.0-alpine3.21 AS console-build
WORKDIR /console
COPY package.json package-lock.json ./
RUN npm ci
COPY index.html vite.config.ts tsconfig.json tsconfig.app.json tsconfig.node.json ./
COPY public ./public
COPY src ./src
# gw/Dockerfile과 동일한 기본값 — 이 이미지가 서빙하는 콘솔은 real
# osiris-api를 향한다(console/src/api/config.ts's USE_MOCK_API 참고).
ARG VITE_API_MODE=real
ENV VITE_API_MODE=$VITE_API_MODE
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.27-alpine
COPY --from=console-build /console/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 8080
