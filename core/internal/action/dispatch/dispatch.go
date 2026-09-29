// Package dispatch is the "general path" this task's brief asks for — P2
// stage ③, item 4: read an :ActionType's data, run its
// validationPredicates through internal/action/predicate's registry,
// compute the required gate class from its data (gateFor, gatefor.go), and
// — only when no gate is required — run its effects through
// internal/action/effect's registry. docs/design/80-action-layer.md §5.3.1
// (the two registries), §6.3 (execute() pseudocode, simplified — see below
// for what is NOT implemented), §7.3 (gateFromTaint), AD4/AC7 (gateFor is a
// pure derivation, not a third authorization engine).
//
// SCOPE — WHERE THIS STAGE'S "일반 경로" STOPS (this task's brief, "제외"
// list): Dispatch computes the gate a call requires and, if that gate is
// "none", runs the effects. It does NOT create an :Approval node, does not
// publish approval.request over NATS, does not implement AD6's
// bound/current revision strictest-merge (§5.2), and does not wait for or
// consume a human decision. A call whose gate is not "none" simply returns
// with Executed=false and the required Gate reported — this is the exact
// stopping point this task's brief names: "게이트 요청을 만들어야 한다는
// 판정까지". Building the round trip beyond that point is P4's job.
//
// NOT §6'S COMPOSITION EXECUTOR: this package does not walk COMPOSED_OF
// edges, does not recompute derivedHash, and does not implement §6.3's
// partial-failure/compensation semantics. Dispatch's `Effects` field is a
// flat, ordered list — exactly what an origin:'system' leaf row's
// :ActionType.effects already is (§4.2: "origin:'composed'이면 빈
// 배열이어야 한다" — i.e. leaf rows are the only rows with a non-empty
// Effects list in the first place). This task's brief is explicit that
// composition's EXECUTION semantics (§6) must not be built before stage ③
// passes (80 §11's own ordering, and this task's "절대 제약": "조합의 실행
// 의미론과 오염을 절대 구현하지 마라").
//
// WHY NO GRAPHDB IMPORT: this package deliberately does not import
// osiris-core/internal/graphdb, mirroring
// internal/graphdb/actiontype_reconcile.go's own stated one-directional
// dependency arrow ("main -> {graphdb, action/predicate, action/effect},
// never graphdb -> action/*") extended one hop further: this package reads
// an already-loaded, already-validated :ActionType projection
// (LoadedActionType, below) rather than a live graphdb.ActionTypeInput —
// keeping "how a row is read from Neo4j" and "how a row's data drives a
// call" as two separately testable concerns. A caller (a future osiris-api
// or osiris-core execution endpoint, not built in this stage) is
// responsible for the graphdb.Client read and the projection into
// LoadedActionType.
//
// PARAMETER NAMING — WHY NO PER-ACTION MAPPING LAYER: predicate.Func and
// effect.Func both take the SAME `params map[string]any` this package
// receives from its caller, unmodified — Dispatch does not rename or
// reshape keys per action id (doing so would require either a hardcoded
// per-action-name mapping table, which is exactly the "if actionCode ==
// ..." branching this task's brief forbids, or a second, undocumented
// mapping language nothing in 80-action-layer.md describes). The practical
// consequence, visible in actiontype_gitcommit.go's parametersSchemaJson:
// an :ActionType's parameter names must double as the literal keys the
// predicates/effects it references expect (e.g. "branch", "max", "size").
// This is a real, load-bearing simplification of this narrow stage — see
// this task's final report for why it is not a general solution.
package dispatch

import (
	"context"
	"errors"
	"fmt"

	"osiris-core/internal/action/effect"
	"osiris-core/internal/action/predicate"
)

// LoadedActionType is the read-side projection of a :ActionType row this
// package's Dispatch needs — deliberately NOT graphdb.ActionTypeInput (that
// type is the WRITE-side shape and carries fields, e.g. createdAtRevision,
// origin, iri, this package has no use for). Field names mirror 80 §4.2's
// property names for the ones that matter to dispatch.
type LoadedActionType struct {
	ID     string
	Status string // must be "active" — anything else fails closed (ErrNotActive)

	ValidationPredicates []string // §4.2 — names looked up via PredicateLookupFunc
	Effects              []string // §4.2 — names looked up via EffectLookupFunc, run in slice order

	GateClass  string // §4.2: none | approval.team_lead | approval.admin
	TaintClass string // §4.2: neutral | escalate | forbidden
}

// PredicateLookupFunc/EffectLookupFunc match predicate.Lookup/effect.Lookup
// exactly, so callers pass those functions directly with no adapter.
type PredicateLookupFunc func(name string) (predicate.Func, bool)
type EffectLookupFunc func(name string) (effect.Func, bool)

// Outcome is what one Dispatch call reports.
type Outcome struct {
	ActionID string
	Gate     string // the gate class this call required, per gateFor
	Executed bool   // true iff Effects actually ran (only possible when Gate == "none")
	Results  []effect.Artifact
}

var (
	// ErrNotActive — a non-active :ActionType may never be dispatched. This
	// is the narrow-stage analogue of AD6's "if a.status != 'active' ->
	// 집행 안 함" — without the surrounding re-read-at-execution-time
	// machinery (AD6 in full presupposes the approval round trip this stage
	// does not build).
	ErrNotActive = errors.New("dispatch: action is not status:'active'")

	// ErrUnknownPredicate/ErrUnknownEffect are AC3's RUNTIME half —
	// fail-closed, not skip. graphdb's CA3 check (actiontype.go) already
	// prevents this at WRITE time and actiontype_reconcile.go's boot sweep
	// prevents a since-broken reference from staying 'active'; this check
	// is what a caller sees if, despite those, a row reaching Dispatch
	// still names something unregistered (e.g. a row loaded by a stale
	// osiris-core binary reading a graph written by a newer one).
	ErrUnknownPredicate = errors.New("dispatch: validationPredicates references a name not in the compiled predicate registry (AC3 runtime half, fail-closed)")
	ErrUnknownEffect    = errors.New("dispatch: effects references a name not in the compiled effect registry (AC3 runtime half, fail-closed)")

	// ErrPredicateFailed — a validation predicate returned ok=false. No
	// effect in the row's Effects list runs.
	ErrPredicateFailed = errors.New("dispatch: a validation predicate returned false")
)

// Dispatch is the general-path entry point this task's item 4 asks for. It
// contains NO branch keyed on a.ID or a.Code anywhere — every decision it
// makes reads only a's declared fields (ValidationPredicates, Effects,
// GateClass, TaintClass, Status) and the caller-supplied params/snapshot/
// taintLevel. See this task's final report for how that absence was
// verified (grep across this file for any string literal action id).
func Dispatch(
	ctx context.Context,
	a LoadedActionType,
	params map[string]any,
	snap predicate.GraphSnapshot,
	taintLevel string,
	lookupPredicate PredicateLookupFunc,
	lookupEffect EffectLookupFunc,
) (Outcome, error) {
	out := Outcome{ActionID: a.ID}

	if a.Status != "active" {
		return out, fmt.Errorf("%w: %s status=%q", ErrNotActive, a.ID, a.Status)
	}

	for _, name := range a.ValidationPredicates {
		fn, ok := lookupPredicate(name)
		if !ok {
			return out, fmt.Errorf("%w: %q", ErrUnknownPredicate, name)
		}
		passed, reason := fn(ctx, params, snap)
		if !passed {
			return out, fmt.Errorf("%w: predicate %q: %s", ErrPredicateFailed, name, reason)
		}
	}

	gate, err := gateFor(a, taintLevel)
	if err != nil {
		return out, err
	}
	out.Gate = gate

	if gate != "none" {
		// This is stage ③'s stopping point — see this file's package doc
		// comment "SCOPE" section. No effect runs; the caller is expected
		// to be (in a future stage) whatever creates the gate request.
		return out, nil
	}

	for _, name := range a.Effects {
		fn, ok := lookupEffect(name)
		if !ok {
			return out, fmt.Errorf("%w: %q", ErrUnknownEffect, name)
		}
		artifact, err := fn(ctx, params)
		if err != nil {
			return out, fmt.Errorf("dispatch: effect %q failed: %w", name, err)
		}
		out.Results = append(out.Results, artifact)
	}
	out.Executed = true
	return out, nil
}
