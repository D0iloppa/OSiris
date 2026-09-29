// Package authn is osiris-api's single authentication gate. It turns an
// incoming request into exactly one authz.Principal, or into a refusal, and it
// is the ONLY writer of that principal into the request context (H19/H28: the
// actor is derived from a server-side record, never from anything the client
// says).
//
// THREE BRANCHES, EXPLICITLY SEPARATED (60-identity-authn.md §19.3):
//
//  1. session cookie          → principal{kind: person | operator}
//  2. Bearer osk_…            → principal{kind: operator}   [master key, H10]
//  3. Bearer osm_…            → principal{kind: machine}
//  4. otherwise               → 401, one generic message
//
// ── [2026-07-27] H10 IS ON. WHAT CHANGED IN THIS FILE ─────────────────────
//
// The previous build's comment here said branch 2 kept accepting the legacy
// unprefixed setup token because IMPLEMENTATION-NOTES.md §102.4 required the
// order "(c) → H10": turning H10 on before machine principals existed would
// have left §81's curl-based completion conditions with nowhere to authenticate.
// (c) shipped, so that constraint is discharged and this build closes the loop:
//
//	the credential  is now `osk_…` (internal/masterkey, H22/§4.0)
//	its usability   is now conditional on the world (H10/§4.3)
//	its lifetime    now ends when the first administrator can log in (H23/§4.5)
//
// BRANCH 2 IS NOW PREFIX-ROUTED, which removes a real (if narrow) hazard rather
// than just tidying: the legacy token was base64url, whose alphabet contains
// `_`, so an unprefixed value could in principle begin with `osm_` (64⁻⁴) and
// only the comparison ORDER kept that collision harmless. With both credentials
// prefixed, the branches are mutually exclusive by construction — §19.3's stated
// purpose for the prefixes ("가드가 시행착오 비교 없이 경로를 고를 수 있고").
//
// H10 IS EVALUATED AFTER THE VALUE MATCHES, NOT BEFORE. A wrong key gets §8.2's
// uniform 401; only a CORRECT but retired key gets the 403 §4.3 specifies. The
// reverse order would answer "does this organization already have a working
// administrator" to anyone who asked with a random string, which is a fact about
// the organization and not something a failed login should teach.
//
// MIXED CREDENTIALS → 400 (§19.3, §102.2's closing line). Scoped precisely to
// the pair the design names: a session cookie AND an `osm_` bearer in the same
// request. Cookie + master-key bearer is NOT an error — the console produces it
// whenever a curl user's cookie jar is still warm, and §104.5's completion
// condition 5 measured that combination as a 200 that must stay a 200.
package authn

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"osiris-api/internal/authncred"
	"osiris-api/internal/authz"
	"osiris-api/internal/enrollment"
	"osiris-api/internal/graphread"
	"osiris-api/internal/machineauth"
	"osiris-api/internal/masterkey"
	"osiris-api/internal/secevents"
	"osiris-api/internal/session"
)

// windowEvaluator is the H10 predicate, and it is an INTERFACE here for one
// reason: this package must not be able to compute it. Satisfied by
// *orgstate.Evaluator, which is the single place the predicate is written
// (§4.3, §19.4, §19.5, MP1 all describe the same two conditions, and a second
// implementation in this file is how those four drift apart).
type windowEvaluator interface {
	Closed(ctx context.Context) (bool, error)
}

type Guard struct {
	keys     *masterkey.Guard
	sessions *session.Store
	machines *machineauth.Store

	// window answers H10 (§4.3). A nil window makes every master-key path fail
	// CLOSED — "판정 불가 시(Neo4j 다운) 거부한다", and a missing collaborator is
	// the most complete form of "cannot determine".
	window windowEvaluator

	// purgeReported keeps a host-level alarm from becoming a hot-path event
	// storm. §4.5 requires an event when the file cannot be deleted; it does not
	// require one per request, and the condition it reports (an undeletable
	// 0600 file) does not change between requests. Reset is deliberately absent:
	// once Purge succeeds the file is gone and Purge stops failing, so the flag
	// only ever suppresses repeats of a still-true alarm.
	purgeReported atomic.Bool

	// retiredReported does the same for the successful transition.
	retiredReported atomic.Bool

	// [2026-07-27] The person branch. §19.6's table said where each kind's
	// codes come from and left the person row empty because there were no
	// people; these four fill it.
	//
	//	perms    effective(party) from the graph — the codes a person session
	//	         carries, resolved per request rather than frozen into the
	//	         session (§3.2: "effectivePermissions를 세션에 캐시하지 않는다")
	//	creds    AUTHN_CRED, for password login
	//	tickets  AUTHN_TICKET, for the one-time enrollment redemption
	//	graph    email → :Person, the console login identifier (20 §3.1)
	perms   *authz.GraphPermissionSource
	creds   *authncred.Store
	tickets *enrollment.Store
	graph   *graphread.Client
	events  *secevents.Publisher
}

// New wires the guard. Every collaborator except tokens/sessions may be nil —
// osiris-api must still start and serve the pre-existing auth paths when Neo4j
// or NATS is unreachable at boot. A nil collaborator makes the branch that
// needs it fail CLOSED, never open.
func New(
	keys *masterkey.Guard,
	sessions *session.Store,
	machines *machineauth.Store,
	window windowEvaluator,
	perms *authz.GraphPermissionSource,
	creds *authncred.Store,
	tickets *enrollment.Store,
	graph *graphread.Client,
	events *secevents.Publisher,
) *Guard {
	return &Guard{
		keys: keys, sessions: sessions, machines: machines, window: window,
		perms: perms, creds: creds, tickets: tickets, graph: graph, events: events,
	}
}

// authError carries the HTTP status a refusal maps to. Everything except
// "store unavailable" and "rate limited" collapses to the same 401 with the
// same message — §8.2's "응답은 성공/실패 모두 동일한 일반 메시지"; there is
// nothing for an attacker to learn from the difference between "no such
// token", "expired" and "class predicate false".
type authError struct {
	status int
	msg    string
}

func (e authError) Error() string { return e.msg }

var (
	errUnauthenticated = authError{http.StatusUnauthorized, "missing or invalid session"}
	errMixed           = authError{http.StatusBadRequest, "a session cookie and a machine principal token may not be presented in the same request"}
	errStoreDown       = authError{http.StatusServiceUnavailable, "credential store unavailable"}
	errRateLimited     = authError{http.StatusTooManyRequests, "rate limit exceeded"}
	errMachineDenied   = authError{http.StatusForbidden, "this endpoint is not available to machine principals"}

	// errMasterKeyRetired is H10's refusal (§4.3: "영구 거부(403)").
	//
	// It is the ONE authentication failure in this file that explains itself,
	// and the exception is justified: §8.2's uniform message exists so an
	// attacker learns nothing from a failure, but this message is only ever
	// reached by someone who ALREADY HELD the correct master key — they have
	// nothing left to learn about it. What they do need to know is that the
	// value is not broken, it is finished, and where to go instead. A generic
	// 401 here would send an operator hunting for a corrupted file.
	errMasterKeyRetired = authError{http.StatusForbidden,
		"the master key has been retired: this organization has an administrator with a login credential. Sign in with that account (H10)"}

	// errWindowUndeterminable is §4.3's fail-closed direction, stated as its own
	// error so the two reasons for refusing a master key never collapse into one
	// status. "We cannot tell whether the window closed" is a 503 an operator can
	// act on (Neo4j is down); a 403 would tell them to stop using a credential
	// that is in fact still valid.
	errWindowUndeterminable = authError{http.StatusServiceUnavailable,
		"cannot determine whether the master key is still valid; refusing (fail-closed)"}
)

// masterKeyUsable evaluates H10 for a master-key credential that has ALREADY
// been verified, and performs H23's 자료 차원 when the answer is "closed".
//
// §4.3, verbatim on both halves:
//
//	closed := currentOrgRevision >= 1  ∧  credentialedHolders(org.admin) >= 1
//	"판정 불가 시(Neo4j 다운) 어떻게 하는가: 거부한다."
//
// THE PURGE IS A SIDE EFFECT, NEVER THE MECHANISM. The refusal below is caused
// by the predicate, not by the file's absence, which is why an undeletable file
// is a logged alarm and not a security failure (§4.5's independence property).
// Deleting here — on the request that observes the closure — is a backstop for
// the eager purge at the credential-setting moment (§4.1 ⑧); either one alone
// leaves the file behind in some ordering, and both are cheap.
func (g *Guard) masterKeyUsable(ctx context.Context) error {
	if g.window == nil {
		return errWindowUndeterminable
	}
	closed, err := g.window.Closed(ctx)
	if err != nil {
		return errWindowUndeterminable
	}
	if !closed {
		return nil
	}
	g.purgeMasterKey(ctx)
	return errMasterKeyRetired
}

// retireMasterKeyIfClosed is the eager half of H23 — called right after a
// credential is written (§4.1 ⑧), which is the exact moment the second H10
// condition can flip from false to true.
//
// It reports nothing to the caller and can fail freely: the authentication path
// re-derives the same predicate on every request, so a missed purge costs a file
// on the host and no authority whatsoever.
func (g *Guard) retireMasterKeyIfClosed(ctx context.Context) {
	if g.window == nil {
		return
	}
	if closed, err := g.window.Closed(ctx); err != nil || !closed {
		return
	}
	g.purgeMasterKey(ctx)
}

// purgeMasterKey deletes the file and reports what happened.
//
// §4.5: "삭제 실패는 로그 경고 + security.masterkey.purge_failed 이벤트이지
// 로그인 실패가 아니다." Both outcomes are audited, because "the credential file
// is gone" and "the credential file could not be removed" are things an operator
// must be able to establish afterwards without reading the disk.
func (g *Guard) purgeMasterKey(ctx context.Context) {
	if g.keys == nil {
		return
	}
	path := g.keys.Path()
	if err := g.keys.Purge(); err != nil {
		if g.purgeReported.CompareAndSwap(false, true) {
			log.Printf("osiris-api: WARNING: the H10 window has closed but the master key file %s could not be deleted (%v). The credential is already refused by the predicate; remove the file manually.", path, err)
			g.audit(ctx, secevents.MasterKeyPurgeFailed, secevents.Event{
				ActorRef: "system", Reason: err.Error(), At: time.Now().UTC(),
			})
		}
		return
	}
	// ONE retirement event per process. Purge is idempotent and this function
	// runs on every request that observes the closed window, so an event per
	// call would bury the single moment that matters under identical repeats.
	// The event's meaning is "this instance observed H10 closed and the file is
	// now gone", which is a fact about a transition, not about a request.
	if g.retiredReported.CompareAndSwap(false, true) {
		log.Printf("osiris-api: the H10 window has closed — master key retired and %s removed (§4.5)", path)
		g.audit(ctx, secevents.MasterKeyRetired, secevents.Event{
			ActorRef: "system", Reason: "h10_closed", At: time.Now().UTC(),
		})
	}
}

// resolve implements the four branches. It never logs, echoes, or returns any
// credential value.
func (g *Guard) resolve(r *http.Request) (authz.Principal, error) {
	cookie := session.FromRequest(r)
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")

	if cookie != "" && machineauth.HasTokenPrefix(bearer) {
		return authz.Principal{}, errMixed
	}

	// 1. cookie
	//
	// [2026-07-27] A cookie can now be either of §3.1's two session kinds. The
	// record says which, and the record is the ONLY source — H19/H28: the
	// actor is derived from a server-side record, never from anything the
	// client sends. There is no header, body field or query parameter anywhere
	// in this service that can influence the value of PartyID.
	if rec, ok := g.sessions.Lookup(cookie); ok {
		return g.principalFor(r.Context(), rec)
	}

	if bearer == "" {
		return authz.Principal{}, errUnauthenticated
	}

	// 2. master key — prefix-routed (§19.3), then H10 (§4.3).
	//
	// The prefix decides the BRANCH; Valid decides the VALUE; the window decides
	// whether the value still means anything. Three separate questions, asked in
	// that order, so that a retired key cannot be mistaken for a typo and a typo
	// cannot learn the organization's state.
	if masterkey.HasKeyPrefix(bearer) {
		if !g.keys.Valid(bearer) {
			return authz.Principal{}, errUnauthenticated
		}
		if err := g.masterKeyUsable(r.Context()); err != nil {
			return authz.Principal{}, err
		}
		return authz.Principal{Kind: authz.KindOperator, Permissions: authz.OperatorScope()}, nil
	}

	// 3. machine principal
	if machineauth.HasTokenPrefix(bearer) {
		if g.machines == nil {
			return authz.Principal{}, errStoreDown
		}
		p, _, err := g.machines.Resolve(r.Context(), bearer)
		switch {
		case err == nil:
			return p, nil
		case errors.Is(err, machineauth.ErrRateLimited):
			return authz.Principal{}, errRateLimited
		case errors.Is(err, machineauth.ErrUnavailable):
			return authz.Principal{}, errStoreDown
		default:
			return authz.Principal{}, errUnauthenticated
		}
	}

	return authz.Principal{}, errUnauthenticated
}

// principalFor turns a session record into a Principal.
//
// THE PERSON BRANCH RESOLVES PERMISSIONS FROM THE GRAPH ON EVERY REQUEST
// (through a ≤10s cache — authz.PermissionCacheTTL). §3.2 is explicit that the
// code set must NOT live in the session: "권한은 그래프가 진실이고 조직 변경
// 즉시 반영되어야 하므로 요청 시점마다 파생한다". A session that carried a
// frozen permission array would keep a demoted manager powerful until they
// logged out — for up to twelve hours.
//
// A resolution failure is an ERROR, not an empty permission set. Empty would
// mean "authenticated but powerless", which reads as a successful login and
// then a confusing 403; the error means "we cannot tell", and the guard turns
// it into a 503 so the operator learns the graph is down instead of thinking
// they were demoted.
func (g *Guard) principalFor(ctx context.Context, rec session.Record) (authz.Principal, error) {
	if rec.Kind != "person" || rec.PartyID == "" {
		// The master-key session — §3.1's "the only session without a :Party".
		//
		// H10 APPLIES TO THE SESSION, NOT ONLY TO THE LOGIN. §4.3 writes the rule
		// against `POST /auth/session {masterKey}`, and stopping there would leave
		// a live cookie outliving the closure by up to §8.4's 12h — the master key
		// would be "retired" while a browser it had already opened kept full
		// operator scope. That is the same defect §104.3 found in the SSE path
		// (authentication at connect time is not authentication for the
		// connection's life), and MP1's principle settles it: the privilege is
		// conditional on the state of the world, re-evaluated per request, for
		// every artifact derived from that credential.
		//
		// This creates no deadlock in §4.1's sequence. The window closes when an
		// administrator gains a credential, and the path that gives them one
		// (ticket redemption) issues a PERSON session before that write lands —
		// so nobody is ever holding only a master-key session at the moment it
		// stops working.
		if err := g.masterKeyUsable(ctx); err != nil {
			return authz.Principal{}, err
		}
		return authz.Principal{Kind: authz.KindOperator, Permissions: authz.OperatorScope()}, nil
	}
	if g.perms == nil {
		return authz.Principal{}, errStoreDown
	}
	codes, err := g.perms.ResolveCtx(ctx, rec.PartyID)
	if err != nil {
		return authz.Principal{}, errStoreDown
	}
	return authz.Principal{
		Kind:              authz.KindPerson,
		PartyID:           rec.PartyID,
		Permissions:       codes,
		CredentialPending: rec.CredentialPending,
	}, nil
}

// RevalidateInterval is how often an OPEN long-lived response re-checks the
// credential that opened it (see RequirePermStreaming).
//
// WHY HALF THE POSITIVE CACHE TTL, AND NOT SOME OTHER NUMBER. §7.4 fixed the
// revocation lag this system is willing to pay at "최대 60초" and stated it as
// an explicit cost rather than hiding it. A long-lived stream must not add a
// SECOND, larger lag on top of that, so the watchdog interval has to be
// strictly inside the 60s envelope, and the check it performs has to be
// authoritative rather than cache-served (machineauth.Store.Revalidate skips
// the positive cache for exactly this reason). At 30s the worst case is:
// revoke → ≤30s until the next tick → ≤2s until the SSE loop notices the
// cancelled context (its fetch window) → stream closed. Comfortably inside the
// number the design already committed to, and no new cost is introduced.
const RevalidateInterval = 30 * time.Second

func deny(w http.ResponseWriter, err error) {
	var ae authError
	if !errors.As(err, &ae) {
		ae = errUnauthenticated
	}
	writeErr(w, ae.status, ae.msg)
}

// --- the three middlewares the router is allowed to use ---------------------
//
// HI15 is a STATIC check over internal/api/server.go's route table
// (bin/lint-identity-authn.sh). It can only be static because every route is
// registered with a Go 1.22 method pattern, which makes "is this a non-GET
// route" a property of the routing table rather than of an `if r.Method != …`
// buried in a handler. See that file's header comment for the full argument.

// RequirePerm authenticates and then applies authz.Authorize — the single
// decision point of §19.6/HI16. Usable by every principal kind; which kinds
// can actually pass follows from which codes they can hold, which for machine
// principals is fixed at issue time to org.read.* only.
func (g *Guard) RequirePerm(required authz.Code, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := g.resolve(r)
		if err != nil {
			deny(w, err)
			return
		}
		if !authz.Authorize(p, required) {
			writeErr(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		next(w, authz.WithPrincipal(r, p))
	}
}

// RequirePermStreaming is RequirePerm for a response that stays open.
//
// It authenticates and authorizes exactly as RequirePerm does, and then starts
// a watchdog that re-checks the credential every RevalidateInterval for as
// long as the handler runs. When the credential stops being valid — revoked,
// expired, logged out, or (for a bootstrap machine principal) the H10 window
// closing underneath it — the request context is cancelled and the handler
// unwinds. internal/events' SSE loop tests r.Context() on every fetch cycle,
// so cancellation closes the stream within one fetch window (2s).
//
// THIS FIXES A DEFECT THAT PREDATES THE MACHINE PRINCIPAL WORK. Authentication
// used to happen once, at connect time, and never again. MEASURED against the
// build before this change: a browser session was logged out (POST
// /api/auth/logout returned 200, and the cookie immediately 401'd on every
// ordinary request) while its SSE stream stayed open and kept receiving events
// for the full 60s the probe ran, ending only when the client hung up. §8.4's
// "로그아웃 시 서버측 무효화" and MP5's "즉시 리보크" were therefore both
// silently false for /api/events. The watchdog is deliberately written against
// the PRINCIPAL, not against machine principals specifically, so both kinds
// are covered by one mechanism.
//
// Only long-lived handlers should use this: for an ordinary request the
// handler returns long before the first tick, so the goroutine would be pure
// overhead. bin/lint-identity-authn.sh pins GET /api/events to this middleware
// so the protection cannot be quietly dropped from the one route that needs
// it.
func (g *Guard) RequirePermStreaming(required authz.Code, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := g.resolve(r)
		if err != nil {
			deny(w, err)
			return
		}
		if !authz.Authorize(p, required) {
			writeErr(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		go g.watchCredential(ctx, cancel, r, p, required)
		next(w, authz.WithPrincipal(r.WithContext(ctx), p))
	}
}

// watchCredential is the watchdog loop. It logs the ActorRef — a public
// identifier — and never the credential (HI10).
func (g *Guard) watchCredential(ctx context.Context, cancel context.CancelFunc, r *http.Request, p authz.Principal, required authz.Code) {
	t := time.NewTicker(RevalidateInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := g.revalidate(ctx, r, required); err != nil {
				log.Printf("osiris-api: closing open stream for %s: credential is no longer valid (%v)", p.ActorRef(), err)
				cancel()
				return
			}
		}
	}
}

// revalidate re-runs the same three branches resolve does, against the same
// request, but takes the authoritative path for each and re-applies the
// permission check (a machine principal's codes are frozen, but a person's
// will not be once effective(party) is wired in — this is where a demoted
// person's stream will close).
func (g *Guard) revalidate(ctx context.Context, r *http.Request, required authz.Code) error {
	cookie := session.FromRequest(r)
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")

	if cookie != "" {
		rec, ok := g.sessions.Lookup(cookie)
		if !ok {
			return errors.New("session revoked or expired")
		}
		// AUTHORITATIVE, cache-bypassing — the same rule
		// machineauth.Revalidate follows. Re-reading the ≤10s cache would ask
		// "was this person authorized ten seconds ago", whose answer we
		// already have. THIS is where a demoted person's open stream closes.
		p := authz.Principal{Kind: authz.KindOperator, Permissions: authz.OperatorScope()}
		if rec.Kind != "person" || rec.PartyID == "" {
			// A master-key session's stream must close when H10 closes, for the
			// same reason §104.3 gave for every other credential: the watchdog
			// exists precisely because connect-time authentication is not
			// lifetime authentication.
			if err := g.masterKeyUsable(ctx); err != nil {
				return errors.New("master key retired")
			}
		}
		if rec.Kind == "person" && rec.PartyID != "" {
			if g.perms == nil {
				return errors.New("permission source unavailable")
			}
			codes, err := g.perms.ResolveFresh(ctx, rec.PartyID)
			if err != nil {
				return errors.New("permissions undeterminable")
			}
			p = authz.Principal{Kind: authz.KindPerson, PartyID: rec.PartyID, Permissions: codes}
		}
		if !authz.Authorize(p, required) {
			return errors.New("permission withdrawn")
		}
		return nil
	}
	if machineauth.HasTokenPrefix(bearer) {
		if g.machines == nil {
			return errors.New("credential store unavailable")
		}
		return g.machines.Revalidate(ctx, bearer)
	}
	if masterkey.HasKeyPrefix(bearer) && g.keys.Valid(bearer) {
		if err := g.masterKeyUsable(ctx); err != nil {
			return errors.New("master key retired")
		}
		return nil
	}
	return errors.New("credential no longer accepted")
}

// RequirePermNoAuth applies a permission check to a request the caller has
// ALREADY authenticated — it reads the principal out of the context instead of
// resolving it a second time.
//
// It exists because the org-role routes stack two middlewares
// (PersonOnly(RequirePermNoAuth(...))) and resolving the credential twice per
// request would double every KV lookup and, for a person, risk the two
// resolutions disagreeing across the ≤10s cache boundary — the outer one
// deciding "person" and the inner one seeing a different code set. One
// resolution, one decision.
//
// It is NOT a way to skip authentication: an unauthenticated request has no
// principal in the context and gets 401 here. The name says "NoAuth" to make
// it obvious at the call site that something else must have authenticated
// first, which the PersonOnly wrapper always has.
func (g *Guard) RequirePermNoAuth(required authz.Code, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := authz.PrincipalFrom(r)
		if !ok {
			deny(w, errUnauthenticated)
			return
		}
		if !authz.Authorize(p, required) {
			writeErr(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		next(w, r)
	}
}

// RequireAnyPerm is RequirePermNoAuth for a disjunction.
//
// §3.3's screen #10 row is literally "org.manage.people 또는 org.manage.roles
// (둘 다 없으면 미노출)" — an onboarding coordinator who may add people but not
// grant roles must still see the list. The disjunction lives here rather than
// as two routes so that the ROUTE TABLE keeps showing one path per endpoint,
// which is what HI15's static check reads.
func (g *Guard) RequireAnyPerm(any []authz.Code, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := authz.PrincipalFrom(r)
		if !ok {
			deny(w, errUnauthenticated)
			return
		}
		for _, c := range any {
			if authz.Authorize(p, c) {
				next(w, r)
				return
			}
		}
		writeErr(w, http.StatusForbidden, "insufficient permissions")
	}
}

// PersonOnly authenticates and then refuses machine principals outright.
//
// This is §19.6's "절대 못 한다 (구조적으로)" row ① — "라우터가 non-GET
// 라우트에 personOnly 미들웨어를 강제(HI15)" — and it is the FIRST of the two
// layers §19.7 diagrams. The second is osiris-core's own rule (§6.5: a
// mutating request without a valid :Party actor is refused), which holds even
// if this layer is bypassed by a bug, because a machine principal has no
// partyId to send.
//
// It is applied to some GET routes too (GET /api/events, GET
// /api/machine-principals). HI15 requires it on every non-GET route; it does
// not forbid it elsewhere.
func (g *Guard) PersonOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := g.resolve(r)
		if err != nil {
			deny(w, err)
			return
		}
		if p.IsMachine() {
			deny(w, errMachineDenied)
			return
		}
		// §4.4 / HI12 — a session whose account has no credential yet is
		// READ-ONLY. "다시 들어올 수 있음을 먼저 확정한 뒤에 바꾼다": an
		// account that cannot log back in must not be steering the
		// organization, because there is no way to continue or undo what it
		// does. Enforced at the middleware, not per handler, for HI15's reason
		// — a handler-internal check is what a new handler forgets.
		//
		// PersonOnly wraps every non-GET route, so this covers every mutating
		// endpoint. It is unreachable in this build (the only path that mints
		// a person session, ticket redemption, sets the credential in the same
		// request), and it is here because the §4.1 ⑥ promotion that DOES
		// produce a pending session is the next task's.
		if p.Kind == authz.KindPerson && p.CredentialPending && r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeErr(w, http.StatusConflict, "credential_required")
			return
		}
		next(w, authz.WithPrincipal(r, p))
	}
}

// PersonOnlyPublic is PersonOnly for the two routes that must remain reachable
// WITHOUT authentication: POST /api/auth/session (a fresh browser has no
// session yet — gating it would be circular) and POST /api/auth/logout (must
// work when the session has already expired; see Logout's idempotency note).
//
// §19.6 row ② still applies to them: "POST /auth/* 전부 — 세션 생성·자격증명
// 설정·티켓 상환 불가" for machine principals. So this middleware requires no
// credential but REFUSES one: a request carrying an `osm_` bearer is rejected
// before the handler runs, which is what stops a machine principal from
// exchanging its way into a session (HI13 — a machine principal must never
// become a session).
//
// The name deliberately contains "PersonOnly" so that HI15's static check over
// the route table matches it with one pattern.
func (g *Guard) PersonOnlyPublic(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie := session.FromRequest(r)
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if machineauth.HasTokenPrefix(bearer) {
			if cookie != "" {
				deny(w, errMixed)
				return
			}
			deny(w, errMachineDenied)
			return
		}
		next(w, r)
	}
}

// --- GET /api/whoami --------------------------------------------------------
//
// §19.6 lists "GET /me가 아닌 GET /whoami" among the things a machine
// principal must be able to call. It reports what the SERVER decided about
// this request — never a credential, never anything the client asserted — and
// it is the intended way for a CI script to confirm its token resolved to the
// scope it expected without dumping a response body full of org data.
func (g *Guard) Whoami(w http.ResponseWriter, r *http.Request) {
	p, ok := authz.PrincipalFrom(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, errUnauthenticated.msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":        string(p.Kind),
		"actorRef":    p.ActorRef(),
		"machineId":   p.MachineID,
		"partyId":     p.PartyID,
		"permissions": authz.Strings(p.Permissions),
	})
}

// RequireAuthenticated gates /api/whoami: any successfully authenticated
// principal, no permission code. Not usable on a non-GET route — HI15 requires
// PersonOnly there.
func (g *Guard) RequireAuthenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := g.resolve(r)
		if err != nil {
			deny(w, err)
			return
		}
		next(w, authz.WithPrincipal(r, p))
	}
}

// --- POST /api/auth/session --------------------------------------------
//
// Deliberately NOT gated by an authenticating middleware (that would be
// circular — this is the one endpoint a fresh browser with no session yet must
// be able to reach), but it IS gated by PersonOnlyPublic so a machine
// principal cannot reach it (§19.6 row ②, HI13).
//
// No credential presented here is ever echoed back, logged, or stored: the
// setup token is compared and dropped, the password becomes an Argon2id
// verifier, the ticket becomes a sha256 key. See each branch below.
type exchangeReq struct {
	// (a) the master key — §4.0's "요청 헤더/본문 키: {"masterKey": …}".
	//
	// THE OLD `setupToken` FIELD IS GONE, not deprecated-but-accepted. Keeping it
	// as an alias would mean the credential that §4.0 says "행동상 이미 로그인
	// 자격증명이었고, 이름만 그 사실을 가리고 있었다" keeps its misleading name
	// alive at the only place a human-facing client actually types it. A client
	// sending the old field now gets a 401 (the field decodes to ""), and the
	// console was updated in the same change — there is exactly one caller.
	MasterKey string `json:"masterKey"`

	// (b) [2026-07-27] password login — §3.1's "email + password" row.
	Email    string `json:"email"`
	Password string `json:"password"`

	// (c) [2026-07-27] enrollment ticket redemption — §5.1's 상환 row:
	// "티켓 + 새 비밀번호 제출 → AUTHN_CRED 기록 → 세션 발급 → 티켓 즉시 폐기".
	// The ticket IS the authentication for that one request (§5.6 state B), so
	// no current password is asked for; there is none to ask for.
	Ticket string `json:"ticket"`
}

// Exchange handles POST /api/auth/session — §3.1's funnel.
//
//	비밀번호 (email + password)  ─┐
//	등록 티켓                     ├─→ 세션 { partyId: "emp-…" } → effective(party)
//	master key (설정 토큰)        ─┘   (master key: partyId null, fixed scope)
//
// THE SHAPE OF THE BODY SELECTS THE BRANCH, AND THE SERVER DECIDES WHAT THE
// RESULT IS. A caller cannot ask for a person session; it can only present a
// credential, and what that credential proves is the server's conclusion. This
// is H19/H28 at the entry point: the identity in the resulting session comes
// from AUTHN_CRED / AUTHN_TICKET, never from an "as" or "partyId" field, and
// no such field exists.
//
// FAILURE IS ALWAYS THE SAME 401 WITH THE SAME MESSAGE (§8.2), for all three
// branches: "wrong password", "no such email", "expired ticket" and "no token"
// are indistinguishable from outside. authncred.Verify even spends the Argon2
// cost on a missing account so the RESPONSE TIME does not become the
// enumeration oracle the shared message was meant to close.
//
// WHAT IS NOT HERE, deliberately: password CHANGE (§5.6 state C, which must
// re-confirm the current password and revoke the person's other sessions), the
// master-key session promotion of §4.1 ⑥, and break-glass redemption. Those
// belong to the H10 / §5.6 / §9 tasks.
func (g *Guard) Exchange(w http.ResponseWriter, r *http.Request) {
	var req exchangeReq
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()

	switch {
	case req.Ticket != "":
		g.redeemTicket(ctx, w, r, req)
	case req.Email != "":
		g.passwordLogin(ctx, w, r, req)
	default:
		// (a) master key — §4.1 ①, now with H10.
		//
		// VALUE FIRST, THEN WINDOW (see the package doc). A wrong key is §8.2's
		// uniform 401; a correct-but-retired key is §4.3's 403, and only someone
		// who held the real key ever sees it.
		if !g.keys.Valid(req.MasterKey) {
			g.audit(ctx, secevents.AuthnLoginFailed, secevents.Event{
				ActorRef: "system", Reason: "master-key", At: time.Now().UTC(),
			})
			writeErr(w, http.StatusUnauthorized, errUnauthenticated.msg)
			return
		}
		if err := g.masterKeyUsable(ctx); err != nil {
			deny(w, err)
			return
		}
		id := g.sessions.Create()
		session.SetCookie(w, r, id)
		g.audit(ctx, secevents.AuthnLoginSucceeded, secevents.Event{
			ActorRef: "master-key", Reason: "master-key", At: time.Now().UTC(),
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "kind": "master-key"})
	}
}

// passwordLogin is §3.1's "email + password" row.
func (g *Guard) passwordLogin(ctx context.Context, w http.ResponseWriter, r *http.Request, req exchangeReq) {
	if g.creds == nil || g.graph == nil {
		writeErr(w, http.StatusServiceUnavailable, errStoreDown.msg)
		return
	}
	email := authncred.NormalizeEmail(req.Email)

	person, found, err := g.graph.PersonByEmail(ctx, email)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, errStoreDown.msg)
		return
	}
	// A missing or deactivated person still goes through Verify against an id
	// that has no record, which spends the hash and returns false — same
	// message, same cost, nothing learned.
	id := person.ID
	if !found || !person.Active {
		id = ""
	}
	ok, err := g.creds.Verify(ctx, id, req.Password)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, errStoreDown.msg)
		return
	}
	if !ok {
		g.audit(ctx, secevents.AuthnLoginFailed, secevents.Event{
			ActorRef: "system", Reason: "password", At: time.Now().UTC(),
		})
		writeErr(w, http.StatusUnauthorized, errUnauthenticated.msg)
		return
	}

	sid := g.sessions.CreateFor(session.Record{
		PartyID: person.ID, Kind: "person", AuthMethod: "password",
	})
	session.SetCookie(w, r, sid)
	g.audit(ctx, secevents.AuthnLoginSucceeded, secevents.Event{
		ActorRef: "party:" + person.ID, TargetPartyID: person.ID,
		Reason: "password", At: time.Now().UTC(),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "kind": "person", "partyId": person.ID})
}

// --- POST /api/me/password ----------------------------------------------
//
// The self-service half of §5.6 state C ("비밀번호 변경") that authncred's
// package doc flagged as deliberately absent: a logged-in person changing
// their own password, re-confirming the current one first.
//
// Routed through PersonOnly (server.go), which already:
//   - refuses a machine principal (§19.6 row ①/②) before this handler runs;
//   - refuses a CredentialPending session on every non-GET route (§4.4/HI12),
//     so an account that cannot yet log back in cannot reach this either —
//     moot in this build (nothing produces CredentialPending yet) but future
//     -proof for the master-key promotion path that will.
//
// CURRENT-PASSWORD RE-CONFIRMATION IS NOT OPTIONAL. It is what distinguishes
// this endpoint from "anyone holding a live cookie can lock the real owner
// out" — a stolen session cookie is not the same threat as a stolen password,
// and collapsing the two would make every XSS or shoulder-surfed cookie into
// a full account takeover. Unlike passwordLogin's uniform 401 (§8.2's
// anti-enumeration rule, which exists because the caller is UNAUTHENTICATED
// and might be probing for valid emails), the caller here is already a known,
// authenticated :Person asking about their OWN credential — there is no
// enumeration surface left to protect, so "current password is wrong" is
// named plainly rather than folded into a generic failure.
type changePasswordReq struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (g *Guard) ChangePassword(w http.ResponseWriter, r *http.Request) {
	actor, ok := authz.PrincipalFrom(r)
	if !ok {
		deny(w, errUnauthenticated)
		return
	}
	// PersonOnly already refuses machine principals, but a handler that can be
	// wired to the wrong middleware by a future edit should not silently start
	// changing a machine principal's (nonexistent) password. Belt and braces.
	if actor.Kind != authz.KindPerson || actor.PartyID == "" {
		writeErr(w, http.StatusForbidden, "only a person account has a password to change")
		return
	}
	if g.creds == nil {
		writeErr(w, http.StatusServiceUnavailable, errStoreDown.msg)
		return
	}

	var req changePasswordReq
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()

	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeErr(w, http.StatusBadRequest, "currentPassword and newPassword are required")
		return
	}

	ok, err := g.creds.Verify(ctx, actor.PartyID, req.CurrentPassword)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, errStoreDown.msg)
		return
	}
	if !ok {
		writeErr(w, http.StatusUnauthorized, "현재 비밀번호가 올바르지 않습니다")
		return
	}

	if req.NewPassword == req.CurrentPassword {
		writeErr(w, http.StatusBadRequest, "새 비밀번호는 현재 비밀번호와 달라야 합니다")
		return
	}

	if err := g.creds.Set(ctx, actor.PartyID, req.NewPassword, "self-change"); err != nil {
		if errors.Is(err, authncred.ErrWeak) {
			writeErr(w, http.StatusBadRequest, "비밀번호는 최소 12자여야 합니다")
			return
		}
		writeErr(w, http.StatusServiceUnavailable, errStoreDown.msg)
		return
	}

	g.audit(ctx, secevents.AuthnCredentialChanged, secevents.Event{
		ActorRef: "party:" + actor.PartyID, TargetPartyID: actor.PartyID,
		Reason: "self-change", At: time.Now().UTC(),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// redeemTicket is §5.1's redemption: the ticket authenticates once, the person
// sets their own password, the ticket is destroyed, and a session is issued.
//
// ORDER: redeem (consumes the ticket) → validate the password → write
// AUTHN_CRED → revoke the person's other sessions → issue the new session.
//
// Consuming the ticket FIRST is deliberate. A ticket is an account-takeover
// primitive (§5.3's 판정 ⑤); if a weak-password rejection left it alive, a
// failed redemption would be a free retry on someone else's account. Spending
// it on a failed attempt costs the legitimate user a re-issue and costs an
// attacker the whole primitive.
//
// Revoking the person's other sessions is §7.3's third mandatory forced-logout
// point ("유출 의심으로 바꿨는데 기존 세션이 살아 있으면 변경의 의미가 없다"),
// applied to the case this build actually has.
func (g *Guard) redeemTicket(ctx context.Context, w http.ResponseWriter, r *http.Request, req exchangeReq) {
	if g.tickets == nil || g.creds == nil || g.graph == nil {
		writeErr(w, http.StatusServiceUnavailable, errStoreDown.msg)
		return
	}
	rec, err := g.tickets.Redeem(ctx, req.Ticket)
	if err != nil {
		if errors.Is(err, enrollment.ErrUnavailable) {
			writeErr(w, http.StatusServiceUnavailable, errStoreDown.msg)
			return
		}
		g.audit(ctx, secevents.AuthnLoginFailed, secevents.Event{
			ActorRef: "system", Reason: "ticket", At: time.Now().UTC(),
		})
		writeErr(w, http.StatusUnauthorized, errUnauthenticated.msg)
		return
	}

	// The graph is still the authority on whether this person exists and is
	// active — a ticket outliving a deactivation must not resurrect anyone.
	person, found, gerr := g.graph.GetPerson(ctx, rec.PersonID)
	if gerr != nil {
		writeErr(w, http.StatusServiceUnavailable, errStoreDown.msg)
		return
	}
	if !found || !person.Active {
		writeErr(w, http.StatusUnauthorized, errUnauthenticated.msg)
		return
	}

	// §5.4's policy: a length floor and nothing else. The 400 here names the
	// requirement — unlike an authentication failure there is nothing to
	// conceal, and refusing without saying why would just produce retries.
	if err := g.creds.Set(ctx, rec.PersonID, req.Password, "ticket"); err != nil {
		if errors.Is(err, authncred.ErrWeak) {
			writeErr(w, http.StatusBadRequest, authncred.ErrWeak.Error())
			return
		}
		writeErr(w, http.StatusServiceUnavailable, errStoreDown.msg)
		return
	}
	g.sessions.RevokeAllFor(rec.PersonID)

	sid := g.sessions.CreateFor(session.Record{
		PartyID: rec.PersonID, Kind: "person", AuthMethod: "ticket",
	})

	// ★ §4.1 ⑧ — THE MOMENT THE MASTER KEY DIES.
	//
	// "AUTHN_CRED[personId] 기록 → ... 이 순간 H10의 두 조건이 모두 참이 됨 →
	// master key 영구 거부 + state/creds/admin/master_key 삭제 (H23)".
	//
	// This is the ONLY place in the service where the second H10 condition can
	// newly become true, because it is the only place a credential is created.
	// The call is unconditional and the predicate decides: redeeming a ticket as
	// an ordinary employee leaves the window exactly as it was (that person holds
	// no org.admin), so this costs one predicate evaluation and changes nothing
	// until the redeemer is an administrator.
	//
	// It runs AFTER the session is issued, deliberately. The person session is
	// what the redeemer continues with, and a purge failure must not stand
	// between them and being logged in — the refusal of the master key is the
	// predicate's job, already done, and §4.5 is explicit that a failed deletion
	// "로그인 실패가 아니다".
	g.retireMasterKeyIfClosed(ctx)
	session.SetCookie(w, r, sid)
	g.audit(ctx, secevents.EnrollmentRedeemed, secevents.Event{
		ActorRef: "party:" + rec.PersonID, TargetPartyID: rec.PersonID, At: time.Now().UTC(),
	})
	g.audit(ctx, secevents.AuthnCredentialChanged, secevents.Event{
		ActorRef: "party:" + rec.PersonID, TargetPartyID: rec.PersonID,
		Reason: "ticket", At: time.Now().UTC(),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "kind": "person", "partyId": rec.PersonID})
}

// auditTimeout bounds a best-effort audit publish.
//
// ── WHY THIS CONSTANT EXISTS: A MEASURED DEFECT, NOT A PRECAUTION ──────────
//
// This function was already documented as best-effort, and it was not. The
// underlying publish is a JetStream publish that WAITS FOR AN ACK, so any
// condition that produces no ack blocks the caller — and the H10 work produced
// one on the first try: security.masterkey.retired was missing from osiris-api's
// NATS publish grant (fixed in orchestrator/internal/natsauth), the server
// answered `Permissions Violation`, no ack ever came, and a credential
// redemption request hung for over 100 seconds. MEASURED with curl, not
// theorized.
//
// "Best effort" has to be enforced by a deadline or it is only an intention. The
// value is 2s: long enough that a healthy JetStream ack (single-digit ms here)
// never trips it, short enough that a broken audit path costs a request less
// than a human notices.
const auditTimeout = 2 * time.Second

// audit publishes a security event best-effort.
//
// BEST EFFORT HERE, unlike DG5's fail-closed publish: refusing to complete an
// authentication because the audit bus is down would turn a NATS outage into a
// total lockout, and the event carries no authorization weight. DG5's events
// are different — they accompany a CHANGE that must not happen unobserved, so
// people.announce keeps waiting on its publish and this one does not.
//
// THE CONTEXT IS DETACHED FROM THE REQUEST, deliberately. A caller who hangs up
// mid-login has still produced an authentication outcome worth recording, and
// inheriting the request context would drop exactly the events an attacker
// generates by disconnecting early. Detached but DEADLINED — the pairing is the
// point.
func (g *Guard) audit(_ context.Context, subject string, ev secevents.Event) {
	if g.events == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()
	if err := g.events.Publish(ctx, subject, ev); err != nil {
		// Logged, never fatal, and never with the credential — the event body
		// carries only ActorRef and an outcome (see secevents.Event).
		log.Printf("osiris-api: security audit event %s not published: %v", subject, err)
	}
}

// --- POST /api/auth/logout ----------------------------------------------
//
// §8.4's "로그아웃 시 서버측 무효화": revokes the session server-side (so
// the cookie value, even if somehow retained by the client, can never
// validate again) and tells the browser to drop its copy. Idempotent — a
// request with no/already-invalid cookie still gets a 200 and a
// clear-cookie response, matching "logging out when already logged out" as
// a non-error.
func (g *Guard) Logout(w http.ResponseWriter, r *http.Request) {
	g.sessions.Revoke(session.FromRequest(r))
	session.ClearCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
