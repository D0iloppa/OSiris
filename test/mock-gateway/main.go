// osiris-mock-gateway — a stand-in for the omniroute LLM gateway (§9), used to
// verify the agent runtime's §5.4/§5.5 invariants WITHOUT spending tokens.
//
// WHY THIS EXISTS. The invariants that matter most in §5 are about the
// supervisor's relationship to a long-running, chatty CHILD PROCESS:
//
//	§5.4  "/livez ... claude 프로세스의 존재 여부, 실행 시간, 응답 여부는 livez의
//	       입력이 아니다" — needs a turn that runs for minutes.
//	§5.4  "헬스서버는 자식 stdout 파이프에 절대 블록되지 않는다 ... 채널 만재 시
//	       로그 라인을 버린다" — needs a child that floods stdout.
//	§5.5  checkpoint → SIGTERM → resume — needs a turn that is genuinely
//	       in-flight when the signal arrives.
//
// None of those require real inference; they require a real `claude` process
// that takes a long time and says a lot. This server produces exactly that, by
// speaking enough of the Anthropic Messages API wire format (SSE) for the real
// Claude Code CLI to drive a real turn against it.
//
// It is a TEST FIXTURE, not part of the platform: it is never referenced by
// docker-compose.yml, never started by osiris-up, and carries no credentials. It
// is committed so the next worker can reproduce the same proofs instead of
// taking this build's word for them (bin/verify-agent-runtime.sh drives it).
//
// Knobs (all env, all optional):
//
//	ADDR              listen address (default :8080)
//	MOCK_TOTAL_MS     stretch one response over this long (default 0 = immediate)
//	MOCK_CHUNKS       number of text deltas (default 3)
//	MOCK_CHUNK_BYTES  bytes per text delta (default 24)
//	MOCK_PING_MS      keepalive ping interval while stretching (default 1000)
//	MOCK_REPLY        the text to reply with when MOCK_CHUNK_BYTES is 0
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var requestCount atomic.Int64

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()

	// §9.2 contract item 4: "GET /healthz 무인증, 100ms 이내". The supervisor's
	// readiness probe hits exactly this and nothing else — no model is invoked,
	// no token is required. Implemented here so the probe path can be verified
	// against a gateway that actually honours the contract (the omniroute build
	// deployed on this host does not expose /healthz; see IMPLEMENTATION-NOTES).
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok","mock":true}`)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"osiris/worker","type":"model"}]}`)
	})

	mux.HandleFunc("/v1/messages", handleMessages)

	mux.HandleFunc("/__stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"requests":%d}`, requestCount.Load())
	})

	log.Printf("osiris-mock-gateway listening on %s (totalMs=%d chunks=%d chunkBytes=%d)",
		addr, envInt("MOCK_TOTAL_MS", 0), envInt("MOCK_CHUNKS", 3), envInt("MOCK_CHUNK_BYTES", 24))
	srv := &http.Server{Addr: addr, Handler: mux}
	log.Fatal(srv.ListenAndServe())
}

func handleMessages(w http.ResponseWriter, r *http.Request) {
	n := requestCount.Add(1)

	var req struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	_ = json.Unmarshal(body, &req)

	// Log the two things §9.2 asks the gateway to preserve for cost attribution
	// (item 6) plus the auth scheme (item 2), so a verification run can assert
	// them from this side rather than trusting the client.
	log.Printf("REQ #%d model=%q stream=%v auth=%q employee=%q task=%q bodyBytes=%d",
		n, req.Model, req.Stream,
		firstWord(r.Header.Get("Authorization")),
		r.Header.Get("X-Osiris-Employee-Id"), r.Header.Get("X-Osiris-Task-Id"), len(body))

	totalMS := envInt("MOCK_TOTAL_MS", 0)
	chunks := envInt("MOCK_CHUNKS", 3)
	chunkBytes := envInt("MOCK_CHUNK_BYTES", 24)
	pingMS := envInt("MOCK_PING_MS", 1000)
	if chunks < 1 {
		chunks = 1
	}

	reply := os.Getenv("MOCK_REPLY")
	if reply == "" {
		reply = "OSiris mock gateway acknowledged the task. No inference was performed."
	}

	if !req.Stream {
		w.Header().Set("Content-Type", "application/json")
		if totalMS > 0 {
			time.Sleep(time.Duration(totalMS) * time.Millisecond)
		}
		out := map[string]any{
			"id": fmt.Sprintf("msg_mock_%d", n), "type": "message", "role": "assistant",
			"model": req.Model, "stop_reason": "end_turn",
			"content": []map[string]any{{"type": "text", "text": reply}},
			"usage":   map[string]any{"input_tokens": 12, "output_tokens": 8},
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}

	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	send := func(event string, payload any) bool {
		b, _ := json.Marshal(payload)
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return false
		}
		fl.Flush()
		return true
	}

	msgID := fmt.Sprintf("msg_mock_%d", n)
	if !send("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant", "model": req.Model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 12, "output_tokens": 1},
		},
	}) {
		return
	}
	if !send("content_block_start", map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "text", "text": ""},
	}) {
		return
	}

	// Stretch the response over MOCK_TOTAL_MS, emitting a text delta every
	// (totalMS/chunks) and a ping in between. Pings are what keep a
	// multi-minute turn from looking like a stalled connection to the client —
	// which is the whole point of the long-turn test: the child must be busy for
	// minutes, not broken for minutes.
	perChunk := time.Duration(0)
	if totalMS > 0 {
		perChunk = time.Duration(totalMS/chunks) * time.Millisecond
	}
	pingEvery := time.Duration(pingMS) * time.Millisecond

	for i := 0; i < chunks; i++ {
		if perChunk > 0 {
			deadline := time.Now().Add(perChunk)
			for time.Now().Before(deadline) {
				sleep := pingEvery
				if rem := time.Until(deadline); rem < sleep {
					sleep = rem
				}
				time.Sleep(sleep)
				if !send("ping", map[string]any{"type": "ping"}) {
					return
				}
			}
		}
		text := reply
		if chunkBytes > 0 {
			text = strings.Repeat("x", chunkBytes)
		}
		if !send("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": text},
		}) {
			return
		}
	}

	if !send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}) {
		return
	}
	if !send("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": chunks},
	}) {
		return
	}
	send("message_stop", map[string]any{"type": "message_stop"})
	log.Printf("REQ #%d complete", n)
}

func firstWord(s string) string {
	// Report only the SCHEME ("Bearer"), never the token itself — §9.1: "평문.
	// 로그·이벤트에 절대 싣지 않음". Verifying that the CLI uses Bearer is the
	// point; logging the secret would not add to it.
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	if s == "" {
		return "(none)"
	}
	return "(malformed)"
}
