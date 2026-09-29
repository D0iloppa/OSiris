package assign

import (
	"sort"
	"strings"
	"time"
)

// FallbackPath is which of §8.6's FB1-FB5 branches applies. There is no
// separate "FB0" — ASSIGNED is the non-fallback happy path.
type FallbackPath string

const (
	FallbackRelaxLevel    FallbackPath = "relax_level"    // FB1
	FallbackDecompose     FallbackPath = "decompose"      // FB2
	FallbackEscalateLead  FallbackPath = "escalate_lead"  // FB3
	FallbackEscalateAdmin FallbackPath = "escalate_admin" // FB4
	FallbackUnassignable  FallbackPath = "unassignable"   // FB5
)

// blockerCode extracts the reason CODE (the part before the first ':') from
// an exclusion reason string such as "CAPABILITY_MISSING:cap.media.buying"
// or "CAPACITY_EXCEEDED" (which has no ':' and is its own code).
func blockerCode(reason string) string {
	if i := strings.IndexByte(reason, ':'); i >= 0 {
		return reason[:i]
	}
	return reason
}

// blockerSet returns the DISTINCT set of blocker codes across every
// excluded candidate's exclusion reasons — §8.6's "blockers := union of all
// exclusionReasons", reduced to codes so `blockers ⊆ {CAPABILITY_MISSING}`
// can be tested as a set-membership check.
func blockerSet(evals []Evaluation) map[string]bool {
	out := map[string]bool{}
	for _, e := range evals {
		if e.Verdict != VerdictExcluded {
			continue
		}
		for _, r := range e.ExclusionReasons {
			out[blockerCode(r)] = true
		}
	}
	return out
}

func subsetOf(set map[string]bool, allowed ...string) bool {
	allowedSet := map[string]bool{}
	for _, a := range allowed {
		allowedSet[a] = true
	}
	for k := range set {
		if !allowedSet[k] {
			return false
		}
	}
	return true
}

// ClassifyFallback is the pure decision half of §8.6: given the full
// (including-excluded) evaluation set and the policy, decide WHICH fallback
// path applies. It does not itself relax levels, compute a cover, persist a
// decision, or send a notification — those need the graph (FB1's re-run,
// FB2's candidate pool) or side effects (notify/enqueue) and are the
// orchestration layer's job (graphdb's Assign). This function is the part
// that must be deterministic and testable without a graph.
//
// canRelaxLevel additionally requires policy.LevelRelaxationAllowed — the
// caller must still verify (by re-running CandidateSet/Evaluate with
// minLevel-1) that some candidate actually clears the relaxed bar before
// committing to FallbackRelaxLevel; ClassifyFallback only tells the caller
// "it's worth trying FB1", matching §8.6's own IF condition ordering
// (levelRelaxationAllowed AND blockers-shape check come before the
// existence check).
func ClassifyFallback(evals []Evaluation, policy Policy) FallbackPath {
	blockers := blockerSet(evals)

	// FB1 candidate check (existence of a relaxed match is verified by the
	// caller after re-evaluating with minLevel-1; see doc comment above).
	if policy.LevelRelaxationAllowed && subsetOf(blockers, "CAPABILITY_MISSING") {
		return FallbackRelaxLevel
	}

	// FB2: capability-shaped gap that might be coverable by a small team.
	if subsetOf(blockers, "CAPABILITY_MISSING") {
		return FallbackDecompose
	}

	// FB3: capable but not available/free right now.
	if subsetOf(blockers, "UNAVAILABLE", "CAPACITY_EXCEEDED", "BUDGET_EXHAUSTED") {
		return FallbackEscalateLead
	}

	// FB4: permission/scope problems are never auto-relaxed.
	if blockers["NOT_PERMITTED"] || blockers["SCOPE_VIOLATION"] {
		return FallbackEscalateAdmin
	}

	// FB5: fallthrough — capability gap the organization has no holder for
	// at all, or a blocker mix not covered by any of the above.
	return FallbackUnassignable
}

// RelaxRequirements returns a copy of reqs with every MinLevel reduced by
// exactly one step (floor 1) — §8.6 FB1's "task with minLevel-1". Only
// MinLevel changes; Code/Weight/RequiredPermissions are untouched.
func RelaxRequirements(reqs []CapabilityRequirement) []CapabilityRequirement {
	out := make([]CapabilityRequirement, len(reqs))
	for i, r := range reqs {
		relaxed := r
		if relaxed.MinLevel > 1 {
			relaxed.MinLevel--
		}
		out[i] = relaxed
	}
	return out
}

// MinimalCover is §8.6 FB2's `minimalCover(reqs, cands, maxSize)` — a
// deterministic, exhaustive set-cover search: find the SMALLEST group of at
// most maxSize candidates whose combined (already-computed) capability
// matches jointly satisfy every requirement, i.e. for every requirement
// there is at least one candidate in the group whose CapabilityMatches[req]
// clears req.MinLevel.
//
// policy.MaxCandidatesForDecompose defaults to 2 (20 §3.5 line 344), so this
// is deliberately brute force (no greedy/approximate heuristic — the task's
// brief explicitly forbids that, since an approximation could pick a
// different cover on a re-run and break decision reproducibility). Complete
// enumeration of C(n,1)+C(n,2)+...+C(n,maxSize) subsets is cheap at
// organization scale (tens of candidates, maxSize typically 2).
//
// Ties (multiple covers of the same minimal size) are broken by
// lexicographically-smallest sorted party-id tuple, for the same reason
// §8.5's tie-break chain ends in party.id: reproducibility.
//
// Returns (partyIDs, true) for the winning cover, or (nil, false) if no
// group of size <= maxSize covers every requirement.
func MinimalCover(reqs []CapabilityRequirement, cands []Candidate, maxSize int) ([]string, bool) {
	if maxSize < 1 {
		maxSize = 1
	}
	if len(reqs) == 0 {
		return nil, true // vacuously covered — but callers only invoke this when there IS a gap
	}

	// Deterministic candidate ordering regardless of caller-supplied order.
	sorted := make([]Candidate, len(cands))
	copy(sorted, cands)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].PartyID < sorted[j].PartyID })

	satisfies := func(c Candidate, req CapabilityRequirement) bool {
		m, ok := c.CapabilityMatches[req.Code]
		return ok && m.Exists && m.Level >= req.MinLevel
	}

	n := len(sorted)
	var bestIdx []int

	// Enumerate combinations of size 1..maxSize in increasing size order so
	// the FIRST full cover found is already minimal-size; among same-size
	// covers we still need the lexicographically smallest, so we collect all
	// covers of the winning size and pick the smallest by sorted party-id
	// tuple (index order == party-id order since `sorted` is id-sorted, so
	// the first combination enumerated in standard combinatorial order is
	// already the lexicographically smallest by index, which equals
	// lexicographically smallest by id).
	for size := 1; size <= maxSize && size <= n; size++ {
		combo := make([]int, size)
		var found bool
		var rec func(start, depth int) bool
		rec = func(start, depth int) bool {
			if depth == size {
				// check coverage
				for _, req := range reqs {
					covered := false
					for _, idx := range combo {
						if satisfies(sorted[idx], req) {
							covered = true
							break
						}
					}
					if !covered {
						return false
					}
				}
				return true
			}
			for i := start; i <= n-(size-depth); i++ {
				combo[depth] = i
				if rec(i+1, depth+1) {
					return true
				}
			}
			return false
		}
		if rec(0, 0) {
			found = true
			bestIdx = append([]int(nil), combo...)
		}
		if found {
			break
		}
	}

	if bestIdx == nil {
		return nil, false
	}
	ids := make([]string, len(bestIdx))
	for i, idx := range bestIdx {
		ids[i] = sorted[idx].PartyID
	}
	sort.Strings(ids)
	return ids, true
}

// EarliestFreeTime is §8.6 FB3's `earliestFreeTime(candidates)` — trivially
// the earliest NextFreeAt across the given candidates (graphdb.CandidateSet
// populates NextFreeAt from each party's active :Assignment.dueAt minimum).
// Candidates with NextFreeAt == nil (no active assignment, i.e. unknown/not
// applicable) are ignored. Returns nil if no candidate has a known
// NextFreeAt.
func EarliestFreeTime(cands []Candidate) *time.Time {
	var best *time.Time
	for _, c := range cands {
		if c.NextFreeAt == nil {
			continue
		}
		if best == nil || c.NextFreeAt.Before(*best) {
			t := *c.NextFreeAt
			best = &t
		}
	}
	return best
}
