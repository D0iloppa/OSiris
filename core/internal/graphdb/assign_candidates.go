package graphdb

// assign_candidates.go is 20-ontology-schema.md §8.3's `candidateSet(task,
// policy)` PLUS the per-candidate data gathering §8.4/§8.5 need — the
// "graph query" third of this task's three-way split. Nothing in this file
// makes an assignment DECISION; it only turns graph state into
// assign.Candidate values for assign.Evaluate/assign.Comparator (pure) to
// consume.
//
// Design note on N+1 queries: the base candidate query (§8.3, one round
// trip) is followed by FOUR small per-candidate queries (capability
// matches, effective permissions, active/next-free load, context
// continuity). This trades some round trips for queries that are each
// obviously correct and independently testable in isolation, which matters
// more than raw query count at the scale §8.2 itself names ("후보 수는 조직
// 규모(수십)이므로 비용은 무시할 만하다").

import (
	"context"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"osiris-core/internal/assign"
)

// runtimeStaleAfter is 20 §8.1 line 618's "stale(>60s)이면 ready=false 취급".
const runtimeStaleAfter = 60 * time.Second

// CandidateSet is 20 §8.3's `candidateSet(task, policy)`, decorated with
// every raw fact needed by assign.Evaluate/assign.Comparator (F1-F5,
// tie-break). Must be called inside an already-open transaction.
//
// The base MATCH is transcribed verbatim from §8.3 (including the
// `[2026-07-27]` IN_TEAM lock discussed there — org-only members, e.g. an
// admin with no team membership, are never candidates, by design, HI20).
func CandidateSet(ctx context.Context, tx neo4j.ManagedTransaction, task assign.Task, policy assign.Policy) ([]assign.Candidate, error) {
	params := map[string]any{}
	teamFilter := ""
	if policy.TeamStrict && task.OwningTeamID != "" {
		teamFilter = "AND t.id = $owningTeamId"
		params["owningTeamId"] = task.OwningTeamID
	}

	res, err := tx.Run(ctx, `
		MATCH (p:Party)-[:HAS_MEMBERSHIP]->(m:Membership)-[:IN_TEAM]->(t:Team)
		WHERE m.validTo IS NULL AND p.closedAtRevision IS NULL
		  AND (p:Person OR p:AIAgent)
		  `+teamFilter+`
		OPTIONAL MATCH (p)-[:RUNTIME]->(rt:AgentRuntime)
		RETURN DISTINCT p.id AS partyId, p.name AS name,
		       head([l IN labels(p) WHERE l IN ['Person','AIAgent']]) AS kind,
		       t.id AS teamId,
		       p.availability AS availability, p.leaveFrom AS leaveFrom, p.leaveTo AS leaveTo,
		       p.maxTaskPoints AS maxTaskPoints,
		       rt.ready AS ready, rt.phase AS phase, rt.observedAt AS observedAt
		ORDER BY partyId ASC
	`, params)
	if err != nil {
		return nil, err
	}
	records, err := res.Collect(ctx)
	if err != nil {
		return nil, err
	}

	reqCodes := make([]string, len(task.RequiredCapabilities))
	for i, r := range task.RequiredCapabilities {
		reqCodes[i] = r.Code
	}

	now := time.Now()
	candidates := make([]assign.Candidate, 0, len(records))
	for _, rec := range records {
		partyID := stringOfAny(getOr(rec, "partyId"))
		kind := assign.PartyKind(stringOfAny(getOr(rec, "kind")))

		c := assign.Candidate{
			PartyID:              partyID,
			Name:                 stringOfAny(getOr(rec, "name")),
			Kind:                 kind,
			TeamID:               stringOfAny(getOr(rec, "teamId")),
			Availability:         stringOfAny(getOr(rec, "availability")),
			LeaveFrom:            timePtrOf(getOr(rec, "leaveFrom")),
			LeaveTo:              timePtrOf(getOr(rec, "leaveTo")),
			MaxTaskPoints:        intOfAny(getOr(rec, "maxTaskPoints")),
			RuntimePhase:         stringOfAny(getOr(rec, "phase")),
			EffectivePermissions: map[string]bool{},
			CapabilityMatches:    map[string]assign.CapabilityMatch{},

			// F6 (assign.Evaluate's BUDGET_EXHAUSTED check): 20 §3.1 line 120
			// gives :Team a monthlyBudgetUsd CAP, but no property anywhere in
			// the schema tracks actual spend against it — 20 §12 explicitly
			// excludes cost time-series from the graph (P6, JetStream's
			// territory). There is nothing to query here, so this is always
			// false ("not exhausted"). See assign.Evaluate's F6 comment for
			// why "no data" must resolve to "not exhausted" rather than the
			// reverse (an :AIAgent-only filter defaulting to "exhausted"
			// would silently exclude every AI candidate from every task).
			BudgetExhausted: false,
		}

		observedAt := timeOf(getOr(rec, "observedAt"))
		rawReady := boolOfAny(getOr(rec, "ready"))
		if observedAt.IsZero() || now.Sub(observedAt) > runtimeStaleAfter {
			c.Ready = false // no runtime row, or stale (§8.1 line 618)
		} else {
			c.Ready = rawReady
		}

		matches, err := loadCapabilityMatches(ctx, tx, partyID, reqCodes)
		if err != nil {
			return nil, err
		}
		c.CapabilityMatches = matches
		c.VerificationRecency = assign.MinVerifiedAt(c, task.RequiredCapabilities)

		perms, err := loadEffectivePermissions(ctx, tx, partyID)
		if err != nil {
			return nil, err
		}
		c.EffectivePermissions = perms

		activePoints, nextFree, err := loadLoadAndNextFree(ctx, tx, partyID)
		if err != nil {
			return nil, err
		}
		c.ActiveTaskPoints = activePoints
		c.NextFreeAt = nextFree

		worked, err := loadHasWorkedOnAncestor(ctx, tx, task.ID, partyID)
		if err != nil {
			return nil, err
		}
		c.HasWorkedOnAncestor = worked

		candidates = append(candidates, c)
	}
	return candidates, nil
}

// getOr is a small helper so record.Get's (value, ok) pair reads inline
// above without repeating the `_ , _ :=` boilerplate for every column;
// missing columns behave like nil.
func getOr(rec *neo4j.Record, key string) any {
	v, _ := rec.Get(key)
	return v
}

// loadCapabilityMatches is F1's `maxLevel(c HAS_CAPABILITY hc WHERE
// hc-[:BROADER*0..3]->req.code)`, one row per requirement code. For each
// code, the candidate's held capabilities are ordered by level (desc) then
// by "is this the exact code, not a broader ancestor" (exact first) so the
// first row is the same "best" match §8.4's pseudocode picks.
func loadCapabilityMatches(ctx context.Context, tx neo4j.ManagedTransaction, partyID string, reqCodes []string) (map[string]assign.CapabilityMatch, error) {
	out := map[string]assign.CapabilityMatch{}
	if len(reqCodes) == 0 {
		return out, nil
	}
	res, err := tx.Run(ctx, `
		UNWIND $reqCodes AS reqCode
		CALL {
			WITH reqCode
			OPTIONAL MATCH (p:Party {id: $partyId})-[h:HAS_CAPABILITY]->(hc:Capability)-[:BROADER*0..3]->(:Capability {code: reqCode})
			WITH h, hc
			ORDER BY h.level DESC, (CASE WHEN hc.code = reqCode THEN 0 ELSE 1 END) ASC
			RETURN h AS bestH, hc AS bestHc LIMIT 1
		}
		RETURN reqCode, bestH.level AS level, bestHc.code AS matchedCode, bestH.verifiedAt AS verifiedAt
	`, map[string]any{"partyId": partyID, "reqCodes": reqCodes})
	if err != nil {
		return nil, err
	}
	records, err := res.Collect(ctx)
	if err != nil {
		return nil, err
	}
	for _, rec := range records {
		reqCode := stringOfAny(getOr(rec, "reqCode"))
		levelVal := getOr(rec, "level")
		if levelVal == nil {
			out[reqCode] = assign.CapabilityMatch{Exists: false}
			continue
		}
		matchedCode := stringOfAny(getOr(rec, "matchedCode"))
		out[reqCode] = assign.CapabilityMatch{
			Exists:     true,
			Level:      intOfAny(levelVal),
			Exact:      matchedCode == reqCode,
			VerifiedAt: timeOf(getOr(rec, "verifiedAt")),
		}
	}
	return out, nil
}

// loadEffectivePermissions is §7.1's effective(party), scoped to a single
// $partyId and NOT restricted to :Person (unlike core/internal/graphdb/
// invariants.go's effectiveHoldersCypher, which is I14's Person-only check).
// This is a deliberate, bounded duplication of the SAME formula for the SAME
// reason invariants.go's own comment gives: an invariant/filter check needs
// the permission SET, not the authorization DECISION logic (which stays in
// osiris-api, H31). Refactoring invariants.go's constant to be reusable here
// was considered and rejected for this task: it is Person-scoped by design
// (I14 is specifically about :Person org.admin holders) and generalizing it
// risks changing behavior of an already-tested boot invariant for no benefit
// — a second small, obviously-correct copy is safer than a shared one with a
// new parameter threading through unrelated code.
func loadEffectivePermissions(ctx context.Context, tx neo4j.ManagedTransaction, partyID string) (map[string]bool, error) {
	res, err := tx.Run(ctx, `
		MATCH (p:Party {id: $partyId})
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
		WITH baseGrants, baseDenies, addGrants, addDenies, collect(DISTINCT pd.code) AS partyDenies
		WITH baseGrants + addGrants AS granted, baseDenies + addDenies + partyDenies AS denied
		RETURN [x IN granted WHERE NOT x IN denied] AS effective
	`, map[string]any{"partyId": partyID})
	if err != nil {
		return nil, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		// No :Party {id: partyId} at all — return an empty set rather than
		// erroring; F2 will then exclude via NOT_PERMITTED for any
		// requirement, which is the correct fail-closed behavior.
		return map[string]bool{}, nil
	}
	effective := stringsOfAny(getOr(rec, "effective"))
	out := make(map[string]bool, len(effective))
	for _, code := range effective {
		out[code] = true
	}
	return out, nil
}

// loadLoadAndNextFree is F4's `rt.activeTaskPoints` plus FB3's
// `earliestFreeTime` input: the sum of taskPoints across this party's
// active/awaiting_human :Assignment rows, and the earliest dueAt among
// them (nil if none).
func loadLoadAndNextFree(ctx context.Context, tx neo4j.ManagedTransaction, partyID string) (int, *time.Time, error) {
	res, err := tx.Run(ctx, `
		MATCH (a:Assignment)-[:TO]->(p:Party {id: $partyId})
		WHERE a.status IN ['active', 'awaiting_human']
		RETURN sum(a.taskPoints) AS activePoints, min(a.dueAt) AS nextFree
	`, map[string]any{"partyId": partyID})
	if err != nil {
		return 0, nil, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return 0, nil, nil
	}
	return intOfAny(getOr(rec, "activePoints")), timePtrOf(getOr(rec, "nextFree")), nil
}

// loadHasWorkedOnAncestor is §8.5 tie-break stage 4's
// `hasWorkedOnAncestor(c, task)`.
//
// [판단] Neither the traversal depth nor "already performed" is defined by
// 20 §8.4/§8.5. Two choices made here, both toward the CONSERVATIVE
// (harder-to-satisfy, fewer false positives) side, since an unjustified
// context-continuity boost silently changes who wins a tie:
//   - Depth: bounded to PARENT_OF*1..6, matching the existing bound this
//     schema already uses for WBS ancestor/descendant walks (20 §10 Q4's
//     `PARENT_OF*0..6`) — reusing an established convention rather than
//     inventing a new one, and finite rather than unbounded so a
//     pathologically deep WBS cannot make this query unbounded.
//   - "Already performed": an assignment counts UNLESS its status is
//     'revoked'. This is the conservative reading precisely because it is
//     the WEAKER of the two obvious choices (any non-revoked assignment vs.
//     only 'completed' assignments) is actually the one that risks more
//     false positives — so to be conservative we exclude 'revoked' only and
//     accept active/awaiting_human/completed as evidence: a revoked
//     assignment demonstrably did NOT happen (it was undone), so it must
//     not count; anything else means the party genuinely held that work at
//     some point, which is the plain-language meaning of "이미 수행했다" this
//     schema gives no narrower definition for. Requiring strictly
//     'completed' was considered and rejected: it would make
//     contextContinuity nearly always false for in-flight WBS trees (most
//     ancestor tasks in an active project are not yet 'completed'), silently
//     neutering tie-break stage 4 for exactly the case (an ongoing project)
//     it exists to help.
func loadHasWorkedOnAncestor(ctx context.Context, tx neo4j.ManagedTransaction, taskID, partyID string) (bool, error) {
	res, err := tx.Run(ctx, `
		MATCH (w:WorkItem {id: $taskId})<-[:PARENT_OF*1..6]-(ancestor:WorkItem)
		MATCH (a:Assignment)-[:OF_TASK]->(ancestor)
		MATCH (a)-[:TO]->(p:Party {id: $partyId})
		WHERE a.status <> 'revoked'
		RETURN count(a) > 0 AS worked
	`, map[string]any{"taskId": taskID, "partyId": partyID})
	if err != nil {
		return false, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return false, nil
	}
	return boolOfAny(getOr(rec, "worked")), nil
}
