package graphdb

// assign_workitem.go adds the MINIMAL :WorkItem writer this task's brief
// calls for: just enough to create a real :WorkItem node (with
// REQUIRES_CAPABILITY and OWNED_BY) so PersistDecision (assign_persist.go)
// has something real to point [:FOR]/[:SELECTED]/WorkItem.status at in a
// test, and so LoadTaskForAssignment (assign_task.go) has something real to
// read. It deliberately does NOT implement:
//   - WBS decomposition (breaking a :WorkItem into children) — owned by
//     agent-master's LLM planning, not this build.
//   - Automatic :WorkItem creation/promotion from anywhere else in the
//     system (81 §4.4 / D1's territory).
//   - Progress rollup, :CompletionCriterion, :Artifact/:Evidence wiring.
//
// Follows writeops.go's existing Create* shape exactly: one write
// transaction, validate references, bump :OrgRevision by 1 (C8 — creating a
// new unit of work is an organizational-intent change, exactly like
// CreateTeam/CreateRole/CreateMembership already are), return WriteResult.

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// WorkItemInput is the minimal set of :WorkItem properties (20 §3.4) plus
// the two relationships assign() actually reads: REQUIRES_CAPABILITY (§8.1)
// and OWNED_BY (the owningTeamId fixup, see assign_task.go). PARENT_OF/
// DEPENDS_ON/CompletionCriterion are out of scope for this minimal writer.
type WorkItemInput struct {
	ID                   string
	Title                string
	Kind                 string // project / deliverable / task / checklistItem
	ExecutionMode        string // digital / physical / hybrid
	EffortPoints         int
	DueAt                string // RFC3339, or "" for unset
	ActionClass          string // internal / external_effect
	SecurityLabel        string // public / internal / confidential; "" means "not set" (see assign.Evaluate F5)
	OwningTeamID         string // "" => no OWNED_BY edge
	RequiredCapabilities []RequiredCapability
	ByPartyID            string
}

// CreateWorkItem is the minimal graph-write API entry for :WorkItem. Status
// always starts 'planned' and progressPct 0 (20 §3.4) — this writer creates
// unstarted work only; assign()/PersistDecision is what moves status to
// 'assigned'.
func (c *Client) CreateWorkItem(ctx context.Context, in WorkItemInput) (WriteResult, error) {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	res, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		for _, rc := range in.RequiredCapabilities {
			check, err := tx.Run(ctx, `MATCH (c:Capability {code: $code}) RETURN c.code`, map[string]any{"code": rc.Code})
			if err != nil {
				return nil, err
			}
			if _, err := check.Single(ctx); err != nil {
				return nil, validationErrorf("capability %q not in catalog", rc.Code)
			}
			if rc.MinLevel < 1 || rc.MinLevel > 5 {
				return nil, validationErrorf("capability %q minLevel must be 1..5, got %d", rc.Code, rc.MinLevel)
			}
		}
		if in.OwningTeamID != "" {
			check, err := tx.Run(ctx, `MATCH (t:Team {id: $id}) RETURN t.id`, map[string]any{"id": in.OwningTeamID})
			if err != nil {
				return nil, err
			}
			if _, err := check.Single(ctx); err != nil {
				return nil, validationErrorf("team %q not found", in.OwningTeamID)
			}
		}

		// Bump the revision FIRST so :WorkItem.sourceRevision (20 §3.4, "이
		// WBS를 만든 시점의 OrgRevision") records the revision this write
		// itself produces, same as every other Create* in this file bumping
		// before returning — the new revision IS "the revision at creation
		// time".
		rev, err := bumpRevision(ctx, tx, in.ByPartyID, fmt.Sprintf("work item %s created/updated", in.ID), "workitem_upserted")
		if err != nil {
			return nil, err
		}

		var dueAtParam any
		if in.DueAt != "" {
			dueAtParam = in.DueAt // Cypher `datetime($dueAt)` parses an RFC3339 string
		}

		_, err = tx.Run(ctx, `
			MERGE (w:WorkItem {id: $id})
			ON CREATE SET w.title = $title, w.kind = $kind, w.executionMode = $executionMode,
			              w.status = 'planned', w.effortPoints = $effortPoints,
			              w.dueAt = CASE WHEN $dueAt IS NULL THEN NULL ELSE datetime($dueAt) END,
			              w.progressPct = 0, w.actionClass = $actionClass,
			              w.securityLabel = $securityLabel, w.sourceRevision = $sourceRevision
			ON MATCH SET  w.title = $title, w.kind = $kind, w.executionMode = $executionMode,
			              w.effortPoints = $effortPoints,
			              w.dueAt = CASE WHEN $dueAt IS NULL THEN NULL ELSE datetime($dueAt) END,
			              w.actionClass = $actionClass, w.securityLabel = $securityLabel
		`, map[string]any{
			"id": in.ID, "title": in.Title, "kind": in.Kind, "executionMode": in.ExecutionMode,
			"effortPoints": in.EffortPoints, "dueAt": dueAtParam,
			"actionClass": in.ActionClass, "securityLabel": in.SecurityLabel,
			"sourceRevision": rev,
		})
		if err != nil {
			return nil, err
		}

		for _, rc := range in.RequiredCapabilities {
			_, err = tx.Run(ctx, `
				MATCH (w:WorkItem {id: $id}), (cap:Capability {code: $code})
				MERGE (w)-[req:REQUIRES_CAPABILITY]->(cap)
				ON CREATE SET req.minLevel = $minLevel, req.weight = $weight
				ON MATCH SET  req.minLevel = $minLevel, req.weight = $weight
			`, map[string]any{"id": in.ID, "code": rc.Code, "minLevel": rc.MinLevel, "weight": rc.Weight})
			if err != nil {
				return nil, err
			}
		}

		if in.OwningTeamID != "" {
			_, err = tx.Run(ctx, `
				MATCH (w:WorkItem {id: $id}), (t:Team {id: $teamId})
				MERGE (w)-[:OWNED_BY]->(t)
			`, map[string]any{"id": in.ID, "teamId": in.OwningTeamID})
			if err != nil {
				return nil, err
			}
		}

		return rev, nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Revision: res.(int64), ID: in.ID}, nil
}
