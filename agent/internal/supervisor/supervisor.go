// Package supervisor is osiris-supervisor: the agent container's PID 1
// (docs/design/10-core-architecture.md §5).
//
// Structure of this package, mapped to the design:
//
//	paths.go      §5.1/§5.6  container-internal paths and timings
//	config.go     §5.6/§8.4  rendered config + credential loading (re-read, never cached)
//	checkpoint.go §5.5       /workspace/.osiris/state.json, fsynced
//	bus.go        §7         NATS connection, durable publish, outbox spool
//	logpipe.go    §5.4       non-blocking, drop-on-full child log fan-in
//	task.go       §5.2/§5.5  claude argv/env, the turn, the drain escalation
//	probe.go      §5.4       readiness probes (none of which calls an LLM)
//	reaper.go     §5.1/§5.4  PID 1 orphan reaping + its own liveness tick
//	gate.go       §8.2       approval gate (loopback listener, separate port)
//	supervisor.go §5.2/§5.3  boot, the main select loop, lifecycle, shutdown
//
// The health endpoints deliberately live in a DIFFERENT package
// (internal/health) that cannot import this one. See its package comment.
package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"

	"osiris-agent/internal/health"
)

// Config is the supervisor's own (non-secret) configuration, from the
// environment the driver sets. Note what is NOT here: no token, no password,
// no gateway URL. Those come from files (G5/§8.4) and are re-read, never
// captured into this struct.
type Config struct {
	EmployeeID   string
	TeamID       string
	TeamSlug     string
	NATSURL      string
	CoreURL      string
	ModelAlias   string
	InboxSubject string
	InboxStream  string
	InboxDurable string
	// InboxMultiFilter is set for the master agent, whose durable consumer has
	// TWO filter subjects. See the driver's comment on OSIRIS_INBOX_MULTIFILTER
	// and bindConsumer below.
	InboxMultiFilter bool
	DrainSeconds     int
	MaxTaskPoints    int
	EgressProxy      string
	NoProxy          string
	ClaudeBin        string
	OneShotPrompt    string // §5.7 --one-shot mode
	OneShotTaskID    string
}

func ConfigFromEnv() Config {
	c := Config{
		EmployeeID:   os.Getenv("OSIRIS_EMPLOYEE_ID"),
		TeamID:       os.Getenv("OSIRIS_TEAM_ID"),
		TeamSlug:     os.Getenv("OSIRIS_TEAM_SLUG"),
		NATSURL:      envOr("OSIRIS_NATS_URL", "nats://nats:4222"),
		CoreURL:      envOr("OSIRIS_CORE_URL", "http://osiris-core:8090"),
		ModelAlias:   envOr("OSIRIS_MODEL_ALIAS", "osiris/worker"),
		InboxStream:  envOr("OSIRIS_INBOX_STREAM", "INBOX"),
		EgressProxy:  envOr("OSIRIS_EGRESS_PROXY", "http://osiris-egress:3128"),
		NoProxy:      envOr("OSIRIS_NO_PROXY", "nats,osiris-core,osiris-nats,localhost,127.0.0.1,::1"),
		ClaudeBin:    envOr("OSIRIS_CLAUDE_BIN", "claude"),
		DrainSeconds: envInt("OSIRIS_DRAIN_SECONDS", 600),
		// §5.2's ticker/points; maxTaskPoints comes from :AIAgent and is used
		// by the heartbeat so the (future) assignment engine can see spare
		// capacity — §5.4: "바쁨/한가함은 하트비트의 state와 activeTaskPoints로
		// 표현하고 배정 엔진이 그것을 본다."
		MaxTaskPoints: envInt("OSIRIS_MAX_TASK_POINTS", 1),
	}
	if c.TeamSlug == "" {
		c.TeamSlug = c.TeamID
	}
	c.InboxSubject = envOr("OSIRIS_INBOX_SUBJECT", fmt.Sprintf("team.%s.inbox.%s", c.TeamSlug, c.EmployeeID))
	c.InboxDurable = envOr("OSIRIS_INBOX_DURABLE", "agent-"+c.EmployeeID)
	c.InboxMultiFilter = os.Getenv("OSIRIS_INBOX_MULTIFILTER") == "1"
	return c
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// Supervisor is the process. Its mutable fields are protected by resultMu or
// are atomics; NONE of them is read by a health handler (see internal/health).
type Supervisor struct {
	cfg Config
	hs  *health.State
	bus *Bus

	employeeID   string
	teamID       string
	modelAlias   string
	claudeBin    string
	drainSeconds int
	egressProxy  string
	noProxy      string
	pathEnv      string
	askpassPath  string

	logs     *LogPipe
	childPID atomic.Int32

	draining    atomic.Bool
	drainSignal chan struct{}
	drainOnce   sync.Once

	// resultMu guards the stream-json result line captured by the log
	// consumer goroutine and read once per turn by runTurn.
	resultMu   sync.Mutex
	lastResult TaskOutcome

	gate *Gate

	claudeVersion string
}

func New(cfg Config) *Supervisor {
	hs := health.New(cfg.EmployeeID)
	s := &Supervisor{
		cfg:          cfg,
		hs:           hs,
		employeeID:   cfg.EmployeeID,
		teamID:       cfg.TeamID,
		modelAlias:   cfg.ModelAlias,
		claudeBin:    cfg.ClaudeBin,
		drainSeconds: cfg.DrainSeconds,
		egressProxy:  cfg.EgressProxy,
		noProxy:      cfg.NoProxy,
		pathEnv:      envOr("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"),
		drainSignal:  make(chan struct{}),
	}
	s.logs = NewLogPipe(func() { hs.AddDroppedLogLines(1) })
	s.bus = NewBus(cfg.EmployeeID, hs)
	return s
}

func (s *Supervisor) Health() *health.State { return s.hs }

// SignalDrain is what the SIGTERM handler calls. Idempotent: §5.5's escalation
// must not restart if the runtime sends SIGTERM twice.
func (s *Supervisor) SignalDrain() {
	s.drainOnce.Do(func() {
		s.draining.Store(true)
		s.hs.SetPhase(health.PhaseDraining)
		close(s.drainSignal)
	})
}

// Run is the whole lifecycle: §5.3's Booting → Ready ⇄ Working → Draining →
// Stopped, with §5.2's main loop in the middle.
func (s *Supervisor) Run(ctx context.Context) error {
	log.Printf("osiris-supervisor: booting employeeId=%s team=%s inbox=%s durable=%s",
		s.employeeID, s.cfg.TeamID, s.cfg.InboxSubject, s.cfg.InboxDurable)

	// ---------------- Booting (§5.3) -----------------------------------
	s.hs.SetPhase(health.PhaseBooting)

	// ---- the health server comes FIRST, before every other boot step ----
	//
	// FOUND BY MEASUREMENT, not by reading: with the health server started after
	// the boot probes, a slow `claude --version` (a cold node start under CPU
	// pressure took >4s here, and its timeout is 30s) leaves :8081 refusing
	// connections for that whole window. Docker's HEALTHCHECK start-period is
	// 20s, after which three failed probes 10s apart mean the orchestrator
	// recreates the container — a boot-time restart loop caused by nothing but
	// probe ordering. Measured before the fix: the first 9 polls of a 1s poller
	// returned no connection at all.
	//
	// Starting it first is also simply correct on its own terms: /livez answers
	// "is the supervisor's control flow alive", and it is alive from the moment
	// the process starts. Everything the boot steps discover is readiness, and
	// readiness has its own endpoint that correctly reports false until they
	// finish.
	hsrv := s.hs.Server(HealthAddr)
	go func() {
		if err := hsrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("supervisor: health server error: %v", err)
		}
	}()

	for _, d := range []string{OsirisDir, OutboxDir, TranscriptDir, ClaudeConfigDir, RepoDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	s.hs.TouchLoop()
	if err := probeWorkspaceWritable(); err != nil {
		// Not fatal: readyz will report WORKSPACE_NOT_WRITABLE and the
		// orchestrator will see readyz=false. Crashing here would produce
		// §5.3's "연속 3회 크래시 → Failed" for a condition that a human fixing
		// a volume permission can resolve without a crash loop.
		log.Printf("supervisor: workspace not writable yet: %v", err)
		s.hs.SetVolumeWritable(false)
	} else {
		s.hs.SetVolumeWritable(true)
	}

	// `claude --version`, ONCE, at boot (§5.4). Never from a probe handler.
	// Bracketed by TouchLoop because this call can legitimately take seconds and
	// lastLoopTick must not be allowed to age towards its 30s threshold while
	// the process is making perfectly good progress.
	s.hs.TouchLoop()
	if ver, err := verifyClaudeVersion(ctx, s.claudeBin); err != nil {
		log.Printf("supervisor: claude --version FAILED: %v", err)
		s.hs.SetClaudeVersionOK(false)
	} else {
		s.claudeVersion = ver
		s.hs.SetClaudeVersionOK(true)
		log.Printf("supervisor: claude --version ok: %s", ver)
	}

	s.hs.TouchLoop()
	creds, err := loadCreds()
	if err != nil {
		log.Printf("supervisor: credentials not loadable yet: %v", err)
		s.hs.SetCredsOK(false)
	} else {
		s.hs.SetCredsOK(true)
	}

	cfgFiles, err := loadConfig()
	if err != nil {
		log.Printf("supervisor: rendered config invalid: %v", err)
		s.hs.SetConfigOK(false)
	} else {
		s.hs.SetConfigOK(true)
		s.hs.SetConfigHash(cfgFiles.Hash)
		log.Printf("supervisor: config loaded configHash=%s allowedTools=%d promptBytes=%d",
			cfgFiles.Hash, len(cfgFiles.AllowedTools), len(cfgFiles.Prompt))
	}

	if p, err := writeAskpass(); err != nil {
		log.Printf("supervisor: could not write GIT_ASKPASS helper: %v", err)
	} else {
		s.askpassPath = p
	}

	// ---------------- background goroutines (§5.2) ----------------------
	stopBG := make(chan struct{})
	defer close(stopBG)

	go reaper(s.hs, &s.childPID, stopBG)
	go s.consumeLogs(stopBG)

	if creds.NATSUser != "" {
		if err := s.bus.Connect(s.cfg.NATSURL, creds); err != nil {
			log.Printf("supervisor: initial nats connect failed: %v", err)
		}
	}

	// Approval gate (§8.2). Loopback-only, separate listener from health.
	s.gate = NewGate(s.bus, s.employeeID)
	gsrv := &http.Server{Addr: GateAddr, Handler: s.gate.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := gsrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("supervisor: gate server error: %v", err)
		}
	}()

	go s.heartbeatLoop(stopBG)
	go s.gatewayLoop(stopBG, &creds)
	go s.volumeLoop(stopBG)
	go s.outboxLoop(stopBG)
	credsCh := make(chan Creds, 1)
	go s.credsWatchLoop(stopBG, creds, credsCh)
	s.subscribeConfigReload()

	// ---------------- §5.7 one-shot mode -------------------------------
	if s.cfg.OneShotPrompt != "" {
		return s.runOneShot(ctx, cfgFiles, creds)
	}

	// ---------------- consumer bind (§5.4 readyz clause) ---------------
	sub := s.bindConsumer()

	// ---------------- resume (§5.5) ------------------------------------
	cp, err := loadCheckpoint()
	if err != nil {
		log.Printf("supervisor: %v", err)
	}
	if cp.Status == StatusInterrupted {
		s.resumeInterrupted(ctx, &cp, cfgFiles, creds)
	} else if cp.Status == StatusRunning {
		// A checkpoint left in "running" means we were SIGKILLed without ever
		// reaching §5.5's graceful path (OOM kill, host power loss, docker
		// kill -9). It is exactly as interrupted as an "interrupted" one — the
		// only difference is that nobody got to write the word down. Treating
		// it as interrupted is what makes hard kills recoverable too.
		log.Printf("supervisor: checkpoint was left in 'running' (hard kill) — treating as interrupted")
		cp.Status = StatusInterrupted
		_ = saveCheckpoint(cp)
		s.resumeInterrupted(ctx, &cp, cfgFiles, creds)
	}

	if !s.draining.Load() {
		s.hs.SetPhase(health.PhaseReady)
	}

	// ---------------- §5.2 main loop -----------------------------------
	err = s.mainLoop(ctx, sub, &cp, cfgFiles, creds, credsCh)

	// ---------------- Draining → Stopped (§5.5) ------------------------
	s.finalDrain(ctx, sub, hsrv, gsrv)
	return err
}

// bindConsumer binds (never creates) the durable pull consumer. Retried by the
// main loop, so a broker that is slow to come up costs readiness, not a crash.
func (s *Supervisor) bindConsumer() *nats.Subscription {
	// Bind subject: for a single-filter consumer nats.go verifies that the
	// subject passed here EQUALS the consumer's FilterSubject and errors
	// otherwise, so it must be supplied. For a multi-filter consumer (master)
	// the server reports an empty FilterSubject and the check is skipped — but
	// passing a non-empty subject would then be compared against "" and, since
	// nats.go only compares when the consumer's filter is non-empty, would be
	// accepted while silently meaning nothing. Passing "" makes the consumer's
	// own filter list unambiguously authoritative.
	subject := s.cfg.InboxSubject
	if s.cfg.InboxMultiFilter {
		subject = ""
	}
	sub, err := s.bus.PullSubscribe(subject, s.cfg.InboxStream, s.cfg.InboxDurable)
	if err != nil {
		log.Printf("supervisor: bind durable consumer %s/%s failed: %v", s.cfg.InboxStream, s.cfg.InboxDurable, err)
		s.hs.SetConsumerBound(false)
		return nil
	}
	s.hs.SetConsumerBound(true)
	log.Printf("supervisor: bound durable consumer %s/%s on %s", s.cfg.InboxStream, s.cfg.InboxDurable, s.cfg.InboxSubject)
	return sub
}

type fetchResult struct {
	msgs []*nats.Msg
	err  error
}

// mainLoop is §5.2, with one structural change and one bug fix, both of which
// preserve the design's intent:
//
//  1. STRUCTURAL: the Fetch runs in its own goroutine and the loop selects on
//     its result. §5.2 draws a select loop with a 5s tick AND a blocking
//     Fetch(MaxWait=30s) in the same iteration — taken literally, the tick can
//     only fire between fetches, so lastLoopTick would routinely be ~30s old
//     and §5.4's "< 30s" test would flap on an idle, perfectly healthy agent.
//     Selecting on the fetch instead of calling it inline keeps MaxWait at the
//     designed 30s while keeping the tick truly periodic.
//
//  2. BUG FIX: §5.2's `if draining.Load() { continue }` is an unbounded busy
//     loop (nothing in the iteration blocks once draining is set). Draining
//     ends the loop here instead, which is what the drainSignal case was
//     clearly meant to express.
func (s *Supervisor) mainLoop(
	ctx context.Context,
	sub *nats.Subscription,
	cp *Checkpoint,
	cfgFiles RenderedConfig,
	creds Creds,
	credsCh <-chan Creds,
) error {
	tick := time.NewTicker(loopTickInterval)
	defer tick.Stop()

	fetchCh := make(chan fetchResult, 1)
	inflight := false
	rebindAt := time.Time{}

	for {
		s.hs.TouchLoop()

		if s.draining.Load() {
			return nil
		}

		if sub == nil && time.Now().After(rebindAt) {
			sub = s.bindConsumer()
			rebindAt = time.Now().Add(5 * time.Second)
		}

		if !inflight && sub != nil {
			inflight = true
			go func(sub *nats.Subscription) {
				// ★ long-poll, not polling: §5.2's "폴링이 아니라 long-poll.
				// 대기 중 CPU·토큰 소모 0." Using nats.Context (rather than
				// nats.MaxWait) additionally makes the fetch cancellable, so
				// SIGTERM with no child in flight exits in ~2s as §5.5
				// requires instead of waiting out the poll.
				fctx, cancel := context.WithTimeout(ctx, fetchMaxWait)
				defer cancel()
				msgs, err := sub.Fetch(1, nats.Context(fctx))
				fetchCh <- fetchResult{msgs: msgs, err: err}
			}(sub)
		}

		select {
		case <-ctx.Done():
			return nil

		case <-s.drainSignal:
			// §5.5 [t=0] with no child running: the fetch goroutine's context
			// is the same ctx tree, so it unblocks on its own.
			return nil

		case <-tick.C:
			// ★ liveness의 유일한 근거 (§5.2). Idle agents tick here forever.
			s.hs.TouchLoop()

		case nc := <-credsCh:
			creds = nc

		case r := <-fetchCh:
			inflight = false
			s.hs.TouchLoop()

			switch {
			case r.err == nil && len(r.msgs) > 0:
				// nothing — fall through to processing below
			case errors.Is(r.err, nats.ErrTimeout),
				errors.Is(r.err, context.DeadlineExceeded):
				continue // 정상 — 메시지가 없었을 뿐 (§5.2)
			case errors.Is(r.err, context.Canceled):
				continue // draining; the loop top will return
			case r.err != nil:
				log.Printf("supervisor: fetch error: %v", r.err)
				s.hs.SetConsumerBound(false)
				sub = nil
				rebindAt = time.Now().Add(2 * time.Second)
				time.Sleep(500 * time.Millisecond) // bounded backoff
				continue
			default:
				continue
			}

			// Re-read the rendered config: §5.6's "다음 태스크부터".
			if nc, err := loadConfig(); err != nil {
				log.Printf("supervisor: config re-read failed, keeping previous: %v", err)
				s.hs.SetConfigOK(false)
			} else {
				s.hs.SetConfigOK(true)
				if nc.Hash != cfgFiles.Hash {
					log.Printf("supervisor: config changed %s -> %s (applying from this task on)", cfgFiles.Hash, nc.Hash)
				}
				cfgFiles = nc
				s.hs.SetConfigHash(nc.Hash)
			}
			// Re-read credentials: §8.4's rotation is "exec 시점에 파일을 읽으므로
			// 재시작 없이 반영" — this is that read.
			if ncreds, err := loadCreds(); err != nil {
				log.Printf("supervisor: creds re-read failed, keeping previous: %v", err)
				s.hs.SetCredsOK(false)
			} else {
				s.hs.SetCredsOK(true)
				creds = ncreds
			}

			s.handleMessage(ctx, r.msgs[0], cp, cfgFiles, creds)
		}
	}
}

// handleMessage runs one inbox message end to end.
func (s *Supervisor) handleMessage(ctx context.Context, m *nats.Msg, cp *Checkpoint, cfgFiles RenderedConfig, creds Creds) {
	req := parseRequest(m)
	log.Printf("supervisor: inbox message taskId=%s bytes=%d", req.TaskID, len(m.Data))

	if meta, err := m.Metadata(); err == nil {
		cp.NATSMsgSeq = meta.Sequence.Stream
	}

	// Fresh session id for a task that has none yet (see buildArgs for why the
	// id is minted here rather than scraped from output).
	resume := false
	if cp.SessionID == "" {
		cp.SessionID = newUUIDv4()
	}
	s.hs.SetSessionID(cp.SessionID)
	if s.turnsOnSession(cp) > 0 {
		// Continuing the agent's ongoing conversation (§5.2's "--resume
		// <sessionId> # 최초 실행이면 생략" — after the first turn, every turn
		// is a resume).
		resume = true
	}
	cp.Attempt = 1

	s.maybePublishSystemPrompt(cp, cfgFiles)

	out := s.runTurn(ctx, req, cfgFiles, creds, cp, resume, m)
	s.finishTurn(req, cp, out, m)
}

// turnsOnSession reports whether this session has already had a turn. Tracked
// via the transcript directory rather than a counter so it survives a
// checkpoint reset.
func (s *Supervisor) turnsOnSession(cp *Checkpoint) int {
	if cp.SessionID == "" {
		return 0
	}
	marker := filepath.Join(OsirisDir, "session-"+cp.SessionID+".turns")
	b, err := os.ReadFile(marker)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func (s *Supervisor) bumpTurnsOnSession(cp *Checkpoint) {
	if cp.SessionID == "" {
		return
	}
	marker := filepath.Join(OsirisDir, "session-"+cp.SessionID+".turns")
	n := s.turnsOnSession(cp) + 1
	_ = os.WriteFile(marker, []byte(strconv.Itoa(n)+"\n"), 0o644)
}

// finishTurn publishes the outcome, updates the checkpoint, and decides the
// message's ack disposition.
func (s *Supervisor) finishTurn(req TaskRequest, cp *Checkpoint, out TaskOutcome, m *nats.Msg) {
	s.bumpTurnsOnSession(cp)
	s.hs.IncTurnsCompleted()

	payload := map[string]any{
		"employeeId": s.employeeID,
		"teamId":     s.teamID,
		"sessionId":  cp.SessionID,
		"exitCode":   out.ExitCode,
		"durationMs": out.DurationMS,
		"numTurns":   out.NumTurns,
		"costUsd":    out.CostUSD,
		"result":     out.Result,
	}

	switch out.Status {
	case "done":
		s.publishTaskEvent(req, cp.Attempt, "done", payload)
		if req.ReplySubject != "" {
			s.bus.PublishEphemeral(req.ReplySubject, payload)
		}
		// §5.5: "안에 끝나면 정상 완료 경로 → 체크포인트 삭제". The sessionId
		// survives (it is agent-level, §5.6); only the in-flight turn fields
		// are cleared.
		cp.Status = StatusIdle
		cp.TaskID, cp.ReplySubject, cp.Attempt, cp.NATSMsgSeq = "", "", 0, 0
		cp.TurnStartedAt = time.Time{}
		_ = saveCheckpoint(*cp)
		_ = m.Ack()

	case "interrupted":
		// The checkpoint (already fsynced inside runTurn, BEFORE any signal)
		// is now the resume authority, so the message is acked rather than
		// left for JetStream redelivery.
		//
		// DECISION (documented in IMPLEMENTATION-NOTES.md): acking here avoids
		// double-delivery on restart, where the agent would otherwise resume
		// from the checkpoint AND receive the redelivered message — two turns
		// for one task. §5.5 makes the checkpoint plus
		// `task.<id>.event=interrupted` ("core가 태스크를 in-flight로 유지")
		// the durability story for an interrupted turn; JetStream redelivery
		// is the durability story for a turn that never STARTED.
		s.publishTaskEvent(req, cp.Attempt, "interrupted_final", payload)
		_ = m.Ack()

	default: // "failed"
		s.publishTaskEvent(req, cp.Attempt, "failed", payload)
		cp.Status = StatusIdle
		cp.TaskID, cp.ReplySubject, cp.Attempt = "", "", 0
		_ = saveCheckpoint(*cp)
		// Nak so JetStream redelivers within max_deliver=3 (§7.2). A failed
		// turn is worth retrying (transient gateway error, transient tool
		// failure); an infinitely retried one is not, which is what
		// max_deliver caps.
		_ = m.NakWithDelay(30 * time.Second)
	}

	log.Printf("supervisor: turn finished taskId=%s status=%s exit=%d cost=%.6f duration=%dms",
		req.TaskID, out.Status, out.ExitCode, out.CostUSD, out.DurationMS)
}

// resumeInterrupted is §5.5's resume block.
func (s *Supervisor) resumeInterrupted(ctx context.Context, cp *Checkpoint, cfgFiles RenderedConfig, creds Creds) {
	cp.Attempt++
	log.Printf("supervisor: found interrupted checkpoint taskId=%s sessionId=%s attempt=%d",
		cp.TaskID, cp.SessionID, cp.Attempt)

	if cp.Attempt > maxResumeAttempts {
		// §5.5 step 3: "attempt > 3 이면 재개하지 않고 태스크 blocked + 팀장
		// 에스컬레이션".
		log.Printf("supervisor: attempt %d exceeds %d — NOT resuming; task blocked + escalating", cp.Attempt, maxResumeAttempts)
		req := TaskRequest{TaskID: cp.TaskID, ReplySubject: cp.ReplySubject}
		s.publishTaskEvent(req, cp.Attempt, "blocked", map[string]any{
			"reason":     "RESUME_ATTEMPTS_EXHAUSTED",
			"attempts":   cp.Attempt - 1,
			"employeeId": s.employeeID,
		})
		// Escalation goes to master.inbox, which is the one cross-team hop an
		// agent's NATS ACL permits (§7.3). Publishing straight to a team lead
		// would be denied by the server for any other team, by design.
		s.bus.PublishDurable("master.inbox", MsgID(cp.TaskID, cp.Attempt, "escalation"), map[string]any{
			"kind": "escalation", "reason": "RESUME_ATTEMPTS_EXHAUSTED",
			"taskId": cp.TaskID, "employeeId": s.employeeID, "teamId": s.teamID,
			"prompt": fmt.Sprintf("에이전트 %s의 태스크 %s가 %d회 재개 실패로 blocked 상태입니다. 사람의 개입이 필요합니다.",
				s.employeeID, cp.TaskID, cp.Attempt-1),
		})
		cp.Status = StatusIdle
		cp.TaskID, cp.ReplySubject, cp.Attempt = "", "", 0
		_ = saveCheckpoint(*cp)
		return
	}

	if cp.SessionID == "" {
		log.Printf("supervisor: interrupted checkpoint has no sessionId — cannot resume, dropping")
		cp.Status = StatusIdle
		_ = saveCheckpoint(*cp)
		return
	}

	req := TaskRequest{TaskID: cp.TaskID, ReplySubject: cp.ReplySubject, Prompt: resumePrompt(*cp)}
	if req.TaskID == "" {
		req.TaskID = "resume-" + cp.SessionID
	}
	s.hs.SetSessionID(cp.SessionID)
	s.maybePublishSystemPrompt(cp, cfgFiles)

	out := s.runTurn(ctx, req, cfgFiles, creds, cp, true, nil)
	s.bumpTurnsOnSession(cp)

	payload := map[string]any{
		"employeeId": s.employeeID, "sessionId": cp.SessionID,
		"exitCode": out.ExitCode, "durationMs": out.DurationMS,
		"costUsd": out.CostUSD, "result": out.Result, "resumed": true,
	}
	if out.Status == "done" {
		s.publishTaskEvent(req, cp.Attempt, "done", payload)
		if req.ReplySubject != "" {
			s.bus.PublishEphemeral(req.ReplySubject, payload)
		}
		cp.Status = StatusIdle
		cp.TaskID, cp.ReplySubject, cp.Attempt = "", "", 0
		_ = saveCheckpoint(*cp)
	} else if out.Status == "interrupted" {
		s.publishTaskEvent(req, cp.Attempt, "interrupted_final", payload)
	} else {
		s.publishTaskEvent(req, cp.Attempt, "failed", payload)
		cp.Status = StatusIdle
		_ = saveCheckpoint(*cp)
	}
	log.Printf("supervisor: resume finished taskId=%s status=%s", req.TaskID, out.Status)
}

// runOneShot is §5.7: "supervisor는 --one-shot 모드로 뜬다: 인박스를 바인드하지
// 않고 인자로 받은 태스크만 수행 후 종료."
func (s *Supervisor) runOneShot(ctx context.Context, cfgFiles RenderedConfig, creds Creds) error {
	cp, _ := loadCheckpoint()
	if cp.SessionID == "" {
		cp.SessionID = newUUIDv4()
	}
	taskID := s.cfg.OneShotTaskID
	if taskID == "" {
		taskID = "oneshot-" + newUUIDv4()
	}
	req := TaskRequest{TaskID: taskID, Prompt: s.cfg.OneShotPrompt}
	s.hs.SetPhase(health.PhaseReady)
	s.maybePublishSystemPrompt(&cp, cfgFiles)
	out := s.runTurn(ctx, req, cfgFiles, creds, &cp, false, nil)
	s.publishTaskEvent(req, 1, out.Status, map[string]any{
		"employeeId": s.employeeID, "oneShot": true, "exitCode": out.ExitCode,
		"costUsd": out.CostUSD, "result": out.Result,
	})
	sent, remaining := s.bus.FlushOutbox()
	log.Printf("supervisor: one-shot complete status=%s (outbox flushed=%d remaining=%d)", out.Status, sent, remaining)
	dctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.bus.Drain(dctx)
	if out.Status != "done" {
		return fmt.Errorf("one-shot task %s ended with status %s", taskID, out.Status)
	}
	return nil
}

// finalDrain is §5.5's tail, common to all three shutdown branches: flush the
// outbox, unbind the consumer, exit 0.
func (s *Supervisor) finalDrain(ctx context.Context, sub *nats.Subscription, servers ...*http.Server) {
	s.hs.SetPhase(health.PhaseDraining)
	s.bus.PublishEphemeral("heartbeat.agent."+s.employeeID, s.heartbeatPayload("draining"))

	if sent, remaining := s.bus.FlushOutbox(); sent > 0 || remaining > 0 {
		log.Printf("supervisor: outbox flush on drain: sent=%d remaining=%d", sent, remaining)
	}
	if sub != nil {
		// Unbind, do NOT delete: the durable consumer belongs to the
		// orchestrator (it created it) and its stored ack floor is what makes
		// a redeploy lossless. Deleting it here would discard the pending set.
		_ = sub.Unsubscribe()
		s.hs.SetConsumerBound(false)
	}

	dctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.bus.Drain(dctx)

	for _, srv := range servers {
		if srv == nil {
			continue
		}
		sctx, c := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(sctx)
		c()
	}
	s.hs.SetPhase(health.PhaseStopped)
	log.Printf("osiris-supervisor: stopped cleanly (phase=Stopped)")
	_ = ctx
}

// --- background loops -----------------------------------------------------

func (s *Supervisor) heartbeatLoop(stop <-chan struct{}) {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			state := "idle"
			switch s.hs.Phase() {
			case health.PhaseWorking:
				state = "working"
			case health.PhaseDraining:
				state = "draining"
			case health.PhaseBooting:
				state = "booting"
			}
			s.bus.PublishEphemeral("heartbeat.agent."+s.employeeID, s.heartbeatPayload(state))
		}
	}
}

func (s *Supervisor) heartbeatPayload(state string) map[string]any {
	live, _ := s.hs.Livez()
	ready, _ := s.hs.Readyz()
	return map[string]any{
		"employeeId": s.employeeID,
		"teamId":     s.teamID,
		// §5.4: busy/idle is expressed HERE, not in readiness.
		"state":            state,
		"phase":            health.PhaseName(s.hs.Phase()),
		"live":             live,
		"ready":            ready,
		"configHash":       s.hs.ConfigHash(),
		"activeTaskPoints": s.activeTaskPointsSnapshot(),
		"maxTaskPoints":    s.cfg.MaxTaskPoints,
		"droppedLogLines":  s.hs.DroppedLogLines(),
		"claudeVersion":    s.claudeVersion,
		"ts":               time.Now().UTC(),
	}
}

func (s *Supervisor) activeTaskPointsSnapshot() int64 {
	_, body := s.hs.Readyz()
	if v, ok := body["activeTaskPoints"].(int64); ok {
		return v
	}
	return 0
}

// gatewayLoop is §5.4's gatewayHealthy probe. It re-reads the base URL from
// creds each round so a rotated gateway endpoint is picked up without a
// restart, and it goes through the egress proxy exactly like the child does.
func (s *Supervisor) gatewayLoop(stop <-chan struct{}, creds *Creds) {
	probe := newGatewayProbe(s.egressProxy, s.noProxy)
	run := func() {
		c, err := loadCreds()
		if err != nil {
			s.hs.SetCredsOK(false)
			s.hs.SetGatewayHealthy(false)
			return
		}
		s.hs.SetCredsOK(true)
		ctx, cancel := context.WithTimeout(context.Background(), gatewayProbeTimeout+time.Second)
		defer cancel()
		s.hs.SetGatewayHealthy(probe.check(ctx, c.GatewayBaseURL, c.GatewayHealthPath))
	}
	run()
	t := time.NewTicker(gatewayProbeEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			run()
		}
	}
}

func (s *Supervisor) volumeLoop(stop <-chan struct{}) {
	t := time.NewTicker(volumeProbeEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.hs.SetVolumeWritable(probeWorkspaceWritable() == nil)
		}
	}
}

func (s *Supervisor) outboxLoop(stop <-chan struct{}) {
	t := time.NewTicker(outboxFlushEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if sent, _ := s.bus.FlushOutbox(); sent > 0 {
				log.Printf("supervisor: outbox flushed %d spooled message(s)", sent)
			}
		}
	}
}

// credsWatchLoop is §5.2's `creds := watchCreds("/var/run/osiris/creds")`.
//
// This is the SUPERVISOR half of credential rotation; the orchestrator half is
// WorkloadDriver.SyncCredentials. What this loop actually has to DO is narrow,
// because most credentials are read at exec time (see loadCreds' comment): the
// only one that needs an action on change is the NATS password, because a live
// TCP connection cannot re-authenticate — §8.4: "30일. 회전 시 supervisor
// 재연결(무중단)".
func (s *Supervisor) credsWatchLoop(stop <-chan struct{}, initial Creds, out chan<- Creds) {
	last := initial
	t := time.NewTicker(credsWatchEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			c, err := loadCreds()
			if err != nil {
				s.hs.SetCredsOK(false)
				continue
			}
			s.hs.SetCredsOK(true)
			if c.Fingerprint == last.Fingerprint {
				continue
			}
			log.Printf("supervisor: credential rotation detected (%s -> %s)", last.Fingerprint, c.Fingerprint)
			if c.NATSUser != last.NATSUser || c.NATSPassword != last.NATSPassword {
				log.Printf("supervisor: NATS credentials rotated — reconnecting (no restart, §8.4)")
				if err := s.bus.Connect(s.cfg.NATSURL, c); err != nil {
					log.Printf("supervisor: reconnect with rotated NATS creds failed: %v", err)
				} else {
					s.hs.SetConsumerBound(false) // main loop rebinds on the new connection
				}
			}
			last = c
			select {
			case out <- c:
			default:
			}
		}
	}
}

// subscribeConfigReload wires §7.1's agent.<employeeId>.config.reload. The
// handler intentionally does almost nothing: the config is re-read at the start
// of every task anyway (§5.6's "다음 태스크부터"), so the notification's only
// job is to make that fact loggable and observable. Re-rendering mid-turn is
// explicitly forbidden ("진행 중 턴은 건드리지 않는다").
func (s *Supervisor) subscribeConfigReload() {
	subj := "agent." + s.employeeID + ".config.reload"
	_, err := s.bus.Subscribe(subj, func(m *nats.Msg) {
		log.Printf("supervisor: %s received (%s) — new config applies from the NEXT task, in-flight turn untouched", subj, strings.TrimSpace(string(m.Data)))
		if c, err := loadConfig(); err == nil {
			s.hs.SetConfigOK(true)
			s.hs.SetConfigHash(c.Hash)
		} else {
			s.hs.SetConfigOK(false)
		}
	})
	if err != nil {
		log.Printf("supervisor: subscribe %s failed: %v", subj, err)
	}
}

// --- log consumption ------------------------------------------------------

// consumeLogs is the single consumer of the child's output. Running the
// parsing/publishing here (rather than in the pipe readers) is what lets the
// readers stay non-blocking, and running it in ONE goroutine keeps transcript
// file ordering deterministic without a lock.
func (s *Supervisor) consumeLogs(stop <-chan struct{}) {
	var (
		curTaskID string
		tf        *os.File
	)
	closeTF := func() {
		if tf != nil {
			_ = tf.Close()
			tf = nil
		}
	}
	defer closeTF()

	for {
		select {
		case <-stop:
			return
		case line := <-s.logs.C():
			if line.Text == "" {
				continue
			}
			var sl streamLine
			isJSON := json.Unmarshal([]byte(line.Text), &sl) == nil && sl.Type != ""

			if line.Stream == "stderr" {
				// stderr is not stream-json; it is diagnostics. Core NATS only.
				s.bus.PublishEphemeral("ops.log."+s.employeeID, map[string]any{
					"employeeId": s.employeeID, "stream": "stderr",
					"line": line.Text, "ts": time.Now().UTC(),
				})
				continue
			}

			taskID := s.currentTaskID()
			if taskID != curTaskID {
				closeTF()
				curTaskID = taskID
			}
			if tf == nil && curTaskID != "" {
				if err := os.MkdirAll(TranscriptDir, 0o755); err == nil {
					f, err := os.OpenFile(filepath.Join(TranscriptDir, sanitize(curTaskID)+".jsonl"),
						os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
					if err == nil {
						tf = f
					}
				}
			}
			// §5.2: "전체 라인 → TRANSCRIPT 스트림(30일) +
			// /workspace/.osiris/transcripts/<taskId>.jsonl"
			if tf != nil {
				_, _ = tf.WriteString(line.Text + "\n")
			}
			s.bus.PublishDurable("ops.transcript."+s.employeeID, "", map[string]any{
				"employeeId": s.employeeID, "taskId": curTaskID,
				"kind": "stream_json", "line": json.RawMessage(orNull(line.Text, isJSON)),
				"raw": maybeRaw(line.Text, isJSON), "ts": time.Now().UTC(),
			})

			if !isJSON {
				continue
			}

			switch sl.Type {
			case "assistant", "user":
				// §5.2: "도구 호출/결과 → ops.log.<id> (Core NATS, 비영속)"
				s.bus.PublishEphemeral("ops.log."+s.employeeID, map[string]any{
					"employeeId": s.employeeID, "taskId": curTaskID,
					"type": sl.Type, "message": sl.Message, "ts": time.Now().UTC(),
				})
			case "result":
				// §5.2: "최종 result 라인의 total_cost_usd·토큰 수 → ops.cost.<id>"
				s.bus.PublishDurable("ops.cost."+s.employeeID, MsgID(curTaskID, 1, "cost"), map[string]any{
					"employeeId": s.employeeID, "taskId": curTaskID,
					"totalCostUsd": sl.CostUSD, "usage": sl.Usage,
					"numTurns": sl.NumTurns, "isError": sl.IsError,
					"modelAlias": s.modelAlias, "ts": time.Now().UTC(),
				})
				s.resultMu.Lock()
				s.lastResult = TaskOutcome{
					Result: sl.Result, CostUSD: sl.CostUSD, SessionID: sl.SessionID,
					NumTurns: sl.NumTurns, IsError: sl.IsError,
				}
				s.resultMu.Unlock()
			case "system":
				if sl.SessionID != "" {
					s.hs.SetSessionID(sl.SessionID)
				}
			}
		}
	}
}

func (s *Supervisor) currentTaskID() string {
	_, body := s.hs.Readyz()
	if v, ok := body["activeTaskId"].(string); ok {
		return v
	}
	return ""
}

func orNull(text string, isJSON bool) string {
	if isJSON {
		return text
	}
	return "null"
}

func maybeRaw(text string, isJSON bool) string {
	if isJSON {
		return ""
	}
	return text
}

func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "unknown"
	}
	return string(out)
}

// maybePublishSystemPrompt implements 20-ontology-schema.md §3.2.1's audit
// requirement: "렌더 결과가 바뀔 때마다 supervisor가 렌더된 프롬프트 전문을
// ops.transcript.<employeeId>에 kind: 'system_prompt' 레코드로 1회 발행하며,
// 이는 TRANSCRIPT 스트림에 30일 보존된다." That record is the ONLY history of
// what a given agent was instructed to be, because the graph deliberately does
// not keep previous promptTemplateMd revisions.
func (s *Supervisor) maybePublishSystemPrompt(cp *Checkpoint, cfg RenderedConfig) {
	if cfg.Hash == "" || cp.LastConfigHash == cfg.Hash {
		return
	}
	s.bus.PublishDurable("ops.transcript."+s.employeeID, "sysprompt:"+cfg.Hash, map[string]any{
		"employeeId":   s.employeeID,
		"teamId":       s.teamID,
		"kind":         "system_prompt",
		"configHash":   cfg.Hash,
		"renderedText": cfg.Prompt,
		"allowedTools": cfg.AllowedTools,
		"ts":           time.Now().UTC(),
	})
	cp.LastConfigHash = cfg.Hash
	_ = saveCheckpoint(*cp)
	log.Printf("supervisor: published kind=system_prompt transcript record for configHash=%s (§3.2.1)", cfg.Hash)
}

func (s *Supervisor) publishTaskEvent(req TaskRequest, attempt int, eventType string, extra map[string]any) {
	if req.TaskID == "" {
		return
	}
	payload := map[string]any{
		"taskId": req.TaskID, "event": eventType, "attempt": attempt,
		"employeeId": s.employeeID, "teamId": s.teamID, "ts": time.Now().UTC(),
	}
	for k, v := range extra {
		payload[k] = v
	}
	s.bus.PublishDurable("task."+req.TaskID+".event", MsgID(req.TaskID, attempt, eventType), payload)
}

// parseRequest accepts either the JSON envelope or raw text (see TaskRequest).
func parseRequest(m *nats.Msg) TaskRequest {
	var req TaskRequest
	if err := json.Unmarshal(m.Data, &req); err != nil || req.Prompt == "" {
		req = TaskRequest{Prompt: string(m.Data)}
	}
	if req.TaskID == "" {
		if meta, err := m.Metadata(); err == nil {
			req.TaskID = fmt.Sprintf("inbox-%s-%d", strings.ReplaceAll(m.Subject, ".", "_"), meta.Sequence.Stream)
		} else {
			req.TaskID = "inbox-" + newUUIDv4()
		}
	}
	if req.Points <= 0 {
		req.Points = 1
	}
	return req
}
