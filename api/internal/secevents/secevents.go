// Package secevents publishes the `security.*` audit events
// 60-identity-authn.md §6.3 routes to the JetStream TASKS stream (90 days).
//
// This build uses only the §19.7 subset — `security.machine.*` — because that
// is the only credential lifecycle this task implements. `security.authn.*`,
// `security.enrollment.*`, `security.breakglass.*` and `security.rbac.*` are
// the later tasks' and land here unchanged when they arrive.
//
// WHY PUBLISHING HERE IS FAIL-CLOSED FOR ISSUANCE. MP6: "발행에 실패하면
// 발급하지 않는다(fail-closed, §9.4와 같은 규칙)." A credential that exists
// without an audit record is exactly the "조용히 존재하는 자격증명" MP6 is
// written to make impossible, so Publish returning an error must abort the
// issue, not be logged and shrugged off. Revocation is the opposite: it is
// best-effort-logged, because refusing to revoke a credential when the audit
// bus is down would keep a compromised token alive to protect a log line.
//
// SECRET HYGIENE. Nothing in this package's payloads may contain a token, a
// session id, a password or a ticket — §8.4/HI10, and this repository's own
// incident history (§79: a NATS URL with an embedded password written straight
// to a log line). The payload builders below take structured fields, never a
// free-form map from a caller, so there is no place for a secret to be handed
// in by accident.
package secevents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// Subject prefix and the §19.7 event names.
const (
	SubjectPrefix = "security.machine."

	MachineIssued       = SubjectPrefix + "issued"
	MachineFirstUsed    = SubjectPrefix + "first_used"
	MachineRevoked      = SubjectPrefix + "revoked"
	MachineExpired      = SubjectPrefix + "expired"
	MachineRateExceeded = SubjectPrefix + "rate_exceeded"
	MachineDenied       = SubjectPrefix + "denied"
)

// [2026-07-27] §6.3's other three security subject families, added by the RBAC
// task because DG5 makes one of them load-bearing rather than merely useful.
//
// security.rbac.* IS DG5's event half and is FAIL-CLOSED at the call site:
// §21.1 says a role change, deactivation or ticket issuance targeting an
// org.admin holder must be visible to every other administrator, and that
// "발행 실패 시 변경하지 않는다". So the publish happens BEFORE osiris-api calls
// osiris-core, and a publish error aborts the operation — the same ordering
// MP6 uses for machine principal issuance, and for the same reason: a change
// that happened with no audit record is the thing being prevented.
//
// The :Notification fan-out that accompanies it is NOT here. It is a graph
// write and therefore osiris-core's, and it runs inside the SAME transaction
// as the change, which is strictly stronger than this ordering: notification
// and change commit or roll back together.
const (
	RBACPrefix = "security.rbac."

	RBACRoleAssigned      = RBACPrefix + "role_assigned"
	RBACRoleRevoked       = RBACPrefix + "role_revoked"
	RBACPersonCreated     = RBACPrefix + "person_created"
	RBACPersonDeactivated = RBACPrefix + "person_deactivated"
	RBACProfileChanged    = RBACPrefix + "profile_changed"
	RBACDenyChanged       = RBACPrefix + "deny_changed"
	RBACDelegationDenied  = RBACPrefix + "delegation_denied"

	EnrollmentPrefix = "security.enrollment."

	EnrollmentIssued   = EnrollmentPrefix + "issued"
	EnrollmentRedeemed = EnrollmentPrefix + "redeemed"

	AuthnPrefix = "security.authn."

	AuthnLoginFailed       = AuthnPrefix + "login_failed"
	AuthnLoginSucceeded    = AuthnPrefix + "login_succeeded"
	AuthnSessionPromoted   = AuthnPrefix + "session_promoted"
	AuthnCredentialChanged = AuthnPrefix + "credential_changed"

	// --- master key, H23 / §4.5 -------------------------------------------
	//
	// MasterKeyRetired records the moment the H10 window is OBSERVED closed and
	// the file removed. It is an observation, not the closure itself: the
	// closure is a property of the world that the predicate re-derives on every
	// request, so this event says "the 자료 차원 caught up", never "the key was
	// switched off here".
	//
	// MasterKeyPurgeFailed is the one §4.5 names by name: "삭제 실패는 로그
	// 경고 + security.masterkey.purge_failed 이벤트이지 로그인 실패가 아니다."
	// It is an alarm about a HOST condition (an undeletable 0600 file), not an
	// authorization outcome — the login was already refused by the predicate,
	// and the two dimensions being independent is exactly what makes a failure
	// here safe to merely report.
	MasterKeyPrefix = "security.masterkey."

	MasterKeyRetired     = MasterKeyPrefix + "retired"
	MasterKeyPurgeFailed = MasterKeyPrefix + "purge_failed"
)

// Publisher wraps the JetStream context osiris-api already holds for SSE.
type Publisher struct {
	js nats.JetStreamContext
}

func New(js nats.JetStreamContext) *Publisher { return &Publisher{js: js} }

// Event is the closed payload shape. Every field is either a public
// identifier, a timestamp, or an outcome — deliberately no `map[string]any`
// escape hatch (see the package doc's secret-hygiene note).
type Event struct {
	// ActorRef is §19.7's event-only actor namespace:
	// party:<id> | machine:<principalId> | master-key | system.
	// NEVER written to a graph attribution field — see authz.Principal.ActorRef.
	ActorRef string `json:"actorRef"`

	// PrincipalID is the machine principal this event is ABOUT (which is not
	// necessarily the actor: an issue event's actor is the issuer).
	PrincipalID string `json:"principalId,omitempty"`

	Class       string    `json:"class,omitempty"`
	Permissions []string  `json:"permissions,omitempty"`
	ExpiresAt   time.Time `json:"expiresAt,omitempty"`
	At          time.Time `json:"at"`

	// Reason is a fixed, enumerated string (not an error message, which could
	// carry values from the request).
	Reason string `json:"reason,omitempty"`

	// --- [2026-07-27] RBAC / enrollment fields (§6.3, §21.1 DG5) ----------
	//
	// Still no map[string]any and still no free-form string from a request
	// body: every field below is a :Party id, a :Role id, a permission code or
	// an enumerated rule name. A ticket value, a password or a session id has
	// no field to land in, which is the package's secret-hygiene property
	// expressed in the type rather than in a review checklist.

	// TargetPartyID is the :Person this action is ABOUT (the actor is
	// ActorRef). Kept distinct for the same reason §19.7 keeps actorRef and
	// actorPartyId distinct: collapsing them is how the wrong value ends up in
	// an attribution field.
	TargetPartyID string `json:"targetPartyId,omitempty"`

	// RoleID / ProfileID / PermCode name WHAT changed. They are audit data,
	// never authorization inputs — HI17 forbids deciding anything on a role or
	// profile id, and this struct is not on the decision path.
	RoleID    string `json:"roleId,omitempty"`
	ProfileID string `json:"profileId,omitempty"`
	PermCode  string `json:"permCode,omitempty"`

	// Rule is the delegation rule that refused, for security.rbac.denied:
	// "DG1" | "DG1'" | "DG2" | "DG3" | "DG4".
	Rule string `json:"rule,omitempty"`

	// Revision is the :OrgRevision this change produced, so an auditor can
	// join the event stream to Q8's graph history.
	Revision int64 `json:"revision,omitempty"`
}

// Publish sends one event and WAITS for the JetStream PubAck. Waiting is the
// point: a fire-and-forget core-NATS publish would "succeed" even when no
// stream captures the subject, which would turn MP6's fail-closed guarantee
// into a no-op the day someone edits the stream's subject list.
func (p *Publisher) Publish(ctx context.Context, subject string, ev Event) error {
	if p == nil || p.js == nil {
		return fmt.Errorf("security event bus unavailable")
	}
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal security event: %w", err)
	}
	if _, err := p.js.Publish(subject, body, nats.Context(ctx)); err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	return nil
}
