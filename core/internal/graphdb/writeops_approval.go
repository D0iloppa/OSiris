// :Approval writeops — docs/design/90-infra-topology.md §11 (IN17/IN18/IN19,
// "C-1 — 제안→승인 척추") and 20-ontology-schema.md §3.6 (the :Approval node
// this file's operations instantiate: `id`, `approvalType`, `status`,
// `requestedAt`, `decidedAt`, plus `REQUESTED_BY`/`DECIDED_BY` -> :Party).
//
// IN17: "제안은 기존 :Approval 노드의 인스턴스다 ... 새 승인 메커니즘을 만들지
// 않는다." This file adds exactly three operations against that existing node
// type — CreateApproval (propose), ListPendingApprovals (the console/MCP read
// surface), DecideApproval (approve/reject) — and nothing else. There is no
// new node label, no new constraint beyond what schema.cypher already has
// (approval_id uniqueness, approval_status index), and no execution: deciding
// an approval here only changes `status`/`decidedAt` — see DecideApproval's
// own doc comment for why that is a deliberate, load-bearing omission (C-2's
// territory, not this file's).
//
// IN18 — THE ONE HI15-SHAPED EXCEPTION, AND WHY IT IS SAFE HERE TOO.
// osiris-api's router enforces HI15 (PersonOnly on every mutating route) with
// one documented exception: POST /_api/infra/proposals, reachable by a
// machine principal (api/internal/api/server.go's route comment explains the
// router side). CreateApproval below is that route's target and is written
// to make the SAME exception safe at the graph layer: it does not require
// ByPartyID to resolve to an active :Party (contrast with, e.g.,
// requireActor/PartyIsActive-gated writes elsewhere in this package) — a
// machine principal has no :Party at all (60-identity-authn.md §19.2), so
// requiring one here would make IN18's exception impossible to exercise.
// Instead, ProposedByRef is a free-text actor-reference string the caller
// supplies (osiris-api derives it from the resolved principal — a person's
// partyId, or a machine principal's public id — never from raw request
// input; see api/internal/infra/proposals.go), recorded for audit but never
// consulted by any authorization decision. What makes this safe is IN18's
// own argument, not a weaker check: creating an :Approval changes nothing
// about what anyone can DO — the proposal is inert until DecideApproval (a
// wholly separate, much more tightly gated operation below) changes its
// status.
//
// IN19 — DecideApproval IS NOT, BY ITSELF, A SUFFICIENT GATE. This function
// has no opinion on WHO may call it — that is osiris-api's job
// (PersonOnly + RequirePerm(authz.PermOrgAdmin), api/internal/infra/
// proposals.go's Decide handler) exactly the same layering every other
// writeop in this package already assumes (actiontype.go's own doc comment:
// "authorization ... is authz.CheckDelegation's job ... evaluated upstream").
// What THIS function refuses no matter who calls it: deciding an :Approval
// that is not currently 'pending' (no re-deciding an already-decided
// proposal), and an empty ByPartyID (a decision, unlike a proposal, always
// records a real deciding actor — DecideApproval DOES require ByPartyID to
// resolve to an active :Party, the same requireActor rule every other
// mutating writeop in this package already follows, because IN19 restricts
// deciding to session-authenticated PERSONS, who always have one).
package graphdb

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// approvalStatuses is 20-ontology-schema.md §3.6's literal enum, unchanged —
// this file does not add a fourth value like "requested"; "pending" is the
// existing vocabulary and is also the value api/internal/infra/proposals.go
// (and the console) query for ("GET /_api/infra/proposals?status=pending").
var approvalStatuses = map[string]struct{}{
	"pending": {}, "approved": {}, "rejected": {}, "changes_requested": {},
}

// ApprovalInput is CreateApproval's argument — a proposal to eventually run
// one call of the named :ActionType (FOR_ACTION, 80-action-layer.md §7.6's
// relationship) against TargetJSON, carrying PayloadJSON as the (opaque,
// unexecuted) proposed manifest/parameters.
type ApprovalInput struct {
	ID           string
	ActionTypeID string // FOR_ACTION target — must exist and be status:'active'
	TargetJSON   string // required — free JSON describing what the action would touch (namespace/kind/name)
	PayloadJSON  string // optional — free JSON, stored verbatim, never parsed/executed by this build

	// ProposedByPartyID is OPTIONAL — non-empty only when the proposer is a
	// :Party (a person's session, or an :AIAgent acting through the ordinary
	// actor path). When present it is validated exactly like every other
	// ByPartyID in this package (must resolve to an active :Party) and a
	// REQUESTED_BY edge is created. When empty (the IN18 case — a bearer
	// machine principal, which has no :Party), no edge is created and only
	// ProposedByRef is recorded.
	ProposedByPartyID string

	// ProposedByRef is REQUIRED — a free-text, audit-only actor reference
	// (e.g. "person:<partyId>" or "machine:<machinePrincipalId>") the caller
	// derived from its own authenticated principal. Never consulted by any
	// authorization decision (see this file's package doc comment).
	ProposedByRef string
}

// CreateApproval inserts a new :Approval{status:'pending'} — IN17's
// "제안 생성". See this file's package doc comment for IN18's exception this
// implements (ProposedByPartyID may be empty).
func (c *Client) CreateApproval(ctx context.Context, in ApprovalInput) (WriteResult, error) {
	if in.ID == "" {
		return WriteResult{}, validationErrorf("Approval.id is required")
	}
	if in.ActionTypeID == "" {
		return WriteResult{}, validationErrorf("Approval.actionTypeId (FOR_ACTION target) is required")
	}
	if in.TargetJSON == "" {
		return WriteResult{}, validationErrorf("Approval.targetJson is required")
	}
	if in.ProposedByRef == "" {
		return WriteResult{}, validationErrorf("Approval.proposedByRef is required (audit trail — who/what proposed this)")
	}

	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		already, err := tx.Run(ctx, `MATCH (a:Approval {id: $id}) RETURN a.id`, map[string]any{"id": in.ID})
		if err != nil {
			return nil, err
		}
		if _, err := already.Single(ctx); err == nil {
			return nil, validationErrorf("Approval %q already exists", in.ID)
		}

		// FOR_ACTION target must exist and be usable (AC7-adjacent: a
		// proposal against a withdrawn/inert/draft action type would be
		// meaningless — nobody could ever lawfully decide "approve" into an
		// execution that CheckActionType-time re-validation would refuse
		// anyway, once C-2 adds execution).
		atCheck, err := tx.Run(ctx, `MATCH (at:ActionType {id: $id}) RETURN at.status AS status`, map[string]any{"id": in.ActionTypeID})
		if err != nil {
			return nil, err
		}
		atRec, err := atCheck.Single(ctx)
		if err != nil {
			return nil, validationErrorf("FOR_ACTION target :ActionType %q not found", in.ActionTypeID)
		}
		if status, _ := atRec.Get("status"); status != "active" {
			return nil, validationErrorf("FOR_ACTION target :ActionType %q is not status:'active' (got %v)", in.ActionTypeID, status)
		}

		if in.ProposedByPartyID != "" {
			pCheck, err := tx.Run(ctx, `MATCH (p:Party {id: $id}) WHERE p.closedAtRevision IS NULL RETURN p.id`,
				map[string]any{"id": in.ProposedByPartyID})
			if err != nil {
				return nil, err
			}
			if _, err := pCheck.Single(ctx); err != nil {
				return nil, validationErrorf("proposedByPartyId %q is not an active :Party", in.ProposedByPartyID)
			}
		}

		// bumpRevision's byPartyId column tolerates a non-:Party string (it
		// is an informational audit field on :OrgRevision, never re-read as
		// an FK — see bumpRevision's own Cypher, which never MATCHes it).
		// Using ProposedByRef here when there is no :Party keeps the
		// revision's own author trail honest instead of leaving it blank.
		revisionActor := in.ProposedByPartyID
		if revisionActor == "" {
			revisionActor = in.ProposedByRef
		}
		n, err := bumpRevision(ctx, tx, revisionActor, fmt.Sprintf("infra proposal %s created (%s)", in.ID, in.ActionTypeID), "approval_proposed")
		if err != nil {
			return nil, err
		}

		_, err = tx.Run(ctx, `
			CREATE (a:Approval {
				id: $id, status: 'pending', requestedAt: datetime(),
				targetJson: $targetJson, payloadJson: $payloadJson,
				proposedByRef: $proposedByRef, createdAtRevision: $rev
			})
		`, map[string]any{
			"id": in.ID, "targetJson": in.TargetJSON, "payloadJson": in.PayloadJSON,
			"proposedByRef": in.ProposedByRef, "rev": n,
		})
		if err != nil {
			return nil, err
		}

		if _, err := tx.Run(ctx, `
			MATCH (a:Approval {id: $id})
			MATCH (at:ActionType {id: $actionTypeId})
			MERGE (a)-[:FOR_ACTION]->(at)
		`, map[string]any{"id": in.ID, "actionTypeId": in.ActionTypeID}); err != nil {
			return nil, err
		}

		if in.ProposedByPartyID != "" {
			if _, err := tx.Run(ctx, `
				MATCH (a:Approval {id: $id})
				MATCH (p:Party {id: $partyId})
				MERGE (a)-[:REQUESTED_BY]->(p)
			`, map[string]any{"id": in.ID, "partyId": in.ProposedByPartyID}); err != nil {
				return nil, err
			}
		}

		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.ID}, nil
}

// ApprovalSummary is what ListPendingApprovals/the decide handler's
// not-found path surface to a caller — a flattened read projection, not the
// raw node.
type ApprovalSummary struct {
	ID            string
	Status        string
	ActionTypeID  string
	TargetJSON    string
	PayloadJSON   string
	ProposedByRef string
	RequestedAt   string
	DecidedAt     string
	DecidedByRef  string
	Comment       string
}

// ListPendingApprovals is the console/MCP read surface behind
// GET /_api/infra/proposals?status=pending — 20-ontology-schema.md §3.6's
// approval_status index (`(status, requestedAt)`) exists for exactly this
// query shape. Read-only.
func (c *Client) ListPendingApprovals(ctx context.Context) ([]ApprovalSummary, error) {
	session := c.NewReadSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, `
			MATCH (a:Approval {status: 'pending'})
			OPTIONAL MATCH (a)-[:FOR_ACTION]->(at:ActionType)
			RETURN a.id AS id, a.status AS status, at.id AS actionTypeId,
			       a.targetJson AS targetJson, a.payloadJson AS payloadJson,
			       a.proposedByRef AS proposedByRef,
			       toString(a.requestedAt) AS requestedAt
			ORDER BY a.requestedAt ASC
		`, nil)
		if err != nil {
			return nil, err
		}
		return result.Collect(ctx)
	})
	if err != nil {
		return nil, err
	}
	records, _ := res.([]*neo4j.Record)
	out := make([]ApprovalSummary, 0, len(records))
	for _, rec := range records {
		out = append(out, ApprovalSummary{
			ID:            stringOfAny(getOr(rec, "id")),
			Status:        stringOfAny(getOr(rec, "status")),
			ActionTypeID:  stringOfAny(getOr(rec, "actionTypeId")),
			TargetJSON:    stringOfAny(getOr(rec, "targetJson")),
			PayloadJSON:   stringOfAny(getOr(rec, "payloadJson")),
			ProposedByRef: stringOfAny(getOr(rec, "proposedByRef")),
			RequestedAt:   stringOfAny(getOr(rec, "requestedAt")),
		})
	}
	return out, nil
}

// DecisionInput is DecideApproval's argument.
type DecisionInput struct {
	ID       string
	Decision string // "approved" | "rejected" — the ONLY two values this function accepts (IN19: a decision is binary at this layer)
	Comment  string

	// ByPartyID is REQUIRED (unlike ApprovalInput.ProposedByPartyID above) —
	// IN19 restricts deciding to a session-authenticated PERSON, who always
	// has a :Party. osiris-api's PersonOnly+RequirePerm(org.admin) gate is
	// what actually enforces "person, and admin"; this field's non-empty,
	// active-:Party requirement is the graph-side wall that still stands if
	// osiris-api is compromised (60 §21.4-1's argument, same shape as
	// requireActor elsewhere in this codebase).
	ByPartyID string

	// DecidedByRef is a free-text actor reference for audit (mirrors
	// ApprovalInput.ProposedByRef) — always derived from the same session
	// that resolved ByPartyID, never client-supplied independently.
	DecidedByRef string
}

// DecideApproval sets status -> approved/rejected + decidedAt + DECIDED_BY.
// IN19: this is the only function in this codebase that can move an
// :Approval out of 'pending', and its only caller is meant to be
// osiris-api's POST /_api/infra/proposals/{id}/decision handler — reached
// exclusively through PersonOnly + RequirePerm(authz.PermOrgAdmin). See this
// file's package doc comment for why that gate lives in osiris-api and not
// here, and why that layering is not a weakness (60 §21.4-1's argument,
// applied to IN19 instead of DG1~DG5).
//
// DOES NOT EXECUTE ANYTHING. Approving an infra.apply/scale/delete proposal
// changes exactly one thing: this node's status. There is no k8s client
// call, no queue enqueue, no effect invocation anywhere in this function —
// C-2 (docs/design/90-infra-topology.md §11) is what turns an approved
// :Approval into a real cluster change, and it does not exist in this build.
// A caller that reads "approved" as "applied" is reading past what this
// function actually did; api/internal/infra/proposals.go's response and the
// console's Approvals screen both say so explicitly.
func (c *Client) DecideApproval(ctx context.Context, in DecisionInput) (WriteResult, error) {
	if in.ID == "" {
		return WriteResult{}, validationErrorf("Approval.id is required")
	}
	if in.Decision != "approved" && in.Decision != "rejected" {
		return WriteResult{}, validationErrorf("decision must be \"approved\" or \"rejected\", got %q", in.Decision)
	}
	if _, ok := approvalStatuses[in.Decision]; !ok {
		return WriteResult{}, validationErrorf("decision %q is not a recognized :Approval.status value", in.Decision)
	}
	if in.ByPartyID == "" {
		return WriteResult{}, validationErrorf("byPartyId is required to decide an approval (IN19: a decision always has a session-authenticated person actor)")
	}
	if in.DecidedByRef == "" {
		return WriteResult{}, validationErrorf("decidedByRef is required (audit trail)")
	}

	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		partyCheck, err := tx.Run(ctx, `MATCH (p:Party {id: $id}) WHERE p.closedAtRevision IS NULL RETURN p.id`,
			map[string]any{"id": in.ByPartyID})
		if err != nil {
			return nil, err
		}
		if _, err := partyCheck.Single(ctx); err != nil {
			return nil, validationErrorf("byPartyId %q is not an active :Party", in.ByPartyID)
		}

		apCheck, err := tx.Run(ctx, `MATCH (a:Approval {id: $id}) RETURN a.status AS status`, map[string]any{"id": in.ID})
		if err != nil {
			return nil, err
		}
		apRec, err := apCheck.Single(ctx)
		if err != nil {
			return nil, validationErrorf("Approval %q not found", in.ID)
		}
		if status, _ := apRec.Get("status"); status != "pending" {
			return nil, validationErrorf("Approval %q is not pending (status: %v) — already decided", in.ID, status)
		}

		n, err := bumpRevision(ctx, tx, in.ByPartyID, fmt.Sprintf("infra proposal %s decided: %s", in.ID, in.Decision), "approval_decided")
		if err != nil {
			return nil, err
		}

		if _, err := tx.Run(ctx, `
			MATCH (a:Approval {id: $id})
			SET a.status = $decision, a.decidedAt = datetime(), a.comment = $comment,
			    a.decidedByRef = $decidedByRef, a.decidedAtRevision = $rev
		`, map[string]any{
			"id": in.ID, "decision": in.Decision, "comment": in.Comment,
			"decidedByRef": in.DecidedByRef, "rev": n,
		}); err != nil {
			return nil, err
		}

		if _, err := tx.Run(ctx, `
			MATCH (a:Approval {id: $id})
			MATCH (p:Party {id: $partyId})
			MERGE (a)-[:DECIDED_BY]->(p)
		`, map[string]any{"id": in.ID, "partyId": in.ByPartyID}); err != nil {
			return nil, err
		}

		return n, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.ID}, nil
}
