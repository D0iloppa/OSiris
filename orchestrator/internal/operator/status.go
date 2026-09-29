package operator

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// updateStatus reads the Pod this operator (should have) materialized and
// writes agent.yaml's status subresource: phase/podName/conditions, plus
// observedSpecHash/observedConfigHash.
//
// observedSpecHash/observedConfigHash ARE NOT A DIFF ENGINE. Per the work
// order's explicit D-2 boundary ("specHash 변경 시 Recreate ... 이번에는
// 해시 기반 분기를 구현하지 마세요 ... 생성 성공 시 현재 값을 그대로
// 기록하는 정도로 두세요"), this function simply mirrors spec.specHash/
// spec.configHash into status whenever the Pod exists — it never compares
// the observed value against a previous one to decide whether to tear
// anything down. That comparison, and the Recreate/ReloadConfig it would
// drive, is Phase D-2's job.
//
// Returns requeue=true if the phase has not yet converged to a terminal-ish
// state (Ready/Failed/Stopped/Suspended), so syncAgent can schedule a quick
// re-check instead of waiting for the informer's full resync period.
func (o *Operator) updateStatus(ctx context.Context, agent *unstructured.Unstructured, spec agentSpec) (requeue bool, err error) {
	name := agentName(spec.employeeID)

	var phase string
	var podName string
	var ready bool
	var readyKnown bool

	if spec.desiredState == "Suspended" {
		phase = "Suspended"
	} else {
		pod, getErr := o.pods().Get(ctx, name, metav1.GetOptions{})
		switch {
		case getErr != nil && apierrors.IsNotFound(getErr):
			phase = "Provisioning"
		case getErr != nil:
			return false, fmt.Errorf("get pod %s for status: %w", name, getErr)
		default:
			podName = pod.GetName()
			if pod.GetDeletionTimestamp() != nil {
				// ensurePod issued the Delete for a SpecHash-triggered
				// Recreate (or ownerReference cascade GC is mid-flight on
				// CR deletion) and the kubelet is honoring
				// terminationGracePeriodSeconds — this IS agent.yaml §5.3's
				// "Draining" phase, not "Provisioning": there is a Pod, it
				// is not gone yet, and it is not accepting new work.
				phase = "Draining"
				ready, readyKnown = false, true
			} else {
				podPhase, _, _ := unstructured.NestedString(pod.Object, "status", "phase")
				ready, readyKnown = podReady(pod)
				phase = mapPodPhase(podPhase, ready, readyKnown)
			}
		}
	}

	existingConditions, _, _ := unstructured.NestedSlice(agent.Object, "status", "conditions")
	newConditions := upsertReadyCondition(existingConditions, phase, ready, readyKnown)

	if err := unstructured.SetNestedField(agent.Object, phase, "status", "phase"); err != nil {
		return false, err
	}
	if podName != "" {
		if err := unstructured.SetNestedField(agent.Object, podName, "status", "podName"); err != nil {
			return false, err
		}
	}
	if err := unstructured.SetNestedField(agent.Object, spec.specHash, "status", "observedSpecHash"); err != nil {
		return false, err
	}
	if err := unstructured.SetNestedField(agent.Object, spec.configHash, "status", "observedConfigHash"); err != nil {
		return false, err
	}
	if err := unstructured.SetNestedSlice(agent.Object, newConditions, "status", "conditions"); err != nil {
		return false, err
	}

	if _, err := o.agents().UpdateStatus(ctx, agent, metav1.UpdateOptions{}); err != nil {
		return false, fmt.Errorf("update agent %s status: %w", name, err)
	}

	switch phase {
	case "Ready", "Working", "Failed", "Stopped", "Suspended":
		return false, nil
	default:
		return true, nil
	}
}

// mapPodPhase translates core/v1 Pod phase + container readiness into
// agent.yaml's status.phase vocabulary (§5.3:
// Pending/Provisioning/Booting/Ready/Working/Draining/Stopped/Suspended/
// Failed). "Working" (actively processing a task) has no Pod-level signal
// this operator can observe — that distinction belongs to the supervisor's
// own heartbeat/task-claim reporting (a later phase's concern), so this
// function never produces it.
func mapPodPhase(podPhase string, ready, readyKnown bool) string {
	switch podPhase {
	case "", "Pending":
		return "Provisioning"
	case "Running":
		if readyKnown && ready {
			return "Ready"
		}
		return "Booting"
	case "Succeeded":
		return "Stopped"
	case "Failed":
		return "Failed"
	default:
		return "Provisioning"
	}
}

// podReady reads the Pod's standard "Ready" PodCondition (not a container
// status) — true readiness needs every container ready AND the Pod's own
// readiness gates satisfied, which is exactly what this condition already
// aggregates; re-deriving it from containerStatuses here would just be a
// worse reimplementation of what the kubelet already computed.
func podReady(pod *unstructured.Unstructured) (ready bool, known bool) {
	conditions, found, _ := unstructured.NestedSlice(pod.Object, "status", "conditions")
	if !found {
		return false, false
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
		return status == "True", true
	}
	return false, false
}

// upsertReadyCondition builds agent.yaml's status.conditions array (the K8s
// standard condition shape), preserving lastTransitionTime across calls
// where the status did not change — a fresh timestamp on every 3-second
// requeue would make "how long has this actually been unready" unreadable.
func upsertReadyCondition(existing []interface{}, phase string, ready, readyKnown bool) []interface{} {
	var status string
	var reason string
	switch {
	case !readyKnown:
		status, reason = "Unknown", "PodStatusUnknown"
	case ready:
		status, reason = "True", "PodReady"
	default:
		status, reason = "False", "PodNotReady"
	}
	switch phase {
	case "Suspended":
		status, reason = "False", "Suspended"
	case "Draining":
		status, reason = "False", "Draining"
	}

	prevStatus := ""
	prevTime := ""
	out := make([]interface{}, 0, len(existing))
	for _, c := range existing {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _, _ := unstructured.NestedString(cm, "type"); t == "Ready" {
			prevStatus, _, _ = unstructured.NestedString(cm, "status")
			prevTime, _, _ = unstructured.NestedString(cm, "lastTransitionTime")
			continue // rebuilt below, not copied forward as-is
		}
		out = append(out, c)
	}

	lastTransition := prevTime
	if prevStatus != status || lastTransition == "" {
		lastTransition = time.Now().UTC().Format(time.RFC3339)
	}

	out = append(out, map[string]interface{}{
		"type":               "Ready",
		"status":             status,
		"reason":             reason,
		"message":            fmt.Sprintf("phase=%s", phase),
		"lastTransitionTime": lastTransition,
	})
	return out
}
