// Package orgstate evaluates ONE predicate, and it exists as its own package
// because that predicate is written down in four different design sections and
// must be the same predicate in all four:
//
//	H10 closed  :=  currentOrgRevision >= 1  ∧  (org.admin 보유자 중 자격증명 보유자 >= 1)
//
//	- 60-identity-authn.md §4.3 — the two conditions that retire the master key
//	- §19.4 — the bootstrap machine-principal class is valid iff ¬H10closed,
//	  "문자 그대로 같은 술어를 평가한다"
//	- §19.5 — why master-key holders are the only issuers before an org exists
//	- MP1 — "요청 시점마다 재평가되는 상태 조건 ... 유출된 bootstrap 토큰은
//	  온보딩이 끝나는 순간 죽는다"
//
// Putting it in one place is what makes MP1 true rather than aspirational: a
// bootstrap principal is not checked against a flag that someone remembered to
// flip, it is checked against the world, on every request.
//
// HI2 COMPLIANCE: no Cypher and no neo4j driver import here. The graph read
// lives in internal/graphread (osiris-api's single point of contact with
// Neo4j); this package takes it as an interface.
package orgstate

import (
	"context"
	"fmt"

	"osiris-api/internal/authz"
)

// RevisionReader is the `currentOrgRevision >= 1` half. Satisfied by
// *graphread.Client.
type RevisionReader interface {
	CurrentOrgRevision(ctx context.Context) (int64, error)
}

// CredentialDirectory is the `org.admin 보유자 중 자격증명 보유자 >= 1` half.
//
// The two conjuncts are separate interfaces on purpose: the first is a graph
// fact, the second is a JetStream KV fact, and the graph does not know that
// the KV store exists (H1's one-way reference). Keeping them apart is what
// lets each be read from its own owner without either learning about the
// other.
//
// [2026-07-27] The implementation is GraphCredentialDirectory below. The
// previous build's NoCredentialStore — a named type returning a MEASURED zero,
// because neither org.admin nor AUTHN_CRED existed yet — has been deleted now
// that both do; see that type's replacement comment for why it was removed
// rather than kept as a fallback.
type CredentialDirectory interface {
	// CountHoldersWithCredential returns how many parties both hold `code`
	// and possess a usable login credential.
	//
	// An error means UNDETERMINABLE. It must never be conflated with 0.
	CountHoldersWithCredential(ctx context.Context, code authz.Code) (int, error)
}

// ===========================================================================
// [2026-07-27] NoCredentialStore IS GONE. This is what replaced it.
//
// The previous build shipped `type NoCredentialStore struct{}` returning 0,
// and its doc comment insisted — correctly — that this was a MEASURED zero and
// not an assumed one: no :Party could hold org.admin (the code was not in the
// seeded :Permission catalog) and no :Party could hold a credential (AUTHN_CRED
// did not exist). A conjunction of two proven zeros is zero.
//
// This task made both halves real. org.admin is now seeded
// (core/internal/graphdb/seed.go) and AUTHN_CRED now exists
// (internal/authncred), so the measurement that justified the constant no
// longer holds and the constant had to go. It was DELETED rather than left
// beside the real implementation: a named type that returns "no admins can log
// in" is one wiring mistake away from re-opening the master-key window
// (§4.3's second condition) and re-validating every bootstrap machine
// principal (MP1). Leaving it available as a fallback would be leaving the
// footgun that its own doc comment was written to prevent.
//
// GraphCredentialDirectory computes the intersection the design describes, and
// keeps the two stores ignorant of each other exactly as H1 requires:
//
//	graph  →  who effectively holds org.admin and is active   (PermissionHolders)
//	KV     →  which of THOSE ids have a usable verifier       (CredentialChecker)
//
// The graph is never told that AUTHN_CRED exists; it is only asked for a list
// of ids. The KV is never told what org.admin means; it is only asked whether
// it has a record for each id. The join happens here, in osiris-api, which is
// the only component the design allows to know both facts.
// ===========================================================================

// PermissionHolders is the graph half. Satisfied by *graphread.Client.
type PermissionHolders interface {
	EffectiveHolders(ctx context.Context, code string) ([]string, error)
}

// CredentialChecker is the KV half. Satisfied by *authncred.Store.
type CredentialChecker interface {
	CountAmong(ctx context.Context, personIDs []string) (int, []string, error)
}

// GraphCredentialDirectory implements CredentialDirectory.
type GraphCredentialDirectory struct {
	holders PermissionHolders
	creds   CredentialChecker
}

func NewCredentialDirectory(holders PermissionHolders, creds CredentialChecker) *GraphCredentialDirectory {
	return &GraphCredentialDirectory{holders: holders, creds: creds}
}

// CountHoldersWithCredential returns |{p : code ∈ effective(p) ∧ p is active ∧
// AUTHN_CRED[p] exists}|.
//
// EVERY FAILURE PATH RETURNS AN ERROR, NEVER 0. That is the whole discipline of
// this function. Zero credentialed administrators is the state that keeps the
// master-key window OPEN (§4.3) and keeps bootstrap machine principals VALID
// (§19.4/MP1); reporting a graph outage or a NATS outage as zero would
// therefore resurrect a bootstrap credential by unplugging a dependency — the
// precise inversion §8.5 forbids and the reason Closed() propagates the error
// instead of choosing a default.
func (d *GraphCredentialDirectory) CountHoldersWithCredential(ctx context.Context, code authz.Code) (int, error) {
	if d == nil || d.holders == nil {
		return 0, fmt.Errorf("permission holder source not wired")
	}
	if d.creds == nil {
		return 0, fmt.Errorf("credential store not wired")
	}
	ids, err := d.holders.EffectiveHolders(ctx, string(code))
	if err != nil {
		return 0, fmt.Errorf("cannot list %s holders: %w", code, err)
	}
	if len(ids) == 0 {
		// A DETERMINATE zero: the graph answered, and nobody holds the code.
		// Distinct from the error paths above, and correct — an organization
		// with no administrator has no credentialed administrator either.
		return 0, nil
	}
	n, _, err := d.creds.CountAmong(ctx, ids)
	if err != nil {
		return 0, fmt.Errorf("cannot check credentials for %s holders: %w", code, err)
	}
	return n, nil
}

// CredentialedHolderIDs returns the ids themselves, for DG4's API-layer
// precondition (60 §21.1). Same error contract.
func (d *GraphCredentialDirectory) CredentialedHolderIDs(ctx context.Context, code authz.Code) ([]string, error) {
	if d == nil || d.holders == nil || d.creds == nil {
		return nil, fmt.Errorf("credential directory not wired")
	}
	ids, err := d.holders.EffectiveHolders(ctx, string(code))
	if err != nil {
		return nil, fmt.Errorf("cannot list %s holders: %w", code, err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	_, have, err := d.creds.CountAmong(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("cannot check credentials for %s holders: %w", code, err)
	}
	return have, nil
}

var _ CredentialDirectory = (*GraphCredentialDirectory)(nil)

// Evaluator answers the H10 question.
type Evaluator struct {
	rev   RevisionReader
	creds CredentialDirectory
}

// New builds an Evaluator. A nil RevisionReader is permitted and is NOT
// treated as "revision 0": osiris-api starts in a degraded mode when Neo4j is
// unreachable at boot (cmd/osiris-api/main.go), and in that mode the predicate
// is undeterminable, so Closed returns an error and every caller denies.
// Reading "graph is missing" as "no org exists" would resurrect the bootstrap
// window by unplugging a database — the precise inversion §8.5 forbids.
func New(rev RevisionReader, creds CredentialDirectory) *Evaluator {
	return &Evaluator{rev: rev, creds: creds}
}

// Closed reports whether the H10 window has closed.
//
//	closed := currentOrgRevision >= 1 ∧ credentialedHolders(org.admin) >= 1
//
// Returns an error if either conjunct is undeterminable. Callers MUST deny on
// error — this function does not pick a default for them, because the two
// callers want the same answer for different reasons and a silent default
// here would be invisible at both call sites.
//
// Short-circuiting is deliberate and is also what makes today's stub safe: if
// no org exists, the credential half is never consulted at all, so the
// bootstrap window stays open during onboarding exactly as §19.5 describes,
// regardless of what the credential directory would have said.
func (e *Evaluator) Closed(ctx context.Context) (bool, error) {
	if e.rev == nil {
		return false, fmt.Errorf("org revision undeterminable: no graph connection")
	}
	n, err := e.rev.CurrentOrgRevision(ctx)
	if err != nil {
		return false, fmt.Errorf("org revision undeterminable: %w", err)
	}
	if n < 1 {
		return false, nil
	}
	if e.creds == nil {
		return false, fmt.Errorf("credentialed org.admin count undeterminable: no credential directory wired")
	}
	admins, err := e.creds.CountHoldersWithCredential(ctx, authz.PermOrgAdmin)
	if err != nil {
		return false, fmt.Errorf("credentialed org.admin count undeterminable: %w", err)
	}
	return admins >= 1, nil
}

// MasterWindowOpen is ¬Closed — §19.4's bootstrap-class validity predicate,
// spelled the way the design spells it so the negation is impossible to lose
// in translation. Same error contract: an error means deny, not allow.
func (e *Evaluator) MasterWindowOpen(ctx context.Context) (bool, error) {
	closed, err := e.Closed(ctx)
	if err != nil {
		return false, err
	}
	return !closed, nil
}
