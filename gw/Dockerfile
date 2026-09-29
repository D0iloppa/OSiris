# osiris-gw image — bakes the console SPA's build OUTPUT into the image
# layer (docs/design/IMPLEMENTATION-NOTES.md, console-wiring worker).
#
# WHY BAKE INTO THE IMAGE RATHER THAN BIND-MOUNT console/dist FROM THE HOST:
# this framework's portability contract is "어디서든 install osiris" — a
# fresh host running `docker compose up -d` must not need Node/npm
# pre-installed, nor a prior manual `npm run build` in console/. A
# multi-stage build performs that build INSIDE the image build step, so
# `docker compose up -d --build` (or a plain `up -d` against an
# already-built image) alone produces a working console. This also survives
# `docker compose down && up -d`: as long as the image itself isn't removed,
# the baked-in dist/ persists across that cycle exactly like every other
# osiris-* image (core/orchestrator/api) already does — no different from
# how those are built once and reused.
#
# gw/conf.d and gw/html remain host bind-mounts (see docker-compose.yml) so
# they stay hand-editable without a rebuild — only the console's build
# OUTPUT is baked in, at /usr/share/nginx/console, a path distinct from
# gw/html's own bind-mounted /usr/share/nginx/html so neither shadows the
# other (see gw/conf.d/default.conf's location blocks for how the two roots
# are split across paths).
#
# BUILD CONTEXT: like api/Dockerfile, this needs `context: .` (repo root)
# because it COPYs console/ from outside gw/ — every path below is relative
# to the repo root, not to this file's directory.

FROM node:22.14.0-alpine3.21 AS console-build
WORKDIR /console
COPY console/package.json console/package-lock.json ./
RUN npm ci
COPY console/index.html console/vite.config.ts console/tsconfig.json console/tsconfig.app.json console/tsconfig.node.json ./
COPY console/public ./public
COPY console/src ./src
# VITE_API_MODE defaults to "real" for the image this compose file actually
# runs — the shipped console talks to the real osiris-api by default (see
# console/src/api/config.ts's USE_MOCK_API). Mock mode stays available for
# `npm run dev` on a developer's own machine (console/README.md); it has no
# reason to be the default for the framework's own served build.
ARG VITE_API_MODE=real
ENV VITE_API_MODE=$VITE_API_MODE
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=console-build /console/dist /usr/share/nginx/console
