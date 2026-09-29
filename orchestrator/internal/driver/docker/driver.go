package docker

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"osiris-orchestrator/internal/workload"
)

// agentUID/agentGID is the fixed non-root identity every agent container
// runs as (§5.1/§8.3: runAsNonRoot). One shared UID across all agents (not
// per-agent) keeps credential-file ownership simple — see the comment on
// writeCredsFiles for why the files this driver writes must be chowned to
// this UID rather than left owned by the orchestrator's own (root) UID.
const (
	agentUID = 65532
	agentGID = 65532

	labelManagedBy  = "osiris.dev/managed-by"
	labelEmployeeID = "osiris.dev/employee-id"
	labelTeam       = "osiris.dev/team"
	labelSpecHash   = "osiris.dev/spec-hash"
	labelConfigHash = "osiris.dev/config-hash"

	// labelGenesis marks the bootstrap-master container (30 §1.3's
	// "osiris.dev/genesis=true"). It is deliberately NOT accompanied by
	// labelEmployeeID: Observe() keys managed agents on that label, so an
	// employee-id here would make the reconcile loop treat the genesis container
	// as a graph-derived agent and delete it as an orphan.
	labelGenesis = "osiris.dev/genesis"

	managedByValue = "osiris-orchestrator"
)

// Driver is the [1단계] docs/design/10-core-architecture.md §6.2
// WorkloadDriver implementation. It is the only type in this repo that
// constructs a Docker container — the reconcile loop above it only ever
// sees workload.AgentWorkloadSpec/WorkloadStatus/WorkloadRef, never a
// container ID typed as anything Docker-specific (G3).
type Driver struct {
	cli            *Client
	hostProjectDir string // host-absolute path to the compose project root, for Mounts.Source (this container's docker.sock talks to the HOST daemon, which resolves bind-mount sources against the HOST filesystem, not this container's own view — classic docker-out-of-docker gotcha)
	stateDir       string // this container's OWN view of the same directory (normally /state), for os.WriteFile

	mu            sync.Mutex
	agentsNetwork string // resolved network ID, cached after first lookup

	// unhealthyStreak tracks §5.4's "3연속 unhealthy" liveness judgment per
	// employeeID. This is orchestrator-local, in-memory state — restarting
	// the orchestrator resets the streak, which is acceptable (§6.1: desired
	// state is recomputed from the graph on boot; a missed unhealthy streak
	// just delays one reconcile tick's worth of liveness enforcement, it
	// does not lose any intent).
	unhealthyStreak map[string]int
}

func NewDriver(cli *Client, hostProjectDir, stateDir string) *Driver {
	return &Driver{
		cli: cli, hostProjectDir: hostProjectDir, stateDir: stateDir,
		unhealthyStreak: map[string]int{},
	}
}

var agentsNetRe = regexp.MustCompile(`(^|_)osiris-agents$`)

func (d *Driver) resolveAgentsNetwork(ctx context.Context) (string, error) {
	d.mu.Lock()
	if d.agentsNetwork != "" {
		id := d.agentsNetwork
		d.mu.Unlock()
		return id, nil
	}
	d.mu.Unlock()

	status, body, err := d.cli.do(ctx, "GET", "/networks", nil, nil)
	if err != nil {
		return "", err
	}
	if !ok2xx(status) {
		return "", engineError(status, body)
	}
	var nets []networkSummary
	if err := unmarshalStrict(body, &nets); err != nil {
		return "", fmt.Errorf("list networks: %w", err)
	}
	for _, n := range nets {
		if agentsNetRe.MatchString(n.Name) {
			d.mu.Lock()
			d.agentsNetwork = n.ID
			d.mu.Unlock()
			return n.ID, nil
		}
	}
	return "", fmt.Errorf("no docker network matching osiris-agents found (is docker compose up?)")
}

func (d *Driver) ensureVolume(ctx context.Context, name string) error {
	status, body, err := d.cli.do(ctx, "POST", "/volumes/create", nil, volumeCreateRequest{Name: name})
	if err != nil {
		return err
	}
	if !ok2xx(status) {
		return engineError(status, body)
	}
	return nil
}

// noProxyList is the three hostnames an agent is allowed to know (§1.2
// invariant 10) plus loopback — exactly the destinations that must NOT go
// through the egress proxy — plus any operator-supplied internal additions.
func noProxyList(extra string) string {
	base := "nats,osiris-core,osiris-nats,osiris-egress,localhost,127.0.0.1,::1"
	if extra = strings.TrimSpace(extra); extra != "" {
		return base + "," + extra
	}
	return base
}

// boolEnv renders a bool as the "1"/"0" the supervisor's env parsing expects.
func boolEnv(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func containerName(employeeID string) string { return "osiris-agent-" + employeeID }
func volumeName(employeeID string) string    { return "osiris-ws-" + employeeID }

// credsDir/configDir return this container's own view (for file writes).
// hostCredsDir/hostConfigDir return the path the HOST daemon must use for
// the new agent container's bind mount Source (see hostProjectDir comment).
func (d *Driver) credsDir(id string) string      { return filepath.Join(d.stateDir, "creds", id) }
func (d *Driver) configDir(id string) string     { return filepath.Join(d.stateDir, "config", id) }
func (d *Driver) hostCredsDir(id string) string  { return d.hostProjectDir + "/state/creds/" + id }
func (d *Driver) hostConfigDir(id string) string { return d.hostProjectDir + "/state/config/" + id }

// writeConfigFiles renders CLAUDE.md/settings.json/mcp.json to disk — this
// is what ReloadConfig calls to update a LIVE container's mounted config
// without recreating it (§6.2 A12), and what Apply calls the first time.
//
// PERMISSIONS NOTE (environment-specific deviation from the design's "0700
// dir / 0400 files owned by agentUID" model — see IMPLEMENTATION-NOTES.md):
// this repo's checkout lives on a WSL2 DrvFs-backed path (/mnt/c/...).
// Verified experimentally: DrvFs does not grant root/owner bypass of a
// restrictive file mode the way native ext4 does — chown to a different UID
// silently fails ("Operation not permitted") and a 0400 file becomes
// unwritable even to the process that just created it. Chasing the
// design's exact 0700/0400/chown model on this filesystem produces
// permission-denied failures, not stronger security. So on this host these
// directories/files are 0755/0644 (world-readable within the bind mount,
// which only this orchestrator and the one agent container it's mounted
// into can see anyway) and chown is not attempted. On a native Linux host
// (production target), reverting to 0700/0400+chown would work as designed.
func (d *Driver) writeConfigFiles(spec workload.AgentWorkloadSpec) error {
	dir := d.configDir(spec.EmployeeID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	files := map[string]string{
		"CLAUDE.md":     spec.RenderedPrompt,
		"settings.json": spec.RenderedSettings,
		"mcp.json":      spec.RenderedMCP,
		// §5.2's --allowedTools list, rendered as a file so a permission change
		// is a no-downtime ReloadConfig instead of a container recreate (A12).
		"allowed_tools": spec.RenderedAllowedTools,
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

// SyncCredentials implements workload.WorkloadDriver's rotation-delivery
// method. See the interface's doc comment for WHY it exists; this is the
// [1단계] Docker realization of it, and it is deliberately boring.
//
// The credential directory is ALREADY bind-mounted read-only into the running
// container at /var/run/osiris/creds, so overwriting a file on the host side is
// instantly visible inside the container — no signal, no restart, no container
// mutation of any kind. The supervisor re-reads at exec time (§8.4's "exec
// 시점에 파일을 읽으므로 재시작 없이 반영") and, for the NATS password only,
// reconnects.
//
// Idempotence is enforced by CONTENT COMPARISON, not by trusting the caller:
// this runs on every reconcile tick (every 15s) for every resident agent, and
// rewriting an identical file would churn its mtime 5760 times a day for
// nothing. Worse, the supervisor's credential watcher would have to distinguish
// "mtime moved" from "value changed" — so the driver simply does not move the
// mtime unless the bytes differ.
func (d *Driver) SyncCredentials(ctx context.Context, employeeID string, creds map[string]string) error {
	_ = ctx // no Engine API call at all: this is pure file delivery into an existing mount
	if len(creds) == 0 {
		return nil
	}
	dir := d.credsDir(employeeID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir creds dir: %w", err)
	}
	changed := 0
	for name, val := range creds {
		p := filepath.Join(dir, name)
		if existing, err := os.ReadFile(p); err == nil && string(existing) == val {
			continue // unchanged — do not touch the mtime
		}
		if err := os.WriteFile(p, []byte(val), 0o644); err != nil {
			return fmt.Errorf("write cred %s: %w", name, err)
		}
		changed++
	}
	if changed > 0 {
		// One line, at debug volume, naming only the COUNT — never a value.
		// §9.1: "평문. 로그·이벤트에 절대 싣지 않음".
		log.Printf("osiris-orchestrator: delivered %d rotated credential file(s) to %s (no restart, no reload event — §8.4)", changed, employeeID)
	}
	return nil
}

// writeCredsFiles writes the agent's file-injected credentials (G5: never
// as compose `environment:` plaintext). See writeConfigFiles' permissions
// note — same WSL2/DrvFs deviation applies here.
func (d *Driver) writeCredsFiles(employeeID string, creds map[string]string) error {
	dir := d.credsDir(employeeID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir creds dir: %w", err)
	}
	for name, val := range creds {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(val), 0o644); err != nil {
			return fmt.Errorf("write cred %s: %w", name, err)
		}
	}
	return nil
}

// Apply implements workload.WorkloadDriver. Idempotent per §1.4: volume
// create is a no-op if it exists, config/creds are always rewritten to the
// latest rendered values, and container creation only happens if Observe
// (called by the reconcile loop, not here) found none — Apply itself does
// not check for an existing container because the reconcile loop already
// guarantees it only calls Apply for employeeIDs missing from Observe's
// result.
func (d *Driver) Apply(ctx context.Context, spec workload.AgentWorkloadSpec) (workload.WorkloadRef, error) {
	netID, err := d.resolveAgentsNetwork(ctx)
	if err != nil {
		return workload.WorkloadRef{}, err
	}
	vol := volumeName(spec.EmployeeID)
	if err := d.ensureVolume(ctx, vol); err != nil {
		return workload.WorkloadRef{}, fmt.Errorf("ensure volume %s: %w", vol, err)
	}
	if err := d.writeConfigFiles(spec); err != nil {
		return workload.WorkloadRef{}, err
	}
	// NATS/core credentials: see internal/natsauth for how the NATS
	// user/password themselves are generated and kept in sync with the
	// server's authorization block. This driver only writes whatever
	// reconcile put in spec.Credentials — generating/rotating the values is
	// reconcile's job, materializing them into the file-injection path
	// (§1.4 Apply step 2) is this driver's job.
	if err := d.writeCredsFiles(spec.EmployeeID, spec.Credentials); err != nil {
		return workload.WorkloadRef{}, err
	}

	req := containerCreateRequest{
		Image: spec.Image,
		// NOTE ON WHAT IS AND IS NOT IN HERE (G5): every value below is
		// non-secret identity/topology. Not one credential is passed as an
		// environment variable — tokens and passwords arrive only as files
		// under /var/run/osiris/creds, because "env는 /proc/<pid>/environ으로
		// 오래 노출된다" and because [2단계] must be able to swap file injection
		// for a projected Secret without the supervisor changing.
		Env: []string{
			"OSIRIS_EMPLOYEE_ID=" + spec.EmployeeID,
			"OSIRIS_TEAM_ID=" + spec.TeamID,
			// TeamSlug is what §7.1's subject tree is keyed on
			// (team.<team>.inbox.<employeeId>), and it is NOT always equal to
			// the team id — the supervisor must be told, not left to guess.
			"OSIRIS_TEAM_SLUG=" + spec.TeamSlug,
			"OSIRIS_NATS_URL=nats://nats:4222",
			"OSIRIS_CORE_URL=http://osiris-core:8090",
			"OSIRIS_EGRESS_PROXY=http://osiris-egress:3128",
			// The three hostnames an agent is allowed to know (§1.2 invariant
			// 10) are exactly the ones that must NOT go through the proxy.
			"OSIRIS_NO_PROXY=" + noProxyList(spec.ExtraNoProxy),
			// §9.2 item 5: only the logical alias travels; the gateway resolves
			// it to a provider model.
			"OSIRIS_MODEL_ALIAS=" + spec.ModelAlias,
			// §7.2: INBOX stream, per-agent durable pull consumer. The
			// orchestrator CREATES the consumer (see internal/jetstream) and the
			// supervisor only BINDS to it — an agent that could create consumers
			// on INBOX could create one filtered on another team's inbox and
			// read it, which D14 closes in the publish direction and this closes
			// in the read direction.
			"OSIRIS_INBOX_STREAM=INBOX",
			"OSIRIS_INBOX_SUBJECT=" + fmt.Sprintf("team.%s.inbox.%s", spec.TeamSlug, spec.EmployeeID),
			"OSIRIS_INBOX_DURABLE=" + "agent-" + spec.EmployeeID,
			// §5.5's drain.waitSeconds and §5.4's activeTaskPoints ceiling.
			"OSIRIS_DRAIN_SECONDS=" + fmt.Sprintf("%d", spec.DrainSeconds),
			"OSIRIS_MAX_TASK_POINTS=" + fmt.Sprintf("%d", spec.Concurrency),
			// Master's durable consumer carries TWO filter subjects (its own team
			// inbox AND master.inbox — §7.1 makes master.inbox the mandatory
			// cross-team hop). A multi-filter consumer reports an empty
			// FilterSubject, and nats.go refuses to bind when a non-empty subject
			// is supplied that does not equal the consumer's single filter — so
			// the supervisor must bind with no subject at all in that case. This
			// flag tells it which situation it is in rather than making it guess.
			"OSIRIS_INBOX_MULTIFILTER=" + boolEnv(spec.IsMaster),
		},
		Labels: map[string]string{
			labelManagedBy:  managedByValue,
			labelEmployeeID: spec.EmployeeID,
			labelTeam:       spec.TeamID,
			labelSpecHash:   spec.SpecHash,
			labelConfigHash: spec.ConfigHash,
		},
		User: fmt.Sprintf("%d:%d", agentUID, agentGID),
		// Targets /livez ONLY. Docker health is what the reconcile loop turns
		// into a RESTART (3 consecutive unhealthy, §5.4); readiness must never
		// cause one, so /readyz is deliberately not probed here. The
		// start-period covers node startup plus the supervisor's one-time
		// `claude --version` check.
		Healthcheck: &healthcheckSpec{
			Test:        []string{"CMD", "/osiris-supervisor", "-healthcheck"},
			Interval:    10 * int64(time.Second),
			Timeout:     3 * int64(time.Second),
			Retries:     3,
			StartPeriod: 20 * int64(time.Second),
		},
		HostConfig: hostConfig{
			Memory: spec.Resources.MemMiB * 1024 * 1024,
			// CPU floor is enforced in reconcile.buildSpec (§5.4: "CPU limit은
			// >= 1000m(1 vCPU)을 강제한다(orchestrator가 검증). 헬스 goroutine이
			// 굶지 않도록."), so by the time a spec reaches this driver its
			// CPUMilli is already >= 1000.
			NanoCPUs:       spec.Resources.CPUMilli * 1_000_000,
			RestartPolicy:  restartPolicy{Name: "unless-stopped"},
			ReadonlyRootfs: true,
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges"},
			NetworkMode:    netID,
			Mounts: []mountSpec{
				{Type: "volume", Source: vol, Target: "/workspace"},
				{Type: "bind", Source: d.hostCredsDir(spec.EmployeeID), Target: "/var/run/osiris/creds", ReadOnly: true},
				{Type: "bind", Source: d.hostConfigDir(spec.EmployeeID), Target: "/var/run/osiris/config", ReadOnly: true},
				{Type: "tmpfs", Target: "/tmp", TmpfsOptions: &tmpfsOptions{SizeBytes: 512 * 1024 * 1024}},
			},
		},
		NetworkingConfig: &networkingConfig{
			EndpointsConfig: map[string]endpointSettings{netID: {}},
		},
	}

	q := url.Values{"name": {containerName(spec.EmployeeID)}}
	status, body, err := d.cli.do(ctx, "POST", "/containers/create", q, req)
	if err != nil {
		return workload.WorkloadRef{}, err
	}
	if !ok2xx(status) {
		return workload.WorkloadRef{}, fmt.Errorf("create container for %s: %w", spec.EmployeeID, engineError(status, body))
	}
	var created containerCreateResponse
	if err := unmarshalStrict(body, &created); err != nil {
		return workload.WorkloadRef{}, err
	}

	status, body, err = d.cli.do(ctx, "POST", "/containers/"+created.ID+"/start", nil, nil)
	if err != nil {
		return workload.WorkloadRef{}, err
	}
	if !ok2xx(status) {
		return workload.WorkloadRef{}, fmt.Errorf("start container for %s: %w", spec.EmployeeID, engineError(status, body))
	}
	return workload.WorkloadRef{ID: created.ID}, nil
}

// Recreate implements workload.WorkloadDriver: drain (graceful SIGTERM,
// bounded wait) → delete → apply. This is the ONLY path that changes a
// container's identity (new container ID) — reserved for SpecHash changes
// (image/resources/network/drain-time), never for config-only edits (A12).
func (d *Driver) Recreate(ctx context.Context, spec workload.AgentWorkloadSpec) error {
	if err := d.Drain(ctx, spec.EmployeeID, spec.DrainSeconds); err != nil {
		return fmt.Errorf("recreate %s: drain: %w", spec.EmployeeID, err)
	}
	if err := d.Delete(ctx, spec.EmployeeID); err != nil {
		return fmt.Errorf("recreate %s: delete: %w", spec.EmployeeID, err)
	}
	if _, err := d.Apply(ctx, spec); err != nil {
		return fmt.Errorf("recreate %s: apply: %w", spec.EmployeeID, err)
	}
	return nil
}

// ReloadConfig implements workload.WorkloadDriver: rewrite the rendered
// files the running container has read-only bind-mounted, WITHOUT touching
// the container itself (no stop/restart, no new container ID — A12's whole
// point). The live process only picks the new files up on ITS OWN terms
// (§5.6: "다음 태스크부터" — the supervisor decides
// when to re-read; this driver's job ends at "the files on disk are now
// current" + notifying it).
func (d *Driver) ReloadConfig(ctx context.Context, spec workload.AgentWorkloadSpec) error {
	if err := d.writeConfigFiles(spec); err != nil {
		return fmt.Errorf("reload config %s: %w", spec.EmployeeID, err)
	}
	// Update the config-hash label so the NEXT Observe() call reports the
	// new ConfigHash without requiring a container recreate — label updates
	// require ContainerUpdate's restart-free `ContainerRename`-adjacent path
	// is not available for labels via the Engine API, so we track the
	// applied ConfigHash in-memory instead (see reconcile.go's
	// appliedConfigHash map) rather than attempting to mutate an existing
	// container's labels (Docker does not support that).
	return nil
}

func (d *Driver) findContainerID(ctx context.Context, employeeID string) (string, bool, error) {
	items, err := d.listManaged(ctx)
	if err != nil {
		return "", false, err
	}
	for _, it := range items {
		if it.Labels[labelEmployeeID] == employeeID {
			return it.ID, true, nil
		}
	}
	return "", false, nil
}

// Drain implements workload.WorkloadDriver via Docker's own ContainerStop,
// whose semantics (SIGTERM, wait up to `t` seconds, then SIGKILL) are
// exactly §5.5's graceful-shutdown contract — no separate signal-then-wait
// loop needed, the Engine API already does it.
func (d *Driver) Drain(ctx context.Context, employeeID string, waitSeconds int) error {
	id, found, err := d.findContainerID(ctx, employeeID)
	if err != nil {
		return err
	}
	if !found {
		return nil // already gone — draining a nonexistent container is a no-op, not an error
	}
	if waitSeconds <= 0 {
		waitSeconds = 600
	}
	q := url.Values{"t": {fmt.Sprintf("%d", waitSeconds)}}
	status, body, err := d.cli.do(ctx, "POST", "/containers/"+id+"/stop", q, nil)
	if err != nil {
		return err
	}
	if !ok2xx(status) && status != 304 { // 304 = already stopped
		return engineError(status, body)
	}
	return nil
}

// Delete implements workload.WorkloadDriver. Named volumes are deliberately
// NOT removed here — see IMPLEMENTATION-NOTES.md's documented gap on
// workspace volume garbage collection.
func (d *Driver) Delete(ctx context.Context, employeeID string) error {
	id, found, err := d.findContainerID(ctx, employeeID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	q := url.Values{"force": {"true"}}
	status, body, err := d.cli.do(ctx, "DELETE", "/containers/"+id, q, nil)
	if err != nil {
		return err
	}
	if !ok2xx(status) && status != 404 {
		return engineError(status, body)
	}
	return nil
}

func (d *Driver) listManaged(ctx context.Context) ([]containerListItem, error) {
	filters := fmt.Sprintf(`{"label":["%s=%s"]}`, labelManagedBy, managedByValue)
	q := url.Values{"all": {"true"}, "filters": {filters}}
	status, body, err := d.cli.do(ctx, "GET", "/containers/json", q, nil)
	if err != nil {
		return nil, err
	}
	if !ok2xx(status) {
		return nil, engineError(status, body)
	}
	var items []containerListItem
	if err := unmarshalStrict(body, &items); err != nil {
		return nil, fmt.Errorf("list managed containers: %w", err)
	}
	return items, nil
}

// Observe implements workload.WorkloadDriver — the OBSERVATION layer
// (§6.1). Every call re-derives phase/health fresh from the Engine API; the
// only cross-call state kept is the consecutive-unhealthy streak (§5.4's
// "3연속" judgment needs memory of the previous ticks).
func (d *Driver) Observe(ctx context.Context) (map[string]workload.WorkloadStatus, error) {
	items, err := d.listManaged(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]workload.WorkloadStatus{}
	now := time.Now()
	seen := map[string]bool{}
	for _, it := range items {
		empID := it.Labels[labelEmployeeID]
		if empID == "" {
			continue
		}
		seen[empID] = true
		status, body, err := d.cli.do(ctx, "GET", "/containers/"+it.ID+"/json", nil, nil)
		if err != nil {
			return nil, err
		}
		if !ok2xx(status) {
			return nil, engineError(status, body)
		}
		var insp containerInspect
		if err := unmarshalStrict(body, &insp); err != nil {
			return nil, err
		}
		health := "none"
		if insp.State.Health != nil {
			health = insp.State.Health.Status
		}
		if health == "unhealthy" {
			d.unhealthyStreak[empID]++
		} else {
			d.unhealthyStreak[empID] = 0
		}
		out[empID] = workload.WorkloadStatus{
			EmployeeID:           empID,
			Ref:                  workload.WorkloadRef{ID: it.ID},
			Phase:                insp.State.Status,
			Health:               health,
			SpecHash:             insp.Config.Labels[labelSpecHash],
			ConfigHash:           insp.Config.Labels[labelConfigHash],
			ConsecutiveUnhealthy: d.unhealthyStreak[empID],
			ObservedAt:           now,
		}
	}
	// forget streaks for employeeIDs no longer observed at all
	for id := range d.unhealthyStreak {
		if !seen[id] {
			delete(d.unhealthyStreak, id)
		}
	}
	return out, nil
}

// KillContainerByExactName sends a raw signal to a container looked up by
// its EXACT name (not by the osiris.dev/managed-by label — this is used
// exactly once, by internal/natsauth, to send SIGHUP to the nats platform
// container for a config reload). Deliberately narrow: no filter-based bulk
// operation, no label match, just "this one well-known name, this one
// signal" — so a bug here cannot accidentally reach any other container.
func (d *Driver) KillContainerByExactName(ctx context.Context, name, signal string) error {
	status, body, err := d.cli.do(ctx, "GET", "/containers/json", url.Values{"all": {"true"}}, nil)
	if err != nil {
		return err
	}
	if !ok2xx(status) {
		return engineError(status, body)
	}
	var items []containerListItem
	if err := unmarshalStrict(body, &items); err != nil {
		return err
	}
	target := "/" + name
	var id string
	for _, it := range items {
		for _, n := range it.Names {
			if n == target {
				id = it.ID
			}
		}
	}
	if id == "" {
		return fmt.Errorf("container named %q not found", name)
	}
	q := url.Values{"signal": {signal}}
	status, body, err = d.cli.do(ctx, "POST", "/containers/"+id+"/kill", q, nil)
	if err != nil {
		return err
	}
	if !ok2xx(status) {
		return engineError(status, body)
	}
	return nil
}

// ImageExists is a diagnostic helper Apply's caller can use to fail with a
// clear message ("image not present locally") rather than a confusing
// 404 deep in container-create — this build does not implement image
// pulling (A11 covers container lifecycle, not registry management; the
// stub/whatever agent image is expected to already be built/loaded
// locally, same as docker-compose's own `build:` services).
func (d *Driver) ImageExists(ctx context.Context, ref string) (bool, error) {
	status, body, err := d.cli.do(ctx, "GET", "/images/"+strings.TrimPrefix(ref, "/")+"/json", nil, nil)
	if err != nil {
		return false, err
	}
	if status == 404 {
		return false, nil
	}
	if !ok2xx(status) {
		return false, engineError(status, body)
	}
	var img imageSummary
	_ = unmarshalStrict(body, &img)
	return true, nil
}
