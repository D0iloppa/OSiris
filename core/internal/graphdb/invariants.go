// Invariant checker — docs/design/20-ontology-schema.md §9.3 (I1-I12).
//
// Everything here is read-only (neo4j.AccessModeRead sessions). This package
// never mutates the graph in response to a check; it only reports. §9.2 step
// 13 calls this same set of queries during onboarding's rehearsal, but the
// checker itself has no idea it's being used that way — it just answers
// "what's wrong with the graph right now."
package graphdb

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

type Violation struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Subjects []string `json:"subjects,omitempty"`
}

type InvariantReport struct {
	Violations []Violation `json:"violations"`
	ErrorCount int         `json:"errorCount"`
	WarnCount  int         `json:"warningCount"`
	OK         bool        `json:"ok"` // true iff ErrorCount == 0 (warnings do not block)
}

// CheckInvariants runs I1-I12 against the current graph state and returns a
// report. It never modifies anything.
func CheckInvariants(ctx context.Context, c *Client, catalogVersion string) (InvariantReport, error) {
	session := c.NewReadSession(ctx)
	defer session.Close(ctx)

	report := InvariantReport{}

	checks := []func(context.Context, neo4j.SessionWithContext, string) ([]Violation, error){
		checkI1_TeamExactlyOneHead,
		checkI2_AIAgentExactlyOneMembership,
		checkI3_ExactlyOneMaster,
		checkI4_RoleRequiredCapsInCatalog,
		checkI5_PhysicalCapabilityPersonOnly,
		checkI6_CapabilityGapWarning,
		checkI7_InertCapabilityWarning,
		checkI8_CapabilityBroaderAcyclic,
		checkI9_PermissionCatalogVersion,
		checkI10_ModelAliasAllowed,
		checkI11_PhysicalRoleNeedsHuman,
		checkI12_NoHybridLeafWorkItem,
		// I13 is deliberately absent — 30-onboarding-provisioning.md §6.3
		// already uses that number for an onboarding-only warning, so
		// 20-ontology-schema.md §9.3 skips it (60 §17 F5's resolution).
		checkI14_AtLeastOneOrgAdmin,
		checkI15_OrgMembershipPersonOnly,
		checkI16_AtMostOneValidOrgMembership,
		checkI17_OrgRoleShape,
	}

	for _, fn := range checks {
		vs, err := fn(ctx, session, catalogVersion)
		if err != nil {
			return report, fmt.Errorf("invariant check failed: %w", err)
		}
		report.Violations = append(report.Violations, vs...)
	}

	for _, v := range report.Violations {
		if v.Severity == SeverityError {
			report.ErrorCount++
		} else {
			report.WarnCount++
		}
	}
	report.OK = report.ErrorCount == 0
	return report, nil
}

func collectStrings(ctx context.Context, res neo4j.ResultWithContext, key string) []string {
	var out []string
	for res.Next(ctx) {
		v, ok := res.Record().Get(key)
		if !ok || v == nil {
			continue
		}
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// I1: every :Team has exactly one isHead:true, currently-valid :Membership.
func checkI1_TeamExactlyOneHead(ctx context.Context, s neo4j.SessionWithContext, _ string) ([]Violation, error) {
	res, err := s.Run(ctx, `
		MATCH (t:Team)
		OPTIONAL MATCH (t)<-[:IN_TEAM]-(m:Membership {isHead: true})
		  WHERE m.validTo IS NULL
		WITH t, count(m) AS heads
		WHERE heads <> 1
		RETURN t.id AS id, heads AS heads
	`, nil)
	if err != nil {
		return nil, err
	}
	var out []Violation
	for res.Next(ctx) {
		rec := res.Record()
		id, _ := rec.Get("id")
		heads, _ := rec.Get("heads")
		out = append(out, Violation{
			Code: "I1", Severity: SeverityError,
			Message:  fmt.Sprintf("team %v has %v head memberships (expected exactly 1)", id, heads),
			Subjects: []string{fmt.Sprintf("%v", id)},
		})
	}
	return out, res.Err()
}

// I2: every :AIAgent has exactly one currently-valid :Membership.
func checkI2_AIAgentExactlyOneMembership(ctx context.Context, s neo4j.SessionWithContext, _ string) ([]Violation, error) {
	res, err := s.Run(ctx, `
		MATCH (a:AIAgent)
		WHERE a.closedAtRevision IS NULL
		OPTIONAL MATCH (a)-[:HAS_MEMBERSHIP]->(m:Membership)
		  WHERE m.validTo IS NULL
		WITH a, count(m) AS memberships
		WHERE memberships <> 1
		RETURN a.id AS id, memberships AS memberships
	`, nil)
	if err != nil {
		return nil, err
	}
	var out []Violation
	for res.Next(ctx) {
		rec := res.Record()
		id, _ := rec.Get("id")
		n, _ := rec.Get("memberships")
		out = append(out, Violation{
			Code: "I2", Severity: SeverityError,
			Message:  fmt.Sprintf("AIAgent %v has %v valid memberships (expected exactly 1)", id, n),
			Subjects: []string{fmt.Sprintf("%v", id)},
		})
	}
	return out, res.Err()
}

// I3: exactly one :AIAgent{isMaster:true}.
func checkI3_ExactlyOneMaster(ctx context.Context, s neo4j.SessionWithContext, _ string) ([]Violation, error) {
	res, err := s.Run(ctx, `
		MATCH (a:AIAgent {isMaster: true})
		WHERE a.closedAtRevision IS NULL
		RETURN collect(a.id) AS ids
	`, nil)
	if err != nil {
		return nil, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return nil, err
	}
	idsRaw, _ := rec.Get("ids")
	ids, _ := idsRaw.([]any)
	if len(ids) == 1 {
		return nil, nil
	}
	var subs []string
	for _, id := range ids {
		subs = append(subs, fmt.Sprintf("%v", id))
	}
	return []Violation{{
		Code: "I3", Severity: SeverityError,
		Message:  fmt.Sprintf("expected exactly 1 AIAgent with isMaster:true, found %d", len(ids)),
		Subjects: subs,
	}}, nil
}

// I4: every :Role's REQUIRES_CAPABILITY target exists in the current catalog.
func checkI4_RoleRequiredCapsInCatalog(ctx context.Context, s neo4j.SessionWithContext, catalogVersion string) ([]Violation, error) {
	res, err := s.Run(ctx, `
		MATCH (r:Role)-[:REQUIRES_CAPABILITY]->(c)
		WHERE NOT c:Capability OR c.catalogVersion <> $catalogVersion OR c.catalogVersion IS NULL
		RETURN r.id AS roleId, c.code AS code
	`, map[string]any{"catalogVersion": catalogVersion})
	if err != nil {
		return nil, err
	}
	var out []Violation
	for res.Next(ctx) {
		rec := res.Record()
		roleID, _ := rec.Get("roleId")
		code, _ := rec.Get("code")
		out = append(out, Violation{
			Code: "I4", Severity: SeverityError,
			Message:  fmt.Sprintf("role %v requires capability %v which is not in catalog version %s", roleID, code, catalogVersion),
			Subjects: []string{fmt.Sprintf("%v", roleID), fmt.Sprintf("%v", code)},
		})
	}
	return out, res.Err()
}

// I5: cap.exec.physical holders must all be :Person (hard constraint, C2).
func checkI5_PhysicalCapabilityPersonOnly(ctx context.Context, s neo4j.SessionWithContext, _ string) ([]Violation, error) {
	res, err := s.Run(ctx, `
		MATCH (p:Party)-[:HAS_CAPABILITY]->(c:Capability {code: "cap.exec.physical"})
		WHERE NOT p:Person
		RETURN p.id AS id
	`, nil)
	if err != nil {
		return nil, err
	}
	var out []Violation
	for res.Next(ctx) {
		id, _ := res.Record().Get("id")
		out = append(out, Violation{
			Code: "I5", Severity: SeverityError,
			Message:  fmt.Sprintf("party %v holds cap.exec.physical but is not a :Person", id),
			Subjects: []string{fmt.Sprintf("%v", id)},
		})
	}
	return out, res.Err()
}

// I6 (warning): every :Role's required capabilities have at least one
// level>=minLevel holder somewhere in the org.
func checkI6_CapabilityGapWarning(ctx context.Context, s neo4j.SessionWithContext, _ string) ([]Violation, error) {
	res, err := s.Run(ctx, `
		MATCH (r:Role)-[req:REQUIRES_CAPABILITY]->(c:Capability)
		OPTIONAL MATCH (p:Party)-[h:HAS_CAPABILITY]->(hc:Capability)-[:BROADER*0..3]->(c)
		  WHERE h.level >= req.minLevel AND p.closedAtRevision IS NULL
		WITH r, c, req, count(DISTINCT p) AS holders
		WHERE holders = 0
		RETURN r.id AS roleId, c.code AS code
	`, nil)
	if err != nil {
		return nil, err
	}
	var out []Violation
	for res.Next(ctx) {
		rec := res.Record()
		roleID, _ := rec.Get("roleId")
		code, _ := rec.Get("code")
		out = append(out, Violation{
			Code: "I6", Severity: SeverityWarning,
			Message:  fmt.Sprintf("CAPABILITY_GAP: role %v requires %v but nobody in the org holds it at the required level", roleID, code),
			Subjects: []string{fmt.Sprintf("%v", roleID), fmt.Sprintf("%v", code)},
		})
	}
	return out, res.Err()
}

// I7 (warning): every :AIAgent's effective permissions must cover the
// REQUIRES_PERMISSION set of the capabilities it holds (§7.1 effective(party),
// simplified — see IMPLEMENTATION-NOTES.md for what's simplified and why).
func checkI7_InertCapabilityWarning(ctx context.Context, s neo4j.SessionWithContext, _ string) ([]Violation, error) {
	res, err := s.Run(ctx, `
		MATCH (a:AIAgent)
		WHERE a.closedAtRevision IS NULL
		OPTIONAL MATCH (a)-[:HAS_CAPABILITY]->(cap:Capability)-[:REQUIRES_PERMISSION]->(perm:Permission)
		WITH a, collect(DISTINCT perm.code) AS neededPerms
		WHERE size(neededPerms) > 0
		OPTIONAL MATCH (a)-[:HAS_MEMBERSHIP]->(m:Membership)-[:AS_ROLE]->(role:Role)
		            -[:HAS_PROFILE {kind: 'base'}]->(bp:PermissionProfile)-[:GRANTS]->(gp:Permission)
		  WHERE m.validTo IS NULL
		OPTIONAL MATCH (a)-[:HAS_PROFILE {kind: 'additive'}]->(ap:PermissionProfile)-[:GRANTS]->(gp2:Permission)
		WITH a, neededPerms,
		     [x IN collect(DISTINCT gp.code) WHERE x IS NOT NULL] +
		     [x IN collect(DISTINCT gp2.code) WHERE x IS NOT NULL] AS granted
		OPTIONAL MATCH (a)-[:DENIES]->(denyPerm:Permission)
		WITH a, neededPerms, granted,
		     [x IN collect(DISTINCT denyPerm.code) WHERE x IS NOT NULL] AS denied
		WITH a, [p IN neededPerms WHERE NOT p IN granted OR p IN denied] AS missing
		WHERE size(missing) > 0
		RETURN a.id AS id, missing AS missing
	`, nil)
	if err != nil {
		return nil, err
	}
	var out []Violation
	for res.Next(ctx) {
		rec := res.Record()
		id, _ := rec.Get("id")
		missingRaw, _ := rec.Get("missing")
		missing, _ := missingRaw.([]any)
		var missingStrs []string
		for _, m := range missing {
			missingStrs = append(missingStrs, fmt.Sprintf("%v", m))
		}
		out = append(out, Violation{
			Code: "I7", Severity: SeverityWarning,
			Message:  fmt.Sprintf("INERT_CAPABILITY: agent %v holds capabilities requiring permissions it doesn't effectively have: %v", id, missingStrs),
			Subjects: append([]string{fmt.Sprintf("%v", id)}, missingStrs...),
		})
	}
	return out, res.Err()
}

// I8: no cycles in the :Capability BROADER graph.
func checkI8_CapabilityBroaderAcyclic(ctx context.Context, s neo4j.SessionWithContext, _ string) ([]Violation, error) {
	res, err := s.Run(ctx, `
		MATCH (c:Capability)-[:BROADER*1..30]->(c)
		RETURN DISTINCT c.code AS code
		LIMIT 20
	`, nil)
	if err != nil {
		return nil, err
	}
	var out []Violation
	for res.Next(ctx) {
		code, _ := res.Record().Get("code")
		out = append(out, Violation{
			Code: "I8", Severity: SeverityError,
			Message:  fmt.Sprintf("capability %v is part of a BROADER cycle", code),
			Subjects: []string{fmt.Sprintf("%v", code)},
		})
	}
	return out, res.Err()
}

// I9: Permission.catalogVersion must match the orchestrator's embedded
// grant-catalog version (osiris-core's CatalogVersion constant, until
// osiris-orchestrator exists to have its own — see IMPLEMENTATION-NOTES.md).
func checkI9_PermissionCatalogVersion(ctx context.Context, s neo4j.SessionWithContext, catalogVersion string) ([]Violation, error) {
	res, err := s.Run(ctx, `
		MATCH (p:Permission)
		WHERE p.catalogVersion <> $catalogVersion OR p.catalogVersion IS NULL
		RETURN collect(p.code) AS codes
	`, map[string]any{"catalogVersion": catalogVersion})
	if err != nil {
		return nil, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return nil, err
	}
	codesRaw, _ := rec.Get("codes")
	codes, _ := codesRaw.([]any)
	if len(codes) == 0 {
		return nil, nil
	}
	var subs []string
	for _, c := range codes {
		subs = append(subs, fmt.Sprintf("%v", c))
	}
	return []Violation{{
		Code: "I9", Severity: SeverityError,
		Message:  fmt.Sprintf("permissions with mismatched/missing catalogVersion (expected %s): %v", catalogVersion, subs),
		Subjects: subs,
	}}, nil
}

// I10: :AIAgent.modelAlias must be one of the three allowed logical aliases.
func checkI10_ModelAliasAllowed(ctx context.Context, s neo4j.SessionWithContext, _ string) ([]Violation, error) {
	res, err := s.Run(ctx, `
		MATCH (a:AIAgent)
		WHERE a.closedAtRevision IS NULL
		  AND NOT a.modelAlias IN ["osiris/planner", "osiris/worker", "osiris/cheap"]
		RETURN a.id AS id, a.modelAlias AS modelAlias
	`, nil)
	if err != nil {
		return nil, err
	}
	var out []Violation
	for res.Next(ctx) {
		rec := res.Record()
		id, _ := rec.Get("id")
		alias, _ := rec.Get("modelAlias")
		out = append(out, Violation{
			Code: "I10", Severity: SeverityError,
			Message:  fmt.Sprintf("agent %v has disallowed modelAlias %v", id, alias),
			Subjects: []string{fmt.Sprintf("%v", id)},
		})
	}
	return out, res.Err()
}

// I11 (warning): if a team's role requires cap.exec.physical, that team
// must have at least one human member.
func checkI11_PhysicalRoleNeedsHuman(ctx context.Context, s neo4j.SessionWithContext, _ string) ([]Violation, error) {
	res, err := s.Run(ctx, `
		MATCH (role:Role)-[:REQUIRES_CAPABILITY]->(:Capability {code: "cap.exec.physical"})
		MATCH (t:Team {id: role.defaultTeamId})
		OPTIONAL MATCH (t)<-[:IN_TEAM]-(m:Membership)<-[:HAS_MEMBERSHIP]-(person:Person)
		  WHERE m.validTo IS NULL
		WITH t, role, count(person) AS humans
		WHERE humans = 0
		RETURN t.id AS teamId, role.id AS roleId
	`, nil)
	if err != nil {
		return nil, err
	}
	var out []Violation
	for res.Next(ctx) {
		rec := res.Record()
		teamID, _ := rec.Get("teamId")
		roleID, _ := rec.Get("roleId")
		out = append(out, Violation{
			Code: "I11", Severity: SeverityWarning,
			Message:  fmt.Sprintf("team %v has role %v requiring physical execution but no human member", teamID, roleID),
			Subjects: []string{fmt.Sprintf("%v", teamID), fmt.Sprintf("%v", roleID)},
		})
	}
	return out, res.Err()
}

// I12: no leaf :WorkItem with executionMode 'hybrid' (C6).
func checkI12_NoHybridLeafWorkItem(ctx context.Context, s neo4j.SessionWithContext, _ string) ([]Violation, error) {
	res, err := s.Run(ctx, `
		MATCH (w:WorkItem {executionMode: "hybrid"})
		WHERE NOT (w)-[:PARENT_OF]->(:WorkItem)
		RETURN w.id AS id
	`, nil)
	if err != nil {
		return nil, err
	}
	var out []Violation
	for res.Next(ctx) {
		id, _ := res.Record().Get("id")
		out = append(out, Violation{
			Code: "I12", Severity: SeverityError,
			Message:  fmt.Sprintf("leaf WorkItem %v has executionMode 'hybrid' (not allowed on leaves)", id),
			Subjects: []string{fmt.Sprintf("%v", id)},
		})
	}
	return out, res.Err()
}

// --- [2026-07-27] I14~I17: the org role model (20 §9.3, 60 §20/§21) --------

// effectivePermissionsCypher is 20-ontology-schema.md §7.1's effective(party),
// transcribed into Cypher ONCE and reused by every check and write operation
// that needs it.
//
//	effective(party) = (∪ grants(base profiles)) ∪ (∪ grants(additive profiles))
//	                   − denies(party) − (∪ denies(profile))
//
// THIS IS THE GRAPH-SIDE COPY OF A FORMULA osiris-api ALSO EVALUATES, and that
// duplication is deliberate and bounded. 60 §21.4-1 is explicit that the
// DELEGATION DECISION lives in exactly one place (osiris-api's
// authz.CheckDelegation, H31/HI16) and that core must NOT re-implement it —
// what core keeps is the INVARIANT CHECK (I14), which survives an osiris-api
// compromise and is the last wall against locking the organization out. An
// invariant check needs the permission set; it does not need, and does not
// have, any notion of who is allowed to change it.
//
// The caller binds $partyId (single-party form) or uses the collect form
// below. base_profiles is deliberately NOT restricted to IN_TEAM memberships:
// §7.1's definition says "the :Role of a currently valid :Membership" and says
// nothing about teams, which is exactly why IN_ORG memberships flow into
// effective(party) with zero changes to the formula (60 §20.2).
const effectiveHoldersCypher = `
	MATCH (p:Person)
	WHERE p.closedAtRevision IS NULL
	OPTIONAL MATCH (p)-[:HAS_MEMBERSHIP]->(mb:Membership)-[:AS_ROLE]->(:Role)
	                 -[:HAS_PROFILE {kind: 'base'}]->(:PermissionProfile)-[:GRANTS]->(bg:Permission)
	  WHERE mb.validTo IS NULL
	WITH p, collect(DISTINCT bg.code) AS baseGrants
	OPTIONAL MATCH (p)-[:HAS_MEMBERSHIP]->(md:Membership)-[:AS_ROLE]->(:Role)
	                 -[:HAS_PROFILE {kind: 'base'}]->(:PermissionProfile)-[:DENIES]->(bd:Permission)
	  WHERE md.validTo IS NULL
	WITH p, baseGrants, collect(DISTINCT bd.code) AS baseDenies
	OPTIONAL MATCH (p)-[:HAS_PROFILE {kind: 'additive'}]->(:PermissionProfile)-[:GRANTS]->(ag:Permission)
	WITH p, baseGrants, baseDenies, collect(DISTINCT ag.code) AS addGrants
	OPTIONAL MATCH (p)-[:HAS_PROFILE {kind: 'additive'}]->(:PermissionProfile)-[:DENIES]->(ad:Permission)
	WITH p, baseGrants, baseDenies, addGrants, collect(DISTINCT ad.code) AS addDenies
	OPTIONAL MATCH (p)-[:DENIES]->(pd:Permission)
	WITH p, baseGrants, baseDenies, addGrants, addDenies, collect(DISTINCT pd.code) AS partyDenies
	WITH p, baseGrants + addGrants AS granted, baseDenies + addDenies + partyDenies AS denied
	WITH p, [c IN granted WHERE NOT c IN denied] AS effective
`

// I14: at least one ACTIVE :Person effectively holds org.admin.
//
// This is the graph half of DG4 (60 §21.1). The other half — "and at least one
// of them can actually log in" — is NOT expressible here and must not be
// faked: credentials live in JetStream KV and the graph does not know that
// store exists (H1's one-way reference). 20 §9.3 records the split explicitly.
// Merging the two layers would require the graph to read a store it is
// designed not to know about; keeping them separate is what lets I14 survive
// an osiris-api compromise while the API-layer precondition catches the
// "admins exist but nobody can log in" state I14 cannot see.
func checkI14_AtLeastOneOrgAdmin(ctx context.Context, s neo4j.SessionWithContext, _ string) ([]Violation, error) {
	res, err := s.Run(ctx, effectiveHoldersCypher+`
		WITH p, effective WHERE "org.admin" IN effective
		RETURN count(p) AS admins
	`, nil)
	if err != nil {
		return nil, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return nil, err
	}
	nRaw, _ := rec.Get("admins")
	n, _ := nRaw.(int64)
	if n >= 1 {
		return nil, nil
	}
	return []Violation{{
		Code: "I14", Severity: SeverityError,
		Message: "no active :Person effectively holds org.admin — this commit would lock the organization out",
	}}, nil
}

// I15 / C9: an IN_ORG membership belongs to a :Person and to nothing else, and
// a :Membership has exactly one of IN_TEAM / IN_ORG.
//
// The :Person restriction is what keeps I2 ("every :AIAgent has exactly one
// valid membership") true without amendment: give an agent an org membership
// and it suddenly has two.
func checkI15_OrgMembershipPersonOnly(ctx context.Context, s neo4j.SessionWithContext, _ string) ([]Violation, error) {
	var out []Violation

	res, err := s.Run(ctx, `
		MATCH (party:Party)-[:HAS_MEMBERSHIP]->(m:Membership)-[:IN_ORG]->(:Organization)
		WHERE NOT party:Person
		RETURN party.id AS id, m.id AS mid
	`, nil)
	if err != nil {
		return nil, err
	}
	for res.Next(ctx) {
		rec := res.Record()
		id, _ := rec.Get("id")
		mid, _ := rec.Get("mid")
		out = append(out, Violation{
			Code: "I15", Severity: SeverityError,
			Message:  fmt.Sprintf("party %v owns IN_ORG membership %v but is not a :Person", id, mid),
			Subjects: []string{fmt.Sprintf("%v", id), fmt.Sprintf("%v", mid)},
		})
	}
	if err := res.Err(); err != nil {
		return out, err
	}

	// C9's second clause. A membership pointing at both, or at neither, makes
	// every "is this a team role or an org role" query silently wrong.
	res2, err := s.Run(ctx, `
		MATCH (m:Membership)
		OPTIONAL MATCH (m)-[:IN_TEAM]->(t:Team)
		OPTIONAL MATCH (m)-[:IN_ORG]->(o:Organization)
		WITH m, count(DISTINCT t) AS teams, count(DISTINCT o) AS orgs
		WHERE teams + orgs <> 1
		RETURN m.id AS mid, teams AS teams, orgs AS orgs
	`, nil)
	if err != nil {
		return out, err
	}
	for res2.Next(ctx) {
		rec := res2.Record()
		mid, _ := rec.Get("mid")
		teams, _ := rec.Get("teams")
		orgs, _ := rec.Get("orgs")
		out = append(out, Violation{
			Code: "I15", Severity: SeverityError,
			Message:  fmt.Sprintf("membership %v has %v IN_TEAM and %v IN_ORG edges (C9 requires exactly one)", mid, teams, orgs),
			Subjects: []string{fmt.Sprintf("%v", mid)},
		})
	}
	return out, res2.Err()
}

// I16: at most one currently-valid IN_ORG membership per :Person.
//
// 60 §20.2's reason for the ceiling: "this person's org role" must be a SINGLE
// value or the console, the audit trail and the delegation rules all have to
// reason about unions. Two org roles would union in effective(party) and
// produce permission combinations nobody chose. The escape hatch for genuine
// exceptions already exists and is visible to the audit query Q6:
// HAS_PROFILE{kind:'additive'} — and because DG1/DG2 compare CODE SETS rather
// than roles, that escape hatch is not a way around the delegation rules.
func checkI16_AtMostOneValidOrgMembership(ctx context.Context, s neo4j.SessionWithContext, _ string) ([]Violation, error) {
	res, err := s.Run(ctx, `
		MATCH (p:Person)-[:HAS_MEMBERSHIP]->(m:Membership)-[:IN_ORG]->(:Organization)
		WHERE m.validTo IS NULL
		WITH p, count(m) AS n
		WHERE n > 1
		RETURN p.id AS id, n AS n
	`, nil)
	if err != nil {
		return nil, err
	}
	var out []Violation
	for res.Next(ctx) {
		rec := res.Record()
		id, _ := rec.Get("id")
		n, _ := rec.Get("n")
		out = append(out, Violation{
			Code: "I16", Severity: SeverityError,
			Message:  fmt.Sprintf("person %v has %v valid IN_ORG memberships (at most 1 allowed)", id, n),
			Subjects: []string{fmt.Sprintf("%v", id)},
		})
	}
	return out, res.Err()
}

// I17: :Role{scope:'org'} carries no capability requirements and no team
// placement, and the two membership kinds point at the matching role scope.
//
// The first clause is what makes I4/I6/I11 vacuous for org roles rather than
// wrong about them. The second (converse) clause is what stops the two role
// vocabularies from being mixed — an IN_TEAM membership pointing at
// role-org-admin would grant org.admin through the assignment-visible path.
func checkI17_OrgRoleShape(ctx context.Context, s neo4j.SessionWithContext, _ string) ([]Violation, error) {
	var out []Violation

	res, err := s.Run(ctx, `
		MATCH (r:Role) WHERE r.scope = 'org'
		OPTIONAL MATCH (r)-[rc:REQUIRES_CAPABILITY]->()
		WITH r, count(rc) AS caps
		WHERE caps > 0 OR coalesce(r.defaultTeamId, '') <> '' OR r.isLead IS NOT NULL
		RETURN r.id AS id, caps AS caps
	`, nil)
	if err != nil {
		return nil, err
	}
	for res.Next(ctx) {
		rec := res.Record()
		id, _ := rec.Get("id")
		caps, _ := rec.Get("caps")
		out = append(out, Violation{
			Code: "I17", Severity: SeverityError,
			Message:  fmt.Sprintf("org-scoped role %v must have 0 REQUIRES_CAPABILITY (has %v) and null defaultTeamId/isLead", id, caps),
			Subjects: []string{fmt.Sprintf("%v", id)},
		})
	}
	if err := res.Err(); err != nil {
		return out, err
	}

	// Converse: membership kind and role scope must agree. A :Role with no
	// scope property is read as 'job' (20 §3.2 — the default preserves the
	// meaning of every pre-existing role).
	res2, err := s.Run(ctx, `
		MATCH (m:Membership)-[:AS_ROLE]->(r:Role)
		OPTIONAL MATCH (m)-[:IN_ORG]->(o:Organization)
		WITH m, r, count(o) > 0 AS isOrg
		WITH m, r, isOrg, coalesce(r.scope, 'job') AS scope
		WHERE (isOrg AND scope <> 'org') OR (NOT isOrg AND scope <> 'job')
		RETURN m.id AS mid, r.id AS rid, scope AS scope, isOrg AS isOrg
	`, nil)
	if err != nil {
		return out, err
	}
	for res2.Next(ctx) {
		rec := res2.Record()
		mid, _ := rec.Get("mid")
		rid, _ := rec.Get("rid")
		scope, _ := rec.Get("scope")
		isOrg, _ := rec.Get("isOrg")
		kind := "IN_TEAM"
		if b, _ := isOrg.(bool); b {
			kind = "IN_ORG"
		}
		out = append(out, Violation{
			Code: "I17", Severity: SeverityError,
			Message:  fmt.Sprintf("%s membership %v points at role %v with scope %v", kind, mid, rid, scope),
			Subjects: []string{fmt.Sprintf("%v", mid), fmt.Sprintf("%v", rid)},
		})
	}
	return out, res2.Err()
}
