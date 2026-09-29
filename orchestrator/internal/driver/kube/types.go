package kube

import (
	"encoding/base64"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"osiris-orchestrator/internal/workload"
)

// agentGVR/secretGVR are the two GroupVersionResources this driver ever
// touches — deliberately just two, matching this phase's mandate ("Agent CR을
// 만들고 지우는 것이 이 드라이버의 일입니다 — Pod를 직접 만들지 마세요",
// docs/design/70-kubernetes-deployment.md §5, deploy/osiris/crds/agent.yaml).
// A driver that reached for pods/v1, persistentvolumeclaims/v1,
// serviceaccounts/v1 or networkpolicies/v1 would be re-implementing the
// operator this phase deliberately leaves unbuilt (§5.1's K9: the operator
// renders those five objects FROM the CR; this driver only writes the CR
// itself, plus the one Secret it — not the operator — is responsible for,
// see driver.go's ensureCredentialsSecret doc comment for why credentials are
// the one exception).
var (
	agentGVR = schema.GroupVersionResource{
		Group: "osiris.dev", Version: "v1alpha1", Resource: "agents",
	}
	secretGVR = schema.GroupVersionResource{
		Group: "", Version: "v1", Resource: "secrets",
	}
)

const (
	agentAPIVersion = "osiris.dev/v1alpha1"
	agentKind       = "Agent"
)

// agentName maps an employeeID to the Agent CR's metadata.name. K8s object
// names must be DNS-1123 labels (lowercase alphanumeric + '-'); this build
// does not verify that graph-derived employeeIDs already satisfy that
// (unverified assumption, flagged rather than silently invented — a stricter
// sanitizer belongs to whichever phase actually seeds employeeIDs).
func agentName(employeeID string) string { return employeeID }

// credsSecretName is the one Secret this driver owns per agent — the K5
// "Secret" in "Pod+PVC+SA+Secret+NetworkPolicy". Everything else in that list
// is the operator's to render from the CR; credentials are reconcile's to
// generate (internal/natsauth, internal/reconcile.buildCredentials) and this
// driver's to deliver, exactly mirroring driver/docker's writeCredsFiles.
func credsSecretName(employeeID string) string { return employeeID + "-creds" }

// buildAgentObject renders spec (workload.AgentWorkloadSpec, the Go-side
// source of truth) into the wire shape deploy/osiris/crds/agent.yaml
// declares. This is a STRUCTURAL, field-by-field mapping — no field is
// invented here that the CRD schema does not already name, and Credentials
// is deliberately the one AgentWorkloadSpec field that does NOT appear
// (agent.yaml's spec.description: "Credentials 맵은 의도적으로 이 spec에
// 없다").
func buildAgentObject(namespace string, spec workload.AgentWorkloadSpec) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(agentAPIVersion)
	obj.SetKind(agentKind)
	obj.SetName(agentName(spec.EmployeeID))
	obj.SetNamespace(namespace)

	grants := make([]interface{}, 0, len(spec.Grants))
	for _, g := range spec.Grants {
		grants = append(grants, g)
	}

	specFields := map[string]interface{}{
		"employeeId": spec.EmployeeID,
		"teamId":     spec.TeamID,
		"teamSlug":   spec.TeamSlug,
		"roleId":     spec.RoleID,
		"isMaster":   spec.IsMaster,
		"isLead":     spec.IsLead,
		"image":      spec.Image,
		"modelAlias": spec.ModelAlias,
		"resources": map[string]interface{}{
			"cpuMilli":   spec.Resources.CPUMilli,
			"memMiB":     spec.Resources.MemMiB,
			"storageGiB": spec.Resources.StorageGiB,
		},
		"grants":               grants,
		"networkName":          spec.NetworkName,
		"concurrency":          int64(spec.Concurrency),
		"drainSeconds":         int64(spec.DrainSeconds),
		"desiredState":         spec.Desired,
		"extraNoProxy":         spec.ExtraNoProxy,
		"renderedPrompt":       spec.RenderedPrompt,
		"renderedSettings":     spec.RenderedSettings,
		"renderedMCP":          spec.RenderedMCP,
		"renderedAllowedTools": spec.RenderedAllowedTools,
		"specHash":             spec.SpecHash,
		"configHash":           spec.ConfigHash,
	}
	obj.Object["spec"] = specFields
	return obj
}

// configPatchFields returns ONLY the ConfigHash axis (agent.yaml: "이 필드가
// 바뀌면 operator가 ReloadConfig … 한다") — the counterpart of
// buildAgentObject's full spec, used by ReloadConfig's merge patch so a
// prompt edit touches nothing SpecHash reads (10-core-architecture.md §6.2,
// the ComputeHashes split this driver must not blur).
func configPatchFields(spec workload.AgentWorkloadSpec) map[string]interface{} {
	return map[string]interface{}{
		"spec": map[string]interface{}{
			"modelAlias":           spec.ModelAlias,
			"renderedPrompt":       spec.RenderedPrompt,
			"renderedSettings":     spec.RenderedSettings,
			"renderedMCP":          spec.RenderedMCP,
			"renderedAllowedTools": spec.RenderedAllowedTools,
			"configHash":           spec.ConfigHash,
		},
	}
}

// newSecretObject renders a plain core/v1 Secret as unstructured content.
// Values are base64-encoded by hand (the "data" field's wire representation)
// rather than using "stringData", because stringData->data promotion is
// apiserver admission-time behavior that a fake/test client does not
// replicate — writing "data" directly keeps SyncCredentials' content-diff
// comparison (see driver.go) correct against both a real cluster and a fake
// one.
func newSecretObject(namespace, name string, data map[string]interface{}) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("v1")
	obj.SetKind("Secret")
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.Object["type"] = "Opaque"
	obj.Object["data"] = data
	return obj
}

func encodeSecretData(creds map[string]string) map[string]interface{} {
	out := make(map[string]interface{}, len(creds))
	for k, v := range creds {
		out[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return out
}

// secretDataEqual compares the "data" map read back from the API
// (map[string]interface{} of base64 strings, per unstructured.NestedMap) with
// a freshly-encoded desired map. Byte-for-byte on the encoded form is
// sufficient because base64 is a deterministic function of the input.
func secretDataEqual(existing, desired map[string]interface{}) bool {
	if len(existing) != len(desired) {
		return false
	}
	for k, v := range desired {
		ev, ok := existing[k]
		if !ok || ev != v {
			return false
		}
	}
	return true
}
