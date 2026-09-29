# osiris-omniroute — 소스 재빌드로 OMNIROUTE_BASE_PATH=/omni 를 빌드 타임에 굽는다.
#
# 배경(docs/design/10-core-architecture.md §9.7, IMPLEMENTATION-NOTES.md I5):
# 벤더 이미지는 Next.js `output: "standalone"`으로 빌드되어 있고, `basePath`는
# Next.js의 성질상 `next build` 시점에만 정해진다 — 배포된 이미지에 런타임
# `OMNIROUTE_BASE_PATH` env를 줘도 반영되지 않는다(실측 확인: 컨테이너를
# 그 env로 띄워도 `/omni/`는 404, `/`는 200으로 응답). 서브패스
# (`<origin>/omni/`)가 요구사항으로 승격되어(사용자 확정), 그 값을
# 빌드에 실제로 반영하는 유일한 방법은 다시 빌드하는 것뿐이다.
#
# **소스는 별도로 git clone하지 않는다.** 벤더의 다이제스트 고정 이미지
# 자체에 전체 소스 트리(src/, open-sse/, package.json, package-lock.json,
# scripts/build/, 심지어 벤더 자신의 Dockerfile까지)가 들어 있다 — 이
# Dockerfile은 그 다이제스트 하나만을 참조해 재현 가능하다(다른 워커가
# 다시 실행해도 같은 결과). 아래 빌더 스테이지는 벤더의 Dockerfile
# (컨테이너 내부 /app/Dockerfile, 이 파일을 쓸 때 그대로 옮겨 적었다)의
# builder 스테이지와 동일한 레시피 — 딱 한 줄(OMNIROUTE_BASE_PATH)만 다르다.
#
# 왜 벤더 이미지의 node_modules를 그대로 못 쓰는가(실측): standalone 런타임
# 이미지의 node_modules는 프로덕션 서버 실행에 필요한 것만 트레이싱되어
# 있고 `next/dist/bin/next`(빌드 컴파일러 자체)는 빠져 있다 — 실측:
# 벤더 이미지 안에서 그대로 `npm run build`를 실행하면
# "Cannot find module '/app/node_modules/next/dist/bin/next'"로 즉시 죽는다.
# 그래서 이 Dockerfile은 벤더의 package-lock.json으로 **새로** `npm ci`를
# 돌려 완전한 빌드 툴체인을 확보한 뒤, 소스(node_modules 제외)만 벤더에서
# 덮어씌운다.
ARG VENDOR_DIGEST=diegosouzapw/omniroute@sha256:badb560971fdc23c2fb84b3e8695116239ff215b4cca4b07076201a8efae7f0d
FROM ${VENDOR_DIGEST} AS vendor

# node_modules/.build은 여기서 지운다 — builder 스테이지가 새로 만든
# node_modules를 이 스테이지에서 나중에 COPY할 때 덮어쓰지 않기 위해서다.
FROM vendor AS vendor-src
USER root
RUN rm -rf /app/node_modules /app/.build

# ── 아래부터는 벤더 Dockerfile의 base/builder 스테이지를 그대로 재현 ──────
FROM node:24-trixie-slim AS base
WORKDIR /app
RUN --mount=type=cache,id=osiris-omni-apt-cache,target=/var/cache/apt,sharing=shared \
  --mount=type=cache,id=osiris-omni-apt-lists,target=/var/lib/apt/lists,sharing=shared \
  apt-get update \
  && apt-get upgrade -y \
  && apt-get install -y --no-install-recommends libsecret-1-0 ca-certificates \
  && rm -rf /var/lib/apt/lists/*
RUN npm install -g npm@latest && npm cache clean --force

FROM base AS builder
RUN --mount=type=cache,id=osiris-omni-apt-cache,target=/var/cache/apt,sharing=shared \
  --mount=type=cache,id=osiris-omni-apt-lists,target=/var/lib/apt/lists,sharing=shared \
  apt-get update \
  && apt-get install -y --no-install-recommends python3 make g++ \
  && rm -rf /var/lib/apt/lists/*

COPY --from=vendor /app/package*.json ./
COPY --from=vendor /app/open-sse/package.json ./open-sse/package.json
COPY --from=vendor /app/scripts/build/postinstall.mjs ./scripts/build/postinstall.mjs
COPY --from=vendor /app/scripts/build/postinstallSupport.mjs ./scripts/build/postinstallSupport.mjs
COPY --from=vendor /app/scripts/build/native-binary-compat.mjs ./scripts/build/native-binary-compat.mjs
ENV NPM_CONFIG_LEGACY_PEER_DEPS=true
RUN test -f package-lock.json \
  || (echo "package-lock.json is required for reproducible Docker builds" >&2 && exit 1)
RUN --mount=type=cache,id=osiris-omni-npm-cache,target=/root/.npm \
  npm ci --no-audit --no-fund --legacy-peer-deps --ignore-scripts \
  && (cd node_modules/better-sqlite3 \
      && node /usr/local/lib/node_modules/npm/node_modules/node-gyp/bin/node-gyp.js rebuild) \
  && node -e "require('better-sqlite3')(':memory:').close()"

ENV OMNIROUTE_USE_TURBOPACK=1
ENV OMNIROUTE_MITM_STUB=1
ARG OMNIROUTE_BUILD_MEMORY_MB=4096
ENV NODE_OPTIONS="--max-old-space-size=${OMNIROUTE_BUILD_MEMORY_MB}"

# *** 이 한 줄이 벤더 Dockerfile과 다른 전부다 *** — §2.9 경로 네임스페이스
# (<origin>/omni/)를 빌드 타임에 굽는다.
ENV OMNIROUTE_BASE_PATH=/omni

COPY --from=vendor-src /app/ ./
RUN --mount=type=cache,id=osiris-omni-next-cache,target=/app/.build/next/cache \
  mkdir -p /app/data && npm run build

# ── 런너 스테이지 — 벤더 Dockerfile의 runner-base와 동일 ────────────────
FROM base AS runner-base

ENV NODE_ENV=production
ENV PORT=20128
ENV HOSTNAME=0.0.0.0
ENV OMNIROUTE_MEMORY_MB=1024
ENV NODE_OPTIONS="--max-old-space-size=${OMNIROUTE_MEMORY_MB}"
# 런타임에도 설정 — next.config.mjs의 basePath는 빌드 타임에 이미 굳었지만,
# 이 값을 참조하는 다른 런타임 코드 경로(예: 자기 자신을 가리키는 URL 조립)
# 가 있을 수 있어 빌드 타임 값과 일치시켜 둔다.
ENV OMNIROUTE_BASE_PATH=/omni

ENV DATA_DIR=/app/data
RUN mkdir -p /app/data

COPY --from=builder /app/.build/next/standalone ./
COPY --from=builder /app/node_modules/better-sqlite3 ./node_modules/better-sqlite3
ENV OMNIROUTE_MIGRATIONS_DIR=/app/migrations

COPY --from=builder /app/scripts/dev/healthcheck.mjs ./healthcheck.mjs

RUN chown -R node:node /app

EXPOSE 20128
USER node

COPY --from=vendor --chmod=755 /app/scripts/check-permissions.sh /tmp/check-permissions.sh
ENTRYPOINT ["/tmp/check-permissions.sh"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD ["node", "healthcheck.mjs"]

CMD ["node", "dev/run-standalone.mjs"]
