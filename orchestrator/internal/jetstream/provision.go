// Package jetstream provisions the §7.2 stream table and the per-agent durable
// pull consumers §7.2 requires ("에이전트별 durable pull consumer").
//
// WHY THE ORCHESTRATOR OWNS THIS. Two reasons, and the second is a security
// boundary rather than a convenience:
//
//  1. §11 issue 4 puts platform-layer provisioning on the bootstrap/platform
//     side and organization-layer convergence on the orchestrator/operator side.
//     Streams are platform; a per-agent CONSUMER is organization-shaped (it
//     exists iff that :AIAgent exists), so it belongs to the same convergence
//     loop that creates the container, the volume and the NATS user — §5.3's
//     Provisioning phase, "볼륨/자격증명/설정/NATS사용자 생성".
//
//  2. If agents created their own consumers they would need
//     $JS.API.CONSUMER.CREATE rights on the INBOX stream — and an agent that can
//     create a consumer on INBOX can create one filtered on ANOTHER TEAM'S inbox
//     subject and read it. D14/§7.3 makes cross-team PUBLISH a server-enforced
//     impossibility; leaving the read direction open would make that half a
//     façade. With creation here, the agent's entire JetStream right set is
//     "next message on MY consumer" + "ack MY consumer" (see
//     internal/natsauth.AgentUser).
//
// This package holds NO Docker/driver types and is not part of the
// WorkloadDriver boundary (G3): NATS is the same in [1단계] and [2단계], so
// there is nothing to abstract.
package jetstream

import (
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

// dedupeWindow is §7.2's "Nats-Msg-Id 중복제거 2분", which is what makes the
// supervisor's at-least-once outbox replay and JetStream redelivery safe
// (§5.5's Nats-Msg-Id = taskId:attempt:eventType).
const dedupeWindow = 2 * time.Minute

// streams is §7.2's table, transcribed.
//
// Subject-pattern note: §7.2 writes the TASKS row as "approval.*", but the
// actual subject tree in §7.1 contains BOTH `approval.request` (one token) and
// `approval.decision.<approvalId>` (two) — a single `*` cannot match the
// latter. `approval.>` is used so the row captures what §7.1 actually
// publishes. Widening a capture pattern does not widen anyone's permissions:
// who may publish to those subjects is decided by the NATS ACL, not by which
// stream stores them.
var streams = []*nats.StreamConfig{
	{
		Name:        "INBOX",
		Description: "Per-employee work queue (§7.2). WorkQueue: a message leaves the stream when its one consumer acks it.",
		Subjects:    []string{"team.*.inbox.*", "master.inbox"},
		Retention:   nats.WorkQueuePolicy,
		Storage:     nats.FileStorage,
		Discard:     nats.DiscardOld,
		Duplicates:  dedupeWindow,
		MaxMsgs:     -1,
		MaxBytes:    -1,
	},
	{
		Name:        "TASKS",
		Description: "Task/approval/org event log (§7.2). The origin of the console's SSE resume via Last-Event-ID = stream sequence (D19).",
		// `security.>` — see the note below the streams table on why this is
		// added here and what it closes.
		Subjects:   []string{"task.*.event", "approval.>", "org.event.>", "cluster.>", "security.>"},
		Retention:  nats.LimitsPolicy,
		Storage:    nats.FileStorage,
		MaxAge:     90 * 24 * time.Hour,
		Duplicates: dedupeWindow,
		MaxMsgs:    -1,
		MaxBytes:   -1,
	},
	{
		Name:        "TRANSCRIPT",
		Description: "stream-json transcript lines, 30 days (§7.2). Also the ONLY history of rendered system prompts (20-ontology-schema.md §3.2.1).",
		Subjects:    []string{"ops.transcript.>"},
		Retention:   nats.LimitsPolicy,
		Storage:     nats.FileStorage,
		MaxAge:      30 * 24 * time.Hour,
		Duplicates:  dedupeWindow,
		MaxMsgs:     -1,
		MaxBytes:    -1,
	},
	{
		Name:        "COST",
		Description: "Per-turn cost, 400 days (§7.2/§9.3) — monthly settlement, budget enforcement.",
		Subjects:    []string{"ops.cost.>"},
		Retention:   nats.LimitsPolicy,
		Storage:     nats.FileStorage,
		MaxAge:      400 * 24 * time.Hour,
		Duplicates:  dedupeWindow,
		MaxMsgs:     -1,
		MaxBytes:    -1,
	},
	{
		Name:        "EGRESS",
		Description: "Egress proxy audit (allow/deny), 30 days (§7.2/A3).",
		Subjects:    []string{"ops.egress"},
		Retention:   nats.LimitsPolicy,
		Storage:     nats.FileStorage,
		MaxAge:      30 * 24 * time.Hour,
		// Duplicates is set on every stream, including this one: the server
		// defaults an unset window to 2m, so leaving it zero here would make the
		// config comparison in streamMatches report a difference forever and
		// UpdateStream would run on every process start.
		Duplicates: dedupeWindow,
		MaxMsgs:    -1,
		MaxBytes:   -1,
	},
}

// `security.>` ON TASKS — [2026-07-27], and it needs an explicit record
// because it closes a discrepancy that two design documents already flagged
// and neither was allowed to fix.
//
// 60-identity-authn.md §17 F2, and IMPLEMENTATION-NOTES.md §100.7 repeating
// it: "§8.5는 security.genesis.used를 TASKS 스트림에 발행한다고 서술하는데,
// §7.2의 TASKS 서브젝트 목록에 security.*가 없다. 기존 내부 불일치이며, 이
// 설계의 security.authn.*/security.breakglass.*/security.enrollment.*도 같은
// 자리를 필요로 한다." The stated resolution, in F2's own words, is "§7.2
// TASKS 행에 security.* 추가" — the design worker recorded it as belonging to
// §7.2's document owner and did not apply it.
//
// The machine principal work (60 §19) is the first thing that actually needs
// it at runtime, because MP6 makes issuance FAIL-CLOSED on the audit publish:
// with no stream capturing security.machine.*, the JetStream PubAck never
// arrives, and no machine principal can ever be issued. So the choice was
// between applying the already-written resolution or leaving §19
// unimplementable. Applied here, deliberately minimally (one subject pattern,
// no retention/storage change, no other stream touched), and reported to the
// supervisor as a change that ratifies F2 rather than one this worker invented.
//
// Widening a stream's CAPTURE pattern grants nobody anything: who may publish
// to security.* is decided by the NATS ACL (internal/natsauth), where the only
// grant is osiris-api's publish-only "security.machine.>".

// Provisioner is idempotent: EnsureStreams/EnsureAgentConsumer are safe to call
// on every reconcile tick and only touch the server when something actually
// differs.
type Provisioner struct {
	js nats.JetStreamContext
}

func New(nc *nats.Conn) (*Provisioner, error) {
	js, err := nc.JetStream(nats.MaxWait(10 * time.Second))
	if err != nil {
		return nil, fmt.Errorf("jetstream context: %w", err)
	}
	return &Provisioner{js: js}, nil
}

// EnsureStreams creates or updates the §7.2 streams.
func (p *Provisioner) EnsureStreams() error {
	for _, want := range streams {
		info, err := p.js.StreamInfo(want.Name)
		if err != nil {
			if _, aerr := p.js.AddStream(want); aerr != nil {
				return fmt.Errorf("add stream %s: %w", want.Name, aerr)
			}
			log.Printf("osiris-orchestrator: created JetStream stream %s subjects=%v", want.Name, want.Subjects)
			continue
		}
		if streamMatches(&info.Config, want) {
			continue
		}
		if _, uerr := p.js.UpdateStream(want); uerr != nil {
			// Retention/Storage are immutable in JetStream. If a previous build
			// created a stream with a different retention policy, an update is
			// refused — and silently continuing would leave OSiris running
			// against a stream whose semantics differ from §7.2 (e.g. an INBOX
			// with Limits retention would keep delivering acked work). Loud,
			// non-fatal: the reconcile loop keeps running so the rest of the
			// system stays observable, but the operator is told exactly what to
			// delete.
			log.Printf("osiris-orchestrator: WARNING: stream %s exists with an incompatible config and cannot be updated (%v). "+
				"Immutable fields (retention/storage) must match §7.2 — delete the stream and let this loop recreate it.", want.Name, uerr)
			continue
		}
		log.Printf("osiris-orchestrator: updated JetStream stream %s", want.Name)
	}
	return nil
}

func streamMatches(have, want *nats.StreamConfig) bool {
	if len(have.Subjects) != len(want.Subjects) {
		return false
	}
	for i := range want.Subjects {
		if have.Subjects[i] != want.Subjects[i] {
			return false
		}
	}
	return have.Retention == want.Retention &&
		have.MaxAge == want.MaxAge &&
		have.Duplicates == want.Duplicates
}

// AgentConsumer describes the durable consumer one agent binds to.
type AgentConsumer struct {
	EmployeeID string
	TeamSlug   string
	IsMaster   bool
	MaxPending int // from :AIAgent.maxTaskPoints
}

// DurableName is the consumer name; it must match what the driver puts in
// OSIRIS_INBOX_DURABLE.
func DurableName(employeeID string) string { return "agent-" + employeeID }

// EnsureAgentConsumer creates (or brings into line) the per-agent durable pull
// consumer on INBOX with §7.2's parameters: ack_wait=30m, max_deliver=3.
//
// The MASTER agent gets a MULTI-filter consumer covering both its own team inbox
// and `master.inbox`, because §7.1 makes master.inbox the single mandatory hop
// for all cross-team traffic ("크로스팀은 반드시 여기로") and §7.3 gives only
// Master the right to fan back out. Without that second filter, every
// cross-team message and every escalation would sit in a WorkQueue stream with
// no consumer at all. Multi-filter consumers need nats-server >= 2.10 (this
// build pins nats:2.10-alpine).
func (p *Provisioner) EnsureAgentConsumer(a AgentConsumer) error {
	maxPending := a.MaxPending
	if maxPending < 1 {
		maxPending = 1
	}
	cfg := &nats.ConsumerConfig{
		Durable:     DurableName(a.EmployeeID),
		Description: "osiris agent " + a.EmployeeID + " inbox (created by osiris-orchestrator; the agent only binds)",
		AckPolicy:   nats.AckExplicitPolicy,
		// §7.2: ack_wait=30m. A turn longer than that keeps its lease via the
		// supervisor's periodic InProgress() (§5.2), so this is the ceiling on
		// "supervisor died without saying anything", not on turn length.
		AckWait:       30 * time.Minute,
		MaxDeliver:    3,
		MaxAckPending: maxPending,
		DeliverPolicy: nats.DeliverAllPolicy,
		ReplayPolicy:  nats.ReplayInstantPolicy,
	}
	ownInbox := fmt.Sprintf("team.%s.inbox.%s", a.TeamSlug, a.EmployeeID)
	if a.IsMaster {
		cfg.FilterSubjects = []string{ownInbox, "master.inbox"}
	} else {
		cfg.FilterSubject = ownInbox
	}

	info, err := p.js.ConsumerInfo("INBOX", cfg.Durable)
	if err != nil {
		if _, aerr := p.js.AddConsumer("INBOX", cfg); aerr != nil {
			return fmt.Errorf("add consumer %s: %w", cfg.Durable, aerr)
		}
		log.Printf("osiris-orchestrator: created durable consumer INBOX/%s filter=%v", cfg.Durable, filtersOf(cfg))
		return nil
	}
	if consumerMatches(&info.Config, cfg) {
		return nil
	}
	if _, uerr := p.js.UpdateConsumer("INBOX", cfg); uerr != nil {
		log.Printf("osiris-orchestrator: WARNING: cannot update consumer INBOX/%s (%v)", cfg.Durable, uerr)
		return nil
	}
	log.Printf("osiris-orchestrator: updated durable consumer INBOX/%s", cfg.Durable)
	return nil
}

// DeleteAgentConsumer removes an agent's consumer when the agent is closed.
// Called only from the same reconcile branch that drains+deletes the container,
// so a merely-Suspended agent keeps its consumer (and therefore its pending
// work and its ack floor) — §5.3: "Suspended: 컨테이너 삭제·볼륨 보존".
func (p *Provisioner) DeleteAgentConsumer(employeeID string) error {
	err := p.js.DeleteConsumer("INBOX", DurableName(employeeID))
	if err != nil && err != nats.ErrConsumerNotFound {
		return err
	}
	return nil
}

func filtersOf(cfg *nats.ConsumerConfig) []string {
	if len(cfg.FilterSubjects) > 0 {
		return cfg.FilterSubjects
	}
	return []string{cfg.FilterSubject}
}

func consumerMatches(have, want *nats.ConsumerConfig) bool {
	if have.AckWait != want.AckWait || have.MaxDeliver != want.MaxDeliver || have.MaxAckPending != want.MaxAckPending {
		return false
	}
	if have.FilterSubject != want.FilterSubject {
		return false
	}
	if len(have.FilterSubjects) != len(want.FilterSubjects) {
		return false
	}
	for i := range want.FilterSubjects {
		if have.FilterSubjects[i] != want.FilterSubjects[i] {
			return false
		}
	}
	return true
}
