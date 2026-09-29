package genesisboot

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// ===========================================================================
// WHY THESE TESTS CARRY THE WEIGHT HERE.
//
// §8.5's security claim is not about any single call — it is about the ORDER of
// four of them: create, issue, write, start. And that order cannot be measured on
// a real deployment, because W1 refuses issuance unless currentOrgRevision == 0
// and any installation that has ever committed an organization is permanently
// past that point (this repo's dev graph sits at revision 40).
//
// So the order is asserted against a fake that records every call. If someone
// later "simplifies" Bring by starting the container before writing the token,
// TestBringOrderIsCreateIssueWriteStart fails — which is the only mechanical
// defense §8.5's window-of-zero argument has.
// ===========================================================================

type call struct {
	name string
	arg  string
}

type fakeDriver struct {
	mu    sync.Mutex
	calls []call

	createID  string
	createErr error
	writeErr  error
	startErr  error
	tearErr   error

	// tokenOnDisk models the file's existence so a test can assert that start
	// never happens while it is absent.
	tokenOnDisk bool
	startedWith string
	// startSawToken records whether the token file existed at the moment start
	// was called — the single most important fact in this package.
	startSawToken bool
}

func (f *fakeDriver) record(name, arg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{name, arg})
}

func (f *fakeDriver) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.name)
	}
	return out
}

func (f *fakeDriver) CreateGenesisContainer(ctx context.Context, spec CreateSpec) (string, error) {
	f.record("create", spec.Image)
	if f.createErr != nil {
		return "", f.createErr
	}
	return f.createID, nil
}

func (f *fakeDriver) WriteGenesisToken(token string) error {
	f.record("write", token)
	if f.writeErr != nil {
		return f.writeErr
	}
	f.tokenOnDisk = true
	return nil
}

func (f *fakeDriver) StartGenesisContainer(ctx context.Context, id string) error {
	f.record("start", id)
	f.startedWith = id
	f.startSawToken = f.tokenOnDisk
	return f.startErr
}

func (f *fakeDriver) TeardownGenesisContainer(ctx context.Context, waitSeconds int) error {
	f.record("teardown", "")
	f.tokenOnDisk = false
	return f.tearErr
}

type fakeIssuer struct {
	mu     sync.Mutex
	calls  []call
	token  string
	expiry time.Time
	err    error
	revErr error

	// sawContainerID is what W4 is about: core must be told the id of the
	// container that already exists.
	sawContainerID string
}

func (f *fakeIssuer) Issue(ctx context.Context, containerID string) (string, time.Time, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call{"issue", containerID})
	f.sawContainerID = containerID
	f.mu.Unlock()
	if f.err != nil {
		return "", time.Time{}, f.err
	}
	return f.token, f.expiry, nil
}

func (f *fakeIssuer) Revoke(ctx context.Context) error {
	f.mu.Lock()
	f.calls = append(f.calls, call{"revoke", ""})
	f.mu.Unlock()
	return f.revErr
}

func (f *fakeIssuer) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.name)
	}
	return out
}

func newFixture() (*Manager, *fakeDriver, *fakeIssuer) {
	d := &fakeDriver{createID: "container-id-abcdef123456"}
	i := &fakeIssuer{token: "osg_tok", expiry: time.Now().Add(24 * time.Hour)}
	return New(d, i, "osiris/agent:dev", []string{"OSIRIS_GENESIS=true"}, nil), d, i
}

// ★ THE CENTRAL TEST. §8.5 steps 2-7 in order, and start strictly after write.
func TestBringOrderIsCreateIssueWriteStart(t *testing.T) {
	m, d, i := newFixture()
	if err := m.Bring(context.Background()); err != nil {
		t.Fatalf("Bring: %v", err)
	}
	want := []string{"create", "write", "start"}
	got := d.names()
	if len(got) != len(want) {
		t.Fatalf("driver calls = %v, want %v", got, want)
	}
	for k := range want {
		if got[k] != want[k] {
			t.Fatalf("driver calls = %v, want %v", got, want)
		}
	}
	if iss := i.names(); len(iss) != 1 || iss[0] != "issue" {
		t.Fatalf("issuer calls = %v, want exactly one issue", iss)
	}
	// The property the whole ordering exists for.
	if !d.startSawToken {
		t.Fatal("the container was started while the token file did not exist — §8.5's window is not zero")
	}
}

// W4: core is told the id of the container that already exists, and the container
// that gets started is that same one.
func TestBringBindsTheTokenToTheCreatedContainer(t *testing.T) {
	m, d, i := newFixture()
	if err := m.Bring(context.Background()); err != nil {
		t.Fatalf("Bring: %v", err)
	}
	if i.sawContainerID != d.createID {
		t.Fatalf("issuer was told %q but the created container is %q", i.sawContainerID, d.createID)
	}
	if d.startedWith != d.createID {
		t.Fatalf("started %q but created %q", d.startedWith, d.createID)
	}
}

// If issuance fails, the created container must not survive: it holds no
// credential and its name would block the next attempt.
func TestIssueFailureTearsDownAndNeverStarts(t *testing.T) {
	m, d, i := newFixture()
	i.err = errors.New("core said no")
	err := m.Bring(context.Background())
	if err == nil {
		t.Fatal("Bring must fail when issuance fails")
	}
	for _, n := range d.names() {
		if n == "start" {
			t.Fatal("the container must never be started without a token")
		}
	}
	if !contains(d.names(), "teardown") {
		t.Fatalf("the un-tokened container must be removed, calls = %v", d.names())
	}
	if m.Snapshot().Phase != PhaseFailed {
		t.Fatalf("phase = %q, want failed", m.Snapshot().Phase)
	}
}

// W1 is terminal and must be reported as such, not buried in a generic failure —
// the caller has to stop rather than retry a condition that cannot change.
func TestOrgExistsIsSurfacedAsTerminal(t *testing.T) {
	m, _, i := newFixture()
	i.err = ErrOrgExists
	err := m.Bring(context.Background())
	if !errors.Is(err, ErrOrgExists) {
		t.Fatalf("W1 refusal must be identifiable, got %v", err)
	}
}

// A token that exists in core but could not be delivered is a live credential
// nobody holds. It must be revoked, not abandoned.
func TestInjectionFailureRevokesTheToken(t *testing.T) {
	m, d, i := newFixture()
	d.writeErr = errors.New("read-only filesystem")
	if err := m.Bring(context.Background()); err == nil {
		t.Fatal("Bring must fail when the token cannot be written")
	}
	if !contains(i.names(), "revoke") {
		t.Fatalf("an undeliverable token must be revoked, issuer calls = %v", i.names())
	}
	if contains(d.names(), "start") {
		t.Fatal("must not start after a failed injection")
	}
}

func TestStartFailureRevokesAndTearsDown(t *testing.T) {
	m, d, i := newFixture()
	d.startErr = errors.New("engine refused")
	if err := m.Bring(context.Background()); err == nil {
		t.Fatal("Bring must fail when start fails")
	}
	if !contains(i.names(), "revoke") {
		t.Fatalf("a token whose container never ran must be revoked, got %v", i.names())
	}
	if !contains(d.names(), "teardown") {
		t.Fatalf("the dead container must be removed, got %v", d.names())
	}
}

// ★ Teardown revokes BEFORE removing the container. Reversing this leaves, for
// the duration of the revoke call, a live token whose bound container is gone —
// and core's binding check compares against a recorded id, so it cannot notice.
func TestTeardownRevokesBeforeRemovingTheContainer(t *testing.T) {
	m, d, i := newFixture()
	if err := m.Bring(context.Background()); err != nil {
		t.Fatalf("Bring: %v", err)
	}
	// Interleave the two fakes' call logs by wrapping the driver's teardown with
	// an assertion that revoke already happened.
	revokedBeforeTeardown := false
	// Record ordering by checking the issuer log at the moment teardown runs.
	td := &orderingDriver{fakeDriver: d, issuer: i, sawRevokeFirst: &revokedBeforeTeardown}
	m2 := New(td, i, "img", nil, nil)
	if err := m2.Teardown(context.Background(), 1); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if !revokedBeforeTeardown {
		t.Fatal("Teardown must revoke the credential before removing its holder")
	}
	if m2.Snapshot().Phase != PhaseTornDown {
		t.Fatalf("phase = %q, want torn_down", m2.Snapshot().Phase)
	}
}

// A revoke failure must not leave the container running.
func TestTeardownRemovesTheContainerEvenIfRevokeFails(t *testing.T) {
	m, d, i := newFixture()
	i.revErr = errors.New("core unreachable")
	err := m.Teardown(context.Background(), 1)
	if err == nil {
		t.Fatal("a failed revoke must be reported")
	}
	if !contains(d.names(), "teardown") {
		t.Fatal("the container must be removed even when revoke fails")
	}
}

// Single-flight: two concurrent bring-ups would collide on the container name and
// the second issuance would invalidate the first container's token.
func TestBringIsSingleFlight(t *testing.T) {
	d := &fakeDriver{createID: "cid"}
	i := &fakeIssuer{token: "osg_x", expiry: time.Now().Add(time.Hour)}
	release := make(chan struct{})
	slow := &blockingDriver{fakeDriver: d, gate: release, entered: make(chan struct{}, 1)}
	m := New(slow, i, "img", nil, nil)

	var wg sync.WaitGroup
	var secondErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = m.Bring(context.Background())
	}()
	// Wait until the first Bring is inside create.
	<-slow.entered
	secondErr = m.Bring(context.Background())
	close(release)
	wg.Wait()

	if !errors.Is(secondErr, ErrInFlight) {
		t.Fatalf("the second concurrent Bring must be refused, got %v", secondErr)
	}
}

// Phase strings are what /status publishes; the snapshot must never carry the
// token itself.
func TestSnapshotCarriesNoCredential(t *testing.T) {
	m, _, _ := newFixture()
	if err := m.Bring(context.Background()); err != nil {
		t.Fatalf("Bring: %v", err)
	}
	st := m.Snapshot()
	if strings.Contains(st.ContainerID, "osg_") || strings.Contains(st.LastError, "osg_") {
		t.Fatal("the snapshot must not contain the token")
	}
	if st.TokenExpiresAt.IsZero() {
		t.Fatal("the expiry timestamp should be reported so the console can warn about W2")
	}
	if m.Phase() != string(PhaseRunning) {
		t.Fatalf("Phase() = %q, want running", m.Phase())
	}
}

// --- helpers ---------------------------------------------------------------

type orderingDriver struct {
	*fakeDriver
	issuer         *fakeIssuer
	sawRevokeFirst *bool
}

func (o *orderingDriver) TeardownGenesisContainer(ctx context.Context, waitSeconds int) error {
	*o.sawRevokeFirst = contains(o.issuer.names(), "revoke")
	return o.fakeDriver.TeardownGenesisContainer(ctx, waitSeconds)
}

type blockingDriver struct {
	*fakeDriver
	gate    chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (b *blockingDriver) CreateGenesisContainer(ctx context.Context, spec CreateSpec) (string, error) {
	b.once.Do(func() {})
	if b.entered != nil {
		b.entered <- struct{}{}
	}
	<-b.gate
	return b.fakeDriver.CreateGenesisContainer(ctx, spec)
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
