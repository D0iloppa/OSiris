// infra.apply / infra.scale / infra.delete :ActionType rows, as DATA —
// docs/design/90-infra-topology.md §11 (IN17/IN18/IN19, "C-1 — 제안→승인
// 척추") and docs/design/80-action-layer.md (the model these rows instantiate
// — §4 schema, §4.6 worked-example shape, §8.1's "M2 흡수" precedent that
// actiontype_gitcommit.go already followed for git.commit).
//
// IN17: "인프라 액션은 :ActionType(infra.apply/scale/delete, gateClass:
// approval.admin)이고, 제안은 기존 :Approval 노드의 인스턴스다 ... 새 승인
// 메커니즘을 만들지 않는다." This file is the :ActionType half of that
// sentence — the row shapes actiontype_gitcommit.go's own doc comment
// pointed at as "stage ③+'s job". The :Approval half (proposal/decision
// writeops) is writeops_approval.go in this same package.
//
// SCOPE: this file returns three Go values only (mirrors
// actiontype_gitcommit.go's GitCommitActionType — "THIS FILE DOES NOT CALL
// CreateActionType"). Unlike git.commit's stage, THIS build DOES seed these
// rows — cmd/osiris-core/main.go's boot sequence now calls CreateActionType
// for all three (idempotent — see that call site's comment) — because C-1's
// whole point is standing up a real, reachable approval round-trip, and an
// unreachable :ActionType row would leave POST /_api/infra/proposals with
// nothing to attach a proposal's FOR_ACTION edge to.
//
// WHY Effects/ValidationPredicates/RequiresCapability ARE ALL EMPTY, ON
// PURPOSE (not an oversight — this is the field-by-field honesty rule
// actiontype_gitcommit.go's own doc comment models): C-1 explicitly excludes
// execution (C-2's job — "실행(apply)은 C-2다 — 이번엔 만들지 마라"). There is
// no effect.infra.apply/scale/delete registered in
// osiris-core/internal/action/effect (CA3 would reject a reference to one
// that doesn't exist), no infra-shaped validation predicate registered in
// internal/action/predicate, and no stk.infra.* key in
// actiontype_capabilitykeys.go's closed registry (CA8 would reject that too
// — 81's capability-stack doc, not this one, owns adding such a key, and
// this task does not touch 81). Leaving all three empty is therefore not a
// shortcut — it is the only field-by-field-honest way to declare "this
// action exists and can be proposed and approved" without also, even
// silently, declaring "and here is what running it does". When C-2 adds the
// infra-operator effect functions, it can UpdateActionType these rows to add
// Effects — a strictly additive, non-relaxing change (adding a leaf effect
// does not touch gateClass/taintClass/effectClass/reversibility, so DG8 does
// not even apply).
//
// GateClass/ApproverRole: "approval.admin"/"admin" — IN17's literal
// requirement, and 80 §813's is the doc section this satisfies (the console/
// REST decide path this stage builds is IN19's first real publisher of
// approval.decision.<id> — see api/internal/infra/proposals.go).
//
// TaintClass/EffectClass/Reversibility: an infra change is exactly the kind
// of thing 80 §4.2 calls "external_effect" (it is meant to eventually touch
// the cluster, even though nothing in THIS build does), and AC15 then
// requires taintClass >= escalate whenever effectClass is external_effect —
// so taintClass:"escalate" is not a free choice, it is AC15's forced
// consequence of effectClass. Reversibility: infra.apply/scale are
// "compensable" (a bad apply/scale can be corrected by another apply/scale —
// C-2's operator territory, not undone automatically); infra.delete is
// "irreversible" (deleting a namespace/workload is not something a
// compensating action can silently restore).
//
// RequiresPermissionCodes is deliberately EMPTY for all three rows — NOT
// "org.admin" (which would be the obvious-looking choice given
// gateClass:"approval.admin"). Reason: actiontype_permission_mirror.go's
// AC20 boot check derives :Permission.gate as the MIN gateClass over every
// :ActionType claiming that permission via REQUIRES_PERMISSION, and
// seed.go's SystemPermissions already seeds {Code: "org.admin", Gate:
// "none"} — org.admin's own gate is deliberately "none" (an admin
// permission does not itself require approval to hold; see 80 §4.5's own
// worked table, which lists no gate for org.admin either). Adding a
// REQUIRES_PERMISSION edge from these rows to "org.admin" would make the
// derived value "approval" and immediately fail AC20's fail-closed boot
// check against the stored "none" — a real conflict this file avoids by not
// creating that edge. The actual authorization gate for
// POST /_api/infra/proposals/{id}/decision is enforced independently, in
// osiris-api's RequirePerm(authz.PermOrgAdmin) (IN19) — :ActionType's own
// REQUIRES_PERMISSION/:Permission mirror is a DIFFERENT, orthogonal
// mechanism (80 §4.5: ":Permission is 'what a container may touch',
// :ActionType is 'what that touch means and what gate it gets'") that this
// task does not need and must not misuse just to get an edge to exist.
package graphdb

// infraActionType is the shared shape-builder for all three rows below —
// they differ only in id/code/label/description/reversibility, so the
// common fields are written once to keep the three declarations impossible
// to accidentally let drift apart on the fields that must be identical
// (gateClass/approverRole/effectClass/taintClass, all IN17/AC15-forced).
func infraActionType(id, label, description, reversibility string) ActionTypeInput {
	return ActionTypeInput{
		ID:            id,
		Code:          id,
		Label:         label,
		DescriptionMd: description,

		Origin: "system",
		Status: "active",

		// [판단] — a minimal, honest parameter shape: what a proposal names
		// (namespace/kind/name) and an opaque manifest/spec payload it
		// carries, stored as a string rather than a nested object schema
		// (§4.4's allowed subset supports arbitrary nesting up to depth 3,
		// but nothing in this build parses the manifest's *contents* — that
		// is the C-2 operator's job — so a nested schema here would imply a
		// precision this stage does not have).
		ParametersSchemaJSON: `{` +
			`"type":"object",` +
			`"properties":{` +
			`"namespace":{"type":"string","minLength":1,"maxLength":253,"pattern":"^[a-z0-9]([a-z0-9-]{0,251}[a-z0-9])?$","description":"target Kubernetes namespace"},` +
			`"kind":{"type":"string","minLength":1,"maxLength":64,"description":"target resource kind, e.g. Deployment"},` +
			`"name":{"type":"string","minLength":1,"maxLength":253,"description":"target resource name"},` +
			`"manifestJson":{"type":"string","maxLength":65536,"description":"opaque proposed manifest/spec, stored verbatim — not parsed or applied by this build (C-1 excludes execution)"}` +
			`},` +
			`"required":["namespace","kind","name"]` +
			`}`,
		ParameterDefaultsJSON: "",

		// Empty, on purpose — see this file's package doc comment.
		ValidationPredicates: nil,
		Effects:              nil,

		EffectClass:  "external_effect", // §4.2 — infra changes touch the cluster (C-2's territory), even though nothing executes yet
		GateClass:    "approval.admin",  // IN17, literal
		ApproverRole: "admin",           // §4.2: required whenever gateClass != none; "admin" is the only vocabulary match to approval.admin
		LimitJSON:    "",

		TaintClass:    "escalate", // AC15: external_effect forces taintClass >= escalate
		Reversibility: reversibility,

		// Empty, on purpose — see this file's package doc comment (no
		// stk.infra.* key exists in the closed CA8 registry yet; that
		// registry belongs to 81-capability-stacks.md, out of this task's
		// scope).
		RequiresCapability: nil,

		DerivedHash: "",
		IRI:         "",

		// Empty, on purpose — see this file's package doc comment (would
		// fail AC20's mirror check against org.admin's seeded gate:"none").
		RequiresPermissionCodes: nil,
		ComposedOf:              nil,

		ByPartyID: "", // caller-supplied at actual CreateActionType call time
	}
}

// InfraApplyActionType is IN17's "infra.apply" row.
func InfraApplyActionType() ActionTypeInput {
	return infraActionType(
		"infra.apply",
		"Apply an infrastructure manifest",
		"Proposes applying a Kubernetes manifest to the cluster. C-1 stores and gates the proposal only — actually applying it is C-2 (docs/design/90-infra-topology.md §11), not implemented by this build.",
		"compensable",
	)
}

// InfraScaleActionType is IN17's "infra.scale" row.
func InfraScaleActionType() ActionTypeInput {
	return infraActionType(
		"infra.scale",
		"Scale an infrastructure workload",
		"Proposes changing a workload's replica count/resources. C-1 stores and gates the proposal only — actually scaling it is C-2, not implemented by this build.",
		"compensable",
	)
}

// InfraDeleteActionType is IN17's "infra.delete" row.
func InfraDeleteActionType() ActionTypeInput {
	return infraActionType(
		"infra.delete",
		"Delete an infrastructure resource",
		"Proposes deleting a Kubernetes resource. C-1 stores and gates the proposal only — actually deleting it is C-2, not implemented by this build.",
		"irreversible",
	)
}
