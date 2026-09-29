# osiris-agent — THE agent runtime image (docs/design/10-core-architecture.md
# §5.1). Replaces the I3-era agent-stub entirely.
#
# ============================================================================
# A4, READ IN BOTH DIRECTIONS
#   osiris-core / osiris-orchestrator: `FROM scratch`, no `claude`, no gateway
#     credential — structurally, not by promise (A4: "배정 로직은 LLM이 있는
#     프로세스에 존재하지 않는다").
#   THIS image: the ONE place `claude` is allowed to exist. That asymmetry is
#     what makes A4 meaningful. Nothing about this image weakens it — this
#     container holds no docker.sock (G8), reaches only nats /
#     osiris-core:8090 / osiris-egress:3128 (§8.3), and cannot write Neo4j
#     except through core's validated API (A5).
#
# NO GATEWAY CREDENTIAL IS BAKED IN. The token arrives at runtime as a FILE at
# /var/run/osiris/creds/gateway_token (G5, §8.4) and is injected into the
# CHILD's environment only, for the duration of one turn (§5.2). It is never in
# this image, never in compose `environment:`, and never in the supervisor's own
# /proc/<pid>/environ.
# ============================================================================
#
# Every tag is pinned explicitly — no floating `latest` (repo convention,
# IMPLEMENTATION-NOTES.md #1). That includes the Claude Code CLI version:
# an agent whose tool surface silently changes under it is an agent whose
# rendered `--allowedTools` list no longer means what the graph said it meant.

# --- 1. supervisor binary (static, no cgo) ---------------------------------
FROM golang:1.22.12-alpine3.20 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
      -o /out/osiris-supervisor ./cmd/osiris-supervisor

# --- 2. Claude Code CLI ----------------------------------------------------
# Installed into a self-contained prefix so it can be copied into the final
# stage without dragging npm's cache or a writable global tree along.
#
# Debian (bookworm-slim) rather than Alpine on purpose: Claude Code ships
# prebuilt native helpers (ripgrep et al) built against glibc, and a musl host
# turns that into a runtime failure inside a tool call — i.e. a failure that
# only shows up once an agent is doing real work. The extra ~40MB is worth not
# having that class of bug.
FROM node:20.20.2-bookworm-slim AS claude
ARG CLAUDE_CODE_VERSION=2.1.220
RUN npm install -g --prefix /opt/claude "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
 && /opt/claude/bin/claude --version \
 && npm cache clean --force

# --- 3. runtime ------------------------------------------------------------
FROM node:20.20.2-bookworm-slim

# git: needed by the workspace-integrity check (§5.5 resume step 1) and by any
#      granted git tool. ca-certificates: TLS to the egress proxy's targets.
# Nothing else is installed — every additional binary is another thing an
# LLM-driven Bash call can reach for.
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*

COPY --from=claude /opt/claude /opt/claude
RUN ln -s /opt/claude/bin/claude /usr/local/bin/claude

COPY --from=build /out/osiris-supervisor /osiris-supervisor

# Non-root by construction (§5.1/§8.3 runAsNonRoot), UID/GID 65532 matching
# orchestrator's driver/docker agentUID/agentGID constants.
#
# /workspace is created AND chowned HERE, at build time, so that when the
# orchestrator mounts a FRESH named volume at /workspace, Docker seeds the
# empty volume's ownership from this image's mount point (documented Docker
# behaviour, verified in I3). Without it, a non-root container with a
# read-only rootfs gets "permission denied" writing into its own workspace and
# every readiness probe fails for a reason that looks like a bug in the
# supervisor. Same for the mount points of the two read-only credential/config
# binds — they must exist in a read-only rootfs.
RUN mkdir -p /workspace /var/run/osiris/creds /var/run/osiris/config \
 && chown -R 65532:65532 /workspace \
 && chmod 0755 /var/run/osiris /var/run/osiris/creds /var/run/osiris/config

USER 65532:65532

# HOME must be inside the writable volume: the rootfs is read-only (§8.3) and
# node/claude will try to write a home directory. CLAUDE_CONFIG_DIR is set for
# the supervisor's own `claude --version` boot check; the child gets its own
# explicit copy of these (see supervisor/task.go childEnv) rather than
# inheriting, so this is belt-and-braces, not the mechanism.
ENV HOME=/workspace \
    CLAUDE_CONFIG_DIR=/workspace/.claude \
    TMPDIR=/tmp \
    NODE_OPTIONS=--max-old-space-size=1024 \
    DISABLE_AUTOUPDATER=1 \
    DISABLE_TELEMETRY=1 \
    DISABLE_ERROR_REPORTING=1

EXPOSE 8081

# HEALTHCHECK targets /livez ONLY (never /readyz) — Docker health drives
# restarts via the orchestrator's 3-consecutive-unhealthy enforcement (§5.4),
# and readiness must never cause one. `--start-period=20s` covers node's
# startup plus the one-time `claude --version` check.
HEALTHCHECK --interval=10s --timeout=3s --retries=3 --start-period=20s \
  CMD ["/osiris-supervisor", "-healthcheck"]

ENTRYPOINT ["/osiris-supervisor"]
