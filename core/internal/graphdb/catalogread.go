package graphdb

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ===========================================================================
// Catalog reads — the "카탈로그 읽기 전용" half of §8.5's genesis scope, and the
// input to the draft validation of 30-onboarding-provisioning.md §3.3.
//
// WHY THIS IS A READ AND NOT A WRITE, SPELLED OUT: 30 §3.3 requires the draft
// endpoint to verify that every code a draft references EXISTS in the catalog,
// and O3 states the reason — "LLM이 만드는 것은 역량/권한 '선택'이지 '발명'이
// 아니다". The consultant therefore needs to SEE the catalog, and nothing more:
// the catalog is system-owned and, per 20-ontology-schema.md §9.2 step 3,
// "부트스트랩이 코드에서 upsert하며 사람이 편집하지 않는다".
//
// One query per label rather than one big join: these run at conversation speed
// (a handful of times per onboarding), the sets are small, and separate queries
// keep each result independently legible when one of them is empty — which is
// itself a diagnosis (an unseeded catalog).
// ===========================================================================

// Catalog is the read-only snapshot the consultant may see and the draft
// validator checks against.
type Catalog struct {
	CatalogVersion     string   `json:"catalogVersion"`
	Capabilities       []string `json:"capabilities"`
	Permissions        []string `json:"permissions"`
	PermissionProfiles []string `json:"permissionProfiles"`
	ResourceProfiles   []string `json:"resourceProfiles"`

	// ModelAliases is I10's fixed triple. It is NOT read from the graph: I10
	// pins the allowed set in code (writeops.go's allowedModelAliases) and
	// omniroute/config.json's comment calls the same three names "고정, 협상
	// 대상 아님". Reporting them here from the same constant the write path
	// enforces is what keeps the consultant's options and the validator's rule
	// the same list.
	ModelAliases []string `json:"modelAliases"`
}

// ReadCatalog returns the catalog snapshot. Read-only session (D1/G7: core is
// the single graph gateway, and this is core reading its own store).
func (c *Client) ReadCatalog(ctx context.Context, catalogVersion string) (Catalog, error) {
	sess := c.NewReadSession(ctx)
	defer sess.Close(ctx)

	out := Catalog{CatalogVersion: catalogVersion, ModelAliases: AllowedModelAliases()}

	type q struct {
		cypher string
		dst    *[]string
	}
	for _, item := range []q{
		{"MATCH (n:Capability) RETURN n.code AS v ORDER BY v", &out.Capabilities},
		{"MATCH (n:Permission) RETURN n.code AS v ORDER BY v", &out.Permissions},
		{"MATCH (n:PermissionProfile) RETURN n.id AS v ORDER BY v", &out.PermissionProfiles},
		{"MATCH (n:RoleResourceProfile) RETURN n.id AS v ORDER BY v", &out.ResourceProfiles},
	} {
		vals, err := readStrings(ctx, sess, item.cypher)
		if err != nil {
			return Catalog{}, err
		}
		*item.dst = vals
	}
	return out, nil
}

func readStrings(ctx context.Context, sess neo4j.SessionWithContext, cypher string) ([]string, error) {
	res, err := sess.Run(ctx, cypher, nil)
	if err != nil {
		return nil, fmt.Errorf("catalog read failed: %w", err)
	}
	out := []string{}
	for res.Next(ctx) {
		if v, ok := res.Record().Get("v"); ok {
			if s, ok := v.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	if err := res.Err(); err != nil {
		return nil, fmt.Errorf("catalog read failed: %w", err)
	}
	return out, nil
}

// OrgSummary is the small, non-sensitive picture the genesis status route
// returns: enough for a consultant to say "지금 이렇게 되어 있습니다", nothing
// that amounts to reading the organization's contents.
type OrgSummary struct {
	CurrentOrgRevision int64 `json:"currentOrgRevision"`
	Teams              int64 `json:"teams"`
	Roles              int64 `json:"roles"`
	People             int64 `json:"people"`
	Agents             int64 `json:"agents"`
}

// ReadOrgSummary counts the nodes the onboarding status contract needs
// (30 §1.0.8's `org` section). Counts only — no names, no emails, no ids.
func (c *Client) ReadOrgSummary(ctx context.Context) (OrgSummary, error) {
	sess := c.NewReadSession(ctx)
	defer sess.Close(ctx)

	var out OrgSummary
	res, err := sess.Run(ctx, `
		OPTIONAL MATCH (r:OrgRevision)
		WITH coalesce(max(r.n), 0) AS rev
		OPTIONAL MATCH (t:Team)          WHERE t.closedAtRevision IS NULL
		WITH rev, count(DISTINCT t) AS teams
		OPTIONAL MATCH (ro:Role)         WHERE ro.closedAtRevision IS NULL
		WITH rev, teams, count(DISTINCT ro) AS roles
		OPTIONAL MATCH (p:Person)        WHERE p.closedAtRevision IS NULL
		WITH rev, teams, roles, count(DISTINCT p) AS people
		OPTIONAL MATCH (a:AIAgent)       WHERE a.closedAtRevision IS NULL
		RETURN rev, teams, roles, people, count(DISTINCT a) AS agents
	`, nil)
	if err != nil {
		return out, fmt.Errorf("org summary read failed: %w", err)
	}
	if res.Next(ctx) {
		rec := res.Record()
		out.CurrentOrgRevision = int64Of(rec, "rev")
		out.Teams = int64Of(rec, "teams")
		out.Roles = int64Of(rec, "roles")
		out.People = int64Of(rec, "people")
		out.Agents = int64Of(rec, "agents")
	}
	if err := res.Err(); err != nil {
		return out, fmt.Errorf("org summary read failed: %w", err)
	}
	return out, nil
}

func int64Of(rec *neo4j.Record, key string) int64 {
	if v, ok := rec.Get(key); ok {
		if n, ok := v.(int64); ok {
			return n
		}
	}
	return 0
}
