package supervisor

import "time"

// Container-internal paths. These are IDENTICAL in [1단계] Docker and [2단계]
// k8s by design (§5.1's table: named volume vs PVC both mount at /workspace;
// host 0700 dir vs projected Secret both land on
// /var/run/osiris/{creds,config}). The supervisor therefore never learns
// which runtime it is in — §5's opening line: "supervisor는 자신이 Pod 안인지
// Docker 컨테이너 안인지 알지 못하며, 알 필요도 없다."
const (
	WorkspaceDir = "/workspace"
	CredsDir     = "/var/run/osiris/creds"
	ConfigDir    = "/var/run/osiris/config"

	// OsirisDir and below are the supervisor's own state, on the workspace
	// volume — §5.6's "권위 위치: 워크스페이스 볼륨". The session JSONL that
	// --resume needs lives in the same volume (CLAUDE_CONFIG_DIR below), so
	// the id and the data it names are inseparable: losing the volume loses
	// both at once instead of leaving a zombie session id behind.
	OsirisDir      = WorkspaceDir + "/.osiris"
	StateFile      = OsirisDir + "/state.json"
	OutboxDir      = OsirisDir + "/outbox"
	TranscriptDir  = OsirisDir + "/transcripts"
	WriteProbeFile = OsirisDir + "/.writeprobe"

	// ClaudeConfigDir is exported to the child as CLAUDE_CONFIG_DIR (§5.6).
	ClaudeConfigDir = WorkspaceDir + "/.claude"

	// RepoDir is the --add-dir target from §5.2's command line. Created
	// empty at boot so the flag is always valid even before any task has
	// cloned anything into it.
	RepoDir = WorkspaceDir + "/repo"

	// HealthAddr is deliberately a DIFFERENT listener from GateAddr below.
	// Sharing one http.Server between the probes and the (potentially
	// 24-hour-blocking, §8.2) approval gate would make the gate's goroutine
	// count and the probes' latency share a fate for no reason. Same
	// separate-listener discipline osiris-core already uses for :8081 vs
	// :8090.
	HealthAddr = ":8081"
	GateAddr   = "127.0.0.1:8082"
)

// Credential file names (§8.4). The CONTRACT is the path, not the value —
// orchestrator writes, supervisor reads, and neither ever passes these
// through an environment variable of a long-lived process (G5: env is
// visible via /proc/<pid>/environ for the whole life of the process).
const (
	CredNATSUser        = "nats_user"
	CredNATSPassword    = "nats_password"
	CredCoreToken       = "core_token"
	CredGatewayToken    = "gateway_token"
	CredGatewayBaseURL  = "gateway_base_url"
	CredGatewayHealthPh = "gateway_health_path"
	CredGitToken        = "git_token"
)

// Rendered config file names (written by the driver from Neo4j-derived
// intent, §5.6's "파생 원본은 Neo4j → 드라이버가 파일로 렌더").
const (
	ConfigPrompt       = "CLAUDE.md"
	ConfigSettings     = "settings.json"
	ConfigMCP          = "mcp.json"
	ConfigAllowedTools = "allowed_tools" // newline-separated; §5.2's --allowedTools argument
)

// Timings. Every one of these is either quoted from the design or noted as
// this implementation's choice in IMPLEMENTATION-NOTES.md.
const (
	loopTickInterval    = 5 * time.Second  // §5.2's tick
	fetchMaxWait        = 30 * time.Second // §5.2's Fetch(1, MaxWait=30s) — long-poll, not polling
	heartbeatInterval   = 10 * time.Second // §7.1 heartbeat.agent.<id>
	inProgressInterval  = 25 * time.Second // §5.2's m.InProgress(every 25s) to extend ack_wait
	gatewayProbeEvery   = 60 * time.Second // §5.4 "omniroute /healthz 60초 주기"
	gatewayProbeTimeout = 5 * time.Second
	gatewayFailTolerate = 2                // §5.4 "연속 2회 실패까지 관용"
	volumeProbeEvery    = 60 * time.Second // §5.4 "/workspace 쓰기 가능 (60초 주기 touch)"
	credsWatchEvery     = 10 * time.Second // §5.2's watchCreds mtime watch
	reaperTickInterval  = 5 * time.Second  // §5.4's "childReaper goroutine 생존 (자체 tick으로 확인)"
	outboxFlushEvery    = 5 * time.Second
	childTermGrace      = 20 * time.Second // §5.5 step 5 — documented in the design itself as an assumption
	maxResumeAttempts   = 3                // §5.5 "attempt > 3 이면 재개하지 않고 태스크 blocked"
)
