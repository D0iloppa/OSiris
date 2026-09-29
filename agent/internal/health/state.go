// Package health is the ONLY thing the /livez and /readyz HTTP handlers are
// allowed to read from, and it deliberately contains nothing but
// sync/atomic values.
//
// ============================================================================
// THE INVARIANT THIS PACKAGE EXISTS TO ENFORCE STRUCTURALLY
// (docs/design/10-core-architecture.md §5.4, "구현 시 반드시 지킬 규칙"):
//
//	"헬스 핸들러는 atomic 변수만 읽는다. 자식 프로세스 실행 경로가 잡는
//	 어떤 mutex도 잡지 않는다. (흔한 실패: mutex로 보호된 상태 구조체를
//	 헬스가 읽다가, 긴 exec 경로가 락을 쥐고 있어 헬스 타임아웃 → 재시작.
//	 금지한다.)"
//
// The enforcement is structural rather than a comment elsewhere: this package
// imports NO package that can block (no os/exec, no nats.go, no os file I/O
// beyond none at all), declares NO sync.Mutex / sync.RWMutex / sync.WaitGroup
// / channel of any kind, and every field of State is a sync/atomic type. A
// future edit that tries to make a health probe "just check one more thing"
// by reaching into the supervisor's mutable state cannot do so from here
// without first adding a lock or a channel to this file — which is the one
// thing a reviewer is told to look for. bin/lint-health-invariant.sh fails
// the build if that ever happens.
//
// Equally forbidden and equally structural: NOTHING in this package can start
// a `claude` process, because it does not import os/exec at all. §5.4:
// "어떤 probe도 LLM을 호출하지 않는다 ... /livez·/readyz 처리 경로에서
// claude 프로세스를 생성하는 코드는 존재하지 않는다."
// ============================================================================
package health

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

// Lifecycle phase codes (§5.3). Stored as an atomic int32 rather than a
// string so the health handler's read is a single atomic load. Only the
// phases a *supervisor inside a running container* can actually be in are
// represented here — Pending/Provisioning (before the container exists),
// Suspended and Failed (§5.3) are orchestrator-side judgments about a
// workload, not states this process can observe about itself, and the
// design is explicit that "phase는 워크로드 드라이버의 관측값이 권위"
// (§5.3). What the supervisor owns authoritatively is Ready vs Working:
// "Working 여부는 supervisor 하트비트가 권위다."
const (
	PhaseBooting int32 = iota
	PhaseReady
	PhaseWorking
	PhaseDraining
	PhaseStopped
)

var phaseNames = [...]string{"Booting", "Ready", "Working", "Draining", "Stopped"}

func PhaseName(p int32) string {
	if p < 0 || int(p) >= len(phaseNames) {
		return "Unknown"
	}
	return phaseNames[p]
}

// livenessStaleAfter is §5.4's "(now - lastLoopTick) < 30s".
const livenessStaleAfter = 30 * time.Second

// State holds every input to /livez and /readyz. Every field is atomic; see
// the package comment for why that is load-bearing rather than stylistic.
type State struct {
	employeeID string // immutable after New — never written again, so no atomic needed

	// --- /livez inputs (§5.4) -------------------------------------------
	lastLoopTick atomic.Int64 // unix millis, updated by the main select loop
	reaperTick   atomic.Int64 // unix millis, updated by the child-reaper goroutine
	natsClosed   atomic.Bool  // true only for nats.CLOSED — RECONNECTING counts as alive

	// --- /readyz inputs (§5.4) ------------------------------------------
	credsOK         atomic.Bool
	configOK        atomic.Bool
	claudeVersionOK atomic.Bool
	consumerBound   atomic.Bool
	volumeWritable  atomic.Bool
	gatewayHealthy  atomic.Bool

	// --- observability only (never an input to either probe) ------------
	phase            atomic.Int32
	droppedLogLines  atomic.Int64
	activeTaskPoints atomic.Int64
	turnsCompleted   atomic.Int64
	natsStatus       atomic.Pointer[string]
	configHash       atomic.Pointer[string]
	sessionID        atomic.Pointer[string]
	activeTaskID     atomic.Pointer[string]
}

func New(employeeID string) *State {
	s := &State{employeeID: employeeID}
	now := time.Now().UnixMilli()
	// Seed both liveness clocks at construction so /livez answers 200 from
	// the very first probe rather than being "stale" for the first tick.
	// The container's HEALTHCHECK start-period covers boot anyway, but a
	// 503 during boot would be misleading, not protective.
	s.lastLoopTick.Store(now)
	s.reaperTick.Store(now)
	s.phase.Store(PhaseBooting)
	s.setStr(&s.natsStatus, "CONNECTING")
	s.setStr(&s.configHash, "")
	s.setStr(&s.sessionID, "")
	s.setStr(&s.activeTaskID, "")
	return s
}

func (s *State) setStr(p *atomic.Pointer[string], v string) { p.Store(&v) }

func getStr(p *atomic.Pointer[string]) string {
	if v := p.Load(); v != nil {
		return *v
	}
	return ""
}

// --- writers (called from the supervisor's goroutines) --------------------

func (s *State) TouchLoop()   { s.lastLoopTick.Store(time.Now().UnixMilli()) }
func (s *State) TouchReaper() { s.reaperTick.Store(time.Now().UnixMilli()) }

// SetNATSStatus records the connection state. Only CLOSED is fatal for
// liveness: §5.4 "natsConnState != CLOSED (RECONNECTING은 살아있는 것으로
// 본다)".
func (s *State) SetNATSStatus(status string) {
	s.setStr(&s.natsStatus, status)
	s.natsClosed.Store(status == "CLOSED")
}

func (s *State) SetCredsOK(v bool)         { s.credsOK.Store(v) }
func (s *State) SetConfigOK(v bool)        { s.configOK.Store(v) }
func (s *State) SetClaudeVersionOK(v bool) { s.claudeVersionOK.Store(v) }
func (s *State) SetConsumerBound(v bool)   { s.consumerBound.Store(v) }
func (s *State) SetVolumeWritable(v bool)  { s.volumeWritable.Store(v) }
func (s *State) SetGatewayHealthy(v bool)  { s.gatewayHealthy.Store(v) }

func (s *State) SetPhase(p int32)            { s.phase.Store(p) }
func (s *State) Phase() int32                { return s.phase.Load() }
func (s *State) AddDroppedLogLines(n int64)  { s.droppedLogLines.Add(n) }
func (s *State) DroppedLogLines() int64      { return s.droppedLogLines.Load() }
func (s *State) SetActiveTaskPoints(n int64) { s.activeTaskPoints.Store(n) }
func (s *State) IncTurnsCompleted()          { s.turnsCompleted.Add(1) }
func (s *State) SetConfigHash(v string)      { s.setStr(&s.configHash, v) }
func (s *State) ConfigHash() string          { return getStr(&s.configHash) }
func (s *State) SetSessionID(v string)       { s.setStr(&s.sessionID, v) }
func (s *State) SetActiveTaskID(v string)    { s.setStr(&s.activeTaskID, v) }

// --- readers -------------------------------------------------------------

// Livez is §5.4's judgment, verbatim and nothing more:
//
//	200 iff (now - lastLoopTick) < 30s
//	     AND natsConnState != CLOSED
//	     AND childReaper goroutine 생존 (자체 tick으로 확인)
//	     AND 헬스서버가 3초 내 응답 (probe timeout이 곧 이 검사)
//
// The fourth clause needs no code: this function performs three atomic loads
// and a time comparison, so the only way it fails to answer inside the
// probe's 3s timeout is if the Go runtime itself is wedged — which is exactly
// the condition the clause is meant to catch.
//
// It deliberately does NOT look at whether a `claude` child exists, how long
// it has been running, or whether it is responding (§5.4: "claude 프로세스의
// 존재 여부, 실행 시간, 응답 여부는 livez의 입력이 아니다"). A 40-minute turn
// keeps the main select loop ticking every 5s, so a busy agent is never
// mistaken for a wedged one.
func (s *State) Livez() (bool, map[string]any) {
	now := time.Now().UnixMilli()
	loopAge := now - s.lastLoopTick.Load()
	reaperAge := now - s.reaperTick.Load()
	closed := s.natsClosed.Load()

	stale := livenessStaleAfter.Milliseconds()
	loopOK := loopAge < stale
	reaperOK := reaperAge < stale
	ok := loopOK && reaperOK && !closed

	return ok, map[string]any{
		"live":              ok,
		"employeeId":        s.employeeID,
		"loopTickAgeMs":     loopAge,
		"loopTickOk":        loopOK,
		"reaperTickAgeMs":   reaperAge,
		"reaperOk":          reaperOK,
		"natsStatus":        getStr(&s.natsStatus),
		"natsClosed":        closed,
		"phase":             PhaseName(s.phase.Load()),
		"droppedLogLines":   s.droppedLogLines.Load(),
		"turnsCompleted":    s.turnsCompleted.Load(),
		"staleThresholdSec": int64(livenessStaleAfter / time.Second),
	}
}

// Readyz is §5.4's readiness judgment:
//
//	200 iff 자격증명 파일 로드 성공 AND 렌더된 설정 유효
//	     AND 부팅 시 `claude --version` 1회 성공
//	     AND JetStream durable consumer 바인드 성공
//	     AND /workspace 쓰기 가능 AND gatewayHealthy
//
// "바쁨"은 절대 여기 실리지 않는다 (§5.4): a Working agent is ready=true.
// The activeTaskPoints/phase fields are reported in the body for the console
// and the (future) assignment engine, but they are NOT part of `ok`.
func (s *State) Readyz() (bool, map[string]any) {
	creds := s.credsOK.Load()
	cfg := s.configOK.Load()
	ver := s.claudeVersionOK.Load()
	cons := s.consumerBound.Load()
	vol := s.volumeWritable.Load()
	gw := s.gatewayHealthy.Load()
	ok := creds && cfg && ver && cons && vol && gw

	var reasons []string
	if !creds {
		reasons = append(reasons, "CREDENTIALS_UNAVAILABLE")
	}
	if !cfg {
		reasons = append(reasons, "CONFIG_INVALID")
	}
	if !ver {
		reasons = append(reasons, "CLAUDE_CLI_UNVERIFIED")
	}
	if !cons {
		reasons = append(reasons, "CONSUMER_UNBOUND")
	}
	if !vol {
		reasons = append(reasons, "WORKSPACE_NOT_WRITABLE")
	}
	if !gw {
		reasons = append(reasons, "GATEWAY_UNHEALTHY")
	}
	if reasons == nil {
		reasons = []string{}
	}

	return ok, map[string]any{
		"ready":      ok,
		"employeeId": s.employeeID,
		"reasons":    reasons,
		"checks": map[string]bool{
			"credentials":    creds,
			"config":         cfg,
			"claudeVersion":  ver,
			"consumerBound":  cons,
			"volumeWritable": vol,
			"gatewayHealthy": gw,
		},
		// Reported, never an input to `ready` — see the doc comment.
		"phase":            PhaseName(s.phase.Load()),
		"activeTaskPoints": s.activeTaskPoints.Load(),
		"configHash":       getStr(&s.configHash),
		"sessionId":        getStr(&s.sessionID),
		"activeTaskId":     getStr(&s.activeTaskID),
	}
}

// Handler returns the mux serving /livez, /readyz and /statusz. Every handler
// body is: atomic loads -> json.Marshal -> write. No lock, no file read, no
// network call, no subprocess.
func (s *State) Handler() *http.ServeMux {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, ok bool, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if ok {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(body)
	}
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		ok, body := s.Livez()
		write(w, ok, body)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ok, body := s.Readyz()
		write(w, ok, body)
	})
	// /statusz is diagnostic only (always 200) — it exists so an operator can
	// see WHY readyz is false without having to interpret a 503 body, and so
	// the [1단계] orchestrator has a place to read heartbeat-adjacent detail
	// from if it ever wants to without adding meaning to readiness.
	mux.HandleFunc("/statusz", func(w http.ResponseWriter, r *http.Request) {
		_, live := s.Livez()
		_, ready := s.Readyz()
		write(w, true, map[string]any{"livez": live, "readyz": ready})
	})
	return mux
}

// Server builds the health HTTP server. Timeouts are set so a slow or
// half-open client can never occupy the accept path: the design's "헬스서버가
// 3초 내 응답" clause is about the handler, but a missing ReadHeaderTimeout
// would let a stuck TCP peer do what a mutex is forbidden from doing.
func (s *State) Server(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
}
