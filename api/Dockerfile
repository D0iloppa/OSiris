# osiris-api — console REST/SSE service.
#
# BUILD CONTEXT NOTE: unlike core/Dockerfile and orchestrator/Dockerfile
# (context: ./core, ./orchestrator respectively), this image needs a file
# that lives OUTSIDE the api/ directory — omniroute/operations.mjs, the
# single source of truth for the omniroute wire contract (see
# internal/omniroute/ops.go's package doc for why it is not vendored/copied
# into api/ instead: that would create exactly the two-copies-of-the-same-
# contract risk the subprocess decision was made to avoid). So
# docker-compose.yml builds this image with `context: .` (repo root) and
# `dockerfile: api/Dockerfile` — every COPY path below is relative to the
# repo root, not to this file's directory.
#
# UNLIKE osiris-core/osiris-orchestrator, this image is NOT `FROM scratch`.
# See internal/omniroute/ops.go's package doc for the full decision record
# (Node subprocess vs. Go reimplementation of omniroute/operations.mjs's
# wire protocol). This Dockerfile is where that decision's cost is paid and
# where it is kept as small as the decision allows:
#
#   - operations.mjs has ZERO npm dependencies (only node:fs/node:url/
#     node:path and the built-in fetch — verified below at build time: the
#     build FAILS if a package.json/node_modules turns up next to it, which
#     would mean someone added a dependency without updating this
#     Dockerfile's assumptions).
#   - No npm registry access at runtime — nothing is `npm install`ed into
#     the final image, ever. The only two files added on top of the base
#     Node image are this service's own Go binary and operations.mjs.
#   - A4 is still honored: no `claude` binary, no LLM gateway credentials
#     anywhere in this image or its build context.
#   - Non-root (base image's built-in `node` user, uid 1000 — see
#     docker-compose.yml's osiris-api service for the matching
#     cap_drop/no-new-privileges/read_only hardening, same posture as the
#     agent runtime and consistent with G8: no docker.sock, ever).

FROM golang:1.22.12-alpine3.20 AS build
WORKDIR /src
COPY api/go.mod api/go.sum ./
RUN go mod download
COPY api/ .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/osiris-api ./cmd/osiris-api

# Guard against operations.mjs quietly growing a dependency this image
# doesn't install — see header comment. This stage's only job is to fail
# the build loudly if that ever happens.
FROM node:22.14.0-alpine3.21 AS depcheck
COPY omniroute/operations.mjs /check/operations.mjs
RUN if [ -f /check/package.json ] || [ -d /check/node_modules ]; then \
      echo "operations.mjs now has npm dependencies — api/Dockerfile must be updated to npm-install them at build time" >&2; \
      exit 1; \
    fi

FROM node:22.14.0-alpine3.21
WORKDIR /app
COPY --from=build /out/osiris-api /osiris-api
COPY --from=depcheck /check/operations.mjs /app/omniroute/operations.mjs
# config.json is operations.mjs's own default-alias-mapping source (§9.4/
# §9.6 — "프레임워크 기본값", never auto-applied, only ever read by
# getDefaultAliasMapping()). operations.mjs resolves this path relative to
# its OWN location (REPO_ROOT = dirname(__dirname)), so it must land at
# exactly /app/omniroute/config.json, next to the script.
COPY omniroute/config.json /app/omniroute/config.json

USER node
EXPOSE 8081 8443
HEALTHCHECK --interval=10s --timeout=3s --retries=3 --start-period=15s \
  CMD ["/osiris-api", "-healthcheck"]
ENTRYPOINT ["/osiris-api"]
