// Package api is osiris-core's graph-write API — validated, high-level
// operations only. There is deliberately NO handler that accepts and
// executes a caller-supplied Cypher string (A5/G7): every route below maps
// 1:1 to a named function in internal/graphdb whose query text is fixed at
// compile time. If you're looking for a "run arbitrary query" endpoint to
// add here, don't — that is exactly what this API exists to prevent.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"osiris-core/internal/action/effect"
	"osiris-core/internal/action/predicate"
	"osiris-core/internal/config"
	"osiris-core/internal/events"
	"osiris-core/internal/genesis"
	"osiris-core/internal/graphdb"
)

type Server struct {
	cfg    config.Config
	db     *graphdb.Client
	pub    *events.Publisher
	health *HealthState

	// gen verifies the genesis principal's K8s bound ServiceAccount token via
	// TokenReview (70-kubernetes-deployment.md §4.1, K7) — see
	// internal/genesis's package doc comment for what replaced §8.5's
	// in-process Store. Nil is tolerated and makes every genesis path answer
	// 503 — the same fail-closed shape s.auth uses for a missing platform
	// token, rather than a nil dereference on the first onboarding request.
	gen *genesis.Guard
}

func NewServer(cfg config.Config, db *graphdb.Client, pub *events.Publisher, health *HealthState, gen *genesis.Guard) *Server {
	return &Server{cfg: cfg, db: db, pub: pub, health: health, gen: gen}
}

// HealthMux serves ONLY /livez and /readyz. Kept on its own listener/port so
// a slow or saturated write-API path can never make health probes queue
// behind it (§5.4 principle).
func (s *Server) HealthMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", s.health.LivezHandler)
	mux.HandleFunc("/readyz", s.health.ReadyzHandler)
	return mux
}

// APIMux serves the graph-write API. Every mutating route requires a bearer
// token (agent-facing port + token auth per §2.6's network table: "에이전트
// 대면 포트는 별도 포트 + 에이전트 토큰 인증"). See IMPLEMENTATION-NOTES.md
// for what's simplified here relative to the full §8.5 genesis-token model
// (that model's W1-W5 window logic is orchestrator/onboarding-conversation
// territory, both out of this build's scope).
func (s *Server) APIMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/teams", s.auth(s.handleCreateTeam))
	mux.HandleFunc("/v1/roles", s.auth(s.handleCreateRole))
	mux.HandleFunc("/v1/people", s.auth(s.handleCreatePerson))
	mux.HandleFunc("/v1/agents", s.auth(s.handleCreateAgent))
	mux.HandleFunc("/v1/agents/close", s.auth(s.handleCloseAgent)) // added for osiris-orchestrator build's completion criterion 4 — see graphdb.CloseAIAgent doc comment
	mux.HandleFunc("/v1/memberships", s.auth(s.handleCreateMembership))

	// [2026-07-27] Org role model (60-identity-authn.md §20.6,
	// 20-ontology-schema.md §11 C10). These five carry organizational INTENT
	// and each bumps :OrgRevision by exactly 1 (C8).
	//
	// THEY DO NOT EVALUATE THE DELEGATION RULES. DG1~DG5 live in osiris-api's
	// authz.CheckDelegation and nowhere else (H31/HI16). What core adds on top
	// of the mutation is the in-transaction I14~I17 re-check — the wall that
	// still stands if osiris-api is compromised (60 §21.4-1). Reading the
	// route list as "core also checks permissions" is the misreading this
	// comment exists to prevent.
	//
	// byPartyId still arrives in the request body, exactly as it does for the
	// six routes above, and §6.5's rule applies to all of them equally: a
	// mutating request whose byPartyId is not a valid, active :Party is
	// refused. osiris-api derives that value from the session and never from
	// anything the client said (H19/H28, HI6).
	mux.HandleFunc("/v1/org-roles/assign", s.auth(s.handleAssignOrgRole))
	mux.HandleFunc("/v1/org-roles/revoke", s.auth(s.handleRevokeOrgRole))
	mux.HandleFunc("/v1/people/deactivate", s.auth(s.handleDeactivatePerson))
	mux.HandleFunc("/v1/parties/additive-profile", s.auth(s.handleSetAdditiveProfile))
	mux.HandleFunc("/v1/parties/deny", s.auth(s.handleSetDeny))
	mux.HandleFunc("/v1/assignment-policies", s.auth(s.handleCreateAssignmentPolicy))

	// [2026-08-12, P2 stage ②] :ActionType (80-action-layer.md §4, §9
	// AC1/AC4/AC9/AC10/AC15/AC19). Same layering note as the org-role block
	// above applies here even more sharply: THIS ROUTE DOES NOT CHECK
	// actiontype.write OR DG6~DG8. Those live in osiris-api's
	// authz.CheckDelegation. What this route adds is
	// graphdb.CreateActionType/UpdateActionType's own
	// whitelist/CA3/CA8/AC15/AC9/AC10/AC19 enforcement (plus, since
	// [2026-08-13], UpdateActionType's own fail-closed DG8 notification gate
	// — see actiontype.go) — the wall that still stands if osiris-api is
	// compromised (60 §21.4-1's argument, same as I14~I17 above).
	//
	// [2026-08-13] DG6~DG8 NOW EXIST in api/internal/authz/delegation.go —
	// this task's item 4. THIS ROUTE ITSELF IS STILL UNGATED BEYOND s.auth's
	// shared CORE_API_TOKEN, and that is judged CORRECT, not a leftover gap,
	// for the same reason every other /v1/* route in this file is: s.auth's
	// token is held ONLY by osiris-api, orchestrator, and core's own tests
	// (deploy/osiris/templates/{api,core,orchestrator}-deployment.yaml — NOT
	// injected into agent pods; see networkpolicy-agents.yaml's "osiris-
	// core:8090" row, which is REACHABLE but not CREDENTIALED for an agent).
	// A compromised agent can reach this port and gets 401, never 200 — the
	// same structural fact that already makes org-roles/people/etc. safe
	// without a second permission check here. Real enforcement therefore
	// still needs a PERSON-AUTHENTICATED caller, which means an osiris-api
	// route — and none exists yet (checked: no /api/action-types handler,
	// no corewrite.Client method for this path, as of this task). THAT is
	// the actual remaining gap, and it is an osiris-api-side gap, not a
	// core-side one: building POST /api/action-types (PersonOnly +
	// RequirePerm(actiontype.write) + authz.CheckDelegation with
	// Action=ActionEditActionType, computing EffAfterExceedsBefore/
	// GateRelaxedOnCallerAction/RelaxesActionType by diffing the proposed
	// :ActionType against graphread, then corewrite.Client.CreateActionType/
	// UpdateActionType) is handed off, unbuilt, to whichever worker owns the
	// console screen for action-type editing. See this task's final report.
	mux.HandleFunc("/v1/action-types", s.auth(s.handleCreateActionType))
	mux.HandleFunc("/v1/action-types/update", s.auth(s.handleUpdateActionType))
	mux.HandleFunc("/v1/invariants", s.auth(s.handleInvariants)) // read-only, still requires a token: internal org state
	mux.HandleFunc("/v1/revision", s.auth(s.handleRevision))

	// [2026-09-11, C-1] :Approval propose/list/decide —
	// docs/design/90-infra-topology.md §11 (IN17/IN18/IN19).
	//
	// Same layering note as :ActionType/org-roles above: THESE THREE ROUTES
	// DO NOT THEMSELVES DECIDE WHO MAY CALL THEM — that is osiris-api's job
	// (POST /_api/infra/proposals: RequirePerm(org.read.ops), reachable by a
	// machine principal per IN18's deliberate exception; POST
	// /_api/infra/proposals/{id}/decision: PersonOnly + RequirePerm(org.admin)
	// per IN19). s.auth's shared CORE_API_TOKEN gate below is held only by
	// osiris-api/orchestrator, identically to every other /v1/* route.
	//
	// handleCreateApproval deliberately does NOT call s.requireActor — IN18's
	// entire point is that a machine principal (which has no :Party) may
	// create a proposal. graphdb.CreateApproval enforces its own, weaker
	// rule instead (ProposedByRef required, ProposedByPartyID validated only
	// when non-empty) — see that function's doc comment.
	//
	// handleDecideApproval DOES call s.requireActor — IN19 restricts
	// deciding to a session-authenticated person, who always resolves to an
	// active :Party; graphdb.DecideApproval refuses an empty ByPartyID
	// outright as a second, independent wall.
	mux.HandleFunc("/v1/approvals", s.auth(s.handleCreateApproval))
	mux.HandleFunc("/v1/approvals/pending", s.auth(s.handleListPendingApprovals))
	mux.HandleFunc("/v1/approvals/decision", s.auth(s.handleDecideApproval))

	// ── [2026-07-28] The genesis principal — K7 (70-kubernetes-deployment.md §4.1) ──
	//
	// ★ THERE IS NO genesis-token ISSUE OR REVOKE ROUTE, AND THAT ABSENCE IS THE
	// DESIGN. K7: "genesis 자격증명을 OSiris가 만들지 않는다." K8s mints the
	// bootstrap-master Pod's projected ServiceAccount token; osiris-core only ever
	// VERIFIES a presented token (genesis.Guard.Verify → TokenReview). W3's
	// revocation is ServiceAccount deletion performed by orchestrator against the
	// K8s API — measured semantics in 70 §4.3 (immediate once the SA is gone, at
	// most 60s if a finalizer delays it, and a same-named recreation does NOT
	// resurrect the old token because the UID differs).
	//
	// An earlier pass kept these two as 410-Gone stubs because bin/lint-identity-
	// authn.sh's GEN(f) demanded they exist. That lint outlived its subject and was
	// rewritten (GEN(f1)~(f4)) to assert the opposite — that minting does NOT
	// exist. Keeping a dead route alive to satisfy a stale check is how a codebase
	// accumulates mechanisms nobody can remove.
	//
	// ★ THERE IS ALSO NO POST /v1/onboarding/commit. F14 removed it from the
	// genesis scope, and 30 §3.4.3 assigns the commit to the session of the human
	// who approved the draft — which reaches core through the ordinary
	// platform-token write routes above, called by osiris-api on that session's
	// behalf. The allowlist would refuse it anyway (default deny), but the clearer
	// statement is that the route does not exist.
	//
	// The three routes below are the complete list in genesis.AllowedRoutes;
	// genesisAuth authenticates via TokenReview.
	mux.HandleFunc("/v1/onboarding/draft", s.genesisAuth(s.handleValidateDraft))
	mux.HandleFunc("/v1/onboarding/status", s.genesisAuth(s.handleGenesisStatus))
	mux.HandleFunc("/v1/onboarding/catalog", s.genesisAuth(s.handleGenesisCatalog))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIToken == "" {
			// Explicit fail-closed: refuse to serve the write API with no
			// token configured rather than silently allowing everything.
			writeErr(w, http.StatusServiceUnavailable, "API token not configured")
			return
		}
		got := r.Header.Get("Authorization")
		want := "Bearer " + s.cfg.APIToken
		if got != want {
			writeErr(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		next(w, r)
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func statusForErr(err error) int {
	if errors.Is(err, graphdb.ErrValidation) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// --- request bodies --------------------------------------------------------

type reqCapability struct {
	Code     string  `json:"code"`
	MinLevel int     `json:"minLevel,omitempty"`
	Weight   float64 `json:"weight,omitempty"`
	Level    int     `json:"level,omitempty"`
	Source   string  `json:"source,omitempty"`
}

type teamReq struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Slug             string  `json:"slug"`
	ChannelSubject   string  `json:"channelSubject"`
	MonthlyBudgetUsd float64 `json:"monthlyBudgetUsd"`
	ParentTeamID     string  `json:"parentTeamId"`
	ByPartyID        string  `json:"byPartyId"`
}

func (s *Server) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	var req teamReq
	if !decode(w, r, &req) {
		return
	}
	res, err := s.db.CreateTeam(r.Context(), graphdb.TeamInput{
		ID: req.ID, Name: req.Name, Slug: req.Slug, ChannelSubject: req.ChannelSubject,
		MonthlyBudgetUsd: req.MonthlyBudgetUsd, ParentTeamID: req.ParentTeamID, ByPartyID: req.ByPartyID,
	})
	s.respondWrite(w, r.Context(), res, err, "team_upserted")
}

type roleReq struct {
	ID                      string          `json:"id"`
	Title                   string          `json:"title"`
	Level                   int             `json:"level"`
	DefaultTeamID           string          `json:"defaultTeamId"`
	IsLead                  bool            `json:"isLead"`
	ResponsibilitiesSummary string          `json:"responsibilitiesSummary"`
	PromptTemplateMd        string          `json:"promptTemplateMd"`
	RequiredCapabilities    []reqCapability `json:"requiredCapabilities"`
	BasePermissionProfileID string          `json:"basePermissionProfileId"`
	ByPartyID               string          `json:"byPartyId"`
}

func (s *Server) handleCreateRole(w http.ResponseWriter, r *http.Request) {
	var req roleReq
	if !decode(w, r, &req) {
		return
	}
	var caps []graphdb.RequiredCapability
	for _, c := range req.RequiredCapabilities {
		caps = append(caps, graphdb.RequiredCapability{Code: c.Code, MinLevel: c.MinLevel, Weight: c.Weight})
	}
	res, err := s.db.CreateRole(r.Context(), graphdb.RoleInput{
		ID: req.ID, Title: req.Title, Level: req.Level, DefaultTeamID: req.DefaultTeamID,
		IsLead: req.IsLead, ResponsibilitiesSummary: req.ResponsibilitiesSummary,
		PromptTemplateMd: req.PromptTemplateMd, RequiredCapabilities: caps,
		BasePermissionProfileID: req.BasePermissionProfileID, ByPartyID: req.ByPartyID,
	})
	s.respondWrite(w, r.Context(), res, err, "role_upserted")
}

type personReq struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Email          string          `json:"email"`
	Timezone       string          `json:"timezone"`
	WorkingHours   string          `json:"workingHours"`
	Availability   string          `json:"availability"`
	NotifyChannels []string        `json:"notifyChannels"`
	MaxTaskPoints  int             `json:"maxTaskPoints"`
	Capabilities   []reqCapability `json:"capabilities"`
	ByPartyID      string          `json:"byPartyId"`
}

func (s *Server) handleCreatePerson(w http.ResponseWriter, r *http.Request) {
	var req personReq
	if !decode(w, r, &req) {
		return
	}
	var caps []graphdb.HeldCapability
	for _, c := range req.Capabilities {
		caps = append(caps, graphdb.HeldCapability{Code: c.Code, Level: c.Level, Source: c.Source})
	}
	res, err := s.db.CreatePerson(r.Context(), graphdb.PersonInput{
		ID: req.ID, Name: req.Name, Email: req.Email, Timezone: req.Timezone,
		WorkingHours: req.WorkingHours, Availability: req.Availability,
		NotifyChannels: req.NotifyChannels, MaxTaskPoints: req.MaxTaskPoints,
		Capabilities: caps, ByPartyID: req.ByPartyID,
	})
	s.respondWrite(w, r.Context(), res, err, "person_upserted")
}

type agentReq struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	RuntimeClass  string          `json:"runtimeClass"`
	ModelAlias    string          `json:"modelAlias"`
	Image         string          `json:"image"`
	MaxTaskPoints int             `json:"maxTaskPoints"`
	DrainSeconds  int             `json:"drainSeconds"`
	DesiredState  string          `json:"desiredState"`
	IsMaster      bool            `json:"isMaster"`
	Capabilities  []reqCapability `json:"capabilities"`
	ByPartyID     string          `json:"byPartyId"`
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req agentReq
	if !decode(w, r, &req) {
		return
	}
	var caps []graphdb.HeldCapability
	for _, c := range req.Capabilities {
		caps = append(caps, graphdb.HeldCapability{Code: c.Code, Level: c.Level, Source: c.Source})
	}
	res, err := s.db.CreateAIAgent(r.Context(), graphdb.AgentInput{
		ID: req.ID, Name: req.Name, RuntimeClass: req.RuntimeClass, ModelAlias: req.ModelAlias,
		Image: req.Image, MaxTaskPoints: req.MaxTaskPoints, DrainSeconds: req.DrainSeconds,
		DesiredState: req.DesiredState, IsMaster: req.IsMaster, Capabilities: caps, ByPartyID: req.ByPartyID,
	})
	s.respondWrite(w, r.Context(), res, err, "agent_upserted")
}

type closeAgentReq struct {
	ID        string `json:"id"`
	ByPartyID string `json:"byPartyId"`
}

func (s *Server) handleCloseAgent(w http.ResponseWriter, r *http.Request) {
	var req closeAgentReq
	if !decode(w, r, &req) {
		return
	}
	res, err := s.db.CloseAIAgent(r.Context(), graphdb.CloseAgentInput{ID: req.ID, ByPartyID: req.ByPartyID})
	s.respondWrite(w, r.Context(), res, err, "agent_closed")
}

type membershipReq struct {
	ID            string `json:"id"`
	PartyID       string `json:"partyId"`
	TeamID        string `json:"teamId"`
	RoleID        string `json:"roleId"`
	IsHead        bool   `json:"isHead"`
	AllocationPct int    `json:"allocationPct"`
	ByPartyID     string `json:"byPartyId"`
}

func (s *Server) handleCreateMembership(w http.ResponseWriter, r *http.Request) {
	var req membershipReq
	if !decode(w, r, &req) {
		return
	}
	if req.AllocationPct == 0 {
		req.AllocationPct = 100
	}
	res, err := s.db.CreateMembership(r.Context(), graphdb.MembershipInput{
		ID: req.ID, PartyID: req.PartyID, TeamID: req.TeamID, RoleID: req.RoleID,
		IsHead: req.IsHead, AllocationPct: req.AllocationPct, ByPartyID: req.ByPartyID,
	})
	s.respondWrite(w, r.Context(), res, err, "membership_upserted")
}

type policyReq struct {
	Version                   int      `json:"version"`
	TieBreakChain             []string `json:"tieBreakChain"`
	TeamStrict                bool     `json:"teamStrict"`
	LevelRelaxationAllowed    bool     `json:"levelRelaxationAllowed"`
	MaxCandidatesForDecompose int      `json:"maxCandidatesForDecompose"`
	ByPartyID                 string   `json:"byPartyId"`
}

func (s *Server) handleCreateAssignmentPolicy(w http.ResponseWriter, r *http.Request) {
	var req policyReq
	if !decode(w, r, &req) {
		return
	}
	if req.MaxCandidatesForDecompose == 0 {
		req.MaxCandidatesForDecompose = 2
	}
	res, err := s.db.CreateAssignmentPolicy(r.Context(), graphdb.AssignmentPolicyInput{
		Version: req.Version, TieBreakChain: req.TieBreakChain, TeamStrict: req.TeamStrict,
		LevelRelaxationAllowed:    req.LevelRelaxationAllowed,
		MaxCandidatesForDecompose: req.MaxCandidatesForDecompose, ByPartyID: req.ByPartyID,
	})
	s.respondWrite(w, r.Context(), res, err, "policy_upserted")
}

// --- [2026-08-12, P2 stage ②] :ActionType ---------------------------------
//
// composedOfReq's json tags are deliberately "childActionTypeId"/"ord" ONLY
// — AC10 enforcement layer 0 (before even graphdb.ComposedOfInput's own
// single-field shape): there is no third key here to decode a stray
// relationship property into, and decode()'s DisallowUnknownFields (below)
// rejects one anyway if a caller tries.
type composedOfReq struct {
	ChildActionTypeID string `json:"childActionTypeId"`
	Ord               int    `json:"ord"`
}

// actionTypeReq's json tags are 80 §4.2's whitelist, verbatim, one field
// each — this is AC4's enforcement layer 0 (see graphdb/actiontype.go's doc
// comment for layers 1/2). decode() below already calls
// json.Decoder.DisallowUnknownFields() (see that function, this file) for
// every write route in this package; an HTTP body naming a property outside
// this list — including any of §4.2's explicitly-absent
// command/script/template/url/endpoint/handlerCode/expr/condition/webhook —
// is REJECTED at decode time (400), not silently dropped. catalogVersion,
// createdAtRevision, and closedAtRevision are deliberately NOT fields here:
// see graphdb.ActionTypeInput's doc comment for why those are system-
// managed, not client-supplied.
type actionTypeReq struct {
	ID            string `json:"id"`
	Code          string `json:"code"`
	Label         string `json:"label"`
	DescriptionMd string `json:"descriptionMd"`

	Origin string `json:"origin"`
	Status string `json:"status"`

	ParametersSchemaJSON  string `json:"parametersSchemaJson"`
	ParameterDefaultsJSON string `json:"parameterDefaultsJson"`

	ValidationPredicates []string `json:"validationPredicates"`
	Effects              []string `json:"effects"`

	EffectClass  string `json:"effectClass"`
	GateClass    string `json:"gateClass"`
	ApproverRole string `json:"approverRole"`
	LimitJSON    string `json:"limitJson"`

	TaintClass    string `json:"taintClass"`
	Reversibility string `json:"reversibility"`

	RequiresCapability []string `json:"requiresCapability"`

	DerivedHash string `json:"derivedHash"`
	IRI         string `json:"iri"`

	// Relationships (§4.3) — not node properties, not part of §4.2's
	// whitelist, but still fixed, named fields (same "no field to smuggle a
	// stray key into" argument).
	RequiresPermissionCodes []string        `json:"requiresPermissionCodes"`
	ComposedOf              []composedOfReq `json:"composedOf"`

	ByPartyID string `json:"byPartyId"`
}

func (in actionTypeReq) toGraphdbInput() graphdb.ActionTypeInput {
	var composed []graphdb.ComposedOfInput
	for _, c := range in.ComposedOf {
		composed = append(composed, graphdb.ComposedOfInput{ChildActionTypeID: c.ChildActionTypeID, Ord: c.Ord})
	}
	return graphdb.ActionTypeInput{
		ID: in.ID, Code: in.Code, Label: in.Label, DescriptionMd: in.DescriptionMd,
		Origin: in.Origin, Status: in.Status,
		ParametersSchemaJSON: in.ParametersSchemaJSON, ParameterDefaultsJSON: in.ParameterDefaultsJSON,
		ValidationPredicates: in.ValidationPredicates, Effects: in.Effects,
		EffectClass: in.EffectClass, GateClass: in.GateClass, ApproverRole: in.ApproverRole, LimitJSON: in.LimitJSON,
		TaintClass: in.TaintClass, Reversibility: in.Reversibility,
		RequiresCapability:      in.RequiresCapability,
		DerivedHash:             in.DerivedHash,
		IRI:                     in.IRI,
		RequiresPermissionCodes: in.RequiresPermissionCodes,
		ComposedOf:              composed,
		ByPartyID:               in.ByPartyID,
	}
}

// knownPredicateSet/knownEffectSet call the compiled-in registries fresh on
// every request rather than caching them on *Server — both are cheap, pure,
// sorted-slice-to-map conversions (see predicate.Names()/effect.Names()'s
// own doc comments: "run once at package init and never written to again"
// for the underlying registry; building the lookup set per-request costs a
// few allocations, not a query), and this avoids adding fields to Server
// (whose constructor, NewServer, this task must not change the signature
// of).
func knownPredicateSet() map[string]struct{} {
	out := make(map[string]struct{})
	for _, n := range predicate.Names() {
		out[n] = struct{}{}
	}
	return out
}

func knownEffectSet() map[string]struct{} {
	out := make(map[string]struct{})
	for _, n := range effect.Names() {
		out[n] = struct{}{}
	}
	return out
}

func (s *Server) handleCreateActionType(w http.ResponseWriter, r *http.Request) {
	var req actionTypeReq
	if !decode(w, r, &req) {
		return
	}
	if !s.requireActor(w, r, req.ByPartyID) {
		return
	}
	res, err := s.db.CreateActionType(r.Context(), req.toGraphdbInput(), s.cfg.CatalogVersion, knownPredicateSet(), knownEffectSet())
	s.respondWrite(w, r.Context(), res, err, "actiontype_created")
}

func (s *Server) handleUpdateActionType(w http.ResponseWriter, r *http.Request) {
	var req actionTypeReq
	if !decode(w, r, &req) {
		return
	}
	if !s.requireActor(w, r, req.ByPartyID) {
		return
	}
	res, err := s.db.UpdateActionType(r.Context(), req.toGraphdbInput(), s.cfg.CatalogVersion, knownPredicateSet(), knownEffectSet())
	s.respondWrite(w, r.Context(), res, err, "actiontype_updated")
}

func (s *Server) handleInvariants(w http.ResponseWriter, r *http.Request) {
	report, err := graphdb.CheckInvariants(r.Context(), s.db, s.cfg.CatalogVersion)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleRevision(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.CurrentRevision(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"revision": n})
}

// --- [2026-09-11, C-1] :Approval propose/list/decide ----------------------
//
// docs/design/90-infra-topology.md §11 IN17/IN18/IN19. See the route
// registration comment above (APIMux) for the auth layering, and
// writeops_approval.go for the actual graph semantics these three handlers
// are thin wrappers over.

type approvalReq struct {
	ID                string `json:"id"`
	ActionTypeID      string `json:"actionTypeId"`
	TargetJSON        string `json:"targetJson"`
	PayloadJSON       string `json:"payloadJson"`
	ProposedByPartyID string `json:"proposedByPartyId"` // optional — empty for a machine-principal proposer (IN18)
	ProposedByRef     string `json:"proposedByRef"`
}

func (s *Server) handleCreateApproval(w http.ResponseWriter, r *http.Request) {
	var req approvalReq
	if !decode(w, r, &req) {
		return
	}
	// NOTE: no s.requireActor call here — see APIMux's route comment (IN18).
	res, err := s.db.CreateApproval(r.Context(), graphdb.ApprovalInput{
		ID: req.ID, ActionTypeID: req.ActionTypeID,
		TargetJSON: req.TargetJSON, PayloadJSON: req.PayloadJSON,
		ProposedByPartyID: req.ProposedByPartyID, ProposedByRef: req.ProposedByRef,
	})
	s.respondWrite(w, r.Context(), res, err, "approval_proposed")
}

func (s *Server) handleListPendingApprovals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	list, err := s.db.ListPendingApprovals(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposals": list})
}

type approvalDecisionReq struct {
	ID           string `json:"id"`
	Decision     string `json:"decision"` // "approved" | "rejected"
	Comment      string `json:"comment"`
	ByPartyID    string `json:"byPartyId"`
	DecidedByRef string `json:"decidedByRef"`
}

func (s *Server) handleDecideApproval(w http.ResponseWriter, r *http.Request) {
	var req approvalDecisionReq
	if !decode(w, r, &req) {
		return
	}
	if !s.requireActor(w, r, req.ByPartyID) {
		return
	}
	res, err := s.db.DecideApproval(r.Context(), graphdb.DecisionInput{
		ID: req.ID, Decision: req.Decision, Comment: req.Comment,
		ByPartyID: req.ByPartyID, DecidedByRef: req.DecidedByRef,
	})
	s.respondWrite(w, r.Context(), res, err, "approval_decided")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return false
	}
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

// respondWrite is the common tail of every write handler: on success it
// publishes the revision-bump event (AFTER the Neo4j commit already
// happened inside s.db.Create*) and returns the new revision number; on
// failure it maps graphdb.ErrValidation to 400 and everything else to 500.
// A publish failure here does not roll back the already-committed graph
// write — see IMPLEMENTATION-NOTES.md for why that's an accepted gap.
func (s *Server) respondWrite(w http.ResponseWriter, ctx context.Context, res graphdb.WriteResult, err error, changeKind string) {
	if err != nil {
		writeErr(w, statusForErr(err), err.Error())
		return
	}
	if pubErr := s.pub.PublishRevisionBump(ctx, events.RevisionEvent{
		Revision:   res.Revision,
		At:         time.Now().UTC().Format(time.RFC3339),
		ChangeKind: changeKind,
	}); pubErr != nil {
		log.Printf("warning: committed revision %d but failed to publish event: %v", res.Revision, pubErr)
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": res.ID, "revision": res.Revision})
}

// --- [2026-07-27] org role model handlers ----------------------------------
//
// §6.5's fail-closed rule, applied uniformly: every one of these requires a
// byPartyId that resolves to an active :Party. requireActor is the single
// place that check lives, so a sixth handler cannot forget it.
//
// WHY THIS CHECK IS HERE AND NOT ONLY IN osiris-api. 60 §19.7's diagram calls
// it "the second wall": osiris-api's PersonOnly middleware stops a machine
// principal at the router, and this stops anything that gets past it, because
// a machine principal has no partyId to send in the first place. Two layers,
// both pre-existing rules, neither invented for this task.

func (s *Server) requireActor(w http.ResponseWriter, r *http.Request, byPartyID string) bool {
	if byPartyID == "" {
		writeErr(w, http.StatusBadRequest, "byPartyId is required (60 §6.5: a mutating request must carry a valid :Party actor)")
		return false
	}
	ok, err := s.db.PartyIsActive(r.Context(), byPartyID)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot verify actor party: "+err.Error())
		return false
	}
	if !ok {
		writeErr(w, http.StatusForbidden, "byPartyId is not an active :Party")
		return false
	}
	return true
}

type orgRoleAssignReq struct {
	MembershipID string `json:"membershipId"`
	PersonID     string `json:"personId"`
	RoleID       string `json:"roleId"`
	ByPartyID    string `json:"byPartyId"`
}

func (s *Server) handleAssignOrgRole(w http.ResponseWriter, r *http.Request) {
	var req orgRoleAssignReq
	if !decode(w, r, &req) {
		return
	}
	if !s.requireActor(w, r, req.ByPartyID) {
		return
	}
	res, err := s.db.AssignOrgRole(r.Context(), graphdb.OrgRoleInput{
		MembershipID: req.MembershipID, PersonID: req.PersonID,
		RoleID: req.RoleID, ByPartyID: req.ByPartyID,
	})
	s.respondWrite(w, r.Context(), res, err, "org_role_assigned")
}

type orgRoleRevokeReq struct {
	PersonID  string `json:"personId"`
	ByPartyID string `json:"byPartyId"`
}

func (s *Server) handleRevokeOrgRole(w http.ResponseWriter, r *http.Request) {
	var req orgRoleRevokeReq
	if !decode(w, r, &req) {
		return
	}
	if !s.requireActor(w, r, req.ByPartyID) {
		return
	}
	res, err := s.db.RevokeOrgRole(r.Context(), graphdb.RevokeOrgRoleInput{
		PersonID: req.PersonID, ByPartyID: req.ByPartyID,
	})
	s.respondWrite(w, r.Context(), res, err, "org_role_revoked")
}

type deactivatePersonReq struct {
	PersonID  string `json:"personId"`
	ByPartyID string `json:"byPartyId"`
}

func (s *Server) handleDeactivatePerson(w http.ResponseWriter, r *http.Request) {
	var req deactivatePersonReq
	if !decode(w, r, &req) {
		return
	}
	if !s.requireActor(w, r, req.ByPartyID) {
		return
	}
	res, err := s.db.DeactivatePerson(r.Context(), graphdb.DeactivatePersonInput{
		PersonID: req.PersonID, ByPartyID: req.ByPartyID,
	})
	s.respondWrite(w, r.Context(), res, err, "person_deactivated")
}

type partyGrantReq struct {
	PartyID   string `json:"partyId"`
	ProfileID string `json:"profileId"`
	PermCode  string `json:"permCode"`
	Attach    bool   `json:"attach"`
	ByPartyID string `json:"byPartyId"`
}

func (s *Server) handleSetAdditiveProfile(w http.ResponseWriter, r *http.Request) {
	var req partyGrantReq
	if !decode(w, r, &req) {
		return
	}
	if !s.requireActor(w, r, req.ByPartyID) {
		return
	}
	res, err := s.db.SetAdditiveProfile(r.Context(), graphdb.PartyGrantInput{
		PartyID: req.PartyID, ProfileID: req.ProfileID, Attach: req.Attach, ByPartyID: req.ByPartyID,
	})
	s.respondWrite(w, r.Context(), res, err, "additive_profile_changed")
}

func (s *Server) handleSetDeny(w http.ResponseWriter, r *http.Request) {
	var req partyGrantReq
	if !decode(w, r, &req) {
		return
	}
	if !s.requireActor(w, r, req.ByPartyID) {
		return
	}
	res, err := s.db.SetDeny(r.Context(), graphdb.PartyGrantInput{
		PartyID: req.PartyID, PermCode: req.PermCode, Attach: req.Attach, ByPartyID: req.ByPartyID,
	})
	s.respondWrite(w, r.Context(), res, err, "deny_changed")
}
