package assign

import (
	"fmt"
	"sort"
	"time"
)

// Evaluate is 20-ontology-schema.md §8.4's `evaluate(c, task, policy)`, F1
// through F6 in full. (An earlier pass of this build scoped this function to
// "F1~F5" per an incorrect citation in that task's brief; F6 is in fact
// inside §8.4, lines 711-713, and 20 §8.6 line 781's
// `blockers ⊆ {UNAVAILABLE, CAPACITY_EXCEEDED, BUDGET_EXHAUSTED}` fallback
// branch depended on F6 ever producing BUDGET_EXHAUSTED — omitting F6 made
// that branch permanently unreachable, not just "a filter left out". See F6's
// own block below for how teamBudgetExhausted, which §8.4 calls but does not
// define, is handled given the schema has no spend-tracking data to compute
// it from.)
//
// Every filter runs to completion; reasons are never short-circuited (§8.2's
// explicit instruction, "모든 필터를 끝까지 평가", because the console's
// rationale popover needs every exclusion reason, not just the first).
//
// Determinism: this function has no hidden state (no time.Now(), no map
// iteration whose order leaks into output) — ExclusionReasons/
// MatchedCapabilities/MissingCapabilities are built by iterating
// task.RequiredCapabilities (caller-ordered, stable) and needPerms is sorted
// before use. Calling Evaluate twice with the same (Candidate, Task, Policy)
// always produces a byte-identical Evaluation.
func Evaluate(c Candidate, task Task, policy Policy) Evaluation {
	var reasons []string
	var matched []string
	var missing []string
	specificity := 0

	// --- F1: capabilities ---------------------------------------------
	for _, req := range task.RequiredCapabilities {
		m := c.CapabilityMatches[req.Code] // zero value = no match, Exists=false
		if m.Exists && m.Level >= req.MinLevel {
			matched = append(matched, fmt.Sprintf("%s@%d>=%d", req.Code, m.Level, req.MinLevel))
			if m.Exact {
				specificity += 2 // exact match
			} else {
				specificity += 1 // broader-ancestor match
			}
		} else {
			missing = append(missing, req.Code)
			reasons = append(reasons, "CAPABILITY_MISSING:"+req.Code)
		}
	}

	// --- F2: permissions -------------------------------------------------
	// needPerms = union of REQUIRES_PERMISSION(cap) for every required
	// capability, plus (for external_effect tasks) gatePermissionFor(task).
	needPermsSet := map[string]bool{}
	for _, req := range task.RequiredCapabilities {
		for _, p := range req.RequiredPermissions {
			needPermsSet[p] = true
		}
	}
	if task.ActionClass == ActionClassExternalEffect {
		if gate, ok := GatePermissionFor(task); ok {
			needPermsSet[gate] = true
		} else {
			// GatePermissionFor's mapping source could not be resolved from
			// the design docs for this build (see its doc comment). Fail
			// closed: an external_effect task whose gate permission is
			// unknown excludes every candidate, rather than silently
			// skipping the check (which would let external-effect actions
			// dispatch ungated — exactly what F2 exists to prevent, 20
			// §7.2 item 1).
			reasons = append(reasons, "NOT_PERMITTED:unresolved-gate")
		}
	}
	needPerms := make([]string, 0, len(needPermsSet))
	for p := range needPermsSet {
		needPerms = append(needPerms, p)
	}
	sort.Strings(needPerms) // determinism: map iteration order is not stable
	for _, perm := range needPerms {
		if !c.EffectivePermissions[perm] {
			reasons = append(reasons, "NOT_PERMITTED:"+perm)
		}
	}

	// --- F3: availability -------------------------------------------------
	if c.Kind == KindAIAgent {
		if !c.Ready || c.RuntimePhase == "Failed" || c.RuntimePhase == "Suspended" || c.RuntimePhase == "Provisioning" {
			reason := c.NotReadyReason
			if reason == "" {
				reason = c.RuntimePhase
			}
			reasons = append(reasons, "UNAVAILABLE:"+reason)
		}
	} else {
		if c.Availability == "off" || c.Availability == "leave" || OnLeaveAt(c, task.DueAt) {
			reasons = append(reasons, "UNAVAILABLE:leave")
		}
	}

	// --- F4: capacity -------------------------------------------------
	var load float64
	if c.MaxTaskPoints > 0 {
		load = float64(c.ActiveTaskPoints) / float64(c.MaxTaskPoints)
	}
	if c.ActiveTaskPoints+task.EffortPoints > c.MaxTaskPoints {
		reasons = append(reasons, "CAPACITY_EXCEEDED")
	}

	// --- F5: data scope --------------------------------------------------
	// scopeAllows(c, task.securityLabel) is held pending by explicit scope
	// decision for this build (not invented here): the schema (20 §3, full
	// pass over :Person/:AIAgent/:Team property tables) has NO property or
	// relationship expressing which security labels a party may access. A
	// fail-OPEN implementation (always true) would silently defeat F5's
	// purpose as a "배정 하드 필터" the moment a real securityLabel value
	// shows up.
	//
	// CORRECTED (was: "" passes, anything else fails-closed — backwards):
	// 20 §3.4 line 244 marks :WorkItem.securityLabel REQUIRED (✔) with three
	// enum values: public / internal / confidential. There is no "unset"
	// option in the real schema — every real :WorkItem carries one of the
	// three. Treating "" as "no restriction" therefore meant every actual
	// task failed F5 and assign() could never succeed at all (including the
	// onboarding rehearsal, 30 §5.2, which requires outcome==ASSIGNED) —
	// not a narrow "security-labeled tasks are held pending" carve-out, but
	// a total outage. The fix: `public` IS the enum's "not sensitive" value
	// — that is the entire reason the enum has three values instead of two
	// (an optional label plus internal/confidential would need no third
	// value if public didn't mean "no restriction"). A filter that no enum
	// value can ever pass isn't a filter, it's a kill switch, and reading
	// the field that way empties out its own documented purpose ("배정 하드
	// 필터"). So:
	//   - "public"                 -> passes unconditionally (nothing to check)
	//   - "internal"/"confidential" -> fail-closed, excludes every candidate
	//     (no scope-grant data exists yet to prove any candidate covers it;
	//     unchanged from before — replace only this branch when a real
	//     security-label access model is designed)
	//   - "" (missing) or any other unrecognized value -> fail-closed too.
	//     A required field being empty is a DATA error, not "no label", and
	//     must not be treated as more permissive than a task that actually
	//     declares "public".
	switch task.SecurityLabel {
	case "public":
		// no restriction — the enum's own "not sensitive" value.
	case "internal", "confidential":
		reasons = append(reasons, "SCOPE_VIOLATION:"+task.SecurityLabel)
	case "":
		reasons = append(reasons, "SCOPE_VIOLATION:unset")
	default:
		reasons = append(reasons, "SCOPE_VIOLATION:"+task.SecurityLabel)
	}

	// --- F6: team budget (AIAgent only) -----------------------------------
	// teamBudgetExhausted(teamOf(c)) (20 §8.4 lines 711-713) is not defined
	// by the doc. 20 §3.1 line 120 confirms :Team DOES have a
	// monthlyBudgetUsd property (a CAP), but no field anywhere tracks actual
	// spend/consumption against that cap — 20 §12's exclusions table is
	// explicit that "고빈도 시계열(비용·로그·하트비트)" is P6's territory
	// (JetStream), deliberately NOT stored in the graph. So there is a
	// budget CEILING in the graph but no CONSUMPTION figure to compare it
	// against, and this function has no way to compute "exhausted" from
	// graph data alone.
	//
	// [판단] Given that, c.BudgetExhausted (populated by the graph layer,
	// see graphdb.CandidateSet) is currently always false — "not exhausted"
	// is the correct default, NOT "exhausted": unlike F5's missing-scope-data
	// case (where failing closed is the safe direction because a security
	// boundary is at stake), treating an absent budget figure as "exhausted"
	// has no security justification and would permanently exclude every
	// :AIAgent from every task, silently overturning 20 §8.3's explicit
	// "사람 + AI 동일 취급 (P1)" in code. Replace only this field's
	// population once real spend tracking is wired from JetStream into
	// RuntimeState-equivalent data.
	if c.Kind == KindAIAgent && c.BudgetExhausted {
		reasons = append(reasons, "BUDGET_EXHAUSTED")
	}

	verdict := VerdictEligible
	if len(reasons) > 0 {
		verdict = VerdictExcluded
	}

	return Evaluation{
		PartyID:             c.PartyID,
		PartyKind:           c.Kind,
		DisplayName:         c.Name,
		Verdict:             verdict,
		ExclusionReasons:    reasons,
		MatchedCapabilities: matched,
		MissingCapabilities: missing,
		SpecificityScore:    specificity,
		LoadAtDecision:      load,
		SameTeam:            c.TeamID != "" && c.TeamID == task.OwningTeamID,
		ContextContinuity:   c.HasWorkedOnAncestor,
		VerificationRecency: c.VerificationRecency,
	}
}

// MinVerifiedAt is 20 §8.4's `minVerifiedAt(c, task.requiredCapabilities)`
// (line 725) — trivially derivable from HAS_CAPABILITY.verifiedAt (20 §4
// line 403): the EARLIEST (least recent) verifiedAt among the capabilities
// that actually satisfied a requirement (i.e. CapabilityMatches[req.Code]
// with Exists && Level >= req.MinLevel). Using the earliest — the weakest
// link — rather than the latest is deliberate: tie-break stage 5 rewards
// "recently verified", and a candidate whose OTHER required capabilities
// were verified long ago shouldn't get credit for one freshly-verified one.
// Requirements with no satisfying match do not contribute (F1 already
// excludes such candidates from the eligible set; this only needs to be
// well-defined for eligible candidates, but is total for any input).
// Returns the zero time.Time if no requirement has a satisfying match.
func MinVerifiedAt(c Candidate, reqs []CapabilityRequirement) time.Time {
	var earliest time.Time
	for _, req := range reqs {
		m, ok := c.CapabilityMatches[req.Code]
		if !ok || !m.Exists || m.Level < req.MinLevel || m.VerifiedAt.IsZero() {
			continue
		}
		if earliest.IsZero() || m.VerifiedAt.Before(earliest) {
			earliest = m.VerifiedAt
		}
	}
	return earliest
}

// GatePermissionFor is 20 §8.4's `gatePermissionFor(task)` (line 688),
// called only for actionClass == external_effect.
//
// ITS MAPPING SOURCE COULD NOT BE CONFIRMED FROM THE DESIGN DOCS for this
// build. The only candidate mapping in the corpus is 80-action-layer.md's
// (:ActionType)-[:REQUIRES_PERMISSION]->(:Permission) mirror (AC20, §8.1),
// which would let a WorkItem reach a gate permission via its ActionType.
//
// [2026-08-13, G-A11 CLOSED — this comment corrected accordingly] An earlier
// revision of this comment blamed G-A11 for why that path could not be
// used: it said CheckActionTypePermissionMirror (actiontype_permission_
// mirror.go) refused to boot the instant a REQUIRES_PERMISSION edge existed
// because the min-derivation formula was unresolved. That is no longer
// true — G-A11 is resolved (80 §10 marks it 해소): the boot check now
// computes and verifies the actual min-of-gateClass derivation instead of
// refusing outright, and a REQUIRES_PERMISSION edge (e.g. git.commit's, per
// §4.6) no longer trips anything.
//
// G-A11's closure does NOT unblock GatePermissionFor, though — the real
// blocker was always a DIFFERENT gap, now tracked as G-A12: there is no
// :WorkItem -> :ActionType bridge at all.
//   - Task (this package) has no ActionTypeID or similar field — 20 §3.4's
//     :WorkItem property table has no such column.
//   - 80 §8.3's existing bridge goes :WorkItem -[:REQUIRES_CAPABILITY]->
//     :Capability -[:REQUIRES_PERMISSION]-> :Permission <-[:REQUIRES_
//     PERMISSION]- :ActionType — it reaches a SET of ActionTypes via shared
//     capabilities/permissions, not one specific ActionType a task invokes,
//     and §8.3 itself names the resulting over-approximation as an accepted
//     cost, not a precision guarantee.
//   - task.actionClass is a 2-valued enum (internal/external_effect) while
//     :ActionType already has 12+ rows (§4.6) — the cardinality does not
//     support recovering "which ActionType" from actionClass alone.
//
// So even with G-A11 closed, there is still no resolvable Task -> gate-
// permission mapping; the blocker moved from "the mirror formula might be
// wrong" (G-A11) to "there is nothing to point the mirror lookup AT" (G-A12).
// GatePermissionFor therefore still always returns ("", false) — fail-
// closed, per this task's original instruction ("문서로 출처를 확정할 수
// 없으면 fail-closed로 구현"). See Evaluate's F2 block for the consequence:
// every external_effect task excludes every candidate until either (a) a
// real Task->gate-permission mapping is designed, or (b) G-A12 is resolved
// (a :WorkItem->:ActionType bridge is introduced) and this function is
// rewritten to use it.
func GatePermissionFor(_ Task) (string, bool) {
	return "", false
}

// OnLeaveAt is 20 §8.4's `onLeaveAt(c, task.dueAt)` — trivially derivable
// from :Person.leaveFrom/leaveTo (20 §3.1 line 99): the due date falls
// inside the candidate's leave window. Both bounds are inclusive, and a
// candidate with no leave window set (LeaveFrom/LeaveTo both nil) is never
// considered on leave.
func OnLeaveAt(c Candidate, dueAt time.Time) bool {
	if c.LeaveFrom == nil || c.LeaveTo == nil || dueAt.IsZero() {
		return false
	}
	return !dueAt.Before(*c.LeaveFrom) && !dueAt.After(*c.LeaveTo)
}
