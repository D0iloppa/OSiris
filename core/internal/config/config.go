// Package config reads osiris-core's runtime configuration from environment
// variables only (12-factor). No file-based config — this component has no
// rendered CLAUDE.md/settings.json/mcp.json (those belong to the agent
// runtime, §5.6 of 10-core-architecture.md), it only needs connection info.
package config

import (
	"fmt"
	"os"
)

type Config struct {
	// Neo4j bolt connection. Host name "neo4j" is one of the three hostnames
	// agents/components are allowed to know (§1.2 invariant asset #10).
	Neo4jURI      string
	Neo4jUser     string
	Neo4jPassword string

	// NATS connection. Host name "nats" — same invariant asset list.
	NatsURL string

	// HTTP listen addresses. Two separate ports on purpose:
	//   - HealthAddr:  /livez /readyz — must stay light (§5.4 principle applied
	//                  here too), never blocked by a write transaction.
	//   - APIAddr:     graph-write API (validated high-level operations only,
	//                  no arbitrary Cypher — A5/G7).
	HealthAddr string
	APIAddr    string

	// CatalogVersion is the system capability/permission catalog version
	// this build of osiris-core seeds and validates against (I9). In the
	// full design this must match the orchestrator's embedded grant-catalog
	// version; osiris-orchestrator does not exist yet in this build, so
	// osiris-core is the sole source of truth for the version string until
	// the orchestrator is implemented (see IMPLEMENTATION-NOTES.md).
	CatalogVersion string

	// OrgName seeds :Organization.name on first run (step 1 of §9.2). Not a
	// secret; safe to default.
	OrgName      string
	OrgLegalName string
	OrgTimezone  string

	// APIToken gates every /v1/* graph-write route (bearer token). This is a
	// deliberate simplification of the full §8.5 genesis-token model — see
	// IMPLEMENTATION-NOTES.md. Required; the server fails closed (503) on
	// every write route if unset, rather than silently allowing all callers.
	APIToken string

	// GenesisNamespace/GenesisServiceAccount name the ONE ServiceAccount
	// identity K7 accepts as the genesis principal
	// (docs/design/70-kubernetes-deployment.md §4.1:
	// "system:serviceaccount:osiris-agents:bootstrap-master"). Configurable
	// rather than hardcoded so a non-default namespace/SA name does not
	// require a rebuild, but the defaults ARE the design's named values —
	// this is not a value a caller supplies at request time (that would
	// defeat the whole point of pinning the expected identity).
	GenesisNamespace      string
	GenesisServiceAccount string
}

func mustEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func Load() (Config, error) {
	c := Config{
		Neo4jURI:              mustEnv("NEO4J_URI", "bolt://neo4j:7687"),
		Neo4jUser:             mustEnv("NEO4J_USER", "neo4j"),
		Neo4jPassword:         os.Getenv("NEO4J_PASSWORD"),
		NatsURL:               mustEnv("NATS_URL", "nats://nats:4222"),
		HealthAddr:            mustEnv("HEALTH_ADDR", ":8081"),
		APIAddr:               mustEnv("API_ADDR", ":8090"),
		CatalogVersion:        mustEnv("CATALOG_VERSION", "2026.08.1"),
		OrgName:               mustEnv("ORG_NAME", "OSiris"),
		OrgLegalName:          mustEnv("ORG_LEGAL_NAME", "OSiris"),
		OrgTimezone:           mustEnv("ORG_TIMEZONE", "Asia/Seoul"),
		APIToken:              os.Getenv("CORE_API_TOKEN"),
		GenesisNamespace:      mustEnv("GENESIS_NAMESPACE", "osiris-agents"),
		GenesisServiceAccount: mustEnv("GENESIS_SERVICE_ACCOUNT", "bootstrap-master"),
	}
	if c.Neo4jPassword == "" {
		return c, fmt.Errorf("NEO4J_PASSWORD is required")
	}
	return c, nil
}
