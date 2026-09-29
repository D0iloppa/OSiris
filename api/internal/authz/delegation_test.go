package authz

import "testing"

// AC5 (치명): "actiontype.write는 :AIAgent에게 부여·위임될 수 없다 (DG6)."
// §9's check method is "CheckDelegation 분기 존재 + 통합테스트 2건" — the two
// below exercise the refusal and its boundary (a :Person target with the same
// grant must still succeed, or DG6 would be indistinguishable from "nobody
// may ever grant actiontype.write").

func TestDG6_RefusesActionTypeWriteGrantToAIAgent(t *testing.T) {
	d := CheckDelegation(DelegationRequest{
		Action:          ActionSetAdditiveProfile,
		ActorPartyID:    "person-1",
		ActorPerms:      []Code{PermOrgAdmin, PermActionTypeWrite},
		TargetPartyID:   "agent-1",
		TargetIsAIAgent: true,
		GrantCodes:      []Code{PermActionTypeWrite},
	})
	if d.Allowed {
		t.Fatalf("expected DG6 to refuse granting actiontype.write to an :AIAgent, got Allowed")
	}
	if d.Rule != "DG6" {
		t.Fatalf("expected refusal rule DG6, got %q (reason: %s)", d.Rule, d.Reason)
	}
	if d.Status != 403 {
		t.Fatalf("expected 403 (you may not), got %d", d.Status)
	}
}

func TestDG6_AllowsActionTypeWriteGrantToPerson(t *testing.T) {
	// Same grant, same actor, but the target is a :Person (TargetIsAIAgent
	// defaults false — every real caller today resolves its target this
	// way). DG6 must not overreach into refusing ALL actiontype.write
	// grants; it is scoped to the AIAgent target only.
	d := CheckDelegation(DelegationRequest{
		Action:        ActionSetAdditiveProfile,
		ActorPartyID:  "person-1",
		ActorPerms:    []Code{PermOrgAdmin, PermActionTypeWrite},
		TargetPartyID: "person-2",
		GrantCodes:    []Code{PermActionTypeWrite},
	})
	if !d.Allowed {
		t.Fatalf("expected a :Person-targeted actiontype.write grant to be allowed, got refusal %q: %s", d.Rule, d.Reason)
	}
}

func TestDG6_ActorLackingCodeStillRefusedByDG1First(t *testing.T) {
	// An actor who does not hold actiontype.write cannot grant it to
	// anyone — DG1 catches this before DG6 would even be reached. This
	// documents the ordering rather than testing DG6 itself.
	d := CheckDelegation(DelegationRequest{
		Action:        ActionSetAdditiveProfile,
		ActorPartyID:  "person-1",
		ActorPerms:    []Code{PermOrgAdmin},
		TargetPartyID: "person-2",
		GrantCodes:    []Code{PermActionTypeWrite},
	})
	if d.Allowed || d.Rule != "DG1" {
		t.Fatalf("expected DG1 refusal (actor does not hold the code), got Allowed=%v Rule=%q", d.Allowed, d.Rule)
	}
}

// AC6 (오류): HumanOnly is a closed 4-element enumeration. A fifth element
// added here without also updating 80 §5.1.3 must fail CI here, not only in
// bin/lint-action-layer.sh's cross-module grep.
func TestHumanOnly_IsExactlyFourElements(t *testing.T) {
	if len(HumanOnly) != 4 {
		t.Fatalf("HumanOnly must have exactly 4 elements (80 §5.1.3 P1-Q), got %d: %v", len(HumanOnly), HumanOnlyCodes())
	}
	for _, want := range []Code{PermOrgAdmin, PermOrgManagePeople, PermOrgManageRoles, PermActionTypeWrite} {
		if !HumanOnly[want] {
			t.Fatalf("HumanOnly is missing %q", want)
		}
	}
}

// AC11 (치명): "게이트·오염·효과·가역성의 완화 변경은 org.admin만 가능하고 …
// 발행에 성공해야 커밋된다 (DG8, fail-closed)." §9's check method is
// "CheckDelegation + 통합테스트". The two below cover the refusal and the
// allowed-with-obligation path.

func TestDG8_RefusesRelaxationWithoutOrgAdmin(t *testing.T) {
	d := CheckDelegation(DelegationRequest{
		Action:            ActionEditActionType,
		ActorPartyID:      "person-1",
		ActorPerms:        []Code{PermActionTypeWrite}, // holds the definition-plane code, NOT org.admin
		TargetPartyID:     "action-git.commit",
		RelaxesActionType: true,
	})
	if d.Allowed {
		t.Fatalf("expected DG8 to refuse a relaxation from a non-org.admin actor")
	}
	if d.Rule != "DG8" {
		t.Fatalf("expected refusal rule DG8, got %q (reason: %s)", d.Rule, d.Reason)
	}
}

func TestDG8_AllowsOrgAdminRelaxationAndRequiresNotify(t *testing.T) {
	d := CheckDelegation(DelegationRequest{
		Action:            ActionEditActionType,
		ActorPartyID:      "person-1",
		ActorPerms:        []Code{PermOrgAdmin, PermActionTypeWrite},
		TargetPartyID:     "action-git.commit",
		RelaxesActionType: true,
	})
	if !d.Allowed {
		t.Fatalf("expected org.admin's relaxation to be allowed, got refusal %q: %s", d.Rule, d.Reason)
	}
	if !d.RequiresRelaxationNotify {
		t.Fatalf("expected RequiresRelaxationNotify=true — DG8's fail-closed publish obligation must not be silently dropped")
	}
}

func TestDG8_NonRelaxingEditDoesNotRequireNotify(t *testing.T) {
	// A strengthening edit (or any non-relaxation) by a non-admin holder of
	// actiontype.write must still succeed, and must NOT carry the
	// notification obligation — DG8 is scoped to relaxations only, per
	// "강화는 actiontype.write만으로 가능하다."
	d := CheckDelegation(DelegationRequest{
		Action:        ActionEditActionType,
		ActorPartyID:  "person-1",
		ActorPerms:    []Code{PermActionTypeWrite},
		TargetPartyID: "action-git.commit",
	})
	if !d.Allowed {
		t.Fatalf("expected a non-relaxing edit to be allowed for a plain actiontype.write holder, got refusal %q: %s", d.Rule, d.Reason)
	}
	if d.RequiresRelaxationNotify {
		t.Fatalf("expected RequiresRelaxationNotify=false for a non-relaxing edit")
	}
}

// DG7 — self-interest editing.

func TestDG7_RefusesSelfGrantedEffectiveExpansion(t *testing.T) {
	d := CheckDelegation(DelegationRequest{
		Action:                ActionEditActionType,
		ActorPartyID:          "person-1",
		ActorPerms:            []Code{PermOrgAdmin, PermActionTypeWrite},
		TargetPartyID:         "action-release.publish",
		EffAfterExceedsBefore: true,
	})
	if d.Allowed || d.Rule != "DG7" {
		t.Fatalf("expected DG7 refusal for eff(A) growth, got Allowed=%v Rule=%q", d.Allowed, d.Rule)
	}
}

func TestDG7_RefusesGateRelaxedOnCallerAction(t *testing.T) {
	d := CheckDelegation(DelegationRequest{
		Action:                    ActionEditActionType,
		ActorPartyID:              "person-1",
		ActorPerms:                []Code{PermOrgAdmin, PermActionTypeWrite},
		TargetPartyID:             "action-release.publish",
		GateRelaxedOnCallerAction: true,
	})
	if d.Allowed || d.Rule != "DG7" {
		t.Fatalf("expected DG7 refusal for a self-serving gate relaxation, got Allowed=%v Rule=%q", d.Allowed, d.Rule)
	}
}
