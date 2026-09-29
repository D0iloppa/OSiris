// Package genesis implements the genesis principal of
// docs/design/10-core-architecture.md §8.5 — the ONE subject in this system
// whose authorization is not derived from a :PermissionProfile — AS
// RE-WIRED by docs/design/70-kubernetes-deployment.md §4.1 (K7).
//
// ── WHAT CHANGED, K7, AND WHY ─────────────────────────────────────────────
//
// §8.5 (compose, [1단계]): osiris-core MINTS a token and binds it to a
// container id it must trust orchestrator's word about ("core가
// orchestrator의 주장을 믿는다", §8.5). That trust argument rests entirely on
// orchestrator being the only holder of docker.sock (G8).
//
// §4.1 (k8s, [2단계], K7): "genesis 자격증명을 OSiris가 만들지 않는다." The
// bootstrap-master Pod gets a dedicated ServiceAccount; kubelet auto-mounts a
// projected SA token (signed by the K8s API server, not by this process);
// osiris-core VERIFIES that token against `tokenreviews.authentication.k8s.io`
// instead of minting and remembering one of its own. §4.1: "core가
// orchestrator의 '이것이 방금 만든 컨테이너다'라는 주장을 믿을 필요 없이…
// K8s 자신에게 직접 검증받는다."
//
// Concretely, this rewrite deletes:
//   - Store, Issue, Revoke, Live — there is nothing left for this package to
//     GENERATE or REMEMBER. The credential's lifecycle (mint, bind, expire,
//     revoke) is now entirely K8s's: `expirationSeconds` is W2's TTL (70 §4.2
//     row W2), ServiceAccount deletion is W3's revoke (70 §4.3, and see the
//     package-level note below on the 60-second nuance that discovery
//     produced), and TokenReview's UID check is W4's binding (70 §4.2 row
//     W4) — none of it is this package's job to implement, only to CALL.
//   - TokenPrefix, TTL — both were properties of a token THIS package used to
//     mint. A K8s bound service account token is a JWT with its own claims
//     (sub, aud, exp, kubernetes.io/serviceaccount/{namespace,name,uid}); this
//     package does not parse it, it hands it to TokenReview and reads the
//     verdict.
//
// This rewrite ADDS: a new authorization boundary K7 introduces that §8.5's
// compose form never needed — verifying WHICH ServiceAccount presented the
// token, not merely that some K8s-issued token is valid. §4.1 names the exact
// subject expected: "system:serviceaccount:osiris-agents:bootstrap-master".
// A valid K8s token for any OTHER ServiceAccount authenticates successfully at
// the TokenReview layer and MUST still be refused — see Guard.Verify.
//
// ── WHAT DID NOT CHANGE ────────────────────────────────────────────────────
//
// Everything below this comment that is not about token issuance:
//
//   - W1 ("조직이 이미 있으면 genesis 주체를 받아들이지 않는다", 70 §4.2 row
//     W1: "무변경. 판정 주체·조건 동일"). §8.5's build checked this in the
//     HANDLER (core/internal/api/onboarding.go's handleIssueGenesisToken,
//     now deleted) before calling Store.Issue. There is no issuance step to
//     hang that check off of anymore, so it moves INTO the verification path
//     — Guard.Verify — which is what "70 §4.1's 흐름: core가
//     currentOrgRevision == 0 확인(W1) → … → core가 TokenReview로 검증" prescribes:
//     W1 and the TokenReview verification happen together, on the same call,
//     because there is no longer a separate issuance call for W1 to gate.
//   - AllowedRoutes / Allows — byte-for-byte unchanged. §8.5.1/F14's
//     allowlist-not-denylist argument is about SCOPE, and K7 only changes
//     AUTHENTICATION. bin/lint-identity-authn.sh's GEN check asserts the
//     literal shape of this list and must keep passing untouched.
package genesis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Route is one (method, path) pair. Both halves matter: granting a path
// without a method would let a POST reach a route intended as a read.
type Route struct {
	Method string
	Path   string
}

func (r Route) String() string { return r.Method + " " + r.Path }

// AllowedRoutes is the genesis principal's ENTIRE reach.
//
// UNCHANGED BY K7 — see the package doc comment. §8.5's table, as amended by
// §8.5.1:
//
//	POST /v1/onboarding/draft      초안 검증 (그래프 쓰기 없음)
//	GET  /v1/onboarding/status     자기 진행 상황·조직 요약
//	GET  /v1/onboarding/catalog    카탈로그 읽기 전용 (O3: 선택이지 발명이 아니다)
//
// NOT HERE, AND THE ABSENCES ARE LOAD-BEARING:
//
//   - POST /v1/onboarding/commit — §8.5.1/F14. The commit is called by the
//     session of the human who approved the draft (30 §3.4.3): the master-key
//     session on the first run, an org.admin person session afterwards.
//   - Every /v1/* graph-write route — teams, roles, people, agents,
//     memberships, org-roles, deactivate, additive-profile, deny,
//     assignment-policies. §8.5: "그 외 전부 403".
//   - /v1/invariants, /v1/revision — internal org state on the platform token.
//     The genesis subject gets what it needs through the status route instead,
//     which is scoped to what a consultant must know.
var AllowedRoutes = []Route{
	{Method: http.MethodPost, Path: "/v1/onboarding/draft"},
	{Method: http.MethodGet, Path: "/v1/onboarding/status"},
	{Method: http.MethodGet, Path: "/v1/onboarding/catalog"},
}

// Allows reports whether the genesis scope covers this request.
//
// DEFAULT DENY. The loop compares against the allowlist and returns false when
// it falls through, which is the behavior that makes "adding a route to core
// does not widen genesis" true without anyone remembering it.
func Allows(method, path string) bool {
	for _, a := range AllowedRoutes {
		if a.Method == method && a.Path == path {
			return true
		}
	}
	return false
}

var (
	// ErrNoToken / ErrUnauthenticated / ErrWrongIdentity / ErrOrgExists /
	// ErrOrgUnreadable all mean 401/403 to an HTTP caller, and are
	// distinguished only so core's own logs can say which, without the
	// response body doing so (same discipline §8.5's build already applied).
	ErrNoToken         = errors.New("no genesis token presented")
	ErrUnauthenticated = errors.New("genesis token failed K8s TokenReview authentication")
	// ErrWrongIdentity is the K7 authorization boundary the package doc
	// comment calls out: a token that IS a valid K8s identity but is not the
	// one expected ServiceAccount must be refused just as hard as an invalid
	// token. Accepting any authenticated Pod's token here would trade §8.5's
	// old trust argument (only orchestrator can create a container) for a
	// much weaker one (any Pod in the cluster can authenticate) — worse, not
	// equivalent.
	ErrWrongIdentity = errors.New("token authenticated, but not as the expected bootstrap-master ServiceAccount")
	ErrOrgExists     = errors.New("an organization already exists; the genesis window is closed")
	ErrOrgUnreadable = errors.New("cannot determine whether an organization exists; refusing (fail-closed)")
)

// Identity is what a successful TokenReview tells this package about the
// presented token — the K8s-native replacement for §8.5's
// "발급 시 컨테이너 ID에 바인딩" (W4): TokenReview's response IS the binding,
// computed by the API server, not by this process (70 §4.2 row W4).
type Identity struct {
	Authenticated  bool
	Namespace      string // e.g. "osiris-agents"
	ServiceAccount string // e.g. "bootstrap-master"
	UID            string // the ServiceAccount's K8s UID — see the package doc's W3 note
}

// TokenReviewer is the seam this package verifies tokens through. Kept as an
// interface — never a concrete k8s.io/client-go type reaching into this
// package's callers — for exactly the reason §110's genesisboot kept its own
// seam: "순서 검증을 위해 인터페이스를 둔 것과 같은 이유" (tests substitute a
// fake; production wires the real K8s TokenReview API call, in
// tokenreview_k8s.go).
type TokenReviewer interface {
	Review(ctx context.Context, token string) (Identity, error)
}

// OrgRevisionReader is the minimal seam W1 needs — reading
// :OrgRevision-equivalent state — without this package importing
// osiris-core/internal/graphdb (which would pull a Neo4j driver dependency
// into a package whose entire job, per the file-level doc comment, is
// staying small). core/internal/api adapts graphdb.Client.ReadOrgSummary to
// this one-method shape.
type OrgRevisionReader interface {
	CurrentOrgRevision(ctx context.Context) (int64, error)
}

// Guard is what replaced Store. It holds no token — there is nothing to
// remember between requests anymore (see the package doc's "WHAT CHANGED"
// section) — only the two things every Verify call needs: how to ask K8s
// whether a token is valid, and how to ask the graph whether the genesis
// window is still open.
type Guard struct {
	reviewer       TokenReviewer
	org            OrgRevisionReader
	namespace      string // expected ServiceAccount namespace, "osiris-agents" (K5/K10)
	serviceAccount string // expected ServiceAccount name, "bootstrap-master" (§4.1)
}

// NewGuard wires the two seams plus the expected identity. namespace/
// serviceAccount are NOT discovered at runtime — they are this system's one
// permitted genesis subject, named in 70 §4.1 ("system:serviceaccount:
// osiris-agents:bootstrap-master") and configured the same way §8.5's build
// configured its one CORE_API_TOKEN: as an operator-supplied constant, not a
// value taken from the request.
func NewGuard(reviewer TokenReviewer, org OrgRevisionReader, namespace, serviceAccount string) *Guard {
	return &Guard{reviewer: reviewer, org: org, namespace: namespace, serviceAccount: serviceAccount}
}

// Verify is genesisAuth's one call. It replaces BOTH the old Store.Verify
// (token validity) AND the W1 check that used to live in
// handleIssueGenesisToken (org-revision gate) — see the package doc's
// explanation of why W1 moved here: there is no separate issuance step left
// for it to guard.
//
// ORDER MATTERS, and is deliberate:
//  1. TokenReview authentication — is this a live K8s identity at all.
//  2. Identity match — is it SPECIFICALLY osiris-agents:bootstrap-master
//     (ErrWrongIdentity — the K7 boundary the package doc comment names).
//  3. W1 — even for the right ServiceAccount, is the genesis window still
//     open. Checked LAST and on every call (not cached), so a request that
//     arrives after a commit — during the up-to-60-second SA-deletion grace
//     window §4.3 measured — is refused by the graph state even in the
//     narrow interval where the SA's token might still pass step 1
//     (see the package doc's pointer to 70 §4.3 for that timing).
//
// FAIL CLOSED on an unreadable graph, exactly as §8.5's build already
// documented for the old issuance handler: reading an outage as "revision 0"
// would accept a bootstrap credential into a living organization by
// unplugging a database.
func (g *Guard) Verify(ctx context.Context, token string) (Identity, error) {
	if strings.TrimSpace(token) == "" {
		return Identity{}, ErrNoToken
	}
	ident, err := g.reviewer.Review(ctx, token)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}
	if !ident.Authenticated {
		return Identity{}, ErrUnauthenticated
	}
	if ident.Namespace != g.namespace || ident.ServiceAccount != g.serviceAccount {
		return Identity{}, ErrWrongIdentity
	}
	rev, err := g.org.CurrentOrgRevision(ctx)
	if err != nil {
		return Identity{}, ErrOrgUnreadable
	}
	if rev >= 1 {
		return Identity{}, ErrOrgExists
	}
	return ident, nil
}

// Fingerprint is a LOG-SAFE identifier for a presented token: the first 8 hex
// chars of its sha256, exactly the shape §8.5's build already used for the
// same reason (audit lines that need to say WHICH token without saying what
// it is). It is now a pure function of the token rather than a Store method,
// because there is no store left to hold state for it to read.
func Fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:4])
}
