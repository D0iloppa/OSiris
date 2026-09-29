// gateFor — 80 §7.3's gateFromTaint composed with an action's own defined
// gateClass. This is the "일반 경로" dispatcher's paraphrase of §7.3's
// pseudocode:
//
//	gateFromTaint(a, level) :=
//	    a.taintClass == 'neutral'    -> none
//	    a.taintClass == 'escalate'   -> {t0:none, t1:approval.team_lead, t2:approval.admin}[level]
//	    a.taintClass == 'forbidden'  -> {t0:none, t1:DENY, t2:DENY}[level]
//
// combined with §6.2's join rule for gateClass (max, over the ordering
// none < approval.team_lead < approval.admin).
//
// *** AC7 (80 §9, "치명") ***: "gateFor의 시그니처에 Party/principal/
// partyId가 나타나지 않는다 — 액션 게이트 계산이 세 번째 인가 엔진이
// 아님의 증거." The signature below takes exactly a LoadedActionType (pure
// action data) and a taint level string — no acting party, principal, or
// party id anywhere in the parameter list or return type. This is the
// literal, mechanically-checkable fact bin/lint-action-layer.sh's AC7
// section greps for: whoever is calling (a :Person? an :AIAgent? which
// one?) is never an input to this function. Authorization — "may THIS
// caller invoke an action requiring THIS gate" — happens elsewhere, in the
// existing authorize(principal, code) / approval pipeline (10 §8.2, 60
// §19.6), unchanged and untouched by this file. gateFor only ever answers
// "what gate does this call require", which is a pure derivation over data
// (AD4).
//
// EXPLICITLY NOT IMPLEMENTED HERE (out of P2 stage ③'s narrow scope, this
// task's brief item "제외"): §5.2's bound/current revision strictest-merge
// (AD5) — that machinery exists to keep a gate from silently disappearing
// out from under an in-flight, already-issued approval request, which
// presupposes the approval round trip this stage does not build. gateFor
// here answers "what gate would a call against this action's CURRENT data
// require", which is exactly as far as "게이트 요청을 만들어야 한다는 판정"
// (this task's stated destination) needs to go.
package dispatch

import "errors"

// gateRank is §6.2's total order over gateClass, loosest first.
var gateRank = map[string]int{
	"none":               0,
	"approval.team_lead": 1,
	"approval.admin":     2,
}

// ErrTaintForbidden is returned by gateFor when taintClass:'forbidden' and
// the call's taint level is not t0 — 80 §7.3: "호출 불가(409
// taint_forbidden)". This stage returns it as a Go error (there is no HTTP
// layer in this narrow verification); a future caller wiring this into
// osiris-api's request handling is expected to map it to that 409.
var ErrTaintForbidden = errors.New("dispatch: taintClass:'forbidden' denies this call at the current taint level (§7.3)")

// maxGate returns the stricter (join) of two gateClass values.
func maxGate(a, b string) string {
	if gateRank[a] >= gateRank[b] {
		return a
	}
	return b
}

// gateFromTaint is §7.3's table, transcribed. taintLevel must be one of
// "t0", "t1", "t2" (§7.2). An unrecognized taintLevel fails closed (treated
// as the most severe, t2) rather than silently defaulting to t0 — an
// unrecognized taint level is itself a caller bug this function refuses to
// paper over as "no taint".
func gateFromTaint(taintClass, taintLevel string) (string, error) {
	switch taintClass {
	case "neutral":
		return "none", nil
	case "escalate":
		switch taintLevel {
		case "t0":
			return "none", nil
		case "t1":
			return "approval.team_lead", nil
		default: // t2, or unrecognized — fail closed to the strictest of the two named levels
			return "approval.admin", nil
		}
	case "forbidden":
		if taintLevel == "t0" {
			return "none", nil
		}
		return "", ErrTaintForbidden
	default:
		return "", errors.New("dispatch: gateFromTaint: unknown taintClass " + taintClass)
	}
}

// gateFor computes the gate class a call against LoadedActionType `a`
// requires, given the invoking turn's taint level. See this file's package
// doc comment for AC7's signature requirement this satisfies.
func gateFor(a LoadedActionType, taintLevel string) (string, error) {
	fromTaint, err := gateFromTaint(a.TaintClass, taintLevel)
	if err != nil {
		return "", err
	}
	return maxGate(a.GateClass, fromTaint), nil
}
