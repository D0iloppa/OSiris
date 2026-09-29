package graphdb

// assign_engine.go is 20-ontology-schema.md §8.2's `assign(task, asOf)` —
// the orchestration that ties CandidateSet (§8.3, assign_candidates.go),
// assign.Evaluate/assign.Comparator/assign.ClassifyFallback (§8.4-§8.6, the
// pure osiris-core/internal/assign package), and PersistDecision (§8.7,
// assign_persist.go) together exactly as the pseudocode sequences them.
//
// REVISION DISCIPLINE (re-stated here because it is easy to get backwards):
// assign() reads :OrgRevision (currentOrgRevisionTx) but never calls
// bumpRevision. §8.2's pseudocode has no revision-bump call, and the design
// reason is explicit: assignment is a CONSEQUENCE of organizational intent
// (who exists, what they can do, what's due), not a CHANGE to it. Only
// CreateWorkItem (a genuinely new unit of intended work, assign_workitem.go)
// bumps the revision in this file's neighborhood.
//
// TRANSACTION SHARING: every exported function here takes a
// neo4j.ManagedTransaction rather than opening its own session. This is
// what lets 30-onboarding-provisioning.md §5.2's rehearsal (step 14, 20
// §9.2) call the EXACT SAME assign() Go code inside the onboarding commit's
// still-open, not-yet-committed transaction, and roll the whole thing back
// together if anything fails — no separate simulation function, matching
// that section's explicit requirement ("별도 시뮬레이션 함수를 만들지
// 않는다"). (*Client).Assign below is the convenience wrapper for the
// normal runtime path (assign() called on its own, not nested inside
// another transaction), and is exactly the "P4가 실제로 배선한다" entry
// point for the non-rehearsal case.
import (
	"context"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"osiris-core/internal/assign"
)

// Result is what a completed assign() call reports back — enough for a
// caller (P4's commit endpoint, the rehearsal, or a future dispatch worker)
// to know what happened without re-deriving it from the graph.
type Result struct {
	Outcome      assign.Outcome
	DecisionID   string
	AssignmentID string     // "" unless Outcome is assigned or degraded
	SelectedID   string     // "" unless Outcome is assigned or degraded
	Cover        []string   // FB2 decompose_required: the suggested party-id group
	EscalateETA  *time.Time // FB3 escalated_lead: earliest known free time, if any
}

// AssignTx is §8.2's assign(task, asOf), operating inside the caller's
// already-open transaction. asOf is passed straight to LoadActivePolicy; a
// zero time.Time means "the currently active policy, no as-of filtering".
func AssignTx(ctx context.Context, tx neo4j.ManagedTransaction, task assign.Task, asOf time.Time) (Result, error) {
	policy, err := LoadActivePolicy(ctx, tx, asOf)
	if err != nil {
		return Result{}, err
	}
	rev, err := currentOrgRevisionTx(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	return assignWithPolicy(ctx, tx, task, policy, rev)
}

// assignWithPolicy is AssignTx's body, factored out so FB1 (level
// relaxation) can re-run candidateSet+evaluate against a RELAXED task
// without recursing into a second LoadActivePolicy/currentOrgRevisionTx
// round trip (the policy and revision do not change mid-decision).
func assignWithPolicy(ctx context.Context, tx neo4j.ManagedTransaction, task assign.Task, policy assign.Policy, rev int64) (Result, error) {
	cands, err := CandidateSet(ctx, tx, task, policy)
	if err != nil {
		return Result{}, err
	}

	evals := make([]assign.Evaluation, len(cands))
	for i, c := range cands {
		evals[i] = assign.Evaluate(c, task, policy)
	}

	ranked := assign.RankEligible(evals, policy)
	if len(ranked) > 0 {
		return persistAssigned(ctx, tx, task, evals, ranked, policy, rev, assign.OutcomeAssigned)
	}

	return runFallback(ctx, tx, task, cands, evals, policy, rev)
}

// persistAssigned is §8.2's happy path (lines 643-647): pick ranked[0],
// persist the decision (with the FULL evals slice, eligible and excluded —
// §8.2's "탈락자 포함 전원"), but with each eligible evaluation's Rank
// already filled in by RankEligible. outcome is a parameter (not hardcoded
// to ASSIGNED) so FB1's relaxed re-run can share this exact function and
// mark the result DEGRADED instead — see runFallback's FB1 branch for why
// that avoids ever mutating an already-persisted :AssignmentDecision.
func persistAssigned(ctx context.Context, tx neo4j.ManagedTransaction, task assign.Task, allEvals []assign.Evaluation, ranked []assign.Evaluation, policy assign.Policy, rev int64, outcome assign.Outcome) (Result, error) {
	winner := ranked[0]

	// Splice the ranked (with Rank filled in) evaluations back into allEvals
	// so persisted CONSIDERED edges carry the real rank, not the
	// zero-value Rank every entry in allEvals started with.
	merged := mergeRanked(allEvals, ranked)

	fallbackReason := ""
	if outcome == assign.OutcomeDegraded {
		fallbackReason = "LEVEL_RELAXED"
	}

	res, err := PersistDecision(ctx, tx, PersistDecisionInput{
		TaskID:           task.ID,
		Evaluations:      merged,
		Policy:           policy,
		OrgRevision:      rev,
		Outcome:          outcome,
		FallbackReason:   fallbackReason,
		SelectedPartyID:  winner.PartyID,
		RequiredCaps:     task.RequiredCapabilities,
		CreateAssignment: true,
		AssigneeDueAt:    task.DueAt,
		TaskPoints:       task.EffortPoints,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{
		Outcome:      outcome,
		DecisionID:   res.DecisionID,
		AssignmentID: res.AssignmentID,
		SelectedID:   winner.PartyID,
	}, nil
}

// mergeRanked returns a copy of allEvals where every entry whose PartyID
// appears in ranked has its Rank overwritten with ranked's value; order
// follows allEvals (candidateSet's deterministic party-id-sorted order),
// not ranked's order — CONSIDERED edges don't need to be written in rank
// order, only tagged with the right rank.
func mergeRanked(allEvals []assign.Evaluation, ranked []assign.Evaluation) []assign.Evaluation {
	rankByParty := make(map[string]int, len(ranked))
	for _, r := range ranked {
		rankByParty[r.PartyID] = r.Rank
	}
	out := make([]assign.Evaluation, len(allEvals))
	for i, e := range allEvals {
		if r, ok := rankByParty[e.PartyID]; ok {
			e.Rank = r
		}
		out[i] = e
	}
	return out
}

// runFallback is §8.6, dispatched via the pure assign.ClassifyFallback.
func runFallback(ctx context.Context, tx neo4j.ManagedTransaction, task assign.Task, cands []assign.Candidate, evals []assign.Evaluation, policy assign.Policy, rev int64) (Result, error) {
	switch assign.ClassifyFallback(evals, policy) {

	case assign.FallbackRelaxLevel:
		// FB1: try once more with every minLevel reduced by 1. §8.6's
		// pseudocode literally re-calls assign() and then "marks"
		// relaxed.decision.outcome = DEGRADED — but that would mean
		// mutating an already-persisted :AssignmentDecision, which directly
		// violates C5 (append-only, no exceptions, 20 §11). This is a
		// DELIBERATE DEVIATION from the literal pseudocode, resolved the
		// same way 20 §9.2's own append-only note resolves its own
		// tension (rehearsal ordering, C5): change the SEQUENCE so the
		// conflict cannot arise, not the invariant. Concretely: re-run
		// candidateSet+evaluate against the relaxed requirements directly
		// (not by re-entering AssignTx, which would call PersistDecision
		// with outcome=ASSIGNED first), and persist exactly once, with
		// outcome=DEGRADED from the start, only if the relaxed run actually
		// produces an eligible winner. If it does not, fall through to FB2
		// (decompose) using the ORIGINAL (non-relaxed) evals — relaxation
		// that finds no one is not itself a persisted decision (§8.6 does
		// not describe persisting a failed FB1 attempt).
		relaxedTask := task
		relaxedTask.RequiredCapabilities = assign.RelaxRequirements(task.RequiredCapabilities)
		relaxedCands, err := CandidateSet(ctx, tx, relaxedTask, policy)
		if err != nil {
			return Result{}, err
		}
		relaxedEvals := make([]assign.Evaluation, len(relaxedCands))
		for i, c := range relaxedCands {
			relaxedEvals[i] = assign.Evaluate(c, relaxedTask, policy)
		}
		relaxedRanked := assign.RankEligible(relaxedEvals, policy)
		if len(relaxedRanked) > 0 {
			return persistAssigned(ctx, tx, relaxedTask, relaxedEvals, relaxedRanked, policy, rev, assign.OutcomeDegraded)
		}
		// No one clears even the relaxed bar — fall through to FB2 using the
		// ORIGINAL evaluation (relaxation genuinely failed, so the decompose
		// suggestion should be computed against the real requirements).
		return persistDecompose(ctx, tx, task, cands, evals, policy, rev)

	case assign.FallbackDecompose:
		return persistDecompose(ctx, tx, task, cands, evals, policy, rev)

	case assign.FallbackEscalateLead:
		eta := assign.EarliestFreeTime(cands)
		res, err := PersistDecision(ctx, tx, PersistDecisionInput{
			TaskID: task.ID, Evaluations: evals, Policy: policy, OrgRevision: rev,
			Outcome: assign.OutcomeEscalatedLead, FallbackReason: "ESCALATED_LEAD",
			RequiredCaps: task.RequiredCapabilities,
		})
		if err != nil {
			return Result{}, err
		}
		// task.status := blocked is 20 §8.6's own instruction; this build's
		// minimal CreateWorkItem/WorkItem model has no separate "set status"
		// writer, and adding one is out of this task's scope (see final
		// report) — P4/whoever wires the dispatch path is expected to set
		// status alongside consuming this Result.
		return Result{Outcome: assign.OutcomeEscalatedLead, DecisionID: res.DecisionID, EscalateETA: eta}, nil

	case assign.FallbackEscalateAdmin:
		res, err := PersistDecision(ctx, tx, PersistDecisionInput{
			TaskID: task.ID, Evaluations: evals, Policy: policy, OrgRevision: rev,
			Outcome: assign.OutcomeEscalatedAdmin, FallbackReason: "ESCALATED_ADMIN",
			RequiredCaps: task.RequiredCapabilities,
		})
		if err != nil {
			return Result{}, err
		}
		return Result{Outcome: assign.OutcomeEscalatedAdmin, DecisionID: res.DecisionID}, nil

	default: // assign.FallbackUnassignable
		res, err := PersistDecision(ctx, tx, PersistDecisionInput{
			TaskID: task.ID, Evaluations: evals, Policy: policy, OrgRevision: rev,
			Outcome: assign.OutcomeUnassignable, FallbackReason: "UNASSIGNABLE",
			RequiredCaps: task.RequiredCapabilities,
		})
		if err != nil {
			return Result{}, err
		}
		return Result{Outcome: assign.OutcomeUnassignable, DecisionID: res.DecisionID}, nil
	}
}

// persistDecompose is FB2: find a minimal cover (policy.MaxCandidatesForDecompose,
// default 2) or fall through to FB5 (unassignable) if none exists.
func persistDecompose(ctx context.Context, tx neo4j.ManagedTransaction, task assign.Task, cands []assign.Candidate, evals []assign.Evaluation, policy assign.Policy, rev int64) (Result, error) {
	maxSize := policy.MaxCandidatesForDecompose
	if maxSize <= 0 {
		maxSize = 2 // 20 §3.5 line 344's documented default
	}
	cover, ok := assign.MinimalCover(task.RequiredCapabilities, cands, maxSize)
	if !ok {
		res, err := PersistDecision(ctx, tx, PersistDecisionInput{
			TaskID: task.ID, Evaluations: evals, Policy: policy, OrgRevision: rev,
			Outcome: assign.OutcomeUnassignable, FallbackReason: "UNASSIGNABLE",
			RequiredCaps: task.RequiredCapabilities,
		})
		if err != nil {
			return Result{}, err
		}
		return Result{Outcome: assign.OutcomeUnassignable, DecisionID: res.DecisionID}, nil
	}
	res, err := PersistDecision(ctx, tx, PersistDecisionInput{
		TaskID: task.ID, Evaluations: evals, Policy: policy, OrgRevision: rev,
		Outcome: assign.OutcomeDecomposeRequired, FallbackReason: "DECOMPOSE_REQUIRED",
		RequiredCaps: task.RequiredCapabilities,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Outcome: assign.OutcomeDecomposeRequired, DecisionID: res.DecisionID, Cover: cover}, nil
}

// Assign is the standalone runtime entry point: opens its own write
// session/transaction and runs AssignTx inside it. Use this for a normal,
// on-its-own assignment call. For the onboarding rehearsal (which must run
// inside an already-open, not-yet-committed transaction so a rehearsal
// failure rolls back with everything else), call AssignTx directly inside
// that transaction instead — do not call this method from there.
func (c *Client) Assign(ctx context.Context, task assign.Task, asOf time.Time) (Result, error) {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return AssignTx(ctx, tx, task, asOf)
	})
	if err != nil {
		return Result{}, err
	}
	return res.(Result), nil
}
