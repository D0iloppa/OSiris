// Package workload defines the driver-agnostic contract between the
// reconcile loop and the underlying runtime (Docker in [1단계], Kube in
// [2단계]). Nothing in this package or its callers (reconcile, main) may
// import a Docker-specific type — that boundary is what lets the [2단계]
// KubeDriver replace DockerDriver with zero changes above this package
// (docs/design/10-core-architecture.md §6.2 D3, G3). Only
// internal/driver/docker may know what a "container" is.
package workload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	DesiredRunning   = "Running"
	DesiredSuspended = "Suspended"
)

// ResourceProfile mirrors :RoleResourceProfile (20-ontology-schema.md §3.7).
type ResourceProfile struct {
	CPUMilli   int64
	MemMiB     int64
	StorageGiB int64
}

// AgentWorkloadSpec is the [1단계] Go embodiment of
// 10-core-architecture.md §6.2's AgentWorkloadSpec. It is always derived
// fresh from Neo4j (the intent) — there is no [1단계] materialization store
// (§6.1: "[1단계] 저장소 없음. orchestrator가 매 루프마다 그래프에서
// 재계산(메모리)").
type AgentWorkloadSpec struct {
	EmployeeID string
	TeamID     string
	TeamSlug   string
	RoleID     string
	IsMaster   bool
	IsLead     bool

	Image       string
	ModelAlias  string
	Resources   ResourceProfile
	Grants      []string // effective permission codes, resolved from §7.1 effective(party), sorted+deduped
	NetworkName string   // resolved osiris-agents network name/ID — part of SpecHash (network is one of the axes §6.2 lists)

	Concurrency  int // maxTaskPoints
	DrainSeconds int
	Desired      string // DesiredRunning | DesiredSuspended

	// ExtraNoProxy is appended to the agent's NO_PROXY (see
	// config.AgentExtraNoProxy). Part of neither hash: it is topology, and
	// changing it should not recreate a container — the supervisor only reads
	// NO_PROXY when it builds a child env, i.e. at the next turn.
	ExtraNoProxy string

	// Rendered configuration — file contents the driver writes without
	// recreating the container (§6.2's ConfigHash axis).
	RenderedPrompt       string // CLAUDE.md, from :Role.promptTemplateMd (§3.2.1)
	RenderedSettings     string // settings.json (permissions.allow/deny derived from Grants)
	RenderedMCP          string // mcp.json — the gated-tool MCP servers derived from Grants (§8.2 fallback b)
	RenderedAllowedTools string // allowed_tools — §5.2's --allowedTools argument list, one per line

	// Credentials is named-blob content (nats_user, nats_password,
	// core_token, ...) the driver must make available to the workload via
	// whatever file-injection mechanism it owns (§1.4 Apply step 2: "자격증명
	// 디렉토리 ... 생성 · 파일 기록"). Added deliberately as a NEUTRAL map —
	// not a docker-specific type — so [2단계]'s KubeDriver can materialize
	// the same map as a Secret's data instead of bind-mounted files without
	// this struct changing. reconcile owns generating/rotating the values
	// (internal/natsauth for NATS creds); the driver only ever writes
	// whatever it's handed. Deliberately EXCLUDED from both SpecHash and
	// ConfigHash (see ComputeHashes) — credential rotation must never force
	// a Recreate or even a logged ReloadConfig; §8.4 says rotation is
	// "재시작 없이 반영"(no restart at all, not even a config reload event).
	Credentials map[string]string

	SpecHash   string
	ConfigHash string
}

// ComputeHashes fills SpecHash/ConfigHash from the fields above. Deliberately
// separated per A12: SpecHash covers only what forces a container recreate
// (image, resources, network, drain time, concurrency); ConfigHash covers
// only what a live ReloadConfig can apply (rendered files + model alias).
// Mixing the two would mean a one-line promptTemplateMd edit recreates every
// agent in that role (§3.2.1, §6.2) — exactly what A12 exists to prevent.
func (s *AgentWorkloadSpec) ComputeHashes() {
	spec := strings.Join([]string{
		s.Image,
		fmt.Sprintf("cpu=%d", s.Resources.CPUMilli),
		fmt.Sprintf("mem=%d", s.Resources.MemMiB),
		fmt.Sprintf("storage=%d", s.Resources.StorageGiB),
		"net=" + s.NetworkName,
		fmt.Sprintf("drain=%d", s.DrainSeconds),
		fmt.Sprintf("concurrency=%d", s.Concurrency),
	}, "|")
	s.SpecHash = shortHash(spec)

	cfg := strings.Join([]string{
		s.ModelAlias,
		s.RenderedPrompt,
		s.RenderedSettings,
		s.RenderedMCP,
		s.RenderedAllowedTools,
	}, "\x00")
	s.ConfigHash = shortHash(cfg)
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// SortedGrants returns a deduplicated, sorted copy — call before hashing or
// rendering so equal grant sets always produce equal output regardless of
// the order Neo4j happened to return them in.
func SortedGrants(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range in {
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// WorkloadRef is the opaque handle Apply returns. [1단계]: a container ID.
// [2단계]: would be a Pod name. Callers must not interpret its contents.
type WorkloadRef struct {
	ID string
}

// WorkloadStatus is the OBSERVATION layer (§6.1) — never authoritative,
// always timestamped. The reconcile loop is the only reader; nothing writes
// this into Neo4j in this build (see IMPLEMENTATION-NOTES.md for why that's
// an accepted, documented gap rather than a silent omission).
type WorkloadStatus struct {
	EmployeeID           string
	Ref                  WorkloadRef
	Phase                string // docker container State.Status: running/created/exited/restarting/...
	Health               string // docker Health.Status: healthy/unhealthy/starting/none
	SpecHash             string // from the container's osiris.dev/spec-hash label
	ConfigHash           string // from the container's osiris.dev/config-hash label
	ConsecutiveUnhealthy int
	ObservedAt           time.Time
}

// WorkloadDriver is 10-core-architecture.md §6.2's driver interface,
// verbatim. [1단계]: driver/docker.Driver. [2단계]: a controller-runtime
// based driver/kube.Driver would implement the same interface with the same
// method set — that substitutability is the entire point of this package
// boundary (D3).
type WorkloadDriver interface {
	Apply(ctx context.Context, spec AgentWorkloadSpec) (WorkloadRef, error)
	Recreate(ctx context.Context, spec AgentWorkloadSpec) error
	ReloadConfig(ctx context.Context, spec AgentWorkloadSpec) error
	Drain(ctx context.Context, employeeID string, waitSeconds int) error
	Delete(ctx context.Context, employeeID string) error
	Observe(ctx context.Context) (map[string]WorkloadStatus, error)

	// SyncCredentials delivers a (possibly rotated) credential set to an
	// ALREADY-RUNNING workload, and is the ONE method added to §6.2's interface
	// by the I4 build. The reasoning, because adding to this interface is not a
	// small thing (D3/G3 make it the single rewrite point for [1단계]→[2단계]):
	//
	// The I3 worker correctly excluded Credentials from BOTH SpecHash and
	// ConfigHash, because §8.4 requires rotation to be "재시작 없이 반영" — not a
	// recreate, and not even a logged ReloadConfig. That decision is right and is
	// kept. What it left open is a WRITER: Apply/Recreate write credentials, but
	// they only run when a container is created; ReloadConfig runs only when
	// ConfigHash moves, which rotation deliberately does not do. So for a
	// long-running agent there was no path at all by which a fresh 24h gateway
	// token (§8.4) could reach the container.
	//
	// Three ways to close that were available:
	//   (1) make ReloadConfig write creds too — rejected: it fires only on a
	//       ConfigHash change, so rotation would be delivered at an unrelated,
	//       arbitrary time, and forcing a ConfigHash change to trigger it would
	//       re-couple exactly what was correctly decoupled;
	//   (2) re-export a docker-specific WriteCreds method — rejected: that is
	//       precisely the G3 violation the I3 worker fixed (NOTES §24);
	//   (3) a neutral interface method. Chosen.
	//
	// It stays driver-agnostic: the argument is a plain map[string]string, the
	// same shape AgentWorkloadSpec.Credentials already uses, so [2단계]'s
	// KubeDriver implements it by updating the Secret's data (kubelet then
	// refreshes the projected volume, and the supervisor's read-at-exec picks it
	// up — identical semantics, zero changes above this interface).
	//
	// CONTRACT: idempotent and SILENT. The driver must not rewrite a file whose
	// content already matches, must not emit an event, and must not touch the
	// container. The supervisor detects rotation by content fingerprint, so a
	// no-op call has literally no observable effect.
	SyncCredentials(ctx context.Context, employeeID string, creds map[string]string) error
}
