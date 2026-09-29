// Package authz holds the ONE authorization vocabulary and the ONE
// authorization decision function this codebase is allowed to have.
//
// docs/design/60-identity-authn.md §19.6 is explicit about why this package
// exists and why it is this small:
//
//	authorize(principal, requiredCode) := requiredCode ∈ principal.permissions
//
//	  principal.permissions 의 출처만 다르다:
//	    kind:"person"     → effective(party)              (20 §7.1, 무변경)
//	    kind:"master-key" → osiris-api 고정 스코프
//	    kind:"machine"    → AUTHN_MACHINE 레코드의 고정 배열
//
// "판정 지점은 하나다(HI16). 어휘도 하나다. 기계 주체를 위해 만든 새 권한
// 코드는 0개다."
//
// So: the five codes below are §20.4's five codes — NOT machine-specific ones —
// and Authorize below is the single decision point. The machine-principal work
// (§19) consumes them; the RBAC work (§20/§21) will produce them from
// effective(party) WITHOUT changing this file's shape. That is the whole point
// of writing this package now instead of later.
//
// WHAT THIS PACKAGE DELIBERATELY DOES NOT CONTAIN (§20/§21 — the next task):
//   - roles, permission profiles, IN_ORG membership, delegation rules DG1~DG5
//   - any query against Neo4j (HI2's spirit: authorization vocabulary is pure;
//     the graph read that PRODUCES a person's codes belongs to the resolver
//     that gets plugged into PermissionSource below, not to this package)
//   - the :Permission graph catalog seed (20 §9.2 step 3). These constants are
//     the Go half of I9's "two places must agree" pair; the graph half is
//     seeded by the RBAC task, which is also when :Permission.catalogVersion
//     and the orchestrator grant-catalog get bumped together.
package authz

import (
	"fmt"
	"sort"
	"strings"
)

// Code is a :Permission.code. Authorization is decided on these values and
// ONLY on these values — never on a role id, a profile id, or a role title
// (§20.3's rule, checked by HI17). Typing it as its own kind rather than
// `string` is what makes an accidental `principal.Permissions` containing
// "role-org-admin" a compile error instead of a silent security hole.
type Code string

// The five codes of docs/design/60-identity-authn.md §20.4, transcribed. Also
// present as five rows in 10-core-architecture.md §8.1's grant-catalog table,
// where all four projection columns are "—" on purpose: these are console
// authorization codes, and projecting them into the agent runtime would be a
// design error (that section says so in as many words).
const (
	// PermOrgAdmin is the top of delegation — the only code that may grant or
	// revoke a profile containing itself (§20.4). Machine principals can never
	// hold it (see MachineGrantable).
	PermOrgAdmin Code = "org.admin"

	// PermOrgManagePeople covers :Person creation/deactivation and enrollment
	// ticket issuance. Deliberately separable from role granting.
	PermOrgManagePeople Code = "org.manage.people"

	// PermOrgManageRoles covers org role membership creation/termination.
	PermOrgManageRoles Code = "org.manage.roles"

	// PermOrgReadOps is operational read: agent health, cluster, event stream,
	// the Ops screen. Shared by managers and machine principals.
	PermOrgReadOps Code = "org.read.ops"

	// PermOrgReadOnboarding is onboarding read, INCLUDING step0 status, and
	// explicitly not onboarding writes. This is the code
	// IMPLEMENTATION-NOTES.md §81's completion conditions actually need.
	PermOrgReadOnboarding Code = "org.read.onboarding"

	// PermActionTypeWrite is docs/design/80-action-layer.md §4.5/§5.1's
	// addition — AD3: "새 권한 코드는 정확히 1개: actiontype.write. 실행
	// 인가 어휘는 한 글자도 늘리지 않는다." It authorizes the DEFINITION
	// plane only (creating/editing :ActionType rows via
	// authz.ActionEditActionType, delegation.go) and is never consumed by
	// authorize(principal, code) for an EXECUTION request — §5.1.1's table
	// keeps execution's vocabulary ("git.commit", "msg.external.send", …)
	// untouched. It is the fourth and newest member of HumanOnly
	// (humanonly.go) — see that file for why it is not the same axis as
	// MachineGrantable below.
	PermActionTypeWrite Code = "actiontype.write"
)

// ReadPrefix is the allowlist prefix of §19.6/MP3: a machine principal's
// scope may contain nothing else. Enforced at ISSUE time (MachineGrantable,
// called by the issuing handler) as well as at decision time — enforcing it
// only at decision time would let a malformed record be written to
// AUTHN_MACHINE and then rely forever on the reader to be careful.
const ReadPrefix = "org.read."

// Catalog is the closed set of codes this build knows. A code outside it is
// rejected on input rather than stored and puzzled over later.
//
// Ordering is the §20.4 table's ordering, not alphabetical, so a reader
// diffing the two sees them line up.
var Catalog = []Code{
	PermOrgAdmin,
	PermOrgManagePeople,
	PermOrgManageRoles,
	PermOrgReadOps,
	PermOrgReadOnboarding,

	// PermActionTypeWrite is 80's addition, appended rather than interleaved
	// into the §20.4 ordering above so a reader diffing this list against
	// that table still sees the original five line up unchanged. It MUST be
	// here: FromStrings (below) drops any code the graph carries that this
	// Catalog does not know, and a dropped actiontype.write would silently
	// authorize nothing for a legitimate holder while looking, from the
	// caller's side, exactly like "this person never had it".
	PermActionTypeWrite,
}

// InCatalog reports whether c is a known code.
func InCatalog(c Code) bool {
	for _, k := range Catalog {
		if k == c {
			return true
		}
	}
	return false
}

// MachineGrantable reports whether c may appear in a machine principal's fixed
// scope array — §19.6's "발급 API가 org.read. 접두가 아닌 코드를 거부
// (허용목록 상수)" and MP3's "권한 상승이 표현 불가능하다".
//
// Note the conjunction: in-catalog AND read-prefixed. A future code named
// "org.read.something" that nobody added to Catalog is still refused, so a
// typo in an issue request cannot mint a scope nobody audited.
//
// NOT THE SAME AXIS AS HumanOnly (humanonly.go). MachineGrantable is an
// ALLOWLIST for a bearer-token machine principal's scope (§19, never a
// :Party); HumanOnly is a DENYLIST of four codes an :AIAgent (a :Party, P1's
// "same superclass as :Person") may never be granted. actiontype.write fails
// MachineGrantable trivially (it has no org.read. prefix) but that is
// incidental — the two checks answer different questions about different
// kinds of principal, and merging them would hide that difference. See
// humanonly.go's doc comment for the full argument.
func MachineGrantable(c Code) bool {
	return InCatalog(c) && strings.HasPrefix(string(c), ReadPrefix)
}

// MachineGrantableCodes is the concrete allowlist HI14 ② greps for. Derived
// from Catalog + MachineGrantable rather than written out a second time, so
// the two can never drift.
func MachineGrantableCodes() []Code {
	out := make([]Code, 0, len(Catalog))
	for _, c := range Catalog {
		if MachineGrantable(c) {
			out = append(out, c)
		}
	}
	return out
}

// Kind enumerates the principal kinds §3.1.1 distinguishes. The distinction is
// load-bearing: H19/H28 say the actor is derived from the server's own
// interpretation of an authentication record, and "which record" is exactly
// this field.
type Kind string

const (
	// KindPerson is a browser session bound to a :Person (§3.1.1). Not yet
	// reachable in this build — no :Person credential store exists (that is
	// the password/enrollment work of §5, a later task) — but the kind is
	// defined now because PersonOnly's contract is written in terms of it.
	KindPerson Kind = "person"

	// KindOperator is this build's console session cookie and the legacy
	// `Authorization: Bearer <setup token>` header.
	//
	// It is NOT KindPerson and it is NOT KindMachine. It is the credential
	// §102.2 row (a) will become once H10 renames it to the master key
	// (`osk_` prefix, single-use exchange, dies with the org) — that rename is
	// explicitly a LATER task (IMPLEMENTATION-NOTES.md §102.4: "(c) → H10
	// 순서를 지킬 것"), so this build keeps the existing two paths working
	// verbatim and only labels them.
	//
	// Authorization-wise it carries osiris-api's fixed scope (§3.1's
	// "master key 세션은 ... §8.1 파생이 아니라 osiris-api에 하드코딩된 고정
	// 스코프"), which is why OperatorScope below is all five codes.
	KindOperator Kind = "operator"

	// KindMachine is §19's verification-only machine principal. Never a
	// session, never a :Party, never able to mutate (MP3/MP4/H27).
	KindMachine Kind = "machine"
)

// Principal is the authenticated subject of one request. Constructed ONLY by
// the guard from a server-side record — never from a request body, query
// parameter, or client-supplied header (H19/H28).
type Principal struct {
	Kind Kind

	// PartyID is the :Person.id for KindPerson. Empty for KindOperator (the
	// master-key session is §3.1's "the only session without a :Party") and
	// for KindMachine, where §3.1.1 says: "없음. 앞으로도 생기지 않는다".
	PartyID string

	// MachineID is the machine principal id (e.g. "mp-ci-verify"). Empty for
	// every other kind.
	MachineID string

	// Permissions is the resolved code set for THIS request. For KindMachine
	// it is the record's frozen array; for KindPerson it is effective(party)
	// resolved from the graph per request (§3.2 forbids caching it in the
	// session — see GraphPermissionSource); for KindOperator it is
	// OperatorScope.
	Permissions []Code

	// CredentialPending is §4.4: a person session whose account has not yet
	// set a credential is READ-ONLY. Only ever true for KindPerson, and the
	// value comes from the session record, never from the request.
	CredentialPending bool
}

// ActorRef is §19.7's event-only actor namespace:
//
//	party:<id> | machine:<principalId> | master-key | system
//
// KEPT SEPARATE FROM actorPartyId ON PURPOSE. §19.7's closing line: "두 필드를
// 합치면 언젠가 machine:*가 byPartyId에 들어간다." Nothing in this codebase may
// write an ActorRef into a graph attribution field; graph attribution takes a
// :Party id and machine principals do not have one, so the type system and the
// absence of the value are both in the way.
func (p Principal) ActorRef() string {
	switch p.Kind {
	case KindPerson:
		return "party:" + p.PartyID
	case KindMachine:
		return "machine:" + p.MachineID
	case KindOperator:
		return "master-key"
	default:
		return "system"
	}
}

// IsMachine is the single test PersonOnly is written in terms of.
func (p Principal) IsMachine() bool { return p.Kind == KindMachine }

// OperatorScope is the fixed scope of KindOperator (§3.1). It is all five
// codes because today's setup-token/cookie holder is the instance operator and
// already has every capability the console exposes — narrowing it here would
// be a behavioral regression on the two live auth paths this task is
// explicitly forbidden to break, not a security improvement (the same human
// can read the token file, which is host-root-equivalent per §9.1).
//
// When H10 lands this becomes the master-key session's scope and shrinks to
// §3.1's "온보딩·이벤트·인증·기계주체발급만"; when §20 lands, real people stop
// using this path entirely and get effective(party) instead. Both changes
// happen HERE, at one call site, which is the reason this is a function of the
// principal kind rather than an `if kind == ...` scattered through handlers.
func OperatorScope() []Code {
	out := make([]Code, len(Catalog))
	copy(out, Catalog)
	return out
}

// PermissionSource is the seam the RBAC task plugs into.
//
// Today osiris-api has exactly one implementation path per kind and none of
// them needs a graph query, so nothing implements this yet. It is declared now
// (signature only, §15.1's convention for "the extension point exists and is
// named") so that §20's worker adds ONE type — a resolver that runs
// effective(party) via internal/graphread — instead of threading a new
// concept through the guard.
//
// Contract: Resolve returns the code set for a :Party at request time. It must
// NOT be cached beyond §3.2's "≤10초" budget, because "권한은 그래프가
// 진실이고 조직 변경 즉시 반영되어야 한다". An error means UNDETERMINABLE, and
// every caller must fail closed on it (§8.5's standing rule).
type PermissionSource interface {
	Resolve(partyID string) ([]Code, error)
}

// Authorize is §19.6's authorize(). One line, one place, both kinds.
//
// There is deliberately no "admin bypass", no wildcard, and no role check:
// every capability question in this system reduces to "is this exact code in
// this principal's resolved set". §20's delegation rules (DG1~DG5) will be a
// SEPARATE function (authz.CheckDelegation, HI16) that answers a different
// question — "may this actor grant that code to that target" — and it will
// call this one, not replace it.
func Authorize(p Principal, required Code) bool {
	for _, held := range p.Permissions {
		if held == required {
			return true
		}
	}
	return false
}

// ParseCodes validates a caller-supplied scope list against the catalog. Used
// by the machine-principal issue handler; returns a stable, de-duplicated,
// sorted slice so that two issue requests with the same set produce byte-equal
// records.
//
// This does NOT apply the org.read. restriction — that is MachineGrantable's
// job and the issue handler applies both, in that order, so its error messages
// can distinguish "no such code" from "that code exists but a machine may not
// have it".
func ParseCodes(raw []string) ([]Code, error) {
	seen := make(map[Code]struct{}, len(raw))
	out := make([]Code, 0, len(raw))
	for _, r := range raw {
		c := Code(strings.TrimSpace(r))
		if !InCatalog(c) {
			return nil, fmt.Errorf("unknown permission code %q", r)
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// Strings renders codes for JSON/storage.
func Strings(codes []Code) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = string(c)
	}
	return out
}

// FromStrings is Strings' inverse for values read back out of storage. Unknown
// codes are DROPPED rather than surfaced: a record written by an older build
// naming a code this build no longer has must not silently authorize anything,
// and it must also not brick the principal's remaining valid codes. §19.4's
// org-class validity condition ("부여된 코드가 여전히 카탈로그에 존재") is the
// design's own statement of this rule.
func FromStrings(raw []string) []Code {
	out := make([]Code, 0, len(raw))
	for _, r := range raw {
		if c := Code(r); InCatalog(c) {
			out = append(out, c)
		}
	}
	return out
}
