// Package config reads osiris-api's runtime configuration from environment
// variables only (12-factor), mirroring osiris-core's internal/config
// package style (docs/design/10-core-architecture.md §3.2: osiris-api has no
// rendered CLAUDE.md/settings.json/mcp.json of its own — those belong to the
// agent runtime).
package config

import (
	"fmt"
	"os"
)

type Config struct {
	// HTTP listen addresses. Two separate listeners on purpose, same §5.4
	// discipline osiris-core already applies: HealthAddr must never be
	// blocked by a slow onboarding/SSE request.
	HealthAddr string
	APIAddr    string

	// PublicOrigin is what osiris-api considers its own public origin for
	// Host-header validation and cookie scoping (§2.4's "osiris-api는 Host
	// 헤더가 <origin>이 아니면 거부" — see internal/api/server.go's hostGuard
	// for where this is actually enforced and why it is deliberately lenient
	// on this build's default loopback deployment).
	PublicOrigin string

	// TrustedProxyCIDR is the network osiris-gw itself is on (osiris-edge) —
	// §2.8's "TRUSTED_PROXY_CIDR(= osiris-edge 대역)". Not enforced by IP
	// inspection in this build (no direct socket peer IP access from
	// net/http in a way that's worth the complexity at this scale — 이
	// 값은 감사 로그 표기에만 쓰인다 지금은), kept as a config field because
	// the design names it explicitly and a future worker may want it for
	// real request validation.
	TrustedProxyCIDR string

	// --- Neo4j: READ-ONLY access only (D1/G7/A5) ---------------------------
	// osiris-api never writes to Neo4j. It opens read-only sessions (see
	// internal/graphread) to answer GET /onboarding/status's `org` section
	// (§12.1 REST table: "GET /orgs/tree ✅ | Neo4j" — direct reads by
	// osiris-api are the design's explicitly allowed exception to
	// "graph-write API 단일 관문", which only ever governed WRITES).
	Neo4jURI      string
	Neo4jUser     string
	Neo4jPassword string

	// --- NATS: SSE resume (D19) ---------------------------------------------
	NatsURL string

	// --- osiris-core: NOT used by this build --------------------------------
	// GET /onboarding/status's `org` section is answered by a direct
	// read-only Neo4j query (internal/graphread) — :OrgRevision and team/role
	// counts are readable straight from the graph without needing core's
	// HTTP surface at all, and this build implements no write-requiring
	// console route (assignment engine / org-seed / WBS are explicitly out
	// of scope — see IMPLEMENTATION-NOTES.md). These fields are kept, unused,
	// as the documented landing spot for the future worker who DOES add a
	// write-requiring console route (§12.1's PATCH /wbs/{taskId} etc.) — that
	// worker's core token must stay narrowly scoped (§9.6: "osiris-api는
	// JetStream 관리 권한이 없어야 한다" — the same minimalism applies here).
	CoreBaseURL string
	CoreToken   string

	// --- omniroute onboarding step0 (§1.0 of 30-onboarding-provisioning.md) ---
	// operations.mjs itself resolves OSIRIS_OMNIROUTE_BASE_URL /
	// OSIRIS_OMNIROUTE_ADMIN_PASSWORD / OSIRIS_OMNIROUTE_MANAGE_KEY from its
	// own environment (see omniroute/operations.mjs resolveOpts) — osiris-api
	// does not re-parse or duplicate those values, it only needs to know
	// where the operations script lives and which node binary runs it, and
	// forwards its OWN process environment to the child (see
	// internal/omniroute/ops.go).
	OperationsScriptPath string
	NodeBinPath          string
	EnvFilePath          string // .env, so applyGatewayTokenToEnv/applyManageKeyToEnv can update it

	// --- step0 verify-gateway inputs (30-onboarding-provisioning.md §1.0.3
	// step 0-5) — same two variables the orchestrator/agent already read
	// (docker-compose.yml's osiris-orchestrator service, .env.example), read
	// here too so osiris-api can independently probe (a) without needing any
	// new env var a human has to duplicate.
	GatewayBaseURL      string
	GatewayHealthPath   string
	OrchestratorBaseURL string

	// omniroute 관리 UI를 콘솔이 어디서 열지(GET /api/omniroute/console). embedded는
	// osiris-omniroute가 /omni 서브패스로 같은 오리진에 있고, external은 이미 운용 중인
	// 인스턴스 자신의 주소다(배포가 모드에 맞춰 넣는다 — 차트 values `omniroute`).
	OmnirouteMode  string
	OmnirouteUIURL string

	// --- master key (60-identity-authn.md §4.0, H22) ------------------------
	// The initial administrator's first-login credential. NOT the §8.5 genesis
	// token (that one is core-scoped, bootstrap-master only, and closes forever
	// once an org exists — W1); the two have different issuers, stores,
	// lifetimes and closure conditions, and §75 already recorded that
	// distinction in code comments.
	//
	// This value dies when the first administrator sets a password (H23/§4.5).
	// internal/masterkey owns the file; internal/orgstate owns the predicate.
	MasterKeyPath string

	// LegacySetupTokenPath is the pre-H22 file name, read ONLY by
	// masterkey.Boot's migration branch (§4.0's migration row) and never
	// accepted as a credential — the value in it has no `osk_` prefix, so the
	// prefix-routing guard cannot select a branch for it at all.
	//
	// It is a config field rather than a constant so that a deployment which
	// moved state/ elsewhere migrates from the right place instead of silently
	// leaving a live credential behind.
	LegacySetupTokenPath string
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func Load() (Config, error) {
	c := Config{
		HealthAddr:           env("HEALTH_ADDR", ":8081"),
		APIAddr:              env("API_ADDR", ":8443"),
		PublicOrigin:         env("PUBLIC_ORIGIN", "http://localhost:18280"),
		TrustedProxyCIDR:     env("TRUSTED_PROXY_CIDR", "172.31.238.0/24"),
		Neo4jURI:             env("NEO4J_URI", "bolt://neo4j:7687"),
		Neo4jUser:            env("NEO4J_USER", "neo4j"),
		Neo4jPassword:        os.Getenv("NEO4J_PASSWORD"),
		NatsURL:              env("NATS_URL", "nats://nats:4222"),
		CoreBaseURL:          env("CORE_BASE_URL", "http://osiris-core:8090"),
		CoreToken:            os.Getenv("CORE_API_TOKEN"), // unused by this build, see doc comment above
		GatewayBaseURL:       env("OSIRIS_GATEWAY_BASE_URL", "http://osiris-omniroute:20128/omni"),
		GatewayHealthPath:    env("OSIRIS_GATEWAY_HEALTH_PATH", "/v1/models"),
		OmnirouteMode:        env("OSIRIS_OMNIROUTE_MODE", "embedded"),
		OmnirouteUIURL:       env("OSIRIS_OMNIROUTE_UI_URL", "/omni/"),
		OrchestratorBaseURL:  env("ORCHESTRATOR_BASE_URL", "http://osiris-orchestrator:8081"),
		OperationsScriptPath: env("OMNIROUTE_OPERATIONS_SCRIPT", "/app/omniroute/operations.mjs"),
		NodeBinPath:          env("NODE_BIN", "node"),
		EnvFilePath:          env("ENV_FILE_PATH", "/repo-env/.env"),
		MasterKeyPath:        env("MASTER_KEY_PATH", "/state/creds/admin/master_key"),
		LegacySetupTokenPath: env("ADMIN_SETUP_TOKEN_PATH", "/state/creds/admin/setup_token"),
	}
	if c.Neo4jPassword == "" {
		return c, fmt.Errorf("NEO4J_PASSWORD is required")
	}
	return c, nil
}
