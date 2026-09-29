// HumanOnly — 80-action-layer.md §5.1.3's P1-Q closed enumeration.
//
// ── WHAT THIS IS ────────────────────────────────────────────────────────────
//
// P1-Q (한정 규칙): "the set of permission codes an :AIAgent may neither hold
// nor be delegated is a CLOSED enumeration, and the single condition for
// membership is: exercising the code changes the definition itself of what
// the organization can do." §5.1.3 found that this rule was already applied
// three times before this task, as an unnamed pattern:
//
//  1. cap.exec.physical's HAS_CAPABILITY origin is :Person only     (20 §11 C2)
//  2. org.admin / org.manage.people / org.manage.roles are          (10 §8.1,
//     Person-only console permissions (all four grant-catalog        60 §20.4)
//     projection columns are "—")
//  3. IN_ORG membership is restricted to :Person                    (60 §21.3 I15,
//     20 §11 C9)
//
// This file adds the fourth and names the set for the first time:
// actiontype.write (80 §5.1.2 DG6).
//
// ── WHAT THIS IS NOT ─────────────────────────────────────────────────────────
//
// This is emphatically NOT MachineGrantable (authz.go). The two restrictions
// look similar — both are "permission code X may not reach principal kind Y"
// — and are otherwise unrelated:
//
//	MachineGrantable   which codes may appear in a KindMachine principal's
//	                   FIXED SCOPE (§19's machine principal — not a :Party,
//	                   never a graph node, verification-only). Allowlist:
//	                   only org.read.* may be issued to one at all.
//	HumanOnly          which codes an :AIAgent (a graph :Party, P1's "same
//	                   superclass as :Person") may never be GRANTED or
//	                   DELEGATED. Denylist: everything else is fair game —
//	                   §5.1.3 is explicit that even git.commit/pr.merge/
//	                   secret.rotate stay reachable by an :AIAgent, gated by
//	                   gateClass, not excluded by principal kind.
//
// A machine principal and an :AIAgent are different subjects entirely (one is
// §19's stateless bearer-token verifier, the other is a graph Party that runs
// tasks). Merging these two constants because both "keep something away from
// a non-human" would conflate a scope allowlist with a grant denylist and
// make neither one auditable on its own terms. Do not merge them.
//
// ── ENFORCEMENT (AC6, P1-Q's own stated upper bound) ────────────────────────
//
// §5.1.3: "AC6이 이 목록에 실행 권한 코드(=grant-catalog에서
// ClaudeAllowedTools/MCPTools/EgressAllowlist 중 하나라도 비어 있지 않은
// 코드)가 들어오면 린트를 실패시킨다." That cross-check spans two Go
// modules (this HumanOnly set lives in osiris-api; the grant-catalog table
// lives in orchestrator/internal/catalog, a separate module this package
// cannot import without creating a dependency neither binary needs at
// runtime) — so AC6 is enforced by bin/lint-action-layer.sh's static text
// check across both files, not by a Go-level test in either module alone.
// See that script's "AC6" section.
//
// ── THE OTHER HALF OF AC6's ENFORCEMENT: THIS SET HAS A THIRD COPY ─────────
//
// core/internal/graphdb/writeops.go's humanOnlyPermissionCodes is an
// INDEPENDENT, hand-maintained copy of this exact map's contents (module
// boundary again — core cannot import this package either). It is not
// merely documentation there: it is what mutatePartyGrant's structural DG6
// guard actually consults, and as of this writing it is the ONLY DG6
// defense wired into a live production path (no osiris-api route yet calls
// CheckDelegation's DG6 branch for a real request — see delegation.go's
// package doc comment). bin/lint-action-layer.sh's AC6 section's
// "sub-check (ii)" is what enforces that this HumanOnly map and
// writeops.go's humanOnlyPermissionCodes are the EXACT SAME SET, element
// for element — not merely the same length. If you add, remove, or rename a
// code here, you MUST make the identical change in writeops.go in the same
// edit, or the lint will fail (and if it somehow didn't, the two structural
// defenses this repository has for DG6 would silently disagree).
package authz

// HumanOnly is P1-Q's closed set. Exactly four elements, transcribed from
// §5.1.3's table. Membership here means DG6-shaped structural code (this
// file's callers, plus core/internal/graphdb/writeops.go's
// humanOnlyPermissionCodes/mutatePartyGrant one layer down — see this
// file's package doc comment on why that is a SEPARATE copy kept in sync by
// bin/lint-action-layer.sh's AC6 cross-check, not an import) refuses to
// grant or delegate the code to an :AIAgent — it is not merely
// documentation.
var HumanOnly = map[Code]bool{
	PermOrgAdmin:        true, // §5.1.3 #2 — console-only, all grant-catalog columns "—"
	PermOrgManagePeople: true, // §5.1.3 #2
	PermOrgManageRoles:  true, // §5.1.3 #2
	PermActionTypeWrite: true, // §5.1.3 #4 — this task's addition, DG6
}

// HumanOnlyCodes is HumanOnly rendered as a stable, sorted slice — for
// error messages and for bin/lint-action-layer.sh's AC6 cross-check, which
// greps this file's map literal directly rather than executing Go, but a
// test in this package (delegation_test.go) asserts len(HumanOnly) == 4 so
// that a fifth element added without updating §5.1.3 first fails CI here,
// not only in the lint.
func HumanOnlyCodes() []Code {
	out := make([]Code, 0, len(HumanOnly))
	for c := range HumanOnly {
		out = append(out, c)
	}
	return out
}
