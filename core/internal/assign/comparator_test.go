package assign_test

import (
	"math/rand"
	"reflect"
	"testing"

	"osiris-core/internal/assign"
)

func defaultChain() []string {
	// 20 §3.5 line 341's example chain.
	return []string{"specificity", "load", "sameTeam", "contextContinuity", "verificationRecency", "partyId"}
}

func TestRankEligible_TieBreakOrder(t *testing.T) {
	policy := assign.Policy{TieBreakChain: defaultChain()}

	evals := []assign.Evaluation{
		{PartyID: "b", Verdict: assign.VerdictEligible, SpecificityScore: 2, LoadAtDecision: 0.5},
		{PartyID: "a", Verdict: assign.VerdictEligible, SpecificityScore: 3, LoadAtDecision: 0.9}, // wins on specificity alone
		{PartyID: "c", Verdict: assign.VerdictExcluded, SpecificityScore: 5},                      // excluded, must not rank
	}

	ranked := assign.RankEligible(evals, policy)
	if len(ranked) != 2 {
		t.Fatalf("expected 2 eligible, got %d", len(ranked))
	}
	if ranked[0].PartyID != "a" || ranked[0].Rank != 1 {
		t.Errorf("expected a to win rank 1 (higher specificity), got %+v", ranked[0])
	}
	if ranked[1].PartyID != "b" || ranked[1].Rank != 2 {
		t.Errorf("expected b rank 2, got %+v", ranked[1])
	}
}

func TestRankEligible_FinalTieBreakIsPartyID(t *testing.T) {
	// Every tie-break key ties; only party.id should decide, ascending.
	policy := assign.Policy{TieBreakChain: defaultChain()}
	evals := []assign.Evaluation{
		{PartyID: "zeta", Verdict: assign.VerdictEligible},
		{PartyID: "alpha", Verdict: assign.VerdictEligible},
		{PartyID: "mu", Verdict: assign.VerdictEligible},
	}
	ranked := assign.RankEligible(evals, policy)
	want := []string{"alpha", "mu", "zeta"}
	for i, id := range want {
		if ranked[i].PartyID != id {
			t.Errorf("position %d: got %s, want %s", i, ranked[i].PartyID, id)
		}
	}
}

// TestRankEligible_DeterministicUnderShuffle is the determinism test this
// task's brief explicitly requires: shuffling the input candidate order must
// never change the final ranked output, for a variety of random seeds and a
// mix of ties across every tie-break stage.
func TestRankEligible_DeterministicUnderShuffle(t *testing.T) {
	policy := assign.Policy{TieBreakChain: defaultChain()}

	base := []assign.Evaluation{
		{PartyID: "emp-a", Verdict: assign.VerdictEligible, SpecificityScore: 3, LoadAtDecision: 0.2, SameTeam: true},
		{PartyID: "emp-b", Verdict: assign.VerdictEligible, SpecificityScore: 3, LoadAtDecision: 0.2, SameTeam: true},
		{PartyID: "emp-c", Verdict: assign.VerdictEligible, SpecificityScore: 3, LoadAtDecision: 0.1, SameTeam: false},
		{PartyID: "emp-d", Verdict: assign.VerdictEligible, SpecificityScore: 1, LoadAtDecision: 0.9, SameTeam: true},
		{PartyID: "emp-e", Verdict: assign.VerdictExcluded, ExclusionReasons: []string{"CAPACITY_EXCEEDED"}},
	}

	canonical := assign.RankEligible(append([]assign.Evaluation(nil), base...), policy)

	for seed := int64(0); seed < 20; seed++ {
		shuffled := append([]assign.Evaluation(nil), base...)
		rnd := rand.New(rand.NewSource(seed))
		rnd.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		got := assign.RankEligible(shuffled, policy)
		if len(got) != len(canonical) {
			t.Fatalf("seed %d: length mismatch", seed)
		}
		for i := range got {
			if got[i].PartyID != canonical[i].PartyID || got[i].Rank != canonical[i].Rank {
				t.Errorf("seed %d position %d: got %+v, want %+v", seed, i, got[i], canonical[i])
			}
		}
	}
}

func TestComparator_Determinism_RepeatedCallsSameResult(t *testing.T) {
	cmp := assign.Comparator(defaultChain())
	a := assign.Evaluation{PartyID: "a", SpecificityScore: 1}
	b := assign.Evaluation{PartyID: "b", SpecificityScore: 1}

	r1 := cmp(a, b)
	r2 := cmp(a, b)
	if r1 != r2 {
		t.Fatalf("Comparator not deterministic: %d vs %d", r1, r2)
	}
	if !reflect.DeepEqual(a, assign.Evaluation{PartyID: "a", SpecificityScore: 1}) {
		t.Fatalf("Comparator must not mutate its arguments")
	}
}
