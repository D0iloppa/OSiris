package authz

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// ===========================================================================
// CheckDelegation — the ONE delegation decision, 60-identity-authn.md §21.1.
//
// H31: "아래 다섯 규칙은 인가 판정 단일 함수(authz.CheckDelegation)에 있고,
// 역할 변경·계정 상태 변경·자격증명 조작(등록 티켓 발급/재발급)의 모든 경로가
// 그 함수를 지난다(HI16이 검사)."
//
// The five rules, verbatim from §21.1, with eff(X) = effective(party X):
//
//	DG1   grants(R) ⊆ eff(A)          you cannot grant what you do not hold
//	DG1′  org.admin ∈ grants(R) ⟹ org.admin ∈ eff(A)
//	DG2   eff(T) ⊆ eff(A)             you cannot touch someone stronger
//	DG3   A ≠ T                       you cannot act on yourself
//	DG4   ≥1 admin survives           you cannot lock the organization out
//	DG5   every other admin is told   you cannot do it quietly
//
// [2026-08-13] EXTENDED WITH DG6~DG8 — docs/design/80-action-layer.md §5.1.2
// (AD4: "새 인가 판정 함수를 만들지 않는다. DG6~DG8은 authz.CheckDelegation에
// 추가되는 규칙이다"). Still the same single function, still zero I/O:
//
//	DG6   actiontype.write ∉ grants(R) if T is an :AIAgent
//	                                   the definition plane is Person-only
//	DG7   eff_after(A) ⊆ eff_before(A) ∧ gate_after ≥ gate_before on A's own
//	      called actions                self-interest editing is refused
//	DG8   relaxes(Δ) ⟹ org.admin ∈ eff(A)   quiet, unattributable relaxation
//	                                        of a gate/taint/effect/reversibility
//	                                        axis is refused to anyone but the
//	                                        top of delegation
//
// DG6 is HumanOnly's (80 §5.1.3, P1-Q) fourth and newest element — see
// humanonly.go. DG7/DG8 apply only to Action = ActionEditActionType, the one
// action this file adds for the definition plane; see that constant's doc
// comment for why editing an :ActionType belongs in THIS file at all rather
// than in a second engine.
//
// ── WHY THIS FILE IS THE WHOLE OF IT ──────────────────────────────────────
//
// A delegation rule that lives in a handler is a rule that the NEXT handler
// forgets. HI16 greps for exactly that: every route that changes a role, an
// account state, a HAS_PROFILE/DENIES edge, or issues an enrollment ticket
// must call this function, and no code outside this file may compare against
// the org.admin constant. Both halves matter — the second is what stops
// someone from "just adding a quick admin check" next to a new endpoint and
// getting DG2 subtly wrong there.
//
// ── SCOPE: WHY TICKETS AND DENY EDGES ARE IN HERE ─────────────────────────
//
// Two of the six actions below look like they do not belong in a file about
// ROLES, and both are here because leaving them out reopens everything else:
//
//   - ActionIssueEnrollmentTicket. §5.3's 판정 ⑤ found this while walking the
//     credential-classification procedure: a ticket lets its holder set a new
//     password on the bound account, so issuing one to an administrator IS
//     taking over that administrator's account. Ticket issuance is therefore
//     under DG2 at exactly the same strength as demotion.
//   - ActionSetDeny / ActionSetAdditiveProfile. §21.4-2: "deny가 항상 이긴다",
//     so DENIES org.admin on an administrator neutralizes them without
//     touching a role at all — DG1~DG4 would all be bypassed through that one
//     edge. C10 lists HAS_PROFILE/DENIES manipulation for this reason.
//
// ── WHAT IS DELIBERATELY *NOT* CHECKED ────────────────────────────────────
//
// Strict containment. DG2 is ⊆, not ⊊, and §21.1 says why in as many words:
// with ⊊ an administrator could never demote another administrator, and since
// DG3 also forbids self-demotion, a COMPROMISED ADMINISTRATOR COULD NEVER BE
// REMOVED. The design accepts peer-on-peer action and watches it with DG5
// rather than closing a hole by creating a worse one. Do not "fix" this.
// ===========================================================================

// Action names the operation being authorized. It exists so that DG3's scope
// ("역할·계정 상태 변경에 한함") is expressed as data rather than as a boolean
// the caller could pass wrongly.
type Action string

const (
	ActionAssignOrgRole         Action = "assign-org-role"
	ActionRevokeOrgRole         Action = "revoke-org-role"
	ActionDeactivatePerson      Action = "deactivate-person"
	ActionSetAdditiveProfile    Action = "set-additive-profile"
	ActionSetDeny               Action = "set-deny"
	ActionIssueEnrollmentTicket Action = "issue-enrollment-ticket"

	// ActionEditActionType is 80 §5.1's definition plane: creating or
	// updating a :ActionType row. It is deliberately a SEVENTH action in
	// THIS enum, not a new function or a new file — AD4 forbids a second
	// delegation engine, and the alternative (a bespoke
	// "authz.CheckActionTypeEdit") would be exactly that engine wearing a
	// different name. DG7/DG8 below are keyed on this one Action value; DG6
	// is keyed on the code alone (actiontype.write can also arrive via
	// ActionSetAdditiveProfile, so it cannot be gated on Action).
	ActionEditActionType Action = "edit-action-type"
)

// selfActionForbidden is DG3's applicability set.
//
// Ticket issuance is the one action a person may perform on themselves: it
// grants no permission, changes no account state, and re-issuing your own
// enrollment ticket when you already control the account escalates nothing.
// Everything else changes what the actor is allowed to do and is refused —
// including the profile/deny actions, because attaching an additive profile to
// yourself is self-promotion by another name.
var selfActionForbidden = map[Action]bool{
	ActionAssignOrgRole:      true,
	ActionRevokeOrgRole:      true,
	ActionDeactivatePerson:   true,
	ActionSetAdditiveProfile: true,
	ActionSetDeny:            true,
}

// DelegationRequest is everything the five rules need, already resolved.
//
// It takes CODE SETS, not role ids, not profile ids, not titles (H30/HI17).
// Resolution happens in the caller, against the graph, at request time; this
// function performs no I/O, which is what makes it exhaustively testable and
// what keeps HI2's separation (authorization vocabulary is pure) intact.
type DelegationRequest struct {
	Action Action

	// ActorPartyID is the :Person.id of A, derived from the authenticated
	// session and from nowhere else (H19/H28). A request body that carried an
	// actor id would be ignored — there is no field for it.
	ActorPartyID string
	ActorPerms   []Code

	// TargetPartyID / TargetPerms describe T at the time of the request.
	TargetPartyID string
	TargetPerms   []Code

	// GrantCodes is grants(R) — the codes this action would ADD to T.
	//
	// Empty for actions that add nothing (revoke, deactivate, ticket
	// issuance, attaching a DENIES edge). Note the inversion for
	// ActionSetDeny with Attach=false: REMOVING a deny gives the code back, so
	// the caller passes that code here and DG1 applies to it. Getting that
	// direction wrong would make "un-deny org.admin" a free promotion.
	GrantCodes []Code

	// RemovesTargetAdmin says whether this action would strip org.admin from
	// T. It is the caller's computation because only the caller knows the
	// post-state (which role T is moving to, whether the deny being attached
	// is org.admin, and so on). It is used ONLY by DG4 and only to decide
	// whether the "someone must still be able to log in" precondition is in
	// play — it can never make a refusal into an approval.
	RemovesTargetAdmin bool

	// CredentialedAdminIDs is the set of ACTIVE people who both effectively
	// hold org.admin AND possess a usable credential — the intersection of a
	// graph fact and a JetStream KV fact.
	//
	// THIS IS DG4's SECOND LAYER AND IT CANNOT BE A GRAPH INVARIANT. §21.1:
	// "'자격증명을 보유하는가'는 AUTHN_CRED(JetStream KV)의 사실이고, 그래프는
	// 그 저장소의 존재를 알지 못한다(H1의 단방향 참조)." The graph's own I14
	// ("at least one active admin") runs inside osiris-core's write
	// transaction and catches everything this layer cannot see — including a
	// compromised osiris-api. Two layers, two different blind spots, neither
	// sufficient alone.
	CredentialedAdminIDs []string

	// TargetIsAIAgent is DG6's whole premise: true when the :Party a grant
	// would attach to is a graph :AIAgent rather than :Person. It defaults
	// false, which is correct for every caller that exists today — every
	// route that reaches CheckDelegation resolves its target through
	// people.GetPerson (Person-only), so no caller can yet set this true.
	// The field exists so that the day a caller CAN target something other
	// than a :Person (e.g. a future agent-grant route), DG6 is already
	// enforced rather than bolted on after the fact — see
	// core/internal/graphdb writeops.go's mutatePartyGrant for the
	// structural half of this same guarantee one layer down.
	TargetIsAIAgent bool

	// --- DG7/DG8 inputs — only meaningful when Action == ActionEditActionType ---

	// EffAfterExceedsBefore is DG7's first clause: applying Δ (the proposed
	// :ActionType create/update) would grow eff(A) itself — e.g. Δ adds a
	// REQUIRES_PERMISSION edge that hands A's own effective permission set a
	// code it did not already hold. The caller computes this by diffing
	// eff(A) before and after Δ against the graph; CheckDelegation performs
	// no I/O and cannot compute it itself (same reasoning as
	// RemovesTargetAdmin above).
	EffAfterExceedsBefore bool

	// GateRelaxedOnCallerAction is DG7's second clause: Δ lowers the gate of
	// an :ActionType that A itself is a caller/subject of. "자기 이익 편집
	// 금지" — the action-plane analogue of DG3's self-target rule.
	GateRelaxedOnCallerAction bool

	// RelaxesActionType is DG8's trigger: Δ moves gateClass, taintClass,
	// effectClass, or reversibility to a LESS strict value, or revives an
	// action from status:'withdrawn'. The caller computes this by comparing
	// the :ActionType's before/after axis values — see
	// core/internal/graphdb/actiontype.go's actionTypeRelaxes for the graph
	// layer's OWN, independent copy of this same comparison (which gates
	// the :Notification/fail-closed commit, not the delegation decision;
	// the two are deliberately not the same function per HI16's "single
	// judgment, layered defense" pattern already used for I14~I17).
	RelaxesActionType bool
}

// Decision is the outcome. Refusals carry the rule that produced them so that
// an operator reading a 403 learns which of the five stopped them — the codes
// are stable identifiers, not prose, and the console maps them to messages.
type Decision struct {
	Allowed bool

	// Rule is "DG1" | "DG1'" | "DG2" | "DG3" | "DG4" when Allowed is false.
	Rule   string
	Reason string

	// Status is the HTTP status the refusal maps to: 403 for the four
	// authorization rules and 409 for DG4, which is not "you may not" but
	// "the organization would be left unusable" — a state conflict, and the
	// status HI18's integration test asserts on.
	Status int

	// NotifyAdmins is DG5: true when T effectively holds org.admin, meaning
	// the caller MUST publish security.rbac.* and MUST NOT proceed if that
	// publish fails (fail-closed, §21.1 DG5 / §9.4's rule reused).
	//
	// It is set on ALLOWED decisions — a refusal changes nothing and needs no
	// notification. The graph-side :Notification fan-out happens inside
	// osiris-core's write transaction, so notification and change commit or
	// roll back together.
	NotifyAdmins bool

	// RequiresRelaxationNotify is DG8's visibility obligation, the action-
	// plane analogue of NotifyAdmins: true when the allowed change RELAXES a
	// gate/taint/effect/reversibility axis, meaning the caller MUST publish
	// security.actiontype.relaxed AND fan out :Notification{critical} to
	// every org.admin, and MUST NOT commit the change if either publish
	// fails (fail-closed, §5.1.2 DG8).
	//
	// ★ THE DIRECTION IS THE OPPOSITE OF AC3's. AC3's boot-time inert-degrade
	// write moves an :ActionType SAFER (active → inert) and is explicitly
	// allowed to proceed even if its own security.actiontype.unknown publish
	// fails — a safety-direction write must not be blocked by a notification
	// outage. DG8's write moves an :ActionType LESS safe (a relaxation), so
	// the fail-closed rule runs the other way: here, publish failure MUST
	// block the write. Same shape as DG5's NotifyAdmins, deliberately: 60
	// §21.1 already established "quiet is the attack" for RBAC, and this is
	// that argument applied to the action-type table instead of the role
	// table.
	RequiresRelaxationNotify bool
}

func allow(notify bool) Decision { return Decision{Allowed: true, NotifyAdmins: notify} }

func refuse(rule, reason string, status int) Decision {
	return Decision{Allowed: false, Rule: rule, Reason: reason, Status: status}
}

// CheckDelegation evaluates DG1, DG1′, DG2, DG3, DG4, DG6, DG7 and DG8 in
// that order and reports whether DG5/DG8's notification obligations apply.
//
// ORDER IS DELIBERATE, cheapest and most absolute first: DG3 needs no sets at
// all, DG1′ and DG6 are single membership tests, DG1 and DG2 are subset
// tests, DG4 needs the credential intersection. DG7/DG8 run last because
// they are the only two that inspect Action == ActionEditActionType rather
// than the RBAC-grant shape every earlier rule shares. A caller that sees
// "DG3" learns nothing about the permission sets, which is the right amount
// of information to leak from a refusal.
func CheckDelegation(req DelegationRequest) Decision {
	actor := codeSet(req.ActorPerms)

	// --- DG3 — self-target ------------------------------------------------
	//
	// §21.2's second question is answered "no, two ways over": DG3 refuses
	// self-promotion outright and DG1 would refuse it again on the codes. Both
	// are kept because they cover different failures — DG3 also stops
	// self-demotion-into-lockout and keeps the audit trail's actor and subject
	// distinct ("who made this person a manager" must never be that person).
	if selfActionForbidden[req.Action] && req.ActorPartyID != "" && req.ActorPartyID == req.TargetPartyID {
		return refuse("DG3",
			"an actor may not change their own org role, account state, or permission grants",
			http.StatusForbidden)
	}

	// --- DG1′ — the top of delegation ------------------------------------
	//
	// Implied by DG1, and written separately anyway. §21.1: "가장 중요한 한
	// 줄이므로 별도로 명문화한다(코드에서도 별도 검사로 두어 DG1의 구현 버그가
	// 이 경로를 열지 못하게)". If the subset test below ever acquires a bug,
	// this line still stands between a manager and an administrator.
	for _, g := range req.GrantCodes {
		if g == PermOrgAdmin && !actor[PermOrgAdmin] {
			return refuse("DG1'",
				"only a holder of the top-level administrative permission may grant it",
				http.StatusForbidden)
		}
	}

	// --- DG6 — the definition plane is Person-only (80 §5.1.2, AC5, 치명) --
	//
	// P1-Q's fourth HumanOnly member (humanonly.go): actiontype.write may not
	// be granted or delegated to an :AIAgent. Checked on the code alone, not
	// on Action, because a grant of actiontype.write can arrive through
	// ActionSetAdditiveProfile (a profile that happens to include it) just
	// as easily as through a purpose-built action-type-grant route — DG6
	// must catch both, the same way DG1′ catches org.admin through every
	// route that can carry a GrantCodes entry.
	for _, g := range req.GrantCodes {
		if g == PermActionTypeWrite && req.TargetIsAIAgent {
			return refuse("DG6",
				"actiontype.write defines what the organization can do and may not be granted or delegated to an :AIAgent (80 §5.1.2 DG6, P1-Q)",
				http.StatusForbidden)
		}
	}

	// --- DG1 — grant ceiling ----------------------------------------------
	if missing := notSubset(req.GrantCodes, actor); len(missing) > 0 {
		return refuse("DG1",
			fmt.Sprintf("the actor does not hold %s and therefore cannot grant it", strings.Join(missing, ", ")),
			http.StatusForbidden)
	}

	// --- DG2 — target ceiling ---------------------------------------------
	//
	// §21.1: "DG2가 이 분석에서 가장 놓치기 쉬운 규칙이다." DG1 alone gives
	// "a manager cannot CREATE an admin" but leaves open "a manager
	// deactivates every admin and becomes the only real power". This is the
	// line that closes it, and it is why the action list above includes
	// deactivation and ticket issuance and not just role assignment.
	if missing := notSubset(req.TargetPerms, actor); len(missing) > 0 {
		return refuse("DG2",
			fmt.Sprintf("the target holds %s which the actor does not; a stronger principal may not be modified", strings.Join(missing, ", ")),
			http.StatusForbidden)
	}

	// --- DG4 — at least one administrator can still log in ----------------
	//
	// Only in play when the action would actually strip org.admin from T. The
	// graph's I14 asks the broader question ("does ANY admin remain"); this
	// layer asks the one the graph structurally cannot ("can any of them still
	// prove who they are"). §21.1: "I14만이면 '관리자는 있는데 아무도 로그인
	// 못 하는' 상태를 허용한다."
	if req.RemovesTargetAdmin {
		remaining := 0
		for _, id := range req.CredentialedAdminIDs {
			if id != req.TargetPartyID {
				remaining++
			}
		}
		if remaining < 1 {
			return refuse("DG4",
				"this change would leave no administrator holding a usable login credential",
				http.StatusConflict)
		}
	}

	// --- DG7/DG8 — definition-plane edits (80 §5.1.2) ----------------------
	//
	// Only in play for Action == ActionEditActionType — every rule above
	// this point is about RBAC grants (Party ⟶ code), and an :ActionType
	// edit carries no GrantCodes/TargetPerms of that shape at all (both are
	// empty for this Action, so DG1/DG2/DG4 above all pass trivially,
	// exactly like DG4 passing trivially when RemovesTargetAdmin is false).
	if req.Action == ActionEditActionType {
		// DG7 — self-interest editing (the action-plane DG3).
		if req.EffAfterExceedsBefore {
			return refuse("DG7",
				"an actor may not use actiontype.write to grow their own effective permission set (80 §5.1.2 DG7)",
				http.StatusForbidden)
		}
		if req.GateRelaxedOnCallerAction {
			return refuse("DG7",
				"an actor may not relax the gate of an :ActionType they themselves call (80 §5.1.2 DG7, self-interest editing)",
				http.StatusForbidden)
		}

		// DG8 — relaxation is org.admin-only, with an unsuppressible
		// notification obligation. §5.1.2: "완화는 최상위만 + 억제 불가능한
		// 가시성." Refusal here carries 403, same as DG1′ — this is not a
		// resource conflict (DG4's 409 shape), it is "you may not."
		if req.RelaxesActionType && !actor[PermOrgAdmin] {
			return refuse("DG8",
				"only org.admin may relax an :ActionType's gate/taint/effect/reversibility or revive it from withdrawn (80 §5.1.2 DG8)",
				http.StatusForbidden)
		}
		return Decision{Allowed: true, RequiresRelaxationNotify: req.RelaxesActionType}
	}

	// --- DG5 — visibility obligation --------------------------------------
	return allow(codeSet(req.TargetPerms)[PermOrgAdmin])
}

func codeSet(codes []Code) map[Code]bool {
	m := make(map[Code]bool, len(codes))
	for _, c := range codes {
		m[c] = true
	}
	return m
}

// notSubset returns the members of want that are absent from have, sorted so
// that the refusal message is deterministic (two identical requests must
// produce byte-identical responses — a message that reorders is a message
// nobody can test against).
func notSubset(want []Code, have map[Code]bool) []string {
	var missing []string
	seen := map[Code]bool{}
	for _, c := range want {
		if !have[c] && !seen[c] {
			seen[c] = true
			missing = append(missing, string(c))
		}
	}
	sort.Strings(missing)
	return missing
}
