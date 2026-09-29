package people

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"osiris-api/internal/authncred"
	"osiris-api/internal/authz"
	"osiris-api/internal/corewrite"
	"osiris-api/internal/secevents"
)

// ===========================================================================
// GET /api/me — 60-identity-authn.md §3.5's contract.
//
// H5 is the reason consoleSurfaces is computed HERE and shipped as data:
//
//	"화면 가시성 규칙을 프론트에 두면 40-console-ia.md의 사이드바가 규칙의
//	 두 번째 사본이 된다 — G10이 좌석 환산에 대해 금지한 바로 그 구조다."
//
// So the console draws what this array says and nothing else. Together with
// GET /api/people's allowedActions (the per-target half), the front end holds
// zero authorization logic — which is the only arrangement in which "the UI
// and the API agree" is a property rather than a hope.
//
// A machine principal cannot call this (§3.5: 403), and the reason is not
// squeamishness: /me is about a person's identity and screens, and a machine
// principal has neither. GET /api/whoami exists for a machine principal to
// confirm its own scope, and its response schema HAS NO partyId FIELD — "있으면
// 언젠가 누군가 채운다".
// ===========================================================================

// Me handles GET /api/me.
func (s *Service) Me(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorOf(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing or invalid session")
		return
	}
	if actor.Kind == authz.KindMachine {
		writeErr(w, http.StatusForbidden, "machine_principal_not_a_person")
		return
	}
	ctx := r.Context()

	// §3.5: a master-key session carries partyId:null and only the onboarding
	// surface. Today's operator session (setup token / cookie) is that
	// credential under its pre-H10 name (authz.KindOperator), and it is
	// reported honestly rather than dressed up as a person.
	if actor.Kind != authz.KindPerson || actor.PartyID == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"partyId":              nil,
			"kind":                 string(actor.Kind),
			"orgRole":              nil,
			"isOrgAdmin":           false,
			"effectivePermissions": authz.Strings(actor.Permissions),
			"consoleSurfaces":      []string{"onboarding"},
			"credentialPending":    false,
		})
		return
	}

	person, found, err := s.graph.GetPerson(ctx, actor.PartyID)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot read the org graph")
		return
	}
	if !found {
		// The session names a :Person the graph no longer has. Fail closed:
		// this is not an anonymous user, it is a session that should not
		// resolve to anything.
		writeErr(w, http.StatusForbidden, "session party no longer exists")
		return
	}

	perms, err := s.perms.ResolveFresh(ctx, actor.PartyID)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot resolve permissions")
		return
	}

	var orgRole any
	if person.OrgRoleID != "" {
		orgRole = map[string]string{
			"id": person.OrgRoleID, "title": person.OrgRole, "membershipId": person.OrgMember,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"partyId":              person.ID,
		"kind":                 "Person",
		"name":                 person.Name,
		"email":                person.Email,
		"active":               person.Active,
		"teams":                person.Teams,
		"roles":                person.JobRoles,
		"orgRole":              orgRole,
		"isOrgAdmin":           holds(perms, authz.PermOrgAdmin),
		"effectivePermissions": authz.Strings(perms),
		"consoleSurfaces":      consoleSurfaces(perms),
		"credentialPending":    actor.CredentialPending,
	})
}

// consoleSurfaces is §3.3's derivation table, backend-side.
//
// Only the rows this build can actually evaluate are computed. The rows that
// depend on facts no code produces yet — #4 Approvals (needs a grant spec's
// approver field), #3 Work's subtree rule, the My Tasks badge — are omitted
// rather than guessed, because a surface listed here is one the console will
// render and then find empty.
//
// Every row is derived from EXISTING facts: four permission codes and the
// isHead membership flag. §3.3's own point is that only §20.4's five codes are
// new vocabulary and nothing else in the table needed inventing.
func consoleSurfaces(perms []authz.Code) []string {
	out := []string{"my-tasks", "notifications", "org", "ontology.read", "work.own"}
	if holds(perms, authz.PermOrgReadOps) {
		out = append(out, "ops")
	}
	if holds(perms, authz.PermOrgReadOnboarding) {
		out = append(out, "onboarding.read")
	}
	if holds(perms, authz.PermOrgManagePeople) || holds(perms, authz.PermOrgManageRoles) {
		out = append(out, "people-and-roles")
	}
	if holds(perms, authz.PermOrgAdmin) {
		out = append(out, "admin-chat", "onboarding", "ontology.edit", "ops.machine-principals")
	}
	return out
}

// ===========================================================================
// POST /api/onboarding/initial-admin — the bootstrap handover, §4.1 ④-⑥.
//
// ── WHY THIS ENDPOINT EXISTS, AND WHY IT IS NOT A BACKDOOR ────────────────
//
// §4.1's sequence assumes the conversational onboarding commit (20 §9.2 steps
// 1-15, one transaction) creates the first :Person, gives them the org.admin
// role and attributes :OrgRevision{n:1} to that very person. That pipeline is
// not implemented in this build, so without something here the organization
// can never acquire its FIRST administrator: creating one requires a mutating
// write, a mutating write requires a :Party actor (§6.5), and the only person
// who could be that actor is the administrator who does not exist yet.
//
// This is the same chicken-and-egg §4.1 solves, solved the same way:
//
//   - Only a MASTER-KEY session may call it. §19.5's argument applies verbatim
//     — before an organization exists the only definable "authorized human" is
//     the one who can read a 0400 file on the host, and that person is already
//     host-root-equivalent (§9.1). Granting them this is not a new privilege,
//     it is a scope-limited expression of one they have.
//
//   - It is refused the moment an active org.admin holder exists. Like W1 and
//     MP1, the gate is a PREDICATE OVER THE WORLD re-evaluated per request,
//     not a burn flag: "특권이 '무엇을 가졌는가'가 아니라 '세계가 어떤
//     상태인가'에 조건부일 때만 유출된 값이 무력해진다". Once the org has an
//     administrator this endpoint is dead, permanently, without anyone
//     remembering to disable it.
//
//   - The write is attributed to the administrator being created, which is
//     exactly §4.1 ⑤'s "byPartyId = 그 초기 관리자 :Person.id". Genesis
//     attribution is self-attribution; there is no earlier party to blame.
//
//   - It returns an enrollment ticket, not a password and not a session. The
//     administrator sets their own credential by redeeming it (§5.1), so no
//     path anywhere lets one person choose another person's secret (§5.6's
//     state D: "경로 자체가 없다").
// ===========================================================================

type initialAdminReq struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (s *Service) BootstrapInitialAdmin(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorOf(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing or invalid session")
		return
	}
	// Master-key session only. A person session must use POST /api/people +
	// POST /api/people/{id}/org-role, where the delegation rules apply.
	if actor.Kind != authz.KindOperator {
		writeErr(w, http.StatusForbidden,
			"the initial administrator is bootstrapped by the master-key session only; use POST /api/people once an administrator exists")
		return
	}
	ctx := r.Context()

	// The world-state gate. UNDETERMINABLE → refuse (fail-closed): a graph we
	// cannot read must never be read as "there is no administrator yet".
	existing, err := s.graph.EffectiveHolders(ctx, string(authz.PermOrgAdmin))
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable,
			"cannot determine whether an administrator already exists; refusing (fail-closed)")
		return
	}
	if len(existing) > 0 {
		writeErr(w, http.StatusConflict,
			"this organization already has an administrator; the bootstrap path is permanently closed")
		return
	}

	var req initialAdminReq
	if !decodeBody(w, r, &req) {
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	req.Email = authncred.NormalizeEmail(req.Email)
	if !idPattern.MatchString(req.ID) || req.Name == "" || req.Email == "" {
		writeErr(w, http.StatusBadRequest, "id (matching "+idPattern.String()+"), name and email are required")
		return
	}

	// [1] :Person, attributed to itself — §4.1 ⑤'s genesis attribution.
	if _, err := s.core.CreatePerson(ctx, corewrite.PersonInput{
		ID: req.ID, Name: req.Name, Email: req.Email, Timezone: "Asia/Seoul",
		ByPartyID: req.ID,
	}); err != nil {
		status, msg := coreStatus(err)
		writeErr(w, status, msg)
		return
	}

	// [2] the org.admin role. core re-checks I14~I17 inside this transaction;
	// after it, I14 holds for the first time in this organization's life.
	res, err := s.core.AssignOrgRole(ctx, corewrite.AssignOrgRoleInput{
		MembershipID: fmt.Sprintf("mb-org-%s-%d", req.ID, s.now().UnixNano()),
		PersonID:     req.ID,
		RoleID:       adminRoleID(ctx, s),
		ByPartyID:    req.ID,
	})
	if err != nil {
		status, msg := coreStatus(err)
		writeErr(w, status, msg)
		return
	}
	s.perms.Invalidate(req.ID)

	// [3] the enrollment ticket. No DG check: there is no other administrator
	// to be stronger than the actor, and the actor is the master key, whose
	// scope already includes everything this endpoint does.
	ticket, rec, err := s.tickets.Issue(ctx, req.ID, "master-key")
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "administrator created but the enrollment ticket could not be issued")
		return
	}
	_ = s.events.Publish(ctx, secevents.EnrollmentIssued, secevents.Event{
		ActorRef: actor.ActorRef(), TargetPartyID: req.ID, Revision: res.Revision, At: s.now(),
	})

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":        req.ID,
		"revision":  res.Revision,
		"ticket":    ticket,
		"expiresAt": rec.ExpiresAt,
		"warning":   "This ticket is shown exactly once. Redeem it at POST /api/auth/session with a password of your choosing.",
	})
}

// adminRoleID finds the org role that grants org.admin.
//
// BY GRANTS, NOT BY ID. H30/HI17: authorization — and anything that decides
// what a role MEANS — reads :Permission.code and never a role id or title,
// because an id can be renamed or reused and a code cannot (the catalog is
// system-owned and not user-editable). Hard-coding the seeded id here would
// work today and would silently pick the wrong role the day an organization
// defines its own administrator role.
//
// Returns "" when no such role exists, which makes the subsequent core call
// fail with a legible "org-scoped :Role not found" rather than assigning
// something arbitrary.
func adminRoleID(ctx context.Context, s *Service) string {
	roles, err := s.graph.ListOrgRoles(ctx)
	if err != nil {
		return ""
	}
	best := ""
	bestN := 0
	for _, role := range roles {
		if !containsCode(role.Grants, authz.PermOrgAdmin) {
			continue
		}
		// Prefer the narrowest role that still grants org.admin — if an
		// organization has defined several, the least powerful one is the
		// right default for a bootstrap.
		if best == "" || len(role.Grants) < bestN {
			best, bestN = role.ID, len(role.Grants)
		}
	}
	return best
}
