// Package people implements 60-identity-authn.md §20.6's account and role
// management surface: create a :Person, grant/end an org role, issue an
// enrollment ticket, deactivate an account, and manipulate the
// HAS_PROFILE{additive} / DENIES edges that C10 puts under the same rules.
//
// ── THE ONE THING TO KNOW BEFORE ADDING A HANDLER HERE ────────────────────
//
// EVERY mutating handler in this file calls authz.CheckDelegation, and it is
// the ONLY place in osiris-api that decides who may act on whom (H31). HI16
// checks both halves of that: that each of these routes calls it, and that
// nothing outside internal/authz compares against the org.admin code.
//
// The reason is not tidiness. DG2 ("you cannot touch a principal stronger than
// yourself") is the rule 60 §21.1 singles out as the easiest to omit, and it
// is easy to omit precisely because it is not about the thing being granted —
// it is about the target. A handler author thinking "this endpoint only ends a
// membership, there is nothing to grant, DG1 does not apply" writes a correct
// DG1 check and a privilege escalation. Routing everything through one
// function makes the rules apply to actions their author did not think about.
//
// ── SHAPE OF EVERY MUTATION, IN ORDER ─────────────────────────────────────
//
//  1. actor  := session principal        (H19/H28 — never from the request)
//  2. target := path parameter, resolved against the graph
//  3. eff(A), eff(T), grants(R) resolved FRESH from the graph (no cache —
//     an authorization decision must not run on a value that was true 9
//     seconds ago)
//  4. authz.CheckDelegation(...)         → refuse ⇒ 403/409 + audit event
//  5. DG5: if the target holds org.admin, publish security.rbac.* and ABORT
//     ON PUBLISH FAILURE (fail-closed) before anything changes
//  6. osiris-core graph-write            → :OrgRevision +1, I14~I17 re-checked
//     inside that transaction, DG5's :Notification fan-out in the same
//     transaction
//  7. invalidate the actor/target permission cache (an optimization only —
//     correctness comes from the TTL, see authz.PermissionCacheTTL)
//
// Step 5 before step 6 is MP6's ordering reused: an unaudited change is the
// failure mode being prevented, so the audit record is established first and
// the change is abandoned if it cannot be.
package people

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"osiris-api/internal/authncred"
	"osiris-api/internal/authz"
	"osiris-api/internal/corewrite"
	"osiris-api/internal/enrollment"
	"osiris-api/internal/graphread"
	"osiris-api/internal/orgstate"
	"osiris-api/internal/secevents"
	"osiris-api/internal/session"
)

// idPattern constrains caller-supplied :Person ids. They appear in event
// payloads, in actorRef strings ("party:<id>") and in graph attribution
// fields; a value containing a colon or whitespace would make those ambiguous.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)

// Service holds the collaborators every handler needs. Each is an interface or
// a concrete type owned by exactly one design section, and none of them
// duplicates another's job:
//
//	graph     20 §7.1 reads (effective(party), people, org roles)  — READ ONLY
//	core      D17/G7's single write gate
//	perms     the cached PermissionSource, for cheap non-decision reads
//	tickets   AUTHN_TICKET
//	creds     AUTHN_CRED
//	sessions  AUTHN_SESSION (in-process in this build)
//	dir       the graph×KV intersection DG4's second layer needs
//	events    security.rbac.* / security.enrollment.*
type Service struct {
	graph    *graphread.Client
	core     *corewrite.Client
	perms    *authz.GraphPermissionSource
	tickets  *enrollment.Store
	creds    *authncred.Store
	sessions *session.Store
	dir      *orgstate.GraphCredentialDirectory
	events   *secevents.Publisher
	now      func() time.Time
}

func NewService(
	graph *graphread.Client,
	core *corewrite.Client,
	perms *authz.GraphPermissionSource,
	tickets *enrollment.Store,
	creds *authncred.Store,
	sessions *session.Store,
	dir *orgstate.GraphCredentialDirectory,
	events *secevents.Publisher,
) *Service {
	return &Service{
		graph: graph, core: core, perms: perms, tickets: tickets,
		creds: creds, sessions: sessions, dir: dir, events: events,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// --- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeRefusal renders a delegation refusal. The RULE is included because an
// operator needs to know WHICH of the five stopped them — "403" alone sends
// them to try the same thing a different way. It is a stable identifier, not
// prose, so the console can localize it.
func writeRefusal(w http.ResponseWriter, d authz.Decision) {
	writeJSON(w, d.Status, map[string]string{
		"error": d.Reason,
		"rule":  d.Rule,
	})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// coreStatus maps an osiris-core write failure onto a console-facing status.
//
// A 400 from core is usually an invariant refusal (I14~I17) and is passed
// through with its message — "this change would leave zero active :Person
// holding org.admin" is exactly what the operator needs to read, and it is the
// GRAPH LAYER of DG4 speaking, distinct from the API layer's 409. Anything
// unexpected becomes 502: it is core's failure, not the caller's.
func coreStatus(err error) (int, string) {
	var ce *corewrite.Error
	if errors.As(err, &ce) {
		switch {
		case ce.Status == http.StatusBadRequest:
			return http.StatusBadRequest, ce.Message
		case ce.Status == http.StatusForbidden:
			return http.StatusForbidden, ce.Message
		case ce.Status == http.StatusServiceUnavailable:
			return http.StatusServiceUnavailable, ce.Message
		default:
			return http.StatusBadGateway, ce.Message
		}
	}
	return http.StatusBadGateway, "graph write failed"
}

// actorOf extracts the authenticated principal and its :Person id.
//
// A KindOperator principal (today's setup-token / master-key session) has NO
// PartyID, and that is not an oversight to paper over: §3.1 calls it "the only
// session without a :Party", and §6.5 says core refuses a mutating request
// whose actor is not a valid :Party. So an operator can READ this surface but
// cannot write through it, and the refusal says so plainly instead of sending
// core a request it will reject.
//
// The ONE exception is the bootstrap path (BootstrapInitialAdmin), which
// attributes the genesis write to the administrator it is creating — exactly
// what §4.1 ⑤ specifies for the org commit.
func actorOf(r *http.Request) (authz.Principal, bool) {
	return authz.PrincipalFrom(r)
}

func requirePersonActor(w http.ResponseWriter, r *http.Request) (authz.Principal, bool) {
	p, ok := actorOf(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing or invalid session")
		return p, false
	}
	if p.Kind != authz.KindPerson || p.PartyID == "" {
		writeErr(w, http.StatusForbidden,
			"this operation changes organizational intent and must be attributed to a :Person; the current session has no party identity (60 §6.5)")
		return p, false
	}
	return p, true
}

// resolveTarget loads T and its effective permissions, both from the graph and
// both FRESH.
//
// Fresh, not cached: authz.PermissionCacheTTL exists so that ordinary request
// authorization does not hit Neo4j 60 times a minute, and a 10-second-old
// answer is fine for "may this person open the Ops screen". It is NOT fine for
// "is this person still an administrator I am forbidden to touch" — a demotion
// that happened four seconds ago must already count. Same argument
// machineauth.Revalidate makes for the streaming watchdog.
func (s *Service) resolveTarget(ctx context.Context, id string) (graphread.Person, []authz.Code, error) {
	p, ok, err := s.graph.GetPerson(ctx, id)
	if err != nil {
		return p, nil, err
	}
	if !ok {
		return p, nil, errNoSuchPerson
	}
	codes, err := s.perms.ResolveFresh(ctx, id)
	if err != nil {
		return p, nil, err
	}
	return p, codes, nil
}

var errNoSuchPerson = errors.New("no such person")

// prepare gathers everything CheckDelegation needs and runs it.
//
// It returns the decision plus the resolved target, so the handler never
// re-reads anything between the decision and the write — a re-read there would
// be a TOCTOU window in the one place that must not have one.
func (s *Service) prepare(ctx context.Context, actor authz.Principal, targetID string, action authz.Action, grants []authz.Code, removesAdmin func(targetPerms []authz.Code) bool) (authz.Decision, graphread.Person, []authz.Code, error) {
	target, targetPerms, err := s.resolveTarget(ctx, targetID)
	if err != nil {
		return authz.Decision{}, target, nil, err
	}

	// eff(A), fresh. The actor's own codes are the ceiling for DG1 and DG2; a
	// stale set could authorize a grant the actor lost seconds ago.
	actorPerms, err := s.perms.ResolveFresh(ctx, actor.PartyID)
	if err != nil {
		return authz.Decision{}, target, nil, err
	}

	// DG4's second layer input. Read even when the action cannot remove an
	// admin — the read is cheap relative to the write that follows, and making
	// it conditional would mean the ONE case that needs it is the one path
	// nobody exercises in testing.
	credentialed, err := s.dir.CredentialedHolderIDs(ctx, authz.PermOrgAdmin)
	if err != nil {
		return authz.Decision{}, target, nil, err
	}

	strips := false
	if removesAdmin != nil {
		strips = removesAdmin(targetPerms)
	}

	d := authz.CheckDelegation(authz.DelegationRequest{
		Action:               action,
		ActorPartyID:         actor.PartyID,
		ActorPerms:           actorPerms,
		TargetPartyID:        target.ID,
		TargetPerms:          targetPerms,
		GrantCodes:           grants,
		RemovesTargetAdmin:   strips,
		CredentialedAdminIDs: credentialed,
	})
	return d, target, targetPerms, nil
}

// refused publishes the audit trail for a denial and writes the response.
//
// A refused escalation attempt is a security event in its own right — 60 §21.1
// DG5's rationale ("조용한 관리자 교체" must be impossible) applies at least as
// much to the attempt as to the success. Publishing is BEST EFFORT here,
// unlike DG5's fail-closed publish on success: refusing to refuse because the
// audit bus is down would turn a logging outage into an authorization bypass.
func (s *Service) refused(ctx context.Context, w http.ResponseWriter, actor authz.Principal, targetID string, action authz.Action, d authz.Decision) {
	_ = s.events.Publish(ctx, secevents.RBACDelegationDenied, secevents.Event{
		ActorRef:      actor.ActorRef(),
		TargetPartyID: targetID,
		Reason:        string(action),
		Rule:          d.Rule,
		At:            s.now(),
	})
	writeRefusal(w, d)
}

// announce is DG5's fail-closed publish, called AFTER the decision and BEFORE
// the write.
//
// §21.1 DG5: "발행 실패 시 변경하지 않는다(fail-closed)". Returning false means
// the handler must abort — the response has already been written.
func (s *Service) announce(ctx context.Context, w http.ResponseWriter, subject string, ev secevents.Event, required bool) bool {
	if err := s.events.Publish(ctx, subject, ev); err != nil {
		if required {
			writeErr(w, http.StatusServiceUnavailable,
				"refusing to change an administrator's account: the mandatory audit event could not be published (DG5 fail-closed)")
			return false
		}
		// Non-DG5 events are still published, still best-effort. The change
		// itself is recorded in the graph as an :OrgRevision either way, so
		// the audit trail is not lost — only its real-time notification is.
		return true
	}
	return true
}

func holds(codes []authz.Code, want authz.Code) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

func containsCode(codes []string, want authz.Code) bool {
	for _, c := range codes {
		if authz.Code(c) == want {
			return true
		}
	}
	return false
}
