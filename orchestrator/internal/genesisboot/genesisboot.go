// Package genesisboot sequences docs/design/10-core-architecture.md §8.5's
// seven steps — the orchestrator half of bringing up the bootstrap-master
// (onboarding consultant) container with a genesis token bound to it.
//
// ── THE ORDER IS THE SECURITY PROPERTY, SO THE ORDER IS THE API ────────────
//
//  2. prepare the 0700 creds dir, ContainerCreate ONLY
//  3. obtain the real container id
//  4. ask osiris-core to issue, passing that id
//  5. core confirms currentOrgRevision == 0 and issues+binds atomically
//  6. write the token file (0400) — container still NOT running
//  7. ContainerStart
//
// §8.5's argument for why this yields a window of ZERO rather than a narrow one:
// the binding is not decided by "who connects first" but by core's single
// issuance decision, and before step 7 nothing is reachable on the agents
// network because the container is not running. Bring() below is written as a
// straight line through those steps for exactly that reason — a caller cannot
// accidentally start the container before the token exists, because there is no
// exported call that starts it.
//
// ── WHY THIS IS NOT IN THE RECONCILE LOOP ─────────────────────────────────
//
// The reconcile loop converges the graph's DESIRED agent set onto the observed
// one. bootstrap-master is not in that set and must not be (30 §1.3: it is not a
// graph node), so a loop that managed it would have to special-case an entity its
// whole design says it does not know about. It is a one-shot, externally
// triggered event, and it lives in its own package for the same reason §1.5 makes
// its teardown explicit rather than convergent.
package genesisboot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ContainerDriver is the slice of the docker driver this package needs.
//
// AN INTERFACE, SO THE ORDER IS TESTABLE. The live happy path cannot be measured
// on any deployment that has ever committed an organization (W1 requires
// revision 0), so the ordering guarantees above are verified against a fake that
// records the sequence. That is the whole reason this is not a concrete type.
type ContainerDriver interface {
	CreateGenesisContainer(ctx context.Context, spec CreateSpec) (string, error)
	WriteGenesisToken(token string) error
	StartGenesisContainer(ctx context.Context, id string) error
	TeardownGenesisContainer(ctx context.Context, waitSeconds int) error
}

// CreateSpec mirrors docker.GenesisSpec without importing the driver, so the
// dependency points one way (driver ← this package) and a test needs no Docker
// types at all.
type CreateSpec struct {
	Image string
	Env   []string
}

// Issuer is osiris-core's genesis-token endpoint.
type Issuer interface {
	Issue(ctx context.Context, containerID string) (token string, expiresAt time.Time, err error)
	Revoke(ctx context.Context) error
}

// ErrOrgExists is returned when core refuses because W1 has closed. It is a
// TERMINAL condition, not a retryable one: an organization exists, so onboarding
// with a genesis subject is over for this installation.
var ErrOrgExists = errors.New("an organization already exists; the genesis window is closed (W1)")

// ErrInFlight is returned when a bring-up is already running. Single-flight
// matters here beyond tidiness: two concurrent sequences would create two
// containers competing for one name and, worse, the second issuance would
// invalidate the first container's token while that container was already
// starting (core keeps at most one record — see genesis.Store).
var ErrInFlight = errors.New("a bootstrap-master bring-up is already in progress")

// Phase is the externally visible progress of the sequence, surfaced through the
// orchestrator's /status so osiris-api can show onboarding progress without
// polling Docker.
type Phase string

const (
	PhaseIdle      Phase = "idle"
	PhaseCreating  Phase = "creating"
	PhaseIssuing   Phase = "issuing"
	PhaseInjecting Phase = "injecting"
	PhaseStarting  Phase = "starting"
	PhaseRunning   Phase = "running"
	PhaseFailed    Phase = "failed"
	PhaseTornDown  Phase = "torn_down"
)

// State is a snapshot. NO TOKEN, NO FINGERPRINT, NO EXPIRY-DERIVED SECRET —
// everything here is safe to serve on /status, which is unauthenticated on the
// internal network.
type State struct {
	Phase       Phase     `json:"phase"`
	ContainerID string    `json:"containerId,omitempty"`
	StartedAt   time.Time `json:"startedAt,omitempty"`
	// TokenExpiresAt is a timestamp, not a credential: the console needs it to
	// warn that a 24h conversation is about to outrun W2's TTL.
	TokenExpiresAt time.Time `json:"tokenExpiresAt,omitempty"`
	LastError      string    `json:"lastError,omitempty"`
}

// Manager owns the sequence and its state.
type Manager struct {
	drv    ContainerDriver
	issuer Issuer
	image  string
	env    []string
	log    func(format string, args ...any)

	mu       sync.Mutex
	inFlight bool
	state    State
}

func New(drv ContainerDriver, issuer Issuer, image string, env []string, logf func(string, ...any)) *Manager {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Manager{
		drv: drv, issuer: issuer, image: image, env: env, log: logf,
		state: State{Phase: PhaseIdle},
	}
}

func (m *Manager) Snapshot() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Manager) setPhase(p Phase, mutate func(*State)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Phase = p
	if mutate != nil {
		mutate(&m.state)
	}
}

// Bring runs steps 2-7. It is synchronous; the caller decides whether to run it
// in a goroutine (the HTTP surface does, so a request handler never waits on the
// Docker API — see internal/httpapi's package doc for that discipline).
//
// ON FAILURE IT DOES NOT LEAVE A RUNNING CONTAINER. Every early return happens
// before step 7, and a failure after creation tears the created-but-never-started
// container down: a stopped container holding no token is harmless, but leaving
// it would make the next attempt collide on the name.
func (m *Manager) Bring(ctx context.Context) error {
	m.mu.Lock()
	if m.inFlight {
		m.mu.Unlock()
		return ErrInFlight
	}
	m.inFlight = true
	m.state = State{Phase: PhaseCreating, StartedAt: time.Now()}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.inFlight = false
		m.mu.Unlock()
	}()

	fail := func(step string, err error) error {
		wrapped := fmt.Errorf("%s: %w", step, err)
		m.setPhase(PhaseFailed, func(s *State) { s.LastError = wrapped.Error() })
		m.log("osiris-orchestrator: bootstrap-master bring-up failed at %s: %v", step, err)
		return wrapped
	}

	// --- steps 2-3 ---------------------------------------------------------
	id, err := m.drv.CreateGenesisContainer(ctx, CreateSpec{Image: m.image, Env: m.env})
	if err != nil {
		return fail("create", err)
	}
	m.setPhase(PhaseIssuing, func(s *State) { s.ContainerID = id })
	m.log("osiris-orchestrator: created bootstrap-master container %s (not started — the token comes first, §8.5)", short(id))

	// --- steps 4-5 ---------------------------------------------------------
	tok, expiresAt, err := m.issuer.Issue(ctx, id)
	if err != nil {
		// The created container is useless without a token, and leaving it would
		// block the next attempt on the container name.
		if tdErr := m.drv.TeardownGenesisContainer(ctx, 1); tdErr != nil {
			m.log("osiris-orchestrator: WARNING: could not remove the un-tokened container %s: %v", short(id), tdErr)
		}
		return fail("issue", err)
	}

	// --- step 6 ------------------------------------------------------------
	m.setPhase(PhaseInjecting, func(s *State) { s.TokenExpiresAt = expiresAt })
	if err := m.drv.WriteGenesisToken(tok); err != nil {
		// A token exists in core but cannot be delivered. Revoke it rather than
		// leaving a live credential nobody holds — the same reasoning W3 applies
		// after a commit, applied to a failed bring-up.
		if rErr := m.issuer.Revoke(ctx); rErr != nil {
			m.log("osiris-orchestrator: WARNING: token written nowhere and revoke failed: %v", rErr)
		}
		if tdErr := m.drv.TeardownGenesisContainer(ctx, 1); tdErr != nil {
			m.log("osiris-orchestrator: WARNING: teardown after injection failure: %v", tdErr)
		}
		return fail("inject", err)
	}

	// --- step 7 ------------------------------------------------------------
	m.setPhase(PhaseStarting, nil)
	if err := m.drv.StartGenesisContainer(ctx, id); err != nil {
		if rErr := m.issuer.Revoke(ctx); rErr != nil {
			m.log("osiris-orchestrator: WARNING: revoke after start failure: %v", rErr)
		}
		if tdErr := m.drv.TeardownGenesisContainer(ctx, 1); tdErr != nil {
			m.log("osiris-orchestrator: WARNING: teardown after start failure: %v", tdErr)
		}
		return fail("start", err)
	}

	m.setPhase(PhaseRunning, nil)
	m.log("osiris-orchestrator: bootstrap-master %s running, genesis token bound and expiring %s (W2)",
		short(id), expiresAt.UTC().Format(time.RFC3339))
	return nil
}

// Phase is the current phase as a string, for the HTTP surface (which must not
// import this package's State type — see internal/httpapi's no-Docker-types
// property, applied one layer up).
func (m *Manager) Phase() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return string(m.state.Phase)
}

// Teardown implements 30 §1.5 plus W3, IN THAT ORDER: revoke the credential
// first, then remove the container.
//
// THE ORDER MATTERS AND IS THE OPPOSITE OF THE INTUITIVE ONE. Removing the
// container first would leave, for the duration of the revoke call, a live
// genesis token whose bound container no longer exists — and core's binding check
// cannot notice that, because it compares against a recorded id, not against
// Docker. Revoking first means the credential is dead before its holder
// disappears, so there is no instant at which a leaked token is both live and
// unattributable.
//
// Both halves are attempted even if the first fails: a revoke error must not
// leave the container running.
func (m *Manager) Teardown(ctx context.Context, waitSeconds int) error {
	var firstErr error
	if err := m.issuer.Revoke(ctx); err != nil {
		firstErr = fmt.Errorf("revoke genesis token: %w", err)
		m.log("osiris-orchestrator: WARNING: %v (continuing to container teardown)", firstErr)
	}
	if err := m.drv.TeardownGenesisContainer(ctx, waitSeconds); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("teardown genesis container: %w", err)
		}
	}
	m.setPhase(PhaseTornDown, func(s *State) { s.ContainerID = ""; s.TokenExpiresAt = time.Time{} })
	if firstErr == nil {
		m.log("osiris-orchestrator: bootstrap-master torn down and genesis token revoked (W3, 30 §1.5)")
	}
	return firstErr
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// ---------------------------------------------------------------------------
// CoreIssuer — the HTTP client for §8.5 steps 4-5.
// ---------------------------------------------------------------------------

// CoreIssuer talks to osiris-core's genesis-token endpoints with the platform
// token. §8.5: "기존 CORE_API_TOKEN으로 충분하다 — 이 요청도 orchestrator가 core에게
// 보내는 플랫폼 내부 호출이라는 점에서 기존 graph-write 호출과 성격이 같고, 새 신뢰
// 경계를 만들지 않는다."
type CoreIssuer struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewCoreIssuer(baseURL, token string) *CoreIssuer {
	return &CoreIssuer{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		// A bounded client, because this call sits between ContainerCreate and
		// ContainerStart: a hang here leaves a created-but-unstarted container,
		// and the caller's teardown can only run once the call returns.
		HTTP: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *CoreIssuer) Issue(ctx context.Context, containerID string) (string, time.Time, error) {
	if c.Token == "" {
		return "", time.Time{}, errors.New("CORE_API_TOKEN is not set; cannot request a genesis token")
	}
	body, _ := json.Marshal(map[string]string{"containerId": containerID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/v1/onboarding/genesis-token", bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusConflict {
		// W1. Surfaced as its own error so the caller can stop instead of
		// retrying a condition that will never change on its own.
		return "", time.Time{}, ErrOrgExists
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("core refused genesis issuance (status %d)", resp.StatusCode)
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", time.Time{}, fmt.Errorf("cannot parse core's issuance response: %w", err)
	}
	if out.Token == "" {
		return "", time.Time{}, errors.New("core returned an empty genesis token")
	}
	return out.Token, out.ExpiresAt, nil
}

func (c *CoreIssuer) Revoke(ctx context.Context) error {
	if c.Token == "" {
		return errors.New("CORE_API_TOKEN is not set; cannot revoke")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/v1/onboarding/genesis-token/revoke", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("core refused revoke (status %d)", resp.StatusCode)
	}
	return nil
}
