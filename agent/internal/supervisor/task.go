package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"osiris-agent/internal/health"
)

// TaskRequest is the inbox message envelope. A message that is not valid JSON
// (or is JSON without a taskId) is still accepted: its whole body becomes the
// prompt and the taskId is synthesized from the JetStream sequence. Rejecting
// malformed inbox messages would mean a human typing into a team inbox gets
// silence, and §7.1 makes team.<team>.inbox.<employeeId> the shared channel
// for "사람·AI 공통".
type TaskRequest struct {
	TaskID       string `json:"taskId"`
	Prompt       string `json:"prompt"`
	ReplySubject string `json:"replySubject,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Points       int    `json:"points,omitempty"`
	Attempt      int    `json:"attempt,omitempty"`
	FromEmployee string `json:"from,omitempty"`
}

// TaskOutcome is what runTurn reports back to the loop.
type TaskOutcome struct {
	Status      string // "done" | "failed" | "interrupted"
	ExitCode    int
	Result      string
	CostUSD     float64
	SessionID   string
	NumTurns    int
	DurationMS  int64
	IsError     bool
	Interrupted bool
}

// child holds a running `claude` process and everything needed to signal it.
type child struct {
	cmd  *exec.Cmd
	pgid int
	done chan error
}

// buildArgs assembles §5.2's command line verbatim, with the deviations that
// the CLI's actual behaviour forces. Each deviation is annotated; none of them
// change what the design is trying to achieve.
//
//	claude -p --bare
//	  --resume <sessionId>                             # 최초 실행이면 생략
//	  --settings   /var/run/osiris/config/settings.json # --bare는 자동탐색 안 함
//	  --mcp-config /var/run/osiris/config/mcp.json      # --resume이 복원 안 함
//	  --allowedTools "<PermissionProfile 파생 목록>"
//	  --permission-mode dontAsk
//	  --add-dir /workspace/repo
//	  --output-format stream-json
func buildArgs(cfg RenderedConfig, sessionID string, resume bool, modelAlias string) []string {
	args := []string{"-p", "--bare"}

	if resume && sessionID != "" {
		// §5.5's resume path.
		args = append(args, "--resume", sessionID)
	} else if sessionID != "" {
		// DEVIATION (additive, not a change of intent): the design says
		// "최초 실행이면 --resume 생략" but does not say where the sessionId
		// comes from. Two options existed: (a) let the CLI mint one and scrape
		// it out of the stream-json output, or (b) mint a UUID ourselves and
		// pass --session-id. (b) is chosen because with (a) there is a window
		// — between exec and the first output line — in which a SIGTERM would
		// leave a checkpoint with NO sessionId, i.e. an unresumable
		// interrupted turn. §5.5's whole promise is that an interrupted turn
		// is resumable, so the id must exist BEFORE the process does.
		args = append(args, "--session-id", sessionID)
	}

	args = append(args,
		// §5.6: "--bare가 자동탐색을 안 하고 --resume이 복원하지 않으므로 매
		// 호출 명시 주입". Both files are re-passed on EVERY invocation,
		// including resumes.
		"--settings", cfg.SettingsPath,
		"--mcp-config", cfg.MCPPath,
		// Only the MCP servers we injected — never a user-level or
		// project-level mcp config that might be sitting in the workspace
		// volume. Without this, a task could write its own .mcp.json into
		// /workspace and gain tools the PermissionProfile never granted.
		"--strict-mcp-config",
		"--permission-mode", "dontAsk",
		"--add-dir", RepoDir,
		"--output-format", "stream-json",
		// stream-json in print mode requires --verbose (CLI constraint,
		// verified empirically).
		"--verbose",
	)

	if len(cfg.AllowedTools) > 0 {
		args = append(args, "--allowedTools")
		args = append(args, cfg.AllowedTools...)
	}

	if cfg.Prompt != "" {
		// DEVIATION forced by --bare: `--bare` explicitly disables "CLAUDE.md
		// auto-memory / auto-discovery" (its own --help says so, verified on
		// claude 2.1.220), so simply having the rendered CLAUDE.md on disk
		// would NOT put the persona in front of the model — the agent would
		// run with no role at all. The rendered prompt is therefore injected
		// as --append-system-prompt. This keeps §3.2.1's contract intact (the
		// text is rendered once, deterministically, from :Role.promptTemplateMd)
		// and keeps it in ConfigHash, i.e. still "다음 태스크부터" (§5.6).
		args = append(args, "--append-system-prompt", cfg.Prompt)
	}

	if modelAlias != "" {
		// §9.2 item 5: OSiris sends only the logical alias
		// (osiris/planner|worker|cheap) and the gateway resolves it. Verified
		// empirically that the CLI passes an unknown --model value through to
		// the request body untouched.
		args = append(args, "--model", modelAlias)
	}
	return args
}

// childEnv builds the child's environment.
//
// §5.2: "ANTHROPIC_BASE_URL, ANTHROPIC_AUTH_TOKEN, GIT_ASKPASS는 자식 프로세스
// env에만 주입한다. supervisor 자신의 환경에는 두지 않는다(장수 프로세스의
// env는 /proc/<pid>/environ으로 오래 노출된다)."
//
// The env is built from scratch (not os.Environ()) so that a secret cannot
// leak in from the supervisor's own environment by accident, and so that the
// child's /proc/<pid>/environ contains exactly the intended set and nothing
// more. It lives only as long as one turn.
func (s *Supervisor) childEnv(c Creds, taskID string) []string {
	env := []string{
		"PATH=" + s.pathEnv,
		"HOME=" + WorkspaceDir,
		"TMPDIR=/tmp",
		"LANG=C.UTF-8",
		"CI=1",
		// §5.6: session JSONL must live on the workspace volume so the id and
		// the data it names cannot be separated.
		"CLAUDE_CONFIG_DIR=" + ClaudeConfigDir,
		// No telemetry / no autoupdate from inside an agent: the egress
		// allowlist would block them anyway, and every blocked attempt is a
		// noisy ops.egress audit record for nothing.
		"DISABLE_AUTOUPDATER=1",
		"DISABLE_TELEMETRY=1",
		"DISABLE_ERROR_REPORTING=1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",

		// --- the gateway (§9) -----------------------------------------------
		"ANTHROPIC_BASE_URL=" + c.GatewayBaseURL,
		// Verified empirically on claude 2.1.220 with a request-inspecting
		// mock: --bare + ANTHROPIC_AUTH_TOKEN produces
		// `Authorization: Bearer <token>` on POST <BASE_URL>/v1/messages,
		// which is exactly §9.2 contract item 2.
		"ANTHROPIC_AUTH_TOKEN=" + c.GatewayToken,

		// --- egress (A3) ----------------------------------------------------
		// Every outbound HTTP hop, INCLUDING the LLM call, goes through the
		// CONNECT proxy. §8.3: "게이트웨이 접근도 egress 프록시 경유. 에이전트가
		// ANTHROPIC_BASE_URL을 스스로 바꿔치기해도 허용목록에 없으면 나가지
		// 못한다."
		"HTTP_PROXY=" + s.egressProxy,
		"HTTPS_PROXY=" + s.egressProxy,
		"http_proxy=" + s.egressProxy,
		"https_proxy=" + s.egressProxy,
		"NO_PROXY=" + s.noProxy,
		"no_proxy=" + s.noProxy,

		"GIT_TERMINAL_PROMPT=0",

		// Consumed by this same binary running as an MCP server (see
		// internal/mcpserver): where to send an approval request. Loopback
		// only, and a DIFFERENT listener from the health port.
		"OSIRIS_GATE_URL=http://" + GateAddr,
		"OSIRIS_EMPLOYEE_ID=" + s.employeeID,
		"OSIRIS_TEAM_ID=" + s.teamID,
		"OSIRIS_TASK_ID=" + taskID,
	}

	// §9.2 item 6: the gateway must be able to attribute cost, so employee
	// and task id travel as request headers.
	env = append(env, "ANTHROPIC_CUSTOM_HEADERS=X-OSiris-Employee-Id: "+s.employeeID+"\nX-OSiris-Task-Id: "+taskID)

	if c.GitToken != "" && s.askpassPath != "" {
		env = append(env, "GIT_ASKPASS="+s.askpassPath)
	}
	return env
}

// startChild launches `claude` in its OWN process group so that §5.5 step 5's
// "프로세스 그룹에 SIGKILL" is possible: a Bash tool call that spawned a
// grandchild must die with its parent, and killing only the direct pid would
// leave the grandchild holding the workspace and the tmpfs.
func (s *Supervisor) startChild(cfg RenderedConfig, c Creds, args []string, prompt, taskID string) (*child, error) {
	cmd := exec.Command(s.claudeBin, args...)
	cmd.Dir = RepoDir
	cmd.Env = s.childEnv(c, taskID)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid
	}
	s.childPID.Store(int32(cmd.Process.Pid))

	// Two dedicated pump goroutines. They are the ONLY readers of the child's
	// pipes and they can never block downstream (LogPipe.TrySend has a default
	// branch), which is what keeps the child from blocking on write(2) and
	// what keeps the health server independent of the child's output volume
	// (§5.4's second invariant).
	go s.logs.Pump("stdout", stdout)
	go s.logs.Pump("stderr", stderr)

	ch := &child{cmd: cmd, pgid: pgid, done: make(chan error, 1)}
	go func() { ch.done <- cmd.Wait() }()
	return ch, nil
}

// signalGroup sends sig to the child's whole process group.
func (c *child) signalGroup(sig syscall.Signal) {
	if c.pgid > 0 {
		_ = syscall.Kill(-c.pgid, sig)
		return
	}
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Signal(sig)
	}
}

// runTurn executes one turn and returns its outcome. THIS is where §5.4's
// central claim is made true:
//
//	"40분짜리 턴이 도는 동안에도 supervisor의 select 루프는 5초 티커로 계속
//	 돌기 때문에 lastLoopTick이 갱신된다. 즉 '작업 중이라서 probe가 실패한다'는
//	 상황이 구조적으로 발생할 수 없다."
//
// §5.2's pseudocode calls runTask() straight-line from the main loop body,
// which taken literally would mean the 5s tick case cannot run for the whole
// duration of a turn — and then lastLoopTick WOULD go stale and a long turn
// WOULD be killed, the precise disaster §5.4 forbids. The resolution is that
// the select loop continues HERE: the wait for the child is itself a select
// with the 5s ticker in it, so control flow keeps proving it is alive without
// a separate always-green watchdog goroutine (which would have made
// lastLoopTick meaningless as a wedge detector).
func (s *Supervisor) runTurn(
	ctx context.Context,
	req TaskRequest,
	cfg RenderedConfig,
	c Creds,
	cp *Checkpoint,
	resume bool,
	msg AckExtender,
) TaskOutcome {

	args := buildArgs(cfg, cp.SessionID, resume, s.modelAlias)
	prompt := req.Prompt

	s.hs.SetPhase(health.PhaseWorking)
	s.hs.SetActiveTaskID(req.TaskID)
	s.hs.SetActiveTaskPoints(int64(max(1, req.Points)))
	defer func() {
		s.hs.SetActiveTaskPoints(0)
		s.hs.SetActiveTaskID("")
		if !s.draining.Load() {
			s.hs.SetPhase(health.PhaseReady)
		}
	}()

	started := time.Now()
	cp.TaskID = req.TaskID
	cp.TurnStartedAt = started.UTC()
	cp.ReplySubject = req.ReplySubject
	cp.Status = StatusRunning
	cp.WorkspaceGitSha = workspaceGitSha()
	if cp.Attempt == 0 {
		cp.Attempt = 1
	}
	if err := saveCheckpoint(*cp); err != nil {
		log.Printf("supervisor: checkpoint (running) write failed: %v", err)
	}

	s.publishTaskEvent(req, cp.Attempt, "started", map[string]any{
		"sessionId": cp.SessionID, "resumed": resume, "configHash": cfg.Hash,
	})

	ch, err := s.startChild(cfg, c, args, prompt, req.TaskID)
	if err != nil {
		s.childPID.Store(0)
		log.Printf("supervisor: failed to start %s: %v", s.claudeBin, err)
		return TaskOutcome{Status: "failed", IsError: true, Result: "failed to start claude: " + err.Error()}
	}
	log.Printf("supervisor: turn started taskId=%s pid=%d resume=%v sessionId=%s", req.TaskID, ch.cmd.Process.Pid, resume, cp.SessionID)

	// --- the select loop that keeps liveness true during a long turn -------
	tick := time.NewTicker(loopTickInterval)
	defer tick.Stop()
	inProgress := time.NewTicker(inProgressInterval)
	defer inProgress.Stop()

	var (
		waitErr        error
		exited         bool
		drainDeadline  time.Time
		termSent       bool
		killDeadline   time.Time
		interrupted    bool
		drainAnnounced bool
	)

	for !exited {
		select {
		case waitErr = <-ch.done:
			exited = true

		case <-tick.C:
			// ★ liveness의 유일한 근거 (§5.2). A turn in progress keeps this
			// fresh; a wedged supervisor does not.
			s.hs.TouchLoop()

		case <-inProgress.C:
			// §5.2: `go m.InProgress(every(25 * time.Second))` — extend
			// ack_wait(30m) so a long turn is not redelivered underneath us.
			if msg != nil {
				if err := msg.InProgress(); err != nil {
					log.Printf("supervisor: InProgress() failed for taskId=%s: %v", req.TaskID, err)
				}
			}

		case <-s.drainSignal:
			// §5.5, the "자식(claude) 실행 중" branch. Reached at most once;
			// drainSignal is closed, so subsequent iterations fall through to
			// the deadline checks below.
			if !drainAnnounced {
				drainAnnounced = true
				interrupted = true
				s.hs.SetPhase(health.PhaseDraining)

				// 1) checkpoint fsync FIRST — before any signal, before any
				//    publish. If the runtime SIGKILLs us at t+1ms, this file
				//    is the only thing that survives.
				cp.Status = StatusInterrupted
				cp.WorkspaceGitSha = workspaceGitSha()
				if err := saveCheckpoint(*cp); err != nil {
					log.Printf("supervisor: CRITICAL: interrupted checkpoint write failed: %v", err)
				} else {
					log.Printf("supervisor: checkpoint(interrupted) fsynced for taskId=%s attempt=%d", req.TaskID, cp.Attempt)
				}

				// 2) tell core the task is still in flight.
				s.publishTaskEvent(req, cp.Attempt, "interrupted", map[string]any{
					"reason": "infrastructure_drain", "sessionId": cp.SessionID,
					"turnStartedAt": started.UTC(), "workspaceGitSha": cp.WorkspaceGitSha,
				})

				// 3) NO SIGNAL YET. Wait drain.waitSeconds for the turn to
				//    finish on its own — killing a turn that was 5 seconds
				//    from done would waste the whole turn's tokens.
				drainDeadline = time.Now().Add(time.Duration(s.drainSeconds) * time.Second)
				log.Printf("supervisor: draining — waiting up to %ds for the in-flight turn to finish naturally", s.drainSeconds)
			}

		case <-time.After(500 * time.Millisecond):
			// Deadline pump for the drain escalation. Kept as its own case
			// (rather than nested timers) so the ticker cases above are never
			// starved while escalating.
		}

		if drainAnnounced && !termSent && !drainDeadline.IsZero() && time.Now().After(drainDeadline) {
			// 4) grace expired → SIGTERM the child (Claude Code aborts the
			//    turn, tears down its Bash process tree, runs SessionEnd,
			//    exits 143).
			log.Printf("supervisor: drain grace expired — SIGTERM to child pgid=%d", ch.pgid)
			ch.signalGroup(syscall.SIGTERM)
			termSent = true
			killDeadline = time.Now().Add(childTermGrace)
		}
		if termSent && !killDeadline.IsZero() && time.Now().After(killDeadline) {
			// 5) still alive after childTermGrace → SIGKILL the whole group.
			log.Printf("supervisor: child did not exit %s after SIGTERM — SIGKILL to process group %d", childTermGrace, ch.pgid)
			ch.signalGroup(syscall.SIGKILL)
			killDeadline = time.Now().Add(childTermGrace) // re-arm; loop exits on ch.done
		}
	}

	s.childPID.Store(0)
	s.hs.TouchLoop()

	exitCode := 0
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}

	out := s.collectOutcome(req.TaskID)
	out.ExitCode = exitCode
	out.DurationMS = time.Since(started).Milliseconds()
	if out.SessionID != "" && out.SessionID != cp.SessionID {
		// The CLI is authoritative about its own session id; if it forked or
		// minted a different one, follow it or the next --resume is a lie.
		log.Printf("supervisor: session id changed %s -> %s", cp.SessionID, out.SessionID)
		cp.SessionID = out.SessionID
	}

	switch {
	case interrupted && exitCode != 0:
		out.Status = "interrupted"
		out.Interrupted = true
	case exitCode == 0 && !out.IsError:
		out.Status = "done"
	default:
		out.Status = "failed"
	}
	return out
}

// collectOutcome drains whatever the result-line collector recorded for this
// task. The collector runs in the log-consumer goroutine (see loop.go) so that
// parsing stream-json never happens on the pipe-reading goroutine.
func (s *Supervisor) collectOutcome(taskID string) TaskOutcome {
	s.resultMu.Lock()
	defer s.resultMu.Unlock()
	r := s.lastResult
	s.lastResult = TaskOutcome{}
	_ = taskID
	return r
}

// workspaceGitSha is §5.5's checkpoint field and §5.5's resume step 1
// ("워크스페이스 무결성 확인"). Best-effort: a workspace with no repo yet has
// no sha and that is not an error.
func workspaceGitSha() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", RepoDir, "rev-parse", "HEAD")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + WorkspaceDir}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// dirtyFiles is §5.5's resume prompt input ("미커밋 변경: <파일 목록>").
func dirtyFiles() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", RepoDir, "status", "--porcelain")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + WorkspaceDir}
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(l) != "" {
			files = append(files, strings.TrimSpace(l))
		}
	}
	return files
}

// resumePrompt is §5.5's resume prompt, translated as written.
func resumePrompt(cp Checkpoint) string {
	files := dirtyFiles()
	list := "(없음)"
	if len(files) > 0 {
		list = strings.Join(files, ", ")
	}
	return fmt.Sprintf(
		"이전 턴이 인프라 사유(드레인/재배포)로 중단되었습니다. 중단 시각 %s.\n"+
			"워크스페이스의 현재 상태를 먼저 확인하고, 이미 완료된 부분을 반복하지 말고\n"+
			"이어서 진행하세요. 미커밋 변경: %s",
		cp.UpdatedAt.Format(time.RFC3339), list)
}

// writeAskpass materializes GIT_ASKPASS on the tmpfs (the root filesystem is
// read-only per §8.3, so /tmp is the only writable place that is NOT the
// workspace — and putting a credential-reading helper in the workspace would
// hand it to every future task as an editable file).
func writeAskpass() (string, error) {
	path := "/tmp/osiris-git-askpass.sh"
	script := "#!/bin/sh\n" +
		"# Generated by osiris-supervisor. Reads the file-injected, READ-ONLY\n" +
		"# scoped git PAT (§8.4: '에이전트별·리포별 fine-grained PAT. 읽기 전용\n" +
		"# 스코프'). Write access lives only in osiris-core (§8.2).\n" +
		"case \"$1\" in\n" +
		"  *[Uu]sername*) printf 'x-access-token' ;;\n" +
		"  *) cat " + filepath.Join(CredsDir, CredGitToken) + " 2>/dev/null ;;\n" +
		"esac\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return "", err
	}
	return path, nil
}

// --- stream-json routing --------------------------------------------------

type streamLine struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Result    string          `json:"result"`
	IsError   bool            `json:"is_error"`
	NumTurns  int             `json:"num_turns"`
	CostUSD   float64         `json:"total_cost_usd"`
	Usage     json.RawMessage `json:"usage"`
	Message   json.RawMessage `json:"message"`
}

// AckExtender is the tiny slice of *nats.Msg runTurn needs (satisfied by
// *nats.Msg's own variadic InProgress). Declared as an interface so the
// drain/checkpoint logic can be exercised without a broker, and so a resume
// turn — which has no message behind it — can pass nil.
type AckExtender interface {
	InProgress(opts ...nats.AckOpt) error
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
