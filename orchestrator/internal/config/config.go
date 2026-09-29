// Package config reads osiris-orchestrator's runtime configuration from
// environment variables (12-factor, same convention as osiris-core). No
// LLM-related config exists here at all (A4/D12) — this process never
// calls an LLM and the image never contains a claude binary or gateway
// credential (core/Dockerfile's FROM-scratch pattern is mirrored in
// orchestrator/Dockerfile for the same reason).
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Neo4jURI      string
	Neo4jUser     string
	Neo4jPassword string

	// NatsURL includes embedded userinfo (nats://osiris-orchestrator:<pw>@nats:4222)
	// so nats.go's Connect(url) authenticates without any separate creds
	// file — orchestrator is a trusted platform component (like osiris-core),
	// not an agent; G5's file-injection requirement is about AGENT
	// credentials specifically (see IMPLEMENTATION-NOTES.md).
	NatsURL string

	// NatsCorePassword/NatsOrchestratorPassword are rendered into
	// nats/generated/authorization.conf (internal/natsauth) so osiris-core's
	// own NATS connection (internal/events/nats.go) keeps working once
	// authentication is turned on. Must match whatever osiris-core's own
	// NATS_URL env embeds — see docker-compose.yml.
	NatsCorePassword         string
	NatsOrchestratorPassword string
	// NatsAPIPassword is osiris-api's static platform NATS credential (same
	// pattern as the two above — see internal/natsauth/natsauth.go's static
	// user block). Optional: an empty value means osiris-api is not deployed
	// in this build/host yet, and the rendered authorization.conf simply
	// omits that user (natsauth.Generator.render skips it when blank) rather
	// than failing the whole orchestrator over a component it does not
	// itself depend on.
	NatsAPIPassword string

	DockerSocketPath string
	HostProjectDir   string // host-absolute compose project root — see driver/docker's Mounts.Source comment
	StateDir         string // this container's own view of ./state (normally /state)

	NatsAuthConfPath  string // this container's own view of nats/generated/authorization.conf
	NatsContainerName string // exact container_name of the nats service, for the reload signal

	MaxResidentAgents int
	CatalogVersion    string

	// --- LLM gateway CREDENTIAL PLUMBING (not an LLM client) ---------------
	//
	// A4 is not weakened by these three fields, and the distinction matters
	// enough to spell out: §9.1 says "LLM 호출 자체는 어댑터를 통하지 않는다 —
	// claude CLI가 ANTHROPIC_BASE_URL로 직접 친다. 어댑터는 자격증명 수명주기와
	// 정산 대조만 담당한다." This process still contains no `claude` binary, no
	// LLM client library and no code path that can produce a completion; it only
	// WRITES a token into a file that an agent container will read. That is the
	// same relationship it already has with NATS passwords.
	//
	// GatewayToken is a static, operator-supplied token in this build. §8.4
	// specifies per-agent, 24h, issued by core's omniroute adapter
	// (IssueToken()) and refreshed by the driver every 6h — core has no such
	// adapter yet, so the ISSUANCE half is a documented gap. The DELIVERY half
	// (which was the actual hole: nothing pushed rotated credentials at a
	// running container) is implemented and exercised — see
	// WorkloadDriver.SyncCredentials.
	GatewayBaseURL    string
	GatewayToken      string
	GatewayHealthPath string

	// AgentExtraNoProxy is appended to each agent's NO_PROXY.
	//
	// A3 says ALL external egress goes through osiris-egress, and that stays
	// true: this list can only ever add INTERNAL hostnames, because anything
	// outside the compose networks has no route at all without the proxy (the
	// agent network is `internal: true`). Its real purpose is a gateway that
	// lives inside the cluster rather than on dev-net — which is exactly the
	// case for the verification mock gateway (test/mock-gateway), and would also
	// be the case for a self-hosted gateway in a [2단계] deployment. Empty by
	// default, so the production path is unchanged.
	AgentExtraNoProxy string

	// AgentImage is only a fallback for :AIAgent.image being empty; the graph
	// remains authoritative (G4: no agent/org information in compose).
	AgentImage string

	// --- osiris-core, for the genesis token issuance of 10 §8.5 ------------
	//
	// HI11: NEITHER OF THESE IS A NEW .env VARIABLE. CORE_API_TOKEN already
	// exists and is already required by osiris-core and osiris-api; CORE_BASE_URL
	// has an in-compose default exactly as it does for osiris-api. This service
	// simply had no reason to read them until §8.5 steps 4-5 gave it one.
	//
	// WHY THE PLATFORM TOKEN IS THE RIGHT CREDENTIAL HERE, per §8.5: "이 요청도
	// orchestrator가 core에게 보내는 플랫폼 내부 호출이라는 점에서 기존
	// graph-write 호출과 성격이 같고, 새 신뢰 경계를 만들지 않는다." What makes it
	// meaningful is not the token's strength but WHO can hold it and what only
	// they can do: only this service has docker.sock (G8), so only it can
	// truthfully claim the id of a container it just created.
	CoreBaseURL  string
	CoreAPIToken string

	// BootstrapMasterImage is the image for the genesis container. Defaults to
	// AgentImage because 30 §1.3 says bootstrap-master uses "Master와 동일한
	// 에이전트 런타임 이미지" — a separate image would be a second runtime to keep
	// in step with the supervisor contract for no gain.
	BootstrapMasterImage string

	HealthAddr        string
	ReconcileInterval time.Duration

	// --- [2단계] driver/operator selection (70 §5, Phase D-1) --------------
	//
	// WorkloadDriver picks which workload.WorkloadDriver main.go wires into
	// reconcile.NewLoop: "docker" (default — compose path, UNCHANGED) or
	// "kube" (driver/kube's Agent-CR-based driver). Env-switched rather than
	// inferred (e.g. "does /var/run/docker.sock exist") because inference
	// would make the choice depend on incidental host state instead of an
	// explicit deployment decision — the same reasoning config.go already
	// applies to every other env-driven choice in this file.
	//
	// Defaulting to "docker" is the load-bearing part: docker-compose.yml is
	// still a valid deployment path (K2 has not been executed as an actual
	// deletion of that file), so a host with no WORKLOAD_DRIVER env set at
	// all — i.e. every existing compose deployment — keeps behaving exactly
	// as before this phase. deploy/osiris/templates/orchestrator-
	// deployment.yaml is the one place that sets WORKLOAD_DRIVER=kube.
	WorkloadDriver string

	// AgentsNamespace/SystemNamespace are only consulted when
	// WorkloadDriver=="kube": AgentsNamespace is where driver/kube and
	// internal/operator both watch/write (K10 — Agent CRs and everything
	// they own live together); SystemNamespace is where this orchestrator
	// process itself runs, needed to build the cross-namespace Service
	// FQDNs an agent Pod's env requires (see internal/operator/objects.go's
	// natsFQDN/coreFQDN/egressFQDN doc comment).
	AgentsNamespace string
	SystemNamespace string
}

func mustEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func Load() (Config, error) {
	c := Config{
		Neo4jURI:                 mustEnv("NEO4J_URI", "bolt://neo4j:7687"),
		Neo4jUser:                mustEnv("NEO4J_USER", "neo4j"),
		Neo4jPassword:            os.Getenv("NEO4J_PASSWORD"),
		NatsURL:                  os.Getenv("NATS_URL"),
		NatsCorePassword:         os.Getenv("NATS_CORE_PASSWORD"),
		NatsOrchestratorPassword: os.Getenv("NATS_ORCHESTRATOR_PASSWORD"),
		NatsAPIPassword:          os.Getenv("NATS_API_PASSWORD"),
		DockerSocketPath:         mustEnv("DOCKER_SOCKET_PATH", "/var/run/docker.sock"),
		HostProjectDir:           os.Getenv("HOST_PROJECT_DIR"),
		StateDir:                 mustEnv("STATE_DIR", "/state"),
		NatsAuthConfPath:         mustEnv("NATS_AUTH_CONF_PATH", "/etc/nats-dynamic/authorization.conf"),
		NatsContainerName:        mustEnv("NATS_CONTAINER_NAME", "osiris-nats"),
		CatalogVersion:           mustEnv("CATALOG_VERSION", "2026.08.1"),
		HealthAddr:               mustEnv("HEALTH_ADDR", ":8081"),
		GatewayBaseURL:           mustEnv("OSIRIS_GATEWAY_BASE_URL", "http://omniroute:20128"),
		GatewayToken:             os.Getenv("OSIRIS_GATEWAY_TOKEN"),
		// §9.2 item 4 says "GET /healthz 무인증, 100ms 이내"; that stays the code
		// default. The deployed omniroute on this host does not implement it
		// (verified: /healthz -> 404, /api/health -> 401), so the deviation is
		// expressed in configuration where it belongs — see .env.example and
		// IMPLEMENTATION-NOTES.md.
		GatewayHealthPath: mustEnv("OSIRIS_GATEWAY_HEALTH_PATH", "/healthz"),
		AgentImage:        mustEnv("OSIRIS_AGENT_IMAGE", "osiris/agent:dev"),
		CoreBaseURL:       mustEnv("CORE_BASE_URL", "http://osiris-core:8090"),
		CoreAPIToken:      os.Getenv("CORE_API_TOKEN"),
		BootstrapMasterImage: mustEnv("OSIRIS_BOOTSTRAP_MASTER_IMAGE",
			mustEnv("OSIRIS_AGENT_IMAGE", "osiris/agent:dev")),
		AgentExtraNoProxy: os.Getenv("OSIRIS_AGENT_EXTRA_NO_PROXY"),
		WorkloadDriver:    mustEnv("WORKLOAD_DRIVER", "docker"),
		AgentsNamespace:   mustEnv("AGENTS_NAMESPACE", "osiris-agents"),
		SystemNamespace:   mustEnv("SYSTEM_NAMESPACE", "osiris-system"),
	}

	maxAgents := mustEnv("OSIRIS_MAX_RESIDENT_AGENTS", "4")
	n, err := strconv.Atoi(maxAgents)
	if err != nil || n <= 0 {
		return c, fmt.Errorf("OSIRIS_MAX_RESIDENT_AGENTS must be a positive integer, got %q", maxAgents)
	}
	c.MaxResidentAgents = n

	interval := mustEnv("RECONCILE_INTERVAL_SECONDS", "15")
	secs, err := strconv.Atoi(interval)
	if err != nil || secs <= 0 {
		return c, fmt.Errorf("RECONCILE_INTERVAL_SECONDS must be a positive integer, got %q", interval)
	}
	c.ReconcileInterval = time.Duration(secs) * time.Second

	if c.Neo4jPassword == "" {
		return c, fmt.Errorf("NEO4J_PASSWORD is required")
	}
	if c.NatsURL == "" {
		return c, fmt.Errorf("NATS_URL is required (must embed osiris-orchestrator's credentials)")
	}
	if c.NatsCorePassword == "" || c.NatsOrchestratorPassword == "" {
		return c, fmt.Errorf("NATS_CORE_PASSWORD and NATS_ORCHESTRATOR_PASSWORD are required")
	}
	if c.HostProjectDir == "" {
		return c, fmt.Errorf("HOST_PROJECT_DIR is required (host-absolute path to the compose project root, for docker-out-of-docker bind mounts)")
	}
	return c, nil
}
