#!/bin/sh
# flood-child.sh — TEST FIXTURE. Stands in for `claude` via the supervisor's
# OSIRIS_CLAUDE_BIN knob, and does one thing: dump an absurd amount of
# stream-json-shaped output at stdout as fast as the kernel will take it.
#
# WHAT IT PROVES, and why a real LLM cannot prove it better.
# §5.4's second implementation rule:
#
#	"헬스서버는 자식 stdout 파이프에 절대 블록되지 않는다: 별도 goroutine +
#	 버퍼드 채널, 채널 만재 시 로그 라인을 버린다 (로그 유실 < 재시작)."
#
# The failure this guards against has nothing to do with inference: it is a
# supervisor whose health server shares a fate with the child's pipe, so that a
# chatty child starves the probes and the container is restarted mid-work. What
# is required to test it is a child that writes far faster than the supervisor's
# log consumer can publish. A real `claude` turn produces at most a few hundred
# stream-json lines and could never reach the drop threshold — driving this with
# real inference would burn tokens AND fail to exercise the code path.
#
# Everything else in the container is real: the real agent image, the real
# supervisor binary as PID 1, the real health server, the real LogPipe, the real
# checkpoint/outbox code. Only the child's identity is substituted.
#
# Arguments are ignored on purpose — the supervisor passes claude's full argv and
# this script's contract is only "be a child process that floods stdout".
set -eu

# The supervisor builds its child's environment FROM SCRATCH (§5.2: secrets must
# not leak in from PID 1's env), so nothing set on the container reaches here.
# The knobs below are therefore compiled-in defaults, not env overrides — and
# `--version` must return instantly, because §5.4's boot check runs this same
# binary with that flag and flooding there would only slow the boot down.
case "${1:-}" in
  --version) echo "flood-child 1.0 (OSiris test fixture, stands in for claude)"; exit 0 ;;
esac

LINES="${FLOOD_LINES:-8000000}"
PADLEN="${FLOOD_PAD_BYTES:-180}"

# awk, not a shell loop: a shell loop forks per line and would be rate-limited by
# the fork, not by the pipe — which is exactly the property under test.
awk -v n="$LINES" -v padlen="$PADLEN" 'BEGIN {
  pad = "";
  for (i = 0; i < padlen; i++) pad = pad "x";
  for (i = 1; i <= n; i++)
    printf("{\"type\":\"assistant\",\"seq\":%d,\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"%s\"}]}}\n", i, pad);
}'

# One over-long line, to exercise the 1MiB per-line truncation path as well.
awk 'BEGIN { printf("{\"type\":\"assistant\",\"note\":\"oversize\",\"pad\":\""); for (i = 0; i < 3000000; i++) printf("y"); printf("\"}\n"); }'

# A well-formed result line so the supervisor records a real outcome rather than
# inferring failure from a missing result.
printf '{"type":"result","subtype":"success","is_error":false,"num_turns":1,"total_cost_usd":0,"session_id":"flood-test","result":"flood complete"}\n'
