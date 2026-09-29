// SystemActionTypeIDs — the "실행 바이너리의 액션 레지스트리" AC2 speaks of:
// docs/design/80-action-layer.md §9 AC2 ("origin:'system' 노드는 실행
// 바이너리의 액션 레지스트리에 같은 id가 존재한다") and §4.6's worked-example
// catalog table (AIDevOps 확장 후).
//
// SCOPE BOUNDARY (this task's brief, item 5 — read before adding a seeding
// call here): this file defines the CONSTANT ID LIST ONLY. It does not seed
// a single :ActionType row into the graph, and main.go (after this task's
// edit) does not call graphdb.CreateActionType for any of these ids either.
// The list exists so cmd/osiris-core/main.go can pass a REAL knownActionIDs
// set into ReconcileActionTypes (AC2's boot-time inert-degrade sweep,
// actiontype_reconcile.go) instead of the empty set that file's own doc
// comment already flagged as "stage ②'s job to fill in" — that wiring is
// this task's item 5, nothing more.
//
// WHY NOT SEED THESE ROWS NOW: this task's brief is explicit that "실제로
// 그래프에 시드하는 행은 최소로 유지하라 — git.commit 한 행의 데이터화는
// ③단계의 몫이다." Populating :ActionType rows requires deciding real
// parametersSchemaJson/validationPredicates/effects/requiresCapability
// values per row and exercising CreateActionType end-to-end (exactly what
// stage ③'s "git.commit 한 행을 데이터로 옮겨 파이프라인이 일반 경로로 도는
// 것을 실증" is for) — doing that here, untested, for eleven more rows at
// once would be "층을 검증 없이 쌓는" exactly what §11 warns against.
//
// Until stage ③ (or later) actually calls CreateActionType for these ids,
// there are ZERO :ActionType{origin:'system'} rows in the graph — so this
// list changes what ReconcileActionTypes is PREPARED to accept, not what
// currently exists. That is the correct state: the allowlist must exist
// before rows start landing, not be back-filled after (mirrors this
// package's own actiontype_reconcile.go comment about the backstop needing
// to exist before stage ② started writing rows).
package graphdb

// SystemActionTypeIDs is transcribed verbatim from 80 §4.6's worked-example
// table (the "id" column, all twelve rows) — no id invented here. Sorted
// alphabetically for readability; order carries no meaning.
var SystemActionTypeIDs = []string{
	"ci.trigger",
	"git.commit",
	"git.push",
	"mail.send",
	"note.write",
	"pr.comment",
	"pr.create",
	"pr.merge",
	"release.publish",
	"secret.rotate",
	"spend.request",
	"wbs.update_status",

	// [2026-09-11, C-1] docs/design/90-infra-topology.md §11 IN17 — the
	// three infra proposal action types this build now actually seeds (see
	// actiontype_infra.go and cmd/osiris-core/main.go's boot-seed call).
	// Listed here (not just seeded) so ReconcileActionTypes' AC2 sweep
	// recognizes them as known rather than degrading them to 'inert' on
	// every boot.
	"infra.apply",
	"infra.scale",
	"infra.delete",
}
