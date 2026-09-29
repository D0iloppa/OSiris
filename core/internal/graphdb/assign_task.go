package graphdb

// assign_task.go turns a :WorkItem node into an assign.Task — the "문법
// 불일치" fix 20 §8.1 needs (Task.owningTeamId is written as a scalar at
// line 610, but :WorkItem has no such property; team ownership is only the
// (:WorkItem)-[:OWNED_BY]->(:Team) relationship, 20 §3.4 line 247). This is
// a pure syntax substitution — LoadTaskForAssignment resolves OwningTeamID
// by following OWNED_BY, nothing more.

import (
	"context"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"osiris-core/internal/assign"
)

// LoadTaskForAssignment reads one :WorkItem and everything §8's Task needs:
//   - scalar properties (dueAt, effortPoints, actionClass, securityLabel,
//     executionMode)
//   - REQUIRES_CAPABILITY edges -> assign.CapabilityRequirement, each one
//     further decorated with RequiredPermissions (the §7.2
//     (:Capability)-[:REQUIRES_PERMISSION]->(:Permission) union for that
//     capability code) so assign.Evaluate's F2 stays pure
//   - OwningTeamID via OWNED_BY (the line-610/line-247 fixup above)
//
// Must be called inside an already-open transaction.
func LoadTaskForAssignment(ctx context.Context, tx neo4j.ManagedTransaction, workItemID string) (assign.Task, error) {
	res, err := tx.Run(ctx, `
		MATCH (w:WorkItem {id: $id})
		OPTIONAL MATCH (w)-[:OWNED_BY]->(team:Team)
		RETURN w.dueAt AS dueAt, w.effortPoints AS effortPoints,
		       w.actionClass AS actionClass, w.securityLabel AS securityLabel,
		       w.executionMode AS executionMode, team.id AS teamId
	`, map[string]any{"id": workItemID})
	if err != nil {
		return assign.Task{}, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return assign.Task{}, validationErrorf("work item %q not found", workItemID)
	}

	dueAt, _ := rec.Get("dueAt")
	effortPoints, _ := rec.Get("effortPoints")
	actionClass, _ := rec.Get("actionClass")
	securityLabel, _ := rec.Get("securityLabel")
	executionMode, _ := rec.Get("executionMode")
	teamID, _ := rec.Get("teamId")

	reqs, err := loadRequiredCapabilities(ctx, tx, workItemID)
	if err != nil {
		return assign.Task{}, err
	}

	return assign.Task{
		ID:                   workItemID,
		OwningTeamID:         stringOfAny(teamID),
		DueAt:                timeOf(dueAt),
		EffortPoints:         intOfAny(effortPoints),
		RequiredCapabilities: reqs,
		ActionClass:          assign.ActionClass(stringOfAny(actionClass)),
		SecurityLabel:        stringOfAny(securityLabel),
		ExecutionMode:        stringOfAny(executionMode),
	}, nil
}

// loadRequiredCapabilities reads (:WorkItem)-[:REQUIRES_CAPABILITY]->(:Capability)
// and, for each, the §7.2 REQUIRES_PERMISSION union.
func loadRequiredCapabilities(ctx context.Context, tx neo4j.ManagedTransaction, workItemID string) ([]assign.CapabilityRequirement, error) {
	res, err := tx.Run(ctx, `
		MATCH (w:WorkItem {id: $id})-[req:REQUIRES_CAPABILITY]->(c:Capability)
		OPTIONAL MATCH (c)-[:REQUIRES_PERMISSION]->(perm:Permission)
		RETURN c.code AS code, req.minLevel AS minLevel, req.weight AS weight,
		       collect(DISTINCT perm.code) AS perms
		ORDER BY c.code ASC
	`, map[string]any{"id": workItemID})
	if err != nil {
		return nil, err
	}
	records, err := res.Collect(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]assign.CapabilityRequirement, 0, len(records))
	for _, rec := range records {
		code, _ := rec.Get("code")
		minLevel, _ := rec.Get("minLevel")
		weight, _ := rec.Get("weight")
		perms, _ := rec.Get("perms")
		out = append(out, assign.CapabilityRequirement{
			Code:                stringOfAny(code),
			MinLevel:            intOfAny(minLevel),
			Weight:              floatOfAny(weight),
			RequiredPermissions: stringsOfAny(perms),
		})
	}
	return out, nil
}
