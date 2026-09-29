#!/usr/bin/env bash
# bin/issue-infra-mcp-token.sh — issue an `osm_…` machine principal token
# scoped to org.read.ops, for the mcp__infra__* MCP server
# (mcp/infra/, docs/design/90-infra-topology.md §7.2 B단계).
#
# WHY THIS IS A SCRIPT, NOT SOMETHING RUN FOR YOU: POST /api/machine-principals
# is PersonOnly (api/internal/api/server.go) — only an authenticated PERSON
# session may issue a machine principal, and issuing an org-class one
# additionally requires that person to hold org.admin
# (api/internal/machineauth/handlers.go). No agent, script, or MCP server can
# satisfy that on its own; a human with an admin console session has to run
# this (or the equivalent curl) themselves.
#
# WHAT THIS SCRIPT DOES: prints (and, if a session cookie is available,
# executes) the exact request that issues the token. It never invents a
# session — it either uses one you already hold (OSIRIS_SESSION_COOKIE) or
# just shows you the curl to run once you have one.
#
# The response's `token` field is shown EXACTLY ONCE by osiris-api — it is
# not stored server-side in recoverable form and not re-readable later
# (api/internal/machineauth/handlers.go's issueResp.Warning says the same).
# Copy it straight into the MCP server's env (OSIRIS_INFRA_TOKEN) or a secret
# store — not into shell history, a log, or a file in this repo.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

GW_PORT="18280"
if [ -f "$ROOT/.env" ]; then
  ENV_PORT="$(grep -E '^OSIRIS_GW_HOST_PORT=' "$ROOT/.env" | head -1 | cut -d= -f2- || true)"
  [ -n "$ENV_PORT" ] && GW_PORT="$ENV_PORT"
fi

BASE_URL="${OSIRIS_BASE_URL:-http://127.0.0.1:${GW_PORT}}"
PRINCIPAL_ID="${OSIRIS_INFRA_PRINCIPAL_ID:-mcp-infra}"
# §19.4: TTL can only ever TIGHTEN the class ceiling (clampTTL), never extend
# it — 90d here is a request, not a guarantee. Omit to take osiris-api's
# org-class default if you don't want to think about it.
TTL_SECONDS="${OSIRIS_INFRA_TOKEN_TTL_SECONDS:-7776000}" # 90d

BODY=$(cat <<JSON
{"principalId":"${PRINCIPAL_ID}","permissions":["org.read.ops"],"ttlSeconds":${TTL_SECONDS}}
JSON
)

echo "This issues an org-class machine principal scoped to org.read.ops," >&2
echo "for the mcp__infra__* MCP server. It requires a PERSON session that" >&2
echo "holds org.admin (POST /api/machine-principals -> org-class branch)." >&2
echo >&2

if [ -n "${OSIRIS_SESSION_COOKIE:-}" ]; then
  echo "OSIRIS_SESSION_COOKIE is set — issuing now against ${BASE_URL} ..." >&2
  curl -sS -X POST "${BASE_URL}/_api/machine-principals" \
    -H 'Content-Type: application/json' \
    -H "Cookie: ${OSIRIS_SESSION_COOKIE}" \
    --data-raw "${BODY}"
  echo >&2
  echo "^ 'token' above is shown exactly once. Put it in the MCP server's" >&2
  echo "  OSIRIS_INFRA_TOKEN env — do not log it or commit it." >&2
else
  echo "No OSIRIS_SESSION_COOKIE in the environment, so nothing was sent." >&2
  echo "Run this once you have a person session with org.admin (log into the" >&2
  echo "osiris console, copy its session cookie), either:" >&2
  echo >&2
  echo "  (a) export OSIRIS_SESSION_COOKIE='osiris_session=<value>' and re-run" >&2
  echo "      this script, or" >&2
  echo "  (b) run the equivalent curl yourself, e.g. from a browser devtools" >&2
  echo "      \"copy as curl\" on a request to ${BASE_URL}/_api/whoami:" >&2
  echo >&2
  echo "  curl -sS -X POST '${BASE_URL}/_api/machine-principals' \\" >&2
  echo "    -H 'Content-Type: application/json' \\" >&2
  echo "    -H 'Cookie: <your osiris session cookie>' \\" >&2
  echo "    --data-raw '${BODY}'" >&2
  echo >&2
  echo "The JSON response's \"token\" field (osm_…) is the value for" >&2
  echo "mcp/infra's OSIRIS_INFRA_TOKEN env var. It is shown exactly once." >&2
fi
