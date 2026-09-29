package graphdb

// P2 stage ③, this task's item 2: "validateActionTypeInput을 이 행에 실제로
// 통과시켜라 — 이것이 ②의 검증 코드가 처음으로 실제 데이터를 받는 지점이다."
//
// PURE UNIT TEST, NO NEO4J: validateActionTypeInput (actiontype.go) is the
// Go-level half of CreateActionType/UpdateActionType's validation — the
// half that needs no live Cypher session (REQUIRES_PERMISSION/COMPOSED_OF
// target existence checks are the other half, deliberately not exercised
// here; see this task's final report for why no Neo4j integration-test
// fixture was introduced: this repository's existing tests are all pure
// unit tests over fakes, e.g. internal/genesis/genesis_test.go's
// fakeOrgReader, and this test follows that precedent rather than adding a
// new testcontainers/docker dependency).
//
// This file imports action/predicate and action/effect ONLY to build the
// knownPredicates/knownEffects sets validateActionTypeInput needs — the
// exact same knownPredicateSet()/knownEffectSet() pattern
// core/internal/api/server.go already uses at its call sites. That import
// exists in a _test.go file, not in actiontype.go or any other production
// file in this package — graphdb's production code still never imports
// action/predicate or action/effect (see actiontype_reconcile.go's doc
// comment on the one-directional main → {graphdb, action/*} dependency
// arrow; a test binary importing both ends of that arrow to verify they
// agree does not violate it).

import (
	"testing"

	"osiris-core/internal/action/effect"
	"osiris-core/internal/action/predicate"
)

func knownPredicateSetForTest() map[string]struct{} {
	out := make(map[string]struct{})
	for _, n := range predicate.Names() {
		out[n] = struct{}{}
	}
	return out
}

func knownEffectSetForTest() map[string]struct{} {
	out := make(map[string]struct{})
	for _, n := range effect.Names() {
		out[n] = struct{}{}
	}
	return out
}

// TestValidateActionTypeInput_GitCommitRow_Passes is this task's item 2's
// actual assertion: GitCommitActionType()'s data, run through the real
// validateActionTypeInput exactly as CreateActionType would, passes.
func TestValidateActionTypeInput_GitCommitRow_Passes(t *testing.T) {
	in := GitCommitActionType()
	if err := validateActionTypeInput(in, knownPredicateSetForTest(), knownEffectSetForTest()); err != nil {
		t.Fatalf("validateActionTypeInput(git.commit row) = %v, want nil — either the row or the validator is wrong; see actiontype_gitcommit.go's doc comment for the row's field-by-field sourcing before changing either", err)
	}
}

// TestValidateActionTypeInput_GitCommitRow_RejectsUnknownEffect is a
// companion negative check: if effect.scm.commit were NOT registered (a
// stand-in for "someone renamed or removed it"), CA3's fail-closed
// reference check must reject the row rather than silently accepting a
// dangling effect name.
func TestValidateActionTypeInput_GitCommitRow_RejectsUnknownEffect(t *testing.T) {
	in := GitCommitActionType()
	knownEffectsWithoutSCMCommit := map[string]struct{}{} // deliberately empty
	err := validateActionTypeInput(in, knownPredicateSetForTest(), knownEffectsWithoutSCMCommit)
	if err == nil {
		t.Fatalf("validateActionTypeInput must reject git.commit's row when effect.scm.commit is not in the known-effects set (CA3 fail-closed)")
	}
}
