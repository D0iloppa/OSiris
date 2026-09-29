package onboarding

import (
	"testing"

	"osiris-core/internal/graphdb"
)

// The catalog a real seeded system reports (core/internal/graphdb/seed.go),
// trimmed to what these tests exercise.
func testCatalog() graphdb.Catalog {
	return graphdb.Catalog{
		CatalogVersion:     "2026.08.1",
		Capabilities:       []string{"cap.mkt.media_buying", "cap.mkt.reporting"},
		Permissions:        []string{"org.admin", "org.manage.people"},
		PermissionProfiles: []string{"pp-org-admin", "builder", "readonly"},
		ResourceProfiles:   []string{"standard", "heavy"},
		ModelAliases:       graphdb.AllowedModelAliases(),
	}
}

func problemCodes(r Result) map[string]int {
	out := map[string]int{}
	for _, p := range r.Problems {
		out[p.Code]++
	}
	return out
}

// A draft that selects only from the catalog passes — the baseline, without
// which every other assertion here could be passing for the wrong reason.
func TestValidateAcceptsACatalogRespectingDraft(t *testing.T) {
	d := Draft{
		DraftID: "draft-1",
		Teams:   []TeamDraft{{Op: OpUpsert, ID: "team-mkt", Name: "마케팅팀", Slug: "marketing"}},
		Roles: []RoleDraft{{
			Op: OpUpsert, ID: "role-perf", Title: "퍼포먼스 마케터", TeamID: "team-mkt", Level: 2,
			RequiresCapabilities:  []CapabilityRequirement{{Code: "cap.mkt.media_buying", MinLevel: 3}},
			BasePermissionProfile: "builder",
		}},
		Parties: []PartyDraft{{
			Op: OpUpsert, Kind: "AIAgent", ID: "agent-mkt-2", Name: "마케팅-AI-2",
			RuntimeClass: "standard", ModelAlias: "osiris/worker",
		}},
		Memberships: []MembershipDraft{{Op: OpUpsert, PartyID: "agent-mkt-2", TeamID: "team-mkt", RoleID: "role-perf"}},
	}
	res := Validate(d, testCatalog())
	if !res.OK {
		t.Fatalf("expected a clean draft, got %+v", res.Problems)
	}
	if res.Checked.Roles != 1 || res.Checked.Parties != 1 {
		t.Fatalf("Checked must report what was examined, got %+v", res.Checked)
	}
}

// §3.3 ①, and O3's whole point: the consultant SELECTS from the catalog, it does
// not INVENT. An invented capability code is the most likely LLM failure and must
// be caught at conversation time, not at commit.
func TestValidateRefusesInventedCapability(t *testing.T) {
	d := Draft{Roles: []RoleDraft{{
		Op: OpUpsert, ID: "r1", BasePermissionProfile: "builder",
		RequiresCapabilities: []CapabilityRequirement{{Code: "cap.marketing.vibes", MinLevel: 5}},
	}}}
	res := Validate(d, testCatalog())
	if res.OK {
		t.Fatal("an invented capability code must be refused")
	}
	if problemCodes(res)["capability_not_in_catalog"] != 1 {
		t.Fatalf("expected capability_not_in_catalog, got %+v", res.Problems)
	}
	// The refusal must carry the allowed set: §3.3's retry loop is a guess
	// without it.
	for _, p := range res.Problems {
		if p.Code == "capability_not_in_catalog" && len(p.Allowed) == 0 {
			t.Fatal("the refusal must tell the consultant what IS allowed")
		}
	}
}

// §3.3 ②
func TestValidateRefusesUnknownPermissionProfile(t *testing.T) {
	d := Draft{Roles: []RoleDraft{{Op: OpUpsert, ID: "r1", BasePermissionProfile: "pp-invented"}}}
	res := Validate(d, testCatalog())
	if problemCodes(res)["permission_profile_not_found"] != 1 {
		t.Fatalf("expected permission_profile_not_found, got %+v", res.Problems)
	}
}

// §3.3 ③
func TestValidateRefusesUnknownRuntimeClass(t *testing.T) {
	d := Draft{Parties: []PartyDraft{{
		Op: OpUpsert, Kind: "AIAgent", ID: "a1", RuntimeClass: "gigantic", ModelAlias: "osiris/worker",
	}}}
	res := Validate(d, testCatalog())
	if problemCodes(res)["resource_profile_not_found"] != 1 {
		t.Fatalf("expected resource_profile_not_found, got %+v", res.Problems)
	}
}

// §3.3 ④ / I10. The validator must apply the SAME predicate the write path
// enforces, which is why it calls graphdb.IsAllowedModelAlias rather than
// carrying its own list.
func TestValidateRefusesModelAliasOutsideI10(t *testing.T) {
	d := Draft{Parties: []PartyDraft{{
		Op: OpUpsert, Kind: "AIAgent", ID: "a1", RuntimeClass: "standard", ModelAlias: "anthropic/claude-opus-5",
	}}}
	res := Validate(d, testCatalog())
	if problemCodes(res)["model_alias_not_allowed"] != 1 {
		t.Fatalf("expected model_alias_not_allowed, got %+v", res.Problems)
	}
}

// A :Person has no model alias. Demanding one would reject every draft that
// hires a human — the kind of over-strictness that makes a validator get
// bypassed.
func TestValidateDoesNotDemandModelAliasFromPeople(t *testing.T) {
	d := Draft{Parties: []PartyDraft{{Op: OpUpsert, Kind: "Person", ID: "emp-1", Name: "사람"}}}
	res := Validate(d, testCatalog())
	if !res.OK {
		t.Fatalf("a Person needs no model alias, got %+v", res.Problems)
	}
}

// §3.2 / P4: the schema does not permit deletion, so the draft vocabulary does
// not contain `delete`. Refused rather than silently read as `close`.
func TestValidateRefusesDeleteOp(t *testing.T) {
	d := Draft{Teams: []TeamDraft{{Op: "delete", ID: "team-x"}}}
	res := Validate(d, testCatalog())
	if problemCodes(res)["op_not_allowed"] != 1 {
		t.Fatalf("expected op_not_allowed for a delete op, got %+v", res.Problems)
	}
}

func TestValidateRequiresAnOp(t *testing.T) {
	d := Draft{Teams: []TeamDraft{{ID: "team-x"}}}
	res := Validate(d, testCatalog())
	if problemCodes(res)["op_missing"] != 1 {
		t.Fatalf("expected op_missing, got %+v", res.Problems)
	}
}

// A close carries only an id. Validating catalog fields on the way out would
// refuse drafts that retire a role whose capability the catalog no longer
// offers — i.e. exactly the cleanup the organization is trying to do.
func TestValidateSkipsCatalogChecksOnClose(t *testing.T) {
	d := Draft{Roles: []RoleDraft{{
		Op: OpClose, ID: "role-old",
		RequiresCapabilities:  []CapabilityRequirement{{Code: "cap.retired.thing"}},
		BasePermissionProfile: "pp-retired",
	}}}
	res := Validate(d, testCatalog())
	if !res.OK {
		t.Fatalf("a close must not be catalog-checked, got %+v", res.Problems)
	}
}

// Intra-draft references. Reported, and deliberately worded as "fine if it
// already exists" — this function reads no graph, so it cannot distinguish a
// typo from a reference to an existing team.
func TestValidateReportsUnresolvedIntraDraftReferences(t *testing.T) {
	d := Draft{
		Roles:       []RoleDraft{{Op: OpUpsert, ID: "r1", TeamID: "team-never-declared", BasePermissionProfile: "builder"}},
		Memberships: []MembershipDraft{{Op: OpUpsert, PartyID: "p-nope", RoleID: "r-nope"}},
	}
	res := Validate(d, testCatalog())
	if got := problemCodes(res)["unresolved_reference_in_draft"]; got != 3 {
		t.Fatalf("expected 3 unresolved references (role.teamId, membership.roleId, membership.partyId), got %d: %+v", got, res.Problems)
	}
}

// An empty draft is "no problems", and Checked is what stops that from reading
// as "validated". Asserted so nobody later treats OK on an empty draft as
// evidence.
func TestEmptyDraftIsOKButReportsNothingChecked(t *testing.T) {
	res := Validate(Draft{}, testCatalog())
	if !res.OK {
		t.Fatalf("an empty draft has no problems, got %+v", res.Problems)
	}
	if res.Checked.Teams+res.Checked.Roles+res.Checked.Parties+res.Checked.Memberships != 0 {
		t.Fatal("Checked must report zero for an empty draft")
	}
}

// Determinism: two identical drafts must produce byte-identical problem lists,
// or nobody can write a test against the output (the same argument
// authz.notSubset makes about sorting its refusal message).
func TestProblemOrderIsDeterministic(t *testing.T) {
	d := Draft{
		Roles: []RoleDraft{
			{Op: OpUpsert, ID: "r2", BasePermissionProfile: "nope-2"},
			{Op: OpUpsert, ID: "r1", BasePermissionProfile: "nope-1"},
		},
	}
	first := Validate(d, testCatalog())
	for i := 0; i < 5; i++ {
		again := Validate(d, testCatalog())
		if len(again.Problems) != len(first.Problems) {
			t.Fatal("problem count must be stable")
		}
		for j := range again.Problems {
			if again.Problems[j].Path != first.Problems[j].Path {
				t.Fatalf("problem order must be stable: %q vs %q", again.Problems[j].Path, first.Problems[j].Path)
			}
		}
	}
}
