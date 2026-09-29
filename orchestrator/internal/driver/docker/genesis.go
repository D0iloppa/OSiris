package docker

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// ===========================================================================
// The bootstrap-master (genesis) container — docs/design/10-core-architecture.md
// §8.5's sequence, steps 2, 3, 6 and 7.
//
// WHY THIS IS NOT Apply(). Apply creates AND starts in one call, which is
// correct for an ordinary agent: everything it needs is known before creation.
// The genesis container is different in exactly one way, and that way is the
// whole security argument of §8.5's third amendment:
//
//	the token must be BOUND to the container id,
//	the container id does not exist until ContainerCreate returns,
//	so the token cannot be written before creation —
//	and the container must not be RUNNING before the token is written,
//	or there is a window in which it is up without a credential.
//
// Hence create → issue → write → start, as four steps a caller sequences. §8.5
// notes this is not a new capability: "기존 §1.4 Apply()가 이미 5단계
// (ContainerCreate)와 6단계(ContainerStart)를 분리된 두 호출로 다루고 있는 이
// 저장소의 기존 패턴 그대로다."
//
// ── ★ DEVIATION FROM §8.5's LITERAL FILE PATH (reported as F16) ───────────
//
// §8.5's table says the token lives at host `state/creds/<containerID>/genesis_token`.
// THAT PATH IS UNIMPLEMENTABLE GIVEN §8.5's OWN STEP ORDER, and the conflict is
// worth stating precisely rather than quietly working around:
//
//   - A bind mount's source is fixed in the ContainerCreate request (step 2).
//   - The container id is only known from ContainerCreate's RESPONSE (step 3).
//   - So a mount source containing the container id cannot be declared at the
//     moment it has to be declared.
//   - Letting Docker auto-create a missing bind source does not rescue it: the
//     engine creates it root-owned, which breaks both the 0700 directory and the
//     agent uid that must read the file.
//
// So the directory is keyed by the container's STABLE NAME instead
// (state/creds/bootstrap-master/). W4 is untouched by this: the binding that W4
// requires is the one osiris-core records between the token and the container id
// (genesis.Store.Issue), not the filesystem path the file happens to sit at. The
// path is an injection detail; the binding is an authorization fact.
// ===========================================================================

// GenesisContainerName is the container's stable name, and therefore also the
// key for its credential directory (see the deviation note above). One name
// because 30 §1.3 permits exactly one bootstrap-master at a time.
const GenesisContainerName = "osiris-bootstrap-master"

// GenesisCredsKey is the credential-directory key. Equal to the container name
// without the osiris- prefix so the on-disk layout reads like the other
// per-workload directories under state/creds/.
const GenesisCredsKey = "bootstrap-master"

// GenesisTokenFile is the file name the supervisor reads inside the container at
// /var/run/osiris/creds/genesis_token.
const GenesisTokenFile = "genesis_token"

// GenesisSpec is everything creation needs. Deliberately tiny: the genesis
// container gets no team, no inbox, no NATS user (§8.5: "NATS 권한 없음"), and no
// model alias beyond the gateway defaults the image already reads.
type GenesisSpec struct {
	Image string

	// Env carries only non-secret topology, same rule as Apply (G5). The token
	// never appears here — it arrives as a file, which is the entire point of
	// steps 6-7.
	Env []string
}

// CreateGenesisContainer performs §8.5 steps 2-3: prepare the 0700 credential
// directory, then ContainerCreate ONLY, and return the id.
//
// IT DOES NOT START THE CONTAINER, and callers must not add that here. The gap
// between this call and StartGenesisContainer is where the token is issued and
// written; closing the gap by starting early would put a credential-less genesis
// container on the agents network, which is the state §8.5 step 7's comment
// says must never exist ("네트워크로 도달 가능한 제3의 프로세스도 없었다").
func (d *Driver) CreateGenesisContainer(ctx context.Context, spec GenesisSpec) (string, error) {
	netID, err := d.resolveAgentsNetwork(ctx)
	if err != nil {
		return "", err
	}

	// Step 2's "호스트에 이 컨테이너 전용 자격증명 디렉토리(0700)를 준비하고".
	// Created BEFORE the create call so the bind source exists and is owned by
	// this process rather than auto-created root-owned by the engine.
	dir := d.credsDir(GenesisCredsKey)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir genesis creds dir: %w", err)
	}
	// A stale token from a previous, abandoned attempt must not survive into a
	// new one: it would be a credential core no longer recognizes, and its
	// presence would make "the file is here" stop meaning "the token is live".
	if err := os.Remove(filepath.Join(dir, GenesisTokenFile)); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("clear stale genesis token: %w", err)
	}

	req := containerCreateRequest{
		Image: spec.Image,
		Env:   spec.Env,
		Labels: map[string]string{
			// ★ NOT labelEmployeeID. 30 §1.3: bootstrap-master는 그래프 노드가
			// 아니므로 orchestrator의 그래프 기반 정리 대상이 아니다. Giving it an
			// employee-id label would make Observe() report it as a managed agent
			// and the reconcile loop would delete it as an orphan — the exact
			// failure §1.5 avoids by making teardown explicit.
			labelManagedBy: managedByValue,
			labelGenesis:   "true",
		},
		User: fmt.Sprintf("%d:%d", agentUID, agentGID),
		HostConfig: hostConfig{
			// No restart policy. A genesis container that resurrects itself after
			// the onboarding commit would outlive the window W3 closes.
			RestartPolicy:  restartPolicy{Name: "no"},
			ReadonlyRootfs: true,
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges"},
			NetworkMode:    netID,
			Mounts: []mountSpec{
				// §8.5: "네트워크 | osiris-agents 하나. osiris-core:8090와
				// osiris-egress만 도달". No /workspace volume — this container
				// writes no deliverables; it holds a conversation.
				{Type: "bind", Source: d.hostCredsDir(GenesisCredsKey), Target: "/var/run/osiris/creds", ReadOnly: true},
				{Type: "tmpfs", Target: "/tmp", TmpfsOptions: &tmpfsOptions{SizeBytes: 256 * 1024 * 1024}},
			},
		},
		NetworkingConfig: &networkingConfig{
			EndpointsConfig: map[string]endpointSettings{netID: {}},
		},
	}

	q := url.Values{"name": {GenesisContainerName}}
	status, body, err := d.cli.do(ctx, "POST", "/containers/create", q, req)
	if err != nil {
		return "", err
	}
	if !ok2xx(status) {
		return "", fmt.Errorf("create genesis container: %w", engineError(status, body))
	}
	var created containerCreateResponse
	if err := unmarshalStrict(body, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

// WriteGenesisToken performs §8.5 step 6: write the token to the already-mounted
// credential directory, 0400, while the container is still not running.
//
// 0400 and not 0600: nothing ever rewrites this file. A token that needs
// replacing is a new token for a new container, and the re-issue path recreates
// the file from scratch (CreateGenesisContainer clears it first).
func (d *Driver) WriteGenesisToken(token string) error {
	if token == "" {
		return fmt.Errorf("refusing to write an empty genesis token")
	}
	dir := d.credsDir(GenesisCredsKey)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir genesis creds dir: %w", err)
	}
	path := filepath.Join(dir, GenesisTokenFile)
	// Remove first: os.WriteFile does not re-apply the mode to an existing file,
	// so a leftover 0644 from any earlier build would silently persist.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear genesis token: %w", err)
	}
	if err := os.WriteFile(path, []byte(token), 0o400); err != nil {
		return fmt.Errorf("write genesis token: %w", err)
	}
	return nil
}

// GenesisTokenPresent reports whether the token file exists on disk. Used only
// for status reporting and for the ordering assertion in tests — never as an
// authorization input, because the authority on a token's validity is
// osiris-core's store, not the presence of a file.
func (d *Driver) GenesisTokenPresent() bool {
	_, err := os.Stat(filepath.Join(d.credsDir(GenesisCredsKey), GenesisTokenFile))
	return err == nil
}

// StartGenesisContainer performs §8.5 step 7.
func (d *Driver) StartGenesisContainer(ctx context.Context, id string) error {
	status, body, err := d.cli.do(ctx, "POST", "/containers/"+id+"/start", nil, nil)
	if err != nil {
		return err
	}
	if !ok2xx(status) {
		return fmt.Errorf("start genesis container: %w", engineError(status, body))
	}
	return nil
}

// TeardownGenesisContainer implements 30 §1.5: drain, then delete, then remove
// the credential file.
//
// BY NAME, NOT BY LABEL. The graph-driven Drain/Delete pair keys on
// labelEmployeeID, and this container deliberately has none (see the label
// comment in CreateGenesisContainer) — so teardown has to be explicit, which is
// what §1.5 says it is: "폐기는 온보딩 커밋 완료 루틴이 명시적으로 수행한다".
//
// Drain is effectively instant here: §1.3 gives bootstrap-master no task queue,
// so there is nothing to finish. The wait exists so a conversation in flight gets
// a SIGTERM and a moment to close rather than being killed mid-write.
//
// THE FILE IS REMOVED LAST AND ITS FAILURE IS NOT FATAL — the same two-dimension
// argument H23 makes for the master key: the credential is already dead because
// core revoked it (W3), so a leftover file opens nothing. Reporting a stuck file
// as a failed teardown would make the caller retry a teardown that already
// succeeded where it mattered.
func (d *Driver) TeardownGenesisContainer(ctx context.Context, waitSeconds int) error {
	if waitSeconds <= 0 {
		waitSeconds = 10
	}
	// SIGTERM + bounded wait. A container that is already gone is not an error:
	// teardown is idempotent because it runs from a cleanup routine that may be
	// retried.
	_, _, _ = d.cli.do(ctx, "POST", "/containers/"+GenesisContainerName+"/stop",
		url.Values{"t": {fmt.Sprintf("%d", waitSeconds)}}, nil)

	status, body, err := d.cli.do(ctx, "DELETE", "/containers/"+GenesisContainerName,
		url.Values{"force": {"true"}}, nil)
	if err != nil {
		return err
	}
	if !ok2xx(status) && status != 404 {
		return fmt.Errorf("delete genesis container: %w", engineError(status, body))
	}

	if rmErr := os.Remove(filepath.Join(d.credsDir(GenesisCredsKey), GenesisTokenFile)); rmErr != nil && !os.IsNotExist(rmErr) {
		return nil // logged by the caller; see the doc comment on why this is not fatal
	}
	return nil
}

// GenesisDrainWait is the bounded SIGTERM wait for teardown.
const GenesisDrainWait = 10 * time.Second
