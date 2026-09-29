package graphdb

// assign_persist.go is 20-ontology-schema.md §8.7's `persistDecision(task,
// evals, policy, rev, outcome, selected)` plus the :Assignment CREATE and
// :WorkItem.status update §8.7's closing line requires to happen in the
// SAME transaction ("단일 트랜잭션으로 :AssignmentDecision + :Assignment +
// :WorkItem.status 변경을 커밋한다").
//
// C5 (append-only :AssignmentDecision/[:CONSIDERED], 20 §11) is enforced
// structurally, not by a runtime guard: every :AssignmentDecision and every
// [:CONSIDERED] edge this file writes is created with a fresh, newly
// generated id via CREATE — never MERGE, never MATCH+SET on an existing
// :AssignmentDecision. There is no code path anywhere in this file (or
// anywhere else in this package, grep confirms it) that matches an existing
// :AssignmentDecision node and writes to it. Append-only therefore holds by
// construction: the only way to make one of these nodes "different" is to
// call PersistDecision again, which creates an ENTIRELY NEW node.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"osiris-core/internal/assign"
)

// PersistDecisionInput is §8.7's persistDecision(...) argument list.
// SelectedPartyID is "" for every outcome except assigned/degraded (20 §3.5
// line 301: "outcome=assigned/degraded일 때만"). When SelectedPartyID != "",
// CreateAssignment must be true if this decision should also produce a new
// :Assignment (the normal case); CreateAssignment is false for FB1's
// "call persistDecision but don't dispatch until approval" — this build,
// however, does not create an :Assignment on any non-assigned outcome (see
// AssignTx's doc comment for how DEGRADED is actually produced without an
// append-only violation).
type PersistDecisionInput struct {
	TaskID          string
	Evaluations     []assign.Evaluation // ALL evaluations, eligible and excluded (§8.2's "탈락자 포함 전원")
	Policy          assign.Policy
	OrgRevision     int64
	Outcome         assign.Outcome
	FallbackReason  string
	SelectedPartyID string // "" unless outcome is assigned/degraded
	RequiredCaps    []assign.CapabilityRequirement

	// CreateAssignment, when true and SelectedPartyID != "", also creates a
	// new :Assignment{status:'active'} node, [:OF_TASK]->WorkItem,
	// [:TO]->winner, and [:PRODUCED] from the decision, and sets
	// WorkItem.status = 'assigned' — all in this same transaction (20 §8.7's
	// closing line). AssigneeDueAt/AssigneeTaskPoints feed :Assignment's own
	// dueAt/taskPoints (20 §3.5 lines 278-281).
	CreateAssignment bool
	AssigneeDueAt    time.Time
	TaskPoints       int
}

// PersistDecisionResult is what the caller needs back: the new decision id
// and (if one was created) the new assignment id.
type PersistDecisionResult struct {
	DecisionID   string
	AssignmentID string // "" if CreateAssignment was false
}

// PersistDecision is §8.7. Must be called inside an already-open
// transaction — it does NOT bump :OrgRevision (assign() never does; see
// AssignTx's doc comment) and it does NOT open its own session, so it can
// run inside the caller's transaction whether that's a standalone
// AssignTx-wrapping session (the normal runtime path) or an onboarding
// commit's still-open, not-yet-committed transaction (the rehearsal path,
// 20 §9.2 step 14 / 30 §5.2).
func PersistDecision(ctx context.Context, tx neo4j.ManagedTransaction, in PersistDecisionInput) (PersistDecisionResult, error) {
	decisionID := "decision-" + uuid.NewString()

	reqCapsJSON, err := json.Marshal(in.RequiredCaps)
	if err != nil {
		return PersistDecisionResult{}, fmt.Errorf("marshal requiredCapsJson: %w", err)
	}
	tieBreakJSON, err := json.Marshal(in.Policy.TieBreakChain)
	if err != nil {
		return PersistDecisionResult{}, fmt.Errorf("marshal tieBreakChainJson: %w", err)
	}

	_, err = tx.Run(ctx, `
		MATCH (w:WorkItem {id: $taskId})
		MATCH (policy:AssignmentPolicy {version: $policyVersion})
		CREATE (d:AssignmentDecision {
			id: $id, decidedAt: datetime(),
			orgRevision: $orgRevision, policyVersion: $policyVersion,
			algorithmVersion: $algorithmVersion,
			requiredCapsJson: $requiredCapsJson, tieBreakChainJson: $tieBreakChainJson,
			outcome: $outcome, fallbackReason: $fallbackReason
		})
		CREATE (d)-[:FOR]->(w)
		CREATE (d)-[:USED_POLICY]->(policy)
	`, map[string]any{
		"taskId": in.TaskID, "id": decisionID,
		"orgRevision": in.OrgRevision, "policyVersion": in.Policy.Version,
		"algorithmVersion": assign.AlgorithmVersion,
		"requiredCapsJson": string(reqCapsJSON), "tieBreakChainJson": string(tieBreakJSON),
		"outcome": string(in.Outcome), "fallbackReason": in.FallbackReason,
	})
	if err != nil {
		return PersistDecisionResult{}, err
	}

	// CONSIDERED — every evaluation, eligible and excluded, deterministically
	// ordered by party id so the write itself (and any test asserting on
	// query order) is reproducible.
	sortedEvals := append([]assign.Evaluation(nil), in.Evaluations...)
	sortEvaluationsByPartyID(sortedEvals)
	for _, e := range sortedEvals {
		var rankParam any
		if e.Rank > 0 {
			rankParam = int64(e.Rank)
		}
		_, err = tx.Run(ctx, `
			MATCH (d:AssignmentDecision {id: $decisionId})
			MATCH (party:Party {id: $partyId})
			CREATE (d)-[:CONSIDERED {
				rank: $rank, verdict: $verdict,
				exclusionReasons: $exclusionReasons,
				matchedCapabilities: $matchedCapabilities,
				missingCapabilities: $missingCapabilities,
				specificityScore: $specificityScore,
				loadAtDecision: $loadAtDecision,
				sameTeam: $sameTeam, contextContinuity: $contextContinuity,
				partyKindAtDecision: $partyKindAtDecision,
				displayNameAtDecision: $displayNameAtDecision
			}]->(party)
		`, map[string]any{
			"decisionId": decisionID, "partyId": e.PartyID,
			"rank": rankParam, "verdict": string(e.Verdict),
			"exclusionReasons": e.ExclusionReasons, "matchedCapabilities": e.MatchedCapabilities,
			"missingCapabilities": e.MissingCapabilities, "specificityScore": e.SpecificityScore,
			"loadAtDecision": e.LoadAtDecision, "sameTeam": e.SameTeam,
			"contextContinuity": e.ContextContinuity, "partyKindAtDecision": string(e.PartyKind),
			"displayNameAtDecision": e.DisplayName,
		})
		if err != nil {
			return PersistDecisionResult{}, err
		}
	}

	result := PersistDecisionResult{DecisionID: decisionID}

	if in.SelectedPartyID != "" {
		_, err = tx.Run(ctx, `
			MATCH (d:AssignmentDecision {id: $decisionId})
			MATCH (party:Party {id: $partyId})
			CREATE (d)-[:SELECTED]->(party)
		`, map[string]any{"decisionId": decisionID, "partyId": in.SelectedPartyID})
		if err != nil {
			return PersistDecisionResult{}, err
		}

		if in.CreateAssignment {
			assignmentID := "assignment-" + uuid.NewString()
			var dueAtParam any
			if !in.AssigneeDueAt.IsZero() {
				dueAtParam = in.AssigneeDueAt
			}
			_, err = tx.Run(ctx, `
				MATCH (d:AssignmentDecision {id: $decisionId})
				MATCH (w:WorkItem {id: $taskId})
				MATCH (party:Party {id: $partyId})
				CREATE (a:Assignment {
					id: $assignmentId, assignedAt: datetime(),
					status: 'active', dueAt: $dueAt, taskPoints: $taskPoints
				})
				CREATE (a)-[:OF_TASK]->(w)
				CREATE (a)-[:TO]->(party)
				CREATE (d)-[:PRODUCED]->(a)
				SET w.status = 'assigned'
			`, map[string]any{
				"decisionId": decisionID, "taskId": in.TaskID, "partyId": in.SelectedPartyID,
				"assignmentId": assignmentID, "dueAt": dueAtParam, "taskPoints": in.TaskPoints,
			})
			if err != nil {
				return PersistDecisionResult{}, err
			}
			result.AssignmentID = assignmentID
		}
	}

	return result, nil
}

func sortEvaluationsByPartyID(evals []assign.Evaluation) {
	// Small, fixed-size in practice (organization scale) — insertion sort is
	// simple and avoids importing sort just for this; correctness matters
	// more than asymptotic performance here.
	for i := 1; i < len(evals); i++ {
		for j := i; j > 0 && evals[j].PartyID < evals[j-1].PartyID; j-- {
			evals[j], evals[j-1] = evals[j-1], evals[j]
		}
	}
}
