// Package kube is the [2단계] workload.WorkloadDriver implementation
// (docs/design/70-kubernetes-deployment.md §5, K9/K10;
// docs/design/10-core-architecture.md §6.2 D3). It replaces
// internal/driver/docker WITHOUT internal/reconcile changing a single line —
// that substitutability is the entire point of workload.WorkloadDriver (G3).
//
// ── WHAT THIS DRIVER DOES, AND DOES NOT, TOUCH ────────────────────────────
//
// This driver creates and deletes exactly one kind of object it did not also
// have to (the credentials Secret, see ensureCredentialsSecret) plus the
// object K9/K10 are actually about: the Agent CustomResource
// (deploy/osiris/crds/agent.yaml). It never creates, patches, or deletes a
// Pod, PVC, ServiceAccount, or NetworkPolicy directly — those four are the
// OPERATOR's job (a controller-runtime reconciliation loop watching Agent
// CRs, §5.1's K9), which is NOT part of this phase and is not implemented
// here. What IS implemented here is the driver/docker-shaped contract this
// repo already has tests and a reconcile loop against: Apply/Recreate/
// ReloadConfig/Drain/Delete/Observe, realized in terms of the CR instead of a
// container.
//
// ── Recreate vs ReloadConfig, carried over from driver/docker ────────────
//
// workload.AgentWorkloadSpec.ComputeHashes deliberately splits SpecHash
// (image/resources/network/drainSeconds/concurrency) from ConfigHash
// (modelAlias + the four rendered* fields), and says why in its own comment:
// "합치면 프롬프트 한 줄 수정이 전 에이전트 재생성이 된다." agent.yaml's schema
// comments on spec.specHash/spec.configHash restate the same split in CRD
// terms. This driver preserves it structurally, not just by convention:
//
//   - Recreate does a full CR Update (every field in buildAgentObject,
//     including specHash) — the CR-level equivalent of driver/docker's
//     drain→delete→apply, because a SpecHash change is exactly the axis the
//     (future) operator must react to by tearing down and re-rendering the
//     Pod.
//   - ReloadConfig does a JSON MERGE PATCH restricted to configPatchFields
//     (modelAlias + rendered* + configHash) — it cannot touch image,
//     resources, networkName, drainSeconds, or concurrency even by accident,
//     because those keys are simply not present in the patch body.
//
// ── Delete deletes ONE object: the CR ─────────────────────────────────────
//
// K10: "kubectl delete agent <id> 하나로 Pod·PVC·SA·Secret·NetworkPolicy가
// 함께 사라진다" — because the operator sets ownerReference on all five of
// them, pointing at the Agent CR, and K8s' garbage collector cascades from
// there. This driver's Delete does not — and must not — reach for the Pod
// itself; it deletes the Agent object and nothing else. (The credentials
// Secret is the one object THIS driver creates directly rather than the
// operator, but it too carries an ownerReference to the CR — see
// ensureCredentialsSecret — so it is swept up by the same cascade, not by a
// second explicit delete call here.)
package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"osiris-orchestrator/internal/workload"
)

// Driver is the dynamic-client realization of workload.WorkloadDriver.
// Dynamic (rather than a generated typed client) so this package has exactly
// one k8s dependency shape for both the Agent CR and the credentials Secret
// — see types.go's GVR comment for why a typed clientset was not added on
// top just for Secrets.
type Driver struct {
	dyn       dynamic.Interface
	namespace string // "osiris-agents" (K10 — CR lives WITH the Pod it owns, not in osiris-system)

	// unhealthyStreak mirrors driver/docker's identical field: the CRD's
	// status has no ConsecutiveUnhealthy field (agent.yaml's status only
	// carries phase/conditions/hashes/heartbeat — see Observe's doc comment),
	// so the "3연속 unhealthy" judgment (§5.4) is reconstructed here, driver-
	// side, exactly the way driver/docker reconstructs it from Health.Status
	// across ticks. In-memory and orchestrator-local, same accepted cost as
	// the docker driver's: a restart loses one streak, not any intent.
	unhealthyStreak map[string]int
}

// NewDriver takes a dynamic.Interface rather than constructing its own
// rest.Config — main.go (the one approved wiring point,
// bin/lint-driver-boundary.sh) owns in-cluster vs kubeconfig discovery, the
// same separation driver/docker keeps between Client (transport) and Driver
// (semantics).
func NewDriver(dyn dynamic.Interface, namespace string) *Driver {
	return &Driver{dyn: dyn, namespace: namespace, unhealthyStreak: map[string]int{}}
}

func (d *Driver) agents() dynamic.ResourceInterface {
	return d.dyn.Resource(agentGVR).Namespace(d.namespace)
}

func (d *Driver) secrets() dynamic.ResourceInterface {
	return d.dyn.Resource(secretGVR).Namespace(d.namespace)
}

// Apply implements workload.WorkloadDriver: create the Agent CR (K9 — this
// IS the "물성화" step; the operator watching it does the rest) and deliver
// the initial credentials. Idempotent per the same contract driver/docker
// documents on its own Apply: reconcile only calls this for employeeIDs
// Observe did not report, so no existence check is needed here.
func (d *Driver) Apply(ctx context.Context, spec workload.AgentWorkloadSpec) (workload.WorkloadRef, error) {
	obj := buildAgentObject(d.namespace, spec)
	created, err := d.agents().Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return workload.WorkloadRef{}, fmt.Errorf("create agent CR for %s: %w", spec.EmployeeID, err)
	}
	if err := d.ensureCredentialsSecret(ctx, spec.EmployeeID, spec.Credentials); err != nil {
		return workload.WorkloadRef{}, err
	}
	// WorkloadRef in [1단계] is a container ID; workload.go's own comment
	// anticipates "[2단계]: would be a Pod name" — but this driver never
	// learns a Pod name (the operator renders the Pod, not this driver), so
	// the CR's own UID is the opaque handle instead (falling back to the
	// CR's name if the API server has not assigned a UID by the time Create
	// returns — a real API server always has by then, but some fake test
	// clients do not, and an empty ref should never be reported as if
	// nothing were created). Nothing above this package interprets
	// WorkloadRef's contents (workload.go: "Callers must not interpret its
	// contents"), so this substitution is exactly as valid as a Pod name
	// would have been.
	ref := string(created.GetUID())
	if ref == "" {
		ref = created.GetName()
	}
	return workload.WorkloadRef{ID: ref}, nil
}

// Recreate implements workload.WorkloadDriver via a full CR Update — see the
// package doc comment for why this, and not a patch, is the SpecHash-axis
// path. Falls back to Create if the CR happens to be missing (e.g. a
// previous Delete raced this call), mirroring driver/docker's Recreate being
// safe to call on a partially-torn-down agent.
func (d *Driver) Recreate(ctx context.Context, spec workload.AgentWorkloadSpec) error {
	name := agentName(spec.EmployeeID)
	obj := buildAgentObject(d.namespace, spec)

	existing, err := d.agents().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("get agent CR %s (recreate): %w", name, err)
		}
		if _, err := d.agents().Create(ctx, obj, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("recreate %s: create: %w", spec.EmployeeID, err)
		}
	} else {
		// A K8s Update requires the current resourceVersion (optimistic
		// concurrency) — carry it over rather than making the caller re-Get.
		obj.SetResourceVersion(existing.GetResourceVersion())
		if _, err := d.agents().Update(ctx, obj, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("recreate %s: update: %w", spec.EmployeeID, err)
		}
	}
	return d.ensureCredentialsSecret(ctx, spec.EmployeeID, spec.Credentials)
}

// ReloadConfig implements workload.WorkloadDriver via a JSON MERGE PATCH
// restricted to the ConfigHash axis (configPatchFields) — see the package
// doc comment. Never touches the credentials Secret: credential rotation has
// its own path, SyncCredentials, and is excluded from ConfigHash for the same
// reason driver/docker excludes it (§8.4 — rotation must cause neither a
// Recreate nor a logged reload).
func (d *Driver) ReloadConfig(ctx context.Context, spec workload.AgentWorkloadSpec) error {
	patch, err := json.Marshal(configPatchFields(spec))
	if err != nil {
		return fmt.Errorf("marshal config patch for %s: %w", spec.EmployeeID, err)
	}
	if _, err := d.agents().Patch(ctx, agentName(spec.EmployeeID), types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("reload config %s: %w", spec.EmployeeID, err)
	}
	return nil
}

// Drain implements workload.WorkloadDriver by marking the CR's OBSERVATION
// layer (agent.yaml's status subresource — "드라이버/supervisor만 쓴다") as
// Draining, best-effort, and returning immediately.
//
// THIS IS DELIBERATELY NOT A SYNCHRONOUS SIGTERM-AND-WAIT the way
// driver/docker's Drain is. driver/docker calls Docker's own ContainerStop,
// which has a real process to signal; this driver has no Pod handle to
// signal (the package doc's central rule: this driver never touches a Pod).
// The graceful-shutdown WAIT itself is spec.DrainSeconds, which is already a
// CR field (agent.yaml's spec.drainSeconds) the NOT-YET-BUILT operator reads
// when it observes the CR is draining/being deleted — so the bounded wait
// this method's signature still carries (waitSeconds) is not re-plumbed
// anywhere here; it is redundant with spec.drainSeconds by construction, and
// the parameter exists only because workload.WorkloadDriver's signature is
// shared with driver/docker, which does need it directly.
//
// A missing CR (already deleted, or never created) is not an error — draining
// something absent is a no-op, matching driver/docker's Drain.
func (d *Driver) Drain(ctx context.Context, employeeID string, waitSeconds int) error {
	_ = waitSeconds // see doc comment: operator consumes spec.drainSeconds, not this parameter
	name := agentName(employeeID)
	existing, err := d.agents().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get agent CR %s (drain): %w", name, err)
	}
	if err := unstructured.SetNestedField(existing.Object, "Draining", "status", "phase"); err != nil {
		return fmt.Errorf("set draining phase for %s: %w", employeeID, err)
	}
	if _, err := d.agents().UpdateStatus(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("mark agent CR %s draining: %w", name, err)
	}
	return nil
}

// Delete implements workload.WorkloadDriver. See the package doc comment's
// "Delete deletes ONE object" section — this call touches nothing but the
// Agent CR. A missing CR is not an error (idempotent, same as driver/docker).
func (d *Driver) Delete(ctx context.Context, employeeID string) error {
	name := agentName(employeeID)
	if err := d.agents().Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete agent CR %s: %w", name, err)
	}
	return nil
}

// Observe implements workload.WorkloadDriver — the OBSERVATION layer (§6.1),
// realized by reading status off every Agent CR in the namespace rather than
// inspecting containers.
//
// PHASE STRING MAPPING, AND WHY IT EXISTS: internal/reconcile/reconcile.go
// (which this package must not, and does not, modify — G3) contains the
// literal string comparison `st.Phase != "running"` to detect a
// desired-running agent that isn't actually up (its liveness-recovery
// branch, §1.3/G9). That literal is driver/docker-shaped: Docker's
// State.Status is "running"/"created"/"exited"/... . agent.yaml's
// status.phase vocabulary is the CR's own lifecycle
// (Pending/Provisioning/Booting/Ready/Working/Draining/Stopped/Suspended/
// Failed, §5.3). Rather than ask reconcile.go to know two vocabularies (which
// would violate G3 by making the convergence loop driver-aware), this
// Observe call translates: Ready and Working both mean "the Pod the operator
// rendered is actually serving traffic", which is exactly what reconcile.go's
// "running" check is testing for — so those two map to the literal string
// "running", and every other CR phase passes through unchanged (which still
// correctly reads as "!= running" and triggers the same Recreate-for-
// liveness branch, e.g. for Pending/Booting/Failed).
//
// ConsecutiveUnhealthy is reconstructed from the CR's Ready condition across
// calls (see the Driver.unhealthyStreak field comment) — there is no
// unhealthy-streak field in agent.yaml's status, by design (that schema is
// the CR's stable public shape; a tick-to-tick counter belongs to whichever
// process is counting ticks, which is this driver, exactly as
// driver/docker's is this driver's Docker counterpart).
func (d *Driver) Observe(ctx context.Context) (map[string]workload.WorkloadStatus, error) {
	list, err := d.agents().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list agent CRs: %w", err)
	}
	out := map[string]workload.WorkloadStatus{}
	now := time.Now()
	seen := map[string]bool{}
	for i := range list.Items {
		item := &list.Items[i]
		empID, _, _ := unstructured.NestedString(item.Object, "spec", "employeeId")
		if empID == "" {
			continue // not a well-formed Agent CR (spec.employeeId is required by the schema) — skip rather than guess
		}
		seen[empID] = true

		phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
		specHash, _, _ := unstructured.NestedString(item.Object, "status", "observedSpecHash")
		configHash, _, _ := unstructured.NestedString(item.Object, "status", "observedConfigHash")
		health, readyStatus := readyCondition(item.Object)

		if readyStatus == "False" {
			d.unhealthyStreak[empID]++
		} else {
			d.unhealthyStreak[empID] = 0
		}

		out[empID] = workload.WorkloadStatus{
			EmployeeID:           empID,
			Ref:                  workload.WorkloadRef{ID: string(item.GetUID())},
			Phase:                mapPhase(phase),
			Health:               health,
			SpecHash:             specHash,
			ConfigHash:           configHash,
			ConsecutiveUnhealthy: d.unhealthyStreak[empID],
			ObservedAt:           now,
		}
	}
	for id := range d.unhealthyStreak {
		if !seen[id] {
			delete(d.unhealthyStreak, id)
		}
	}
	return out, nil
}

// mapPhase implements the translation the Observe doc comment describes.
func mapPhase(crPhase string) string {
	switch crPhase {
	case "Ready", "Working":
		return "running"
	default:
		return crPhase
	}
}

// readyCondition reads the standard K8s condition array
// (agent.yaml's status.conditions) for type=="Ready" and returns a
// driver/docker-shaped health string plus the raw condition status
// ("True"/"False"/"Unknown"), so callers can act on either.
func readyCondition(obj map[string]interface{}) (health string, rawStatus string) {
	conditions, found, _ := unstructured.NestedSlice(obj, "status", "conditions")
	if !found {
		return "none", ""
	}
	for _, c := range conditions {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _, _ := unstructured.NestedString(cm, "type"); t != "Ready" {
			continue
		}
		status, _, _ := unstructured.NestedString(cm, "status")
		switch status {
		case "True":
			return "healthy", status
		case "False":
			return "unhealthy", status
		default:
			return "unknown", status
		}
	}
	return "none", ""
}

// SyncCredentials implements workload.WorkloadDriver's rotation-delivery
// method (see workload.WorkloadDriver's doc comment for why this method
// exists at all). The k8s realization anticipated by that doc comment:
// "[2단계]'s KubeDriver implements it by updating the Secret's data (kubelet
// then refreshes the projected volume, and the supervisor's read-at-exec
// picks it up — identical semantics, zero changes above this interface)."
// This is exactly that.
func (d *Driver) SyncCredentials(ctx context.Context, employeeID string, creds map[string]string) error {
	return d.ensureCredentialsSecret(ctx, employeeID, creds)
}

// ensureCredentialsSecret upserts the one Secret this driver (not the
// operator) owns per agent — see the package doc comment. Idempotent by
// CONTENT COMPARISON (secretDataEqual), the same discipline
// driver/docker.SyncCredentials documents and for the same reason: this runs
// every reconcile tick for every running-wanted agent, and touching an
// unchanged Secret would churn its resourceVersion/managedFields for nothing
// and could mask a real change with generation noise.
//
// CALLED BEFORE THE CR NECESSARILY EXISTS: reconcile.go's step 1b calls
// SyncCredentials for every running-wanted agent BEFORE the admission loop
// that calls Apply (see reconcile.go's ordering comment on why — a real
// supervisor needs its NATS user validated before it connects). So this Secret
// may be created before its owning Agent CR exists. When that happens the
// ownerReference is simply left unset for this call; the next call that DOES
// find the CR (typically Apply's own follow-up call, moments later in the
// same tick) stamps it — K10's garbage-collection guarantee only has to hold
// once the CR exists, and it always will by the time an agent is actually
// resident.
func (d *Driver) ensureCredentialsSecret(ctx context.Context, employeeID string, creds map[string]string) error {
	if len(creds) == 0 {
		return nil
	}
	name := credsSecretName(employeeID)
	desired := encodeSecretData(creds)

	owner := d.ownerReferenceIfAgentExists(ctx, employeeID)

	existing, err := d.secrets().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("get credentials secret %s: %w", name, err)
		}
		obj := newSecretObject(d.namespace, name, desired)
		if owner != nil {
			obj.SetOwnerReferences([]metav1.OwnerReference{*owner})
		}
		if _, err := d.secrets().Create(ctx, obj, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create credentials secret %s: %w", name, err)
		}
		return nil
	}

	existingData, _, _ := unstructured.NestedMap(existing.Object, "data")
	ownerAlreadySet := len(existing.GetOwnerReferences()) > 0
	if secretDataEqual(existingData, desired) && (ownerAlreadySet || owner == nil) {
		return nil // unchanged — SyncCredentials' idempotent-and-silent contract
	}
	if err := unstructured.SetNestedMap(existing.Object, desired, "data"); err != nil {
		return fmt.Errorf("set credentials secret %s data: %w", name, err)
	}
	if owner != nil && !ownerAlreadySet {
		existing.SetOwnerReferences([]metav1.OwnerReference{*owner})
	}
	if _, err := d.secrets().Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update credentials secret %s: %w", name, err)
	}
	return nil
}

func (d *Driver) ownerReferenceIfAgentExists(ctx context.Context, employeeID string) *metav1.OwnerReference {
	agent, err := d.agents().Get(ctx, agentName(employeeID), metav1.GetOptions{})
	if err != nil {
		return nil
	}
	blockDeletion := true
	controller := true
	return &metav1.OwnerReference{
		APIVersion:         agentAPIVersion,
		Kind:               agentKind,
		Name:               agent.GetName(),
		UID:                agent.GetUID(),
		BlockOwnerDeletion: &blockDeletion,
		Controller:         &controller,
	}
}
