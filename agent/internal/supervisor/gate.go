package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
)

// newUUIDv4 produces a RFC 4122 v4 UUID. Hand-rolled rather than pulling in
// google/uuid: the agent image should carry as little third-party code as a
// process that runs LLM-authored tool calls reasonably can.
func newUUIDv4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not recoverable in a way that matters here;
		// a time-derived id keeps the process alive and is still unique enough
		// for a session id, which is all this is used for.
		return fmt.Sprintf("%016x-%016x", time.Now().UnixNano(), time.Now().UnixNano()>>7)[:36]
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// gateMaxWait is §8.2's "훅은 블록(최대 24h. 그동안 하트비트는 계속 →
// liveness 영향 없음)". The 24h block is safe precisely because of §5.4: the
// blocked thing is a child process and an HTTP goroutine, and NEITHER is an
// input to /livez.
const gateMaxWait = 24 * time.Hour

// Gate implements §8.2's approval gate.
//
// DESIGN NOTE — why this is an MCP-backed gate rather than a PreToolUse hook.
// §8.2 says: "`[미검증]` --bare 모드에서 --settings로 명시 전달한 파일의 hooks
// 섹션이 로드되는지 공식 문서로 확인되지 않았다. 로드되지 않으면 대안: (a)
// --bare를 빼고 --settings만 지정, (b) MCP 툴 래핑 ... (b)가 더 견고하므로
// 훅이 안 되면 (b)로 간다."
//
// That question is now ANSWERED, not assumed: `claude --help` on 2.1.220
// documents --bare as "Minimal mode: skip hooks, LSP, plugin sync, ...". Hooks
// are skipped, full stop — a hooks section in the rendered settings.json would
// be silently inert, which is the worst possible outcome for a security
// control. So the design's own fallback (b) is taken: the gated operations are
// exposed ONLY as MCP tools (internal/mcpserver), the corresponding Bash forms
// are DENIED in settings.json, and those MCP tools call this gate.
//
// Note this changes nothing about the enforcement story in §8.2: MCP tools and
// hooks are both "협조" (cooperation) layers that prompt injection can attack.
// The actual enforcement remains (i) the agent's git credential being
// read-only, and (ii) osiris-core performing the real commit/push with its own
// credential. This gate exists to make the intended path work and auditable,
// not to be the last line of defence.
type Gate struct {
	bus        *Bus
	employeeID string
}

func NewGate(bus *Bus, employeeID string) *Gate {
	return &Gate{bus: bus, employeeID: employeeID}
}

type GateRequest struct {
	ApprovalType string          `json:"approvalType"` // commit | message | spend
	Tool         string          `json:"tool"`
	TaskID       string          `json:"taskId"`
	Summary      string          `json:"summary"`
	Payload      json.RawMessage `json:"payload"`
}

type GateResponse struct {
	ApprovalID string `json:"approvalId"`
	Decision   string `json:"decision"` // approved | rejected | timeout | error
	Reason     string `json:"reason,omitempty"`
	ExecutedBy string `json:"executedBy,omitempty"`
}

// Handler serves the loopback gate. It is mounted on GateAddr — a listener
// SEPARATE from the health listener — so that N goroutines parked for hours in
// here cannot interact with probe latency in any way.
func (g *Gate) Handler() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/gate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req GateRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, GateResponse{Decision: "error", Reason: "bad request: " + err.Error()})
			return
		}
		resp := g.Request(r.Context(), req)
		writeJSON(w, http.StatusOK, resp)
	})
	return mux
}

// Request publishes approval.request and blocks on approval.decision.<id>.
func (g *Gate) Request(ctx context.Context, req GateRequest) GateResponse {
	approvalID := newUUIDv4()

	// Subscribe BEFORE publishing: an approver fast enough to decide between
	// our publish and our subscribe would otherwise leave us blocked for 24h
	// on a decision that already happened.
	decisions := make(chan *nats.Msg, 1)
	sub, err := g.bus.Subscribe("approval.decision."+approvalID, func(m *nats.Msg) {
		select {
		case decisions <- m:
		default:
		}
	})
	if err != nil {
		return GateResponse{ApprovalID: approvalID, Decision: "error", Reason: "cannot subscribe for decision: " + err.Error()}
	}
	defer func() { _ = sub.Unsubscribe() }()

	g.bus.PublishDurable("approval.request", MsgID(req.TaskID, 1, "approval:"+approvalID), map[string]any{
		"approvalId":   approvalID,
		"approvalType": req.ApprovalType,
		"tool":         req.Tool,
		"taskId":       req.TaskID,
		"employeeId":   g.employeeID,
		"summary":      req.Summary,
		"payload":      req.Payload,
		"ts":           time.Now().UTC(),
	})
	log.Printf("supervisor/gate: approval.request published id=%s type=%s tool=%s task=%s — blocking (max %s)",
		approvalID, req.ApprovalType, req.Tool, req.TaskID, gateMaxWait)

	timer := time.NewTimer(gateMaxWait)
	defer timer.Stop()

	select {
	case m := <-decisions:
		var d struct {
			Decision   string `json:"decision"`
			Reason     string `json:"reason"`
			ExecutedBy string `json:"executedBy"`
		}
		if err := json.Unmarshal(m.Data, &d); err != nil {
			return GateResponse{ApprovalID: approvalID, Decision: "error", Reason: "undecodable decision payload"}
		}
		log.Printf("supervisor/gate: decision for %s = %s", approvalID, d.Decision)
		return GateResponse{ApprovalID: approvalID, Decision: d.Decision, Reason: d.Reason, ExecutedBy: d.ExecutedBy}
	case <-timer.C:
		return GateResponse{ApprovalID: approvalID, Decision: "timeout", Reason: fmt.Sprintf("no decision within %s", gateMaxWait)}
	case <-ctx.Done():
		// The child was killed (drain) — abandon the wait. The approval record
		// itself is already durable in the TASKS stream, so a human can still
		// act on it and core can still execute it after the fact.
		return GateResponse{ApprovalID: approvalID, Decision: "error", Reason: "agent draining before decision"}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
