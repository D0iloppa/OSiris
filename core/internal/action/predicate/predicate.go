// Package predicate is osiris-core's validation-predicate registry —
// docs/design/80-action-layer.md §5.3 (the third of AD2's "three
// safeguards", (c) "선언은 데이터, 실행체는 코드") and §5.3.1's registry
// table:
//
//	predicate.*  →  func(ctx, params, graphSnapshot) (ok bool, reason string)
//	                 — 부작용 없음. 순수 함수.
//
// A predicate answers ONE yes/no question about a proposed action call's
// resolved parameters. It never mutates anything, never performs I/O of its
// own (any fact it needs beyond `params` comes through the GraphSnapshot the
// caller passes in — see that type's doc comment), and never chooses which
// predicate runs — that selection is entirely :ActionType.validationPredicates
// data (§4.2), read and dispatched by code outside this package.
//
// REGISTRY DISCIPLINE (mirrors orchestrator/internal/catalog/catalog.go's
// Table, and grant-catalog's "nobody edits this at runtime" rule): the name
// → Func map below is an unexported package-level literal, populated once at
// package init and never written to again. Lookup/Names are the only public
// surface; there is no exported map and no setter. This is what makes
// AD2(c)'s "declaration is data, executable is code" true in the compiled
// binary — the SET of predicates a running osiris-core can invoke is fixed
// at build time, and no graph data can add, remove, or redefine an entry.
//
// SKELETON SCOPE (P2 stage ①, §11's "구현 워커" ordering item ①): this
// package holds ONLY the two predicates the git.commit worked example
// (§4.6, §5.3.1) needs to eventually validate a commit-shaped call —
// diff.size_under and branch.not_protected. It does not implement
// predicate.amount.within_limit or predicate.target.owned_by_own_team (also
// named in §5.3.1's table as illustrative examples, not as a requirement of
// this stage) or predicate.mail.recipient_in_directory (§5.3.3's
// mail.send-mitigation example) — those are for whichever stage actually
// wires up the :ActionType row that needs them (§11 item ④, "나머지 행").
// Registering a predicate nothing yet calls would be exactly the kind of
// speculative surface docs/design's own conventions discourage.
//
// [판단, 문서 결정 아님] §4.6's worked-example table has no
// validationPredicates column, so nothing in 80-action-layer.md actually
// pins "git.commit requires exactly these two predicates" — that pairing is
// this implementation's own conservative reading of §5.3.1's two named
// examples as the natural minimum for a commit-shaped action (a size limit
// and a protected-branch check). Whoever authors git.commit's actual
// :ActionType.validationPredicates row (stage ③, §11 item ③) may reasonably
// choose a different subset; nothing here presumes it must be these two.
//
// CATALOG_VERSION: §5.3.1's closing paragraph says the predicate and effect
// registries "share" CATALOG_VERSION with the rest of the grant-catalog (an
// "I9 확장" — I9 being checkI9_PermissionCatalogVersion,
// internal/graphdb/invariants.go). Deliberately NOT modeled as a version
// constant in this package: I9 exists because osiris-orchestrator is a
// SEPARATE compiled binary that can independently drift from osiris-core's
// CATALOG_VERSION, which is exactly what CheckCatalogVersion cross-checks.
// This package is not a separate binary — it is compiled directly into
// osiris-core, so whatever CATALOG_VERSION value a given osiris-core build
// is configured with (config.Config.CatalogVersion) already, by
// construction, governs the exact set of predicates available in that same
// build. There is no second artifact for a version string to drift against,
// so adding one here would be a constant with nothing to check it against —
// see this task's final report for the explicit call-out of this judgment.
package predicate

import (
	"context"
	"fmt"
	"sort"
)

// GraphSnapshot is the read-only fact source a predicate may consult beyond
// its own `params`. Kept to exactly the one method today's two predicates
// need (Karpathy #2 — do not build the wide interface you don't yet have a
// second caller for); a future predicate needing a different fact grows
// this interface then, not now.
type GraphSnapshot interface {
	// BranchProtected reports whether `branch` is a protected branch in the
	// repository the calling :ActionType invocation targets. `ok` is false
	// when protection status could not be determined (e.g. the SCM lookup
	// that backs this is unavailable) — predicate.branch.not_protected
	// treats "unknown" the same as "protected": fail-closed. A predicate
	// being "부작용 없음" (§5.3.1) means the predicate itself performs no
	// I/O; it says nothing about what the caller does with an unknown fact,
	// and this package chooses the safe default.
	BranchProtected(ctx context.Context, branch string) (protected bool, ok bool)
}

// Func is the predicate registry's value signature, verbatim from §5.3.1's
// table: pure, no side effects, decides one proposed call.
type Func func(ctx context.Context, params map[string]any, snap GraphSnapshot) (ok bool, reason string)

// registry is the fixed, version-pinned map — see the package doc comment's
// "REGISTRY DISCIPLINE" section. Unexported; Lookup/Names are the only way
// in.
var registry = map[string]Func{
	"predicate.diff.size_under":      diffSizeUnder,
	"predicate.branch.not_protected": branchNotProtected,
}

// Lookup returns the predicate function registered under name, or
// ok=false if name is not registered. Callers (the future action-execution
// path, stage ③) MUST treat ok=false as a hard validation failure — never
// as "no predicate, so allow" — matching AC3's fail-closed contract
// (80 §9). This mirrors graphdb.ReconcileActionTypes' boot-time check
// against Names() below, so a row that references an unregistered
// predicate id can never even reach this call path (it is degraded to
// status:'inert' first).
func Lookup(name string) (Func, bool) {
	fn, ok := registry[name]
	return fn, ok
}

// Names returns the sorted list of registered predicate ids — the
// reference set graphdb.ReconcileActionTypes checks
// :ActionType.validationPredicates against (80 §9 AC3).
func Names() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// diffSizeUnder implements predicate.diff.size_under (§5.3.1's own worked
// example — "predicate.diff.size_under(1000)"). Both operands come from
// `params`:
//
//	max  (required, number) — the :ActionType-authored inclusive-exclusive
//	     upper bound (this call's effective parameter value, per §6.5's
//	     "실효값" rule — never a raw, unresolved default).
//	size (required, number) — the diff size ACTUALLY measured for this
//	     call. Populating this honestly, from the real diff rather than a
//	     caller-supplied claim, is the execution engine's responsibility
//	     (stage ③) — a pure predicate has no way to verify where its own
//	     inputs came from; that is why this stage does not implement the
//	     engine that calls it.
//
// Missing or non-numeric params fail closed (ok=false).
func diffSizeUnder(_ context.Context, params map[string]any, _ GraphSnapshot) (bool, string) {
	max, okMax := numberParam(params, "max")
	size, okSize := numberParam(params, "size")
	if !okMax || !okSize {
		return false, "predicate.diff.size_under: missing or non-numeric 'max'/'size' parameter"
	}
	if size < max {
		return true, ""
	}
	return false, fmt.Sprintf("predicate.diff.size_under: diff size %v is not under limit %v", size, max)
}

// branchNotProtected implements predicate.branch.not_protected (§5.3.1's
// other named worked example). `params["branch"]` names the target branch;
// protection status is NOT trusted from params (a caller could otherwise
// simply claim its own target branch is unprotected) — it comes from
// GraphSnapshot.BranchProtected, an external fact source resolved by the
// caller before this predicate runs, exactly per the type this package
// exposes for that purpose.
func branchNotProtected(ctx context.Context, params map[string]any, snap GraphSnapshot) (bool, string) {
	branchRaw, present := params["branch"]
	branch, isStr := branchRaw.(string)
	if !present || !isStr || branch == "" {
		return false, "predicate.branch.not_protected: missing 'branch' parameter"
	}
	if snap == nil {
		return false, "predicate.branch.not_protected: no graph snapshot available (fail-closed)"
	}
	protected, known := snap.BranchProtected(ctx, branch)
	if !known {
		return false, fmt.Sprintf("predicate.branch.not_protected: protection status of %q is unknown (fail-closed)", branch)
	}
	if protected {
		return false, fmt.Sprintf("predicate.branch.not_protected: branch %q is protected", branch)
	}
	return true, ""
}

// numberParam extracts a float64 from a JSON-decoded params map. Accepts
// float64 (the standard encoding/json numeric type) and int for callers
// that build params programmatically rather than via json.Unmarshal.
func numberParam(params map[string]any, key string) (float64, bool) {
	v, ok := params[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
