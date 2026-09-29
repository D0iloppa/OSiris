package graphdb

// This task's item 5: mutatePartyGrant's DG6 guard (80 §5.1.2 / §5.1.3 P1-Q)
// needs a live Neo4j session to exercise end-to-end (it runs inside an
// ExecuteWrite transaction), and this package's existing convention is pure
// unit tests over fakes with no testcontainers/Neo4j fixture (see
// actiontype_gitcommit_test.go's doc comment). So this file tests the two
// PURE pieces the guard is built from — partyHasLabel's label-matching and
// humanOnlyPermissionCodes' closed-set shape — the same boundary
// GitCommitActionType's test draws around validateActionTypeInput.
//
// WHAT THIS FILE CANNOT DO: verify that humanOnlyPermissionCodes agrees with
// api/internal/authz/humanonly.go's HumanOnly. This is a Go test in the
// osiris-core module; that set lives in the separate osiris-api module, and
// no import can cross that boundary. That cross-copy agreement is enforced
// ONLY by bin/lint-action-layer.sh's AC6 section, which reads both files'
// source text directly — see this file's second test for the corrected
// account of what it actually checks (a prior version of that test's name
// and comment claimed to check API-layer agreement; it did not).

import "testing"

func TestPartyHasLabel(t *testing.T) {
	cases := []struct {
		name  string
		v     any
		label string
		want  bool
	}{
		{"present", []any{"Party", "AIAgent"}, "AIAgent", true},
		{"absent", []any{"Party", "Person"}, "AIAgent", false},
		{"empty", []any{}, "AIAgent", false},
		{"nil", nil, "AIAgent", false},
		{"wrong type element ignored", []any{1, "AIAgent"}, "AIAgent", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := partyHasLabel(c.v, c.label); got != c.want {
				t.Fatalf("partyHasLabel(%v, %q) = %v, want %v", c.v, c.label, got, c.want)
			}
		})
	}
}

// TestHumanOnlyPermissionCodes_PinnedContents anchors this package's OWN
// copy of 80 §5.1.3's four-element closed set to a literal `want` list, so a
// silent edit to humanOnlyPermissionCodes (an accidental deletion, a typo in
// a code string, an extra entry) fails `go test` immediately.
//
// WHAT THIS TEST DOES NOT DO, AND WHY THE NAME NO LONGER CLAIMS IT: it does
// NOT read api/internal/authz/humanonly.go, does NOT import the api module,
// and therefore CANNOT detect the two copies drifting apart from each
// other — only from this file's own hardcoded `want`. An earlier version of
// this test was named TestHumanOnlyPermissionCodes_MatchesAPILayer and
// claimed in its doc comment to check agreement with "api/internal/authz/
// humanonly.go's HumanOnly" and to be "checked independently by both core
// and orchestrator" the way I9 is — BOTH claims were false: this test's
// only import is "testing" (never api/internal/authz), and I9's precedent
// does not actually apply here, because I9's two checkers each compare
// against a COMMON THIRD REFERENCE (the graph's own
// :Permission.catalogVersion values), while this test and any api-side test
// would each only ever compare against their OWN hardcoded literal — never
// against each other, and never against a shared source of truth. A real
// cross-copy check needs to read BOTH files' source text in one place; Go's
// module boundary (core and osiris-api are separate modules, no import
// possible) makes that impossible from inside a `go test` in either module.
// That comparison now lives in bin/lint-action-layer.sh's AC6 section (see
// its "sub-check (ii)" comment) — THIS test's only job is to make sure this
// file's own copy doesn't drift out from under itself between lint runs.
func TestHumanOnlyPermissionCodes_PinnedContents(t *testing.T) {
	want := []string{"org.admin", "org.manage.people", "org.manage.roles", "actiontype.write"}
	if len(humanOnlyPermissionCodes) != len(want) {
		t.Fatalf("humanOnlyPermissionCodes has %d entries, want %d: %v", len(humanOnlyPermissionCodes), len(want), humanOnlyPermissionCodes)
	}
	for _, code := range want {
		if _, ok := humanOnlyPermissionCodes[code]; !ok {
			t.Fatalf("humanOnlyPermissionCodes is missing %q", code)
		}
	}
}
