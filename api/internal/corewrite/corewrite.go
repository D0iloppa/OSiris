// Package corewrite is osiris-api's client for osiris-core's graph-write API.
//
// ── WHY THIS EXISTS AT ALL ────────────────────────────────────────────────
//
// D17/G7/A5: Neo4j WRITES go through osiris-core's graph-write API and nowhere
// else. osiris-api holds a read-only Neo4j session (internal/graphread) and
// must never gain a second one that writes — so every org-role mutation of
// 60-identity-authn.md §20.6 crosses this HTTP boundary.
//
// The config fields this uses (CoreBaseURL, CoreToken) have existed, unused,
// since the first osiris-api build, with a doc comment naming this exact
// situation: "the documented landing spot for the future worker who DOES add a
// write-requiring console route". This is that worker.
//
// ── WHAT DOES *NOT* CROSS THIS BOUNDARY ───────────────────────────────────
//
// The delegation decision. authz.CheckDelegation runs BEFORE any call here and
// its outcome is not transmitted — core is not told "this was allowed", it is
// told "person X asks to do Y as actor Z", and it applies its own, different
// check (§6.5's valid-actor rule and the in-transaction I14~I17 re-check).
// 60 §21.4-1 is explicit that core must not hold a copy of DG1~DG5, and the
// request shapes below are deliberately incapable of carrying one.
//
// byPartyId is always derived from the authenticated session by the caller
// (H19/H28). There is no code path in this package that can be handed an actor
// id from a request body — the handler passes a value it obtained from
// authz.Principal, which only the guard writes.
package corewrite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to osiris-core's /v1/* write surface.
type Client struct {
	base  string
	token string
	http  *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		base:  strings.TrimRight(baseURL, "/"),
		token: token,
		// Generous but bounded: a write transaction runs the I14~I17 re-check
		// and the DG5 notification fan-out inside it. Unbounded would let a
		// stuck core hold a console request forever.
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

// Configured reports whether this client can actually be used. osiris-api
// starts even without a core token (its onboarding surface does not need one),
// so the org-role handlers must be able to answer 503 rather than panic.
func (c *Client) Configured() bool { return c != nil && c.base != "" && c.token != "" }

// Result is core's uniform write response: the new revision and the subject id.
type Result struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

// Error carries core's status and message so the handler can map 400 →
// 400 (bad request), 403 → 403 (§6.5's actor rule), and everything else to 502.
//
// The message is core's, and core's write path never echoes a credential — it
// never sees one. Passing it through is what makes an I14 refusal legible to
// the operator ("this change would leave zero active :Person holding
// org.admin") instead of an opaque 502.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("osiris-core %d: %s", e.Status, e.Message) }

func (c *Client) post(ctx context.Context, path string, body any) (Result, error) {
	var zero Result
	if !c.Configured() {
		return zero, &Error{Status: http.StatusServiceUnavailable,
			Message: "osiris-core graph-write API is not configured for this process"}
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return zero, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(buf))
	if err != nil {
		return zero, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return zero, &Error{Status: http.StatusBadGateway, Message: "osiris-core unreachable"}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = "graph write refused"
		}
		return zero, &Error{Status: resp.StatusCode, Message: e.Error}
	}
	var out Result
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, fmt.Errorf("decode osiris-core response: %w", err)
	}
	return out, nil
}

// --- §20.6 [1] :Person creation --------------------------------------------

type PersonInput struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Timezone  string `json:"timezone"`
	ByPartyID string `json:"byPartyId"`
}

func (c *Client) CreatePerson(ctx context.Context, in PersonInput) (Result, error) {
	return c.post(ctx, "/v1/people", in)
}

// --- §20.6 [2]/[5] org role assignment and revocation -----------------------

type AssignOrgRoleInput struct {
	MembershipID string `json:"membershipId"`
	PersonID     string `json:"personId"`
	RoleID       string `json:"roleId"`
	ByPartyID    string `json:"byPartyId"`
}

func (c *Client) AssignOrgRole(ctx context.Context, in AssignOrgRoleInput) (Result, error) {
	return c.post(ctx, "/v1/org-roles/assign", in)
}

type RevokeOrgRoleInput struct {
	PersonID  string `json:"personId"`
	ByPartyID string `json:"byPartyId"`
}

func (c *Client) RevokeOrgRole(ctx context.Context, in RevokeOrgRoleInput) (Result, error) {
	return c.post(ctx, "/v1/org-roles/revoke", in)
}

// --- §20.6 [6] deactivation -------------------------------------------------

type DeactivateInput struct {
	PersonID  string `json:"personId"`
	ByPartyID string `json:"byPartyId"`
}

func (c *Client) DeactivatePerson(ctx context.Context, in DeactivateInput) (Result, error) {
	return c.post(ctx, "/v1/people/deactivate", in)
}

// --- C10's other two: HAS_PROFILE{additive} and DENIES ----------------------

type PartyGrantInput struct {
	PartyID   string `json:"partyId"`
	ProfileID string `json:"profileId,omitempty"`
	PermCode  string `json:"permCode,omitempty"`
	Attach    bool   `json:"attach"`
	ByPartyID string `json:"byPartyId"`
}

func (c *Client) SetAdditiveProfile(ctx context.Context, in PartyGrantInput) (Result, error) {
	return c.post(ctx, "/v1/parties/additive-profile", in)
}

func (c *Client) SetDeny(ctx context.Context, in PartyGrantInput) (Result, error) {
	return c.post(ctx, "/v1/parties/deny", in)
}

// --- [2026-09-11, C-1] :Approval propose/list/decide ------------------------
//
// docs/design/90-infra-topology.md §11 IN17/IN18/IN19. See
// core/internal/graphdb/writeops_approval.go for the graph semantics and
// core/internal/api/server.go's APIMux comment for why these three routes
// exist at all and what auth osiris-api itself must apply on top (this
// client performs NO authorization — it is a thin HTTP wrapper, exactly like
// every other method in this file).

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	if !c.Configured() {
		return nil, &Error{Status: http.StatusServiceUnavailable,
			Message: "osiris-core graph-write API is not configured for this process"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Status: http.StatusBadGateway, Message: "osiris-core unreachable"}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = "graph read refused"
		}
		return nil, &Error{Status: resp.StatusCode, Message: e.Error}
	}
	return raw, nil
}

// ApprovalInput mirrors graphdb.ApprovalInput (osiris-core's shape) — kept as
// a separate type here, not an import, for the same reason every other
// *Input type in this file is separate: osiris-api must never import
// osiris-core's internal packages (the HTTP boundary IS the contract).
type ApprovalInput struct {
	ID                string `json:"id"`
	ActionTypeID      string `json:"actionTypeId"`
	TargetJSON        string `json:"targetJson"`
	PayloadJSON       string `json:"payloadJson,omitempty"`
	ProposedByPartyID string `json:"proposedByPartyId,omitempty"`
	ProposedByRef     string `json:"proposedByRef"`
}

func (c *Client) CreateApproval(ctx context.Context, in ApprovalInput) (Result, error) {
	return c.post(ctx, "/v1/approvals", in)
}

// ApprovalSummary mirrors graphdb.ApprovalSummary.
type ApprovalSummary struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	ActionTypeID  string `json:"actionTypeId"`
	TargetJSON    string `json:"targetJson"`
	PayloadJSON   string `json:"payloadJson"`
	ProposedByRef string `json:"proposedByRef"`
	RequestedAt   string `json:"requestedAt"`
}

func (c *Client) ListPendingApprovals(ctx context.Context) ([]ApprovalSummary, error) {
	raw, err := c.get(ctx, "/v1/approvals/pending")
	if err != nil {
		return nil, err
	}
	var out struct {
		Proposals []ApprovalSummary `json:"proposals"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode osiris-core response: %w", err)
	}
	return out.Proposals, nil
}

type DecisionInput struct {
	ID           string `json:"id"`
	Decision     string `json:"decision"` // "approved" | "rejected"
	Comment      string `json:"comment,omitempty"`
	ByPartyID    string `json:"byPartyId"`
	DecidedByRef string `json:"decidedByRef"`
}

func (c *Client) DecideApproval(ctx context.Context, in DecisionInput) (Result, error) {
	return c.post(ctx, "/v1/approvals/decision", in)
}
