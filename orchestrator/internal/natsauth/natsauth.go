// Package natsauth provisions NATS accounts/ACLs and is the fix for
// IMPLEMENTATION-NOTES.md's G-1 gap ("NATS에 인증/ACL이 전혀 없다"). It
// implements D14 (10-core-architecture.md §7.3): cross-team publish is
// rejected by the NATS SERVER, not by agent cooperation.
//
// Simplification vs. the full design text: §7.3/§8.4 describe per-team NATS
// ACCOUNTS with nkey/JWT-issued users (decentralized auth). This build uses
// NATS's traditional config-based `authorization { users: [...] }` block —
// ONE global account, one user per platform component/agent, each with an
// explicit publish/subscribe subject allow-list. This still gives the
// property D14 actually asks for ("타 팀 서브젝트로의 publish를 서버가
// 거부") because each user's permission list is exactly the per-employee
// list §7.3 spells out (team.<slug>.*, master.inbox, etc, deny everything
// else) — decentralized JWT accounts would add stronger tenant boundary
// (whole-account isolation with cross-account imports/exports) but that
// boundary isn't what D14's completion bar requires; it requires that a
// compromised/prompt-injected agent's NATS user literally cannot publish to
// another team's inbox subject, which config-based per-user permissions
// already enforce. Documented as a deliberate scope simplification, not an
// oversight — see IMPLEMENTATION-NOTES.md.
package natsauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Signaler interface {
	// KillContainerByExactName must send `signal` to the container named
	// `name` and nothing else — see driver/docker's implementation comment
	// for why this is intentionally the narrowest possible operation.
	KillContainerByExactName(ctx context.Context, name, signal string) error
}

// User is one `authorization.users[]` entry.
type User struct {
	Username       string
	Password       string
	PublishAllow   []string
	SubscribeAllow []string
}

// AgentUser builds the §7.3 permission set for one AIAgent's NATS identity.
// isMaster grants the cross-team publish that makes "크로스팀은 Master 경유"
// a technical fact rather than a convention (§7.3's whole point).
func AgentUser(employeeID, teamSlug, password string, isMaster bool) User {
	publish := []string{
		fmt.Sprintf("team.%s.channel", teamSlug),
		fmt.Sprintf("team.%s.inbox.*", teamSlug),
		"master.inbox",
		"master.reply.>",
		"heartbeat.agent." + employeeID,
		"ops.log." + employeeID,
		"ops.cost." + employeeID,
		"ops.transcript." + employeeID,
		"task.*.event",
		"approval.request",
	}
	if isMaster {
		// Master's whole reason to exist on this ACL layer: it alone may
		// publish into ANY team's inbox — §7.3 "Master만 모든
		// team.*.inbox.*에 publish 권한을 갖는다".
		publish = append(publish, "team.*.inbox.*")
	}

	// --- JetStream API rights: the NARROWEST set that lets an agent do its job.
	//
	// §7.3's list is written in terms of the ORGANIZATION subject tree and does
	// not mention the $JS.API.* control plane, because at design time nobody had
	// tried to actually bind a durable pull consumer under these permissions.
	// The I4 worker did, and the answer was that the §7.3 list alone yields
	// "Permissions Violation for Publish to $JS.API.CONSUMER.MSG.NEXT.INBOX..."
	// — i.e. §5.2's entire main loop is impossible. So the list has to be
	// extended, and the ONLY interesting question is by how much.
	//
	// The dangerous grant, and the one deliberately NOT given, is
	// $JS.API.CONSUMER.CREATE / DURABLE.CREATE on INBOX: with it, an agent
	// could create a consumer filtered on `team.<other>.inbox.*` and READ
	// another team's mail. D14 spends its whole argument on making cross-team
	// publish server-enforced; granting consumer-creation would have left the
	// read direction wide open and made that argument hollow. Consumers are
	// therefore created by osiris-orchestrator (internal/jetstream) and the
	// agent may only:
	//   - ask for the next message on ITS OWN consumer, by name
	//   - read that consumer's info (nats.go does this when binding)
	//   - ack/nak/InProgress ITS OWN consumer's deliveries
	// Every one of those subjects is pinned to this employee's durable name, so
	// there is no wildcard an agent can walk.
	durable := "agent-" + employeeID
	publish = append(publish,
		"$JS.API.CONSUMER.MSG.NEXT.INBOX."+durable,
		"$JS.API.CONSUMER.INFO.INBOX."+durable,
		// Ack subjects are $JS.ACK.<stream>.<consumer>.<delivered>.<...>; pinned
		// to this stream AND this consumer.
		"$JS.ACK.INBOX."+durable+".>",
		// Stream INFO is needed by nats.go's JetStream publish path to resolve
		// which stream a subject belongs to. Read-only metadata about streams
		// the agent already publishes into; no create/update/delete/purge.
		"$JS.API.STREAM.INFO.TASKS",
		"$JS.API.STREAM.INFO.TRANSCRIPT",
		"$JS.API.STREAM.INFO.COST",
		"$JS.API.STREAM.INFO.INBOX",
	)

	subscribe := []string{
		fmt.Sprintf("team.%s.inbox.%s", teamSlug, employeeID),
		fmt.Sprintf("team.%s.channel", teamSlug),
		"approval.decision.>",
		"agent." + employeeID + ".config.reload",
		// Request/reply inboxes: every JetStream API call and every
		// js.Publish PubAck comes back on a client-chosen _INBOX subject, and
		// pull consumers receive deliveries on one too. Without this the client
		// cannot hear ANY reply. _INBOX subjects are per-connection random
		// tokens, so this is not a channel another agent can address.
		"_INBOX.>",
	}
	if isMaster {
		subscribe = append(subscribe, "master.inbox")
	}
	return User{Username: durable, Password: password, PublishAllow: publish, SubscribeAllow: subscribe}
}

// GenerateOrLoadPassword returns the password persisted at path, generating
// and persisting a new random one if the file does not exist yet. Stable
// across reconcile ticks/orchestrator restarts so agents don't get a new
// NATS password (and a forced reconnect) every 15 seconds.
func GenerateOrLoadPassword(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	// The employee's creds directory may not exist yet — this is called for
	// the FULL running-wanted set every tick (to keep the NATS ACL current
	// even for agents not yet Applied), not just for agents the driver has
	// already created a directory for. Mode 0755/0644 rather than the
	// design's 0700/0400 — see driver/docker/driver.go's writeConfigFiles
	// permissions note (WSL2 DrvFs does not honor root/owner bypass of a
	// restrictive mode on this host's checkout path).
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir creds dir for nats password: %w", err)
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate nats password: %w", err)
	}
	pw := base64.RawURLEncoding.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(pw), 0o644); err != nil {
		return "", fmt.Errorf("persist nats password: %w", err)
	}
	return pw, nil
}

// Generator owns the single generated NATS config file
// (nats/generated/authorization.conf, mounted read-only into the nats
// container and read-write here) and reloads nats via SIGHUP whenever the
// rendered content actually changes.
type Generator struct {
	path              string
	natsContainerName string
	corePassword      string
	orchPassword      string
	// apiPassword is osiris-api's static platform NATS credential. Blank
	// means osiris-api is not deployed against this authorization.conf yet —
	// render() omits that user entirely rather than writing a
	// password-less/broken entry (see render()'s doc comment on this field).
	apiPassword string
	signaler    Signaler

	mu          sync.Mutex
	lastWritten string
}

func NewGenerator(path, natsContainerName, corePassword, orchPassword, apiPassword string, signaler Signaler) *Generator {
	g := &Generator{path: path, natsContainerName: natsContainerName, corePassword: corePassword, orchPassword: orchPassword, apiPassword: apiPassword, signaler: signaler}
	if b, err := os.ReadFile(path); err == nil {
		g.lastWritten = string(b)
	}
	return g
}

// requiredOrchestratorGrants are the permissions THIS process needs before it
// can provision JetStream at all. Used by EnsureBootstrap to detect an
// authorization.conf written by an older build.
var requiredOrchestratorGrants = []string{`"$JS.API.>"`, `"_INBOX.>"`}

// EnsureBootstrap fixes a genuine chicken-and-egg that cost this worker a
// debugging cycle and is worth preserving in code rather than in a runbook.
//
// The orchestrator provisions the §7.2 JetStream streams, which requires its own
// NATS user to hold $JS.API.> (publish) and _INBOX.> (subscribe). Those grants
// live in the very file this package generates. On an upgrade from a build whose
// generated authorization.conf predates them, the sequence is:
//
//	connect → subscribe _INBOX.>  → PERMISSION VIOLATION
//	                                (nats.go creates its request/reply inbox
//	                                 subscription ONCE, lazily; once rejected,
//	                                 every subsequent request silently times out
//	                                 rather than erroring — the failure looks
//	                                 like "JetStream is unreachable")
//	→ ensure streams → times out → ACL rewritten + SIGHUP → too late: this
//	  connection's dead inbox subscription is never retried.
//
// Observed exactly that way: "add stream INBOX: context deadline exceeded" with
// no violation logged for the CREATE, because the reply path, not the request,
// was what had been denied.
//
// So the ACL must be correct BEFORE the connection exists. This method is called
// from main() before nats.Connect and writes the platform users only. It is a
// NO-OP when the file already carries the required grants, which matters: a
// platform-only rewrite temporarily drops the agent user entries, and doing that
// on every restart would make every live agent reauthenticate for no reason.
func (g *Generator) EnsureBootstrap(ctx context.Context) error {
	if b, err := os.ReadFile(g.path); err == nil {
		have := string(b)
		ok := true
		for _, grant := range requiredOrchestratorGrants {
			if !strings.Contains(have, grant) {
				ok = false
				break
			}
		}
		if ok {
			return nil
		}
	}
	// Platform users only. The first reconcile tick immediately re-renders with
	// the full agent set, so any live agent is without a NATS user for at most
	// one reconcile interval and reconnects on its own (MaxReconnects(-1)).
	return g.Sync(ctx, nil)
}

// Sync renders the full authorization block (static osiris-core/
// osiris-orchestrator users + one entry per currently-desired agent) and,
// only if the content actually changed since the last successful write,
// rewrites the file and sends SIGHUP to nats (nats-server reloads its
// config on SIGHUP — no restart, no dropped JetStream data).
func (g *Generator) Sync(ctx context.Context, agents []User) error {
	content := g.render(agents)

	g.mu.Lock()
	unchanged := content == g.lastWritten
	g.mu.Unlock()
	if unchanged {
		return nil
	}

	if err := os.WriteFile(g.path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write nats authorization.conf: %w", err)
	}
	if err := g.signaler.KillContainerByExactName(ctx, g.natsContainerName, "HUP"); err != nil {
		return fmt.Errorf("reload nats (SIGHUP %s): %w", g.natsContainerName, err)
	}

	g.mu.Lock()
	g.lastWritten = content
	g.mu.Unlock()
	return nil
}

func (g *Generator) render(agents []User) string {
	sorted := append([]User(nil), agents...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Username < sorted[j].Username })

	var b strings.Builder
	b.WriteString("# GENERATED by osiris-orchestrator (internal/natsauth). Do not edit by hand —\n")
	b.WriteString("# rewritten every reconcile tick whenever the agent set or platform passwords\n")
	b.WriteString("# change. See docs/design/10-core-architecture.md §7.3 (D14) and\n")
	b.WriteString("# IMPLEMENTATION-NOTES.md for the config-based-permissions simplification.\n")
	b.WriteString("authorization {\n  users: [\n")

	writeUser := func(u User) {
		b.WriteString("    { user: \"" + escape(u.Username) + "\", password: \"" + escape(u.Password) + "\", permissions: {\n")
		b.WriteString("      publish: { allow: [" + quoteList(u.PublishAllow) + "] }\n")
		b.WriteString("      subscribe: { allow: [" + quoteList(u.SubscribeAllow) + "] }\n")
		b.WriteString("    } }\n")
	}

	// Static platform users. osiris-core only ever publishes org.revision /
	// org.event.<kind> (internal/events/nats.go) — no subscribe needed.
	writeUser(User{
		Username: "osiris-core", Password: g.corePassword,
		// org.event.> lands in the TASKS stream (§7.2), so core's publishes now
		// go through JetStream and need the PubAck inbox. Kept minimal: no
		// $JS.API.* management rights at all — stream/consumer provisioning is
		// osiris-orchestrator's job (§11 issue 4's two-tier split), and core is
		// the graph's write gate, not the bus's administrator.
		PublishAllow:   []string{"org.revision", "org.event.>", "$JS.API.STREAM.INFO.TASKS"},
		SubscribeAllow: []string{"_INBOX.>"},
	})
	// osiris-orchestrator subscribes org.revision (reconcile trigger, §1.4)
	// and publishes config-reload notifications (§6.2 ReloadConfig) plus
	// audit-style capacity-rejection events (this build's addition, see
	// internal/reconcile).
	writeUser(User{
		Username: "osiris-orchestrator", Password: g.orchPassword,
		// $JS.API.> is the one broad grant in this file, and it is here because
		// this component IS the JetStream administrator: it creates the §7.2
		// streams and every per-agent durable consumer (internal/jetstream).
		// Concentrating that power in the component that already holds
		// docker.sock adds no new blast radius — §1.4 already calls that
		// "사실상 호스트 root" — whereas spreading it to agents would (see
		// AgentUser's comment on why consumer-creation is withheld).
		PublishAllow:   []string{"agent.*.config.reload", "org.event.>", "$JS.API.>"},
		SubscribeAllow: []string{"org.revision", "_INBOX.>"},
	})
	// osiris-api (§7.4/D19's SSE resume implementation, internal/events/
	// sse.go there) is a read-only OBSERVER of the TASKS stream — the
	// narrowest grant that lets it open one ephemeral (non-durable) pull
	// consumer per browser connection, per §9.6's explicit instruction:
	// "osiris-api는 JetStream 관리 권한이 없어야 한다... 필요한 것(ephemeral
	// consumer 생성)만 좁게 줄 것." Notably ABSENT: $JS.API.CONSUMER.
	// DURABLE.CREATE.* (it never creates a durable), any grant on INBOX (it
	// is not a work-queue participant), and any $JS.ACK.* subject (its
	// consumers use AckNone — see sse.go — so there is nothing to ack).
	// Wildcarded only on the CONSUMER NAME component, because an ephemeral
	// consumer's name is server-generated and unknown ahead of time; never
	// wildcarded on the STREAM component.
	//
	// [2026-07-27] osiris-api additionally owns ONE KV bucket, AUTHN_MACHINE
	// (60-identity-authn.md §19; §14.1's diagram states the ownership as
	// "AUTHN_* (JetStream KV) — osiris-api 단독 소유"). The grants below are
	// the narrowest set that lets it create-if-absent, read, write and purge
	// keys in THAT ONE BUCKET and nothing else:
	//
	//   - Every $JS.API.* subject names the stream component literally
	//     (KV_AUTHN_MACHINE — JetStream's own name for a KV bucket's backing
	//     stream). Never a wildcard on the stream component, same rule the
	//     TASKS grants above already follow.
	//   - No $JS.API.STREAM.DELETE.* at all: osiris-api can create its bucket
	//     but cannot destroy it, so a compromised osiris-api cannot erase the
	//     audit surface of its own credentials.
	//   - No grant on any other stream, no $JS.API.> anywhere.
	//
	// HI9 is the lint over this: "$KV.AUTHN_ 권한이 osiris-api 사용자 외
	// 누구에게도 부여되지 않았는지". osiris-orchestrator's own $JS.API.> grant
	// is broad by design (it IS the JetStream administrator, §11 issue 4) but
	// it holds no $KV.AUTHN_* subject, and nothing below gives it one.
	//
	// security.machine.> is the §19.7/MP6 audit path. It is a PUBLISH grant
	// only — osiris-api emits its own credential lifecycle events and cannot
	// read anyone else's.
	if g.apiPassword != "" {
		writeUser(User{
			Username: "osiris-api",
			Password: g.apiPassword,
			PublishAllow: []string{
				"$JS.API.CONSUMER.CREATE.TASKS",
				"$JS.API.CONSUMER.DELETE.TASKS.*",
				"$JS.API.CONSUMER.INFO.TASKS.*",
				"$JS.API.CONSUMER.MSG.NEXT.TASKS.*",
				"$JS.API.STREAM.INFO.TASKS",
				// AUTHN_MACHINE KV bucket — osiris-api only.
				//
				// $JS.API.INFO is an account-level probe, not a stream grant:
				// nats.go's CreateKeyValue calls AccountInfo first to confirm
				// JetStream is enabled for this account before it tries to
				// create the bucket's backing stream. MEASURED, not guessed —
				// without it the create call times out and nats-server logs
				// `Publish Violation - User "osiris-api", Subject
				// "$JS.API.INFO"`. It exposes this account's JetStream limits
				// and usage totals and confers no rights over any stream.
				"$JS.API.INFO",
				"$JS.API.STREAM.CREATE.KV_AUTHN_MACHINE",
				"$JS.API.STREAM.UPDATE.KV_AUTHN_MACHINE",
				"$JS.API.STREAM.INFO.KV_AUTHN_MACHINE",
				"$JS.API.STREAM.MSG.GET.KV_AUTHN_MACHINE",
				"$JS.API.DIRECT.GET.KV_AUTHN_MACHINE",
				"$JS.API.DIRECT.GET.KV_AUTHN_MACHINE.>",
				"$JS.API.CONSUMER.CREATE.KV_AUTHN_MACHINE",
				"$JS.API.CONSUMER.CREATE.KV_AUTHN_MACHINE.>",
				"$JS.API.CONSUMER.DELETE.KV_AUTHN_MACHINE.*",
				"$JS.API.CONSUMER.INFO.KV_AUTHN_MACHINE.*",
				"$JS.API.CONSUMER.MSG.NEXT.KV_AUTHN_MACHINE.*",
				"$KV.AUTHN_MACHINE.>",
				// [2026-07-27] AUTHN_CRED and AUTHN_TICKET — the person
				// credential and enrollment ticket buckets (60 §3.2, §5.1).
				// Same shape and same reasoning as AUTHN_MACHINE above,
				// repeated per bucket rather than collapsed into a
				// KV_AUTHN_* wildcard: a wildcard on the stream component
				// would let a compromised osiris-api create arbitrary
				// KV_AUTHN_<anything> streams, and HI9's whole claim is that
				// the AUTHN_ surface is enumerable.
				//
				// STREAM.DELETE is withheld here too — osiris-api must not be
				// able to erase the store holding its own credential records.
				"$JS.API.STREAM.CREATE.KV_AUTHN_CRED",
				"$JS.API.STREAM.UPDATE.KV_AUTHN_CRED",
				"$JS.API.STREAM.INFO.KV_AUTHN_CRED",
				"$JS.API.STREAM.MSG.GET.KV_AUTHN_CRED",
				"$JS.API.DIRECT.GET.KV_AUTHN_CRED",
				"$JS.API.DIRECT.GET.KV_AUTHN_CRED.>",
				"$JS.API.CONSUMER.CREATE.KV_AUTHN_CRED",
				"$JS.API.CONSUMER.CREATE.KV_AUTHN_CRED.>",
				"$JS.API.CONSUMER.DELETE.KV_AUTHN_CRED.*",
				"$JS.API.CONSUMER.INFO.KV_AUTHN_CRED.*",
				"$JS.API.CONSUMER.MSG.NEXT.KV_AUTHN_CRED.*",
				"$KV.AUTHN_CRED.>",
				"$JS.API.STREAM.CREATE.KV_AUTHN_TICKET",
				"$JS.API.STREAM.UPDATE.KV_AUTHN_TICKET",
				"$JS.API.STREAM.INFO.KV_AUTHN_TICKET",
				"$JS.API.STREAM.MSG.GET.KV_AUTHN_TICKET",
				"$JS.API.DIRECT.GET.KV_AUTHN_TICKET",
				"$JS.API.DIRECT.GET.KV_AUTHN_TICKET.>",
				"$JS.API.CONSUMER.CREATE.KV_AUTHN_TICKET",
				"$JS.API.CONSUMER.CREATE.KV_AUTHN_TICKET.>",
				"$JS.API.CONSUMER.DELETE.KV_AUTHN_TICKET.*",
				"$JS.API.CONSUMER.INFO.KV_AUTHN_TICKET.*",
				"$JS.API.CONSUMER.MSG.NEXT.KV_AUTHN_TICKET.*",
				"$KV.AUTHN_TICKET.>",
				// Credential lifecycle audit (60 §19.7, MP6).
				"security.machine.>",
				// [2026-07-27] DG5's event half (60 §21.1) plus the
				// enrollment and authentication audit subjects of §6.3.
				// Publish-only, same as security.machine.>: osiris-api emits
				// its own security events and can read nobody else's.
				"security.rbac.>",
				"security.enrollment.>",
				"security.authn.>",
				// [2026-09-11, C-1] approval.decision.<id> — plain core-NATS
				// publish (not JetStream: agent/internal/supervisor/gate.go's
				// Gate.Request subscribes with a plain nc.Subscribe, no
				// durable consumer, so no $JS.API grant is needed here,
				// unlike the KV/TASKS grants above).
				//
				// docs/design/80-action-layer.md §813 / 90-infra-topology.md
				// §11 IN19: "approval.decision.<id>의 유일한 발행자는 세션
				// 인증된 콘솔/REST 경로다." Before this grant, NO user in this
				// file could publish this subject at all — §813 documents
				// that the subject had zero publishers. osiris-api's
				// POST /_api/infra/proposals/{id}/decision handler
				// (PersonOnly + RequirePerm(org.admin)) is the first, and
				// this grant is scoped to exactly that one subject family —
				// not "approval.>", so osiris-api cannot publish
				// approval.request (that stays agent-only, AgentUser above).
				"approval.decision.>",
				// [2026-07-28] H23's two subjects (60 §4.5):
				// security.masterkey.retired and .purge_failed.
				//
				// FOUND BY MEASUREMENT, NOT BY REVIEW. The H10 implementation
				// published security.masterkey.retired and nats-server answered
				// `Permissions Violation for Publish`; because secevents.Publish
				// uses a JetStream publish that waits for an ack, the violation
				// did not merely drop the event — it STALLED the credential
				// redemption request that triggered it. An audit subject missing
				// from this list is therefore not a silent gap in the audit
				// trail, it is latency on the request path, which is why it is
				// worth stating here: every new secevents subject must be added
				// to this grant in the same change that introduces it.
				"security.masterkey.>",
			},
			SubscribeAllow: []string{"_INBOX.>"},
		})
	}
	for _, u := range sorted {
		writeUser(u)
	}
	b.WriteString("  ]\n}\n")
	return b.String()
}

func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = "\"" + escape(it) + "\""
	}
	return strings.Join(quoted, ", ")
}
