// Package assign holds the DETERMINISTIC, LLM-FREE assignment core of P3
// (docs/design/20-ontology-schema.md §8, "결정적 배정 알고리즘"). §8's own
// opening line is the reason this is a separate, dependency-free package:
// "이 알고리즘은 LLM을 호출하지 않는다. 실행되는 프로세스(osiris-core)에는
// claude 바이너리도 게이트웨이 자격증명도 없다. 런타임 토큰 비용 0." Nothing
// in this package imports a graph driver, an HTTP client, or a gateway
// client — it is pure computation over Go structs, mirroring this
// repository's existing "pure + fakes" test convention (core/internal/
// action/dispatch's predicate/effect split, core/internal/genesis's
// interface stubs) rather than introducing Neo4j/testcontainers here.
//
// The three-way split this task's brief requires:
//   - THIS package (assign): Evaluate (F1-F5), Comparator (tie-break, §8.5),
//     MinimalCover/fallback classification (§8.6) — all pure functions over
//     Task/Candidate/Policy. RuntimeState is not a separate type: its fields
//     (ready, activeTaskPoints, maxTaskPoints, availability) are folded
//     directly onto Candidate, exactly as §8.1 anticipates ("RuntimeState는
//     맵 주입 가능") — a test can construct a Candidate by hand with whatever
//     RuntimeState-shaped fields it wants, with no graph involved.
//   - core/internal/graphdb's CandidateSet / LoadTaskForAssignment /
//     LoadActivePolicy: the graph QUERIES (§8.3) that populate Task and
//     []Candidate from Neo4j.
//   - core/internal/graphdb's PersistDecision / CreateWorkItem: the graph
//     WRITES (§8.7) that turn an Evaluation slice + a winner into
//     :AssignmentDecision / :CONSIDERED / :Assignment / :WorkItem.status.
package assign

import "time"

// ActionClass mirrors :WorkItem.actionClass (20 §3.4).
type ActionClass string

const (
	ActionClassInternal       ActionClass = "internal"
	ActionClassExternalEffect ActionClass = "external_effect"
)

// PartyKind mirrors the two :Party subtypes assign() ever considers (20
// §8.3: "사람 + AI 동일 취급, P1" — :Team and :Organization are never
// candidates).
type PartyKind string

const (
	KindPerson  PartyKind = "Person"
	KindAIAgent PartyKind = "AIAgent"
)

// CapabilityRequirement is one row of Task.requiredCapabilities (20 §8.1,
// line 611) plus RequiredPermissions — the §7.2 REQUIRES_PERMISSION union
// for this specific capability code. RequiredPermissions is populated by the
// graph layer (graphdb.LoadTaskForAssignment) because it is catalog data,
// not something Evaluate can derive on its own; folding it onto the
// requirement (rather than passing a second graph-shaped argument into
// Evaluate) is what keeps Evaluate a pure function of (Candidate, Task,
// Policy) alone.
type CapabilityRequirement struct {
	Code                string
	MinLevel            int
	Weight              float64
	RequiredPermissions []string // §7.2 union for this capability code
}

// Task is 20 §8.1's Task struct.
//
// NOTE on OwningTeamID: §8.1 line 610 writes "Task.owningTeamId" as a scalar,
// but :WorkItem (20 §3.4, properties table lines 234-246) has NO such
// property — team ownership is exclusively the relationship
// (:WorkItem)-[:OWNED_BY]->(:Team) (line 247). This is a pure syntax
// substitution, not a design decision: OwningTeamID here is the team id
// ALREADY RESOLVED by following OWNED_BY (done by
// graphdb.LoadTaskForAssignment), not a WorkItem property read directly.
type Task struct {
	ID                   string
	OwningTeamID         string // "" if the WorkItem has no OWNED_BY edge
	DueAt                time.Time
	EffortPoints         int
	RequiredCapabilities []CapabilityRequirement
	ActionClass          ActionClass
	// SecurityLabel is 20 §3.4's :WorkItem.securityLabel (public / internal /
	// confidential). See evaluate.go's F5 comment for how "" vs non-"" is
	// treated — that treatment is a scope decision handed down for this task,
	// not something invented here.
	SecurityLabel string
	ExecutionMode string
}

// CapabilityMatch is the result of resolving one CapabilityRequirement
// against one Candidate's HAS_CAPABILITY holdings, including the
// (:Capability)-[:BROADER*0..3]->(:Capability) closure (20 §8.4's
// `maxLevel(c HAS_CAPABILITY hc WHERE hc-[:BROADER*0..3]->req.code)`). This
// closure walk is graph-shaped (variable-length path over catalog data), so
// graphdb.CandidateSet computes it once per (candidate, requirement) pair and
// hands Evaluate the answer — Evaluate itself never walks BROADER.
type CapabilityMatch struct {
	Exists     bool      // candidate holds ANY capability in the BROADER closure of req.Code
	Level      int       // best (max) level found
	Exact      bool      // the matching capability's own code == req.Code (vs. a broader ancestor)
	VerifiedAt time.Time // HAS_CAPABILITY.verifiedAt of the matching edge; zero value if unknown
}

// Candidate is one row of §8.3's candidateSet output, decorated with every
// raw fact §8.4/§8.5 need. Every field here is either read directly off a
// graph node/relationship, or (CapabilityMatches, EffectivePermissions,
// HasWorkedOnAncestor, NextFreeAt) computed once by the graph layer so that
// Evaluate/Comparator/MinimalCover stay pure.
type Candidate struct {
	PartyID string
	Name    string
	Kind    PartyKind
	TeamID  string // team id from this party's current IN_TEAM membership ("" if none, e.g. org-only member)

	// F1 — one entry per Task.RequiredCapabilities[i].Code
	CapabilityMatches map[string]CapabilityMatch

	// F2 — the party's effective(party) set (§7.1), as a set for O(1) lookup
	EffectivePermissions map[string]bool

	// F3 — availability
	Ready          bool   // AIAgent only: :AgentRuntime.ready (stale >60s already folded to false by the graph layer, §8.1 line 618)
	RuntimePhase   string // AIAgent only: :AgentRuntime.phase
	NotReadyReason string // AIAgent only: best-effort reason string for the UNAVAILABLE reason code
	Availability   string // Person only: :Person.availability
	LeaveFrom      *time.Time
	LeaveTo        *time.Time

	// F4 — capacity
	ActiveTaskPoints int
	MaxTaskPoints    int

	// F6 — AIAgent-only team budget check (20 §8.4 lines 711-713,
	// `teamBudgetExhausted(teamOf(c))`). Populated by the graph layer;
	// Evaluate only ever reads it for candidates where Kind == KindAIAgent.
	// See graphdb.CandidateSet's doc comment for why this is currently
	// ALWAYS false (no spend-tracking field exists in the graph to compute
	// it from — 20 §12 excludes cost time-series from the graph entirely).
	BudgetExhausted bool

	// tie-break inputs (§8.5)
	HasWorkedOnAncestor bool      // [판단] see graphdb's resolver doc comment for the conservative definition used
	VerificationRecency time.Time // minVerifiedAt(c, task.requiredCapabilities) — zero value if no requirement matched

	// FB3 (§8.6) input: when this candidate is next expected to free capacity.
	// nil if unknown / not computable (e.g. no active assignments at all).
	NextFreeAt *time.Time
}

// Verdict is §8.4's Eval.verdict.
type Verdict string

const (
	VerdictEligible Verdict = "eligible"
	VerdictExcluded Verdict = "excluded"
)

// Evaluation is §8.4's Eval record plus Rank (filled in after tie-break,
// §8.2 line 641) — this is exactly the shape §8.7's persistDecision writes
// onto the [:CONSIDERED] edge (20 §3.5 lines 306-320), field for field.
type Evaluation struct {
	PartyID     string
	PartyKind   PartyKind
	DisplayName string

	Verdict             Verdict
	ExclusionReasons    []string // ALL reasons, never short-circuited (§8.2's own instruction)
	MatchedCapabilities []string
	MissingCapabilities []string

	SpecificityScore    int
	LoadAtDecision      float64
	SameTeam            bool
	ContextContinuity   bool
	VerificationRecency time.Time

	Rank int // 0 = unranked (excluded candidates keep Rank 0, persisted as "no rank")
}

// Policy is :AssignmentPolicy (20 §3.5 lines 335-345), already read from the
// graph by graphdb.LoadActivePolicy.
type Policy struct {
	Version                   int
	EffectiveFrom             time.Time
	TieBreakChain             []string
	TeamStrict                bool
	LevelRelaxationAllowed    bool
	MaxCandidatesForDecompose int
}

// Outcome is :AssignmentDecision.outcome (20 §3.5 line 296).
type Outcome string

const (
	OutcomeAssigned          Outcome = "assigned"
	OutcomeDegraded          Outcome = "degraded"
	OutcomeDecomposeRequired Outcome = "decompose_required"
	OutcomeEscalatedLead     Outcome = "escalated_lead"
	OutcomeEscalatedAdmin    Outcome = "escalated_admin"
	OutcomeUnassignable      Outcome = "unassignable"
)

// AlgorithmVersion is :AssignmentDecision.algorithmVersion (20 §3.5 line
// 293, "배정 코드 버전"). Bump this when Evaluate/Comparator/fallback logic
// changes in a way that could change a past decision's outcome if re-run.
const AlgorithmVersion = "assign-1.0.0"
