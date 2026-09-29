// Package api wires osiris-api's two listeners (health + main), following
// osiris-core's internal/api/server.go split for the same reason (§5.4):
// a slow SSE connection or a stalled subprocess call must never make health
// probes queue behind it.
package api

import (
	"net/http"

	"osiris-api/internal/authn"
	"osiris-api/internal/authz"
	"osiris-api/internal/events"
	"osiris-api/internal/health"
	"osiris-api/internal/infra"
	"osiris-api/internal/machineauth"
	"osiris-api/internal/onboarding"
	"osiris-api/internal/people"
	"osiris-api/internal/providers"
)

type Server struct {
	health *health.State
	sse    *events.Hub
	ob     *onboarding.Service
	guard  *authn.Guard
	mp     *machineauth.Handlers
	ppl    *people.Service
	infra  *infra.Service
	prov   *providers.Service
}

func NewServer(h *health.State, sse *events.Hub, ob *onboarding.Service, guard *authn.Guard, mp *machineauth.Handlers, ppl *people.Service, infraSvc *infra.Service, provSvc *providers.Service) *Server {
	return &Server{health: h, sse: sse, ob: ob, guard: guard, mp: mp, ppl: ppl, infra: infraSvc, prov: provSvc}
}

// HealthMux serves ONLY /livez and /readyz — see osiris-core's identical
// convention (core/internal/api/server.go's HealthMux doc comment).
func (s *Server) HealthMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", s.health.Livez)
	mux.HandleFunc("/readyz", s.health.Readyz)
	return mux
}

// APIMux serves osiris-api's own console-facing surface. Every route here
// is implemented under the LITERAL path `/api/*` — 10-core-architecture.md
// §2.9's I5 3차 수정 moved the PUBLIC path to `/_api/*`, but explicitly left
// osiris-api's own internal implementation on `/api/*` unchanged (osiris-gw
// rewrites `/_api/*` -> `/api/*` before proxying here; see
// gw/conf.d/default.conf's `location /_api/`). Do not rename these routes to
// `/_api/*` — that would break the gateway's rewrite contract, not honor it.
//
// ─────────────────────────────────────────────────────────────────────────
// THIS TABLE IS A SECURITY ARTIFACT. READ THE NEXT FOUR PARAGRAPHS BEFORE
// ADDING A ROUTE.
// ─────────────────────────────────────────────────────────────────────────
//
// WHY EVERY PATTERN CARRIES AN HTTP METHOD. 60-identity-authn.md §19.6 says a
// machine principal "절대 못 한다 (구조적으로)" mutate anything, and names the
// mechanism: "라우터가 non-GET 라우트에 personOnly 미들웨어를 강제(HI15)".
// HI15 is specified as a STATIC check over the route registration table.
//
// Until this build, that check had nothing to inspect. Routes were registered
// as bare paths and the method was tested inside the handler:
//
//	mux.HandleFunc("/api/onboarding/step0/providers", s.guard.Require(s.ob.Providers))
//	  → func Providers(...) { if r.Method != http.MethodPost { 405 } … }
//
// "Non-GET route" was therefore not a property of the routing table at all,
// and a lint had no object to judge. The previous design worker recorded this
// as [미검증] for exactly that reason. Go 1.22's ServeMux accepts method
// patterns ("POST /path"), which turns the method back into a static property
// of the table — so HI15 becomes both enforceable and lintable, and a new
// handler that forgets its own method check can no longer silently widen the
// surface. api/go.mod is already go 1.22.
//
// MEASURED, NOT ASSUMED — the two behaviors that could have regressed:
//
//   - Wrong method on a registered path still yields 405, not 404. Go 1.22's
//     ServeMux, when the path matches a pattern but the method does not,
//     responds 405 with an Allow header. Verified against the running stack:
//     GET on the POST-only /step0/providers returned 405 before this change
//     (handler check) and 405 after (router).
//   - No path in this service accepts more than one method. Checked
//     exhaustively against the console's client (console/src/api/*.ts) and
//     every handler in internal/onboarding — each path is single-method.
//
// Two paths DID change behavior, and both changes are strict corrections of
// latent bugs, recorded in IMPLEMENTATION-NOTES.md: the read-only GETs
// (/onboarding/status, /step0/status, /step0/alias-defaults) and the two
// method-checkless POSTs (/step0/gateway-token, /step0/verify-gateway) had NO
// method check at all, so e.g. `PUT /api/onboarding/status` used to return
// 200. They now return 405.
//
// THE DUPLICATE r.Method CHECKS INSIDE HANDLERS ARE KEPT. internal/onboarding's
// decode() still refuses non-POST. That is now unreachable for these routes,
// and it stays anyway: a handler must not be correct only by virtue of where
// it happens to be registered, and the cost is one comparison. HI15's point is
// that the handler check is not the GUARANTEE — not that it is harmful.
//
// AUTH, per IMPLEMENTATION-NOTES.md §102.2's three branches:
//
//	(a)+(b) operator — session cookie OR the legacy Bearer setup token. Both
//	        still work, unchanged; H10's rename/retirement is the next task.
//	(c)     machine principal — Bearer osm_…, org.read.* only (60 §19).
//
// The middleware vocabulary is exactly three names, and which one a route gets
// is not a matter of taste:
//
//	RequirePerm(code, h)   authenticate + authz.Authorize. The only routes a
//	                       machine principal can reach. GET only.
//	PersonOnly(h)          authenticate + refuse machine principals. REQUIRED
//	                       on every non-GET route (HI15). Also used on the two
//	                       GETs that must stay human-only.
//	PersonOnlyPublic(h)    no authentication (the login/logout pair) but still
//	                       refuses a machine principal's bearer.
func (s *Server) APIMux() *http.ServeMux {
	mux := http.NewServeMux()

	// /api/auth/* — §19.6 row ②: a machine principal may not create a session,
	// set a credential, or redeem a ticket. Unauthenticated by necessity
	// (a fresh browser has nothing yet), machine-refusing by rule.
	mux.HandleFunc("POST /api/auth/session", s.guard.PersonOnlyPublic(s.guard.Exchange))
	mux.HandleFunc("POST /api/auth/logout", s.guard.PersonOnlyPublic(s.guard.Logout))

	// Reads — (b) or (c). IMPLEMENTATION-NOTES.md §102.3 lists exactly these
	// three as machine-reachable, and §19.10 maps them to §81's completion
	// conditions #2 and #4. org.read.onboarding is §20.4's code for it.
	mux.HandleFunc("GET /api/onboarding/status", s.guard.RequirePerm(authz.PermOrgReadOnboarding, s.ob.Status))
	mux.HandleFunc("GET /api/onboarding/step0/status", s.guard.RequirePerm(authz.PermOrgReadOnboarding, s.ob.Step0Status))
	mux.HandleFunc("GET /api/onboarding/step0/alias-defaults", s.guard.RequirePerm(authz.PermOrgReadOnboarding, s.ob.AliasDefaults))
	mux.HandleFunc("GET /api/omniroute/console", s.guard.RequirePerm(authz.PermOrgReadOnboarding, s.ob.OmnirouteConsole))

	// Writes — (b) only. §102.3: "나머지 6개(POST 전부) — (b) 전용. 쓰기이므로
	// 기계 주체는 구조적으로 부를 수 없다." The structure IS these five lines:
	// the method is in the pattern, and the pattern's handler is PersonOnly.
	mux.HandleFunc("POST /api/onboarding/step0/omniroute-admin", s.guard.PersonOnly(s.ob.OmniouteAdmin))
	mux.HandleFunc("POST /api/onboarding/step0/providers", s.guard.PersonOnly(s.ob.Providers))
	mux.HandleFunc("POST /api/onboarding/step0/aliases", s.guard.PersonOnly(s.ob.Aliases))
	mux.HandleFunc("POST /api/onboarding/step0/gateway-token", s.guard.PersonOnly(s.ob.GatewayToken))
	mux.HandleFunc("POST /api/onboarding/step0/verify-gateway", s.guard.PersonOnly(s.ob.VerifyGateway))

	// SSE — (b) or (c), gated on org.read.ops.
	//
	// §19.6 lists "GET /api/events SSE 구독(§81 #5)" among what a machine
	// principal must be able to do, and §20.4 defines org.read.ops as
	// "운영 상태 읽기 — 에이전트 헬스, 클러스터, 이벤트 스트림, Ops 화면" —
	// the event stream is named in the code's own definition.
	//
	// IMPLEMENTATION-NOTES.md §102.3 had classified this route "(b) 전용" on
	// the grounds that a browser EventSource cannot set custom headers. That
	// premise is true but proves only that BROWSERS must use the cookie; it
	// does not exclude non-browser clients, which can and do send an
	// Authorization header. The supervisor withdrew the classification;
	// §104 records the correction and its argument. With this route open to
	// (c), the last unmet row of §19.10's replacement-path table (§81 #5) is
	// filled.
	//
	// RequirePermStreaming, not RequirePerm: this is the one long-lived
	// response in the service, so the credential is re-checked every 30s for
	// as long as the stream is open and the stream is CLOSED when it stops
	// being valid. Without that, MP5's "즉시 리보크" would be false for the
	// only endpoint where a stale credential keeps receiving data — and it
	// already was, for person sessions, before this build (measured; see that
	// middleware's doc comment).
	mux.HandleFunc("GET /api/events", s.guard.RequirePermStreaming(authz.PermOrgReadOps, s.sse.ServeHTTP))

	// Machine principal lifecycle (§19.5) — all three PersonOnly, including
	// the GET. H27: a machine principal can neither issue, rotate, nor revoke
	// a machine principal, its own included. See internal/machineauth's
	// handler file for why the read is gated this way too.
	mux.HandleFunc("POST /api/machine-principals", s.guard.PersonOnly(s.mp.Issue))
	mux.HandleFunc("GET /api/machine-principals", s.guard.PersonOnly(s.mp.List))
	mux.HandleFunc("DELETE /api/machine-principals/{id}", s.guard.PersonOnly(s.mp.Revoke))

	// §19.6 lists GET /whoami among what a machine principal must be able to
	// call. Reports only what the server decided about this request.
	mux.HandleFunc("GET /api/whoami", s.guard.RequireAuthenticated(s.guard.Whoami))

	// ── [2026-07-27] The org role model — 60 §20.6, console screen #10 ────
	//
	// GET /api/me is PersonOnly, not RequirePerm: §3.5 says a machine
	// principal gets 403 machine_principal_not_a_person here, because /me is
	// about a person's identity and screens and a machine principal has
	// neither. GET /api/whoami above is what a machine principal calls
	// instead, and its response schema HAS NO partyId field.
	mux.HandleFunc("GET /api/me", s.guard.PersonOnly(s.ppl.Me))

	// POST /api/me/password — the self-service password change §5.6 state C
	// names and authncred's package doc had flagged as absent. PersonOnly for
	// the same reason as GET /api/me above (a machine principal has no
	// password), and it is a non-GET route so HI15 requires PersonOnly here
	// regardless.
	mux.HandleFunc("POST /api/me/password", s.guard.PersonOnly(s.guard.ChangePassword))

	// The two reads behind screen #10. Gated on the MANAGEMENT codes, not on
	// org.read.*, for §19.6 row ④'s reason: credential state ("has this person
	// enrolled yet") is org.manage.people territory and must not be reachable
	// by a read-only machine principal. Both are PersonOnly on top of the
	// permission gate — belt and braces on a route that lists who can be
	// taken over.
	mux.HandleFunc("GET /api/people", s.guard.PersonOnly(s.guard.RequireAnyPerm(
		[]authz.Code{authz.PermOrgManagePeople, authz.PermOrgManageRoles}, s.ppl.List)))
	mux.HandleFunc("GET /api/org-roles", s.guard.PersonOnly(s.guard.RequireAnyPerm(
		[]authz.Code{authz.PermOrgManagePeople, authz.PermOrgManageRoles}, s.ppl.ListOrgRoles)))

	// ── The mutating surface. ────────────────────────────────────────────
	//
	// EVERY ONE IS PersonOnly (HI15 — the router refuses machine principals on
	// non-GET routes) AND carries a permission gate AND calls
	// authz.CheckDelegation inside the handler (HI16). The three layers answer
	// three different questions and none substitutes for another:
	//
	//	PersonOnly     what KIND of principal is this        (structural)
	//	RequirePerm    may this actor do this AT ALL          (capability)
	//	CheckDelegation may this actor do it TO THAT TARGET    (delegation)
	//
	// The third is the one 60 §21 is about, and it is the only one that can
	// answer "no" to an administrator asking about another administrator.
	//
	// ★ NOTE THE GATE ON additive-profile AND deny: org.manage.roles, NOT
	// org.admin. Gating them on org.admin would make a manager's attempt fail
	// at the ROUTER, which looks identical from outside but means DG2 never
	// runs on that path — and the day someone widens the gate the hole opens
	// silently. Letting the request reach CheckDelegation makes the refusal a
	// property of the delegation rules, which is what §21.4-2 requires
	// (DENIES manipulation is inside the DG perimeter, not outside it).
	mux.HandleFunc("POST /api/people", s.guard.PersonOnly(
		s.guard.RequirePermNoAuth(authz.PermOrgManagePeople, s.ppl.Create)))
	mux.HandleFunc("POST /api/people/{id}/org-role", s.guard.PersonOnly(
		s.guard.RequirePermNoAuth(authz.PermOrgManageRoles, s.ppl.AssignRole)))
	mux.HandleFunc("DELETE /api/people/{id}/org-role", s.guard.PersonOnly(
		s.guard.RequirePermNoAuth(authz.PermOrgManageRoles, s.ppl.RevokeRole)))
	mux.HandleFunc("POST /api/people/{id}/deactivate", s.guard.PersonOnly(
		s.guard.RequirePermNoAuth(authz.PermOrgManagePeople, s.ppl.Deactivate)))
	mux.HandleFunc("POST /api/people/{id}/enrollment-ticket", s.guard.PersonOnly(
		s.guard.RequirePermNoAuth(authz.PermOrgManagePeople, s.ppl.IssueTicket)))
	mux.HandleFunc("POST /api/people/{id}/additive-profile", s.guard.PersonOnly(
		s.guard.RequirePermNoAuth(authz.PermOrgManageRoles, s.ppl.SetAdditiveProfile)))
	mux.HandleFunc("POST /api/people/{id}/deny", s.guard.PersonOnly(
		s.guard.RequirePermNoAuth(authz.PermOrgManageRoles, s.ppl.SetDeny)))

	// The bootstrap handover (§4.1 ④-⑥). Master-key session only, and refused
	// forever once an active org.admin holder exists — a predicate over the
	// world re-evaluated per request, not a burn flag (W1/MP1's shape). See
	// people.BootstrapInitialAdmin's doc comment for why an organization
	// cannot otherwise acquire its first administrator in this build.
	mux.HandleFunc("POST /api/onboarding/initial-admin", s.guard.PersonOnly(s.ppl.BootstrapInitialAdmin))

	// ── Infra topology (docs/design/90-infra-topology.md §7, A/B단계) ──────
	//
	// All five GET, gated on org.read.ops via RequirePerm — reachable by (b)
	// a person session (console) AND (c) a machine principal (the
	// mcp__infra__* read tools, §7.1/§7.2 B단계). This is a NEW read surface
	// with its own RBAC story (deploy/osiris/templates/rbac-api.yaml, §4.3).
	// IN4: read-only, no create/update/patch/delete anywhere behind these
	// five handlers (internal/infra, internal/k8sread).
	//
	// A단계 had these PersonOnly, with the router comment noting "extending
	// cluster topology reads to machine principals is a separate decision
	// this build does not make." B단계 makes that decision: org.read.ops is
	// already defined as "operational read: agent health, cluster, event
	// stream... 기계주체 공유" (the same gate as GET /api/events above) and
	// is exactly the right shape for infra topology reads too. mcp__infra__*
	// calls these five routes with a machine principal's `osm_…` Bearer
	// (org.read.ops scope, issued via POST /api/machine-principals) instead
	// of reading k8s directly, keeping a single read surface/projection
	// (§7.1's "osiris-api read 표면을 감싼다").
	mux.HandleFunc("GET /api/infra/topology", s.guard.RequirePerm(authz.PermOrgReadOps, s.infra.Topology))
	mux.HandleFunc("GET /api/infra/nodes", s.guard.RequirePerm(authz.PermOrgReadOps, s.infra.Nodes))
	mux.HandleFunc("GET /api/infra/namespaces", s.guard.RequirePerm(authz.PermOrgReadOps, s.infra.Namespaces))
	mux.HandleFunc("GET /api/infra/pods", s.guard.RequirePerm(authz.PermOrgReadOps, s.infra.Pods))
	mux.HandleFunc("GET /api/infra/metrics", s.guard.RequirePerm(authz.PermOrgReadOps, s.infra.Metrics))

	// ── [2026-09-11] C-1 — 제안→승인 척추 (90-infra-topology.md §11) ──────
	//
	// IN17: the proposal IS an :Approval instance, gated by an
	// :ActionType{gateClass:"approval.admin"} row — no new approval
	// mechanism (core/internal/graphdb/writeops_approval.go,
	// actiontype_infra.go).
	//
	// IN18 — THE ONE DELIBERATE HI15 EXCEPTION IN THIS CODEBASE.
	// bin/lint-identity-authn.sh's HI15 check has a named, narrow carve-out
	// for exactly this one route (see that script's mutating-route case);
	// every other non-GET route in this file remains PersonOnly, unchanged.
	// POST /api/infra/proposals is registered on RequirePerm, not
	// PersonOnly, and is therefore reachable by a machine principal holding
	// org.read.ops — the same scope B단계 already granted machine
	// principals for the five read routes above. Reusing org.read.ops
	// (rather than minting a new scope) is deliberate: MachineGrantable only
	// allows org.read.* codes (authz.go's MachineGrantable), and IN18's own
	// argument is that a proposal is INERT — creating one grants no
	// capability, so it needs no capability-shaped scope, only "this is an
	// ops-adjacent action a machine may take", which org.read.ops already
	// means for this exact class of caller. The safety property this
	// route's whole existence rests on is graphdb.CreateApproval's, not this
	// gate's: see that function's doc comment.
	mux.HandleFunc("POST /api/infra/proposals", s.guard.RequirePerm(authz.PermOrgReadOps, s.infra.Propose))

	// The list is an ordinary read, gated exactly like the five above.
	mux.HandleFunc("GET /api/infra/proposals", s.guard.RequirePerm(authz.PermOrgReadOps, s.infra.ListProposals))

	// IN19: deciding is PersonOnly + RequirePerm(org.admin) — the ONLY
	// combination that can move an :Approval out of 'pending'
	// (core/internal/graphdb/writeops_approval.go's DecideApproval refuses
	// an empty ByPartyID as a second, independent wall). No supervisor, no
	// agent, no machine principal reaches this handler under any
	// circumstance — HI15 applies here in its ordinary, unexcepted form.
	mux.HandleFunc("POST /api/infra/proposals/{id}/decision", s.guard.PersonOnly(
		s.guard.RequirePermNoAuth(authz.PermOrgAdmin, s.infra.Decide)))

	// ── [2026-09-11] 프로바이더 연결 (설정 → omniroute 설정 → 프로바이더
	// 연결 탭) ──────────────────────────────────────────────────────────
	//
	// 콘솔이 omniroute를 직접 부르지 않고 osiris-api를 경유하는 계약(§84)
	// 그대로, 여기도 internal/onboarding과 동일한 패턴: 이 서비스는
	// omniroute/operations.mjs가 정의한 오퍼레이션의 얇은 HTTP 래퍼일
	// 뿐이고 wire-protocol 지식은 그 파일 하나에만 있다.
	//
	// 전부 PersonOnly: 프로바이더 설정은 관리자 작업이고, 목록 read를
	// 포함해 이 화면 전체가 콘솔 전용(기계 주체가 쓸 이유가 없다) —
	// org.read.onboarding 같은 별도 권한 코드를 새로 만들지 않고
	// PersonOnly로 통일한다(감독 지시). 다섯 개의 OAuth 라우트는 각각
	// 자신의 action 리터럴로만 omniroute.OAuthAction을 부른다(providers.
	// Service의 doc comment) — 클라이언트가 action을 고를 수 있는 경로는
	// 없다.
	mux.HandleFunc("GET /api/providers", s.guard.PersonOnly(s.prov.List))
	mux.HandleFunc("POST /api/providers", s.guard.PersonOnly(s.prov.Create))
	mux.HandleFunc("PUT /api/providers/{id}", s.guard.PersonOnly(s.prov.Update))
	mux.HandleFunc("DELETE /api/providers/{id}", s.guard.PersonOnly(s.prov.Delete))
	mux.HandleFunc("GET /api/oauth/{provider}/authorize", s.guard.PersonOnly(s.prov.Authorize))
	mux.HandleFunc("POST /api/oauth/{provider}/exchange", s.guard.PersonOnly(s.prov.Exchange))
	mux.HandleFunc("GET /api/oauth/{provider}/device-code", s.guard.PersonOnly(s.prov.DeviceCode))
	mux.HandleFunc("POST /api/oauth/{provider}/poll", s.guard.PersonOnly(s.prov.Poll))
	mux.HandleFunc("POST /api/oauth/{provider}/import-token", s.guard.PersonOnly(s.prov.ImportToken))

	return mux
}
