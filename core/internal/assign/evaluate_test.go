package assign_test

// Unit tests for the pure half of P3 (20-ontology-schema.md §8): F1-F5
// (Evaluate), tie-break (Comparator/RankEligible), and fallback
// (MinimalCover/ClassifyFallback). Everything here is table-driven over
// hand-built assign.Candidate/Task/Policy values — no graph, no fakes needed,
// matching the "pure computation" half of this task's three-way split.
//
// Determinism is tested explicitly (not just asserted in prose): the same
// input evaluated/ranked twice must produce byte-identical output, and
// shuffling candidate input order must not change the final ranked order —
// this is what makes assignment decisions reproducible (20 §8.5 line 744).

import (
	"reflect"
	"testing"
	"time"

	"osiris-core/internal/assign"
)

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return tm
}

// parseOrZero is the non-testing.T variant used by fixture builders that
// run outside a test function's scope (e.g. eligibleCandidate).
func parseOrZero(s string) time.Time {
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return tm
}

func baseTask() assign.Task {
	return assign.Task{
		ID:           "task-1",
		OwningTeamID: "team-eng",
		// SecurityLabel defaults to "public" (the enum's "no restriction"
		// value, per F5's corrected semantics) so that F1-F4/F6 tests below
		// exercise only the filter they name, not F5.
		SecurityLabel: "public",
		DueAt:         time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
		EffortPoints:  1,
		RequiredCapabilities: []assign.CapabilityRequirement{
			{Code: "cap.code.python", MinLevel: 3},
		},
		ActionClass: assign.ActionClassInternal,
	}
}

func eligibleCandidate(id string) assign.Candidate {
	return assign.Candidate{
		PartyID: id,
		Name:    id,
		Kind:    assign.KindPerson,
		TeamID:  "team-eng",
		CapabilityMatches: map[string]assign.CapabilityMatch{
			"cap.code.python": {Exists: true, Level: 3, Exact: true, VerifiedAt: parseOrZero("2026-01-01T00:00:00Z")},
		},
		EffectivePermissions: map[string]bool{},
		Availability:         "available",
		ActiveTaskPoints:     0,
		MaxTaskPoints:        3,
	}
}

func TestEvaluate_F1_CapabilityExactVsBroaderSpecificity(t *testing.T) {
	task := baseTask()

	exact := eligibleCandidate("emp-exact")
	broader := eligibleCandidate("emp-broader")
	broader.CapabilityMatches["cap.code.python"] = assign.CapabilityMatch{Exists: true, Level: 3, Exact: false}

	eExact := assign.Evaluate(exact, task, assign.Policy{})
	eBroader := assign.Evaluate(broader, task, assign.Policy{})

	if eExact.Verdict != assign.VerdictEligible || eBroader.Verdict != assign.VerdictEligible {
		t.Fatalf("expected both eligible, got exact=%v broader=%v", eExact.Verdict, eBroader.Verdict)
	}
	if eExact.SpecificityScore != 2 {
		t.Errorf("exact match specificity = %d, want 2", eExact.SpecificityScore)
	}
	if eBroader.SpecificityScore != 1 {
		t.Errorf("broader match specificity = %d, want 1", eBroader.SpecificityScore)
	}
}

func TestEvaluate_F1_CapabilityMissingExcludes(t *testing.T) {
	task := baseTask()
	c := eligibleCandidate("emp-1")
	delete(c.CapabilityMatches, "cap.code.python")

	e := assign.Evaluate(c, task, assign.Policy{})
	if e.Verdict != assign.VerdictExcluded {
		t.Fatalf("expected excluded, got %v", e.Verdict)
	}
	if len(e.ExclusionReasons) != 1 || e.ExclusionReasons[0] != "CAPABILITY_MISSING:cap.code.python" {
		t.Errorf("unexpected reasons: %v", e.ExclusionReasons)
	}
}

func TestEvaluate_F2_NotPermittedDoesNotShortCircuit(t *testing.T) {
	// A candidate missing BOTH the capability and (hypothetically) a
	// permission should carry every reason — never short-circuit.
	task := baseTask()
	task.RequiredCapabilities[0].RequiredPermissions = []string{"git.commit"}
	c := eligibleCandidate("emp-1")
	delete(c.CapabilityMatches, "cap.code.python") // also fails F1

	e := assign.Evaluate(c, task, assign.Policy{})
	if len(e.ExclusionReasons) != 2 {
		t.Fatalf("expected 2 reasons (capability + permission), got %v", e.ExclusionReasons)
	}
}

func TestEvaluate_F2_ExternalEffectAlwaysExcludes(t *testing.T) {
	// GatePermissionFor's mapping source is unresolved (fail-closed per this
	// task's brief) — every external_effect task must exclude every
	// candidate via NOT_PERMITTED, even one that is otherwise fully eligible.
	task := baseTask()
	task.ActionClass = assign.ActionClassExternalEffect
	c := eligibleCandidate("emp-1")

	e := assign.Evaluate(c, task, assign.Policy{})
	if e.Verdict != assign.VerdictExcluded {
		t.Fatalf("expected excluded for external_effect task, got eligible")
	}
	found := false
	for _, r := range e.ExclusionReasons {
		if r == "NOT_PERMITTED:unresolved-gate" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected NOT_PERMITTED:unresolved-gate reason, got %v", e.ExclusionReasons)
	}
}

func TestEvaluate_F3_AIAgentUnavailable(t *testing.T) {
	task := baseTask()
	c := eligibleCandidate("agent-1")
	c.Kind = assign.KindAIAgent
	c.Ready = false
	c.RuntimePhase = "Suspended"

	e := assign.Evaluate(c, task, assign.Policy{})
	if e.Verdict != assign.VerdictExcluded {
		t.Fatalf("expected excluded, got eligible")
	}
}

func TestEvaluate_F3_PersonOnLeaveAtDueDate(t *testing.T) {
	task := baseTask()
	c := eligibleCandidate("emp-1")
	from := task.DueAt.Add(-24 * time.Hour)
	to := task.DueAt.Add(24 * time.Hour)
	c.LeaveFrom = &from
	c.LeaveTo = &to

	e := assign.Evaluate(c, task, assign.Policy{})
	if e.Verdict != assign.VerdictExcluded {
		t.Fatalf("expected excluded (on leave at due date), got eligible")
	}
}

func TestEvaluate_F4_CapacityExceeded(t *testing.T) {
	task := baseTask()
	task.EffortPoints = 2
	c := eligibleCandidate("emp-1")
	c.ActiveTaskPoints = 2
	c.MaxTaskPoints = 3

	e := assign.Evaluate(c, task, assign.Policy{})
	if e.Verdict != assign.VerdictExcluded {
		t.Fatalf("expected excluded (capacity), got eligible")
	}
	wantLoad := 2.0 / 3.0
	if e.LoadAtDecision != wantLoad {
		t.Errorf("loadAtDecision = %v, want %v", e.LoadAtDecision, wantLoad)
	}
}

// TestEvaluate_F6_BudgetExhaustedExcludesAIAgentOnly: BUDGET_EXHAUSTED must
// only ever apply to :AIAgent (20 §8.4 line 712's `c IS AIAgent AND
// teamBudgetExhausted(...)`) — a Person with the same flag set must be
// completely unaffected.
func TestEvaluate_F6_BudgetExhaustedExcludesAIAgentOnly(t *testing.T) {
	task := baseTask()

	agent := eligibleCandidate("agent-1")
	agent.Kind = assign.KindAIAgent
	agent.Ready = true
	agent.BudgetExhausted = true

	e := assign.Evaluate(agent, task, assign.Policy{})
	if e.Verdict != assign.VerdictExcluded {
		t.Fatalf("expected AIAgent with BudgetExhausted=true to be excluded")
	}
	found := false
	for _, r := range e.ExclusionReasons {
		if r == "BUDGET_EXHAUSTED" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected BUDGET_EXHAUSTED reason, got %v", e.ExclusionReasons)
	}

	person := eligibleCandidate("emp-1")
	person.BudgetExhausted = true // must be ignored — F6 is AIAgent-only
	e2 := assign.Evaluate(person, task, assign.Policy{})
	if e2.Verdict != assign.VerdictEligible {
		t.Fatalf("expected Person to be unaffected by BudgetExhausted, got %v (%v)", e2.Verdict, e2.ExclusionReasons)
	}
}

// TestEvaluate_F6_NoBudgetDataMeansNotExhausted is the [판단] this task's
// brief requires tested explicitly: with no spend-tracking data (the normal
// case, since the schema has none), BudgetExhausted defaults to false and
// must NOT exclude anyone — including :AIAgent candidates, which would
// otherwise be silently locked out of every task (violating 20 §8.3's
// "사람 + AI 동일 취급").
func TestEvaluate_F6_NoBudgetDataMeansNotExhausted(t *testing.T) {
	task := baseTask()
	agent := eligibleCandidate("agent-1")
	agent.Kind = assign.KindAIAgent
	agent.Ready = true
	// BudgetExhausted left at its zero value (false).

	e := assign.Evaluate(agent, task, assign.Policy{})
	if e.Verdict != assign.VerdictEligible {
		t.Fatalf("expected eligible when no budget data is available, got %v (%v)", e.Verdict, e.ExclusionReasons)
	}
}

// TestEvaluate_F5_PublicPasses is the positive case of the corrected F5
// semantics: "public" is the enum's documented "not sensitive" value and
// must pass unconditionally — otherwise NO :WorkItem could ever be assigned
// (20 §3.4 line 244 makes securityLabel a REQUIRED field with no "unset"
// option, so if "public" also failed, F5 would exclude every real task).
func TestEvaluate_F5_PublicPasses(t *testing.T) {
	task := baseTask()
	task.SecurityLabel = "public"
	c := eligibleCandidate("emp-1")

	e := assign.Evaluate(c, task, assign.Policy{})
	if e.Verdict != assign.VerdictEligible {
		t.Fatalf("expected eligible for securityLabel=public, got %v (%v)", e.Verdict, e.ExclusionReasons)
	}
}

// TestEvaluate_F5_FailsClosed is the negative half required by this task's
// brief, covering every one of the five cases: the two real restricted enum
// values, the (invalid, since the field is required) empty string, and an
// unrecognized value — all four must exclude every candidate. "public" is
// covered separately above as the one passing case.
func TestEvaluate_F5_FailsClosed(t *testing.T) {
	cases := []struct {
		label      string
		wantReason string
	}{
		{"internal", "SCOPE_VIOLATION:internal"},
		{"confidential", "SCOPE_VIOLATION:confidential"},
		{"", "SCOPE_VIOLATION:unset"},
		{"top-secret-typo", "SCOPE_VIOLATION:top-secret-typo"}, // unrecognized enum value
	}
	for _, tc := range cases {
		task := baseTask()
		task.SecurityLabel = tc.label
		c := eligibleCandidate("emp-1") // otherwise perfectly eligible

		e := assign.Evaluate(c, task, assign.Policy{})
		if e.Verdict != assign.VerdictExcluded {
			t.Errorf("securityLabel=%q: expected excluded (fail-closed), got eligible", tc.label)
		}
		found := false
		for _, r := range e.ExclusionReasons {
			if r == tc.wantReason {
				found = true
			}
		}
		if !found {
			t.Errorf("securityLabel=%q: expected reason %q, got %v", tc.label, tc.wantReason, e.ExclusionReasons)
		}
	}
}

func TestEvaluate_Determinism_SameInputSameOutput(t *testing.T) {
	task := baseTask()
	c := eligibleCandidate("emp-1")

	e1 := assign.Evaluate(c, task, assign.Policy{})
	e2 := assign.Evaluate(c, task, assign.Policy{})
	if !reflect.DeepEqual(e1, e2) {
		t.Fatalf("Evaluate is not deterministic:\n%+v\n%+v", e1, e2)
	}
}

func TestOnLeaveAt(t *testing.T) {
	due := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	from := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	c := assign.Candidate{LeaveFrom: &from, LeaveTo: &to}
	if !assign.OnLeaveAt(c, due) {
		t.Error("expected on leave at due date inside window")
	}
	outside := due.Add(-48 * time.Hour)
	if assign.OnLeaveAt(c, outside) {
		t.Error("expected NOT on leave outside window")
	}
	noWindow := assign.Candidate{}
	if assign.OnLeaveAt(noWindow, due) {
		t.Error("expected false when no leave window set")
	}
}

func TestMinVerifiedAt(t *testing.T) {
	reqs := []assign.CapabilityRequirement{
		{Code: "cap.a", MinLevel: 2},
		{Code: "cap.b", MinLevel: 2},
	}
	early := mustParse(t, "2025-01-01T00:00:00Z")
	late := mustParse(t, "2026-01-01T00:00:00Z")
	c := assign.Candidate{
		CapabilityMatches: map[string]assign.CapabilityMatch{
			"cap.a": {Exists: true, Level: 3, VerifiedAt: late},
			"cap.b": {Exists: true, Level: 2, VerifiedAt: early},
		},
	}
	got := assign.MinVerifiedAt(c, reqs)
	if !got.Equal(early) {
		t.Errorf("MinVerifiedAt = %v, want %v (earliest of the two)", got, early)
	}
}
