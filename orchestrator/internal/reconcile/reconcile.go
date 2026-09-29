// Package reconcile is the [1단계] convergence loop
// (docs/design/10-core-architecture.md §1.4's reconcile() pseudocode,
// implemented here almost verbatim). It reads INTENT from Neo4j
// (graphdb.DeriveDesiredAgents), reads OBSERVATION from the driver
// (driver.Observe), and calls Apply/Recreate/ReloadConfig/Drain/Delete to
// close the gap — never writing anything back into Neo4j itself (this
// build does not implement the :AgentRuntime cache described in §6.1; see
// IMPLEMENTATION-NOTES.md for why that's a documented, deliberate gap
// rather than a silent omission).
//
// G3 (docs/design/10-core-architecture.md §1.7): this package depends ONLY
// on workload.WorkloadDriver, the driver-agnostic interface — never on
// internal/driver/docker or any other concrete driver package. That is the
// whole point of the interface (§6.2 D3: "이 인터페이스가 1→2단계 전환의
// 유일한 재작성 지점") — a [2단계] driver/kube.Driver satisfying the same
// interface must be able to replace the Loop's driver field with zero
// changes to this file. Concrete driver construction happens exactly once,
// in cmd/osiris-orchestrator/main.go, which is the only file in this repo
// (other than internal/driver/docker itself) allowed to import a concrete
// driver package — see bin/lint-driver-boundary.sh, which fails CI if that
// ever stops being true.
package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"osiris-orchestrator/internal/catalog"
	"osiris-orchestrator/internal/config"
	"osiris-orchestrator/internal/graphdb"
	"osiris-orchestrator/internal/jetstream"
	"osiris-orchestrator/internal/natsauth"
	"osiris-orchestrator/internal/workload"
)

type AgentSummary struct {
	EmployeeID string `json:"employeeId"`
	TeamID     string `json:"teamId"`
	IsMaster   bool   `json:"isMaster"`
	Desired    string `json:"desiredState"`
	SpecHash   string `json:"specHash"`
	ConfigHash string `json:"configHash"`
	Phase      string `json:"phase,omitempty"`
	Health     string `json:"health,omitempty"`
}

type RejectedAgent struct {
	EmployeeID string `json:"employeeId"`
	Reason     string `json:"reason"`
}

// Snapshot is the observable status this build exposes in place of writing
// an :AgentRuntime cache into Neo4j (see package doc). It is what
// GET /status on the orchestrator's health port returns — the mechanism
// completion criterion 6 (capacity rejection must be "관측 가능") relies on.
type Snapshot struct {
	UpdatedAt         time.Time       `json:"updatedAt"`
	CatalogVersionOK  bool            `json:"catalogVersionOk"`
	CatalogMismatch   []string        `json:"catalogMismatch,omitempty"`
	MaxResidentAgents int             `json:"maxResidentAgents"`
	ResidentCount     int             `json:"residentCount"`
	Desired           []AgentSummary  `json:"desired"`
	Rejected          []RejectedAgent `json:"rejected"`
	LastError         string          `json:"lastError,omitempty"`

	// GatewayTokenConfigured reports ONLY whether l.cfg.GatewayToken is
	// non-empty — never the value itself. Added for
	// docs/design/30-onboarding-provisioning.md §1.0.3 step 0-5(c): the
	// onboarding step0 pipeline (osiris-api) needs a way to confirm "did
	// orchestrator actually restart with the freshly-issued gateway token"
	// without osiris-api ever needing read access to the token's value (it
	// doesn't otherwise have any reason to hold it — §8.4's credential lives
	// in orchestrator's own env and the per-agent creds files it writes).
	GatewayTokenConfigured bool `json:"gatewayTokenConfigured"`
}

type Loop struct {
	cfg     config.Config
	db      *graphdb.Client
	driver  workload.WorkloadDriver // G3: interface only — see package doc comment
	natsGen *natsauth.Generator
	jsp     *jetstream.Provisioner
	nc      *nats.Conn

	streamsEnsured bool

	mu              sync.Mutex
	snapshot        Snapshot
	appliedConfig   map[string]string // employeeID -> last ConfigHash this loop itself applied via ReloadConfig/Apply/Recreate
	seededFromLabel map[string]bool   // employeeID -> whether appliedConfig has been seeded from the observed label yet (post-restart bootstrap)
}

// NewLoop takes `driver` as the workload.WorkloadDriver interface — the
// concrete *docker.Driver constructed in main.go satisfies it, but this
// constructor (and everything in this package) never knows or cares that
// it's Docker. Swap in a [2단계] driver/kube.Driver here and nothing else
// in this file changes.
func NewLoop(cfg config.Config, db *graphdb.Client, driver workload.WorkloadDriver, natsGen *natsauth.Generator, jsp *jetstream.Provisioner, nc *nats.Conn) *Loop {
	return &Loop{
		cfg: cfg, db: db, driver: driver, natsGen: natsGen, jsp: jsp, nc: nc,
		appliedConfig:   map[string]string{},
		seededFromLabel: map[string]bool{},
	}
}

// Snapshot returns a copy of the last tick's status for the /status handler
// (httpapi.Snapshotter). Returns `any` (rather than the concrete Snapshot
// type) purely to satisfy that tiny interface without httpapi importing
// this package's types — httpapi only ever needs to JSON-encode the result.
func (l *Loop) Snapshot() any {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snapshot
}

// Run blocks: a 15s ticker (default, config.ReconcileInterval) plus an
// immediate tick whenever org.revision is received — exactly §1.4's "15초
// 주기 + org.revision 이벤트 수신 시 즉시".
func (l *Loop) Run(ctx context.Context) error {
	trigger := make(chan struct{}, 1)
	sub, err := l.nc.Subscribe("org.revision", func(_ *nats.Msg) {
		select {
		case trigger <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return fmt.Errorf("subscribe org.revision: %w", err)
	}
	defer sub.Unsubscribe()

	ticker := time.NewTicker(l.cfg.ReconcileInterval)
	defer ticker.Stop()

	// Tick once immediately on boot — §6.1: "기동 시 그래프에서 전량
	// 재계산하므로 재기동만으로 회복".
	l.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			l.tick(ctx)
		case <-trigger:
			l.tick(ctx)
		}
	}
}

func (l *Loop) tick(ctx context.Context) {
	if err := l.reconcileOnce(ctx); err != nil {
		log.Printf("osiris-orchestrator: reconcile error: %v", err)
		l.mu.Lock()
		l.snapshot.LastError = err.Error()
		l.snapshot.UpdatedAt = time.Now()
		l.mu.Unlock()
	}
}

func (l *Loop) credsDir(employeeID string) string {
	return filepath.Join(l.cfg.StateDir, "creds", employeeID)
}

func (l *Loop) reconcileOnce(ctx context.Context) error {
	ok, mismatched, err := l.db.CheckCatalogVersion(ctx, l.cfg.CatalogVersion)
	if err != nil {
		return fmt.Errorf("check catalog version: %w", err)
	}
	if !ok {
		// FAIL CLOSED (I9, docs/design/20-ontology-schema.md §9.3): the
		// grant-catalog Go map this binary embeds must match what core
		// seeded. If it doesn't, we do not touch any container — creating
		// agents against a permission catalog this binary can't vouch for
		// would silently grant/withhold the wrong things.
		log.Printf("osiris-orchestrator: CATALOG VERSION MISMATCH (expected %s), mismatched permissions: %v — refusing to reconcile", l.cfg.CatalogVersion, mismatched)
		l.mu.Lock()
		l.snapshot = Snapshot{
			UpdatedAt: time.Now(), CatalogVersionOK: false, CatalogMismatch: mismatched,
			MaxResidentAgents:      l.cfg.MaxResidentAgents,
			GatewayTokenConfigured: l.cfg.GatewayToken != "",
		}
		l.mu.Unlock()
		return nil
	}

	rawDesired, err := l.db.DeriveDesiredAgents(ctx)
	if err != nil {
		return fmt.Errorf("derive desired agents: %w", err)
	}

	specs := map[string]workload.AgentWorkloadSpec{}
	summaries := map[string]AgentSummary{}
	for _, d := range rawDesired {
		spec, err := l.buildSpec(d)
		if err != nil {
			log.Printf("osiris-orchestrator: skipping agent %s: %v", d.EmployeeID, err)
			continue
		}
		specs[d.EmployeeID] = spec
		summaries[d.EmployeeID] = AgentSummary{
			EmployeeID: d.EmployeeID, TeamID: d.TeamID, IsMaster: d.IsMaster,
			Desired: d.DesiredState, SpecHash: spec.SpecHash, ConfigHash: spec.ConfigHash,
		}
	}

	actual, err := l.driver.Observe(ctx)
	if err != nil {
		return fmt.Errorf("observe: %w", err)
	}

	runningWanted := map[string]workload.AgentWorkloadSpec{}
	for id, spec := range specs {
		if spec.Desired == workload.DesiredRunning {
			runningWanted[id] = spec
		}
	}

	// --- 1. anything running that is no longer wanted (closed, suspended,
	// or dropped from the org entirely) gets drained then deleted. ---
	for id, st := range actual {
		if _, stillRunningWanted := runningWanted[id]; stillRunningWanted {
			continue
		}
		drainSeconds := 30
		if s, ok := specs[id]; ok { // suspended, not closed — spec still exists, honor its drainSeconds
			drainSeconds = s.DrainSeconds
		}
		log.Printf("osiris-orchestrator: %s no longer desired-running (containerId=%s) — draining then deleting", id, st.Ref.ID)
		if err := l.driver.Drain(ctx, id, drainSeconds); err != nil {
			log.Printf("osiris-orchestrator: drain %s: %v", id, err)
			continue
		}
		if err := l.driver.Delete(ctx, id); err != nil {
			log.Printf("osiris-orchestrator: delete %s: %v", id, err)
			continue
		}
		// Only a CLOSED agent loses its durable consumer. A merely Suspended one
		// (spec still present, §5.3: "컨테이너 삭제·볼륨 보존, resume 시
		// Provisioning 복귀") keeps it, so the work queued for it while it was
		// away is still there when it comes back.
		if _, stillHasSpec := specs[id]; !stillHasSpec {
			if err := l.jsp.DeleteAgentConsumer(id); err != nil {
				log.Printf("osiris-orchestrator: delete consumer for %s: %v", id, err)
			}
		}
		delete(l.appliedConfig, id)
		delete(l.seededFromLabel, id)
	}

	// --- 1b. PROVISIONING, for every running-wanted agent, BEFORE any
	// container is created (§5.3's Provisioning phase: "볼륨/자격증명/설정/
	// NATS사용자 생성" happens before Booting).
	//
	// Ordering here is not cosmetic. The I3 build synced the NATS ACL at the END
	// of the tick, i.e. AFTER Apply, which was harmless only because the stub
	// agent never connected to NATS. A real supervisor connects during boot: if
	// its user does not exist yet it gets an authorization error, and while it
	// does recover (MaxReconnects(-1)) it spends its first ~15s unready for a
	// reason that looks like a bug. Same for the durable consumer — §5.4's
	// readyz includes "JetStream durable consumer 바인드 성공", so the consumer
	// must exist before the container does.
	var natsUsers []natsauth.User
	for id, spec := range runningWanted {
		creds, err := l.buildCredentials(spec.EmployeeID)
		if err != nil {
			log.Printf("osiris-orchestrator: build credentials for %s: %v", id, err)
			continue
		}
		natsUsers = append(natsUsers, natsauth.AgentUser(id, spec.TeamSlug, creds["nats_password"], spec.IsMaster))

		// CREDENTIAL ROTATION DELIVERY (§8.4). This is the gap the supervising
		// review found: Credentials are correctly excluded from SpecHash and
		// ConfigHash (rotation must cause neither a recreate nor a reload
		// event), which left nothing at all writing fresh credential files at an
		// ALREADY-RUNNING container. This call is that writer. It runs every
		// tick for every running-wanted agent and is a no-op unless a value
		// actually changed (the driver compares content, see SyncCredentials) —
		// so a rotated 24h gateway token reaches the container within one
		// reconcile interval, with no restart, no reload event and no hash
		// movement, exactly as §8.4 requires.
		if err := l.driver.SyncCredentials(ctx, id, creds); err != nil {
			log.Printf("osiris-orchestrator: sync credentials for %s: %v", id, err)
		}
	}
	if err := l.natsGen.Sync(ctx, natsUsers); err != nil {
		log.Printf("osiris-orchestrator: nats auth sync: %v", err)
	}

	// §7.2's streams, AFTER the ACL sync above and never before it.
	//
	// This ordering was found the hard way and is worth stating: on a first boot
	// the committed bootstrap authorization.conf is whatever the repo shipped, so
	// this process's OWN NATS user may not yet hold the $JS.API.> rights that
	// creating a stream needs. Ensuring streams first therefore fails with
	// "Permissions Violation for Publish to $JS.API.STREAM.CREATE.INBOX" — the
	// orchestrator locked out of JetStream by an ACL only it can fix. Syncing the
	// ACL first (which needs no JetStream at all — it is a file write plus a
	// SIGHUP) makes the sequence self-bootstrapping from any starting state.
	if !l.streamsEnsured {
		if err := l.jsp.EnsureStreams(); err != nil {
			// Non-fatal: the ACL reload we just triggered may still be settling.
			// The next tick retries, and EnsureStreams is idempotent.
			log.Printf("osiris-orchestrator: ensure jetstream streams (will retry next tick): %v", err)
		} else {
			l.streamsEnsured = true
		}
	}

	for id, spec := range runningWanted {
		if err := l.jsp.EnsureAgentConsumer(jetstream.AgentConsumer{
			EmployeeID: id, TeamSlug: spec.TeamSlug,
			IsMaster: spec.IsMaster, MaxPending: spec.Concurrency,
		}); err != nil {
			log.Printf("osiris-orchestrator: ensure consumer for %s: %v", id, err)
		}
	}

	// --- 2. capacity-gated admission of not-yet-applied agents (G10: the
	// seat count is always computed here, in code, from
	// OSIRIS_MAX_RESIDENT_AGENTS — never hardcoded in a frontend). Agents
	// already running keep their seats; only NEW admissions are gated, so a
	// config/env change to the cap never evicts an already-resident agent. ---
	existingResidents := 0
	var pending []string
	for id := range runningWanted {
		if _, ok := actual[id]; ok {
			existingResidents++
		} else {
			pending = append(pending, id)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		si, sj := specs[pending[i]], specs[pending[j]]
		if si.IsMaster != sj.IsMaster {
			return si.IsMaster // master admitted first, deterministically
		}
		return pending[i] < pending[j] // then employeeId ascending — same final tiebreak §8.5's AssignmentPolicy uses
	})

	var rejected []RejectedAgent
	admitted := 0
	for _, id := range pending {
		if existingResidents+admitted >= l.cfg.MaxResidentAgents {
			reason := fmt.Sprintf("MAX_RESIDENT_AGENTS_EXCEEDED: OSIRIS_MAX_RESIDENT_AGENTS=%d, already resident=%d", l.cfg.MaxResidentAgents, existingResidents+admitted)
			log.Printf("osiris-orchestrator: REJECTED agent %s: %s", id, reason)
			rejected = append(rejected, RejectedAgent{EmployeeID: id, Reason: reason})
			l.publishEvent("org.event.capacity_exceeded", map[string]any{"employeeId": id, "reason": reason})
			continue
		}
		spec := specs[id]
		creds, err := l.buildCredentials(spec.EmployeeID)
		if err != nil {
			log.Printf("osiris-orchestrator: provision creds for %s: %v", id, err)
			continue
		}
		spec.Credentials = creds
		if _, err := l.driver.Apply(ctx, spec); err != nil {
			log.Printf("osiris-orchestrator: apply %s: %v", id, err)
			continue
		}
		l.appliedConfig[id] = spec.ConfigHash
		l.seededFromLabel[id] = true
		admitted++
		log.Printf("osiris-orchestrator: applied new agent %s (team=%s specHash=%s)", id, spec.TeamID, spec.SpecHash)
	}

	// --- 3. drift correction for already-running, admitted agents:
	// SpecHash change -> Recreate; else liveness (3 consecutive unhealthy)
	// -> Recreate; else ConfigHash change -> ReloadConfig (A12: never both
	// at once, ConfigHash changes NEVER recreate). ---
	for id, spec := range runningWanted {
		st, ok := actual[id]
		if !ok {
			continue // just applied above, or rejected — nothing more to do this tick
		}
		if !l.seededFromLabel[id] {
			// First time this orchestrator process has seen this container
			// (e.g. right after a restart) — trust the label as "already
			// applied" so we don't spuriously ReloadConfig on every boot.
			l.appliedConfig[id] = st.ConfigHash
			l.seededFromLabel[id] = true
		}

		switch {
		case st.SpecHash != spec.SpecHash:
			log.Printf("osiris-orchestrator: SpecHash changed for %s (%s -> %s) — Recreate", id, st.SpecHash, spec.SpecHash)
			creds, err := l.buildCredentials(spec.EmployeeID)
			if err != nil {
				log.Printf("osiris-orchestrator: provision creds for %s: %v", id, err)
				continue
			}
			spec.Credentials = creds
			if err := l.driver.Recreate(ctx, spec); err != nil {
				log.Printf("osiris-orchestrator: recreate %s: %v", id, err)
				continue
			}
			l.appliedConfig[id] = spec.ConfigHash
		case st.Phase != "running":
			// LIVENESS, the other half of it (§1.3/G9). A container that is
			// desired-running but has EXITED is not restarted by Docker (the
			// restart policy is `unless-stopped`, and a deliberate stop — or an
			// exit after a completed drain — leaves it stopped forever), and the
			// two branches around this one only fire on a hash change or on
			// health flapping. So without this case a drained agent stays down
			// until a human notices, which is precisely the "restart: always에
			// 의존한 복구" that G9 forbids AND the convergence the loop is
			// supposed to provide instead.
			//
			// Recreate rather than start: it re-renders config/credentials from
			// the CURRENT graph revision, and it is the path §5.5 already
			// documents. The workspace volume is not touched (Delete never
			// removes volumes), so the interrupted-turn checkpoint survives and
			// the new container resumes from it — verified end to end.
			log.Printf("osiris-orchestrator: %s is desired-running but phase=%q — Recreate (liveness convergence, §1.3/G9)", id, st.Phase)
			creds, err := l.buildCredentials(spec.EmployeeID)
			if err != nil {
				log.Printf("osiris-orchestrator: provision creds for %s: %v", id, err)
				continue
			}
			spec.Credentials = creds
			if err := l.driver.Recreate(ctx, spec); err != nil {
				log.Printf("osiris-orchestrator: recreate %s: %v", id, err)
				continue
			}
			l.appliedConfig[id] = spec.ConfigHash
		case st.ConsecutiveUnhealthy >= 3:
			log.Printf("osiris-orchestrator: %s unhealthy for %d consecutive observations — Recreate (liveness enforcement, §5.4)", id, st.ConsecutiveUnhealthy)
			if err := l.driver.Recreate(ctx, spec); err != nil {
				log.Printf("osiris-orchestrator: recreate %s: %v", id, err)
				continue
			}
			l.appliedConfig[id] = spec.ConfigHash
		case l.appliedConfig[id] != spec.ConfigHash:
			log.Printf("osiris-orchestrator: ConfigHash changed for %s (%s -> %s) — ReloadConfig (no recreate, A12)", id, l.appliedConfig[id], spec.ConfigHash)
			if err := l.driver.ReloadConfig(ctx, spec); err != nil {
				log.Printf("osiris-orchestrator: reload config %s: %v", id, err)
				continue
			}
			l.appliedConfig[id] = spec.ConfigHash
			l.publishEvent("agent."+id+".config.reload", map[string]any{"employeeId": id, "configHash": spec.ConfigHash})
		}
	}

	// (NATS ACL sync + durable-consumer provisioning + credential rotation
	// delivery all moved to step 1b, above admission — see the comment there
	// for why the ordering matters now that a real supervisor connects.)

	// --- refresh Observe()-derived phase/health into the summaries for /status ---
	actual2, err := l.driver.Observe(ctx)
	if err == nil {
		for id, st := range actual2 {
			if s, ok := summaries[id]; ok {
				s.Phase = st.Phase
				s.Health = st.Health
				summaries[id] = s
			}
		}
	}

	l.mu.Lock()
	var desiredList []AgentSummary
	for _, s := range summaries {
		desiredList = append(desiredList, s)
	}
	sort.Slice(desiredList, func(i, j int) bool { return desiredList[i].EmployeeID < desiredList[j].EmployeeID })
	l.snapshot = Snapshot{
		UpdatedAt: time.Now(), CatalogVersionOK: true,
		MaxResidentAgents:      l.cfg.MaxResidentAgents,
		ResidentCount:          existingResidents + admitted,
		Desired:                desiredList,
		Rejected:               rejected,
		GatewayTokenConfigured: l.cfg.GatewayToken != "",
	}
	l.mu.Unlock()
	return nil
}

// buildCredentials generates/loads (never rewrites, see
// natsauth.GenerateOrLoadPassword) the named credential blobs for one
// employeeID and returns them as a plain map — this function makes NO
// driver call. The caller attaches the result to
// AgentWorkloadSpec.Credentials and hands the spec to
// driver.Apply/Recreate, which is the only place that actually writes
// anything to the workload's file-injection path (or, for a future
// [2단계] driver, a Secret) — see workload.AgentWorkloadSpec.Credentials'
// doc comment for why that responsibility moved off this package's direct
// driver call (G3: this package must not call any docker-specific method,
// only the six workload.WorkloadDriver methods).
func (l *Loop) buildCredentials(employeeID string) (map[string]string, error) {
	pwPath := filepath.Join(l.credsDir(employeeID), "nats_password")
	pw, err := natsauth.GenerateOrLoadPassword(pwPath)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"nats_user":     "agent-" + employeeID,
		"nats_password": pw,

		// --- gateway (§8.4 row 1, §9) -----------------------------------
		// §8.4 wants a PER-AGENT 24h token issued by core's omniroute adapter
		// (IssueToken) and refreshed by the driver every 6h. core has no such
		// adapter in this build, so this build delivers the operator-supplied
		// token instead. The two halves of §8.4's row are separable and only one
		// of them was actually missing:
		//   ISSUANCE  — still a gap (needs core's LLMGateway adapter; that is
		//               the §9.1 interface, unimplemented).
		//   DELIVERY  — implemented and verified: whatever value ends up here is
		//               pushed to a RUNNING container by SyncCredentials and read
		//               by the supervisor at the next exec, with no restart and
		//               no hash movement. Swapping a static token for a
		//               per-agent issued one later changes ONLY these two lines.
		"gateway_base_url":    l.cfg.GatewayBaseURL,
		"gateway_token":       l.cfg.GatewayToken,
		"gateway_health_path": l.cfg.GatewayHealthPath,

		// core_token: still blank. core supports exactly one static
		// CORE_API_TOKEN (I2's genesis simplification), so handing agents a
		// per-agent core token would mean handing them THE token — strictly
		// worse than an empty file. The path stays so the contract is stable.
		"core_token": "",
	}, nil
}

func (l *Loop) publishEvent(subject string, payload map[string]any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if err := l.nc.Publish(subject, b); err != nil {
		log.Printf("osiris-orchestrator: publish %s: %v", subject, err)
	}
}

// buildSpec turns a raw graphdb.DesiredAgent into a fully rendered
// workload.AgentWorkloadSpec: resolves grant-catalog allowedTools, renders
// CLAUDE.md/settings.json/mcp.json, and computes SpecHash/ConfigHash.
// minAgentCPUMilli enforces §5.4's "CPU limit은 >= 1000m(1 vCPU)을 강제한다
// (orchestrator가 검증). 헬스 goroutine이 굶지 않도록."
//
// This bites immediately in practice: the seeded `light` RoleResourceProfile is
// 500m (osiris-core's seed.go), so without this clamp every light agent runs at
// half a core — and the failure mode is precisely the one §5.4 is written to
// prevent. Under CPU pressure from a `claude` child, the supervisor's 5s ticker
// and the health server's accept loop get scheduled late, lastLoopTick goes
// stale, /livez 503s, and the orchestrator restarts a container that was doing
// exactly what it was told to. Clamping here (rather than editing the seeded
// profile) keeps the profile meaning what the org said it means for MEMORY while
// making the CPU floor a platform invariant that no profile can violate.
const minAgentCPUMilli = 1000

func (l *Loop) buildSpec(d graphdb.DesiredAgent) (workload.AgentWorkloadSpec, error) {
	grants := workload.SortedGrants(d.EffectiveGrants)

	var caps []workload.CapabilityRef
	for _, c := range d.Capabilities {
		caps = append(caps, workload.CapabilityRef{Code: c.Code, Label: c.Label, Level: c.Level})
	}

	prompt, err := workload.RenderPrompt(d.PromptTemplateMd, workload.RoleContext{
		RoleTitle: d.RoleTitle, Responsibilities: d.RoleResponsibilities, RoleLevel: d.RoleLevel,
		TeamName: d.TeamName, TeamSlug: d.TeamSlug, Capabilities: caps, Grants: grants,
		IsMaster: d.IsMaster, IsLead: d.IsHead,
	})
	if err != nil {
		return workload.AgentWorkloadSpec{}, fmt.Errorf("render prompt for role %s: %w", d.RoleID, err)
	}
	// §8.1's single declaration fanning out to the layers this build
	// materializes. All four derivations come from ONE call each into the
	// version-pinned grant-catalog, so they cannot disagree with each other.
	allowedTools := catalog.AllowedTools(grants)
	deniedTools := catalog.DeniedTools(grants)
	mcpServers := catalog.MCPServers(grants)
	egress := catalog.EgressAllowlist(grants)

	settings, err := workload.RenderSettings(workload.SettingsInput{
		AllowedTools:    allowedTools,
		DeniedTools:     deniedTools,
		MCPServers:      mcpServers,
		EgressAllowlist: egress,
		CatalogVersion:  l.cfg.CatalogVersion,
	}.WithGrants(grants))
	if err != nil {
		return workload.AgentWorkloadSpec{}, err
	}

	cpu := d.CPUMilli
	if cpu < minAgentCPUMilli {
		log.Printf("osiris-orchestrator: %s: RoleResourceProfile cpuMilli=%d is below the §5.4 floor — clamping to %d (health goroutine must not starve)",
			d.EmployeeID, cpu, minAgentCPUMilli)
		cpu = minAgentCPUMilli
	}

	image := d.Image
	if image == "" {
		image = l.cfg.AgentImage
	}

	spec := workload.AgentWorkloadSpec{
		EmployeeID: d.EmployeeID, TeamID: d.TeamID, TeamSlug: d.TeamSlug, RoleID: d.RoleID,
		IsMaster: d.IsMaster, IsLead: d.IsHead,
		Image: image, ModelAlias: d.ModelAlias,
		Resources: workload.ResourceProfile{CPUMilli: cpu, MemMiB: d.MemMiB, StorageGiB: d.StorageGiB},
		Grants:    grants, NetworkName: "osiris-agents",
		Concurrency: d.MaxTaskPoints, DrainSeconds: d.DrainSeconds, Desired: d.DesiredState,
		ExtraNoProxy:         l.cfg.AgentExtraNoProxy,
		RenderedPrompt:       prompt,
		RenderedSettings:     settings,
		RenderedMCP:          workload.RenderMCP(mcpServers),
		RenderedAllowedTools: workload.RenderAllowedTools(allowedTools),
	}
	spec.ComputeHashes()
	return spec, nil
}
