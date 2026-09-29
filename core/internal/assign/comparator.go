package assign

import (
	"sort"
	"time"
)

// Comparator builds 20 §8.5's comparator(chain) — a total order over
// Evaluation, ending unconditionally in party.id ascending (line 742: "6번이
// 있어야 '같은 입력이면 항상 같은 결과'가 성립"). The returned function
// returns <0, 0, >0 exactly like strings.Compare / sort's less-than
// convention (negative means a sorts before b).
//
// Every key in the chain that this build recognizes is handled; an unknown
// key is simply skipped (falls through to the next key, and ultimately to
// party.id) rather than panicking — a policy typo should degrade to "less
// tie-break precision", not crash assignment.
func Comparator(chain []string) func(a, b Evaluation) int {
	return func(a, b Evaluation) int {
		for _, key := range chain {
			if d := compareByKey(key, a, b); d != 0 {
				return d
			}
		}
		return comparePartyID(a, b)
	}
}

// RankEligible is §8.2 lines 635-641:
//
//	eligible := [e FOR e IN evals IF e.verdict == ELIGIBLE]
//	ranked := sortStable(eligible, comparator(policy.tieBreakChain))
//	FOR i, e IN enumerate(ranked): e.rank := i + 1
//
// evals is NOT mutated; RankEligible returns a new, sorted slice of the
// eligible subset with Rank filled in (1-based). Uses sort.SliceStable per
// §8.5's explicit "정렬은 stable sort를 쓴다" — though in practice the
// party.id tie-break at the end of the chain already forces a total order,
// so stability only matters if a caller supplies a chain that omits
// "partyId" outright (Comparator still appends it as the final fallback,
// so this is defensive, not load-bearing).
func RankEligible(evals []Evaluation, policy Policy) []Evaluation {
	var eligible []Evaluation
	for _, e := range evals {
		if e.Verdict == VerdictEligible {
			eligible = append(eligible, e)
		}
	}
	less := Comparator(policy.TieBreakChain)
	sort.SliceStable(eligible, func(i, j int) bool {
		return less(eligible[i], eligible[j]) < 0
	})
	for i := range eligible {
		eligible[i].Rank = i + 1
	}
	return eligible
}

func compareByKey(key string, a, b Evaluation) int {
	switch key {
	case "specificity":
		// descending: higher specificity wins
		return intCompare(b.SpecificityScore, a.SpecificityScore)
	case "load":
		// ascending: lower load (less busy) wins
		return floatCompare(a.LoadAtDecision, b.LoadAtDecision)
	case "sameTeam":
		// true first
		return boolCompareTrueFirst(a.SameTeam, b.SameTeam)
	case "contextContinuity":
		// true first
		return boolCompareTrueFirst(a.ContextContinuity, b.ContextContinuity)
	case "verificationRecency":
		// most recent first (descending time)
		return timeCompareDescending(a.VerificationRecency, b.VerificationRecency)
	case "partyId":
		return comparePartyID(a, b)
	default:
		return 0
	}
}

func comparePartyID(a, b Evaluation) int {
	switch {
	case a.PartyID < b.PartyID:
		return -1
	case a.PartyID > b.PartyID:
		return 1
	default:
		return 0
	}
}

func intCompare(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func floatCompare(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func boolCompareTrueFirst(a, b bool) int {
	if a == b {
		return 0
	}
	if a {
		return -1 // a is true, b is false: a first
	}
	return 1
}

// timeCompareDescending puts the more recent time first. A zero time.Time
// (never verified / unknown) sorts after any known time, on both sides —
// i.e. it is treated as "least recent".
func timeCompareDescending(a, b time.Time) int {
	switch {
	case a.IsZero() && b.IsZero():
		return 0
	case a.IsZero():
		return 1 // a unknown, b known: b (more recent) first
	case b.IsZero():
		return -1
	case a.After(b):
		return -1
	case a.Before(b):
		return 1
	default:
		return 0
	}
}
