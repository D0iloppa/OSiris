package graphdb

// AC11 (치명): "게이트·오염·효과·가역성의 완화 변경은 org.admin만 가능하고
// … 발행에 성공해야 커밋된다 (DG8, fail-closed)." §9's check method is
// "CheckDelegation + 통합테스트". api/internal/authz/delegation_test.go
// covers the CheckDelegation half; this file covers actionTypeRelaxes, the
// graph layer's independent relaxation detector that gates UpdateActionType's
// fail-closed notification write (see that function's doc comment for why
// it is a second, deliberately separate copy of the same comparison).
//
// PURE UNIT TEST, NO NEO4J — same convention as actiontype_gitcommit_test.go
// (this package has no testcontainers/Neo4j fixture; UpdateActionType's
// end-to-end transactional behavior, including the notify-or-abort
// mechanism itself, is out of this test's reach for the same reason).

import "testing"

func baseAxes() actionTypeAxes {
	return actionTypeAxes{
		GateClass:     "approval.admin",
		TaintClass:    "escalate",
		EffectClass:   "external_effect",
		Reversibility: "irreversible",
		Status:        "active",
	}
}

func baseInput() ActionTypeInput {
	b := baseAxes()
	return ActionTypeInput{
		GateClass:     b.GateClass,
		TaintClass:    b.TaintClass,
		EffectClass:   b.EffectClass,
		Reversibility: b.Reversibility,
		Status:        b.Status,
	}
}

func TestActionTypeRelaxes_NoChange(t *testing.T) {
	if actionTypeRelaxes(baseAxes(), baseInput()) {
		t.Fatalf("identical before/after must not be a relaxation")
	}
}

func TestActionTypeRelaxes_GateLowered(t *testing.T) {
	after := baseInput()
	after.GateClass = "approval.team_lead"
	if !actionTypeRelaxes(baseAxes(), after) {
		t.Fatalf("lowering gateClass (approval.admin -> approval.team_lead) must be a relaxation")
	}
}

func TestActionTypeRelaxes_GateRaisedIsNotRelaxation(t *testing.T) {
	before := baseAxes()
	before.GateClass = "none"
	after := baseInput()
	after.GateClass = "approval.admin"
	if actionTypeRelaxes(before, after) {
		t.Fatalf("raising gateClass must not be flagged as a relaxation — 강화는 actiontype.write만으로 가능해야 한다")
	}
}

func TestActionTypeRelaxes_TaintLowered(t *testing.T) {
	after := baseInput()
	after.TaintClass = "neutral"
	if !actionTypeRelaxes(baseAxes(), after) {
		t.Fatalf("lowering taintClass (escalate -> neutral) must be a relaxation")
	}
}

func TestActionTypeRelaxes_EffectClassLowered(t *testing.T) {
	after := baseInput()
	after.EffectClass = "internal"
	if !actionTypeRelaxes(baseAxes(), after) {
		t.Fatalf("moving effectClass from external_effect to internal must be a relaxation")
	}
}

func TestActionTypeRelaxes_ReversibilityLowered(t *testing.T) {
	after := baseInput()
	after.Reversibility = "reversible"
	if !actionTypeRelaxes(baseAxes(), after) {
		t.Fatalf("moving reversibility from irreversible to reversible must be a relaxation")
	}
}

func TestActionTypeRelaxes_RevivedFromWithdrawn(t *testing.T) {
	before := baseAxes()
	before.Status = "withdrawn"
	after := baseInput()
	after.Status = "active"
	if !actionTypeRelaxes(before, after) {
		t.Fatalf("reviving an action from status:'withdrawn' must be a relaxation")
	}
}

func TestActionTypeRelaxes_WithdrawingIsNotRelaxation(t *testing.T) {
	before := baseAxes()
	after := baseInput()
	after.Status = "withdrawn"
	if actionTypeRelaxes(before, after) {
		t.Fatalf("withdrawing an action must not be flagged as a relaxation")
	}
}
