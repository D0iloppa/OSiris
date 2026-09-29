package dispatch_test

// P2 stage ③, this task's item 5 — "실증 테스트": the general path
// (predicate registry lookup -> predicate evaluation -> gateFor -> effect
// registry lookup -> effect execution) is driven by DATA, not by branching
// on git.commit's name, and both required negative (fail-closed) paths
// hold.
//
// HONEST LIMIT (this task's brief requires this be stated in the test file
// too, not just the final report): there is no Neo4j here. LoadedActionType
// values below are built BY HAND to mirror what a real graph read would
// project from graphdb.GitCommitActionType()'s row (this file imports that
// function for the ID-bearing fields so the two never silently drift, but
// the GateClass/TaintClass/ValidationPredicates/Effects copy is manual —
// see TestMain-adjacent comments below for exactly which fields are
// cross-checked vs. duplicated). No graphdb.Client, no live graph-write
// round trip, no ReconcileActionTypes boot sweep runs in this test. This
// mirrors this repository's existing precedent (internal/genesis/
// genesis_test.go's fakeOrgReader) rather than introducing a
// testcontainers/docker Neo4j dependency.
import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"osiris-core/internal/action/dispatch"
	"osiris-core/internal/action/effect"
	"osiris-core/internal/action/predicate"
	"osiris-core/internal/graphdb"
)

// fakeSnapshot is predicate.GraphSnapshot's test double — the "graph read is
// fake" honesty callout applies here too: predicate.branch.not_protected
// would, in a real system, ask Neo4j (or wherever branch-protection facts
// live) whether a branch is protected. Here it is a hardcoded map.
type fakeSnapshot struct {
	protected map[string]bool
}

func (f fakeSnapshot) BranchProtected(_ context.Context, branch string) (bool, bool) {
	p, known := f.protected[branch]
	return p, known
}

// gitCommitLoadedActionType projects the dispatch-relevant fields of
// graphdb.GitCommitActionType()'s row into a dispatch.LoadedActionType.
// GateClass/TaintClass/ValidationPredicates/Effects are transcribed by hand
// from that function's return value (see the assertions in
// TestGitCommitLoadedActionTypeMatchesGraphdbRow below, which fail loudly
// the moment this copy drifts from the real row instead of silently
// testing a stale fixture).
func gitCommitLoadedActionType(status string) dispatch.LoadedActionType {
	row := graphdb.GitCommitActionType()
	return dispatch.LoadedActionType{
		ID:                   row.ID,
		Status:               status,
		ValidationPredicates: row.ValidationPredicates,
		Effects:              row.Effects,
		GateClass:            row.GateClass,
		TaintClass:           row.TaintClass,
	}
}

// TestGitCommitLoadedActionTypeMatchesGraphdbRow guards the manual
// projection above against drift from actiontype_gitcommit.go — if someone
// changes GitCommitActionType()'s GateClass/TaintClass/effects/predicates
// without updating this test file, THIS test (not the ones exercising
// Dispatch's behavior) is the one that fails, keeping the "why does gate
// come out approval.team_lead" assertions below anchored to the real row.
func TestGitCommitLoadedActionTypeMatchesGraphdbRow(t *testing.T) {
	row := graphdb.GitCommitActionType()
	if row.GateClass != "approval.team_lead" {
		t.Fatalf("git.commit row GateClass = %q, want %q (80 §4.6) — update this test file's assumptions if this changed intentionally", row.GateClass, "approval.team_lead")
	}
	if row.TaintClass != "escalate" {
		t.Fatalf("git.commit row TaintClass = %q, want %q (80 §4.6)", row.TaintClass, "escalate")
	}
	if len(row.Effects) != 1 || row.Effects[0] != "effect.scm.commit" {
		t.Fatalf("git.commit row Effects = %v, want [effect.scm.commit] (80 §4.6)", row.Effects)
	}
}

// setupTempRepo creates a throwaway git repository under t.TempDir() —
// NEVER under the OSiris workspace checkout itself. Returns the repo
// path and the branch checked out in it.
func setupTempRepo(t *testing.T) (repoPath, branch string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH — cannot exercise effect.scm.commit")
	}
	dir := t.TempDir()
	branch = "work"
	run := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", dir}, args...)
		cmd := exec.Command("git", full...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-q", "-m", "initial")
	run("checkout", "-q", "-b", branch)
	return dir, branch
}

func headSHA(t *testing.T, repoPath string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse HEAD failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestDispatch_GitCommitRow_RequiresApproval_NeverExecutes is the negative
// half of this task's target condition read literally: git.commit's OWN
// documented gateClass is approval.team_lead (80 §4.6), unconditionally —
// even at taint level t0 (max(approval.team_lead, none) = approval.team_lead,
// §6.2's join). So the real git.commit row, run through the exact same
// generic Dispatch function used everywhere else in this test file, can
// NEVER reach the "effect executes" branch by itself. This is not a bug in
// Dispatch — it is git.commit's actual designed behavior (a commit always
// needs a human gate) and is exactly why this task's brief separately asks
// for a "게이트가 불필요한 경우" scenario using DIFFERENT data further down
// (TestDispatch_NoGateVariant_ExecutesRealCommit) rather than expecting
// git.commit's own row to ever skip its gate.
func TestDispatch_GitCommitRow_RequiresApproval_NeverExecutes(t *testing.T) {
	repoPath, branch := setupTempRepo(t)
	beforeSHA := headSHA(t, repoPath)

	a := gitCommitLoadedActionType("active")
	params := map[string]any{
		"repoPath": repoPath,
		"branch":   branch,
		"message":  "should never land",
		"max":      1000,
		"size":     10,
	}
	snap := fakeSnapshot{protected: map[string]bool{branch: false}}

	out, err := dispatch.Dispatch(context.Background(), a, params, snap, "t0", predicate.Lookup, effect.Lookup)
	if err != nil {
		t.Fatalf("Dispatch = %v, want nil error (gate-required is not a failure)", err)
	}
	if out.Gate != "approval.team_lead" {
		t.Fatalf("Outcome.Gate = %q, want %q", out.Gate, "approval.team_lead")
	}
	if out.Executed {
		t.Fatalf("Outcome.Executed = true, want false — a gated call must not run its effects in this stage (no approval round trip exists yet)")
	}
	if got := headSHA(t, repoPath); got != beforeSHA {
		t.Fatalf("HEAD changed from %s to %s — a commit was made despite the call being gated", beforeSHA, got)
	}
}

// TestDispatch_NoGateVariant_ExecutesRealCommit is this task's core positive
// proof: predicate lookup+evaluation -> gateFor -> effect lookup+execution,
// entirely data-driven, ending in a REAL commit inside a throwaway repo.
//
// The LoadedActionType here is NOT graphdb.GitCommitActionType()'s row —
// git.commit's own gateClass (approval.team_lead) can never resolve to
// "none" (see the test above), so reaching the "effect actually runs"
// branch requires a row whose GateClass is "none". This is a test-only
// fixture: same effect (effect.scm.commit), same predicates, same taint
// class shape §4.6 itself uses elsewhere (pr.create is
// gateClass:none/taintClass:escalate/effectClass:external_effect — an
// identical combination, just with a different effect) — never written to
// any graph, not part of graphdb.SystemActionTypeIDs, and not a substitute
// for git.commit's real row. It exists solely to exercise the branch of the
// GENERIC dispatcher (same Dispatch function, same registries) that a
// gate-free action takes — proving the effect-execution path is real and
// wired correctly, without contradicting §4.6's documented gate for
// git.commit itself.
func TestDispatch_NoGateVariant_ExecutesRealCommit(t *testing.T) {
	repoPath, branch := setupTempRepo(t)

	if err := os.WriteFile(filepath.Join(repoPath, "hello.txt"), []byte("hello from stage 3\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	a := dispatch.LoadedActionType{
		ID:                   "test.internal.gate_free_commit_demo",
		Status:               "active",
		ValidationPredicates: []string{"predicate.diff.size_under", "predicate.branch.not_protected"},
		Effects:              []string{"effect.scm.commit"},
		GateClass:            "none",
		TaintClass:           "escalate",
	}
	params := map[string]any{
		"repoPath": repoPath,
		"branch":   branch,
		"message":  "stage 3 narrow-verification commit",
		"max":      1000,
		"size":     10,
	}
	snap := fakeSnapshot{protected: map[string]bool{branch: false}}

	out, err := dispatch.Dispatch(context.Background(), a, params, snap, "t0", predicate.Lookup, effect.Lookup)
	if err != nil {
		t.Fatalf("Dispatch = %v, want nil", err)
	}
	if out.Gate != "none" {
		t.Fatalf("Outcome.Gate = %q, want %q", out.Gate, "none")
	}
	if !out.Executed {
		t.Fatalf("Outcome.Executed = false, want true")
	}
	if len(out.Results) != 1 || out.Results[0].Kind != "commit" {
		t.Fatalf("Outcome.Results = %+v, want one commit artifact", out.Results)
	}
	gotSHA := out.Results[0].URI
	if gotSHA != headSHA(t, repoPath) {
		t.Fatalf("Artifact.URI = %q, does not match repo HEAD %q", gotSHA, headSHA(t, repoPath))
	}
	// Confirm the commit really carries the message and the file.
	out2, err := exec.Command("git", "-C", repoPath, "log", "-1", "--format=%s").CombinedOutput()
	if err != nil {
		t.Fatalf("git log failed: %v\n%s", err, out2)
	}
	if strings.TrimSpace(string(out2)) != "stage 3 narrow-verification commit" {
		t.Fatalf("commit message = %q, want the message passed to Dispatch", strings.TrimSpace(string(out2)))
	}
}

// TestDispatch_PredicateFailure_EffectNeverRuns is the "술어가 거짓을
// 반환하면 이펙트가 실행되지 않는다" negative test this task's brief
// requires. Uses the same gate-free fixture as above so the ONLY thing
// under test is the predicate short-circuit, not gating.
func TestDispatch_PredicateFailure_EffectNeverRuns(t *testing.T) {
	repoPath, branch := setupTempRepo(t)
	beforeSHA := headSHA(t, repoPath)
	if err := os.WriteFile(filepath.Join(repoPath, "should-not-be-committed.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	a := dispatch.LoadedActionType{
		ID:                   "test.internal.gate_free_commit_demo",
		Status:               "active",
		ValidationPredicates: []string{"predicate.diff.size_under", "predicate.branch.not_protected"},
		Effects:              []string{"effect.scm.commit"},
		GateClass:            "none",
		TaintClass:           "escalate",
	}
	params := map[string]any{
		"repoPath": repoPath,
		"branch":   branch,
		"message":  "must not land",
		"max":      1000,
		// size >= max: predicate.diff.size_under must fail.
		"size": 999999,
	}
	snap := fakeSnapshot{protected: map[string]bool{branch: false}}

	out, err := dispatch.Dispatch(context.Background(), a, params, snap, "t0", predicate.Lookup, effect.Lookup)
	if err == nil {
		t.Fatalf("Dispatch = nil error, want ErrPredicateFailed")
	}
	if out.Executed {
		t.Fatalf("Outcome.Executed = true, want false — predicate failure must block the effect")
	}
	if got := headSHA(t, repoPath); got != beforeSHA {
		t.Fatalf("HEAD changed from %s to %s — the effect ran despite a failed predicate", beforeSHA, got)
	}
}

// TestDispatch_UnknownEffect_FailsClosed is the "미지 이펙트 이름을 가진
// 행은 디스패치가 거부된다(fail-closed, AC3의 런타임 절반)" negative test
// this task's brief requires.
func TestDispatch_UnknownEffect_FailsClosed(t *testing.T) {
	a := dispatch.LoadedActionType{
		ID:                   "test.internal.unknown_effect_demo",
		Status:               "active",
		ValidationPredicates: nil,
		Effects:              []string{"effect.does.not.exist"},
		GateClass:            "none",
		TaintClass:           "neutral",
	}
	out, err := dispatch.Dispatch(context.Background(), a, map[string]any{}, nil, "t0", predicate.Lookup, effect.Lookup)
	if err == nil {
		t.Fatalf("Dispatch = nil error, want ErrUnknownEffect")
	}
	if out.Executed {
		t.Fatalf("Outcome.Executed = true, want false")
	}
}

// TestDispatch_NotActive_FailsClosed exercises the Status guard.
func TestDispatch_NotActive_FailsClosed(t *testing.T) {
	a := dispatch.LoadedActionType{
		ID:     "test.internal.inert_demo",
		Status: "inert",
	}
	out, err := dispatch.Dispatch(context.Background(), a, map[string]any{}, nil, "t0", predicate.Lookup, effect.Lookup)
	if err == nil {
		t.Fatalf("Dispatch = nil error, want ErrNotActive")
	}
	if out.Executed {
		t.Fatalf("Outcome.Executed = true, want false")
	}
}
