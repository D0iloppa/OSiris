package people

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"osiris-api/internal/authncred"
	"osiris-api/internal/authz"
	"osiris-api/internal/corewrite"
	"osiris-api/internal/graphread"
	"osiris-api/internal/secevents"
)

// ===========================================================================
// GET /api/people — the People & Roles list (console screen #10's data).
//
// §3.3's closing note is the contract this response implements:
//
//	"People & Roles 화면에서 '이 사람의 역할을 바꿀 수 있는가 / 티켓을 발급할
//	 수 있는가'는 §21의 위임 규칙이 결정하는데, 이 규칙을 프론트가
//	 재구현하면 인가 판정이 두 곳에 존재하게 된다(H31이 금지하는 바로 그
//	 상태). 그래서 사람 목록 응답의 각 행에 백엔드가 allowedActions를 계산해
//	 싣고, 콘솔은 그 배열에 없는 버튼을 아예 그리지 않는다."
//
// So allowedActions is not a convenience field. It is the mechanism that keeps
// the delegation rules from being reimplemented in TypeScript, and it is
// computed by calling the SAME authz.CheckDelegation the mutating handlers
// call — a dry run per row per action. If the console ever renders a button
// this array does not contain, the request behind it is refused anyway; the
// array exists so the user is not offered actions that will fail.
// ===========================================================================

type personRow struct {
	graphread.Person

	// HasCredential / TicketOutstanding are AUTHN_CRED and AUTHN_TICKET facts.
	// The graph cannot answer either (H1's one-way reference), so they are
	// joined here — this is the one place in the system allowed to know both.
	HasCredential     bool `json:"hasCredential"`
	TicketOutstanding bool `json:"ticketOutstanding"`

	// EffectivePermissions is 20 §7.1's result for this person, so an
	// administrator can audit "why does this person have that" without a
	// Cypher shell (Q6's purpose, surfaced).
	EffectivePermissions []string `json:"effectivePermissions"`

	// AllowedActions is the subset of {"assign-role","revoke-role",
	// "issue-ticket","deactivate","set-additive-profile","set-deny"} that the
	// CURRENT actor may perform on THIS row. See the block comment above.
	AllowedActions []string `json:"allowedActions"`
}

// List handles GET /api/people.
func (s *Service) List(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorOf(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing or invalid session")
		return
	}
	ctx := r.Context()

	rows, err := s.graph.ListPeople(ctx)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot read people from the org graph")
		return
	}

	// Actor's own codes, resolved once for the whole list.
	var actorPerms []authz.Code
	if actor.Kind == authz.KindPerson && actor.PartyID != "" {
		actorPerms, err = s.perms.ResolveFresh(ctx, actor.PartyID)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "cannot resolve actor permissions")
			return
		}
	} else {
		actorPerms = actor.Permissions
	}

	credentialed, err := s.dir.CredentialedHolderIDs(ctx, authz.PermOrgAdmin)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot determine credentialed administrators")
		return
	}

	out := make([]personRow, 0, len(rows))
	for _, p := range rows {
		perms, err := s.perms.ResolveFresh(ctx, p.ID)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "cannot resolve permissions")
			return
		}
		hasCred, err := s.creds.Has(ctx, p.ID)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "credential store unavailable")
			return
		}
		outstanding, err := s.tickets.Outstanding(ctx, p.ID)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "enrollment ticket store unavailable")
			return
		}
		out = append(out, personRow{
			Person:               p,
			HasCredential:        hasCred,
			TicketOutstanding:    outstanding,
			EffectivePermissions: authz.Strings(perms),
			AllowedActions:       s.allowedActions(actor, actorPerms, p, perms, credentialed),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"people": out,
		// Stated rather than implied — the console shows "changes take effect
		// within N seconds" instead of appearing to lie when a demotion is not
		// instantly visible on another tab.
		"permissionCacheSeconds": int(authz.PermissionCacheTTL.Seconds()),
	})
}

// allowedActions dry-runs the delegation rules for one row.
//
// Deliberately NOT a reimplementation with "if actor is admin" shortcuts: it
// builds the same DelegationRequest the mutating handler would and asks the
// same function. That is what makes the button the console draws and the
// request it sends agree by construction rather than by review.
func (s *Service) allowedActions(actor authz.Principal, actorPerms []authz.Code, target graphread.Person, targetPerms []authz.Code, credentialed []string) []string {
	var out []string
	try := func(name string, action authz.Action, grants []authz.Code, removes bool) {
		d := authz.CheckDelegation(authz.DelegationRequest{
			Action:               action,
			ActorPartyID:         actor.PartyID,
			ActorPerms:           actorPerms,
			TargetPartyID:        target.ID,
			TargetPerms:          targetPerms,
			GrantCodes:           grants,
			RemovesTargetAdmin:   removes,
			CredentialedAdminIDs: credentialed,
		})
		if d.Allowed {
			out = append(out, name)
		}
	}
	// assign-role is reported WITHOUT a concrete role: it means "the actor may
	// open the role picker for this person". The picker's own options are
	// filtered by GET /api/org-roles, which applies DG1 per role — the two
	// together are the complete answer, and neither alone is.
	if holds(actorPerms, authz.PermOrgManageRoles) {
		try("assign-role", authz.ActionAssignOrgRole, nil, false)
		if target.OrgRoleID != "" {
			try("revoke-role", authz.ActionRevokeOrgRole, nil, holds(targetPerms, authz.PermOrgAdmin))
		}
		try("set-additive-profile", authz.ActionSetAdditiveProfile, nil, false)
		try("set-deny", authz.ActionSetDeny, nil, false)
	}
	if holds(actorPerms, authz.PermOrgManagePeople) {
		try("issue-ticket", authz.ActionIssueEnrollmentTicket, nil, false)
		if target.Active {
			try("deactivate", authz.ActionDeactivatePerson, nil, holds(targetPerms, authz.PermOrgAdmin))
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// ===========================================================================
// GET /api/org-roles — the assignable org roles, filtered by DG1.
// ===========================================================================

// ListOrgRoles reports every :Role{scope:'org'} and whether THIS actor may
// grant it (DG1: grants(R) ⊆ eff(A)).
//
// The filtering is advisory — the assign handler re-checks — but it is the
// same function, so the list and the outcome cannot disagree.
func (s *Service) ListOrgRoles(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorOf(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing or invalid session")
		return
	}
	ctx := r.Context()
	roles, err := s.graph.ListOrgRoles(ctx)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot read org roles")
		return
	}
	actorPerms := actor.Permissions
	if actor.Kind == authz.KindPerson && actor.PartyID != "" {
		actorPerms, err = s.perms.ResolveFresh(ctx, actor.PartyID)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "cannot resolve actor permissions")
			return
		}
	}
	type roleRow struct {
		graphread.OrgRole
		Grantable bool `json:"grantable"`
	}
	out := make([]roleRow, 0, len(roles))
	for _, role := range roles {
		d := authz.CheckDelegation(authz.DelegationRequest{
			Action:       authz.ActionAssignOrgRole,
			ActorPartyID: actor.PartyID,
			ActorPerms:   actorPerms,
			// No target: this is the grant-ceiling question only (DG1/DG1′).
			// A per-target answer is what GET /api/people's allowedActions is
			// for, and conflating them would make this endpoint's meaning
			// depend on a target it was not given.
			TargetPartyID: "",
			GrantCodes:    toCodes(role.Grants),
		})
		out = append(out, roleRow{OrgRole: role, Grantable: d.Allowed})
	}
	writeJSON(w, http.StatusOK, map[string]any{"orgRoles": out})
}

func toCodes(raw []string) []authz.Code {
	out := make([]authz.Code, 0, len(raw))
	for _, r := range raw {
		if c := authz.Code(r); authz.InCatalog(c) {
			out = append(out, c)
		}
	}
	return out
}

// ===========================================================================
// POST /api/people — §20.6 [1], create a :Person.
// ===========================================================================

type createPersonReq struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Timezone string `json:"timezone"`
}

// Create handles POST /api/people (org.manage.people).
//
// NO DELEGATION CHECK, AND THAT IS CORRECT: a brand-new :Person has no
// permissions, so DG1 has nothing to compare (the creation grants nothing),
// DG2's eff(T) is empty and trivially a subset of anything, and there is no
// target to be self. §20.6's table gates step [1] on org.manage.people alone,
// and the step that actually confers power — [2], the org role — is where the
// rules bite.
//
// §15.2's constraint is preserved: this path goes console → osiris-api →
// core's single graph-write gate and bumps the revision. An IdP adapter still
// cannot create organizational members, which is what that sentence was for.
func (s *Service) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := requirePersonActor(w, r)
	if !ok {
		return
	}
	var req createPersonReq
	if !decodeBody(w, r, &req) {
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	req.Email = authncred.NormalizeEmail(req.Email)
	if !idPattern.MatchString(req.ID) {
		writeErr(w, http.StatusBadRequest, "id must match "+idPattern.String())
		return
	}
	if req.Name == "" || req.Email == "" {
		writeErr(w, http.StatusBadRequest, "name and email are required")
		return
	}
	if req.Timezone == "" {
		req.Timezone = "Asia/Seoul"
	}

	res, err := s.core.CreatePerson(r.Context(), corewrite.PersonInput{
		ID: req.ID, Name: req.Name, Email: req.Email, Timezone: req.Timezone,
		ByPartyID: actor.PartyID,
	})
	if err != nil {
		status, msg := coreStatus(err)
		writeErr(w, status, msg)
		return
	}
	_ = s.events.Publish(r.Context(), secevents.RBACPersonCreated, secevents.Event{
		ActorRef: actor.ActorRef(), TargetPartyID: req.ID,
		Revision: res.Revision, At: s.now(),
	})
	writeJSON(w, http.StatusCreated, map[string]any{"id": res.ID, "revision": res.Revision})
}

// ===========================================================================
// POST /api/people/{id}/org-role — §20.6 [2]/[5], grant or change a role.
// ===========================================================================

type assignRoleReq struct {
	RoleID string `json:"roleId"`
}

func (s *Service) AssignRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := requirePersonActor(w, r)
	if !ok {
		return
	}
	targetID := r.PathValue("id")
	var req assignRoleReq
	if !decodeBody(w, r, &req) {
		return
	}
	ctx := r.Context()

	grantsRaw, found, err := s.graph.OrgRoleGrants(ctx, req.RoleID)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot read org role")
		return
	}
	if !found {
		// NOT treated as "grants nothing". An empty grant set trivially
		// satisfies DG1, so conflating a missing role with an empty one would
		// let a typo'd id sail past the very check meant to stop it.
		writeErr(w, http.StatusBadRequest, "no such org-scoped role")
		return
	}
	grants := toCodes(grantsRaw)
	newRoleGrantsAdmin := containsCode(grantsRaw, authz.PermOrgAdmin)

	d, target, targetPerms, err := s.prepare(ctx, actor, targetID, authz.ActionAssignOrgRole, grants,
		func(tp []authz.Code) bool {
			// The role change strips org.admin when the target holds it today
			// and the incoming role does not grant it. Conservative on
			// purpose: if the target ALSO holds org.admin through an additive
			// profile the change would not really strip it, and DG4 would
			// refuse something that is in fact safe. Over-refusing a
			// lockout-adjacent change is the acceptable direction of error.
			return holds(tp, authz.PermOrgAdmin) && !newRoleGrantsAdmin
		})
	if err != nil {
		s.resolveErr(w, err)
		return
	}
	if !d.Allowed {
		s.refused(ctx, w, actor, targetID, authz.ActionAssignOrgRole, d)
		return
	}

	// DG5 — fail-closed BEFORE the change. Required when the target is an
	// administrator today or would become one.
	notify := d.NotifyAdmins || newRoleGrantsAdmin
	if !s.announce(ctx, w, secevents.RBACRoleAssigned, secevents.Event{
		ActorRef: actor.ActorRef(), TargetPartyID: target.ID,
		RoleID: req.RoleID, At: s.now(),
	}, notify) {
		return
	}

	res, err := s.core.AssignOrgRole(ctx, corewrite.AssignOrgRoleInput{
		MembershipID: fmt.Sprintf("mb-org-%s-%d", target.ID, s.now().UnixNano()),
		PersonID:     target.ID,
		RoleID:       req.RoleID,
		ByPartyID:    actor.PartyID,
	})
	if err != nil {
		status, msg := coreStatus(err)
		writeErr(w, status, msg)
		return
	}
	s.perms.Invalidate(target.ID)
	_ = targetPerms
	writeJSON(w, http.StatusOK, map[string]any{"id": target.ID, "roleId": req.RoleID, "revision": res.Revision})
}

// DELETE /api/people/{id}/org-role — end the membership without replacing it.
func (s *Service) RevokeRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := requirePersonActor(w, r)
	if !ok {
		return
	}
	targetID := r.PathValue("id")
	ctx := r.Context()

	d, target, _, err := s.prepare(ctx, actor, targetID, authz.ActionRevokeOrgRole, nil,
		func(tp []authz.Code) bool { return holds(tp, authz.PermOrgAdmin) })
	if err != nil {
		s.resolveErr(w, err)
		return
	}
	if !d.Allowed {
		s.refused(ctx, w, actor, targetID, authz.ActionRevokeOrgRole, d)
		return
	}
	if !s.announce(ctx, w, secevents.RBACRoleRevoked, secevents.Event{
		ActorRef: actor.ActorRef(), TargetPartyID: target.ID, At: s.now(),
	}, d.NotifyAdmins) {
		return
	}
	res, err := s.core.RevokeOrgRole(ctx, corewrite.RevokeOrgRoleInput{
		PersonID: target.ID, ByPartyID: actor.PartyID,
	})
	if err != nil {
		status, msg := coreStatus(err)
		writeErr(w, status, msg)
		return
	}
	s.perms.Invalidate(target.ID)
	writeJSON(w, http.StatusOK, map[string]any{"id": target.ID, "revision": res.Revision})
}

// ===========================================================================
// POST /api/people/{id}/deactivate — §20.6 [6].
// ===========================================================================

func (s *Service) Deactivate(w http.ResponseWriter, r *http.Request) {
	actor, ok := requirePersonActor(w, r)
	if !ok {
		return
	}
	targetID := r.PathValue("id")
	ctx := r.Context()

	d, target, _, err := s.prepare(ctx, actor, targetID, authz.ActionDeactivatePerson, nil,
		func(tp []authz.Code) bool { return holds(tp, authz.PermOrgAdmin) })
	if err != nil {
		s.resolveErr(w, err)
		return
	}
	if !d.Allowed {
		s.refused(ctx, w, actor, targetID, authz.ActionDeactivatePerson, d)
		return
	}
	if !s.announce(ctx, w, secevents.RBACPersonDeactivated, secevents.Event{
		ActorRef: actor.ActorRef(), TargetPartyID: target.ID, At: s.now(),
	}, d.NotifyAdmins) {
		return
	}

	res, err := s.core.DeactivatePerson(ctx, corewrite.DeactivateInput{
		PersonID: target.ID, ByPartyID: actor.PartyID,
	})
	if err != nil {
		status, msg := coreStatus(err)
		writeErr(w, status, msg)
		return
	}

	// §14.1's one-way arrow, walked: THE GRAPH COMMITTED FIRST and the KV
	// follows. Ordering matters — purging credentials before a write that
	// might be refused by I14 would log someone out of an account that still
	// exists. Failures here are logged into the audit event rather than
	// rolled back: the person is deactivated in the graph, which is the
	// authoritative fact, and a stale credential is caught on the next request
	// because effective(party) of a closed :Person is empty.
	credErr := s.creds.Revoke(ctx, target.ID)
	tickErr := s.tickets.RevokeFor(ctx, target.ID)
	sessions := s.sessions.RevokeAllFor(target.ID)
	s.perms.Invalidate(target.ID)

	reason := ""
	if credErr != nil || tickErr != nil {
		reason = "credential-cleanup-partial"
	}
	_ = s.events.Publish(ctx, secevents.RBACPersonDeactivated, secevents.Event{
		ActorRef: actor.ActorRef(), TargetPartyID: target.ID,
		Revision: res.Revision, Reason: reason, At: s.now(),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"id": target.ID, "revision": res.Revision, "sessionsRevoked": sessions,
	})
}

// ===========================================================================
// POST /api/people/{id}/enrollment-ticket — §20.6 [3].
//
// THE ACCOUNT-TAKEOVER PRIMITIVE (§5.3's 판정 ⑤). Gated on org.manage.people
// AND passed through DG2, because a ticket lets its holder set a new password
// on the bound account: issuing one for an administrator IS taking over that
// administrator.
// ===========================================================================

func (s *Service) IssueTicket(w http.ResponseWriter, r *http.Request) {
	actor, ok := requirePersonActor(w, r)
	if !ok {
		return
	}
	targetID := r.PathValue("id")
	ctx := r.Context()

	d, target, _, err := s.prepare(ctx, actor, targetID, authz.ActionIssueEnrollmentTicket, nil, nil)
	if err != nil {
		s.resolveErr(w, err)
		return
	}
	if !d.Allowed {
		s.refused(ctx, w, actor, targetID, authz.ActionIssueEnrollmentTicket, d)
		return
	}
	if !target.Active {
		writeErr(w, http.StatusConflict, "cannot enroll a deactivated person")
		return
	}
	if !s.announce(ctx, w, secevents.EnrollmentIssued, secevents.Event{
		ActorRef: actor.ActorRef(), TargetPartyID: target.ID, At: s.now(),
	}, d.NotifyAdmins) {
		return
	}

	ticket, rec, err := s.tickets.Issue(ctx, target.ID, actor.PartyID)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot issue enrollment ticket")
		return
	}
	// The plaintext appears HERE and nowhere else, ever: not in a log, not in
	// the store (hash only), not in a second response. §5.2's out-of-band
	// delivery means the administrator reads it once and hands it over.
	writeJSON(w, http.StatusCreated, map[string]any{
		"personId":  target.ID,
		"ticket":    ticket,
		"expiresAt": rec.ExpiresAt,
		"warning":   "This ticket is shown exactly once and is not recoverable. Deliver it out of band; do not write it to a file, a log, or a chat that is retained.",
	})
}

// ===========================================================================
// POST /api/people/{id}/additive-profile and .../deny — C10's other two.
//
// §21.4-2 is the reason these are endpoints at all rather than something an
// administrator does in a Cypher shell: "deny가 항상 이긴다", so attaching
// DENIES org.admin to an administrator neutralizes them without touching any
// role, and every one of DG1~DG4 would be bypassed through that single edge if
// the operation lived outside this file.
//
// They are gated on org.manage.roles rather than org.admin ON PURPOSE. Gating
// them on org.admin would make a manager's attempt fail at the router, which
// looks the same from outside but is a WEAKER guarantee: it would mean DG2
// never runs on this path, and the day someone adds a "permission exceptions"
// screen for managers the hole opens with no test failing. Letting the request
// reach CheckDelegation is what makes the refusal a property of the rules.
// ===========================================================================

type additiveProfileReq struct {
	ProfileID string `json:"profileId"`
	Attach    bool   `json:"attach"`
}

func (s *Service) SetAdditiveProfile(w http.ResponseWriter, r *http.Request) {
	actor, ok := requirePersonActor(w, r)
	if !ok {
		return
	}
	targetID := r.PathValue("id")
	var req additiveProfileReq
	if !decodeBody(w, r, &req) {
		return
	}
	ctx := r.Context()

	grantsRaw, found, err := s.graph.ProfileGrants(ctx, req.ProfileID)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot read permission profile")
		return
	}
	if !found {
		writeErr(w, http.StatusBadRequest, "no such permission profile")
		return
	}

	// DG1 applies to what the operation ADDS. Attaching adds the profile's
	// grants; detaching adds nothing but may REMOVE org.admin, which is DG4's
	// concern instead.
	var grants []authz.Code
	if req.Attach {
		grants = toCodes(grantsRaw)
	}
	profileGrantsAdmin := containsCode(grantsRaw, authz.PermOrgAdmin)

	d, target, _, err := s.prepare(ctx, actor, targetID, authz.ActionSetAdditiveProfile, grants,
		func(tp []authz.Code) bool {
			return !req.Attach && profileGrantsAdmin && holds(tp, authz.PermOrgAdmin)
		})
	if err != nil {
		s.resolveErr(w, err)
		return
	}
	if !d.Allowed {
		s.refused(ctx, w, actor, targetID, authz.ActionSetAdditiveProfile, d)
		return
	}
	if !s.announce(ctx, w, secevents.RBACProfileChanged, secevents.Event{
		ActorRef: actor.ActorRef(), TargetPartyID: target.ID,
		ProfileID: req.ProfileID, At: s.now(),
	}, d.NotifyAdmins || profileGrantsAdmin) {
		return
	}
	res, err := s.core.SetAdditiveProfile(ctx, corewrite.PartyGrantInput{
		PartyID: target.ID, ProfileID: req.ProfileID, Attach: req.Attach, ByPartyID: actor.PartyID,
	})
	if err != nil {
		status, msg := coreStatus(err)
		writeErr(w, status, msg)
		return
	}
	s.perms.Invalidate(target.ID)
	writeJSON(w, http.StatusOK, map[string]any{"id": target.ID, "revision": res.Revision})
}

type denyReq struct {
	PermCode string `json:"permCode"`
	Attach   bool   `json:"attach"`
}

func (s *Service) SetDeny(w http.ResponseWriter, r *http.Request) {
	actor, ok := requirePersonActor(w, r)
	if !ok {
		return
	}
	targetID := r.PathValue("id")
	var req denyReq
	if !decodeBody(w, r, &req) {
		return
	}
	code := authz.Code(strings.TrimSpace(req.PermCode))
	if !authz.InCatalog(code) {
		writeErr(w, http.StatusBadRequest, "unknown permission code")
		return
	}
	ctx := r.Context()

	// THE INVERSION THAT MATTERS. Attaching a deny grants nothing, so DG1 has
	// no input. REMOVING a deny hands the code back, which is a grant in every
	// sense that matters, so the code goes into GrantCodes and DG1/DG1′ apply
	// to it. Getting this backwards would make "un-deny org.admin" a free
	// promotion for anyone who could write a deny edge.
	var grants []authz.Code
	if !req.Attach {
		grants = []authz.Code{code}
	}

	d, target, _, err := s.prepare(ctx, actor, targetID, authz.ActionSetDeny, grants,
		func(tp []authz.Code) bool {
			return req.Attach && code == authz.PermOrgAdmin && holds(tp, authz.PermOrgAdmin)
		})
	if err != nil {
		s.resolveErr(w, err)
		return
	}
	if !d.Allowed {
		s.refused(ctx, w, actor, targetID, authz.ActionSetDeny, d)
		return
	}
	if !s.announce(ctx, w, secevents.RBACDenyChanged, secevents.Event{
		ActorRef: actor.ActorRef(), TargetPartyID: target.ID,
		PermCode: string(code), At: s.now(),
	}, d.NotifyAdmins || code == authz.PermOrgAdmin) {
		return
	}
	res, err := s.core.SetDeny(ctx, corewrite.PartyGrantInput{
		PartyID: target.ID, PermCode: string(code), Attach: req.Attach, ByPartyID: actor.PartyID,
	})
	if err != nil {
		status, msg := coreStatus(err)
		writeErr(w, status, msg)
		return
	}
	s.perms.Invalidate(target.ID)
	writeJSON(w, http.StatusOK, map[string]any{"id": target.ID, "revision": res.Revision})
}

// resolveErr maps the shared failure modes of prepare().
//
// A graph or credential-store failure is 503, NEVER an allow: 60 §8.5's
// standing rule and this task's explicit instruction — "판정 불가(그래프 도달
// 불가) 시 거부(fail-closed)".
func (s *Service) resolveErr(w http.ResponseWriter, err error) {
	if errors.Is(err, errNoSuchPerson) {
		writeErr(w, http.StatusNotFound, "no such person")
		return
	}
	writeErr(w, http.StatusServiceUnavailable,
		"cannot evaluate the delegation rules right now; refusing (fail-closed)")
}
