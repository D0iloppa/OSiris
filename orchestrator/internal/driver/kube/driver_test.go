package kube

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"osiris-orchestrator/internal/workload"
)

const testNamespace = "osiris-agents"

func newFakeDriver(objs ...runtime.Object) (*Driver, *dynamicfake.FakeDynamicClient) {
	scheme := runtime.NewScheme()
	fc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		agentGVR:  "AgentList",
		secretGVR: "SecretList",
	}, objs...)
	return NewDriver(fc, testNamespace), fc
}

func testSpec(employeeID string) workload.AgentWorkloadSpec {
	s := workload.AgentWorkloadSpec{
		EmployeeID:           employeeID,
		TeamID:               "team-1",
		TeamSlug:             "eng",
		RoleID:               "role-1",
		Image:                "osiris/worker:1",
		ModelAlias:           "osiris/worker",
		Resources:            workload.ResourceProfile{CPUMilli: 1000, MemMiB: 512, StorageGiB: 5},
		Grants:               []string{"fs.read"},
		NetworkName:          "osiris-agents",
		Concurrency:          1,
		DrainSeconds:         30,
		Desired:              workload.DesiredRunning,
		RenderedPrompt:       "CLAUDE.md v1",
		RenderedSettings:     "{}",
		RenderedMCP:          "{}",
		RenderedAllowedTools: "Read",
		Credentials:          map[string]string{"nats_password": "secret1"},
	}
	s.ComputeHashes()
	return s
}

func TestApplyCreatesAgentCRAndCredentialsSecret(t *testing.T) {
	d, fc := newFakeDriver()
	spec := testSpec("emp-1")

	ref, err := d.Apply(context.Background(), spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if ref.ID == "" {
		t.Fatal("Apply must return a non-empty WorkloadRef")
	}

	agent, err := fc.Resource(agentGVR).Namespace(testNamespace).Get(context.Background(), "emp-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected Agent CR to exist: %v", err)
	}
	empID, _, _ := unstructured.NestedString(agent.Object, "spec", "employeeId")
	if empID != "emp-1" {
		t.Fatalf("spec.employeeId = %q, want emp-1", empID)
	}
	image, _, _ := unstructured.NestedString(agent.Object, "spec", "image")
	if image != spec.Image {
		t.Fatalf("spec.image = %q, want %q", image, spec.Image)
	}
	specHash, _, _ := unstructured.NestedString(agent.Object, "spec", "specHash")
	if specHash != spec.SpecHash {
		t.Fatalf("spec.specHash = %q, want %q", specHash, spec.SpecHash)
	}

	sec, err := fc.Resource(secretGVR).Namespace(testNamespace).Get(context.Background(), "emp-1-creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected credentials secret to exist: %v", err)
	}
	data, _, _ := unstructured.NestedMap(sec.Object, "data")
	if len(data) != 1 {
		t.Fatalf("expected 1 credential key, got %d", len(data))
	}
	owners := sec.GetOwnerReferences()
	if len(owners) != 1 || owners[0].Name != "emp-1" || owners[0].Kind != "Agent" {
		t.Fatalf("expected the credentials secret to be owned by the Agent CR, got %+v", owners)
	}

	// AgentWorkloadSpec.Credentials must never leak into the CR's spec (agent.yaml:
	// "Credentials 맵은 의도적으로 이 spec에 없다").
	if _, found, _ := unstructured.NestedFieldNoCopy(agent.Object, "spec", "credentials"); found {
		t.Fatal("Agent CR spec must not carry a credentials field")
	}
}

func TestSyncCredentialsIsIdempotent(t *testing.T) {
	d, fc := newFakeDriver()
	spec := testSpec("emp-2")
	if _, err := d.Apply(context.Background(), spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	before, err := fc.Resource(secretGVR).Namespace(testNamespace).Get(context.Background(), "emp-2-creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	beforeRV := before.GetResourceVersion()

	// Same content again — must not touch the object (content-comparison idempotence).
	if err := d.SyncCredentials(context.Background(), "emp-2", spec.Credentials); err != nil {
		t.Fatalf("SyncCredentials (no-op call): %v", err)
	}
	after, err := fc.Resource(secretGVR).Namespace(testNamespace).Get(context.Background(), "emp-2-creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret after no-op sync: %v", err)
	}
	if after.GetResourceVersion() != beforeRV {
		t.Fatalf("SyncCredentials must not rewrite unchanged content: resourceVersion changed %q -> %q", beforeRV, after.GetResourceVersion())
	}

	// Different content — must update.
	if err := d.SyncCredentials(context.Background(), "emp-2", map[string]string{"nats_password": "rotated"}); err != nil {
		t.Fatalf("SyncCredentials (rotation): %v", err)
	}
	rotated, err := fc.Resource(secretGVR).Namespace(testNamespace).Get(context.Background(), "emp-2-creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret after rotation: %v", err)
	}
	data, _, _ := unstructured.NestedMap(rotated.Object, "data")
	if data["nats_password"] == "" {
		t.Fatal("rotated credential must be present")
	}
}

// TestDeleteOnlyTouchesTheAgentCR is the K10 assertion the task calls out
// explicitly: Delete must remove the Agent CR and MUST NOT reach for the
// credentials Secret (or anything else) directly — cleanup of the rest is
// ownerReference-driven K8s garbage collection, not this driver's job.
func TestDeleteOnlyTouchesTheAgentCR(t *testing.T) {
	d, fc := newFakeDriver()
	spec := testSpec("emp-3")
	if _, err := d.Apply(context.Background(), spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if err := d.Delete(context.Background(), "emp-3"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := fc.Resource(agentGVR).Namespace(testNamespace).Get(context.Background(), "emp-3", metav1.GetOptions{}); err == nil {
		t.Fatal("Agent CR must be gone after Delete")
	}
	// The fake client has no real garbage collector, so the Secret survives
	// this call in the test — the point being asserted is narrower: Delete
	// itself never issues a Secret delete call. If it had, the real cluster's
	// ownerReference-driven GC plus this call would double-delete, which is
	// harmless — what would NOT be harmless is Delete reaching for a Pod it
	// has no business touching, which this call's exclusive target (the
	// Agent GVR) rules out by construction.
	if _, err := fc.Resource(secretGVR).Namespace(testNamespace).Get(context.Background(), "emp-3-creds", metav1.GetOptions{}); err != nil {
		t.Fatalf("Delete must not have removed the credentials secret itself (that is ownerReference GC's job): %v", err)
	}
}

func TestDeleteIsIdempotentOnMissingCR(t *testing.T) {
	d, _ := newFakeDriver()
	if err := d.Delete(context.Background(), "never-existed"); err != nil {
		t.Fatalf("Delete of a nonexistent agent must be a no-op, got %v", err)
	}
}

func TestRecreateUpdatesSpecHashAxis(t *testing.T) {
	d, fc := newFakeDriver()
	spec := testSpec("emp-4")
	if _, err := d.Apply(context.Background(), spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	spec.Image = "osiris/worker:2"
	spec.ComputeHashes()
	if err := d.Recreate(context.Background(), spec); err != nil {
		t.Fatalf("Recreate: %v", err)
	}

	agent, err := fc.Resource(agentGVR).Namespace(testNamespace).Get(context.Background(), "emp-4", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	image, _, _ := unstructured.NestedString(agent.Object, "spec", "image")
	if image != "osiris/worker:2" {
		t.Fatalf("Recreate must update spec.image, got %q", image)
	}
	specHash, _, _ := unstructured.NestedString(agent.Object, "spec", "specHash")
	if specHash != spec.SpecHash {
		t.Fatal("Recreate must update spec.specHash")
	}
}

// TestReloadConfigDoesNotTouchSpecHashAxis is the A12/§6.2 assertion: a
// ConfigHash-only change must not touch image/resources/networkName/
// drainSeconds/concurrency — those are exactly the fields configPatchFields
// omits from its merge patch.
func TestReloadConfigDoesNotTouchSpecHashAxis(t *testing.T) {
	d, fc := newFakeDriver()
	spec := testSpec("emp-5")
	if _, err := d.Apply(context.Background(), spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	originalSpecHash := spec.SpecHash

	spec.RenderedPrompt = "CLAUDE.md v2 — one line changed"
	spec.ComputeHashes()
	if spec.SpecHash != originalSpecHash {
		t.Fatal("test setup invariant broken: editing only RenderedPrompt must not move SpecHash")
	}

	if err := d.ReloadConfig(context.Background(), spec); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}

	agent, err := fc.Resource(agentGVR).Namespace(testNamespace).Get(context.Background(), "emp-5", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	image, _, _ := unstructured.NestedString(agent.Object, "spec", "image")
	if image != "osiris/worker:1" {
		t.Fatalf("ReloadConfig must not touch spec.image, got %q", image)
	}
	specHash, _, _ := unstructured.NestedString(agent.Object, "spec", "specHash")
	if specHash != originalSpecHash {
		t.Fatal("ReloadConfig must not touch spec.specHash — that is Recreate's axis")
	}
	prompt, _, _ := unstructured.NestedString(agent.Object, "spec", "renderedPrompt")
	if prompt != spec.RenderedPrompt {
		t.Fatal("ReloadConfig must update spec.renderedPrompt")
	}
	configHash, _, _ := unstructured.NestedString(agent.Object, "spec", "configHash")
	if configHash != spec.ConfigHash {
		t.Fatal("ReloadConfig must update spec.configHash")
	}
}

func TestObserveMapsPhaseAndTracksUnhealthyStreak(t *testing.T) {
	d, fc := newFakeDriver()
	spec := testSpec("emp-6")
	if _, err := d.Apply(context.Background(), spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	setStatus := func(t *testing.T, phase, readyStatus, specHash string) {
		t.Helper()
		agent, err := fc.Resource(agentGVR).Namespace(testNamespace).Get(context.Background(), "emp-6", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get agent: %v", err)
		}
		_ = unstructured.SetNestedField(agent.Object, phase, "status", "phase")
		_ = unstructured.SetNestedField(agent.Object, specHash, "status", "observedSpecHash")
		_ = unstructured.SetNestedSlice(agent.Object, []interface{}{
			map[string]interface{}{"type": "Ready", "status": readyStatus},
		}, "status", "conditions")
		if _, err := fc.Resource(agentGVR).Namespace(testNamespace).UpdateStatus(context.Background(), agent, metav1.UpdateOptions{}); err != nil {
			t.Fatalf("update status: %v", err)
		}
	}

	setStatus(t, "Ready", "True", spec.SpecHash)
	out, err := d.Observe(context.Background())
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	st, ok := out["emp-6"]
	if !ok {
		t.Fatal("Observe must report emp-6")
	}
	if st.Phase != "running" {
		t.Fatalf("phase Ready must map to \"running\", got %q", st.Phase)
	}
	if st.Health != "healthy" || st.ConsecutiveUnhealthy != 0 {
		t.Fatalf("expected healthy/0, got %q/%d", st.Health, st.ConsecutiveUnhealthy)
	}

	for i := 0; i < 3; i++ {
		setStatus(t, "Working", "False", spec.SpecHash)
		out, err = d.Observe(context.Background())
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	st = out["emp-6"]
	if st.ConsecutiveUnhealthy != 3 {
		t.Fatalf("expected 3 consecutive unhealthy observations, got %d", st.ConsecutiveUnhealthy)
	}
	if st.Phase != "running" {
		t.Fatalf("phase Working must also map to \"running\", got %q", st.Phase)
	}

	setStatus(t, "Failed", "False", spec.SpecHash)
	out, err = d.Observe(context.Background())
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	st = out["emp-6"]
	if st.Phase != "Failed" {
		t.Fatalf("a non-Ready/Working phase must pass through unchanged, got %q", st.Phase)
	}
}

func TestDrainMarksStatusDrainingAndIsNoopWhenMissing(t *testing.T) {
	d, fc := newFakeDriver()
	spec := testSpec("emp-7")
	if _, err := d.Apply(context.Background(), spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := d.Drain(context.Background(), "emp-7", 30); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	agent, err := fc.Resource(agentGVR).Namespace(testNamespace).Get(context.Background(), "emp-7", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	phase, _, _ := unstructured.NestedString(agent.Object, "status", "phase")
	if phase != "Draining" {
		t.Fatalf("Drain must set status.phase=Draining, got %q", phase)
	}

	if err := d.Drain(context.Background(), "never-existed", 30); err != nil {
		t.Fatalf("Drain of a missing CR must be a no-op, got %v", err)
	}
}
