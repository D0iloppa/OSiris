// Propose/list/decide — docs/design/90-infra-topology.md §11 (IN17/IN18/IN19,
// "C-1 — 제안→승인 척추"). Unlike handlers.go's five read-only routes (A/B단계),
// these three ARE mutating, and each is deliberately gated differently at the
// ROUTER (internal/api/server.go), not just in this file — see that file's
// route-registration comment for the full argument. This file assumes that
// gating already happened and does not re-derive it, except where the graph
// layer (osiris-core) requires an actor to be attributed regardless (IN19's
// "person, always" rule for deciding).
//
// WHAT THIS FILE DOES NOT DO: apply, scale, or delete anything. Every
// handler below ends at a graphdb write (via corewrite.Client) that changes
// only :Approval/:ActionType-adjacent graph state. C-2
// (docs/design/90-infra-topology.md §11) is what would turn an approved
// proposal into a real cluster change, and it does not exist in this build —
// Decide's response says so explicitly rather than letting a caller infer
// "approved" means "applied".
package infra

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"osiris-api/internal/authz"
	"osiris-api/internal/corewrite"
)

// newProposalID mirrors machineauth's token-minting discipline (CSPRNG, hex,
// prefixed) — no third-party uuid dependency needed for an id that is never
// parsed, only compared and stored.
func newProposalID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate proposal id: %w", err)
	}
	return "prop-" + hex.EncodeToString(buf), nil
}

// infraActionTypeIDs is the closed set POST /_api/infra/proposals may target
// — the three rows core/internal/graphdb/actiontype_infra.go seeds at boot.
// A caller-supplied actionTypeId outside this set is rejected here, before
// ever reaching osiris-core (osiris-core's own FOR_ACTION existence + status
// check, writeops_approval.go's CreateApproval, is the wall that still
// stands if this one is skipped — belt and braces, same pattern as
// requireActor elsewhere in this codebase).
var infraActionTypeIDs = map[string]struct{}{
	"infra.apply":  {},
	"infra.scale":  {},
	"infra.delete": {},
}

type proposeReq struct {
	ActionTypeID string `json:"actionTypeId"`
	Namespace    string `json:"namespace"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	ManifestJSON string `json:"manifestJson,omitempty"`
}

// Propose -> POST /_api/infra/proposals.
//
// IN18: reachable by a machine principal (the router gates this on
// RequirePerm(org.read.ops), not PersonOnly — see server.go's route
// comment). Creating a proposal grants no capability by itself: the
// :Approval this writes is inert until a person decides it (Decide, below).
func (s *Service) Propose(w http.ResponseWriter, r *http.Request) {
	if s.core == nil || !s.core.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "osiris-core graph-write API is not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req proposeReq
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if _, ok := infraActionTypeIDs[req.ActionTypeID]; !ok {
		writeErr(w, http.StatusBadRequest, "actionTypeId must be one of infra.apply|infra.scale|infra.delete")
		return
	}
	if req.Namespace == "" || req.Kind == "" || req.Name == "" {
		writeErr(w, http.StatusBadRequest, "namespace, kind and name are required")
		return
	}

	principal, ok := authz.PrincipalFrom(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing or invalid credential")
		return
	}

	id, err := newProposalID()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to generate proposal id")
		return
	}

	target := map[string]string{"namespace": req.Namespace, "kind": req.Kind, "name": req.Name}
	targetJSON, _ := json.Marshal(target)

	in := corewrite.ApprovalInput{
		ID:            id,
		ActionTypeID:  req.ActionTypeID,
		TargetJSON:    string(targetJSON),
		PayloadJSON:   req.ManifestJSON,
		ProposedByRef: principal.ActorRef(),
	}
	// A person's session carries a real :Party; a machine principal (IN18's
	// case) does not (60-identity-authn.md §19.2) — corewrite.ApprovalInput
	// leaves ProposedByPartyID empty in that case, exactly as
	// graphdb.CreateApproval's doc comment expects.
	if principal.Kind == authz.KindPerson && principal.PartyID != "" {
		in.ProposedByPartyID = principal.PartyID
	}

	res, err := s.core.CreateApproval(r.Context(), in)
	if err != nil {
		status, msg := corewriteStatus(err)
		writeErr(w, status, msg)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": res.ID, "revision": res.Revision, "status": "pending"})
}

// ListProposals -> GET /_api/infra/proposals?status=pending.
//
// C-1 only ever has one meaningful status to list (pending — the queue an
// admin must clear); the query parameter is accepted and echoed rather than
// silently ignored so a future status value is not a silent behavior
// change, but osiris-core's ListPendingApprovals only ever returns
// status:'pending' rows (writeops_approval.go), so any other value returns
// an empty, honestly-labeled list rather than inventing filtering this
// build does not implement.
func (s *Service) ListProposals(w http.ResponseWriter, r *http.Request) {
	if s.core == nil || !s.core.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "osiris-core graph-write API is not configured")
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	if status != "pending" {
		writeJSON(w, http.StatusOK, map[string]any{"proposals": []corewrite.ApprovalSummary{}, "note": "this build only ever lists status=pending proposals"})
		return
	}
	list, err := s.core.ListPendingApprovals(r.Context())
	if err != nil {
		status, msg := corewriteStatus(err)
		writeErr(w, status, msg)
		return
	}
	if list == nil {
		list = []corewrite.ApprovalSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposals": list})
}

type decisionReq struct {
	Decision string `json:"decision"` // "approve" | "reject" (console vocabulary; mapped to graphdb's approved/rejected below)
	Comment  string `json:"comment,omitempty"`
}

// Decide -> POST /_api/infra/proposals/{id}/decision.
//
// IN19: the router requires PersonOnly + RequirePerm(authz.PermOrgAdmin) —
// see server.go's route comment. This handler additionally requires the
// resolved principal to carry a real :Party (requirePersonActor-equivalent),
// because osiris-core's DecideApproval refuses an empty ByPartyID outright
// (the graph-side wall — 60 §21.4-1's argument, applied to IN19).
//
// After a successful core decision, this handler publishes
// approval.decision.<id> on the plain core-NATS bus — see
// agent/internal/supervisor/gate.go's Gate.Request, which has subscribed to
// exactly this subject shape since before this build and has had, per
// 80-action-layer.md §813, ZERO publishers until now. This is that
// publisher, and per IN19 it is the ONLY one this codebase adds — see
// orchestrator/internal/natsauth/natsauth.go's grant comment for why
// osiris-api's NATS user needed a new, narrowly-scoped PublishAllow entry to
// make this call succeed at all.
//
// STILL NO EXECUTION: publishing this event only unblocks an agent that
// happens to be synchronously waiting on this exact approval id (the
// existing gate.go flow, used by git.commit et al) — nothing about infra
// proposals in this build creates such a waiter, so today this publish is
// pure audit/observability plumbing, wired correctly rather than left
// absent, not a trigger for any infra change.
func (s *Service) Decide(w http.ResponseWriter, r *http.Request) {
	if s.core == nil || !s.core.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "osiris-core graph-write API is not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing proposal id")
		return
	}

	principal, ok := authz.PrincipalFrom(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing or invalid session")
		return
	}
	if principal.Kind != authz.KindPerson || principal.PartyID == "" {
		// Defense in depth: the router already refuses a machine principal
		// here (PersonOnly, IN19) and KindOperator (the legacy master-key
		// session) has no :Party either — this handler must not attribute a
		// decision to nobody.
		writeErr(w, http.StatusForbidden, "deciding a proposal must be attributed to a :Person session (IN19)")
		return
	}

	var req decisionReq
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	var decision string
	switch req.Decision {
	case "approve", "approved":
		decision = "approved"
	case "reject", "rejected":
		decision = "rejected"
	default:
		writeErr(w, http.StatusBadRequest, `decision must be "approve" or "reject"`)
		return
	}

	res, err := s.core.DecideApproval(r.Context(), corewrite.DecisionInput{
		ID: id, Decision: decision, Comment: req.Comment,
		ByPartyID: principal.PartyID, DecidedByRef: principal.ActorRef(),
	})
	if err != nil {
		status, msg := corewriteStatus(err)
		writeErr(w, status, msg)
		return
	}

	if s.nc != nil {
		payload, _ := json.Marshal(map[string]any{
			"decision":   decision,
			"reason":     req.Comment,
			"executedBy": principal.ActorRef(),
			"at":         time.Now().UTC().Format(time.RFC3339),
		})
		if pubErr := s.nc.Publish("approval.decision."+id, payload); pubErr != nil {
			// Best-effort, logged loudly, does not roll back the already-
			// committed graph decision — same accepted-gap shape as
			// core/internal/api/server.go's respondWrite ("a publish
			// failure here does not roll back the already-committed graph
			// write"). The decision is real either way; only a
			// synchronously-waiting agent's unblock would be delayed to its
			// own 24h timeout, and nothing in this build creates such a
			// waiter for an infra proposal (see this handler's doc comment).
			log.Printf("osiris-api: infra.Decide: committed decision for %s but failed to publish approval.decision.%s: %v", id, id, pubErr)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id": res.ID, "revision": res.Revision, "status": decision,
		"note": "approved only changes this proposal's status — actually applying it to the cluster is a separate stage (C-2) not implemented by this build",
	})
}

// corewriteStatus maps a *corewrite.Error (or any other error) to an HTTP
// status/message pair — the same mapping people.go's respondCore already
// performs for the org-role surface, duplicated here rather than shared
// because the two packages must not import each other's internals for one
// switch statement.
func corewriteStatus(err error) (int, string) {
	if ce, ok := err.(*corewrite.Error); ok {
		if ce.Status == http.StatusBadRequest || ce.Status == http.StatusForbidden || ce.Status == http.StatusConflict {
			return ce.Status, ce.Message
		}
		return http.StatusBadGateway, ce.Message
	}
	return http.StatusInternalServerError, "graph operation failed"
}
