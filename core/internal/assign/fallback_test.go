package assign_test

import (
	"math/rand"
	"testing"

	"osiris-core/internal/assign"
)

func capCandidate(id string, level int) assign.Candidate {
	return assign.Candidate{
		PartyID: id,
		CapabilityMatches: map[string]assign.CapabilityMatch{
			"cap.x": {Exists: level > 0, Level: level},
		},
	}
}

func TestMinimalCover_SingleCandidateCoversAll(t *testing.T) {
	reqs := []assign.CapabilityRequirement{{Code: "cap.x", MinLevel: 2}}
	cands := []assign.Candidate{capCandidate("a", 3), capCandidate("b", 0)}

	got, ok := assign.MinimalCover(reqs, cands, 2)
	if !ok {
		t.Fatal("expected a cover to be found")
	}
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("expected minimal cover [a], got %v", got)
	}
}

func TestMinimalCover_RequiresTwoCandidates(t *testing.T) {
	reqs := []assign.CapabilityRequirement{
		{Code: "cap.x", MinLevel: 2},
		{Code: "cap.y", MinLevel: 2},
	}
	a := assign.Candidate{PartyID: "a", CapabilityMatches: map[string]assign.CapabilityMatch{
		"cap.x": {Exists: true, Level: 3},
	}}
	b := assign.Candidate{PartyID: "b", CapabilityMatches: map[string]assign.CapabilityMatch{
		"cap.y": {Exists: true, Level: 3},
	}}
	// c alone can't cover either but shouldn't break the search.
	c := assign.Candidate{PartyID: "c", CapabilityMatches: map[string]assign.CapabilityMatch{}}

	got, ok := assign.MinimalCover(reqs, []assign.Candidate{c, b, a}, 2)
	if !ok {
		t.Fatal("expected a cover of size 2 to be found")
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("expected [a b], got %v", got)
	}
}

func TestMinimalCover_NoCoverWithinMaxSize(t *testing.T) {
	reqs := []assign.CapabilityRequirement{
		{Code: "cap.x", MinLevel: 5},
	}
	cands := []assign.Candidate{capCandidate("a", 1)}

	_, ok := assign.MinimalCover(reqs, cands, 2)
	if ok {
		t.Fatal("expected no cover to be found")
	}
}

// TestMinimalCover_ExhaustiveSearchIsActuallyMinimal verifies the brute-force
// search does not settle for a size-2 cover when a size-1 cover exists, and
// vice versa correctly reports failure when the true minimum exceeds maxSize.
func TestMinimalCover_ExhaustiveSearchIsActuallyMinimal(t *testing.T) {
	reqs := []assign.CapabilityRequirement{
		{Code: "cap.x", MinLevel: 2},
		{Code: "cap.y", MinLevel: 2},
	}
	// "solo" alone covers both requirements; if the search were sloppy it
	// might return a 2-candidate combination instead.
	solo := assign.Candidate{PartyID: "solo", CapabilityMatches: map[string]assign.CapabilityMatch{
		"cap.x": {Exists: true, Level: 3},
		"cap.y": {Exists: true, Level: 3},
	}}
	partial1 := assign.Candidate{PartyID: "p1", CapabilityMatches: map[string]assign.CapabilityMatch{
		"cap.x": {Exists: true, Level: 3},
	}}
	partial2 := assign.Candidate{PartyID: "p2", CapabilityMatches: map[string]assign.CapabilityMatch{
		"cap.y": {Exists: true, Level: 3},
	}}

	got, ok := assign.MinimalCover(reqs, []assign.Candidate{partial1, partial2, solo}, 2)
	if !ok {
		t.Fatal("expected a cover")
	}
	if len(got) != 1 || got[0] != "solo" {
		t.Errorf("expected minimal cover [solo] (size 1 beats any size-2 combo), got %v", got)
	}
}

func TestMinimalCover_DeterministicUnderShuffle(t *testing.T) {
	reqs := []assign.CapabilityRequirement{
		{Code: "cap.x", MinLevel: 2},
		{Code: "cap.y", MinLevel: 2},
	}
	base := []assign.Candidate{
		{PartyID: "a", CapabilityMatches: map[string]assign.CapabilityMatch{"cap.x": {Exists: true, Level: 3}}},
		{PartyID: "b", CapabilityMatches: map[string]assign.CapabilityMatch{"cap.y": {Exists: true, Level: 3}}},
		{PartyID: "c", CapabilityMatches: map[string]assign.CapabilityMatch{"cap.x": {Exists: true, Level: 3}, "cap.y": {Exists: true, Level: 3}}},
	}
	canonical, ok := assign.MinimalCover(reqs, base, 2)
	if !ok {
		t.Fatal("expected a cover")
	}

	for seed := int64(0); seed < 10; seed++ {
		shuffled := append([]assign.Candidate(nil), base...)
		rnd := rand.New(rand.NewSource(seed))
		rnd.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		got, ok2 := assign.MinimalCover(reqs, shuffled, 2)
		if ok2 != ok {
			t.Fatalf("seed %d: ok mismatch", seed)
		}
		if len(got) != len(canonical) {
			t.Fatalf("seed %d: length mismatch got %v want %v", seed, got, canonical)
		}
		for i := range got {
			if got[i] != canonical[i] {
				t.Errorf("seed %d: got %v, want %v", seed, got, canonical)
			}
		}
	}
}

func TestClassifyFallback_FB1RelaxLevel(t *testing.T) {
	policy := assign.Policy{LevelRelaxationAllowed: true}
	evals := []assign.Evaluation{
		{PartyID: "a", Verdict: assign.VerdictExcluded, ExclusionReasons: []string{"CAPABILITY_MISSING:cap.x"}},
	}
	if got := assign.ClassifyFallback(evals, policy); got != assign.FallbackRelaxLevel {
		t.Errorf("expected FallbackRelaxLevel, got %v", got)
	}
}

func TestClassifyFallback_FB2DecomposeWhenRelaxationDisallowed(t *testing.T) {
	policy := assign.Policy{LevelRelaxationAllowed: false}
	evals := []assign.Evaluation{
		{PartyID: "a", Verdict: assign.VerdictExcluded, ExclusionReasons: []string{"CAPABILITY_MISSING:cap.x"}},
	}
	if got := assign.ClassifyFallback(evals, policy); got != assign.FallbackDecompose {
		t.Errorf("expected FallbackDecompose, got %v", got)
	}
}

func TestClassifyFallback_FB3EscalateLead(t *testing.T) {
	policy := assign.Policy{}
	evals := []assign.Evaluation{
		{PartyID: "a", Verdict: assign.VerdictExcluded, ExclusionReasons: []string{"UNAVAILABLE:leave"}},
		{PartyID: "b", Verdict: assign.VerdictExcluded, ExclusionReasons: []string{"CAPACITY_EXCEEDED"}},
	}
	if got := assign.ClassifyFallback(evals, policy); got != assign.FallbackEscalateLead {
		t.Errorf("expected FallbackEscalateLead, got %v", got)
	}
}

// TestClassifyFallback_FB3ReachableViaBudgetExhausted is the reachability
// check the F6 fix requires: 20 §8.6 line 781's FB3 branch names
// BUDGET_EXHAUSTED explicitly as one of the three allowed blocker codes
// (`blockers ⊆ {UNAVAILABLE, CAPACITY_EXCEEDED, BUDGET_EXHAUSTED}`). Before
// F6 was implemented, Evaluate could never PRODUCE that code, so this
// branch — even though ClassifyFallback already recognized it — was
// unreachable end to end. This test proves it alone, and mixed with the
// other two allowed codes, both classify to FallbackEscalateLead.
func TestClassifyFallback_FB3ReachableViaBudgetExhausted(t *testing.T) {
	policy := assign.Policy{}

	onlyBudget := []assign.Evaluation{
		{PartyID: "agent-1", Verdict: assign.VerdictExcluded, ExclusionReasons: []string{"BUDGET_EXHAUSTED"}},
	}
	if got := assign.ClassifyFallback(onlyBudget, policy); got != assign.FallbackEscalateLead {
		t.Errorf("BUDGET_EXHAUSTED alone: expected FallbackEscalateLead, got %v", got)
	}

	mixed := []assign.Evaluation{
		{PartyID: "agent-1", Verdict: assign.VerdictExcluded, ExclusionReasons: []string{"BUDGET_EXHAUSTED"}},
		{PartyID: "emp-1", Verdict: assign.VerdictExcluded, ExclusionReasons: []string{"UNAVAILABLE:leave"}},
		{PartyID: "emp-2", Verdict: assign.VerdictExcluded, ExclusionReasons: []string{"CAPACITY_EXCEEDED"}},
	}
	if got := assign.ClassifyFallback(mixed, policy); got != assign.FallbackEscalateLead {
		t.Errorf("mixed budget+unavailable+capacity: expected FallbackEscalateLead, got %v", got)
	}
}

// TestClassifyFallback_FB3EndToEndFromEvaluate chains assign.Evaluate's real
// F6 output straight into assign.ClassifyFallback, proving the full path
// (not just ClassifyFallback in isolation) reaches FB3 now that F6 exists.
func TestClassifyFallback_FB3EndToEndFromEvaluate(t *testing.T) {
	task := assign.Task{ID: "task-1", SecurityLabel: "public", EffortPoints: 1}
	agent := assign.Candidate{
		PartyID: "agent-1", Kind: assign.KindAIAgent, Ready: true,
		BudgetExhausted: true, MaxTaskPoints: 3,
	}
	eval := assign.Evaluate(agent, task, assign.Policy{})
	if eval.Verdict != assign.VerdictExcluded {
		t.Fatalf("expected excluded, got eligible")
	}

	got := assign.ClassifyFallback([]assign.Evaluation{eval}, assign.Policy{})
	if got != assign.FallbackEscalateLead {
		t.Errorf("expected FallbackEscalateLead end to end, got %v (reasons: %v)", got, eval.ExclusionReasons)
	}
}

func TestClassifyFallback_FB4EscalateAdmin(t *testing.T) {
	policy := assign.Policy{}
	evals := []assign.Evaluation{
		{PartyID: "a", Verdict: assign.VerdictExcluded, ExclusionReasons: []string{"NOT_PERMITTED:git.commit"}},
	}
	if got := assign.ClassifyFallback(evals, policy); got != assign.FallbackEscalateAdmin {
		t.Errorf("expected FallbackEscalateAdmin, got %v", got)
	}

	evals2 := []assign.Evaluation{
		{PartyID: "a", Verdict: assign.VerdictExcluded, ExclusionReasons: []string{"SCOPE_VIOLATION:confidential"}},
	}
	if got := assign.ClassifyFallback(evals2, policy); got != assign.FallbackEscalateAdmin {
		t.Errorf("expected FallbackEscalateAdmin for scope violation, got %v", got)
	}
}

func TestClassifyFallback_FB5Unassignable(t *testing.T) {
	policy := assign.Policy{}
	// mixed blocker types not covered by any earlier branch
	evals := []assign.Evaluation{
		{PartyID: "a", Verdict: assign.VerdictExcluded, ExclusionReasons: []string{"CAPABILITY_MISSING:cap.x", "UNAVAILABLE:leave"}},
	}
	if got := assign.ClassifyFallback(evals, policy); got != assign.FallbackUnassignable {
		t.Errorf("expected FallbackUnassignable, got %v", got)
	}
}

func TestRelaxRequirements_FloorsAtOne(t *testing.T) {
	in := []assign.CapabilityRequirement{{Code: "cap.x", MinLevel: 1}, {Code: "cap.y", MinLevel: 3}}
	out := assign.RelaxRequirements(in)
	if out[0].MinLevel != 1 {
		t.Errorf("expected floor at 1, got %d", out[0].MinLevel)
	}
	if out[1].MinLevel != 2 {
		t.Errorf("expected 3-1=2, got %d", out[1].MinLevel)
	}
	// original untouched
	if in[1].MinLevel != 3 {
		t.Errorf("RelaxRequirements mutated its input")
	}
}
