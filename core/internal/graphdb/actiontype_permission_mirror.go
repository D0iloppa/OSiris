// AC20 boot check — docs/design/80-action-layer.md §8.1 (AD13) and §9 AC20:
// ":Permission.gate/approverRole/limitJson은 :ActionType에서 파생된 미러이며
// 직접 쓰기 금지. 부팅 시 불일치면 부팅 실패 (AD13, I9와 같은 등급)."
//
// [2026-08-13] G-A11 CLOSURE — this file now computes and checks the ACTUAL
// min-of-gateClass derivation, not just "does any REQUIRES_PERMISSION edge
// exist". See docs/design/80-action-layer.md §10 (G-A11 marked 해소) for the
// closure record and this task's final report for the full audit trail
// (zero readers of orchestrator's Grant.Gate / zero readers of
// :Permission.gate in any authorization decision, confirmed by
// bin/lint-action-layer.sh's new AC20 section).
//
// *** WHAT §8.1/AD13 ACTUALLY SPECIFIES, AND WHAT IT DOES NOT ***
//
// §8.1 gives an explicit derivation formula for exactly ONE of the three
// mirrored fields:
//
//	":Permission.gate = 그 코드를 요구하는 모든 :ActionType의 gateClass의 min"
//
// It gives NO formula for approverRole or limitJson — no arithmetic, no
// "take the approverRole of the min-gateClass ActionType", nothing. 80 §10's
// gap list is explicit that only the gate formula was even argued, and this
// task's brief is explicit: "문서가 정하지 않았으면 그 필드는 대조하지 않고
// 갭으로 보고하라 (추측으로 규칙을 만들지 마라)". So: this function checks
// `gate` against the derived min. It does NOT check approverRole or
// limitJson — there is no rule to check them against, and inventing one here
// would be exactly the "second undocumented derivation" this design
// explicitly rejects (see internal/assign/evaluate.go's GatePermissionFor
// doc comment, which independently arrives at the same "do not invent a
// mapping" conclusion for a different function). This is left as an
// explicit, named gap — see this task's final report item 3.
//
// *** 3값 → 2값 축소 규칙: 이전 [판단], 이제 `80-action-layer.md §8.1.1` 근거 ***
//
// §4.2 defines :ActionType.gateClass as a THREE-valued enum: "none" /
// "approval.team_lead" / "approval.admin" (actionTypeGateClasses,
// actiontype.go). But docs/design/20-ontology-schema.md's own :Permission
// node table (§7.2-adjacent, line 218) defines :Permission.gate as a
// TWO-valued enum: "none" / "approval" — and this build's grant-catalog
// mirrors of that field (orchestrator/internal/catalog/catalog.go's
// Grant.Gate, core/internal/graphdb/seed.go's permSeed.Gate /
// SystemPermissions) are both two-valued in exactly that way today. §8.1
// says "min of gateClass" but never states how a three-valued min collapses
// into the two-valued :Permission.gate domain. This collapse rule is now
// documented explicitly in `80-action-layer.md §8.1.1 [2026-08-15 명문화]`.
//
// The collapse rule (previously a judgment call, now documented):
//
//	min-rank == gateClassRank["none"] (0)  =>  derived gate = "none"
//	min-rank >  0                          =>  derived gate = "approval"
//
// This is justified by §8.1's own rationale: the decision :Permission.gate
// makes is binary — "expose this tool via MCP gate or plain Bash" — not
// "which of three approval tiers". The two-tier distinction is preserved in
// :ActionType.approverRole (per-action), not lost. The collapse uses the SAME
// rank order AC11/DG8 already use elsewhere in this package (gateClassRank,
// actiontype.go) — no new ordering is invented.
//
// *** THE EMPTY-SET CASE ***
//
// A :Permission that NO :ActionType currently requires has nothing to
// derive a mirror value FROM — min over the empty set is undefined. This
// file's rule: leave such a :Permission's gate/approverRole/limitJson
// exactly as grant-catalog (SystemPermissions/seedPermissions) set it, i.e.
// do not include it in the comparison at all. Rationale: the mirror's whole
// point is "derived FROM claiming ActionTypes"; if none exist, the
// authoritative source (§8.1's own words: "권위: :ActionType.gateClass") has
// not spoken, so grant-catalog's pre-existing value is not a derivation
// failure to flag — it is simply not yet superseded. This falls out
// naturally below: the Cypher only visits :Permission nodes that HAVE an
// incoming REQUIRES_PERMISSION edge from an :ActionType, so an unclaimed
// :Permission is never fetched, never compared, and cannot fail this check.
//
// *** RECOMPUTATION TRIGGER — LEFT AS A GAP, ON PURPOSE ***
//
// This task's brief asked whether 80 §8.2's table specifies what must
// happen to the mirror when an :ActionType changes. It does not — §8.2's
// table ("파이프라인의 일반화") describes the git-commit-approval pipeline's
// stage-by-stage behavior, not a mirror-recomputation algorithm, and no
// other section of 80 gives one either (AC19 only requires :OrgRevision to
// bump by exactly 1 per :ActionType write; it says nothing about eagerly
// re-deriving :Permission fields on that write). Per this task's explicit
// instruction ("구체 알고리즘이 문서에 없으면 이번 범위에서 구현하지 말고
// 갭으로 남겨라"), this file does NOT add an eager recompute-on-write hook
// to CreateActionType/UpdateActionType. The boot-time fail-closed check
// below is judged sufficient for this stage: any drift between a live
// :ActionType.gateClass and its :Permission.gate mirror — however it
// arose — is caught the next time osiris-core boots, and boots do not
// happen rarely enough in this system's deployment model for that latency
// to matter more than the scope discipline of not inventing a second
// mechanism. See final report item 5.
package graphdb

import (
	"context"
	"fmt"
	"sort"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// permissionGateMirrorRow is one :Permission that at least one :ActionType
// claims via REQUIRES_PERMISSION, plus everything needed to explain a
// mismatch without a second query.
type permissionGateMirrorRow struct {
	code        string
	actualGate  string
	gateClasses []string // distinct :ActionType.gateClass values claiming this permission
	actionIDs   []string // distinct :ActionType.id values claiming this permission (for the error message)
}

// CheckActionTypePermissionMirror is AC20's boot-time gate. Call it the same
// way main.go calls ReconcileActionTypes — once, at boot, before the readyz
// prober goes live — and treat a non-nil error as fatal (same grade as I9,
// per AC20's "부팅 실패").
//
// It checks ONLY the `gate` field, against the min-of-gateClass formula
// §8.1 actually specifies (collapsed to :Permission.gate's two-valued
// domain — see this file's package doc comment). approverRole and
// limitJson are NOT checked: §8.1 gives no derivation rule for either, and
// this task's brief forbids inventing one. Permissions with zero claiming
// :ActionType rows are not fetched and therefore cannot fail this check —
// see the package doc comment's "EMPTY-SET CASE" section.
func CheckActionTypePermissionMirror(ctx context.Context, c *Client) error {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, `
			MATCH (a:ActionType)-[:REQUIRES_PERMISSION]->(p:Permission)
			WITH p, collect(DISTINCT a.gateClass) AS gateClasses, collect(DISTINCT a.id) AS actionIds
			RETURN p.code AS code, p.gate AS actualGate, gateClasses, actionIds
			ORDER BY p.code
		`, nil)
		if err != nil {
			return nil, err
		}
		records, err := result.Collect(ctx)
		if err != nil {
			return nil, err
		}

		rows := make([]permissionGateMirrorRow, 0, len(records))
		for _, rec := range records {
			codeV, _ := rec.Get("code")
			gateV, _ := rec.Get("actualGate")
			gateClassesV, _ := rec.Get("gateClasses")
			actionIdsV, _ := rec.Get("actionIds")

			row := permissionGateMirrorRow{
				code:       fmt.Sprintf("%v", codeV),
				actualGate: fmt.Sprintf("%v", gateV),
			}
			if gcs, ok := gateClassesV.([]any); ok {
				for _, gc := range gcs {
					row.gateClasses = append(row.gateClasses, fmt.Sprintf("%v", gc))
				}
			}
			if ids, ok := actionIdsV.([]any); ok {
				for _, id := range ids {
					row.actionIDs = append(row.actionIDs, fmt.Sprintf("%v", id))
				}
			}
			rows = append(rows, row)
		}
		return rows, nil
	})
	if err != nil {
		return fmt.Errorf("AC20 boot check (:ActionType-REQUIRES_PERMISSION->:Permission mirror): %w", err)
	}

	rows, _ := res.([]permissionGateMirrorRow)
	if len(rows) == 0 {
		// No :ActionType currently claims any :Permission. Nothing to
		// derive from — normal, expected state until stage ④ seeds rows
		// with REQUIRES_PERMISSION edges. See package doc comment's
		// EMPTY-SET CASE.
		return nil
	}

	var mismatches []string
	for _, row := range rows {
		derived := derivePermissionGate(row.gateClasses)
		if derived == "" {
			// One of the collected gateClass values isn't in gateClassRank
			// at all — this should be structurally impossible (graph-write
			// only ever accepts actionTypeGateClasses' three values), but
			// fail closed rather than silently skip if it ever happens.
			sort.Strings(row.gateClasses)
			mismatches = append(mismatches, fmt.Sprintf(
				"permission %q: claiming ActionType(s) %v carry unrecognized gateClass value(s) %v — cannot derive a mirror value",
				row.code, row.actionIDs, row.gateClasses))
			continue
		}
		if derived != row.actualGate {
			sort.Strings(row.gateClasses)
			mismatches = append(mismatches, fmt.Sprintf(
				"permission %q: stored gate=%q, but derived (min of claiming ActionType gateClass %v via %v) = %q",
				row.code, row.actualGate, row.gateClasses, row.actionIDs, derived))
		}
	}

	if len(mismatches) > 0 {
		return fmt.Errorf("AC20: :Permission.gate mirror is out of sync with :ActionType.gateClass (AD13's min derivation) — refusing to boot: %v", mismatches)
	}

	return nil
}

// derivePermissionGate implements §8.1's "min of every claiming ActionType's
// gateClass", collapsed onto :Permission.gate's two-valued domain
// ("none"/"approval") — see this file's package doc comment for why that
// collapse is necessary and what judgment call it represents. Returns "" if
// gateClasses is empty (should not happen — callers only invoke this with
// at least one claiming ActionType) or contains a value gateClassRank does
// not recognize.
func derivePermissionGate(gateClasses []string) string {
	if len(gateClasses) == 0 {
		return ""
	}
	minRank := -1
	for _, gc := range gateClasses {
		rank, ok := gateClassRank[gc]
		if !ok {
			return ""
		}
		if minRank == -1 || rank < minRank {
			minRank = rank
		}
	}
	if minRank == gateClassRank["none"] {
		return "none"
	}
	return "approval"
}
