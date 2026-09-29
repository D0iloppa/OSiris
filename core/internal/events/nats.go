// Package events publishes the subset of docs/design/10-core-architecture.md
// §7.1's subject tree that osiris-core is actually responsible for in this
// build: org.revision (the revision-bump trigger the future orchestrator's
// reconcile loop listens for) and org.event.<type> (an audit trail of what
// kind of change happened). osiris-core does not publish heartbeat.*,
// task.*.event, approval.*, ops.* — those belong to components this build
// does not implement (osiris-api, the agent runtime, the assignment engine).
//
// [2026-08-12 addition] security.actiontype.unknown (below) is the one
// exception carved out by docs/design/80-action-layer.md §5.3.1/§9 AC2-AC3:
// a security.* event core itself must publish at boot when a graph-native
// :ActionType row references an unknown id/predicate/effect. Before this,
// writeops.go's own doc comment noted "The security.rbac.* event half is
// osiris-api's" — i.e. no security.* subject had ever been published from
// this binary. This is core's first one, scoped narrowly to this one
// subject; it does not make this package a general security-event bus.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

type Publisher struct {
	nc *nats.Conn
}

func Connect(url string) (*Publisher, error) {
	nc, err := nats.Connect(url,
		nats.Name("osiris-core"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Printf("nats: disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			log.Printf("nats: reconnected")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	return &Publisher{nc: nc}, nil
}

func (p *Publisher) Close() {
	if p.nc != nil {
		p.nc.Close()
	}
}

func (p *Publisher) Ping() error {
	if p.nc == nil || !p.nc.IsConnected() {
		return fmt.Errorf("nats not connected")
	}
	return nil
}

type RevisionEvent struct {
	Revision   int64  `json:"revision"`
	At         string `json:"at"`
	ByPartyID  string `json:"byPartyId,omitempty"`
	Summary    string `json:"summary,omitempty"`
	ChangeKind string `json:"changeKind,omitempty"`
}

// PublishRevisionBump publishes to "org.revision" (the reconcile-loop
// trigger, §7.1) and "org.event.<changeKind>" (an audit-trail-flavored
// event). Called by the api layer AFTER a write operation's Neo4j
// transaction has committed — never before, and never if the commit failed.
func (p *Publisher) PublishRevisionBump(_ context.Context, ev RevisionEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal revision event: %w", err)
	}
	if err := p.nc.Publish("org.revision", payload); err != nil {
		return fmt.Errorf("publish org.revision: %w", err)
	}
	if ev.ChangeKind != "" {
		subject := "org.event." + ev.ChangeKind
		if err := p.nc.Publish(subject, payload); err != nil {
			return fmt.Errorf("publish %s: %w", subject, err)
		}
	}
	return nil
}

// ActionTypeUnknownEvent is security.actiontype.unknown's payload
// (docs/design/80-action-layer.md §5.3.1, §9 AC2/AC3). It is intentionally
// its own event, not folded into org.event.* — degrading a row to
// status:'inert' is a boot-time ENFORCEMENT action taken by osiris-core
// itself, not an organizational decision authored by a :Person, so it does
// not bump :OrgRevision and does not belong on the org.event.<changeKind>
// audit stream that RevisionEvent feeds.
type ActionTypeUnknownEvent struct {
	IDs []string `json:"ids"` // :ActionType.id values just degraded to status:'inert'
	At  string   `json:"at"`
}

// PublishSecurityActionTypeUnknown publishes to "security.actiontype.unknown".
// Called by graphdb.ReconcileActionTypes AFTER the corresponding
// :ActionType rows have already been written as status:'inert' in the same
// boot pass — see that function's doc comment for the fail-closed reasoning
// on why a publish failure here does not roll back the degrade or abort
// boot. *Publisher satisfies graphdb.ActionTypeEventPublisher structurally
// via this method; nothing in this package imports graphdb.
func (p *Publisher) PublishSecurityActionTypeUnknown(_ context.Context, ids []string) error {
	payload, err := json.Marshal(ActionTypeUnknownEvent{
		IDs: ids,
		At:  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("marshal security.actiontype.unknown event: %w", err)
	}
	if err := p.nc.Publish("security.actiontype.unknown", payload); err != nil {
		return fmt.Errorf("publish security.actiontype.unknown: %w", err)
	}
	return nil
}
