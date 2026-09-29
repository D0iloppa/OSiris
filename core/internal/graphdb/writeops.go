// graph-write API operations — the ONLY way anything outside this package
// changes organizational data in Neo4j (A5/G7, D17). Every exported function
// here is a fixed, parameterized Cypher operation; there is no function that
// accepts a caller-supplied query string. Each one:
//  1. runs inside a single Neo4j write transaction,
//  2. validates its references (e.g. a Role's required capability must
//     already exist in the catalog — an unresolvable reference fails the
//     whole transaction, nothing partial is left behind),
//  3. bumps :OrgRevision by exactly 1 (C8) in the SAME transaction,
//  4. returns the new revision number so the caller (api package) can
//     publish org.revision / org.event.<type> AFTER commit succeeds.
//
// This file intentionally covers only what onboarding needs to seed a team
// (§9.2 steps 7-12: Team, Role, Person, AIAgent, Membership,
// AssignmentPolicy). See IMPLEMENTATION-NOTES.md for this build's history.
//
// [P3 update] The assignment engine (20 §8) is NOW in scope, but lives in
// its own files rather than being added to this one: the graph-query half
// of §8.3 is assign_candidates.go/assign_task.go/assign_policy.go, and the
// graph-write half of §8.7 is assign_persist.go (:AssignmentDecision/
// :Assignment/:WorkItem.status, all in one transaction, append-only per C5)
// plus assign_workitem.go's minimal CreateWorkItem (just enough of :WorkItem
// to exercise PersistDecision — not WBS decomposition, which stays
// agent-master's territory). The pure F1-F5/tie-break/fallback computation
// itself lives outside this package entirely, in osiris-core/internal/
// assign, per that package's doc comment.
//
// [2026-09-11, C-1] :Approval IS NO LONGER OUT OF SCOPE.
// docs/design/90-infra-topology.md §11 (IN17/IN18/IN19) stands up the first
// real propose→decide round-trip against the existing :Approval node
// (schema.cypher already carried its `id` uniqueness constraint and
// `(status, requestedAt)` index — only the writeops were missing). The three
// operations (CreateApproval/ListPendingApprovals/DecideApproval) live in
// writeops_approval.go, not in this file, to keep this header's file list
// accurate without editing this paragraph again — see that file's own doc
// comment for IN18's one HI15-style exception (machine principals may
// create a proposal; deciding one is PersonOnly+admin territory, IN19) and
// for why creating a proposal does not, by itself, grant any capability
// (an :Approval is inert until a person decides it).
package graphdb

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ErrValidation marks a rejected write caused by bad caller input (missing
// reference, duplicate isMaster, etc.) — the api layer maps this to HTTP 400
// rather than 500.
var ErrValidation = errors.New("validation error")

func validationErrorf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrValidation}, args...)...)
}

// bumpRevision must be called inside an already-open write transaction. It
// is the only place that creates :OrgRevision nodes outside of seed's n:0.
func bumpRevision(ctx context.Context, tx neo4j.ManagedTransaction, byPartyID, summary, changeKind string) (int64, error) {
	res, err := tx.Run(ctx, `
		MATCH (prev:OrgRevision)
		WITH prev ORDER BY prev.n DESC LIMIT 1
		CREATE (next:OrgRevision {n: prev.n + 1, at: datetime(), byPartyId: $byPartyId, summary: $summary, changeKind: $changeKind})
		CREATE (next)-[:PREV]->(prev)
		RETURN next.n AS n
	`, map[string]any{"byPartyId": byPartyID, "summary": summary, "changeKind": changeKind})
	if err != nil {
		return 0, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := rec.Get("n")
	nInt, _ := n.(int64)
	return nInt, nil
}

// --- Team -------------------------------------------------------------

type TeamInput struct {
	ID               string
	Name             string
	Slug             string
	ChannelSubject   string
	MonthlyBudgetUsd float64
	ParentTeamID     string // "" => PART_OF the Organization
	ByPartyID        string
}

type WriteResult struct {
	Revision int64
	ID       string
}

func (c *Client) CreateTeam(ctx context.Context, in TeamInput) (WriteResult, error) {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		if in.ParentTeamID != "" {
			check, err := tx.Run(ctx, `MATCH (t:Team {id: $id}) RETURN t.id`, map[string]any{"id": in.ParentTeamID})
			if err != nil {
				return nil, err
			}
			if _, err := check.Single(ctx); err != nil {
				return nil, validationErrorf("parent team %q not found", in.ParentTeamID)
			}
		}
		_, err := tx.Run(ctx, `
			MERGE (t:Team:Party {id: $id})
			ON CREATE SET t.name = $name, t.slug = $slug, t.channelSubject = $channelSubject,
			              t.monthlyBudgetUsd = $budget, t.createdAtRevision = 0
			ON MATCH SET  t.name = $name, t.slug = $slug, t.channelSubject = $channelSubject,
			              t.monthlyBudgetUsd = $budget
		`, map[string]any{
			"id": in.ID, "name": in.Name, "slug": in.Slug, "channelSubject": in.ChannelSubject,
			"budget": in.MonthlyBudgetUsd,
		})
		if err != nil {
			return nil, err
		}

		// Two fixed queries chosen by a Go branch, deliberately NOT a single
		// query with FOREACH/CASE conditional MERGE: that pattern produced a
		// duplicate :Organization node in testing (a documented Neo4j
		// footgun — MERGE's node-reuse guarantee is unreliable inside
		// FOREACH). Plain branching in Go is clearer and was verified not to
		// duplicate anything.
		if in.ParentTeamID != "" {
			_, err = tx.Run(ctx, `
				MATCH (t:Team {id: $id})
				MATCH (parent:Team {id: $parentId})
				MERGE (t)-[:PART_OF]->(parent)
			`, map[string]any{"id": in.ID, "parentId": in.ParentTeamID})
		} else {
			_, err = tx.Run(ctx, `
				MATCH (t:Team {id: $id})
				MATCH (org:Organization {id: "org-root"})
				MERGE (t)-[:PART_OF]->(org)
			`, map[string]any{"id": in.ID})
		}
		if err != nil {
			return nil, err
		}
		n, err := bumpRevision(ctx, tx, in.ByPartyID, fmt.Sprintf("team %s created/updated", in.ID), "team_upserted")
		if err != nil {
			return nil, err
		}
		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.ID}, nil
}

// --- Role ---------------------------------------------------------------

type RequiredCapability struct {
	Code     string
	MinLevel int
	Weight   float64
}

type RoleInput struct {
	ID                      string
	Title                   string
	Level                   int
	DefaultTeamID           string
	IsLead                  bool
	ResponsibilitiesSummary string
	PromptTemplateMd        string
	RequiredCapabilities    []RequiredCapability
	BasePermissionProfileID string
	ByPartyID               string
}

func (c *Client) CreateRole(ctx context.Context, in RoleInput) (WriteResult, error) {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		for _, rc := range in.RequiredCapabilities {
			check, err := tx.Run(ctx, `MATCH (c:Capability {code: $code}) RETURN c.code`, map[string]any{"code": rc.Code})
			if err != nil {
				return nil, err
			}
			if _, err := check.Single(ctx); err != nil {
				return nil, validationErrorf("capability %q not in catalog", rc.Code)
			}
			if rc.MinLevel < 1 || rc.MinLevel > 5 {
				return nil, validationErrorf("capability %q minLevel must be 1..5, got %d", rc.Code, rc.MinLevel)
			}
		}
		if in.BasePermissionProfileID != "" {
			check, err := tx.Run(ctx, `MATCH (p:PermissionProfile {id: $id}) RETURN p.id`, map[string]any{"id": in.BasePermissionProfileID})
			if err != nil {
				return nil, err
			}
			if _, err := check.Single(ctx); err != nil {
				return nil, validationErrorf("permission profile %q not found", in.BasePermissionProfileID)
			}
		}

		// [2026-07-27] :Role.scope (20 §3.2). This entry point creates JOB
		// roles only — the three scope:'org' roles are code-owned system seed
		// (seed.go's SystemOrgRoles) and are not editable through the generic
		// role API. Without this guard a POST /v1/roles naming role-org-admin
		// would silently convert it into a job role, which would (a) strip the
		// org role every admin's membership points at and (b) make an IN_ORG
		// membership point at a scope:'job' role, violating I17 — a privilege
		// change disguised as a role edit.
		scopeCheck, err := tx.Run(ctx, `MATCH (r:Role {id: $id}) WHERE r.scope = 'org' RETURN r.id`,
			map[string]any{"id": in.ID})
		if err != nil {
			return nil, err
		}
		if _, err := scopeCheck.Single(ctx); err == nil {
			return nil, validationErrorf("role %q is a system org-scoped role and cannot be edited through the job-role API", in.ID)
		}

		_, err = tx.Run(ctx, `
			MERGE (r:Role {id: $id})
			ON CREATE SET r.scope = 'job', r.title = $title, r.level = $level, r.defaultTeamId = $teamId,
			              r.isLead = $isLead, r.responsibilitiesSummary = $summary,
			              r.promptTemplateMd = $prompt, r.promptTemplateVersion = 1,
			              r.promptAuthoredAtRevision = 0
			ON MATCH SET  r.scope = 'job', r.title = $title, r.level = $level, r.defaultTeamId = $teamId,
			              r.isLead = $isLead, r.responsibilitiesSummary = $summary,
			              r.promptTemplateMd = $prompt,
			              r.promptTemplateVersion = coalesce(r.promptTemplateVersion, 0) + 1
		`, map[string]any{
			"id": in.ID, "title": in.Title, "level": in.Level, "teamId": in.DefaultTeamID,
			"isLead": in.IsLead, "summary": in.ResponsibilitiesSummary, "prompt": in.PromptTemplateMd,
		})
		if err != nil {
			return nil, err
		}

		for _, rc := range in.RequiredCapabilities {
			_, err := tx.Run(ctx, `
				MATCH (r:Role {id: $roleId})
				MATCH (c:Capability {code: $code})
				MERGE (r)-[req:REQUIRES_CAPABILITY]->(c)
				SET req.minLevel = $minLevel, req.weight = $weight
			`, map[string]any{"roleId": in.ID, "code": rc.Code, "minLevel": rc.MinLevel, "weight": rc.Weight})
			if err != nil {
				return nil, err
			}
		}

		if in.BasePermissionProfileID != "" {
			_, err := tx.Run(ctx, `
				MATCH (r:Role {id: $roleId})
				// Same stale-edge class as CreateAIAgent's USES_PROFILE fix
				// (see comment there): drop any base HAS_PROFILE edge that no
				// longer matches the requested profile before merging the
				// new one, so changing a Role's base permission profile
				// doesn't leave the old grants still unioned in via a
				// leftover edge.
				OPTIONAL MATCH (r)-[stale:HAS_PROFILE {kind: 'base'}]->(staleP:PermissionProfile)
				  WHERE staleP.id <> $profileId
				DELETE stale
				WITH r
				MATCH (p:PermissionProfile {id: $profileId})
				MERGE (r)-[hp:HAS_PROFILE {kind: 'base'}]->(p)
			`, map[string]any{"roleId": in.ID, "profileId": in.BasePermissionProfileID})
			if err != nil {
				return nil, err
			}
		}

		n, err := bumpRevision(ctx, tx, in.ByPartyID, fmt.Sprintf("role %s created/updated", in.ID), "role_upserted")
		if err != nil {
			return nil, err
		}
		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.ID}, nil
}

// --- Person ---------------------------------------------------------------

type HeldCapability struct {
	Code   string
	Level  int
	Source string // declared | verified | inferred
}

type PersonInput struct {
	ID             string
	Name           string
	Email          string
	Timezone       string
	WorkingHours   string
	Availability   string
	NotifyChannels []string
	MaxTaskPoints  int
	Capabilities   []HeldCapability
	ByPartyID      string
}

func (c *Client) CreatePerson(ctx context.Context, in PersonInput) (WriteResult, error) {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		for _, hc := range in.Capabilities {
			check, err := tx.Run(ctx, `MATCH (c:Capability {code: $code}) RETURN c.code`, map[string]any{"code": hc.Code})
			if err != nil {
				return nil, err
			}
			if _, err := check.Single(ctx); err != nil {
				return nil, validationErrorf("capability %q not in catalog", hc.Code)
			}
		}

		// OD1 / the person_email uniqueness constraint. Checked explicitly so
		// the caller gets a 400 with a legible message instead of a 500
		// carrying a raw Neo4j ConstraintValidationFailed. 20 §3.1 makes email
		// the console login identifier, so a duplicate would make "who logs in
		// with this address" non-deterministic (60 H6).
		if in.Email != "" {
			dup, err := tx.Run(ctx, `MATCH (o:Person) WHERE o.email = $email AND o.id <> $id RETURN o.id`,
				map[string]any{"email": in.Email, "id": in.ID})
			if err != nil {
				return nil, err
			}
			if rec, err := dup.Single(ctx); err == nil {
				other, _ := rec.Get("id")
				return nil, validationErrorf("email %q is already used by person %v (person_email is unique)", in.Email, other)
			}
		}

		_, err := tx.Run(ctx, `
			MERGE (p:Person:Party {id: $id})
			ON CREATE SET p.name = $name, p.email = $email, p.timezone = $timezone,
			              p.workingHours = $workingHours, p.availability = $availability,
			              p.notifyChannels = $notifyChannels, p.maxTaskPoints = $maxTaskPoints,
			              p.createdAtRevision = 0
			ON MATCH SET  p.name = $name, p.email = $email, p.timezone = $timezone,
			              p.workingHours = $workingHours, p.availability = $availability,
			              p.notifyChannels = $notifyChannels, p.maxTaskPoints = $maxTaskPoints
		`, map[string]any{
			"id": in.ID, "name": in.Name, "email": in.Email, "timezone": in.Timezone,
			"workingHours": in.WorkingHours, "availability": in.Availability,
			"notifyChannels": in.NotifyChannels, "maxTaskPoints": in.MaxTaskPoints,
		})
		if err != nil {
			return nil, err
		}

		for _, hc := range in.Capabilities {
			_, err := tx.Run(ctx, `
				MATCH (p:Person {id: $id})
				MATCH (c:Capability {code: $code})
				MERGE (p)-[h:HAS_CAPABILITY]->(c)
				SET h.level = $level, h.source = $source
			`, map[string]any{"id": in.ID, "code": hc.Code, "level": hc.Level, "source": hc.Source})
			if err != nil {
				return nil, err
			}
		}

		n, err := bumpRevision(ctx, tx, in.ByPartyID, fmt.Sprintf("person %s created/updated", in.ID), "person_upserted")
		if err != nil {
			return nil, err
		}
		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.ID}, nil
}

// --- AIAgent ----------------------------------------------------------

type AgentInput struct {
	ID            string
	Name          string
	RuntimeClass  string // = :RoleResourceProfile.id
	ModelAlias    string // osiris/planner | osiris/worker | osiris/cheap
	Image         string
	MaxTaskPoints int
	DrainSeconds  int
	DesiredState  string // Running | Suspended
	IsMaster      bool
	Capabilities  []HeldCapability
	ByPartyID     string
}

var allowedModelAliases = map[string]bool{
	"osiris/planner": true, "osiris/worker": true, "osiris/cheap": true,
}

// AllowedModelAliases exposes I10's fixed triple as a sorted slice, so the
// onboarding catalog read and the draft validator can OFFER exactly what this
// write path ENFORCES.
//
// Deliberately derived from the same map rather than duplicated: a second list
// of these three names is a second thing to forget to update, and I10 exists
// precisely because the alias set must not drift (omniroute/config.json's
// comment: "이건 고정, 협상 대상 아님").
func AllowedModelAliases() []string {
	out := make([]string, 0, len(allowedModelAliases))
	for a := range allowedModelAliases {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// IsAllowedModelAlias is the same predicate the write path applies, exported so
// the draft validator refuses at conversation time what the commit would refuse
// later (30 §3.3: "커밋 시점의 검사는 이미 조기 검증된 것들의 최종 확인").
func IsAllowedModelAlias(a string) bool { return allowedModelAliases[a] }

func (c *Client) CreateAIAgent(ctx context.Context, in AgentInput) (WriteResult, error) {
	if !allowedModelAliases[in.ModelAlias] {
		return WriteResult{}, validationErrorf("modelAlias %q is not one of osiris/planner|osiris/worker|osiris/cheap", in.ModelAlias)
	}
	if in.DesiredState != "Running" && in.DesiredState != "Suspended" {
		return WriteResult{}, validationErrorf("desiredState must be Running or Suspended, got %q", in.DesiredState)
	}

	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		if in.IsMaster {
			check, err := tx.Run(ctx, `
				MATCH (a:AIAgent {isMaster: true})
				WHERE a.id <> $id AND a.closedAtRevision IS NULL
				RETURN count(a) AS n
			`, map[string]any{"id": in.ID})
			if err != nil {
				return nil, err
			}
			rec, err := check.Single(ctx)
			if err != nil {
				return nil, err
			}
			nRaw, _ := rec.Get("n")
			if n, _ := nRaw.(int64); n > 0 {
				return nil, validationErrorf("another AIAgent already has isMaster:true (I3 forbids more than one)")
			}
		}

		check, err := tx.Run(ctx, `MATCH (rp:RoleResourceProfile {id: $id}) RETURN rp.id`, map[string]any{"id": in.RuntimeClass})
		if err != nil {
			return nil, err
		}
		if _, err := check.Single(ctx); err != nil {
			return nil, validationErrorf("runtimeClass/RoleResourceProfile %q not found", in.RuntimeClass)
		}

		for _, hc := range in.Capabilities {
			if hc.Code == "cap.exec.physical" {
				return nil, validationErrorf("cap.exec.physical cannot be granted to an AIAgent (I5 — Person only)")
			}
			c2, err := tx.Run(ctx, `MATCH (c:Capability {code: $code}) RETURN c.code`, map[string]any{"code": hc.Code})
			if err != nil {
				return nil, err
			}
			if _, err := c2.Single(ctx); err != nil {
				return nil, validationErrorf("capability %q not in catalog", hc.Code)
			}
		}

		_, err = tx.Run(ctx, `
			MERGE (a:AIAgent:Party {id: $id})
			ON CREATE SET a.name = $name, a.runtimeClass = $runtimeClass, a.modelAlias = $modelAlias,
			              a.image = $image, a.maxTaskPoints = $maxTaskPoints, a.drainSeconds = $drainSeconds,
			              a.desiredState = $desiredState, a.isMaster = $isMaster, a.createdAtRevision = 0
			ON MATCH SET  a.name = $name, a.runtimeClass = $runtimeClass, a.modelAlias = $modelAlias,
			              a.image = $image, a.maxTaskPoints = $maxTaskPoints, a.drainSeconds = $drainSeconds,
			              a.desiredState = $desiredState, a.isMaster = $isMaster
			WITH a
			// Drop any USES_PROFILE edge that no longer matches the requested
			// runtimeClass BEFORE merging the new one. Found by the
			// osiris-orchestrator worker: on an update that changes
			// runtimeClass (e.g. light -> standard), the old MERGE-only
			// version below left the STALE edge in place (MERGE only ever
			// creates-if-absent for the node named $runtimeClass; it has no
			// reason to touch an edge pointing at a different node), so an
			// agent could accumulate multiple :USES_PROFILE edges — exactly
			// the kind of Neo4j footgun IMPLEMENTATION-NOTES.md §11 already
			// documented once for :Organization/FOREACH. Same fix pattern:
			// an explicit DELETE for the "no longer wanted" edge, not a
			// MERGE that only ever adds.
			OPTIONAL MATCH (a)-[stale:USES_PROFILE]->(staleRp:RoleResourceProfile)
			  WHERE staleRp.id <> $runtimeClass
			DELETE stale
			WITH a
			MATCH (rp:RoleResourceProfile {id: $runtimeClass})
			MERGE (a)-[:USES_PROFILE]->(rp)
		`, map[string]any{
			"id": in.ID, "name": in.Name, "runtimeClass": in.RuntimeClass, "modelAlias": in.ModelAlias,
			"image": in.Image, "maxTaskPoints": in.MaxTaskPoints, "drainSeconds": in.DrainSeconds,
			"desiredState": in.DesiredState, "isMaster": in.IsMaster,
		})
		if err != nil {
			return nil, err
		}

		for _, hc := range in.Capabilities {
			_, err := tx.Run(ctx, `
				MATCH (a:AIAgent {id: $id})
				MATCH (c:Capability {code: $code})
				MERGE (a)-[h:HAS_CAPABILITY]->(c)
				SET h.level = $level, h.source = $source
			`, map[string]any{"id": in.ID, "code": hc.Code, "level": hc.Level, "source": hc.Source})
			if err != nil {
				return nil, err
			}
		}

		n, err := bumpRevision(ctx, tx, in.ByPartyID, fmt.Sprintf("agent %s created/updated", in.ID), "agent_upserted")
		if err != nil {
			return nil, err
		}
		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.ID}, nil
}

// --- Membership -----------------------------------------------------------

type MembershipInput struct {
	ID            string
	PartyID       string
	TeamID        string
	RoleID        string
	IsHead        bool
	AllocationPct int
	ByPartyID     string
}

func (c *Client) CreateMembership(ctx context.Context, in MembershipInput) (WriteResult, error) {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		// Three fixed, separate queries instead of one query text built with
		// fmt.Sprintf — no query string in this codebase is ever assembled
		// from a label/field name, only ever from bound parameters ($id
		// etc.). That keeps "grep for Sprintf near .Run(" a sufficient audit
		// for A5/G7 rather than something a reviewer has to reason about.
		partyCheck, err := tx.Run(ctx, `MATCH (n:Party {id: $id}) RETURN n.id`, map[string]any{"id": in.PartyID})
		if err != nil {
			return nil, err
		}
		if _, err := partyCheck.Single(ctx); err != nil {
			return nil, validationErrorf("Party %q not found", in.PartyID)
		}
		teamCheck, err := tx.Run(ctx, `MATCH (n:Team {id: $id}) RETURN n.id`, map[string]any{"id": in.TeamID})
		if err != nil {
			return nil, err
		}
		if _, err := teamCheck.Single(ctx); err != nil {
			return nil, validationErrorf("Team %q not found", in.TeamID)
		}
		// I17's converse clause, enforced at write time: an IN_TEAM membership
		// must point at a scope:'job' role. Letting a team membership name
		// role-org-admin would grant org.admin through base_profiles while
		// looking like an ordinary team assignment — and it would reach the
		// assignment engine's candidate set, which §8.3's IN_TEAM restriction
		// assumes contains only job roles.
		roleCheck, err := tx.Run(ctx, `MATCH (n:Role {id: $id}) RETURN coalesce(n.scope, 'job') AS scope`,
			map[string]any{"id": in.RoleID})
		if err != nil {
			return nil, err
		}
		roleRec, err := roleCheck.Single(ctx)
		if err != nil {
			return nil, validationErrorf("Role %q not found", in.RoleID)
		}
		if sc, _ := roleRec.Get("scope"); fmt.Sprintf("%v", sc) != "job" {
			return nil, validationErrorf("Role %q has scope %v; an IN_TEAM membership requires a scope 'job' role (I17)", in.RoleID, sc)
		}

		if in.IsHead {
			check, err := tx.Run(ctx, `
				MATCH (:Team {id: $teamId})<-[:IN_TEAM]-(m:Membership {isHead: true})
				WHERE m.validTo IS NULL AND m.id <> $id
				RETURN count(m) AS n
			`, map[string]any{"teamId": in.TeamID, "id": in.ID})
			if err != nil {
				return nil, err
			}
			rec, err := check.Single(ctx)
			if err != nil {
				return nil, err
			}
			nRaw, _ := rec.Get("n")
			if n, _ := nRaw.(int64); n > 0 {
				return nil, validationErrorf("team %q already has a head membership (I1 forbids more than one)", in.TeamID)
			}
		}

		_, err = tx.Run(ctx, `
			MATCH (party {id: $partyId}), (team:Team {id: $teamId}), (role:Role {id: $roleId})
			MERGE (m:Membership {id: $id})
			ON CREATE SET m.validFrom = date(), m.createdAtRevision = 0,
			              m.isHead = $isHead, m.allocationPct = $allocationPct
			ON MATCH SET  m.isHead = $isHead, m.allocationPct = $allocationPct
			MERGE (party)-[:HAS_MEMBERSHIP]->(m)
			MERGE (m)-[:IN_TEAM]->(team)
			MERGE (m)-[:AS_ROLE]->(role)
		`, map[string]any{
			"id": in.ID, "partyId": in.PartyID, "teamId": in.TeamID, "roleId": in.RoleID,
			"isHead": in.IsHead, "allocationPct": in.AllocationPct,
		})
		if err != nil {
			return nil, err
		}

		n, err := bumpRevision(ctx, tx, in.ByPartyID, fmt.Sprintf("membership %s created/updated", in.ID), "membership_upserted")
		if err != nil {
			return nil, err
		}
		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.ID}, nil
}

// --- AssignmentPolicy -------------------------------------------------------

type AssignmentPolicyInput struct {
	Version                   int
	TieBreakChain             []string
	TeamStrict                bool
	LevelRelaxationAllowed    bool
	MaxCandidatesForDecompose int
	ByPartyID                 string
}

func (c *Client) CreateAssignmentPolicy(ctx context.Context, in AssignmentPolicyInput) (WriteResult, error) {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	id := fmt.Sprintf("policy-v%d", in.Version)
	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx, `
			MERGE (p:AssignmentPolicy {version: $version})
			ON CREATE SET p.effectiveFrom = datetime(), p.tieBreakChain = $chain,
			              p.teamStrict = $teamStrict, p.levelRelaxationAllowed = $relax,
			              p.maxCandidatesForDecompose = $maxCand
			ON MATCH SET  p.tieBreakChain = $chain, p.teamStrict = $teamStrict,
			              p.levelRelaxationAllowed = $relax, p.maxCandidatesForDecompose = $maxCand
		`, map[string]any{
			"version": in.Version, "chain": in.TieBreakChain, "teamStrict": in.TeamStrict,
			"relax": in.LevelRelaxationAllowed, "maxCand": in.MaxCandidatesForDecompose,
		})
		if err != nil {
			return nil, err
		}
		n, err := bumpRevision(ctx, tx, in.ByPartyID, fmt.Sprintf("assignment policy v%d created/updated", in.Version), "policy_upserted")
		if err != nil {
			return nil, err
		}
		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: id}, nil
}

// --- Close AIAgent (added by the osiris-orchestrator worker) --------------
//
// Neither §9.2 (onboarding sequence) nor this file's original write surface
// (I1/I2 workers' scope: Team/Role/Person/AIAgent/Membership/
// AssignmentPolicy CREATE/upsert) included a way to CLOSE an AIAgent. But
// completion criterion 4 of this build ("에이전트를 닫으면(validTo/
// closedAtRevision) 컨테이너가 드레인 후 제거된다") requires exactly that,
// and D17/G7 forbid writing it any other way than through this API. This is
// the minimal necessary extension: it does NOT delete anything (P4, "삭제하지
// 않는다") — it sets :AIAgent.closedAtRevision and closes (validTo)
// the agent's current :Membership, exactly mirroring how a Person would be
// offboarded. osiris-orchestrator never calls this itself; it only REACTS
// to closedAtRevision/validTo already being set (by whatever calls this
// endpoint — a human operator via CORE_API_TOKEN in this build, since
// osiris-api/console are out of scope). See IMPLEMENTATION-NOTES.md.
type CloseAgentInput struct {
	ID        string
	ByPartyID string
}

func (c *Client) CloseAIAgent(ctx context.Context, in CloseAgentInput) (WriteResult, error) {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		check, err := tx.Run(ctx, `MATCH (a:AIAgent {id: $id}) WHERE a.closedAtRevision IS NULL RETURN a.id`, map[string]any{"id": in.ID})
		if err != nil {
			return nil, err
		}
		if _, err := check.Single(ctx); err != nil {
			return nil, validationErrorf("AIAgent %q not found or already closed", in.ID)
		}

		n, err := bumpRevision(ctx, tx, in.ByPartyID, fmt.Sprintf("agent %s closed", in.ID), "agent_closed")
		if err != nil {
			return nil, err
		}

		_, err = tx.Run(ctx, `
			MATCH (a:AIAgent {id: $id})
			SET a.closedAtRevision = $rev, a.desiredState = "Suspended"
			WITH a
			OPTIONAL MATCH (a)-[:HAS_MEMBERSHIP]->(m:Membership)
			  WHERE m.validTo IS NULL
			SET m.validTo = date(), m.closedAtRevision = $rev
		`, map[string]any{"id": in.ID, "rev": n})
		if err != nil {
			return nil, err
		}
		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.ID}, nil
}

// CurrentRevision reads the highest :OrgRevision.n — used by /readyz-adjacent
// status endpoints and by the genesis-scope window check (§8.5 W1) if/when
// that is wired up by the orchestrator worker.
func (c *Client) CurrentRevision(ctx context.Context) (int64, error) {
	session := c.NewReadSession(ctx)
	defer session.Close(ctx)
	res, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, `MATCH (r:OrgRevision) RETURN max(r.n) AS n`, nil)
		if err != nil {
			return nil, err
		}
		rec, err := res.Single(ctx)
		if err != nil {
			return nil, err
		}
		n, _ := rec.Get("n")
		return n, nil
	})
	if err != nil {
		return 0, err
	}
	if res == nil {
		return 0, nil
	}
	n, _ := res.(int64)
	return n, nil
}

// ===========================================================================
// [2026-07-27] Org role model write surface — 60-identity-authn.md §20.6,
// 20-ontology-schema.md §9.2 step 11 / §11 C9·C10.
//
// WHAT THIS ADDS AND WHAT IT DELIBERATELY DOES NOT.
//
// It adds the five graph mutations §20.6 enumerates: grant an org role, end
// one, deactivate a person, attach/detach an additive permission profile, and
// add/remove a DENIES edge. Every one of them bumps :OrgRevision by exactly 1
// (C8) — they are organizational INTENT. Credential operations (password,
// enrollment ticket) do not appear here at all and never will: they are not
// intent, they live in JetStream KV, and H32 is precisely the statement that
// this line runs between them.
//
// IT DOES NOT CONTAIN THE DELEGATION RULES. DG1~DG5 are evaluated in exactly
// one place — osiris-api's authz.CheckDelegation (H31, checked by HI16) — and
// re-implementing them here would create the second decision point that H31
// forbids. 60 §21.4-1 confronts the obvious objection (an attacker who owns
// osiris-api gets past DG1~DG5 entirely) and answers it deliberately: the
// answer is NOT a second copy of the rules, it is I14 — a graph invariant that
// holds no matter what osiris-api believes, and that stops the one outcome
// that cannot be undone from the outside, an organization with no admin.
//
// So what every operation below does is: mutate, then re-check the org
// invariants INSIDE THE SAME TRANSACTION, and roll back the whole thing if any
// error-severity invariant fails. 20 §9.3's note requires exactly this scope —
// I14~I17 on every role-changing transaction, but NOT the full §9.2 step-14
// rehearsal, because org roles cannot reach the assignment path (§8.3's
// IN_TEAM restriction is what makes that true rather than hopeful).
// ===========================================================================

// PermOrgAdmin is the one permission code this package needs to name, and it
// names it for ONE purpose: I14 (is the organization about to be left with no
// administrator) and the DG5 notification fan-out that shares the same query.
// Authorization is not decided here — see the block comment above.
const PermOrgAdmin = "org.admin"

// orgInvariantCheck is one in-transaction invariant probe: a query that must
// return zero rows, and the message to raise if it does not.
type orgInvariantCheck struct {
	code  string
	query string
	msg   string
}

// orgInvariantChecks mirrors checkI14..checkI17 in invariants.go. They are
// separate code because they run in DIFFERENT CONTEXTS and must not be forced
// into one shape: invariants.go reports (read-only session, collects every
// violation with subjects, feeds GET /v1/invariants and the onboarding
// rehearsal), while these ABORT (inside the caller's open write transaction,
// first failure wins, nothing partial survives). Sharing one function would
// mean either running a reporting pass inside a write transaction or teaching
// the reporter to roll back — both worse than two small query lists that the
// invariant-injection test exercises together.
var orgInvariantChecks = []orgInvariantCheck{
	{
		code: "I15",
		query: `MATCH (party:Party)-[:HAS_MEMBERSHIP]->(m:Membership)-[:IN_ORG]->(:Organization)
		        WHERE NOT party:Person RETURN party.id AS x LIMIT 1`,
		msg: "I15: only a :Person may own an IN_ORG membership",
	},
	{
		code: "I15",
		query: `MATCH (m:Membership)
		        OPTIONAL MATCH (m)-[:IN_TEAM]->(t:Team)
		        OPTIONAL MATCH (m)-[:IN_ORG]->(o:Organization)
		        WITH m, count(DISTINCT t) AS teams, count(DISTINCT o) AS orgs
		        WHERE teams + orgs <> 1 RETURN m.id AS x LIMIT 1`,
		msg: "C9/I15: a :Membership must have exactly one of IN_TEAM / IN_ORG",
	},
	{
		code: "I16",
		query: `MATCH (p:Person)-[:HAS_MEMBERSHIP]->(m:Membership)-[:IN_ORG]->(:Organization)
		        WHERE m.validTo IS NULL
		        WITH p, count(m) AS n WHERE n > 1 RETURN p.id AS x LIMIT 1`,
		msg: "I16: a :Person may hold at most one valid IN_ORG membership",
	},
	{
		code: "I17",
		query: `MATCH (r:Role) WHERE r.scope = 'org'
		        OPTIONAL MATCH (r)-[rc:REQUIRES_CAPABILITY]->()
		        WITH r, count(rc) AS caps
		        WHERE caps > 0 OR coalesce(r.defaultTeamId, '') <> '' OR r.isLead IS NOT NULL
		        RETURN r.id AS x LIMIT 1`,
		msg: "I17: an org-scoped :Role must have no REQUIRES_CAPABILITY and null defaultTeamId/isLead",
	},
	{
		code: "I17",
		query: `MATCH (m:Membership)-[:AS_ROLE]->(r:Role)
		        OPTIONAL MATCH (m)-[:IN_ORG]->(o:Organization)
		        WITH m, r, count(o) > 0 AS isOrg, coalesce(r.scope, 'job') AS scope
		        WHERE (isOrg AND scope <> 'org') OR (NOT isOrg AND scope <> 'job')
		        RETURN m.id AS x LIMIT 1`,
		msg: "I17: membership kind and role scope must agree (IN_ORG->scope 'org', IN_TEAM->scope 'job')",
	},
}

// assertOrgInvariants runs I14~I17 inside the caller's OPEN write transaction,
// after the mutation. Returning an error aborts the transaction, so a change
// that would violate an invariant leaves the graph byte-identical.
//
// I14 is checked separately and LAST, because its failure message is the one a
// human most needs to understand ("this would lock the organization out") and
// because it is the only check whose query needs the effective-permission
// traversal.
func assertOrgInvariants(ctx context.Context, tx neo4j.ManagedTransaction) error {
	for _, chk := range orgInvariantChecks {
		res, err := tx.Run(ctx, chk.query, nil)
		if err != nil {
			return err
		}
		if res.Next(ctx) {
			v, _ := res.Record().Get("x")
			return validationErrorf("%s (subject: %v)", chk.msg, v)
		}
		if err := res.Err(); err != nil {
			return err
		}
	}

	admins, err := countEffectiveHolders(ctx, tx, PermOrgAdmin)
	if err != nil {
		return err
	}
	if admins < 1 {
		return validationErrorf(
			"I14: this change would leave zero active :Person holding %s — refusing to lock the organization out",
			PermOrgAdmin)
	}
	return nil
}

// countEffectiveHolders counts active people whose effective(party) contains
// code. Uses the same §7.1 traversal as invariants.go's I14.
func countEffectiveHolders(ctx context.Context, tx neo4j.ManagedTransaction, code string) (int64, error) {
	res, err := tx.Run(ctx, effectiveHoldersCypher+`
		WITH p, effective WHERE $code IN effective
		RETURN count(p) AS n
	`, map[string]any{"code": code})
	if err != nil {
		return 0, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return 0, err
	}
	nRaw, _ := rec.Get("n")
	n, _ := nRaw.(int64)
	return n, nil
}

// effectiveHolderIDs returns the ids of active people effectively holding code.
func effectiveHolderIDs(ctx context.Context, tx neo4j.ManagedTransaction, code string) ([]string, error) {
	res, err := tx.Run(ctx, effectiveHoldersCypher+`
		WITH p, effective WHERE $code IN effective
		RETURN p.id AS id
	`, map[string]any{"code": code})
	if err != nil {
		return nil, err
	}
	var out []string
	for res.Next(ctx) {
		v, _ := res.Record().Get("id")
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out, res.Err()
}

// partyHolds reports whether a single party effectively holds code right now.
func partyHolds(ctx context.Context, tx neo4j.ManagedTransaction, partyID, code string) (bool, error) {
	ids, err := effectiveHolderIDs(ctx, tx, code)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if id == partyID {
			return true, nil
		}
	}
	return false, nil
}

// notifyOrgAdmins is DG5's graph half — "억제 불가능한 가시성".
//
// 60 §21.1 DG5: a role change, deactivation or ticket issuance TARGETING an
// org.admin holder must produce a :Notification{severity:"critical"} for every
// OTHER org.admin, and must not happen at all if that cannot be published
// (fail-closed).
//
// It runs INSIDE the mutating transaction, which is stronger than the
// publish-then-write ordering MP6 uses for machine principals: there is no
// window in which the change is committed and the notification is not, in
// EITHER direction. The `security.rbac.*` event half is osiris-api's, published
// before it calls core, and is fail-closed there for the same reason.
//
// The actor is excluded (they know what they did) but every other admin is
// included, including the target if the target is an admin — being demoted is
// exactly the thing you should be told about.
func notifyOrgAdmins(ctx context.Context, tx neo4j.ManagedTransaction, actorPartyID, kind, summary string, revision int64) error {
	recipients, err := effectiveHolderIDs(ctx, tx, PermOrgAdmin)
	if err != nil {
		return err
	}
	res, err := tx.Run(ctx, `MATCH (n:Notification) RETURN coalesce(max(n.seq), 0) AS s`, nil)
	if err != nil {
		return err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return err
	}
	sRaw, _ := rec.Get("s")
	seq, _ := sRaw.(int64)

	for _, pid := range recipients {
		if pid == actorPartyID {
			continue
		}
		seq++
		if _, err := tx.Run(ctx, `
			MATCH (p:Party {id: $pid})
			CREATE (n:Notification {
				id: $id, kind: $kind, seq: $seq, createdAt: datetime(),
				readAt: null, deepLink: "/people", severity: "critical",
				summary: $summary, revision: $revision
			})
			CREATE (n)-[:FOR_PARTY]->(p)
		`, map[string]any{
			"pid": pid, "kind": kind, "seq": seq,
			"id":       fmt.Sprintf("ntf-%s-%d-%d", kind, revision, seq),
			"summary":  summary,
			"revision": revision,
		}); err != nil {
			return err
		}
	}
	return nil
}

// --- AssignOrgRole (§20.6 [2] and [5]) --------------------------------------

type OrgRoleInput struct {
	MembershipID string
	PersonID     string
	RoleID       string
	ByPartyID    string
}

// AssignOrgRole grants a :Role{scope:'org'} to a :Person via an IN_ORG
// membership, closing whatever org role they held before.
//
// §20.6 [5] is explicit that a role CHANGE is "close and create", not "edit":
// setting validTo on the old membership and creating a new one leaves "was a
// manager from 2026-07-01 to 2026-09-30" permanently in the graph, which is
// what P4 (never delete) and Q8 (reproduce the org as of revision N) both
// require. An UPDATE of :Membership.roleId would satisfy neither, and would
// also make I16 trivially true for the wrong reason.
//
// Closing the old membership in the SAME transaction is also what makes I16
// ("at most one valid IN_ORG membership") hold by construction rather than by
// the caller remembering to revoke first.
func (c *Client) AssignOrgRole(ctx context.Context, in OrgRoleInput) (WriteResult, error) {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		// C9: :Person only, and active. A closed person must not be given a
		// new role — that would resurrect them into effective(party) counting
		// without reopening the account.
		check, err := tx.Run(ctx, `MATCH (p:Person {id: $id}) WHERE p.closedAtRevision IS NULL RETURN p.id`,
			map[string]any{"id": in.PersonID})
		if err != nil {
			return nil, err
		}
		if _, err := check.Single(ctx); err != nil {
			return nil, validationErrorf("active :Person %q not found (C9: IN_ORG membership requires a :Person)", in.PersonID)
		}

		roleCheck, err := tx.Run(ctx, `MATCH (r:Role {id: $id}) WHERE r.scope = 'org' RETURN r.id`,
			map[string]any{"id": in.RoleID})
		if err != nil {
			return nil, err
		}
		if _, err := roleCheck.Single(ctx); err != nil {
			return nil, validationErrorf("org-scoped :Role %q not found (I17: IN_ORG memberships point at scope 'org' roles)", in.RoleID)
		}

		wasAdmin, err := partyHolds(ctx, tx, in.PersonID, PermOrgAdmin)
		if err != nil {
			return nil, err
		}

		n, err := bumpRevision(ctx, tx, in.ByPartyID,
			fmt.Sprintf("org role %s assigned to %s", in.RoleID, in.PersonID), "org_role_assigned")
		if err != nil {
			return nil, err
		}

		if _, err := tx.Run(ctx, `
			MATCH (p:Person {id: $personId})-[:HAS_MEMBERSHIP]->(m:Membership)-[:IN_ORG]->(:Organization)
			WHERE m.validTo IS NULL
			SET m.validTo = date(), m.closedAtRevision = $rev
		`, map[string]any{"personId": in.PersonID, "rev": n}); err != nil {
			return nil, err
		}

		if _, err := tx.Run(ctx, `
			MATCH (p:Person {id: $personId}), (o:Organization {id: "org-root"}), (r:Role {id: $roleId})
			CREATE (m:Membership {
				id: $id, validFrom: date(), validTo: null,
				createdAtRevision: $rev, closedAtRevision: null,
				isHead: null, allocationPct: 0
			})
			CREATE (p)-[:HAS_MEMBERSHIP]->(m)
			CREATE (m)-[:IN_ORG]->(o)
			CREATE (m)-[:AS_ROLE]->(r)
		`, map[string]any{
			"id": in.MembershipID, "personId": in.PersonID, "roleId": in.RoleID, "rev": n,
		}); err != nil {
			return nil, err
		}

		isAdmin, err := partyHolds(ctx, tx, in.PersonID, PermOrgAdmin)
		if err != nil {
			return nil, err
		}
		if wasAdmin || isAdmin {
			if err := notifyOrgAdmins(ctx, tx, in.ByPartyID, "rbac.role_changed",
				fmt.Sprintf("org role of %s changed to %s", in.PersonID, in.RoleID), n); err != nil {
				return nil, err
			}
		}

		if err := assertOrgInvariants(ctx, tx); err != nil {
			return nil, err
		}
		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.MembershipID}, nil
}

// --- RevokeOrgRole (§20.6 [5], the "close without replacement" half) --------

type RevokeOrgRoleInput struct {
	PersonID  string
	ByPartyID string
}

func (c *Client) RevokeOrgRole(ctx context.Context, in RevokeOrgRoleInput) (WriteResult, error) {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		check, err := tx.Run(ctx, `
			MATCH (p:Person {id: $id})-[:HAS_MEMBERSHIP]->(m:Membership)-[:IN_ORG]->(:Organization)
			WHERE m.validTo IS NULL
			RETURN m.id AS mid
		`, map[string]any{"id": in.PersonID})
		if err != nil {
			return nil, err
		}
		if _, err := check.Single(ctx); err != nil {
			return nil, validationErrorf("person %q holds no valid org role", in.PersonID)
		}

		wasAdmin, err := partyHolds(ctx, tx, in.PersonID, PermOrgAdmin)
		if err != nil {
			return nil, err
		}

		n, err := bumpRevision(ctx, tx, in.ByPartyID,
			fmt.Sprintf("org role of %s revoked", in.PersonID), "org_role_revoked")
		if err != nil {
			return nil, err
		}
		if _, err := tx.Run(ctx, `
			MATCH (p:Person {id: $id})-[:HAS_MEMBERSHIP]->(m:Membership)-[:IN_ORG]->(:Organization)
			WHERE m.validTo IS NULL
			SET m.validTo = date(), m.closedAtRevision = $rev
		`, map[string]any{"id": in.PersonID, "rev": n}); err != nil {
			return nil, err
		}
		if wasAdmin {
			if err := notifyOrgAdmins(ctx, tx, in.ByPartyID, "rbac.role_revoked",
				fmt.Sprintf("org role of %s revoked", in.PersonID), n); err != nil {
				return nil, err
			}
		}
		if err := assertOrgInvariants(ctx, tx); err != nil {
			return nil, err
		}
		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.PersonID}, nil
}

// --- DeactivatePerson (§20.6 [6]) -------------------------------------------

type DeactivatePersonInput struct {
	PersonID  string
	ByPartyID string
}

// DeactivatePerson sets :Person.closedAtRevision and closes every valid
// membership — the offboarding shape CloseAIAgent already established, applied
// to a human.
//
// It DELETES NOTHING (P4). The credential side of offboarding — revoking
// AUTHN_CRED / AUTHN_SESSION / unredeemed AUTHN_TICKET / org-class machine
// principals the person issued — is osiris-api's, reacting to org.event.* .
// §14.1's arrow only points one way: the graph is the truth and the KV
// follows.
func (c *Client) DeactivatePerson(ctx context.Context, in DeactivatePersonInput) (WriteResult, error) {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		check, err := tx.Run(ctx, `MATCH (p:Person {id: $id}) WHERE p.closedAtRevision IS NULL RETURN p.id`,
			map[string]any{"id": in.PersonID})
		if err != nil {
			return nil, err
		}
		if _, err := check.Single(ctx); err != nil {
			return nil, validationErrorf("active :Person %q not found or already deactivated", in.PersonID)
		}

		wasAdmin, err := partyHolds(ctx, tx, in.PersonID, PermOrgAdmin)
		if err != nil {
			return nil, err
		}

		n, err := bumpRevision(ctx, tx, in.ByPartyID,
			fmt.Sprintf("person %s deactivated", in.PersonID), "person_deactivated")
		if err != nil {
			return nil, err
		}
		if _, err := tx.Run(ctx, `
			MATCH (p:Person {id: $id})
			SET p.closedAtRevision = $rev
			WITH p
			OPTIONAL MATCH (p)-[:HAS_MEMBERSHIP]->(m:Membership) WHERE m.validTo IS NULL
			SET m.validTo = date(), m.closedAtRevision = $rev
		`, map[string]any{"id": in.PersonID, "rev": n}); err != nil {
			return nil, err
		}
		if wasAdmin {
			if err := notifyOrgAdmins(ctx, tx, in.ByPartyID, "rbac.person_deactivated",
				fmt.Sprintf("person %s deactivated", in.PersonID), n); err != nil {
				return nil, err
			}
		}
		if err := assertOrgInvariants(ctx, tx); err != nil {
			return nil, err
		}
		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.PersonID}, nil
}

// --- HAS_PROFILE{additive} / DENIES (§21.4-2, C10) --------------------------
//
// These two exist because of the bypass 60 §21.4-2 names: "deny always wins"
// (20 §7.1), so attaching DENIES org.admin to an administrator neutralizes
// them without touching a single role. If that edge could be written outside
// the delegation rules, DG1~DG4 would all be bypassable through one hole.
// C10 therefore lists HAS_PROFILE/DENIES manipulation alongside role changes,
// and osiris-api routes both through the same authz.CheckDelegation call.

type PartyGrantInput struct {
	PartyID   string
	ProfileID string // for SetAdditiveProfile
	PermCode  string // for SetDeny
	Attach    bool   // true = add the edge, false = remove it
	ByPartyID string
}

func (c *Client) SetAdditiveProfile(ctx context.Context, in PartyGrantInput) (WriteResult, error) {
	return c.mutatePartyGrant(ctx, in, "additive_profile",
		`MATCH (p:Party {id: $partyId}), (pp:PermissionProfile {id: $ref})
		 MERGE (p)-[:HAS_PROFILE {kind: 'additive'}]->(pp)`,
		`MATCH (p:Party {id: $partyId})-[h:HAS_PROFILE {kind: 'additive'}]->(pp:PermissionProfile {id: $ref})
		 DELETE h`,
		`MATCH (pp:PermissionProfile {id: $ref}) RETURN pp.id`,
		in.ProfileID)
}

func (c *Client) SetDeny(ctx context.Context, in PartyGrantInput) (WriteResult, error) {
	return c.mutatePartyGrant(ctx, in, "deny",
		`MATCH (p:Party {id: $partyId}), (perm:Permission {code: $ref})
		 MERGE (p)-[:DENIES]->(perm)`,
		`MATCH (p:Party {id: $partyId})-[d:DENIES]->(perm:Permission {code: $ref})
		 DELETE d`,
		`MATCH (perm:Permission {code: $ref}) RETURN perm.code`,
		in.PermCode)
}

// humanOnlyPermissionCodes is this package's OWN copy of 80 §5.1.3's P1-Q
// closed set (also named api/internal/authz/humanonly.go's HumanOnly) —
// duplicated, not imported, because osiris-core and osiris-api are separate
// Go modules with no import path between them.
//
// THIS IS NOT I9's SHAPE, and an earlier version of this comment wrongly
// said it was. I9's two checkers (core's checkI9_PermissionCatalogVersion,
// orchestrator's CheckCatalogVersion) each compare their OWN value against a
// COMMON THIRD REFERENT — the graph's own :Permission.catalogVersion — so
// there is always a shared source of truth neither side controls. This pair
// has no third referent at all: it is a direct, two-file comparison of one
// hardcoded literal against another, enforced by bin/lint-action-layer.sh's
// AC6 section reading BOTH files' source text and checking SET EQUALITY
// (not just matching counts — see that script's "sub-check (ii)" for why a
// count-only check previously shipped here and passed while the two copies
// could still have silently disagreed on which four codes they were).
//
// If you add, remove, or rename a code in EITHER this map or
// api/internal/authz/humanonly.go's HumanOnly, you must make the identical
// change in the other in the SAME edit — the lint will fail otherwise, and
// until an osiris-api route actually calls CheckDelegation's DG6 branch
// (see delegation.go's package doc comment), THIS map is the only DG6
// defense wired into a live write path (mutatePartyGrant, below), so a
// drift here is not cosmetic.
//
// This is the SAME style CreateAIAgent already uses for cap.exec.physical
// (I5) a few hundred lines above — a hardcoded, closed, single-purpose
// denylist next to the one write path it guards, rather than a shared
// "permission taxonomy" package neither binary needs.
var humanOnlyPermissionCodes = map[string]struct{}{
	"org.admin":         {},
	"org.manage.people": {},
	"org.manage.roles":  {},
	"actiontype.write":  {},
}

// partyHasLabel reports whether label appears in a Cypher labels(n) result
// (which the driver hands back as []any of strings).
func partyHasLabel(labelsRaw any, label string) bool {
	labels, _ := labelsRaw.([]any)
	for _, l := range labels {
		if s, ok := l.(string); ok && s == label {
			return true
		}
	}
	return false
}

func (c *Client) mutatePartyGrant(ctx context.Context, in PartyGrantInput, kind, attachQ, detachQ, refCheckQ, ref string) (WriteResult, error) {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		if ref == "" {
			return nil, validationErrorf("%s reference is required", kind)
		}
		// DG6's structural precondition (80 §5.1.2, this task's item 5): we
		// need the TARGET PARTY'S KIND, not just its existence, so that a
		// HumanOnly code can be refused for an :AIAgent target regardless of
		// what any caller ABOVE this layer already checked. labels(p) is
		// returned alongside the existence check rather than in a second
		// query — one round trip, and there is no window in which the kind
		// could be read stale relative to the existence check it is part of.
		partyCheck, err := tx.Run(ctx, `MATCH (p:Party {id: $id}) WHERE p.closedAtRevision IS NULL RETURN p.id, labels(p) AS labels`,
			map[string]any{"id": in.PartyID})
		if err != nil {
			return nil, err
		}
		partyRec, err := partyCheck.Single(ctx)
		if err != nil {
			return nil, validationErrorf("active :Party %q not found", in.PartyID)
		}
		labelsRaw, _ := partyRec.Get("labels")
		targetIsAIAgent := partyHasLabel(labelsRaw, "AIAgent")

		refCheck, err := tx.Run(ctx, refCheckQ, map[string]any{"ref": ref})
		if err != nil {
			return nil, err
		}
		if _, err := refCheck.Single(ctx); err != nil {
			return nil, validationErrorf("%s reference %q not found", kind, ref)
		}

		// DG6 enforcement, STRUCTURAL not incidental (this task's item 5's
		// whole point — see mutatePartyGrant's package-level doc note this
		// replaces): refuse a HumanOnly code from reaching an :AIAgent no
		// matter which of the two grant shapes carries it.
		//
		//   - SetDeny(Attach=false) HANDS BACK the denied code (the same
		//     inversion api/internal/people/handlers.go's SetDeny handler
		//     comments on for DG1/DG1′) — so it is a grant in every sense
		//     that matters and is checked directly against `ref`, which for
		//     this kind IS the permission code.
		//   - SetAdditiveProfile(Attach=true) grants whatever the profile
		//     itself GRANTS, which this function does not otherwise ever
		//     read — so it runs one extra query, ONLY when the target is an
		//     :AIAgent, to ask whether that profile contains a HumanOnly
		//     code.
		//
		// Attaching a deny, and detaching a profile, both REMOVE capability
		// and are never a DG6 concern.
		if targetIsAIAgent {
			if kind == "deny" && !in.Attach {
				if _, humanOnly := humanOnlyPermissionCodes[ref]; humanOnly {
					return nil, validationErrorf("DG6 (80 §5.1.2): %q is a HumanOnly permission code and may not be restored to :AIAgent %q by removing a DENIES edge", ref, in.PartyID)
				}
			}
			if kind == "additive_profile" && in.Attach {
				grantCheck, err := tx.Run(ctx, `
					MATCH (pp:PermissionProfile {id: $ref})-[:GRANTS]->(perm:Permission)
					RETURN collect(perm.code) AS codes
				`, map[string]any{"ref": ref})
				if err != nil {
					return nil, err
				}
				grantRec, err := grantCheck.Single(ctx)
				if err != nil {
					return nil, err
				}
				codesRaw, _ := grantRec.Get("codes")
				codes, _ := codesRaw.([]any)
				for _, cRaw := range codes {
					code, _ := cRaw.(string)
					if _, humanOnly := humanOnlyPermissionCodes[code]; humanOnly {
						return nil, validationErrorf("DG6 (80 §5.1.2): permission profile %q grants HumanOnly code %q and may not be attached to :AIAgent %q", ref, code, in.PartyID)
					}
				}
			}
		}

		wasAdmin, err := partyHolds(ctx, tx, in.PartyID, PermOrgAdmin)
		if err != nil {
			return nil, err
		}

		verb := "detached from"
		if in.Attach {
			verb = "attached to"
		}
		n, err := bumpRevision(ctx, tx, in.ByPartyID,
			fmt.Sprintf("%s %s %s %s", kind, ref, verb, in.PartyID), kind+"_changed")
		if err != nil {
			return nil, err
		}

		q := detachQ
		if in.Attach {
			q = attachQ
		}
		if _, err := tx.Run(ctx, q, map[string]any{"partyId": in.PartyID, "ref": ref}); err != nil {
			return nil, err
		}

		isAdmin, err := partyHolds(ctx, tx, in.PartyID, PermOrgAdmin)
		if err != nil {
			return nil, err
		}
		if wasAdmin || isAdmin {
			if err := notifyOrgAdmins(ctx, tx, in.ByPartyID, "rbac."+kind+"_changed",
				fmt.Sprintf("%s %s %s %s", kind, ref, verb, in.PartyID), n); err != nil {
				return nil, err
			}
		}
		if err := assertOrgInvariants(ctx, tx); err != nil {
			return nil, err
		}
		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.PartyID}, nil
}

// PartyIsActive reports whether id names a :Party with closedAtRevision IS
// NULL — 60-identity-authn.md §6.5's precondition for any mutating request
// ("actorPartyId가 유효한 :Party가 아니면 core는 mutating 요청을 거부한다").
//
// Read-only, but it lives here rather than in invariants.go because it is part
// of the WRITE path's contract, not part of the org's health report.
func (c *Client) PartyIsActive(ctx context.Context, id string) (bool, error) {
	session := c.NewReadSession(ctx)
	defer session.Close(ctx)
	res, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		r, err := tx.Run(ctx, `MATCH (p:Party {id: $id}) WHERE p.closedAtRevision IS NULL RETURN count(p) AS n`,
			map[string]any{"id": id})
		if err != nil {
			return nil, err
		}
		rec, err := r.Single(ctx)
		if err != nil {
			return nil, err
		}
		n, _ := rec.Get("n")
		return n, nil
	})
	if err != nil {
		return false, err
	}
	n, _ := res.(int64)
	return n > 0, nil
}
