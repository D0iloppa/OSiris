package operator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// specHashAnnotation is stamped on every Pod this operator creates, holding
// the spec.specHash value the Pod was rendered from (D-2's Recreate axis —
// see ensurePod's doc comment). ConfigHash is deliberately NOT stamped here
// and NOT compared anywhere in this file: A12 requires a ConfigHash change
// to touch only the config Secret (ensureConfigSecret, unconditional mirror,
// already true since D-1) and never the Pod.
const specHashAnnotation = "osiris.dev/spec-hash"

// agentSpec is this package's own, narrow read of agent.yaml's spec —
// deliberately NOT workload.AgentWorkloadSpec (importing internal/workload
// would be harmless dependency-wise, but this operator's only real contract
// is "what agent.yaml's schema says", and reading the CR's own wire shape
// directly keeps that contract explicit: if the CRD schema and
// AgentWorkloadSpec ever drift, this operator sees the CRD's truth, not a
// Go struct that might lag it).
type agentSpec struct {
	employeeID   string
	teamID       string
	teamSlug     string
	isMaster     bool
	image        string
	modelAlias   string
	cpuMilli     int64
	memMiB       int64
	storageGiB   int64
	networkName  string
	concurrency  int64
	drainSeconds int64
	desiredState string
	extraNoProxy string

	renderedPrompt       string
	renderedSettings     string
	renderedMCP          string
	renderedAllowedTools string

	specHash   string
	configHash string
}

func parseSpec(agent *unstructured.Unstructured) (agentSpec, error) {
	var s agentSpec
	str := func(fields ...string) string {
		v, _, _ := unstructured.NestedString(agent.Object, append([]string{"spec"}, fields...)...)
		return v
	}
	i64 := func(fields ...string) int64 {
		v, _, _ := unstructured.NestedInt64(agent.Object, append([]string{"spec"}, fields...)...)
		return v
	}
	b := func(fields ...string) bool {
		v, _, _ := unstructured.NestedBool(agent.Object, append([]string{"spec"}, fields...)...)
		return v
	}

	s.employeeID = str("employeeId")
	if s.employeeID == "" {
		return s, fmt.Errorf("spec.employeeId is required")
	}
	s.teamID = str("teamId")
	s.teamSlug = str("teamSlug")
	s.isMaster = b("isMaster")
	s.image = str("image")
	s.modelAlias = str("modelAlias")
	s.cpuMilli = i64("resources", "cpuMilli")
	s.memMiB = i64("resources", "memMiB")
	s.storageGiB = i64("resources", "storageGiB")
	s.networkName = str("networkName")
	s.concurrency = i64("concurrency")
	s.drainSeconds = i64("drainSeconds")
	s.desiredState = str("desiredState")
	s.extraNoProxy = str("extraNoProxy")
	s.renderedPrompt = str("renderedPrompt")
	s.renderedSettings = str("renderedSettings")
	s.renderedMCP = str("renderedMCP")
	s.renderedAllowedTools = str("renderedAllowedTools")
	s.specHash = str("specHash")
	s.configHash = str("configHash")

	if s.cpuMilli < 1000 {
		// Belt-and-suspenders with the CRD schema's own `minimum: 1000` and
		// the namespace LimitRange (resourcequota-agents.yaml) — a value
		// that got here despite both is a bug elsewhere, not something to
		// silently clamp.
		return s, fmt.Errorf("spec.resources.cpuMilli %d is below the 1000m floor (10 §5.4)", s.cpuMilli)
	}
	return s, nil
}

// -- naming ------------------------------------------------------------

func agentName(employeeID string) string    { return employeeID }
func pvcName(employeeID string) string       { return "osiris-ws-" + employeeID }
func credsSecretName(employeeID string) string { return employeeID + "-creds" } // MUST match driver/kube.credsSecretName
func configSecretName(employeeID string) string { return employeeID + "-config" }

// -- ServiceAccount ------------------------------------------------------

func (o *Operator) ensureServiceAccount(ctx context.Context, owner metav1.OwnerReference, spec agentSpec) error {
	name := agentName(spec.employeeID)
	_, err := o.sas().Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil // identity object, nothing to reconcile beyond existence
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get serviceaccount %s: %w", name, err)
	}
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ServiceAccount",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": o.namespace,
			"labels":    agentLabels(spec.employeeID),
			"ownerReferences": []interface{}{ownerRefMap(owner)},
		},
		// No K8s API permissions bound to this SA (deliberately no
		// RoleBinding created anywhere for it) — the agent talks to NATS
		// and osiris-core over the network, never to the K8s API, so an
		// auto-mounted token would be a credential it has no use for and a
		// bigger blast radius if the Pod were ever compromised. See
		// buildPod's automountServiceAccountToken: false for the other half
		// of this decision.
	}}
	if _, err := o.sas().Create(ctx, obj, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create serviceaccount %s: %w", name, err)
	}
	return nil
}

// -- PVC ------------------------------------------------------------------

func (o *Operator) ensurePVC(ctx context.Context, owner metav1.OwnerReference, spec agentSpec) error {
	name := pvcName(spec.employeeID)
	_, err := o.pvcs().Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil // PVCs are immutable in the fields that would matter here (storage size shrink/grow is its own can of worms, out of scope)
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get pvc %s: %w", name, err)
	}
	sizeGiB := spec.storageGiB
	if sizeGiB < 1 {
		sizeGiB = 1 // resourcequota-agents.yaml's LimitRange.PersistentVolumeClaim.min is 1Gi
	}
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata": map[string]interface{}{
			"name":            name,
			"namespace":       o.namespace,
			"labels":          agentLabels(spec.employeeID),
			"ownerReferences": []interface{}{ownerRefMap(owner)},
		},
		"spec": map[string]interface{}{
			"accessModes": []interface{}{"ReadWriteOnce"},
			// storageClassName intentionally omitted (not set at all) —
			// same "" -> cluster default convention values.yaml already
			// uses for the other PVCs in this chart (e.g. neo4j/nats/
			// omniroute's storageClassName: "").
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"storage": fmt.Sprintf("%dGi", sizeGiB),
				},
			},
		},
	}}
	if _, err := o.pvcs().Create(ctx, obj, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create pvc %s: %w", name, err)
	}
	return nil
}

// -- config Secret ---------------------------------------------------------
//
// WHY THIS IS A SECRET, NOT A CONFIGMAP (and why that is not a workaround):
// rbac-operator.yaml's osiris-agent-operator Role grants
// ["pods","persistentvolumeclaims","serviceaccounts","secrets"] — no
// "configmaps". The work order says to treat that Role as complete and add
// to it only when "정말 필요할 때" (truly necessary), with the reason
// written down. It is not necessary here: agent.yaml's spec.renderedPrompt/
// renderedSettings/renderedMCP/renderedAllowedTools are already plaintext
// in the Agent CR's spec (`kubectl get agent -o yaml` already shows them —
// unlike Credentials, which agent.yaml's own doc comment says was
// deliberately kept OUT of spec for exactly this readability reason). So
// putting them in a Secret buys no additional confidentiality; it is
// reused here purely because the verb set the Role already grants for
// "secrets" (create/get/list/watch/update/patch/delete) already covers it,
// and adding "configmaps" to the Role would be widening RBAC for a
// distinction (Secret vs ConfigMap) that carries no security difference for
// this particular content.
func (o *Operator) ensureConfigSecret(ctx context.Context, owner metav1.OwnerReference, spec agentSpec) error {
	name := configSecretName(spec.employeeID)
	data := map[string]interface{}{
		"CLAUDE.md":     b64(spec.renderedPrompt),
		"settings.json": b64(spec.renderedSettings),
		"mcp.json":      b64(spec.renderedMCP),
		"allowed_tools": b64(spec.renderedAllowedTools),
	}
	existing, err := o.secrets().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("get config secret %s: %w", name, err)
		}
		obj := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"type":       "Opaque",
			"metadata": map[string]interface{}{
				"name":            name,
				"namespace":       o.namespace,
				"labels":          agentLabels(spec.employeeID),
				"ownerReferences": []interface{}{ownerRefMap(owner)},
			},
			"data": data,
		}}
		if _, err := o.secrets().Create(ctx, obj, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create config secret %s: %w", name, err)
		}
		return nil
	}
	// Keep content in sync every reconcile — this is NOT the D-2
	// ConfigHash-triggered ReloadConfig state machine (there is no
	// recreate/patch DECISION here, just always-mirror), see the package
	// doc comment on syncAgent for the explicit D-2 boundary.
	if err := unstructured.SetNestedMap(existing.Object, data, "data"); err != nil {
		return fmt.Errorf("set config secret %s data: %w", name, err)
	}
	if _, err := o.secrets().Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update config secret %s: %w", name, err)
	}
	return nil
}

// -- Pod --------------------------------------------------------------------

// noProxyList mirrors driver/docker.noProxyList (§1.2 invariant 10's three
// hostnames + loopback + operator-supplied extras), translated to this
// namespace's cross-namespace FQDNs — see buildPod's doc comment on why
// FQDNs, not the bare "nats"/"osiris-core"/"osiris-egress" literals.
func noProxyList(systemNamespace, extra string) string {
	base := fmt.Sprintf("%s,%s,%s,localhost,127.0.0.1,::1",
		natsFQDN(systemNamespace), coreFQDN(systemNamespace), egressFQDN(systemNamespace))
	if extra = strings.TrimSpace(extra); extra != "" {
		return base + "," + extra
	}
	return base
}

// natsFQDN/coreFQDN/egressFQDN cross the osiris-agents -> osiris-system
// namespace boundary via Kubernetes Service DNS.
//
// ★ DEVIATION FROM §1.2 invariant 10, RECORDED: the design says an agent
// knows exactly three bare hostnames — "nats", "osiris-core",
// "osiris-egress" — a compose-network-alias assumption (docker-compose put
// all three in the same network with those aliases). In K8s these Services
// live in osiris-system while the agent Pod lives in osiris-agents (K10 —
// namespaces the CR/Pod cannot cross), and bare-name DNS resolution does
// not cross a namespace boundary. Two ways to preserve the literal
// three-hostname invariant were available: (a) add ExternalName Service
// objects named "nats"/"osiris-core"/"osiris-egress" INSIDE osiris-agents
// that alias to the real FQDNs, or (b) use the FQDN directly as the value
// of OSIRIS_NATS_URL/OSIRIS_CORE_URL/OSIRIS_EGRESS_PROXY (and NO_PROXY).
// This operator does (b) — it is the smaller, self-contained change (no new
// Service objects, no new NetworkPolicy surface to reason about), and it is
// functionally equivalent for every purpose the invariant actually serves
// (§8.3's "도달 가능한 것은 이 셋뿐" is enforced by NetworkPolicy pod/
// namespace selectors, which do not care what hostname string a client
// used to resolve the IP). Reported rather than silently chosen — (a) is
// the literal-invariant-preserving option and remains available to a future
// worker/supervisor if bare hostnames turn out to matter for a reason this
// phase did not anticipate (e.g. a hardcoded hostname check in the
// supervisor image).
func natsFQDN(systemNamespace string) string {
	return fmt.Sprintf("nats.%s.svc.cluster.local", systemNamespace)
}
func coreFQDN(systemNamespace string) string {
	return fmt.Sprintf("osiris-core.%s.svc.cluster.local", systemNamespace)
}
func egressFQDN(systemNamespace string) string {
	return fmt.Sprintf("osiris-egress.%s.svc.cluster.local", systemNamespace)
}

func boolEnv(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// ensurePod is D-2's Recreate axis. Contract (10 §6.2 / spec.go's
// ComputeHashes / agent.yaml's specHash doc comment, verbatim: "합치면
// 프롬프트 한 줄 수정이 전 에이전트 재생성이 된다"):
//
//   - ConfigHash alone changing must NEVER reach this function's delete
//     path — this function only ever compares spec.specHash against the
//     Pod's own osiris.dev/spec-hash annotation, so a ConfigHash-only patch
//     (reconcile.go's ReloadConfig -> driver/kube's merge-patch restricted
//     to configPatchFields) leaves that annotation, and therefore this
//     comparison, untouched. The config Secret still gets refreshed every
//     tick by ensureConfigSecret, unconditionally — that is the ONLY
//     observable effect of a ConfigHash change on this operator's output.
//   - SpecHash changing recreates the Pod, bounded by spec.drainSeconds:
//     the Pod's terminationGracePeriodSeconds was set to spec.drainSeconds
//     at creation time (below), so a plain Delete — no SIGKILL-now, no
//     unbounded wait either — already gives the outgoing process up to
//     drainSeconds to finish its current turn before the kubelet SIGKILLs
//     it. That same Pod field is what makes ownerReference cascade GC (on
//     `kubectl delete agent`) respect the same bound with zero extra code
//     here — see operator.go's package doc comment on why no finalizer is
//     used for that path.
//   - The PVC is never touched by this function (ensurePVC is a separate,
//     idempotent-on-exists call in syncAgent) — Recreate replaces the Pod
//     object only, so the workspace volume survives verbatim.
func (o *Operator) ensurePod(ctx context.Context, owner metav1.OwnerReference, spec agentSpec) error {
	name := agentName(spec.employeeID)
	existing, err := o.pods().Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		observedHash := existing.GetAnnotations()[specHashAnnotation]
		if observedHash == spec.specHash {
			return nil // unchanged — nothing to do
		}
		if observedHash == "" {
			// Adoption: a Pod predating this annotation (or one this very
			// function is adopting for the first time). Do NOT recreate on
			// this tick — that would be exactly the "first tick after this
			// feature ships recreates the whole fleet" disaster A12 exists
			// to prevent.
			//
			// BUT the annotation MUST be stamped here, not left empty. If it
			// stayed empty, EVERY future tick would hit this same branch
			// forever — observedHash == "" would keep being true no matter
			// how many times spec.specHash actually changes afterward,
			// because nothing else in this file ever writes the annotation
			// except Pod creation (which does not run again while the Pod
			// exists). That would make an adopted Pod PERMANENTLY IMMUNE to
			// Recreate: change image, change resources, change anything on
			// the SpecHash axis — nothing would happen, silently, forever.
			// A silent, permanent no-op is exactly the failure mode this
			// codebase is least tolerant of. So: patch the annotation to the
			// CURRENT spec.specHash right now, which establishes the
			// baseline this tick adopts from — the NEXT real SpecHash
			// change compares against that baseline and Recreates normally.
			if err := o.stampSpecHashAnnotation(ctx, name, spec.specHash); err != nil {
				// Do NOT fall through to the delete path on a patch failure
				// (e.g. a resourceVersion conflict from a concurrent
				// update) — misreading "couldn't stamp" as "hash differs,
				// delete the Pod" would be strictly worse than doing
				// nothing. Return the error so the workqueue retries this
				// same adoption on the next tick.
				return fmt.Errorf("adopt pod %s (stamp spec-hash annotation): %w", name, err)
			}
			return nil
		}
		if existing.GetDeletionTimestamp() != nil {
			// Already draining from a previous tick's Delete call below —
			// nothing to do until it's actually gone; status.go reports
			// "Draining" for this state and syncAgent's requeue keeps
			// polling every 3s (see updateStatus).
			return nil
		}
		grace := spec.drainSeconds
		if grace < 0 {
			grace = 0
		}
		log.Printf("osiris-orchestrator: agent operator: SpecHash changed for %s (pod annotation %s -> spec %s) — deleting pod with %ds grace (Recreate, PVC preserved)",
			spec.employeeID, observedHash, spec.specHash, grace)
		if err := o.pods().Delete(ctx, name, metav1.DeleteOptions{GracePeriodSeconds: &grace}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete pod %s for recreate: %w", name, err)
		}
		return nil // next tick (or this Delete's own resync) creates the replacement once the old Pod is actually gone
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get pod %s: %w", name, err)
	}

	env := []interface{}{
		envVar("OSIRIS_EMPLOYEE_ID", spec.employeeID),
		envVar("OSIRIS_TEAM_ID", spec.teamID),
		envVar("OSIRIS_TEAM_SLUG", spec.teamSlug),
		envVar("OSIRIS_NATS_URL", "nats://"+natsFQDN(o.systemNamespace)+":4222"),
		envVar("OSIRIS_CORE_URL", "http://"+coreFQDN(o.systemNamespace)+":8090"),
		envVar("OSIRIS_EGRESS_PROXY", "http://"+egressFQDN(o.systemNamespace)+":3128"),
		envVar("OSIRIS_NO_PROXY", noProxyList(o.systemNamespace, spec.extraNoProxy)),
		envVar("OSIRIS_MODEL_ALIAS", spec.modelAlias),
		envVar("OSIRIS_INBOX_STREAM", "INBOX"),
		envVar("OSIRIS_INBOX_SUBJECT", fmt.Sprintf("team.%s.inbox.%s", spec.teamSlug, spec.employeeID)),
		envVar("OSIRIS_INBOX_DURABLE", "agent-"+spec.employeeID),
		envVar("OSIRIS_DRAIN_SECONDS", fmt.Sprintf("%d", spec.drainSeconds)),
		envVar("OSIRIS_MAX_TASK_POINTS", fmt.Sprintf("%d", spec.concurrency)),
		envVar("OSIRIS_INBOX_MULTIFILTER", boolEnv(spec.isMaster)),
	}

	uid65532 := int64(65532)

	container := map[string]interface{}{
		"name":  "agent",
		"image": spec.image,
		"env":   env,
		"resources": map[string]interface{}{
			"requests": map[string]interface{}{
				"cpu":    fmt.Sprintf("%dm", spec.cpuMilli),
				"memory": fmt.Sprintf("%dMi", spec.memMiB),
			},
			"limits": map[string]interface{}{
				"cpu":    fmt.Sprintf("%dm", spec.cpuMilli),
				"memory": fmt.Sprintf("%dMi", spec.memMiB),
			},
		},
		"securityContext": map[string]interface{}{
			"allowPrivilegeEscalation": false,
			"readOnlyRootFilesystem":   true,
			"capabilities":             map[string]interface{}{"drop": []interface{}{"ALL"}},
			"runAsNonRoot":             true,
			"runAsUser":                uid65532,
			"runAsGroup":               uid65532,
		},
		"volumeMounts": []interface{}{
			map[string]interface{}{"name": "workspace", "mountPath": "/workspace"},
			map[string]interface{}{"name": "creds", "mountPath": "/var/run/osiris/creds", "readOnly": true},
			map[string]interface{}{"name": "config", "mountPath": "/var/run/osiris/config", "readOnly": true},
			map[string]interface{}{"name": "tmp", "mountPath": "/tmp"},
		},
	}

	pod := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": o.namespace,
			"labels":    agentLabels(spec.employeeID),
			"annotations": map[string]interface{}{
				// The Recreate axis's only piece of state — see ensurePod's
				// doc comment. Stamped once at create time; never patched
				// in place (a spec-hash change always goes through
				// delete-then-recreate, never an Update of this field).
				specHashAnnotation: spec.specHash,
			},
			"ownerReferences": []interface{}{ownerRefMap(owner)},
		},
		"spec": map[string]interface{}{
			"serviceAccountName": name,
			// See ensureServiceAccount's doc comment: this SA has no K8s
			// RBAC bound to it, so the default token mount would be a
			// credential with no purpose — don't create it at all.
			"automountServiceAccountToken": false,
			"restartPolicy":                "Always", // kubelet-restarts-in-place, the k8s analogue of driver/docker's RestartPolicy: unless-stopped
			// Bounds BOTH drain paths (ensurePod's own SpecHash-triggered
			// Delete above, and ownerReference cascade GC on `kubectl
			// delete agent`) to spec.drainSeconds — see the package doc
			// comment on why no finalizer is needed for the latter.
			"terminationGracePeriodSeconds": spec.drainSeconds,
			"securityContext": map[string]interface{}{
				"runAsNonRoot": true,
				"runAsUser":    uid65532,
				"runAsGroup":   uid65532,
				"fsGroup":      uid65532,
			},
			"containers": []interface{}{container},
			"volumes": []interface{}{
				map[string]interface{}{
					"name":                  "workspace",
					"persistentVolumeClaim": map[string]interface{}{"claimName": pvcName(spec.employeeID)},
				},
				map[string]interface{}{
					"name": "creds",
					"secret": map[string]interface{}{
						"secretName": credsSecretName(spec.employeeID),
						// optional: true — driver/kube's ensureCredentialsSecret
						// (internal/driver/kube/driver.go) may not have created
						// this Secret yet on a very first tick race (its own doc
						// comment documents that exact ordering gap); the Pod
						// should still come up rather than block forever on a
						// Secret volume k8s treats as required by default.
						"optional": true,
					},
				},
				map[string]interface{}{
					"name": "config",
					"secret": map[string]interface{}{
						"secretName": configSecretName(spec.employeeID),
					},
				},
				map[string]interface{}{
					"name": "tmp",
					"emptyDir": map[string]interface{}{
						"medium":    "Memory",
						"sizeLimit": "512Mi",
					},
				},
			},
		},
	}}

	if _, err := o.pods().Create(ctx, pod, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create pod %s: %w", name, err)
	}
	return nil
}

// stampSpecHashAnnotation adopts a Pod that predates (or otherwise lacks)
// specHashAnnotation by patching JUST that annotation onto it — a merge
// patch restricted to metadata.annotations, the same "touch only the axis
// you mean to touch" discipline driver/kube.ReloadConfig uses for its
// configPatchFields patch. Not a full object Update: this must not perturb
// anything else about a Pod that is, from this operator's perspective,
// already correct.
func (o *Operator) stampSpecHashAnnotation(ctx context.Context, podName, specHash string) error {
	patch, err := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				specHashAnnotation: specHash,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal spec-hash annotation patch: %w", err)
	}
	if _, err := o.pods().Patch(ctx, podName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("patch pod %s annotations: %w", podName, err)
	}
	return nil
}

func envVar(name, value string) map[string]interface{} {
	return map[string]interface{}{"name": name, "value": value}
}

func agentLabels(employeeID string) map[string]interface{} {
	return map[string]interface{}{
		"app.kubernetes.io/managed-by": "osiris-agent-operator",
		"osiris.dev/employee-id":       employeeID,
	}
}

func ownerRefMap(owner metav1.OwnerReference) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion":         owner.APIVersion,
		"kind":               owner.Kind,
		"name":               owner.Name,
		"uid":                string(owner.UID),
		"controller":         boolDeref(owner.Controller),
		"blockOwnerDeletion": boolDeref(owner.BlockOwnerDeletion),
	}
}

// boolDeref reads a *bool for embedding in unstructured content, where only
// plain JSON-ish primitives (bool/int64/float64/string/map/slice/nil) are
// legal — a raw *bool (e.g. metav1.OwnerReference.Controller) makes
// runtime.DeepCopyJSONValue panic ("cannot deep copy *bool"), caught by
// this package's own test.
func boolDeref(p *bool) bool { return p != nil && *p }

// b64 encodes for a Secret's "data" field by hand for the same reason
// driver/kube.encodeSecretData does it (types.go's doc comment): writing
// "data" directly (not "stringData") keeps this correct against both a real
// apiserver and a fake dynamic client that does not replicate
// stringData->data admission-time promotion.
func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
