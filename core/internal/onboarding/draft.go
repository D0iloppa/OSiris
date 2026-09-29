// Package onboarding implements the OrgDefinitionDraft intermediate
// representation of docs/design/30-onboarding-provisioning.md §3.2 and the
// early validation of §3.3.
//
// ── THIS PACKAGE WRITES NOTHING ────────────────────────────────────────────
//
// That is not an implementation detail, it is the point. O2 fixed the boundary:
// "대화는 LLM이 구조화된 중간표현(OrgDefinitionDraft)을 만드는 것까지만. 그래프
// 쓰기·불변식 검사·리허설은 전부 결정적 코드". §8.5.1 (F14) then removed
// POST /onboarding/commit from the genesis principal's scope so that the
// boundary is enforced by AUTHORIZATION rather than by convention. This package
// is the other half of that: even if the scope were widened by mistake, there is
// no code path from a draft to a MERGE in here — validation returns problems, and
// the commit lives elsewhere, called by a human's session (30 §3.4.3).
//
// ── WHY VALIDATION IS CODE AND NOT THE MODEL'S JUDGMENT ────────────────────
//
// §3.1's table assigns "카탈로그 코드 존재 여부 확인" to 코드 and, in the row
// below it, "I1~I12 불변식 검사" to 코드 with the reason stated outright: "LLM이
// '이 정도면 될 것 같다' 판단하는 일이 없어야 함". The four checks below are
// therefore exact set-membership tests against a catalog snapshot. None of them
// consults a model, and none of them accepts a free-form string where a code
// belongs (O3: 선택이지 발명이 아니다).
//
// The payoff §3.3 names: by the time the human presses commit, the catalog-level
// errors are already gone, so "대표가 커밋 버튼을 누른 뒤에 처음 오류를 보는
// 상황"이 줄어든다.
package onboarding

import (
	"fmt"
	"sort"
	"strings"

	"osiris-core/internal/graphdb"
)

// ---------------------------------------------------------------------------
// §3.2's shape. Only the fields this build validates are typed; unknown fields
// are tolerated by encoding/json so a consultant that sends a richer draft is
// not rejected for being ahead of this validator.
//
// Op is `upsert` or `close` and NOTHING ELSE — §3.2: "delete는 이 표현에 존재하지
// 않는다 — 스키마가 삭제를 허용하지 않기 때문(P4)". A draft carrying `delete` is
// refused rather than reinterpreted, because silently treating it as `close`
// would let a caller believe deletion happened.
// ---------------------------------------------------------------------------

const (
	OpUpsert = "upsert"
	OpClose  = "close"
)

type CapabilityRequirement struct {
	Code     string `json:"code"`
	MinLevel int    `json:"minLevel"`
}

type TeamDraft struct {
	Op   string `json:"op"`
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type RoleDraft struct {
	Op                    string                  `json:"op"`
	ID                    string                  `json:"id"`
	Title                 string                  `json:"title"`
	TeamID                string                  `json:"teamId"`
	Level                 int                     `json:"level"`
	RequiresCapabilities  []CapabilityRequirement `json:"requiresCapabilities"`
	BasePermissionProfile string                  `json:"basePermissionProfile"`
	NeedsPromptAuthoring  bool                    `json:"needsPromptAuthoring"`
}

type PartyDraft struct {
	Op           string `json:"op"`
	Kind         string `json:"kind"` // "Person" | "AIAgent"
	ID           string `json:"id"`
	Name         string `json:"name"`
	RuntimeClass string `json:"runtimeClass"`
	ModelAlias   string `json:"modelAlias"`
	IsMaster     bool   `json:"isMaster"`
}

type MembershipDraft struct {
	Op      string `json:"op"`
	PartyID string `json:"partyId"`
	TeamID  string `json:"teamId"`
	RoleID  string `json:"roleId"`
	IsHead  bool   `json:"isHead"`
}

type Draft struct {
	DraftID              string            `json:"draftId"`
	BaseOrgRevision      int64             `json:"baseOrgRevision"`
	Teams                []TeamDraft       `json:"teams"`
	Roles                []RoleDraft       `json:"roles"`
	Parties              []PartyDraft      `json:"parties"`
	Memberships          []MembershipDraft `json:"memberships"`
	WarningsAcknowledged []string          `json:"warningsAcknowledged"`
}

// ---------------------------------------------------------------------------
// Problems
// ---------------------------------------------------------------------------

// Problem is one refusal, addressed to the CONSULTANT rather than to the human.
//
// §3.3 describes the loop: a violation is "LLM에게 되돌려 재생성을 요구한다
// (대표에게 보이지 않는 내부 재시도 1회)". For that to work the message must say
// which field and which allowed set, so Path and Allowed are separate fields —
// a prose sentence would make the retry a guess.
type Problem struct {
	Code    string   `json:"code"`
	Path    string   `json:"path"`
	Message string   `json:"message"`
	Allowed []string `json:"allowed,omitempty"`
}

// Result is what the draft endpoint returns. `ok` is exactly "no problems" — it
// is NOT a promise that the commit will succeed, because the commit additionally
// runs I1~I17 and the assignment rehearsal inside its transaction (§5.2). Saying
// otherwise would be the "이 정도면 될 것 같다" this validator exists to avoid.
type Result struct {
	OK       bool      `json:"ok"`
	DraftID  string    `json:"draftId"`
	Problems []Problem `json:"problems"`

	// Checked reports what was actually examined, so a caller can tell "clean
	// draft" from "validator did not look" — an empty problems list on an empty
	// draft is not evidence of anything.
	Checked struct {
		Teams       int `json:"teams"`
		Roles       int `json:"roles"`
		Parties     int `json:"parties"`
		Memberships int `json:"memberships"`
	} `json:"checked"`
}

// Validate runs §3.3's checks against a catalog snapshot.
//
// PURE: no I/O, no graph, no clock. The snapshot is read by the caller so that
// this function is exhaustively testable and so that "what the validator allowed"
// is a value that can be logged alongside its verdict.
func Validate(d Draft, cat graphdb.Catalog) Result {
	var res Result
	res.DraftID = d.DraftID
	res.Checked.Teams = len(d.Teams)
	res.Checked.Roles = len(d.Roles)
	res.Checked.Parties = len(d.Parties)
	res.Checked.Memberships = len(d.Memberships)

	caps := setOf(cat.Capabilities)
	profiles := setOf(cat.PermissionProfiles)
	resProfiles := setOf(cat.ResourceProfiles)

	// --- op vocabulary (§3.2, P4) -----------------------------------------
	for i, t := range d.Teams {
		res.Problems = appendOpProblem(res.Problems, fmt.Sprintf("teams[%d].op", i), t.Op)
	}
	for i, r := range d.Roles {
		res.Problems = appendOpProblem(res.Problems, fmt.Sprintf("roles[%d].op", i), r.Op)
	}
	for i, p := range d.Parties {
		res.Problems = appendOpProblem(res.Problems, fmt.Sprintf("parties[%d].op", i), p.Op)
	}
	for i, m := range d.Memberships {
		res.Problems = appendOpProblem(res.Problems, fmt.Sprintf("memberships[%d].op", i), m.Op)
	}

	for i, r := range d.Roles {
		if r.Op == OpClose {
			// A close carries only an id; validating the catalog fields of a role
			// being retired would refuse drafts that are removing something the
			// catalog no longer offers.
			continue
		}

		// --- §3.3 ① requiresCapabilities[].code ∈ :Capability (I4 조기 검사) ---
		for j, rc := range r.RequiresCapabilities {
			path := fmt.Sprintf("roles[%d].requiresCapabilities[%d].code", i, j)
			if strings.TrimSpace(rc.Code) == "" {
				res.Problems = append(res.Problems, Problem{
					Code: "capability_code_missing", Path: path,
					Message: "a capability requirement needs a code from the catalog",
					Allowed: cat.Capabilities,
				})
				continue
			}
			if !caps[rc.Code] {
				res.Problems = append(res.Problems, Problem{
					Code: "capability_not_in_catalog", Path: path,
					Message: fmt.Sprintf("%q is not in the :Capability catalog; the catalog is system-owned and a draft may only select from it", rc.Code),
					Allowed: cat.Capabilities,
				})
			}
		}

		// --- §3.3 ② basePermissionProfile ∈ :PermissionProfile ---------------
		if p := strings.TrimSpace(r.BasePermissionProfile); p != "" && !profiles[p] {
			res.Problems = append(res.Problems, Problem{
				Code: "permission_profile_not_found", Path: fmt.Sprintf("roles[%d].basePermissionProfile", i),
				Message: fmt.Sprintf("%q is not a seeded or previously cloned :PermissionProfile", p),
				Allowed: cat.PermissionProfiles,
			})
		}
	}

	for i, p := range d.Parties {
		if p.Op == OpClose {
			continue
		}
		// --- §3.3 ③ runtimeClass ∈ :RoleResourceProfile ----------------------
		if rc := strings.TrimSpace(p.RuntimeClass); rc != "" && !resProfiles[rc] {
			res.Problems = append(res.Problems, Problem{
				Code: "resource_profile_not_found", Path: fmt.Sprintf("parties[%d].runtimeClass", i),
				Message: fmt.Sprintf("%q is not a :RoleResourceProfile", rc),
				Allowed: cat.ResourceProfiles,
			})
		}

		// --- §3.3 ④ modelAlias ∈ I10's three ---------------------------------
		//
		// Only for AIAgent: a :Person has no model alias, and demanding one would
		// reject every draft that hires a human.
		if p.Kind == "AIAgent" {
			if !graphdb.IsAllowedModelAlias(p.ModelAlias) {
				res.Problems = append(res.Problems, Problem{
					Code: "model_alias_not_allowed", Path: fmt.Sprintf("parties[%d].modelAlias", i),
					Message: fmt.Sprintf("%q is not one of the three aliases I10 fixes", p.ModelAlias),
					Allowed: cat.ModelAliases,
				})
			}
		} else if p.Kind != "Person" && p.Kind != "" {
			res.Problems = append(res.Problems, Problem{
				Code: "party_kind_unknown", Path: fmt.Sprintf("parties[%d].kind", i),
				Message: fmt.Sprintf("%q is not a party kind this draft may create", p.Kind),
				Allowed: []string{"Person", "AIAgent"},
			})
		}
	}

	// --- internal reference integrity ------------------------------------
	//
	// NOT in §3.3's list, and included anyway with a reason: §3.3's purpose is to
	// move failures earlier, and a membership pointing at a role the same draft
	// never defines fails at commit for a reason the consultant could have been
	// told at conversation time. This checks only WITHIN the draft — a reference
	// to an existing team is not resolvable here (this function reads no graph)
	// and is therefore left to the commit, which is where existence is knowable.
	declaredTeams := map[string]bool{}
	for _, t := range d.Teams {
		if t.ID != "" {
			declaredTeams[t.ID] = true
		}
	}
	declaredRoles := map[string]bool{}
	for _, r := range d.Roles {
		if r.ID != "" {
			declaredRoles[r.ID] = true
		}
	}
	declaredParties := map[string]bool{}
	for _, p := range d.Parties {
		if p.ID != "" {
			declaredParties[p.ID] = true
		}
	}
	for i, r := range d.Roles {
		if r.Op == OpClose || r.TeamID == "" {
			continue
		}
		if !declaredTeams[r.TeamID] {
			res.Problems = append(res.Problems, Problem{
				Code: "unresolved_reference_in_draft", Path: fmt.Sprintf("roles[%d].teamId", i),
				Message: fmt.Sprintf("%q is not declared in this draft; if it already exists in the organization this is fine and the commit will resolve it", r.TeamID),
			})
		}
	}
	for i, m := range d.Memberships {
		if m.Op == OpClose {
			continue
		}
		if m.RoleID != "" && !declaredRoles[m.RoleID] {
			res.Problems = append(res.Problems, Problem{
				Code: "unresolved_reference_in_draft", Path: fmt.Sprintf("memberships[%d].roleId", i),
				Message: fmt.Sprintf("%q is not declared in this draft; if it already exists this is fine and the commit will resolve it", m.RoleID),
			})
		}
		if m.PartyID != "" && !declaredParties[m.PartyID] {
			res.Problems = append(res.Problems, Problem{
				Code: "unresolved_reference_in_draft", Path: fmt.Sprintf("memberships[%d].partyId", i),
				Message: fmt.Sprintf("%q is not declared in this draft; if it already exists this is fine and the commit will resolve it", m.PartyID),
			})
		}
	}

	sort.SliceStable(res.Problems, func(a, b int) bool { return res.Problems[a].Path < res.Problems[b].Path })
	res.OK = len(res.Problems) == 0
	return res
}

func appendOpProblem(list []Problem, path, op string) []Problem {
	switch op {
	case OpUpsert, OpClose:
		return list
	case "":
		return append(list, Problem{
			Code: "op_missing", Path: path,
			Message: "op is required", Allowed: []string{OpUpsert, OpClose},
		})
	default:
		return append(list, Problem{
			Code: "op_not_allowed", Path: path,
			Message: fmt.Sprintf("%q is not an allowed op; the schema does not permit deletion (P4) — close instead", op),
			Allowed: []string{OpUpsert, OpClose},
		})
	}
}

func setOf(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}
