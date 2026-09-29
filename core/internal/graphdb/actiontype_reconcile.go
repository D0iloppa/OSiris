// Boot-time :ActionType reconciliation — docs/design/80-action-layer.md
// §5.3.1, §9 AC2/AC3. This is P2 stage ①'s deliverable (§11 "구현 워커"
// ordering item ①): the registry skeletons live in
// osiris-core/internal/action/{predicate,effect}; this file is the
// fail-closed gate that checks graph data against them.
//
// WHY THIS IS A SEPARATE FILE/FUNCTION FROM invariants.go's I1-I17, NOT A
// NEW checkI* ADDED TO THAT SLICE:
//   - CheckInvariants (invariants.go) is wired to api/server.go's
//     "/v1/..." diagnostics route (see server.go:351) — it runs on demand,
//     on every admin poll, and is documented as read-only ("Everything here
//     is read-only (neo4j.AccessModeRead sessions). This package never
//     mutates the graph in response to a check"). Folding a full
//     :ActionType table scan *plus a write* into that hot, repeatable,
//     read-only path would (a) break that documented invariant of the file,
//     (b) re-run a graph-wide write on every single poll instead of once at
//     boot, and (c) fire security.actiontype.unknown repeatedly for rows
//     that were already degraded on a prior boot, which the idempotency
//     requirement below rules out.
//   - AC2/AC3 are graded 치명(critical) specifically because an undetected
//     drift lets a since-removed predicate/effect/action id keep looking
//     "active". That calls for a boot-time gate exactly like
//     ApplyMigrations/SeedCatalog (cmd/osiris-core/main.go's existing boot
//     sequence) — run once, before any traffic (readyz) is served — not a
//     periodically-polled diagnostic.
//
// FAIL-CLOSED, NOT SKIP: this file is the deliberate photographic negative
// of orchestrator/internal/catalog/catalog.go's
// `row, ok := Table[g]; if !ok { continue }` idiom (see e.g. that file's
// AllowedTools/DeniedTools/MCPServers/EgressAllowlist). There, an
// unrecognized grant CODE is safe to silently skip — a stale/unknown grant
// contributes nothing, and version drift is caught elsewhere
// (CheckCatalogVersion). Here, silently skipping an :ActionType row with an
// unknown id/predicate/effect would leave it status:'active' —
// indistinguishable in the graph from a fully valid, correctly-gated
// action. §5.3.1 is explicit about why that is the wrong failure mode:
// "건너뛴 액션이 '게이트가 없는 액션'으로 읽히면 정확히 반대의 결과가
// 나온다." So this file DEGRADES (status:'inert') instead of ignoring.
package graphdb

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ActionTypeEventPublisher is the narrow slice of *events.Publisher this
// file needs (one method). Declared here instead of importing the events
// package so graphdb does not have to depend on the NATS client just to
// call one method — *events.Publisher satisfies this interface
// structurally; cmd/osiris-core/main.go is what wires the concrete type in.
type ActionTypeEventPublisher interface {
	PublishSecurityActionTypeUnknown(ctx context.Context, ids []string) error
}

// ActionTypeReconcileReport is what main.go logs after a boot reconcile
// pass.
type ActionTypeReconcileReport struct {
	// DegradedIDs holds the :ActionType.id values newly moved to
	// status:'inert' THIS run. A row already 'inert' from a previous boot
	// is neither rewritten nor reported here (idempotency requirement,
	// §5.3.1: fail-closed does not mean "notify forever").
	DegradedIDs []string

	// PublishErr is non-nil only when DegradedIDs is non-empty AND the
	// security.actiontype.unknown publish itself failed. See this file's
	// ReconcileActionTypes doc comment for why that does not become a
	// return error (and therefore does not fail boot).
	PublishErr error
}

// ReconcileActionTypes implements the boot-time sweep 80 §5.3.1 requires:
// walk every :ActionType, and for any row whose origin:'system' id is not
// in knownActionIDs (AC2), or whose validationPredicates/effects contains
// an element not in knownPredicates/knownEffects (AC3), flip
// status → 'inert'. It is the caller's job (main.go) to build those three
// sets from the compiled-in registries — this package deliberately does
// NOT import osiris-core/internal/action/{predicate,effect} itself, so that
// graphdb (the single graph-write chokepoint, A5/G7/D17) never needs to
// know the action layer's registry packages exist. Passing plain
// map[string]struct{} sets keeps the dependency arrow one-directional:
// main → {graphdb, action/predicate, action/effect}, never
// graphdb → action/*.
//
// Idempotent by construction: the Cypher predicate below already excludes
// status = 'inert' rows, so re-running this every boot (as main.go does)
// only ever touches rows that just became non-compliant, and never re-fires
// the event for rows degraded on an earlier boot.
//
// There are no :ActionType nodes in the graph today — §4's schema and the
// graph-write whitelist that would let anything create one are stage ②'s
// job (docs/design/80-action-layer.md §11's ordering). Until ② ships, this
// query always matches zero rows and the function is a no-op. That is the
// intended state, not a bug: the backstop needs to exist BEFORE ② starts
// writing rows, not after.
//
// knownActionIDs is deliberately passed in as an empty set by main.go for
// now (see that call site's comment) — AC2's "실행 바이너리의 액션
// 레지스트리" is the future :ActionType seed list (analogous to seed.go's
// SystemPermissions/SystemCapabilities), which does not exist until stage ②
// adds :ActionType seeding. Passing an empty set here does not silently
// approve anything: with zero :ActionType{origin:'system'} rows in the
// graph (also true until ②), the AC2 half of the WHERE clause matches
// nothing either way. Wiring the real list in is ②'s job, not a regression
// introduced here.
func ReconcileActionTypes(
	ctx context.Context,
	c *Client,
	pub ActionTypeEventPublisher,
	knownActionIDs map[string]struct{},
	knownPredicates map[string]struct{},
	knownEffects map[string]struct{},
) (ActionTypeReconcileReport, error) {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, `
			MATCH (a:ActionType)
			WHERE a.status <> 'inert'
			  AND (
			    (a.origin = 'system' AND NOT a.id IN $knownActionIDs)
			    OR any(p IN coalesce(a.validationPredicates, []) WHERE NOT p IN $knownPredicates)
			    OR any(e IN coalesce(a.effects, []) WHERE NOT e IN $knownEffects)
			  )
			SET a.status = 'inert'
			RETURN a.id AS id
		`, map[string]any{
			"knownActionIDs":  setToSlice(knownActionIDs),
			"knownPredicates": setToSlice(knownPredicates),
			"knownEffects":    setToSlice(knownEffects),
		})
		if err != nil {
			return nil, err
		}
		return collectStrings(ctx, result, "id"), nil
	})
	if err != nil {
		return ActionTypeReconcileReport{}, fmt.Errorf("reconcile action types (AC2/AC3): %w", err)
	}

	degraded, _ := res.([]string)
	report := ActionTypeReconcileReport{DegradedIDs: degraded}
	if len(degraded) == 0 {
		return report, nil // nothing changed this run — no event, per the idempotency requirement
	}

	// Publish security.actiontype.unknown (shared by AC2 and AC3, per
	// §5.3.1's single sentence covering both).
	//
	// WHY A PUBLISH FAILURE HERE DOES NOT FAIL BOOT (contrast with DG8/AC11,
	// §5.1.2): DG8's rule — "발행 실패 시 변경하지 않는다(fail-closed)" — exists
	// because THAT write is a *relaxation* (gate/taint/effect/reversibility
	// moving to the LESS strict side, or reviving a withdrawn action): the
	// dangerous direction, so an unnotified relaxation must not be allowed
	// to stand. The write this function makes is the opposite direction —
	// it can only ever move a row TO 'inert' (strictly more restrictive,
	// "cannot execute"). That safety property is already fully established
	// by the graph write above, before any publish is attempted. A failed
	// notification means an operator learns about it late (or via the next
	// boot's idempotent re-check, if the row somehow reverted), not that an
	// unsafe state was reached. Aborting boot over a notification failure
	// here would convert a transient NATS hiccup into a full core outage to
	// protect a property that does not need protecting — the graph is
	// already fail-closed. This mirrors the codebase's existing accepted
	// gap for org.revision (api/server.go's respondWrite: "A publish
	// failure here does not roll back the already-committed graph write").
	// By the time this runs, connectNatsWithRetry (main.go) has already
	// established the NATS connection, so a failure here is a genuine
	// runtime problem worth surfacing loudly — hence PublishErr is
	// returned to the caller to log, not swallowed.
	if pubErr := pub.PublishSecurityActionTypeUnknown(ctx, degraded); pubErr != nil {
		report.PublishErr = pubErr
	}
	return report, nil
}

func setToSlice(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
