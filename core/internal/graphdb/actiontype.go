// :ActionType graph-write — docs/design/80-action-layer.md §4 (schema), §9
// AC1/AC4/AC9/AC10/AC15/AC19 (this file's job), P2 stage ② per §11's
// "구현 워커" ordering ("② :ActionType 스키마 + graph-write 화이트리스트
// (AC4/CA1)"). Stage ③ (git.commit as a real row, effect.scm.commit) and §6/
// §7 (composition EXECUTION semantics, taint) are explicitly NOT this
// file's job — see the package-level notes below on what is and is not
// implemented.
//
// WHITELIST ENFORCEMENT — TWO INDEPENDENT LAYERS (CA1/AC4, "치명"):
//  1. ActionTypeInput below has exactly one Go field per §4.2 node property
//     (plus two edge-shaped fields explicitly called out as NOT properties —
//     RequiresPermissionCodes/ComposedOf, §4.3). A caller building this
//     struct in Go cannot express a property outside §4.2 — there is no
//     field to put it in. This mirrors how api/server.go's `decode()`
//     helper already calls `json.Decoder.DisallowUnknownFields()` for every
//     other write endpoint in this codebase (see that function, server.go)
//     — an HTTP body naming an extra key is REJECTED at decode time, not
//     silently dropped, which is exactly AC4's "화이트리스트 밖 속성은
//     거부(무시 아님)" requirement. This file relies on that pre-existing
//     mechanism rather than reinventing a second one; see IMPLEMENTATION
//     judgment #1 in this task's final report.
//  2. ActionTypeAttributeWhitelist (below) is the SAME 22-key list, exposed
//     as data so bin/lint-action-layer.sh can mechanically confirm the two
//     representations (Go struct field set, Cypher CREATE/SET clause) never
//     drift apart, and that the specific banned names §4.2's "왜 이 필드들이
//     없는가" paragraph names (command/script/template/url/endpoint/
//     handlerCode/expr/condition/webhook) never appear as a property key
//     anywhere in this file.
//
// WHAT THIS FILE DOES NOT DO (explicitly out of scope, not an oversight):
//   - No execution. §6.3's execute() pseudocode, §6.4's derivedHash
//     recomputation at call time (AD6/AD8/AC8), and any effect/predicate
//     INVOCATION are stage ③+ (this task's brief: "조합을 실행하는 코드는
//     넣지 마라"). derivedHash is stored verbatim as a caller-supplied
//     string and never computed or verified here.
//   - No taint model (§7) — taint-aware gate escalation (gateFromTaint),
//     :Approval/TAINTED_BY, and AC13/AC14/AC16 are untouched.
//   - No DG6/DG7, and no DG8 GATE (§5.1.2) — actiontype.write's authorization
//     (who may call this at all, self-service editing, and "may THIS actor
//     relax THIS axis") is authz.CheckDelegation's job (api/internal/authz),
//     evaluated upstream of this package by osiris-api before osiris-core's
//     graph-write API is ever called (the same layering CreateTeam/
//     CreateRole/etc. above already assume — none of them re-check
//     permission codes either).
//     [2026-08-13] UpdateActionType BELOW does implement DG8's OTHER half —
//     the fail-closed notification-or-no-commit rule — because that half is
//     a property of the WRITE TRANSACTION (same :Notification mechanism
//     DG5 already uses, writeops.go's notifyOrgAdmins), not of the
//     delegation decision. See UpdateActionType's doc comment and
//     actionTypeRelaxes below.
//   - No :Permission.gate/approverRole/limitJson mirror WRITE (AD13/AC20) —
//     see actiontype_permission_mirror.go for why the derivation formula is
//     deliberately NOT implemented here (80 G-A11).
package graphdb

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ActionTypeAttributeWhitelist is 80 §4.2's node-property whitelist,
// verbatim (22 keys) — see this file's doc comment, enforcement layer 2.
// This is a NODE property whitelist; §4.3's relationship properties
// (REQUIRES_PERMISSION has none, COMPOSED_OF has exactly `ord`) are a
// separate whitelist enforced structurally by ComposedOfInput's shape and
// by writeComposedOfEdges' fixed CREATE clause (AC10) — not part of this
// list, and not something that belongs in it (relationship properties are
// not node properties).
var ActionTypeAttributeWhitelist = map[string]struct{}{
	"id": {}, "code": {}, "label": {}, "descriptionMd": {}, "catalogVersion": {},
	"origin": {}, "status": {}, "createdAtRevision": {}, "closedAtRevision": {},
	"parametersSchemaJson": {}, "parameterDefaultsJson": {},
	"validationPredicates": {}, "effects": {}, "effectClass": {},
	"gateClass": {}, "approverRole": {}, "limitJson": {},
	"taintClass": {}, "reversibility": {}, "requiresCapability": {},
	"derivedHash": {}, "iri": {},
}

// actionTypeBannedFieldNames is §4.2's "왜 이 필드들이 없는가" paragraph,
// transcribed verbatim — NOT consulted by any validation code path (there is
// no field to carry these values in ActionTypeInput at all, which is the
// actual enforcement). Declared purely so bin/lint-action-layer.sh has a
// single, textual source to grep both this file and ActionTypeInput against,
// instead of the lint script hand-copying the list a second time.
var actionTypeBannedFieldNames = []string{
	"command", "script", "template", "url", "endpoint", "handlerCode", "expr", "condition", "webhook",
}

var (
	actionTypeIDCodeFormat = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z0-9]+)+$`)

	actionTypeOrigins       = map[string]struct{}{"system": {}, "composed": {}}
	actionTypeStatuses      = map[string]struct{}{"draft": {}, "active": {}, "deprecated": {}, "withdrawn": {}, "inert": {}}
	actionTypeEffectClasses = map[string]struct{}{"internal": {}, "external_effect": {}}
	actionTypeGateClasses   = map[string]struct{}{"none": {}, "approval.team_lead": {}, "approval.admin": {}}
	actionTypeApproverRoles = map[string]struct{}{"team-lead": {}, "admin": {}}
	actionTypeTaintClasses  = map[string]struct{}{"neutral": {}, "escalate": {}, "forbidden": {}}
	actionTypeReversibility = map[string]struct{}{"reversible": {}, "compensable": {}, "irreversible": {}}
	taintClassRank          = map[string]int{"neutral": 0, "escalate": 1, "forbidden": 2}

	// gateClassRank/effectClassRank/reversibilityRank are actionTypeRelaxes'
	// (DG8, 80 §5.1.2/§9 AC11) three companions to taintClassRank above —
	// same shape, one rank map per axis DG8 names. Higher = stricter/more
	// protected; a transition to a LOWER rank on any axis is a relaxation.
	//
	//   gateClass:      none < approval.team_lead < approval.admin
	//   effectClass:    internal < external_effect         (AC15: external_effect
	//                   requires taintClass >= escalate, so it is the more
	//                   regulated category)
	//   reversibility:  reversible < compensable < irreversible (declaring an
	//                   action MORE reversible than before removes whatever
	//                   extra handling its stricter category implied
	//                   downstream — the relaxation is in the DECLARATION,
	//                   not a claim that undo-ability itself got worse)
	gateClassRank     = map[string]int{"none": 0, "approval.team_lead": 1, "approval.admin": 2}
	effectClassRank   = map[string]int{"internal": 0, "external_effect": 1}
	reversibilityRank = map[string]int{"reversible": 0, "compensable": 1, "irreversible": 2}
)

// AC9's fixed limits (§4.7 CA5 / §9 AC9). Not configurable — §10 G-A9 notes
// these are acknowledged arbitrary-but-finite values pending real-world
// observation; changing them is a design decision for whoever owns that gap,
// not a runtime parameter.
const (
	composedOfMaxDepth       = 3
	composedOfMaxLeafEffects = 8
)

// ComposedOfInput is `(:ActionType)-[:COMPOSED_OF]->(:ActionType)` (§4.3).
// AC10 enforcement layer 1: this struct has exactly one value field (Ord) —
// there is nowhere in Go to attach a second relationship property, the same
// "no field to put it in" argument ActionTypeInput makes for AC4. Layer 2 is
// writeComposedOfEdges' CREATE clause below, which sets only `ord`.
type ComposedOfInput struct {
	ChildActionTypeID string
	Ord               int
}

// ActionTypeInput is §4.2's node in Go-struct form, plus two edge-shaped
// fields (RequiresPermissionCodes, ComposedOf — §4.3, NOT node properties)
// and ByPartyID (the acting :Party, same convention as every other *Input
// struct in this file — TeamInput, RoleInput, ...).
//
// CatalogVersion is deliberately NOT a field here — see CreateActionType's
// signature. It is supplied by the caller (osiris-core's own configured
// CatalogVersion, cfg.CatalogVersion) exactly the way seedPermissions takes
// it as a function parameter rather than trusting client-submitted data
// (I9's "같은 버전이어야 한다" is a fact about THIS BINARY's build, not
// something an API caller should be able to assert).
//
// CreatedAtRevision/ClosedAtRevision are likewise not settable fields —
// CreatedAtRevision is computed inside the write transaction from
// bumpRevision's own result (§2.1's "재현용 시스템 시간"), and
// ClosedAtRevision is a withdraw-transition concern this file does not
// implement (CA7's "철회는 status:'withdrawn' + closedAtRevision" — no
// dedicated WithdrawActionType function exists yet; see final report).
type ActionTypeInput struct {
	ID            string
	Code          string
	Label         string
	DescriptionMd string

	Origin string // system | composed
	Status string // draft | active | deprecated | withdrawn | inert

	ParametersSchemaJSON  string // required — §4.4 subset, validated by ValidateParametersSchemaJSON
	ParameterDefaultsJSON string // optional JSON

	ValidationPredicates []string // must all resolve via knownPredicates (CA3)
	Effects              []string // must all resolve via knownEffects (CA3); must be empty when Origin=="composed" (CA4)

	EffectClass  string // internal | external_effect
	GateClass    string // none | approval.team_lead | approval.admin
	ApproverRole string // team-lead | admin — required iff GateClass != "none"
	LimitJSON    string // optional JSON

	TaintClass    string // neutral | escalate | forbidden
	Reversibility string // reversible | compensable | irreversible

	RequiresCapability []string // must all satisfy validCapabilityKey (CA8/81 C-1/C-2)

	DerivedHash string // opaque, stored verbatim — see this file's doc comment: never computed/verified here
	IRI         string

	// --- relationships, §4.3 — NOT node properties, NOT part of
	// ActionTypeAttributeWhitelist ---
	RequiresPermissionCodes []string // -> REQUIRES_PERMISSION (existing relationship type, 20 §7.2)
	ComposedOf              []ComposedOfInput

	ByPartyID string
}

// CreateActionType inserts a new :ActionType. Fails (ErrValidation) if a
// node with this id already exists — use UpdateActionType for that case
// (mirrors this file's general "explicit create vs update, not a single
// silent upsert" choice, made because ActionType writes are security-
// relevant in a way team/role upserts are not — see final report judgment).
func (c *Client) CreateActionType(ctx context.Context, in ActionTypeInput, catalogVersion string, knownPredicates, knownEffects map[string]struct{}) (WriteResult, error) {
	if err := validateActionTypeInput(in, knownPredicates, knownEffects); err != nil {
		return WriteResult{}, err
	}
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		already, err := tx.Run(ctx, `MATCH (a:ActionType {id: $id}) RETURN a.id`, map[string]any{"id": in.ID})
		if err != nil {
			return nil, err
		}
		if _, err := already.Single(ctx); err == nil {
			return nil, validationErrorf(":ActionType %q already exists — use UpdateActionType", in.ID)
		}

		if err := validateRequiresPermissionRefs(ctx, tx, in.RequiresPermissionCodes); err != nil {
			return nil, err
		}
		if err := validateComposedOfRefs(ctx, tx, in.ID, in.Origin, in.ComposedOf); err != nil {
			return nil, err
		}

		n, err := bumpRevision(ctx, tx, in.ByPartyID, fmt.Sprintf("action type %s created", in.ID), "actiontype_created")
		if err != nil {
			return nil, err
		}

		// Every property named explicitly (CA1/AC4 enforcement layer 2 — see
		// package doc comment). No SET a += $map / no `*` — a Cypher clause
		// this narrow cannot silently grow to accept a new key just because
		// a caller's map happened to have one.
		_, err = tx.Run(ctx, `
			CREATE (a:ActionType {
				id: $id, code: $code, label: $label, descriptionMd: $descriptionMd,
				catalogVersion: $catalogVersion, origin: $origin, status: $status,
				createdAtRevision: $rev,
				parametersSchemaJson: $parametersSchemaJson, parameterDefaultsJson: $parameterDefaultsJson,
				validationPredicates: $validationPredicates, effects: $effects,
				effectClass: $effectClass, gateClass: $gateClass, approverRole: $approverRole,
				limitJson: $limitJson, taintClass: $taintClass, reversibility: $reversibility,
				requiresCapability: $requiresCapability, derivedHash: $derivedHash, iri: $iri
			})
		`, actionTypeCreateParams(in, catalogVersion, n))
		if err != nil {
			return nil, err
		}

		if err := writeRequiresPermissionEdges(ctx, tx, in.ID, in.RequiresPermissionCodes); err != nil {
			return nil, err
		}
		if err := writeComposedOfEdges(ctx, tx, in.ID, in.ComposedOf); err != nil {
			return nil, err
		}
		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.ID}, nil
}

// UpdateActionType overwrites an existing :ActionType's whitelisted
// properties (CreatedAtRevision is left untouched — it records when the row
// was FIRST created, not last edited; §4.2 gives no separate "last edited
// at revision" field, and this file does not invent one). Fails
// (ErrValidation) if the id does not already exist.
func (c *Client) UpdateActionType(ctx context.Context, in ActionTypeInput, catalogVersion string, knownPredicates, knownEffects map[string]struct{}) (WriteResult, error) {
	if err := validateActionTypeInput(in, knownPredicates, knownEffects); err != nil {
		return WriteResult{}, err
	}
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		found, err := tx.Run(ctx, `MATCH (a:ActionType {id: $id}) RETURN a.id`, map[string]any{"id": in.ID})
		if err != nil {
			return nil, err
		}
		if _, err := found.Single(ctx); err != nil {
			return nil, validationErrorf(":ActionType %q not found — use CreateActionType", in.ID)
		}

		// DG8's "before" snapshot (80 §5.1.2/§9 AC11) — read now, inside this
		// same transaction, and BEFORE the SET below overwrites it.
		before, err := readActionTypeAxesForUpdate(ctx, tx, in.ID)
		if err != nil {
			return nil, err
		}

		if err := validateRequiresPermissionRefs(ctx, tx, in.RequiresPermissionCodes); err != nil {
			return nil, err
		}
		if err := validateComposedOfRefs(ctx, tx, in.ID, in.Origin, in.ComposedOf); err != nil {
			return nil, err
		}

		_, err = tx.Run(ctx, `
			MATCH (a:ActionType {id: $id})
			SET a.code = $code, a.label = $label, a.descriptionMd = $descriptionMd,
			    a.catalogVersion = $catalogVersion, a.origin = $origin, a.status = $status,
			    a.parametersSchemaJson = $parametersSchemaJson, a.parameterDefaultsJson = $parameterDefaultsJson,
			    a.validationPredicates = $validationPredicates, a.effects = $effects,
			    a.effectClass = $effectClass, a.gateClass = $gateClass, a.approverRole = $approverRole,
			    a.limitJson = $limitJson, a.taintClass = $taintClass, a.reversibility = $reversibility,
			    a.requiresCapability = $requiresCapability, a.derivedHash = $derivedHash, a.iri = $iri
		`, actionTypeUpdateParams(in, catalogVersion))
		if err != nil {
			return nil, err
		}

		// REQUIRES_PERMISSION: additive merge only (same convention as
		// CreateRole's REQUIRES_CAPABILITY loop above in this file) — a
		// permission no longer listed is left in place rather than silently
		// dropped, matching this codebase's existing precedent rather than
		// inventing a new "remove what's missing" semantics for this one
		// relationship type.
		if err := writeRequiresPermissionEdges(ctx, tx, in.ID, in.RequiresPermissionCodes); err != nil {
			return nil, err
		}
		// COMPOSED_OF: full replace (delete then recreate) — unlike
		// REQUIRES_PERMISSION, stale composition edges are not safe to leave
		// behind: they would silently change AC9's leaf-effect count and
		// depth out from under a caller who only meant to reorder or
		// shrink the composition.
		if err := replaceComposedOfEdges(ctx, tx, in.ID, in.ComposedOf); err != nil {
			return nil, err
		}

		n, err := bumpRevision(ctx, tx, in.ByPartyID, fmt.Sprintf("action type %s updated", in.ID), "actiontype_updated")
		if err != nil {
			return nil, err
		}

		// AC11/DG8, fail-closed, and DELIBERATELY THE OPPOSITE DIRECTION OF
		// AC2/AC3's boot-time inert-degrade write (actiontype_reconcile.go):
		// THAT write moves an :ActionType SAFER (active -> inert) and is
		// allowed to proceed even if its own security.actiontype.unknown
		// publish fails, because a safety-direction change must not be
		// blockable by a notification outage. THIS write moves an
		// :ActionType LESS safe — a relaxation — so the rule runs the other
		// way: if the notification cannot be written, the relaxation must
		// not commit either. Reusing notifyOrgAdmins (writeops.go, DG5's own
		// mechanism) gets that for free: it runs inside this SAME
		// transaction, so a failure here returns an error from this closure
		// and neo4j-go-driver rolls the whole write back — the relaxation
		// and its notification either both land or neither does.
		//
		// The org.admin-only GATE on relaxation (DG8's other half) is
		// authz.CheckDelegation's job at the osiris-api layer (same
		// layering note as this file's package doc comment on DG6~DG8) —
		// this function only guarantees that IF a relaxation reaches here,
		// it cannot commit silently.
		if actionTypeRelaxes(before, in) {
			if err := notifyOrgAdmins(ctx, tx, in.ByPartyID, "security.actiontype.relaxed",
				fmt.Sprintf("action type %s relaxed a gate/taint/effect/reversibility axis", in.ID), n); err != nil {
				return nil, err
			}
		}
		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.ID}, nil
}

// actionTypeAxes is the subset of :ActionType properties DG8 (80 §5.1.2)
// compares before vs. after an update — exactly the four axes DG8 names,
// plus status (for the "revive from withdrawn" clause).
type actionTypeAxes struct {
	GateClass     string
	TaintClass    string
	EffectClass   string
	Reversibility string
	Status        string
}

// readActionTypeAxesForUpdate reads the CURRENT (pre-SET) values of the five
// fields actionTypeRelaxes compares, inside the same write transaction
// UpdateActionType is already running — so "before" and "the row about to be
// overwritten" can never be two different revisions.
func readActionTypeAxesForUpdate(ctx context.Context, tx neo4j.ManagedTransaction, id string) (actionTypeAxes, error) {
	res, err := tx.Run(ctx, `
		MATCH (a:ActionType {id: $id})
		RETURN a.gateClass AS gateClass, a.taintClass AS taintClass,
		       a.effectClass AS effectClass, a.reversibility AS reversibility, a.status AS status
	`, map[string]any{"id": id})
	if err != nil {
		return actionTypeAxes{}, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return actionTypeAxes{}, err
	}
	str := func(key string) string {
		v, _ := rec.Get(key)
		s, _ := v.(string)
		return s
	}
	return actionTypeAxes{
		GateClass:     str("gateClass"),
		TaintClass:    str("taintClass"),
		EffectClass:   str("effectClass"),
		Reversibility: str("reversibility"),
		Status:        str("status"),
	}, nil
}

// actionTypeRelaxes is DG8's own detector (80 §5.1.2: "게이트·오염·효과·
// 가역성의 완화 변경은 org.admin만 가능하고 … 발행에 성공해야 커밋된다").
// This is the GRAPH LAYER's independent copy of the same comparison
// authz.DelegationRequest.RelaxesActionType asks its caller to compute
// (api/internal/authz/delegation.go) — deliberately not shared code, per
// HI16's "single judgment, layered defense" precedent already used for
// I14~I17: the delegation decision (may this actor do it) and the graph's
// own fail-closed notification gate (did the notification actually
// succeed) are two different walls, and a bug in one must not silently
// disable the other.
func actionTypeRelaxes(before actionTypeAxes, after ActionTypeInput) bool {
	if gateClassRank[after.GateClass] < gateClassRank[before.GateClass] {
		return true
	}
	if taintClassRank[after.TaintClass] < taintClassRank[before.TaintClass] {
		return true
	}
	if effectClassRank[after.EffectClass] < effectClassRank[before.EffectClass] {
		return true
	}
	if reversibilityRank[after.Reversibility] < reversibilityRank[before.Reversibility] {
		return true
	}
	if before.Status == "withdrawn" && after.Status != "withdrawn" {
		return true
	}
	return false
}

func actionTypeCreateParams(in ActionTypeInput, catalogVersion string, rev int64) map[string]any {
	p := actionTypeUpdateParams(in, catalogVersion)
	p["rev"] = rev
	return p
}

func actionTypeUpdateParams(in ActionTypeInput, catalogVersion string) map[string]any {
	return map[string]any{
		"id": in.ID, "code": in.Code, "label": in.Label, "descriptionMd": in.DescriptionMd,
		"catalogVersion": catalogVersion, "origin": in.Origin, "status": in.Status,
		"parametersSchemaJson": in.ParametersSchemaJSON, "parameterDefaultsJson": in.ParameterDefaultsJSON,
		"validationPredicates": in.ValidationPredicates, "effects": in.Effects,
		"effectClass": in.EffectClass, "gateClass": in.GateClass, "approverRole": in.ApproverRole,
		"limitJson": in.LimitJSON, "taintClass": in.TaintClass, "reversibility": in.Reversibility,
		"requiresCapability": in.RequiresCapability, "derivedHash": in.DerivedHash, "iri": in.IRI,
	}
}

// validateActionTypeInput runs every Go-level (no DB access needed) check —
// enum membership, format, §4.4's schema subset, CA3's predicate/effect
// reference check, CA8's capability-key check, and AC15's cross-field rule.
// DB-dependent checks (REQUIRES_PERMISSION targets, COMPOSED_OF targets/
// cycle/depth/leaf-count) run separately inside the write transaction (see
// validateRequiresPermissionRefs/validateComposedOfRefs) because they need a
// live Cypher session.
func validateActionTypeInput(in ActionTypeInput, knownPredicates, knownEffects map[string]struct{}) error {
	if !actionTypeIDCodeFormat.MatchString(in.ID) {
		return validationErrorf("ActionType.id %q must be lowercase dot-separated (AC1, same convention as :Permission.code)", in.ID)
	}
	if !actionTypeIDCodeFormat.MatchString(in.Code) {
		return validationErrorf("ActionType.code %q must be lowercase dot-separated (AC1)", in.Code)
	}
	if _, ok := actionTypeOrigins[in.Origin]; !ok {
		return validationErrorf("ActionType.origin %q must be one of system|composed", in.Origin)
	}
	if _, ok := actionTypeStatuses[in.Status]; !ok {
		return validationErrorf("ActionType.status %q must be one of draft|active|deprecated|withdrawn|inert", in.Status)
	}
	if _, ok := actionTypeEffectClasses[in.EffectClass]; !ok {
		return validationErrorf("ActionType.effectClass %q must be one of internal|external_effect", in.EffectClass)
	}
	if _, ok := actionTypeGateClasses[in.GateClass]; !ok {
		return validationErrorf("ActionType.gateClass %q must be one of none|approval.team_lead|approval.admin", in.GateClass)
	}
	if in.GateClass != "none" {
		if _, ok := actionTypeApproverRoles[in.ApproverRole]; !ok {
			return validationErrorf("ActionType.approverRole %q must be one of team-lead|admin when gateClass != none (§4.2)", in.ApproverRole)
		}
	} else if in.ApproverRole != "" {
		return validationErrorf("ActionType.approverRole must be empty when gateClass == none")
	}
	if _, ok := actionTypeTaintClasses[in.TaintClass]; !ok {
		return validationErrorf("ActionType.taintClass %q must be one of neutral|escalate|forbidden", in.TaintClass)
	}
	if _, ok := actionTypeReversibility[in.Reversibility]; !ok {
		return validationErrorf("ActionType.reversibility %q must be one of reversible|compensable|irreversible", in.Reversibility)
	}

	// AC15, first half: effectClass:'external_effect' ⟹ taintClass ≥ 'escalate'.
	if in.EffectClass == "external_effect" && taintClassRank[in.TaintClass] < taintClassRank["escalate"] {
		return validationErrorf("AC15: effectClass:'external_effect' requires taintClass to be at least 'escalate', got %q", in.TaintClass)
	}
	// AC15, second half (§7.7-2's "짝"): effectClass:'internal' ⟹ reversibility != 'irreversible'.
	if in.EffectClass == "internal" && in.Reversibility == "irreversible" {
		return validationErrorf("AC15: effectClass:'internal' must not be reversibility:'irreversible' (an internal action that cannot be undone is not representable)")
	}

	// §4.4 subset check.
	if err := ValidateParametersSchemaJSON(in.ParametersSchemaJSON); err != nil {
		return validationErrorf("%v", err)
	}
	if in.ParameterDefaultsJSON != "" {
		var v any
		if err := json.Unmarshal([]byte(in.ParameterDefaultsJSON), &v); err != nil {
			return validationErrorf("parameterDefaultsJson: invalid JSON: %v", err)
		}
	}
	if in.LimitJSON != "" {
		var v any
		if err := json.Unmarshal([]byte(in.LimitJSON), &v); err != nil {
			return validationErrorf("limitJson: invalid JSON: %v", err)
		}
	}

	// CA3: validationPredicates/effects must resolve against the registries
	// osiris-core actually has compiled in. Same fail-closed contract as
	// ReconcileActionTypes' boot-time sweep, applied at write time instead
	// (§9 AC3: "쓰기 시 거부, 부팅 시 inert").
	for _, p := range in.ValidationPredicates {
		if _, ok := knownPredicates[p]; !ok {
			return validationErrorf("CA3: validationPredicates references unknown predicate %q — not in the compiled predicate registry", p)
		}
	}
	for _, e := range in.Effects {
		if _, ok := knownEffects[e]; !ok {
			return validationErrorf("CA3: effects references unknown effect %q — not in the compiled effect registry", e)
		}
	}
	// CA4, first half: origin:'composed' rows carry no leaf effects of their
	// own (effects come from the parts they compose).
	if in.Origin == "composed" && len(in.Effects) > 0 {
		return validationErrorf("CA4: origin:'composed' requires effects to be empty (effects are inherited from COMPOSED_OF parts)")
	}
	// [판단, 문서 결정 아님] origin:'system' rows are code-registry-backed
	// leaf actions (§5.3.1) — this build does not allow a 'system' row to
	// also declare COMPOSED_OF; only 'composed' rows compose. Nothing in
	// §4/§6 states this explicitly either way; see final report.
	if in.Origin == "system" && len(in.ComposedOf) > 0 {
		return validationErrorf("origin:'system' rows may not declare COMPOSED_OF (composition is an origin:'composed' concept, §6.1) — this is this implementation's reading, not an explicit doc rule; see task report")
	}

	// CA8/AC21: every requiresCapability key must satisfy 81 C-1's regex AND
	// exist in the closed registry (actiontype_capabilitykeys.go).
	for _, k := range in.RequiresCapability {
		if !validCapabilityKey(k) {
			return validationErrorf("CA8: requiresCapability key %q does not match 81 §2.3 C-1's grammar or is not in the static registry (81 C-2/CS4 — definition-time hard error, not invented at runtime)", k)
		}
	}

	return nil
}

// validateRequiresPermissionRefs mirrors CreateRole's capability-existence
// check (writeops.go, ~line 166) for the (:ActionType)-[:REQUIRES_PERMISSION]
// ->(:Permission) edge target.
func validateRequiresPermissionRefs(ctx context.Context, tx neo4j.ManagedTransaction, codes []string) error {
	for _, code := range codes {
		check, err := tx.Run(ctx, `MATCH (p:Permission {code: $code}) RETURN p.code`, map[string]any{"code": code})
		if err != nil {
			return err
		}
		if _, err := check.Single(ctx); err != nil {
			return validationErrorf("REQUIRES_PERMISSION references unknown :Permission code %q", code)
		}
	}
	return nil
}

func writeRequiresPermissionEdges(ctx context.Context, tx neo4j.ManagedTransaction, actionID string, codes []string) error {
	for _, code := range codes {
		if _, err := tx.Run(ctx, `
			MATCH (a:ActionType {id: $id})
			MATCH (p:Permission {code: $code})
			MERGE (a)-[:REQUIRES_PERMISSION]->(p)
		`, map[string]any{"id": actionID, "code": code}); err != nil {
			return err
		}
	}
	return nil
}

// validateComposedOfRefs implements AC9 (사이클 없음, 깊이 ≤ 3, leaf 이펙트
// ≤ 8) and AC4's "빈 배열이면 CA4 위반" companion (>=1 edge for
// origin:'composed'). This is a WRITE-TIME REJECTION check — it never
// executes anything and never computes a gate/taint join (§6.2/§6.4 are
// untouched); it only decides whether the proposed edge set may be
// persisted at all. See this file's doc comment for the scope line this
// draws.
//
// [판단, 문서 결정 아님] leaf-effect counting below sums each child's own
// leaf-effect count independently and adds them together; if two children
// of the SAME composed action share a leaf action deeper in their own
// COMPOSED_OF trees, that shared leaf's effects are counted once per parent
// that reaches it, not once overall. This over-counts rather than
// under-counts — the same "안전한 방향의 오류" this codebase already
// accepts elsewhere (§8.3's M3 over-approximation) — so a legitimate
// composition sharing a leaf could be rejected slightly earlier than a
// precise count would reject it, never later. Exact deduplication would
// need a single graph-wide reachability query across all children at once;
// left as a documented conservative approximation rather than attempted
// under this task's time budget — see final report.
func validateComposedOfRefs(ctx context.Context, tx neo4j.ManagedTransaction, selfID, origin string, composedOf []ComposedOfInput) error {
	if len(composedOf) == 0 {
		if origin == "composed" {
			return validationErrorf("CA4: origin:'composed' requires at least one COMPOSED_OF edge")
		}
		return nil
	}
	if origin != "composed" {
		return validationErrorf("COMPOSED_OF may only be declared on origin:'composed' rows")
	}

	seenOrd := map[int]struct{}{}
	seenChild := map[string]struct{}{}
	maxDepthBelowAnyChild := 0
	totalLeafEffects := 0

	for _, part := range composedOf {
		if part.ChildActionTypeID == "" {
			return validationErrorf("COMPOSED_OF entry missing childActionTypeId")
		}
		if part.ChildActionTypeID == selfID {
			return validationErrorf("AC9: COMPOSED_OF cannot reference itself (%q) — immediate cycle", selfID)
		}
		if part.Ord < 1 {
			return validationErrorf("COMPOSED_OF.ord must be >= 1, got %d for child %q", part.Ord, part.ChildActionTypeID)
		}
		if _, dup := seenOrd[part.Ord]; dup {
			return validationErrorf("COMPOSED_OF.ord %d is used more than once — order must be unambiguous (AD7, no branching)", part.Ord)
		}
		seenOrd[part.Ord] = struct{}{}
		if _, dup := seenChild[part.ChildActionTypeID]; dup {
			return validationErrorf("COMPOSED_OF references child %q more than once", part.ChildActionTypeID)
		}
		seenChild[part.ChildActionTypeID] = struct{}{}

		res, err := tx.Run(ctx, `
			MATCH (child:ActionType {id: $childId, status: 'active'})
			OPTIONAL MATCH p = (child)-[:COMPOSED_OF*0..]->(d:ActionType)
			WITH child, collect(DISTINCT d) AS descendants, collect(length(p)) AS depths
			WITH child, descendants, depths,
			     [n IN descendants WHERE NOT (n)-[:COMPOSED_OF]->()] AS leaves
			RETURN child.id AS childId,
			       [n IN descendants | n.id] AS descendantIds,
			       reduce(m = 0, x IN depths | CASE WHEN x > m THEN x ELSE m END) AS depthBelowChild,
			       reduce(s = 0, l IN leaves | s + size(coalesce(l.effects, []))) AS leafEffectCount
		`, map[string]any{"childId": part.ChildActionTypeID})
		if err != nil {
			return err
		}
		rec, err := res.Single(ctx)
		if err != nil {
			return validationErrorf("COMPOSED_OF references unknown or non-active :ActionType %q (§6.1: only status:'active' may be composed)", part.ChildActionTypeID)
		}

		descendantIDsRaw, _ := rec.Get("descendantIds")
		if descendantIDs, ok := descendantIDsRaw.([]any); ok {
			for _, d := range descendantIDs {
				if s, ok := d.(string); ok && s == selfID {
					return validationErrorf("AC9: COMPOSED_OF would create a cycle — %q is already reachable below child %q", selfID, part.ChildActionTypeID)
				}
			}
		}

		depthRaw, _ := rec.Get("depthBelowChild")
		depthBelowChild, _ := asInt(depthRaw)
		if depthBelowChild > maxDepthBelowAnyChild {
			maxDepthBelowAnyChild = depthBelowChild
		}

		leafRaw, _ := rec.Get("leafEffectCount")
		leafCount, _ := asInt(leafRaw)
		totalLeafEffects += leafCount
	}

	selfDepth := 1 + maxDepthBelowAnyChild
	if selfDepth > composedOfMaxDepth {
		return validationErrorf("AC9: composition depth %d exceeds the maximum of %d", selfDepth, composedOfMaxDepth)
	}
	if totalLeafEffects > composedOfMaxLeafEffects {
		return validationErrorf("AC9: composition has %d leaf effect(s), exceeding the maximum of %d (conservative count — see this function's doc comment on shared-leaf over-counting)", totalLeafEffects, composedOfMaxLeafEffects)
	}
	return nil
}

// writeComposedOfEdges is used by CreateActionType, where no prior
// COMPOSED_OF edges can exist yet.
func writeComposedOfEdges(ctx context.Context, tx neo4j.ManagedTransaction, actionID string, composedOf []ComposedOfInput) error {
	for _, part := range composedOf {
		// AC10 enforcement layer 2: this CREATE names exactly one
		// relationship property (`ord`). There is no second key in the
		// parameter map to smuggle a second property through.
		if _, err := tx.Run(ctx, `
			MATCH (a:ActionType {id: $id})
			MATCH (child:ActionType {id: $childId})
			CREATE (a)-[:COMPOSED_OF {ord: $ord}]->(child)
		`, map[string]any{"id": actionID, "childId": part.ChildActionTypeID, "ord": part.Ord}); err != nil {
			return err
		}
	}
	return nil
}

// replaceComposedOfEdges is used by UpdateActionType — full delete-then-
// recreate, because a stale COMPOSED_OF edge left over from a previous
// version of the composition would silently corrupt AC9's depth/leaf-count
// accounting for anyone composing THIS action in turn. See UpdateActionType.
func replaceComposedOfEdges(ctx context.Context, tx neo4j.ManagedTransaction, actionID string, composedOf []ComposedOfInput) error {
	if _, err := tx.Run(ctx, `
		MATCH (a:ActionType {id: $id})-[r:COMPOSED_OF]->()
		DELETE r
	`, map[string]any{"id": actionID}); err != nil {
		return err
	}
	return writeComposedOfEdges(ctx, tx, actionID, composedOf)
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int64:
		return int(n), true
	case int:
		return n, true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}
