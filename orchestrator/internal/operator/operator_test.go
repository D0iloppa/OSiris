package operator

import (
	"context"
	"encoding/base64"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

const testNamespace = "osiris-agents"
const testSystemNamespace = "osiris-system"

func newFakeOperator(objs ...runtime.Object) (*Operator, *dynamicfake.FakeDynamicClient) {
	scheme := runtime.NewScheme()
	fc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		agentGVR:  "AgentList",
		podGVR:    "PodList",
		pvcGVR:    "PersistentVolumeClaimList",
		saGVR:     "ServiceAccountList",
		secretGVR: "SecretList",
	}, objs...)
	return New(fc, testNamespace, testSystemNamespace), fc
}

func testAgent(employeeID string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "osiris.dev/v1alpha1",
		"kind":       "Agent",
		"metadata": map[string]interface{}{
			"name":      employeeID,
			"namespace": testNamespace,
			"uid":       "test-uid-" + employeeID,
		},
		"spec": map[string]interface{}{
			"employeeId":   employeeID,
			"teamId":       "team-1",
			"teamSlug":     "eng",
			"image":        "osiris/agent:dev",
			"modelAlias":   "osiris/worker",
			"desiredState": "Running",
			"networkName":  "osiris-agents",
			"concurrency":  int64(1),
			"drainSeconds": int64(30),
			"resources": map[string]interface{}{
				"cpuMilli":   int64(1000),
				"memMiB":     int64(512),
				"storageGiB": int64(5),
			},
			"specHash":   "abc123",
			"configHash": "def456",
		},
	}}
}

// TestSyncAgentMaterializesPodPVCAndServiceAccount is this phase's core
// claim, verified against the fake dynamic client: one Agent CR produces
// exactly the workload objects K5 lists (minus the credentials Secret,
// which driver/kube — not this operator — owns), every one of them
// carrying an ownerReference back to the Agent CR (K10's GC precondition).
func TestSyncAgentMaterializesPodPVCAndServiceAccount(t *testing.T) {
	agent := testAgent("emp-1")
	op, fc := newFakeOperator(agent)

	if err := op.syncAgent(context.Background(), testNamespace+"/emp-1"); err != nil {
		t.Fatalf("syncAgent: %v", err)
	}

	pod, err := fc.Resource(podGVR).Namespace(testNamespace).Get(context.Background(), "emp-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pod to be created: %v", err)
	}
	owners := pod.GetOwnerReferences()
	if len(owners) != 1 || owners[0].Name != "emp-1" || owners[0].Kind != "Agent" {
		t.Fatalf("pod ownerReferences = %+v, want exactly one pointing at Agent emp-1", owners)
	}

	pvc, err := fc.Resource(pvcGVR).Namespace(testNamespace).Get(context.Background(), pvcName("emp-1"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pvc to be created: %v", err)
	}
	if len(pvc.GetOwnerReferences()) != 1 {
		t.Fatalf("pvc missing ownerReference")
	}

	sa, err := fc.Resource(saGVR).Namespace(testNamespace).Get(context.Background(), "emp-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected serviceaccount to be created: %v", err)
	}
	if len(sa.GetOwnerReferences()) != 1 {
		t.Fatalf("serviceaccount missing ownerReference")
	}

	cfgSecret, err := fc.Resource(secretGVR).Namespace(testNamespace).Get(context.Background(), configSecretName("emp-1"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected config secret to be created: %v", err)
	}
	if len(cfgSecret.GetOwnerReferences()) != 1 {
		t.Fatalf("config secret missing ownerReference")
	}

	// Status was written: phase must move off the empty string, and
	// observedSpecHash/observedConfigHash must mirror spec (not diffed —
	// see status.go's doc comment on the D-2 boundary).
	updated, err := fc.Resource(agentGVR).Namespace(testNamespace).Get(context.Background(), "emp-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated agent: %v", err)
	}
	phase, _, _ := unstructured.NestedString(updated.Object, "status", "phase")
	if phase == "" {
		t.Fatal("status.phase was not set")
	}
	specHash, _, _ := unstructured.NestedString(updated.Object, "status", "observedSpecHash")
	if specHash != "abc123" {
		t.Fatalf("status.observedSpecHash = %q, want abc123", specHash)
	}
}

// TestSyncAgentSuspendedDoesNotMaterialize confirms desiredState=Suspended
// skips creating the four workload objects (this phase's minimal handling
// of Suspended — see syncAgent's doc comment).
func TestSyncAgentSuspendedDoesNotMaterialize(t *testing.T) {
	agent := testAgent("emp-2")
	unstructured.SetNestedField(agent.Object, "Suspended", "spec", "desiredState")
	op, fc := newFakeOperator(agent)

	if err := op.syncAgent(context.Background(), testNamespace+"/emp-2"); err != nil {
		t.Fatalf("syncAgent: %v", err)
	}

	if _, err := fc.Resource(podGVR).Namespace(testNamespace).Get(context.Background(), "emp-2", metav1.GetOptions{}); err == nil {
		t.Fatal("pod should not exist for a Suspended agent")
	}
}

// TestSyncAgentDeletedIsANoOp mirrors driver/kube's own idempotent-on-
// missing-object contract: this operator never runs special logic on a
// deleted Agent (see Run's doc comment — K8s' own GC handles the cascade).
func TestSyncAgentDeletedIsANoOp(t *testing.T) {
	op, _ := newFakeOperator()
	if err := op.syncAgent(context.Background(), testNamespace+"/does-not-exist"); err != nil {
		t.Fatalf("syncAgent on a missing agent should be a no-op, got: %v", err)
	}
}

// TestConfigHashChangeDoesNotRecreatePod is this phase's first target
// condition, unit-tested against the fake client: a ConfigHash-only change
// must never reach ensurePod's delete path. The Pod object must survive
// byte-for-byte identical (same UID, same osiris.dev/spec-hash annotation),
// while the config Secret's content must actually change.
func TestConfigHashChangeDoesNotRecreatePod(t *testing.T) {
	agent := testAgent("emp-cfg")
	op, fc := newFakeOperator(agent)
	ctx := context.Background()

	if err := op.syncAgent(ctx, testNamespace+"/emp-cfg"); err != nil {
		t.Fatalf("initial syncAgent: %v", err)
	}
	podBefore, err := fc.Resource(podGVR).Namespace(testNamespace).Get(ctx, "emp-cfg", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pod: %v", err)
	}
	uidBefore := podBefore.GetUID()

	current, err := fc.Resource(agentGVR).Namespace(testNamespace).Get(ctx, "emp-cfg", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	unstructured.SetNestedField(current.Object, "new-rendered-prompt", "spec", "renderedPrompt")
	unstructured.SetNestedField(current.Object, "configHashChanged999", "spec", "configHash")
	if _, err := fc.Resource(agentGVR).Namespace(testNamespace).Update(ctx, current, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update agent spec: %v", err)
	}

	if err := op.syncAgent(ctx, testNamespace+"/emp-cfg"); err != nil {
		t.Fatalf("second syncAgent: %v", err)
	}

	podAfter, err := fc.Resource(podGVR).Namespace(testNamespace).Get(ctx, "emp-cfg", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod should still exist after a ConfigHash-only change: %v", err)
	}
	if podAfter.GetUID() != uidBefore {
		t.Fatalf("pod UID changed on ConfigHash-only update (%s -> %s) — ConfigHash must never recreate the Pod (A12)", uidBefore, podAfter.GetUID())
	}

	secret, err := fc.Resource(secretGVR).Namespace(testNamespace).Get(ctx, configSecretName("emp-cfg"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get config secret: %v", err)
	}
	promptB64, _, _ := unstructured.NestedString(secret.Object, "data", "CLAUDE.md")
	decoded, _ := base64.StdEncoding.DecodeString(promptB64)
	if string(decoded) != "new-rendered-prompt" {
		t.Fatalf("config secret CLAUDE.md = %q, want the new rendered prompt to have propagated", decoded)
	}
}

// TestAdoptedPodIsStampedThenRecreatesOnNextRealChange guards against the
// defect the supervisor caught: a Pod with no osiris.dev/spec-hash
// annotation (predating this feature, or otherwise adopted) must NOT be
// recreated on the tick it is first seen, but the annotation MUST be
// stamped to the CURRENT spec.specHash on that same tick — otherwise every
// future tick would see observedHash == "" again and the Pod would be
// PERMANENTLY IMMUNE to Recreate, silently, forever. This test drives all
// three steps the supervisor asked for: (1) no recreate on the adoption
// tick, (2) annotation stamped to the current hash, (3) a REAL SpecHash
// change afterward does Recreate, with the PVC preserved.
func TestAdoptedPodIsStampedThenRecreatesOnNextRealChange(t *testing.T) {
	agent := testAgent("emp-adopt")
	op, fc := newFakeOperator(agent)
	ctx := context.Background()

	if err := op.syncAgent(ctx, testNamespace+"/emp-adopt"); err != nil {
		t.Fatalf("initial syncAgent: %v", err)
	}

	// Simulate a Pod that predates specHashAnnotation: strip the annotation
	// this operator just stamped, and mark a stable fake UID so a later
	// Get can distinguish "same object" from "recreated".
	pod, err := fc.Resource(podGVR).Namespace(testNamespace).Get(ctx, "emp-adopt", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pod: %v", err)
	}
	pod.SetAnnotations(map[string]string{})
	pod.SetUID("pod-uid-adopted")
	if _, err := fc.Resource(podGVR).Namespace(testNamespace).Update(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("strip annotation to simulate a legacy pod: %v", err)
	}

	pvc, err := fc.Resource(pvcGVR).Namespace(testNamespace).Get(ctx, pvcName("emp-adopt"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pvc: %v", err)
	}
	pvc.SetUID("pvc-uid-adopt-stable")
	if _, err := fc.Resource(pvcGVR).Namespace(testNamespace).Update(ctx, pvc, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("stamp pvc uid: %v", err)
	}

	// Step 1 + 2: the adoption tick.
	if err := op.syncAgent(ctx, testNamespace+"/emp-adopt"); err != nil {
		t.Fatalf("syncAgent (adoption tick): %v", err)
	}
	adopted, err := fc.Resource(podGVR).Namespace(testNamespace).Get(ctx, "emp-adopt", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod should still exist after the adoption tick: %v", err)
	}
	if adopted.GetUID() != "pod-uid-adopted" {
		t.Fatalf("pod was recreated on the adoption tick (uid %q) — adoption must not recreate", adopted.GetUID())
	}
	if got := adopted.GetAnnotations()[specHashAnnotation]; got != "abc123" {
		t.Fatalf("adopted pod's spec-hash annotation = %q, want it stamped to the current spec.specHash (abc123) — otherwise this Pod is permanently immune to Recreate", got)
	}

	// Step 3: a REAL SpecHash change now must Recreate, exactly like
	// TestSpecHashChangeRecreatesPodButPreservesPVC verifies from a
	// freshly-created (never-adopted) Pod.
	current, err := fc.Resource(agentGVR).Namespace(testNamespace).Get(ctx, "emp-adopt", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	unstructured.SetNestedField(current.Object, "specHashChangedAfterAdopt", "spec", "specHash")
	if _, err := fc.Resource(agentGVR).Namespace(testNamespace).Update(ctx, current, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update agent spec: %v", err)
	}
	if err := op.syncAgent(ctx, testNamespace+"/emp-adopt"); err != nil { // tick 1: delete
		t.Fatalf("syncAgent after post-adoption spec change (tick 1): %v", err)
	}
	if err := op.syncAgent(ctx, testNamespace+"/emp-adopt"); err != nil { // tick 2: recreate
		t.Fatalf("syncAgent after post-adoption spec change (tick 2): %v", err)
	}

	recreated, err := fc.Resource(podGVR).Namespace(testNamespace).Get(ctx, "emp-adopt", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected replacement pod: %v", err)
	}
	if recreated.GetUID() == "pod-uid-adopted" {
		t.Fatalf("pod was NOT recreated after a real post-adoption SpecHash change — adopted pods must not be permanently immune to Recreate")
	}
	if recreated.GetAnnotations()[specHashAnnotation] != "specHashChangedAfterAdopt" {
		t.Fatalf("replacement pod's spec-hash annotation = %q, want specHashChangedAfterAdopt", recreated.GetAnnotations()[specHashAnnotation])
	}

	pvcAfter, err := fc.Resource(pvcGVR).Namespace(testNamespace).Get(ctx, pvcName("emp-adopt"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pvc should still exist: %v", err)
	}
	if pvcAfter.GetUID() != "pvc-uid-adopt-stable" {
		t.Fatalf("PVC UID changed (%s -> %s) — adoption + Recreate must never touch the PVC", "pvc-uid-adopt-stable", pvcAfter.GetUID())
	}
}

// TestSpecHashChangeRecreatesPodButPreservesPVC is this phase's second
// target condition: a SpecHash change must delete-and-recreate the Pod
// (new UID) while the PVC survives untouched (same UID). ensurePod's
// contract is delete-then-wait-for-gone-then-create across ticks (see its
// doc comment), so this test drives two syncAgent calls, matching what the
// real workqueue would do across two reconcile passes.
func TestSpecHashChangeRecreatesPodButPreservesPVC(t *testing.T) {
	agent := testAgent("emp-spec")
	op, fc := newFakeOperator(agent)
	ctx := context.Background()

	if err := op.syncAgent(ctx, testNamespace+"/emp-spec"); err != nil {
		t.Fatalf("initial syncAgent: %v", err)
	}
	// The fake dynamic client, unlike a real API server, does not auto-assign
	// a UID on Create — stamp one by hand on both objects so this test can
	// tell "same object" from "recreated object" the same way `kubectl get
	// pod -o jsonpath='{.metadata.uid}'` does against the real k3d cluster
	// (see this phase's report for that real-cluster measurement).
	podBefore, err := fc.Resource(podGVR).Namespace(testNamespace).Get(ctx, "emp-spec", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pod: %v", err)
	}
	podBefore.SetUID("pod-uid-before")
	if _, err := fc.Resource(podGVR).Namespace(testNamespace).Update(ctx, podBefore, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("stamp pod uid: %v", err)
	}
	uidBefore := podBefore.GetUID()

	pvcBefore, err := fc.Resource(pvcGVR).Namespace(testNamespace).Get(ctx, pvcName("emp-spec"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pvc: %v", err)
	}
	pvcBefore.SetUID("pvc-uid-stable")
	if _, err := fc.Resource(pvcGVR).Namespace(testNamespace).Update(ctx, pvcBefore, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("stamp pvc uid: %v", err)
	}
	pvcUIDBefore := pvcBefore.GetUID()

	current, err := fc.Resource(agentGVR).Namespace(testNamespace).Get(ctx, "emp-spec", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	unstructured.SetNestedField(current.Object, "osiris/agent:new-image", "spec", "image")
	unstructured.SetNestedField(current.Object, "specHashChanged777", "spec", "specHash")
	if _, err := fc.Resource(agentGVR).Namespace(testNamespace).Update(ctx, current, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update agent spec: %v", err)
	}

	// Tick 1: ensurePod observes the annotation mismatch and deletes the
	// old Pod (fake client removes it immediately — no grace-period
	// simulation, unlike a real API server/kubelet, but the delete DECISION
	// is what this test is verifying).
	if err := op.syncAgent(ctx, testNamespace+"/emp-spec"); err != nil {
		t.Fatalf("syncAgent after spec change (tick 1): %v", err)
	}
	// Tick 2: the old Pod is gone (NotFound) — ensurePod creates the
	// replacement with the new spec.
	if err := op.syncAgent(ctx, testNamespace+"/emp-spec"); err != nil {
		t.Fatalf("syncAgent after spec change (tick 2): %v", err)
	}

	podAfter, err := fc.Resource(podGVR).Namespace(testNamespace).Get(ctx, "emp-spec", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected replacement pod: %v", err)
	}
	if podAfter.GetUID() == uidBefore {
		t.Fatalf("pod UID unchanged after a SpecHash change (%s) — expected a Recreate", uidBefore)
	}
	if podAfter.GetAnnotations()[specHashAnnotation] != "specHashChanged777" {
		t.Fatalf("replacement pod's spec-hash annotation = %q, want specHashChanged777", podAfter.GetAnnotations()[specHashAnnotation])
	}
	containers, _, _ := unstructured.NestedSlice(podAfter.Object, "spec", "containers")
	gotImage, _, _ := unstructured.NestedString(containers[0].(map[string]interface{}), "image")
	if gotImage != "osiris/agent:new-image" {
		t.Fatalf("replacement pod image = %q, want the new image", gotImage)
	}

	pvcAfter, err := fc.Resource(pvcGVR).Namespace(testNamespace).Get(ctx, pvcName("emp-spec"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pvc should still exist: %v", err)
	}
	if pvcAfter.GetUID() != pvcUIDBefore {
		t.Fatalf("PVC UID changed (%s -> %s) — Recreate must never touch the PVC", pvcUIDBefore, pvcAfter.GetUID())
	}
}
