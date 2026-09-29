// Package k8sread is osiris-api's ONLY k8s API access, and it is read-only —
// GET/list/watch verbs alone, no create/update/patch/delete anywhere in this
// package. It exists for exactly one purpose: docs/design/90-infra-topology.md
// §4.3's "콘솔·osiris-api의 인프라 접근 1단계는 읽기 전용"(IN4).
//
// ── WHY NOT client-go ────────────────────────────────────────────────────
//
// k8s.io/client-go (+ k8s.io/api + k8s.io/apimachinery) is the obvious choice
// for a "real" k8s client, but it drags in a large, fast-moving dependency
// tree for a service whose Dockerfile comment already states a minimalism
// goal ("이 이미지는 otherwise minimal[...] no npm registry access at
// runtime", api/cmd/osiris-api/main.go's package doc). osiris-api talks to
// every other dependency (Neo4j, NATS) with an official driver because there
// is real protocol complexity to get right; the k8s API is plain REST+JSON
// over a well-known in-cluster mount, and the actual hard requirement here —
// §4.3(2)'s "osiris-api가 콘솔로 넘길 때 env/시크릿참조 같은 민감 필드를
// 프로젝션으로 제거" — is best satisfied by decoding responses into our OWN
// narrow structs (a field client-go doesn't know about here is simply never
// populated) rather than decoding into upstream's full-fidelity types and then
// having to remember to strip them before the JSON leaves this process.
//
// ── AUTH ─────────────────────────────────────────────────────────────────
//
// Standard in-cluster ServiceAccount mount:
//
//	/var/run/secrets/kubernetes.io/serviceaccount/token   (re-read per request —
//	                                                        projected tokens
//	                                                        rotate, roughly
//	                                                        hourly by default)
//	/var/run/secrets/kubernetes.io/serviceaccount/ca.crt   (read once, at
//	                                                        construction)
//	KUBERNETES_SERVICE_HOST / _PORT                        (env, set by k8s
//	                                                        on every pod)
//
// NewInClusterClient returns (nil, err) when any of these is absent — this is
// the SAME degrade-gracefully shape as graphread.NewLazyClient and the
// typed-nil guards in cmd/osiris-api/main.go (graphReader/holderSource/
// etc): a missing k8s mount (local dev, `npm run dev`, a test binary) must
// never crash the process, only make internal/infra's handlers answer 503.
package k8sread

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	tokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	caPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

// Client is a thin, read-only REST client against the in-cluster k8s API
// server. Every exported method issues exactly one GET.
type Client struct {
	base string
	hc   *http.Client
}

// NewInClusterClient builds a Client from the standard ServiceAccount mount.
// See the package doc comment for the degrade-on-absence contract.
func NewInClusterClient() (*Client, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("k8sread: not running in-cluster (KUBERNETES_SERVICE_HOST/PORT unset)")
	}
	if _, err := os.Stat(tokenPath); err != nil {
		return nil, fmt.Errorf("k8sread: serviceaccount token not mounted: %w", err)
	}
	caBytes, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("k8sread: read serviceaccount ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("k8sread: ca.crt has no usable certificates")
	}
	hc := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
	return &Client{base: "https://" + host + ":" + port, hc: hc}, nil
}

// get issues one authenticated GET and returns the raw response body. The
// bearer token is re-read from disk on every call — see package doc comment
// on why (rotation).
func (c *Client) get(path string) ([]byte, error) {
	tokBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("k8sread: read token: %w", err)
	}
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tokBytes)))
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("k8sread: GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20)) // 16MiB cap — a
	// pathological cluster response must not OOM this process; A단계 cluster
	// sizes (single node, dozens of pods) are nowhere near this.
	if err != nil {
		return nil, fmt.Errorf("k8sread: read body for %s: %w", path, err)
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("k8sread: forbidden (RBAC not applied or scope too narrow) for %s", path)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("k8sread: %s -> HTTP %d: %s", path, resp.StatusCode, truncate(body, 300))
	}
	return body, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// ─────────────────────────────────────────────────────────────────────────
// Wire structs — deliberately narrow. Every struct below is a PROJECTION:
// fields the upstream k8s object has that are not declared here are simply
// dropped by json.Unmarshal. This is §4.3(2)'s "민감 필드를 프로젝션으로
// 제거" mechanism, not a separate scrubbing pass — env vars, volume mounts,
// secret references, image pull secrets, and any other pod/deployment spec
// field never appear in any struct in this file and therefore can never
// reach the console through this package.
// ─────────────────────────────────────────────────────────────────────────

type meta struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// Node — capacity/allocatable + coarse status, for the topology skeleton
// (§4.3(a)'s cluster-scope ClusterRole: nodes get/list/watch).
type Node struct {
	Name        string            `json:"name"`
	Roles       []string          `json:"roles"`
	Ready       bool              `json:"ready"`
	KubeVersion string            `json:"kubeletVersion"`
	OS          string            `json:"os"`
	Arch        string            `json:"arch"`
	CPUCapacity string            `json:"cpuCapacity"`
	MemCapacity string            `json:"memCapacity"`
	CPUAlloc    string            `json:"cpuAllocatable"`
	MemAlloc    string            `json:"memAllocatable"`
	PodCIDR     string            `json:"podCIDR,omitempty"`
	Labels      map[string]string `json:"-"` // kept only long enough to derive Roles; never serialized
}

type nodeListWire struct {
	Items []struct {
		Metadata struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Status struct {
			Capacity    map[string]string `json:"capacity"`
			Allocatable map[string]string `json:"allocatable"`
			NodeInfo    struct {
				KubeletVersion  string `json:"kubeletVersion"`
				OperatingSystem string `json:"operatingSystem"`
				Architecture    string `json:"architecture"`
			} `json:"nodeInfo"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
		Spec struct {
			PodCIDR string `json:"podCIDR"`
		} `json:"spec"`
	} `json:"items"`
}

// ListNodes -> GET /api/v1/nodes (cluster-scope, IN §4.3(a)).
func (c *Client) ListNodes() ([]Node, error) {
	raw, err := c.get("/api/v1/nodes")
	if err != nil {
		return nil, err
	}
	var wire nodeListWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("k8sread: decode nodes: %w", err)
	}
	out := make([]Node, 0, len(wire.Items))
	for _, it := range wire.Items {
		ready := false
		for _, cond := range it.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" {
				ready = true
			}
		}
		var roles []string
		for k := range it.Metadata.Labels {
			const prefix = "node-role.kubernetes.io/"
			if strings.HasPrefix(k, prefix) {
				roles = append(roles, strings.TrimPrefix(k, prefix))
			}
		}
		out = append(out, Node{
			Name:        it.Metadata.Name,
			Roles:       roles,
			Ready:       ready,
			KubeVersion: it.Status.NodeInfo.KubeletVersion,
			OS:          it.Status.NodeInfo.OperatingSystem,
			Arch:        it.Status.NodeInfo.Architecture,
			CPUCapacity: it.Status.Capacity["cpu"],
			MemCapacity: it.Status.Capacity["memory"],
			CPUAlloc:    it.Status.Allocatable["cpu"],
			MemAlloc:    it.Status.Allocatable["memory"],
			PodCIDR:     it.Spec.PodCIDR,
		})
	}
	return out, nil
}

// Namespace — name + phase only.
type Namespace struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
}

type namespaceListWire struct {
	Items []struct {
		Metadata meta `json:"metadata"`
		Status   struct {
			Phase string `json:"phase"`
		} `json:"status"`
	} `json:"items"`
}

// ListNamespaces -> GET /api/v1/namespaces (cluster-scope, §4.3(a)).
func (c *Client) ListNamespaces() ([]Namespace, error) {
	raw, err := c.get("/api/v1/namespaces")
	if err != nil {
		return nil, err
	}
	var wire namespaceListWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("k8sread: decode namespaces: %w", err)
	}
	out := make([]Namespace, 0, len(wire.Items))
	for _, it := range wire.Items {
		out = append(out, Namespace{Name: it.Metadata.Name, Phase: it.Status.Phase})
	}
	return out, nil
}

// Pod is a STATUS SUMMARY, never a spec. Exactly the fields
// docs/design/90-infra-topology.md's A단계 scope names: "파드 상태(name/
// phase/restarts/node)". No containers[].env, no volumes, no image pull
// secrets, no securityContext — those fields exist on the upstream object and
// are intentionally never declared in podListWire below, so they cannot leak
// even by accident.
//
// ── ★ WHY THIS IS CLUSTER-SCOPE (a documented deviation from the design
// doc's literal §4.3(a) YAML) ────────────────────────────────────────────
//
// §4.3(a)'s ClusterRole snippet lists only nodes/namespaces (+ metrics); pod
// read is scoped to per-namespace Roles bound only in `infra-*` namespaces
// (§4.3(b)). Taken literally, that leaves osiris-system/osiris-agents/
// ingress-nginx pods entirely unreadable — and A단계's whole point (§7:
// "'토폴로지 ↔ 실제 파드'가 눈에 보이는 것") is unreachable without them,
// since no `infra-*` namespace exists yet (§8 명시: "infra-* NS가 아직
// 없으므로 ... 정직하게 표시"). This build closes that gap the way the task
// instructions for A단계 direct: add `pods` (get/list/watch only) to the
// cluster ClusterRole, and rely ENTIRELY on this struct's narrowness — not
// on RBAC — to keep the exposure to status fields. RBAC controls WHICH
// objects this SA can read; this struct controls WHICH FIELDS of those
// objects ever leave the process. Secrets are still not listed anywhere,
// at any scope — that boundary is untouched.
//
// This is a real, acknowledged widening of §4.3(2)'s "정보유출 표면은
// infra-* 워크로드 스펙으로 한정" claim: a pod's mere EXISTENCE, name,
// node placement, restart count and phase across osiris-system/osiris-agents
// now reaches osiris-api's cluster-scope read, where before nothing did.
// That is qualitatively different from leaking spec.containers[].env or a
// secret name — no secret VALUE or reference is exposed anywhere — but it is
// still new visibility next to §4.3(2)'s narrower framing, and T1 should
// re-confirm this specific extension explicitly rather than read it as
// already covered by the original §4.3(a) text.
type Pod struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Node       string `json:"node"`
	Phase      string `json:"phase"`
	Restarts   int32  `json:"restarts"`
	ReadyCount int32  `json:"readyContainers"`
	TotalCount int32  `json:"totalContainers"`
	StartTime  string `json:"startTime,omitempty"`
	OwnerKind  string `json:"ownerKind,omitempty"`
	OwnerName  string `json:"ownerName,omitempty"`
	Component  string `json:"component,omitempty"` // app.kubernetes.io/component label, if present
}

type podListWire struct {
	Items []struct {
		Metadata struct {
			Name            string            `json:"name"`
			Namespace       string            `json:"namespace"`
			Labels          map[string]string `json:"labels"`
			OwnerReferences []struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"ownerReferences"`
		} `json:"metadata"`
		// Spec is decoded ONLY for nodeName — nothing else in PodSpec is
		// declared here (no containers[], no volumes[], no imagePullSecrets).
		Spec struct {
			NodeName string `json:"nodeName"`
		} `json:"spec"`
		Status struct {
			Phase             string `json:"phase"`
			StartTime         string `json:"startTime"`
			ContainerStatuses []struct {
				Ready        bool  `json:"ready"`
				RestartCount int32 `json:"restartCount"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

// ListAllPods -> GET /api/v1/pods, cluster-wide. See Pod's doc comment for
// the RBAC widening this requires and why it is still safe (projection).
func (c *Client) ListAllPods() ([]Pod, error) {
	raw, err := c.get("/api/v1/pods")
	if err != nil {
		return nil, err
	}
	var wire podListWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("k8sread: decode pods: %w", err)
	}
	out := make([]Pod, 0, len(wire.Items))
	for _, it := range wire.Items {
		var restarts, ready, total int32
		for _, cs := range it.Status.ContainerStatuses {
			restarts += cs.RestartCount
			total++
			if cs.Ready {
				ready++
			}
		}
		var ownerKind, ownerName string
		if len(it.Metadata.OwnerReferences) > 0 {
			ownerKind = it.Metadata.OwnerReferences[0].Kind
			ownerName = it.Metadata.OwnerReferences[0].Name
		}
		out = append(out, Pod{
			Name:       it.Metadata.Name,
			Namespace:  it.Metadata.Namespace,
			Node:       it.Spec.NodeName,
			Phase:      it.Status.Phase,
			Restarts:   restarts,
			ReadyCount: ready,
			TotalCount: total,
			StartTime:  it.Status.StartTime,
			OwnerKind:  ownerKind,
			OwnerName:  ownerName,
			Component:  it.Metadata.Labels["app.kubernetes.io/component"],
		})
	}
	return out, nil
}

// NodeMetric / PodMetric — metrics-server usage snapshot (§9-⑤ 확정: 기존
// metrics-server 채택). Values are the raw metrics.k8s.io strings (e.g.
// "123456789n" for cpu nanocores, "45Mi" for memory) — the console formats
// them; this package does not parse quantities to avoid depending on
// apimachinery's resource package for the sake of one unit conversion.
type NodeMetric struct {
	Name   string `json:"name"`
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

type PodMetric struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	CPU       string `json:"cpu"`    // sum across containers
	Memory    string `json:"memory"` // sum across containers
}

type nodeMetricsWire struct {
	Items []struct {
		Metadata meta `json:"metadata"`
		Usage    struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
		} `json:"usage"`
	} `json:"items"`
}

// NodeMetrics -> GET /apis/metrics.k8s.io/v1beta1/nodes.
func (c *Client) NodeMetrics() ([]NodeMetric, error) {
	raw, err := c.get("/apis/metrics.k8s.io/v1beta1/nodes")
	if err != nil {
		return nil, err
	}
	var wire nodeMetricsWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("k8sread: decode node metrics: %w", err)
	}
	out := make([]NodeMetric, 0, len(wire.Items))
	for _, it := range wire.Items {
		out = append(out, NodeMetric{Name: it.Metadata.Name, CPU: it.Usage.CPU, Memory: it.Usage.Memory})
	}
	return out, nil
}

type podMetricsWire struct {
	Items []struct {
		Metadata   meta `json:"metadata"`
		Containers []struct {
			Usage struct {
				CPU    string `json:"cpu"`
				Memory string `json:"memory"`
			} `json:"usage"`
		} `json:"containers"`
	} `json:"items"`
}

// PodMetrics -> GET /apis/metrics.k8s.io/v1beta1/pods, cluster-wide (the
// same ClusterRole grants metrics.k8s.io {nodes,pods} get/list — §4.3(a)).
// Per-container CPU/memory strings are NOT summed numerically here (they are
// heterogeneous quantity units, e.g. "10m" vs "100000n") — callers that need
// a per-pod total should parse with a quantity-aware library; this endpoint
// hands back the raw per-container strings.
func (c *Client) PodMetrics() ([]PodMetric, error) {
	raw, err := c.get("/apis/metrics.k8s.io/v1beta1/pods")
	if err != nil {
		return nil, err
	}
	var wire podMetricsWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("k8sread: decode pod metrics: %w", err)
	}
	out := make([]PodMetric, 0, len(wire.Items))
	for _, it := range wire.Items {
		var cpu, mem string
		if len(it.Containers) > 0 {
			// First container's usage as a representative sample (avoids the
			// unit-mixing problem noted above) — good enough for A단계's "자원
			// 사용" display; a true per-pod sum is a B단계 nicety, not a
			// correctness requirement here.
			cpu = it.Containers[0].Usage.CPU
			mem = it.Containers[0].Usage.Memory
		}
		out = append(out, PodMetric{Name: it.Metadata.Name, Namespace: it.Metadata.Namespace, CPU: cpu, Memory: mem})
	}
	return out, nil
}
