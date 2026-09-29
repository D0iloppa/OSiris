package graphdb

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// CapabilityRow is one (:AIAgent)-[:HAS_CAPABILITY]->(:Capability) edge,
// raw from the graph — no rendering/derivation happens in this package.
type CapabilityRow struct {
	Code  string
	Label string
	Level int
}

// DesiredAgent is the raw intent for one non-closed :AIAgent, exactly as
// stored in Neo4j (§20-ontology-schema.md §3.1/§3.2/§3.5). Turning this into
// a workload.AgentWorkloadSpec (rendering CLAUDE.md, resolving
// grant-catalog allowedTools, computing SpecHash/ConfigHash) is
// internal/reconcile's job, deliberately kept out of this package so
// "what does the graph say" and "what does that mean for a container" stay
// separable (mirrors §6.1's INTENT vs MATERIALIZATION split).
type DesiredAgent struct {
	EmployeeID    string
	Name          string
	RuntimeClass  string
	ModelAlias    string
	Image         string
	MaxTaskPoints int
	DrainSeconds  int
	DesiredState  string
	IsMaster      bool

	CPUMilli   int64
	MemMiB     int64
	StorageGiB int64

	TeamID   string
	TeamSlug string
	TeamName string

	RoleID               string
	RoleTitle            string
	RoleLevel            int
	RoleResponsibilities string
	PromptTemplateMd     string
	IsHead               bool

	// EffectiveGrants = (base ∪ additive) − denies, §7.1 effective(party)
	// simplified to the fields this build's AIAgent write path actually
	// populates (base profile via Role, additive/denies via optional
	// per-party edges the graph-write API doesn't currently expose a way to
	// write — see IMPLEMENTATION-NOTES.md). Queried anyway so the moment
	// core grows a way to write HAS_PROFILE{kind:'additive'}/DENIES, this
	// already picks it up with no query change.
	EffectiveGrants []string
	Capabilities    []CapabilityRow
}

// DeriveDesiredAgents reads every non-closed :AIAgent with a currently valid
// :Membership (I2's invariant guarantees at most one) and returns the raw
// facts the reconcile loop needs. An :AIAgent missing its
// :Membership/:Team/:Role/:RoleResourceProfile chain (any of the MATCHes
// below) is silently excluded from the result — that agent is mid-onboarding
// or malformed, and I2/I4 invariants (checked by core) are the place that
// catches it, not this query.
func (c *Client) DeriveDesiredAgents(ctx context.Context) ([]DesiredAgent, error) {
	session := c.read(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, `
			MATCH (a:AIAgent)
			WHERE a.closedAtRevision IS NULL
			MATCH (a)-[:USES_PROFILE]->(rp:RoleResourceProfile)
			MATCH (a)-[:HAS_MEMBERSHIP]->(m:Membership)-[:IN_TEAM]->(team:Team)
			WHERE m.validTo IS NULL
			MATCH (m)-[:AS_ROLE]->(role:Role)
			OPTIONAL MATCH (role)-[:HAS_PROFILE {kind: 'base'}]->(:PermissionProfile)-[:GRANTS]->(bgp:Permission)
			OPTIONAL MATCH (a)-[:HAS_PROFILE {kind: 'additive'}]->(:PermissionProfile)-[:GRANTS]->(agp:Permission)
			OPTIONAL MATCH (a)-[:DENIES]->(dp:Permission)
			OPTIONAL MATCH (a)-[hc:HAS_CAPABILITY]->(cap:Capability)
			WITH a, rp, team, m, role,
			     [x IN collect(DISTINCT bgp.code) WHERE x IS NOT NULL] AS baseGrants,
			     [x IN collect(DISTINCT agp.code) WHERE x IS NOT NULL] AS additiveGrants,
			     [x IN collect(DISTINCT dp.code) WHERE x IS NOT NULL] AS denies,
			     [x IN collect(DISTINCT {code: cap.code, label: cap.label, level: hc.level}) WHERE x.code IS NOT NULL] AS caps
			RETURN a.id AS employeeId, a.name AS name, a.runtimeClass AS runtimeClass,
			       a.modelAlias AS modelAlias, a.image AS image, a.maxTaskPoints AS maxTaskPoints,
			       a.drainSeconds AS drainSeconds, a.desiredState AS desiredState, a.isMaster AS isMaster,
			       rp.cpuMilli AS cpuMilli, rp.memMiB AS memMiB, rp.storageGiB AS storageGiB,
			       team.id AS teamId, team.slug AS teamSlug, team.name AS teamName,
			       role.id AS roleId, role.title AS roleTitle, role.level AS roleLevel,
			       role.responsibilitiesSummary AS roleSummary, role.promptTemplateMd AS promptTemplateMd,
			       m.isHead AS isHead,
			       baseGrants, additiveGrants, denies, caps
		`, nil)
		if err != nil {
			return nil, err
		}
		var out []DesiredAgent
		for result.Next(ctx) {
			rec := result.Record()
			out = append(out, recordToDesiredAgent(rec))
		}
		return out, result.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("derive desired agents: %w", err)
	}
	return res.([]DesiredAgent), nil
}

func recordToDesiredAgent(rec *neo4j.Record) DesiredAgent {
	get := func(k string) any { v, _ := rec.Get(k); return v }
	str := func(k string) string { s, _ := get(k).(string); return s }
	i64 := func(k string) int64 { n, _ := get(k).(int64); return n }
	i := func(k string) int { n, _ := get(k).(int64); return int(n) }
	b := func(k string) bool { v, _ := get(k).(bool); return v }

	base := toStringSlice(get("baseGrants"))
	additive := toStringSlice(get("additiveGrants"))
	denySet := map[string]bool{}
	for _, d := range toStringSlice(get("denies")) {
		denySet[d] = true
	}
	grantSet := map[string]bool{}
	var effective []string
	for _, g := range append(base, additive...) {
		if grantSet[g] || denySet[g] {
			continue
		}
		grantSet[g] = true
		effective = append(effective, g)
	}

	var caps []CapabilityRow
	if list, ok := get("caps").([]any); ok {
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			code, _ := m["code"].(string)
			label, _ := m["label"].(string)
			level, _ := m["level"].(int64)
			caps = append(caps, CapabilityRow{Code: code, Label: label, Level: int(level)})
		}
	}

	return DesiredAgent{
		EmployeeID: str("employeeId"), Name: str("name"), RuntimeClass: str("runtimeClass"),
		ModelAlias: str("modelAlias"), Image: str("image"), MaxTaskPoints: i("maxTaskPoints"),
		DrainSeconds: i("drainSeconds"), DesiredState: str("desiredState"), IsMaster: b("isMaster"),
		CPUMilli: i64("cpuMilli"), MemMiB: i64("memMiB"), StorageGiB: i64("storageGiB"),
		TeamID: str("teamId"), TeamSlug: str("teamSlug"), TeamName: str("teamName"),
		RoleID: str("roleId"), RoleTitle: str("roleTitle"), RoleLevel: i("roleLevel"),
		RoleResponsibilities: str("roleSummary"), PromptTemplateMd: str("promptTemplateMd"),
		IsHead: b("isHead"), EffectiveGrants: effective, Capabilities: caps,
	}
}

func toStringSlice(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// CheckCatalogVersion is orchestrator's independent half of I9
// (docs/design/20-ontology-schema.md §9.3): ":Permission.catalogVersion이
// 오케스트레이터 grant-catalog와 일치해야 한다." osiris-core checks the same
// invariant against its own CATALOG_VERSION env (internal/graphdb/
// invariants.go checkI9); this is the "actual cross-validation" the I1/I2
// IMPLEMENTATION-NOTES entry said would exist once this binary did.
// Mismatch is fail-closed: the reconcile loop must not run against a graph
// whose permission catalog it cannot trust.
func (c *Client) CheckCatalogVersion(ctx context.Context, expected string) (ok bool, mismatched []string, err error) {
	session := c.read(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, `
			MATCH (p:Permission)
			WHERE p.catalogVersion <> $expected OR p.catalogVersion IS NULL
			RETURN collect(p.code) AS codes
		`, map[string]any{"expected": expected})
		if err != nil {
			return nil, err
		}
		rec, err := result.Single(ctx)
		if err != nil {
			return nil, err
		}
		codes, _ := rec.Get("codes")
		return toStringSlice(codes), nil
	})
	if err != nil {
		return false, nil, fmt.Errorf("check catalog version: %w", err)
	}
	mismatched = res.([]string)
	return len(mismatched) == 0, mismatched, nil
}
