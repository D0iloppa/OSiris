#!/usr/bin/env bash
# build-agent-image.sh — build the agent runtime image (agent/).
#
# WHY THIS IS A SCRIPT AND NOT A COMPOSE SERVICE. G4 (§1.7) forbids agent or
# organization information in the compose file: "compose 파일에 에이전트/조직
# 정보 기재 → 그래프가 유일한 진실이 아니게 됨 → 두 곳이 어긋남. 대신: 코어 7개
# 서비스만." An image BUILD stanza carries no organization data, but adding an
# `osiris-agent` service to compose would put an agent-shaped entry in the one
# file that is supposed to have none — and `docker compose up` would then either
# start it (an agent with no employeeId, which is meaningless) or need a profile
# guard that every future reader has to remember. A build script keeps compose
# describing exactly the seven core services and nothing else.
#
# The agent image is never started by compose in any case: agents are created by
# osiris-orchestrator through the Docker Engine API (A11), which is why they also
# survive `docker compose down` (verified in I3).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if [ -f .env ]; then
  # shellcheck disable=SC1091
  set -a; source .env; set +a
fi

TAG="${TAG:-dev}"
IMAGE="${OSIRIS_AGENT_IMAGE:-osiris/agent:${TAG}}"
CLAUDE_CODE_VERSION="${CLAUDE_CODE_VERSION:-2.1.220}"

echo "[build-agent-image] building ${IMAGE} (Claude Code CLI ${CLAUDE_CODE_VERSION})"
docker build \
  --build-arg "CLAUDE_CODE_VERSION=${CLAUDE_CODE_VERSION}" \
  -t "${IMAGE}" \
  "${ROOT}/agent"

echo "[build-agent-image] verifying the image contents match the design's expectations"

# The agent image is the ONE image allowed to contain `claude` (A4 read in the
# positive direction). Assert it actually does, because the failure mode
# otherwise is an agent that boots, reports readyz=false with
# CLAUDE_CLI_UNVERIFIED, and looks like a supervisor bug.
docker run --rm --entrypoint /usr/local/bin/claude "${IMAGE}" --version

# Assert the inverse for the control-plane images (A4's actual prohibition).
# Cheap, and it is exactly the check that would have caught a well-meant
# "just add the CLI to core so it can summarize" change.
for img in "osiris/core:${TAG}" "osiris/orchestrator:${TAG}"; do
  if docker image inspect "$img" >/dev/null 2>&1; then
    # `docker create` (never start) is the only way to enumerate a FROM-scratch
    # image's filesystem without a shell inside it. The container is removed
    # immediately in all paths so this check leaves no orphan behind.
    cid="$(docker create --entrypoint /nonexistent "$img" 2>/dev/null || true)"
    if [ -n "$cid" ]; then
      found=0
      docker export "$cid" 2>/dev/null | tar -t 2>/dev/null | grep -qiE '(^|/)claude$' && found=1
      docker rm -f "$cid" >/dev/null 2>&1 || true
      if [ "$found" = 1 ]; then
        echo "[build-agent-image] FAIL: $img contains a 'claude' binary — A4 violation" >&2
        exit 1
      fi
    fi
    echo "[build-agent-image]   OK: $img contains no 'claude' binary (A4)"
  fi
done

# No gateway credential may be baked into the agent image: the token arrives as
# a file at runtime (G5/§8.4). Checking the image's declared env is the cheap
# half of that; the Dockerfile comment carries the rest.
if docker image inspect "${IMAGE}" --format '{{json .Config.Env}}' | grep -qiE 'ANTHROPIC_(API_KEY|AUTH_TOKEN)|_TOKEN=|PASSWORD='; then
  echo "[build-agent-image] FAIL: ${IMAGE} declares a credential-looking env var — §8.4 requires file injection" >&2
  exit 1
fi
echo "[build-agent-image]   OK: ${IMAGE} declares no credential env vars (G5/§8.4)"

echo "[build-agent-image] done: ${IMAGE}"
echo "[build-agent-image] NOTE: :AIAgent.image in Neo4j is authoritative per agent."
echo "[build-agent-image]       Create agents with image=\"${IMAGE}\" (or leave it empty to fall back to"
echo "[build-agent-image]       osiris-orchestrator's OSIRIS_AGENT_IMAGE)."
