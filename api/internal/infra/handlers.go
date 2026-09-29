// Package infra implements `/api/infra/*` — docs/design/90-infra-topology.md
// §7's A단계 (읽기 전용 토폴로지 뷰). Every handler in this file:
//
//   - is GET-only (registered as such in internal/api/server.go)
//   - is wired behind authn.Guard.PersonOnly (no machine principal reach —
//     this is a new console-only administrative surface; extending it to
//     org.read.* machine principals is a later decision, not assumed here)
//   - never calls anything but internal/k8sread's read-only methods
//   - creates, mutates or deletes NOTHING (IN4: "쓰기 엔드포인트는 1단계에
//     만들지 않는다")
//
// The k8s client can be nil (not running in-cluster, or the RBAC in
// deploy/osiris/templates/rbac-api.yaml has not been applied yet) — every
// handler checks that first and answers 503, matching the same
// degrade-not-crash discipline as graphread's nil-client handling elsewhere
// in this codebase (see cmd/osiris-api/main.go's typed-nil guards).
package infra

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"

	"osiris-api/internal/corewrite"
	"osiris-api/internal/k8sread"
)

type Service struct {
	k8s *k8sread.Client

	// [2026-09-11, C-1] docs/design/90-infra-topology.md §11 — the
	// propose/list/decide surface (proposals.go). core is osiris-core's
	// graph-write client (D17/G7: Neo4j writes never happen directly from
	// osiris-api) and nc is the plain core-NATS connection used ONLY to
	// publish approval.decision.<id> after a successful decision (IN19) —
	// see proposals.go's Decide handler for why this is the one and only
	// place in this codebase that publishes that subject.
	core *corewrite.Client
	nc   natsPublisher
}

// natsPublisher is the one method proposals.go needs from *nats.Conn —
// declared as a narrow interface so this file does not force every caller of
// NewService (including tests) to hold a real NATS connection.
type natsPublisher interface {
	Publish(subject string, data []byte) error
}

func NewService(k8s *k8sread.Client, core *corewrite.Client, nc natsPublisher) *Service {
	return &Service{k8s: k8s, core: core, nc: nc}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// unavailable reports (and answers) the nil-client case. Returns true when
// the caller must stop.
func (s *Service) unavailable(w http.ResponseWriter) bool {
	if s.k8s == nil {
		writeErr(w, http.StatusServiceUnavailable,
			"k8s read access not available (not running in-cluster, or the infra read-only RBAC from deploy/osiris/templates/rbac-api.yaml has not been applied to this cluster yet)")
		return true
	}
	return false
}

// Nodes -> GET /api/infra/nodes.
func (s *Service) Nodes(w http.ResponseWriter, r *http.Request) {
	if s.unavailable(w) {
		return
	}
	nodes, err := s.k8s.ListNodes()
	if err != nil {
		log.Printf("osiris-api: infra.Nodes: %v", err)
		writeErr(w, http.StatusBadGateway, "failed to list nodes")
		return
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

// Namespaces -> GET /api/infra/namespaces.
func (s *Service) Namespaces(w http.ResponseWriter, r *http.Request) {
	if s.unavailable(w) {
		return
	}
	nss, err := s.k8s.ListNamespaces()
	if err != nil {
		log.Printf("osiris-api: infra.Namespaces: %v", err)
		writeErr(w, http.StatusBadGateway, "failed to list namespaces")
		return
	}
	sort.Slice(nss, func(i, j int) bool { return nss[i].Name < nss[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"namespaces": nss})
}

// Pods -> GET /api/infra/pods. Status summary only — see k8sread.Pod's doc
// comment for exactly what is and is not projected.
func (s *Service) Pods(w http.ResponseWriter, r *http.Request) {
	if s.unavailable(w) {
		return
	}
	pods, err := s.k8s.ListAllPods()
	if err != nil {
		log.Printf("osiris-api: infra.Pods: %v", err)
		writeErr(w, http.StatusBadGateway, "failed to list pods")
		return
	}
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].Namespace != pods[j].Namespace {
			return pods[i].Namespace < pods[j].Namespace
		}
		return pods[i].Name < pods[j].Name
	})
	writeJSON(w, http.StatusOK, map[string]any{"pods": pods})
}

// Metrics -> GET /api/infra/metrics. Best-effort: metrics-server can be
// briefly unavailable (it has its own scrape interval) without that being an
// error for the rest of the topology view, so a metrics failure is reported
// INSIDE a 200 body (available:false) rather than failing the whole request.
func (s *Service) Metrics(w http.ResponseWriter, r *http.Request) {
	if s.unavailable(w) {
		return
	}
	type resp struct {
		Available bool                 `json:"available"`
		Error     string               `json:"error,omitempty"`
		Nodes     []k8sread.NodeMetric `json:"nodes,omitempty"`
		Pods      []k8sread.PodMetric  `json:"pods,omitempty"`
	}
	nodeM, nErr := s.k8s.NodeMetrics()
	podM, pErr := s.k8s.PodMetrics()
	if nErr != nil || pErr != nil {
		msg := ""
		if nErr != nil {
			msg += "nodes: " + nErr.Error() + "; "
		}
		if pErr != nil {
			msg += "pods: " + pErr.Error()
		}
		log.Printf("osiris-api: infra.Metrics: %s", msg)
		writeJSON(w, http.StatusOK, resp{Available: false, Error: strings.TrimSpace(msg)})
		return
	}
	writeJSON(w, http.StatusOK, resp{Available: true, Nodes: nodeM, Pods: podM})
}

// ─────────────────────────────────────────────────────────────────────────
// Topology — the A단계 hierarchy view (§7: "노드 → 네임스페이스 → 워크로드
// → 파드 계층").
//
// ★ HONESTY NOTE (repo discipline — "없는 것을 있는 척하지 마라"): this
// build groups pods into "workloads" using the `app.kubernetes.io/component`
// label (this chart's own, consistently-applied convention — see e.g.
// rbac-operator.yaml, api-deployment.yaml) with a fallback to the pod's
// immediate ownerReference (ReplicaSet/StatefulSet/DaemonSet name), NOT by
// reading Deployment/StatefulSet objects. That is a deliberate consequence
// of §4.3(b): Deployment/StatefulSet READ is scoped to `infra-*` namespace
// Roles only, and none exist yet (§8 blocker note, IN13/IN14 open) — so for
// osiris-system/osiris-agents/ingress-nginx, this process has no permission
// to list Deployments at all, cluster-wide or otherwise. Once an infra-*
// namespace exists and its Role is bound, ListNodes/ListNamespaces/ListAllPods
// stay as-is and a namespace-scoped workload lister (using that Role) can be
// added alongside this label-based grouping — this function's structure
// already keeps "namespace" and "workload-within-namespace" as separate
// levels for exactly that extension.
// ─────────────────────────────────────────────────────────────────────────

type topologyWorkload struct {
	Name string        `json:"name"` // component label, or "<ownerKind>/<ownerName>", or "(ungrouped)"
	Pods []k8sread.Pod `json:"pods"`
}

type topologyNamespace struct {
	Name      string             `json:"name"`
	Phase     string             `json:"phase"`
	IsInfra   bool               `json:"isInfra"` // true iff name has the "infra-" prefix (none exist yet — see note above)
	Workloads []topologyWorkload `json:"workloads"`
}

type topologyResponse struct {
	Nodes      []k8sread.Node      `json:"nodes"`
	Namespaces []topologyNamespace `json:"namespaces"`
}

// Topology -> GET /api/infra/topology.
func (s *Service) Topology(w http.ResponseWriter, r *http.Request) {
	if s.unavailable(w) {
		return
	}
	nodes, err := s.k8s.ListNodes()
	if err != nil {
		log.Printf("osiris-api: infra.Topology: nodes: %v", err)
		writeErr(w, http.StatusBadGateway, "failed to list nodes")
		return
	}
	nss, err := s.k8s.ListNamespaces()
	if err != nil {
		log.Printf("osiris-api: infra.Topology: namespaces: %v", err)
		writeErr(w, http.StatusBadGateway, "failed to list namespaces")
		return
	}
	pods, err := s.k8s.ListAllPods()
	if err != nil {
		log.Printf("osiris-api: infra.Topology: pods: %v", err)
		writeErr(w, http.StatusBadGateway, "failed to list pods")
		return
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })

	// namespace -> workload key -> pods
	byNS := map[string]map[string][]k8sread.Pod{}
	for _, p := range pods {
		key := workloadKey(p)
		if byNS[p.Namespace] == nil {
			byNS[p.Namespace] = map[string][]k8sread.Pod{}
		}
		byNS[p.Namespace][key] = append(byNS[p.Namespace][key], p)
	}

	out := make([]topologyNamespace, 0, len(nss))
	for _, ns := range nss {
		workloadsMap := byNS[ns.Name]
		workloads := make([]topologyWorkload, 0, len(workloadsMap))
		for k, ps := range workloadsMap {
			sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
			workloads = append(workloads, topologyWorkload{Name: k, Pods: ps})
		}
		sort.Slice(workloads, func(i, j int) bool { return workloads[i].Name < workloads[j].Name })
		out = append(out, topologyNamespace{
			Name:      ns.Name,
			Phase:     ns.Phase,
			IsInfra:   strings.HasPrefix(ns.Name, "infra-"),
			Workloads: workloads,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	writeJSON(w, http.StatusOK, topologyResponse{Nodes: nodes, Namespaces: out})
}

func workloadKey(p k8sread.Pod) string {
	if p.Component != "" {
		return p.Component
	}
	if p.OwnerKind != "" && p.OwnerName != "" {
		return p.OwnerKind + "/" + p.OwnerName
	}
	return "(ungrouped)"
}
