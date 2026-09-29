package graphdb

// assign_policy.go adds the read half of :AssignmentPolicy (20 §8.2 line
// 626's `loadActivePolicy(asOf)`). The write half (CreateAssignmentPolicy)
// already existed in writeops.go before this task; this file adds the
// missing reader without touching that function.

import (
	"context"
	"sort"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"osiris-core/internal/assign"
)

// LoadActivePolicy is 20 §8.2's `loadActivePolicy(asOf)`: the highest-
// versioned :AssignmentPolicy whose effectiveFrom is at or before asOf. A
// zero asOf means "now" is not being simulated — it is simply not used as a
// filter, and the highest version overall is returned (this matches how
// every other read in this codebase treats "as of" when the caller has no
// specific point in time in mind, and keeps the common assign() call site,
// which always wants "the currently active policy", to a single simple
// query).
//
// Must be called inside an already-open transaction (read or write) — see
// AssignTx's doc comment for why assign() shares its caller's transaction
// rather than opening its own session.
func LoadActivePolicy(ctx context.Context, tx neo4j.ManagedTransaction, asOf time.Time) (assign.Policy, error) {
	params := map[string]any{}
	filter := ""
	if !asOf.IsZero() {
		filter = "WHERE p.effectiveFrom <= $asOf"
		params["asOf"] = asOf
	}
	res, err := tx.Run(ctx, `
		MATCH (p:AssignmentPolicy) `+filter+`
		RETURN p.version AS version, p.effectiveFrom AS effectiveFrom,
		       p.tieBreakChain AS tieBreakChain, p.teamStrict AS teamStrict,
		       p.levelRelaxationAllowed AS levelRelaxationAllowed,
		       p.maxCandidatesForDecompose AS maxCandidatesForDecompose
		ORDER BY p.version DESC
	`, params)
	if err != nil {
		return assign.Policy{}, err
	}
	records, err := res.Collect(ctx)
	if err != nil {
		return assign.Policy{}, err
	}
	if len(records) == 0 {
		return assign.Policy{}, validationErrorf("no :AssignmentPolicy exists as of the given time — onboarding step 12 (20 §9.2) has not run")
	}

	// records are already ORDER BY version DESC; take the first (highest
	// version). Sorted defensively again here in case a future edit drops
	// the ORDER BY — determinism must not depend on driver row order.
	sort.SliceStable(records, func(i, j int) bool {
		vi, _ := records[i].Get("version")
		vj, _ := records[j].Get("version")
		return intOfAny(vi) > intOfAny(vj)
	})
	rec := records[0]

	version, _ := rec.Get("version")
	effectiveFrom, _ := rec.Get("effectiveFrom")
	tieBreakChain, _ := rec.Get("tieBreakChain")
	teamStrict, _ := rec.Get("teamStrict")
	levelRelax, _ := rec.Get("levelRelaxationAllowed")
	maxCand, _ := rec.Get("maxCandidatesForDecompose")

	return assign.Policy{
		Version:                   intOfAny(version),
		EffectiveFrom:             timeOf(effectiveFrom),
		TieBreakChain:             stringsOfAny(tieBreakChain),
		TeamStrict:                boolOfAny(teamStrict),
		LevelRelaxationAllowed:    boolOfAny(levelRelax),
		MaxCandidatesForDecompose: intOfAny(maxCand),
	}, nil
}

// currentOrgRevisionTx reads the current :OrgRevision.n inside an
// already-open transaction — assign() needs this for
// :AssignmentDecision.orgRevision (20 §3.5 line 291) but must NOT bump it
// (assign() does not change organizational intent; see AssignTx's doc
// comment). This is a READ, unlike writeops.go's bumpRevision.
func currentOrgRevisionTx(ctx context.Context, tx neo4j.ManagedTransaction) (int64, error) {
	res, err := tx.Run(ctx, `
		MATCH (r:OrgRevision)
		RETURN r.n AS n ORDER BY r.n DESC LIMIT 1
	`, nil)
	if err != nil {
		return 0, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := rec.Get("n")
	if v, ok := n.(int64); ok {
		return v, nil
	}
	return 0, nil
}
