#!/usr/bin/env bash
# verify-agent-runtime.sh — reproduces the §5 agent-runtime proofs the I4 build
# was accepted on, so a later worker can re-run them instead of trusting a report.
#
# It spends NO LLM tokens: a local mock gateway (test/mock-gateway) stands in for
# omniroute, and the stdout-flood case substitutes a flooding child
# (test/flood-child.sh) for `claude` via the supervisor's OSIRIS_CLAUDE_BIN knob.
# Everything else is real — the real agent image, the real supervisor as PID 1,
# the real health server, the real checkpoint/drain/resume code, and (for the
# long-turn case) the real Claude Code CLI driving a real 4-minute turn.
#
# What each section proves, in the design's own words:
#   1  idle -> no `claude` process at all           §11 issue 1 ("유휴 시 ... 토큰 0")
#   2  inbox message -> claude runs -> result       §5.2
#   3  long turn -> /livez stays 200, no restart    §5.4 (the central invariant)
#   4  stdout flood -> /livez stays 200, drops      §5.4 ("채널 만재 시 ... 버린다")
#   5  SIGTERM -> checkpoint -> resume              §5.5
#
# USAGE: bin/verify-agent-runtime.sh <employeeId>
#   The agent must already exist and be Running (create it through osiris-core's
#   graph-write API; this script never writes to Neo4j — A5/G7).
#
# SAFETY: every container this script creates carries the label
# osiris.dev/i4-verify and is removed on exit. It never touches a container it
# did not create, and never touches anything outside the osiris-* namespace.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
set -a; source .env; set +a

EMP="${1:-}"
[ -n "$EMP" ] || { echo "usage: $0 <employeeId>" >&2; exit 2; }

CT="osiris-agent-${EMP}"
NET="$(docker network ls --format '{{.Name}}' | grep -E '(^|_)osiris-agents$' | head -1)"
PASS=0; FAIL=0
ok()   { echo "PASS: $*"; PASS=$((PASS+1)); }
bad()  { echo "FAIL: $*" >&2; FAIL=$((FAIL+1)); }
info() { echo "  ... $*"; }

cleanup() {
  docker rm -f osiris-i4v-mockgw osiris-i4v-poller osiris-i4v-flood >/dev/null 2>&1 || true
  docker volume rm osiris-i4v-flood-ws >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

docker inspect "$CT" >/dev/null 2>&1 || { echo "ERROR: $CT does not exist — create the agent via osiris-core first" >&2; exit 1; }

# The inbox subject is read from the container's own environment rather than
# reconstructed here, so this script cannot disagree with what the driver
# actually configured (team slug != team id in general — §7.1).
INBOX_SUBJECT="$(docker inspect "$CT" --format '{{range .Config.Env}}{{println .}}{{end}}' | grep '^OSIRIS_INBOX_SUBJECT=' | cut -d= -f2)"

pub() { # publish one inbox message using the agent's OWN file-injected NATS creds
  local body="$1" u p
  u="$(cat "state/creds/${EMP}/nats_user")"; p="$(cat "state/creds/${EMP}/nats_password")"
  docker run --rm --network "$NET" natsio/nats-box:0.14.5 \
    nats -s "nats://${u}:${p}@nats:4222" pub "$INBOX_SUBJECT" "$body" >/dev/null 2>&1
}

echo "=== building fixtures ==="
docker build -q -t osiris/mock-gateway:dev "$ROOT/test/mock-gateway" >/dev/null || { echo "mock gateway build failed" >&2; exit 1; }

start_mock() { # start_mock <total_ms> <chunks>
  docker rm -f osiris-i4v-mockgw >/dev/null 2>&1
  docker run -d --name osiris-i4v-mockgw --network "$NET" --network-alias mockgw \
    --label osiris.dev/i4-verify=mock-gateway \
    -e MOCK_TOTAL_MS="$1" -e MOCK_CHUNKS="$2" -e MOCK_CHUNK_BYTES=0 -e MOCK_PING_MS=1000 \
    osiris/mock-gateway:dev >/dev/null
}

# ---------------------------------------------------------------------------
echo; echo "=== 1. idle: no claude process (the basis of 'idle token cost = 0') ==="
# ---------------------------------------------------------------------------
PROCS="$(docker top "$CT" 2>/dev/null | tail -n +2 | wc -l)"
CLAUDES="$(docker top "$CT" 2>/dev/null | grep -c 'claude' || true)"
info "process count=$PROCS claude-matching=$CLAUDES"
if [ "$CLAUDES" -eq 0 ]; then ok "idle agent runs no claude process (§11 issue 1)"
else bad "idle agent has $CLAUDES claude process(es) — idle token cost is not 0"; fi

# ---------------------------------------------------------------------------
echo; echo "=== 2/3. long turn: /livez stays 200 and the container is not restarted ==="
# ---------------------------------------------------------------------------
echo "NOTE: this section needs the agent's gateway_base_url to point at the mock."
echo "      Set OSIRIS_GATEWAY_BASE_URL=http://mockgw:8080, OSIRIS_GATEWAY_HEALTH_PATH=/healthz"
echo "      and OSIRIS_AGENT_EXTRA_NO_PROXY=mockgw in .env, then"
echo "      'docker compose up -d osiris-orchestrator' — the rotated credential files reach"
echo "      the RUNNING container within one reconcile tick and no restart occurs (§8.4)."
if ! grep -q '^OSIRIS_GATEWAY_BASE_URL=http://mockgw' .env; then
  echo "SKIP: .env does not point at the mock gateway; sections 2/3 skipped."
else
  start_mock 270000 9
  ID_BEFORE="$(docker inspect -f '{{.Id}}' "$CT")"
  RC_BEFORE="$(docker inspect -f '{{.RestartCount}}' "$CT")"
  docker run -d --name osiris-i4v-poller --network "container:${ID_BEFORE}" \
    --label osiris.dev/i4-verify=poller --entrypoint sh curlimages/curl:8.10.1 -c \
    'i=0; while true; do i=$((i+1)); d=$(curl -s -m 3 -w " HTTP=%{http_code}" http://127.0.0.1:8081/livez | tr -d "\n"); echo "poll#$i $d"; sleep 1; done' >/dev/null
  pub '{"taskId":"t-verify-long","prompt":"long-turn health invariant test"}'
  info "waiting for the turn to complete (about 4.5 minutes)"
  for _ in $(seq 1 60); do
    docker logs "$CT" 2>&1 | grep -q 'taskId=t-verify-long status=' && break
    sleep 10
  done
  CODES="$(docker logs osiris-i4v-poller 2>&1 | grep -o 'HTTP=[0-9]*' | sort -u | tr '\n' ' ')"
  NPOLL="$(docker logs osiris-i4v-poller 2>&1 | grep -c 'poll#')"
  ID_AFTER="$(docker inspect -f '{{.Id}}' "$CT")"
  RC_AFTER="$(docker inspect -f '{{.RestartCount}}' "$CT")"
  info "polls=$NPOLL distinct codes='$CODES' restarts ${RC_BEFORE}->${RC_AFTER}"
  docker logs "$CT" 2>&1 | grep 't-verify-long' | tail -2
  [ "$CODES" = "HTTP=200 " ] && ok "every /livez poll during the long turn returned 200" \
                             || bad "non-200 /livez during the long turn: $CODES"
  [ "$ID_BEFORE" = "$ID_AFTER" ] && ok "container identity unchanged across the long turn" \
                                 || bad "container was recreated during the long turn"
  [ "$RC_BEFORE" = "$RC_AFTER" ] && ok "RestartCount unchanged ($RC_AFTER)" \
                                 || bad "container restarted during the long turn"
  docker rm -f osiris-i4v-poller >/dev/null 2>&1
fi

# ---------------------------------------------------------------------------
echo; echo "=== 4. stdout flood: /livez stays 200 and log lines are dropped, not queued ==="
# ---------------------------------------------------------------------------
docker volume create osiris-i4v-flood-ws >/dev/null
docker run -d --name osiris-i4v-flood --label osiris.dev/i4-verify=flood-child \
  --network "$NET" --user 65532:65532 --read-only --cap-drop ALL \
  --security-opt no-new-privileges --memory 2048m --cpus 1 \
  -v osiris-i4v-flood-ws:/workspace \
  -v "${HOST_PROJECT_DIR}/state/creds/${EMP}:/var/run/osiris/creds:ro" \
  -v "${HOST_PROJECT_DIR}/state/config/${EMP}:/var/run/osiris/config:ro" \
  -v "${HOST_PROJECT_DIR}/test/flood-child.sh:/flood-child.sh:ro" \
  --tmpfs /tmp:size=512m \
  -e "OSIRIS_EMPLOYEE_ID=${EMP}" -e OSIRIS_TEAM_ID=team-alpha -e OSIRIS_TEAM_SLUG=alpha \
  -e OSIRIS_NATS_URL=nats://nats:4222 -e OSIRIS_EGRESS_PROXY=http://osiris-egress:3128 \
  -e OSIRIS_CLAUDE_BIN=/flood-child.sh \
  "${OSIRIS_AGENT_IMAGE:-osiris/agent:dev}" -one-shot "stdout flood invariant test" -one-shot-task-id t-verify-flood >/dev/null
FID="$(docker inspect -f '{{.Id}}' osiris-i4v-flood)"
docker run -d --name osiris-i4v-poller --network "container:${FID}" \
  --label osiris.dev/i4-verify=poller --entrypoint sh curlimages/curl:8.10.1 -c \
  'i=0; while true; do i=$((i+1)); d=$(curl -s -m 3 -w " HTTP=%{http_code}" http://127.0.0.1:8081/livez | tr -d "\n"); echo "poll#$i $d"; sleep 1; done' >/dev/null
info "flooding (8,000,000 lines)"
for _ in $(seq 1 60); do
  [ "$(docker inspect -f '{{.State.Status}}' osiris-i4v-flood)" != running ] && break
  sleep 5
done
FCODES="$(docker logs osiris-i4v-poller 2>&1 | grep -o 'HTTP=[0-9]*' | grep -v 'HTTP=000' | sort -u | tr '\n' ' ')"
DROPPED="$(docker logs osiris-i4v-poller 2>&1 | grep -o '"droppedLogLines":[0-9]*' | tail -1 | cut -d: -f2)"
info "distinct non-zero codes='$FCODES' finalDroppedLogLines=${DROPPED:-0}"
[ "$FCODES" = "HTTP=200 " ] && ok "every answered /livez poll during the flood returned 200" \
                            || bad "non-200 /livez during the flood: $FCODES"
[ "${DROPPED:-0}" -gt 0 ] && ok "log lines were dropped (${DROPPED}) rather than queued or blocking (§5.4)" \
                          || bad "droppedLogLines is 0 — the flood did not exercise the drop path"
docker rm -f osiris-i4v-poller osiris-i4v-flood >/dev/null 2>&1

# ---------------------------------------------------------------------------
echo; echo "=== 5. SIGTERM -> checkpoint -> resume ==="
# ---------------------------------------------------------------------------
echo "NOTE: set :AIAgent.drainSeconds to something small (20) through osiris-core"
echo "      before running this, or the drain grace makes the check take 10 minutes."
DS="$(docker inspect "$CT" --format '{{range .Config.Env}}{{println .}}{{end}}' | grep '^OSIRIS_DRAIN_SECONDS=' | cut -d= -f2)"
info "agent drainSeconds=$DS"
if ! grep -q '^OSIRIS_GATEWAY_BASE_URL=http://mockgw' .env; then
  echo "SKIP: needs the mock gateway (see section 2/3)."
elif [ "${DS:-600}" -gt 60 ]; then
  echo "SKIP: drainSeconds=$DS is too long for an automated check."
else
  start_mock 270000 9
  pub '{"taskId":"t-verify-sigterm","prompt":"turn that will be interrupted"}'
  for _ in $(seq 1 30); do docker top "$CT" 2>/dev/null | grep -q claude && break; sleep 2; done
  docker kill -s TERM "$CT" >/dev/null
  sleep 4
  CP="$(docker run --rm -v "osiris-ws-${EMP}:/w" --entrypoint sh alpine:3.20.3 -c 'cat /w/.osiris/state.json' 2>/dev/null)"
  echo "$CP" | grep -q '"status": "interrupted"' \
    && ok "checkpoint fsynced with status=interrupted within 4s of SIGTERM (§5.5 step 1)" \
    || bad "no interrupted checkpoint after SIGTERM: $CP"
  start_mock 0 2   # fast, so the resumed turn finishes quickly
  info "waiting for the drain escalation, the convergence recreate, and the resume"
  for _ in $(seq 1 40); do docker logs "$CT" 2>&1 | grep -q 'resume finished' && break; sleep 5; done
  docker logs "$CT" 2>&1 | grep -qE 'found interrupted checkpoint .*attempt=2' \
    && ok "the replacement container found the checkpoint and resumed at attempt=2 (§5.5)" \
    || bad "no resume observed"
  docker logs "$CT" 2>&1 | grep -q 'resume finished .*status=done' \
    && ok "the resumed turn completed" || bad "the resumed turn did not complete"
fi

echo; echo "=== $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ]
