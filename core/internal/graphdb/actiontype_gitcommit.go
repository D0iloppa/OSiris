// git.commit's :ActionType row, as DATA — docs/design/80-action-layer.md
// §11's "구현 워커" ordering item ③: "git.commit 한 행을 데이터로 옮겨
// 10 §8.2 파이프라인이 일반 경로로 도는 것을 실증." This file is P2 stage
// ③'s item 1 deliverable: a single ActionTypeInput value, built from §4.6's
// worked-example table and §4.2's field definitions, that is meant to be
// run through validateActionTypeInput (this package, actiontype.go) —
// exactly like any :ActionType a real API caller would submit — proving §②
// 's graph-write validator accepts real data, not just its own unit tests'
// synthetic fixtures.
//
// THIS FILE DOES NOT CALL CreateActionType. No :ActionType node is written
// to any graph by this file — GitCommitActionType() only returns a Go
// value. Seeding a real row (calling CreateActionType from main.go's boot
// sequence, or from an API request) is a separate decision this narrow
// stage does not make — see this task's final report, item 9 ("인계
// 사항"), for why: seeding implies the row is live and reachable the moment
// osiris-core boots, and that decision belongs with whoever wires the
// approval round-trip this stage explicitly excludes (P4).
//
// SOURCES, FIELD BY FIELD (every value is either transcribed from a
// specific section of 80-action-layer.md, or marked [판단] — this
// implementation's own choice where the doc is silent; per this task's
// brief, nothing here is invented silently):
//
//	Field                   Value                          Source
//	ID / Code               "git.commit"                   §4.6 (id column), matches
//	                                                        SystemActionTypeIDs (actiontype_catalog.go)
//	Label / DescriptionMd   (below)                         [판단] — §4.6 has no label/description column
//	Origin                  "system"                        §4.6's table is "AIDevOps 확장 후" catalog of
//	                                                        code-registry-backed actions (§5.3.1); matches
//	                                                        SystemActionTypeIDs already listing "git.commit"
//	Status                  "active"                        [판단] — §4.2 lists status as required with no
//	                                                        stated initial value; "active" is chosen because
//	                                                        this task's target condition requires the row to
//	                                                        be dispatchable, and "draft"/"withdrawn"/"inert"
//	                                                        would each defeat that by construction
//	ParametersSchemaJSON    (below)                         [판단] — §4.6's table has no parameters column at
//	                                                        all; shaped to §4.4's allowed-keyword subset and
//	                                                        to carry exactly what this stage's two registered
//	                                                        predicates (predicate.diff.size_under,
//	                                                        predicate.branch.not_protected) and the one
//	                                                        registered effect (effect.scm.commit) read from a
//	                                                        call's resolved parameters — see the dispatch
//	                                                        package's doc comment for why parameter names are
//	                                                        shared verbatim between schema, predicates, and
//	                                                        effect instead of being remapped per-action
//	ParameterDefaultsJSON   {"max":1000}                    [판단], but not an arbitrary number — verbatim
//	                                                        the numeric example §5.3.1 itself gives for this
//	                                                        exact predicate: "predicate.diff.size_under(1000)"
//	ValidationPredicates    diff.size_under,                [판단, 문서 결정 아님] — see predicate.go's own doc
//	                        branch.not_protected            comment: §4.6 has no validationPredicates column,
//	                                                        this is this implementation's reading of §5.3.1's
//	                                                        two named examples as the natural minimum for a
//	                                                        commit-shaped action
//	Effects                 ["effect.scm.commit"]           §4.6 (effects column)
//	EffectClass             "external_effect"               §4.6
//	GateClass               "approval.team_lead"            §4.6
//	ApproverRole            "team-lead"                     [판단] — §4.2 requires this whenever
//	                                                        gateClass != none; §4.6's table has no
//	                                                        approverRole column. "team-lead" is chosen as the
//	                                                        literal vocabulary match to "approval.team_lead"
//	                                                        (§4.2: "gate가 team_lead면 approverRole도
//	                                                        team-lead" is not stated either, but no other
//	                                                        value in {team-lead,admin} is defensible here)
//	LimitJSON               "" (absent)                     [판단] — §4.6 names no amount/count limit for
//	                                                        git.commit; a diff-size limit already exists as a
//	                                                        parameter (max, above), not a §4.2 limitJson
//	TaintClass              "escalate"                      §4.6
//	Reversibility           "compensable"                   §4.6
//	RequiresCapability      ["stk.scm.repo.write"]          §4.6, key verified against
//	                                                        actiontype_capabilitykeys.go's static registry
//	                                                        (present — no CA8 rejection)
//	DerivedHash / IRI       "" (absent)                     N/A — DerivedHash is a composed-action concept
//	                                                        (§6.4/AC8) and this is an origin:'system' leaf row;
//	                                                        IRI is optional RDF-export metadata §4.2 never
//	                                                        requires
//	RequiresPermissionCodes ["git.commit"]                  §4.6 (REQUIRES_PERMISSION column) — see the
//	                                                        paragraph below for why this is now filled in
//	ComposedOf              nil                             N/A — git.commit is a leaf, not origin:'composed'
//
// *** [2026-08-13] G-A11 RESOLVED — RequiresPermissionCodes IS NOW
// ["git.commit"], MATCHING §4.6'S TABLE LITERALLY ***
//
// Earlier revisions of this file deliberately left RequiresPermissionCodes
// empty: actiontype_permission_mirror.go's CheckActionTypePermissionMirror
// used to refuse to boot the instant ANY
// (:ActionType)-[:REQUIRES_PERMISSION]->(:Permission) edge existed, because
// AD13/AC20's mirror-derivation formula (":Permission.gate = min of every
// :ActionType.gateClass requiring that permission") was flagged [미검증] by
// 80 §10 G-A11 — untested against catalog.go's actual consumption of
// :Permission.gate.
//
// That boot check has since been upgraded (this task) to actually compute
// and verify the min derivation instead of refusing outright — see
// actiontype_permission_mirror.go's package doc comment for the full
// derivation, the enum-mismatch judgment call it had to make, and the
// empty-set rule. With that in place, declaring git.commit's
// REQUIRES_PERMISSION edge here is safe: the boot check will actually
// verify it, not blindly reject it.
//
// CONSISTENCY CHECK PERFORMED: git.commit's GateClass is
// "approval.team_lead" (rank 1, gateClassRank in actiontype.go). It is the
// ONLY :ActionType this build declares that requires the "git.commit"
// :Permission (no other row in this codebase's data references it via
// RequiresPermissionCodes), so the min over its claiming set is trivially
// its own gateClass — rank 1, i.e. non-"none", which
// derivePermissionGate collapses to "approval". seed.go's SystemPermissions
// already carries `{Code: "git.commit", Gate: "approval", ApproverRole:
// "team-lead"}` (seed.go line ~84) — gate="approval" matches the derived
// value exactly. No mismatch. (approverRole is not compared by AC20 — see
// actiontype_permission_mirror.go's doc comment on why only gate has a
// stated formula — but note in passing that "team-lead" here and
// ApproverRole: "team-lead" on this row agree by construction, since both
// were chosen as the literal vocabulary match to "approval.team_lead"/
// "team_lead".)
//
// NOTE: this file still only returns a Go value — see below, unchanged —
// so this edge is not actually written to any graph by this file alone;
// the consistency check above describes what CheckActionTypePermissionMirror
// would find IF/WHEN a caller seeds this row via CreateActionType (still
// out of scope here, per this file's next paragraph).
package graphdb

// GitCommitActionType returns 80 §4.6's git.commit row as an
// ActionTypeInput value, ready to be passed to validateActionTypeInput
// (this package) or, with a live Neo4j session and a ByPartyID set, to
// CreateActionType. See this file's package-level doc comment for the full
// field-by-field source table and the one deliberate deviation from §4.6's
// literal table (RequiresPermissionCodes).
func GitCommitActionType() ActionTypeInput {
	return ActionTypeInput{
		ID:   "git.commit",
		Code: "git.commit",
		// [판단] — free-text; never consulted by any authorization or gating
		// decision (§4.2: "인가 판정에 절대 쓰이지 않는다").
		Label:         "Commit changes to a git repository",
		DescriptionMd: "Stages the current worktree changes and creates a commit, using osiris-core's own git identity (never the calling agent's).",

		Origin: "system",
		Status: "active",

		// [판단] §4.4 subset only: type/properties/required/pattern/
		// minLength/maxLength/minimum on string/integer properties. Every
		// property name here is deliberately the exact key the registered
		// predicates (params["max"], params["size"], params["branch"]) and
		// effect.scm.commit (params["repoPath"], params["branch"],
		// params["message"]) read — see action/dispatch's doc comment for
		// why this stage does not add a per-action parameter-name remapping
		// layer.
		ParametersSchemaJSON: `{` +
			`"type":"object",` +
			`"properties":{` +
			`"repoPath":{"type":"string","minLength":1,"maxLength":4096,"description":"absolute path to a local git worktree effect.scm.commit is allowed to touch"},` +
			`"branch":{"type":"string","minLength":1,"maxLength":255,"pattern":"^[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$","description":"branch that must already be checked out in repoPath"},` +
			`"message":{"type":"string","minLength":1,"maxLength":2000,"description":"commit message"},` +
			`"max":{"type":"integer","minimum":1,"description":"predicate.diff.size_under's upper bound (effective value, §6.5)"},` +
			`"size":{"type":"integer","minimum":0,"description":"the diff size actually measured for this call"}` +
			`},` +
			`"required":["repoPath","branch","message","max","size"]` +
			`}`,
		// [판단], value taken verbatim from §5.3.1's own worked example:
		// "predicate.diff.size_under(1000)".
		ParameterDefaultsJSON: `{"max":1000}`,

		// [판단, 문서 결정 아님] — see this file's package doc comment and
		// predicate.go's own doc comment.
		ValidationPredicates: []string{
			"predicate.diff.size_under",
			"predicate.branch.not_protected",
		},
		Effects: []string{"effect.scm.commit"}, // §4.6

		EffectClass:  "external_effect",    // §4.6
		GateClass:    "approval.team_lead", // §4.6
		ApproverRole: "team-lead",          // [판단] — see package doc comment
		LimitJSON:    "",

		TaintClass:    "escalate",    // §4.6
		Reversibility: "compensable", // §4.6

		RequiresCapability: []string{"stk.scm.repo.write"}, // §4.6

		DerivedHash: "",
		IRI:         "",

		// §4.6 REQUIRES_PERMISSION column — see package doc comment for why
		// this is now filled in (G-A11 resolved) and the min-derivation
		// consistency check performed against it.
		RequiresPermissionCodes: []string{"git.commit"},
		ComposedOf:              nil,

		ByPartyID: "", // caller-supplied at actual CreateActionType call time
	}
}
