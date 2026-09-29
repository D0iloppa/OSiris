package genesis

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// ===========================================================================
// K7 rewired this package from a token MINTER (Store.Issue/Verify/Revoke) to
// a token VERIFIER (Guard.Verify calling a TokenReviewer seam) — see
// genesis.go's package doc comment for the full rationale.
//
// WHAT SURVIVES FROM THE §8.5 (compose) BUILD'S TEST FILE, AND WHY:
//
//   - TestAllowsIsDefaultDeny / TestAllowsIsMethodSensitive /
//     TestCommitIsNotInTheAllowlist — AllowedRoutes and Allows are BYTE-FOR-
//     BYTE unchanged by K7 (only authentication changed, not scope). These
//     three keep asserting exactly what they asserted before, unmodified.
//
// WHAT WAS DELETED, AND WHY (not silently — reported in this phase's work
// summary):
//
//   - TestIssueRequiresContainerID, TestIssueThenVerifyReturnsBinding,
//     TestVerifyRefusesUnprefixedAndUnknown, TestVerifyRefusesExpiredAndForgets,
//     TestRevokeIsIdempotentAndTerminal, TestIssueReplacesThePreviousToken,
//     TestLiveReportsIssuedState — every one of these tested Store, and Store
//     no longer exists: OSiris does not mint tokens under K7. The tests named
//     a mechanism (in-process issuance, TTL arithmetic, an at-most-one
//     in-memory record) K8s now owns entirely (expirationSeconds, the API
//     server's own token lifecycle). There is nothing left in this package to
//     re-test their assertions against.
//
// WHAT IS NEW, replacing the deleted tests' COVERAGE (not their code):
//
//   - TestGuardVerifyAcceptsExpectedServiceAccount — the happy path, W1 open.
//   - TestGuardVerifyRefusesWrongServiceAccount — the K7 authorization
//     boundary itself: a VALID K8s token for the wrong ServiceAccount must
//     not pass, exactly as the task's own goal condition names it ("다른 SA의
//     유효한 토큰을 받아들이면 안 된다").
//   - TestGuardVerifyRefusesWhenOrgAlreadyExists — W1, relocated from the old
//     handleIssueGenesisToken into Guard.Verify (genesis.go's Verify doc
//     comment explains the relocation).
//   - TestGuardVerifyFailsClosedOnUnreadableGraph — the standing fail-closed
//     rule §8.5 already required of the old issuance handler, now required of
//     Verify since that is where the check lives.
//   - TestGuardVerifyRefusesUnauthenticatedToken / …EmptyToken — the token-
//     shape checks Store.Verify used to do at the top of its function, now
//     Guard.Verify's job.
// ===========================================================================

func TestAllowsIsDefaultDeny(t *testing.T) {
	// The three grants of §8.5, as amended by §8.5.1.
	for _, r := range AllowedRoutes {
		if !Allows(r.Method, r.Path) {
			t.Fatalf("Allows must accept its own allowlist entry %s", r)
		}
	}
	// Anything else, including routes that plausibly "belong" to onboarding.
	denied := []Route{
		{http.MethodPost, "/v1/onboarding/commit"}, // ★ F14 — the whole point
		{http.MethodPost, "/v1/onboarding/genesis-token"},
		{http.MethodPost, "/v1/teams"},
		{http.MethodPost, "/v1/people"},
		{http.MethodPost, "/v1/org-roles/assign"},
		{http.MethodGet, "/v1/revision"},
		{http.MethodGet, "/v1/invariants"},
		{http.MethodGet, "/v1/onboarding/unknown"},
	}
	for _, r := range denied {
		if Allows(r.Method, r.Path) {
			t.Fatalf("Allows must refuse %s (default deny)", r)
		}
	}
}

// The method half of a Route is not decoration: granting a path without pinning
// the method would let a POST reach a route the design lists as a read.
func TestAllowsIsMethodSensitive(t *testing.T) {
	if Allows(http.MethodPost, "/v1/onboarding/catalog") {
		t.Fatal("catalog is a GET grant; POST to it must be refused")
	}
	if Allows(http.MethodGet, "/v1/onboarding/draft") {
		t.Fatal("draft is a POST grant; GET to it must be refused")
	}
}

// F14, asserted on the data rather than on a comment. The lint checks the source
// text; this checks the value the program actually runs on.
func TestCommitIsNotInTheAllowlist(t *testing.T) {
	for _, r := range AllowedRoutes {
		if strings.Contains(r.Path, "commit") {
			t.Fatalf("the genesis allowlist must not contain a commit route, found %s", r)
		}
	}
}

// --- test doubles ----------------------------------------------------------

type fakeReviewer struct {
	identity Identity
	err      error
}

func (f fakeReviewer) Review(_ context.Context, _ string) (Identity, error) {
	return f.identity, f.err
}

type fakeOrgReader struct {
	rev int64
	err error
}

func (f fakeOrgReader) CurrentOrgRevision(_ context.Context) (int64, error) {
	return f.rev, f.err
}

const (
	testNamespace = "osiris-agents"
	testSA        = "bootstrap-master"
)

func newTestGuard(reviewer TokenReviewer, org OrgRevisionReader) *Guard {
	return NewGuard(reviewer, org, testNamespace, testSA)
}

func TestGuardVerifyAcceptsExpectedServiceAccount(t *testing.T) {
	g := newTestGuard(
		fakeReviewer{identity: Identity{Authenticated: true, Namespace: testNamespace, ServiceAccount: testSA, UID: "uid-1"}},
		fakeOrgReader{rev: 0},
	)
	ident, err := g.Verify(context.Background(), "some-k8s-bound-token")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ident.Namespace != testNamespace || ident.ServiceAccount != testSA {
		t.Fatalf("Verify must return the reviewed identity, got %+v", ident)
	}
}

// ★ The K7 authorization boundary the task calls out explicitly: a token that
// authenticates successfully (it IS a live K8s identity) but belongs to some
// OTHER ServiceAccount must still be refused. Accepting it would trade §8.5's
// old trust argument for "any authenticated Pod in the cluster may onboard",
// which is strictly worse.
func TestGuardVerifyRefusesWrongServiceAccount(t *testing.T) {
	cases := []Identity{
		{Authenticated: true, Namespace: "osiris-agents", ServiceAccount: "some-other-agent"},
		{Authenticated: true, Namespace: "osiris-system", ServiceAccount: "bootstrap-master"}, // right name, wrong namespace
		{Authenticated: true, Namespace: "default", ServiceAccount: "default"},
	}
	for _, ident := range cases {
		g := newTestGuard(fakeReviewer{identity: ident}, fakeOrgReader{rev: 0})
		if _, err := g.Verify(context.Background(), "tok"); !errors.Is(err, ErrWrongIdentity) {
			t.Fatalf("Verify(%+v) = %v, want ErrWrongIdentity", ident, err)
		}
	}
}

func TestGuardVerifyRefusesUnauthenticatedToken(t *testing.T) {
	g := newTestGuard(fakeReviewer{identity: Identity{Authenticated: false}}, fakeOrgReader{rev: 0})
	if _, err := g.Verify(context.Background(), "tok"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Verify = %v, want ErrUnauthenticated", err)
	}
}

func TestGuardVerifyRefusesEmptyToken(t *testing.T) {
	g := newTestGuard(fakeReviewer{identity: Identity{Authenticated: true, Namespace: testNamespace, ServiceAccount: testSA}}, fakeOrgReader{rev: 0})
	if _, err := g.Verify(context.Background(), "   "); !errors.Is(err, ErrNoToken) {
		t.Fatalf("Verify(blank) = %v, want ErrNoToken (must not even call the reviewer)", err)
	}
}

// W1, relocated: 70 §4.2 row W1 — "무변경. 판정 주체·조건 동일" — the ONLY
// thing that moved is WHERE the check runs (there is no more issuance step),
// not what it decides.
func TestGuardVerifyRefusesWhenOrgAlreadyExists(t *testing.T) {
	g := newTestGuard(
		fakeReviewer{identity: Identity{Authenticated: true, Namespace: testNamespace, ServiceAccount: testSA}},
		fakeOrgReader{rev: 1},
	)
	if _, err := g.Verify(context.Background(), "tok"); !errors.Is(err, ErrOrgExists) {
		t.Fatalf("Verify = %v, want ErrOrgExists (W1)", err)
	}
}

// FAIL CLOSED: an unreadable graph must not be read as "revision 0". Same
// standing rule §8.5's build already applied to the (now deleted)
// handleIssueGenesisToken, re-asserted here since the check moved.
func TestGuardVerifyFailsClosedOnUnreadableGraph(t *testing.T) {
	g := newTestGuard(
		fakeReviewer{identity: Identity{Authenticated: true, Namespace: testNamespace, ServiceAccount: testSA}},
		fakeOrgReader{err: errors.New("neo4j unreachable")},
	)
	if _, err := g.Verify(context.Background(), "tok"); !errors.Is(err, ErrOrgUnreadable) {
		t.Fatalf("Verify = %v, want ErrOrgUnreadable (fail-closed)", err)
	}
}

func TestGuardVerifyPropagatesReviewerError(t *testing.T) {
	g := newTestGuard(fakeReviewer{err: errors.New("apiserver unavailable")}, fakeOrgReader{rev: 0})
	if _, err := g.Verify(context.Background(), "tok"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Verify = %v, want ErrUnauthenticated wrapping the transport error", err)
	}
}

// Fingerprint is what appears in logs. It must never be derivable back to the
// token.
func TestFingerprintIsNotASubstringOfToken(t *testing.T) {
	tok := "some.jwt.looking.value"
	fp := Fingerprint(tok)
	if len(fp) != 8 {
		t.Fatalf("fingerprint should be 8 hex chars, got %q", fp)
	}
	if strings.Contains(tok, fp) {
		t.Fatal("the fingerprint must not be a substring of the token")
	}
	if Fingerprint(tok) != fp {
		t.Fatal("Fingerprint must be a pure function of its input")
	}
}
