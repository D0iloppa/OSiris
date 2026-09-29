// Package operator is Phase D-1's answer to the gap driver/kube's package
// doc explicitly calls out: "This driver never creates, patches, or deletes
// a Pod, PVC, ServiceAccount, or NetworkPolicy directly — those four are the
// OPERATOR's job ..., which is NOT part of this phase and is not
// implemented here." This package IS that operator.
//
// ── DESIGN DEVIATION FROM 70 §5.1, RECORDED HERE ON PURPOSE ───────────────
//
// docs/design/70-kubernetes-deployment.md §5.1 says the Agent CRD operator
// is "controller-runtime 기반" (controller-runtime based). This package
// deliberately does NOT use controller-runtime. It uses client-go directly:
// a dynamic.Interface, a dynamicinformer-driven SharedIndexInformer, and a
// plain workqueue.RateLimitingInterface. Rationale (supervisor's call, not
// silently substituted):
//
//   - internal/driver/kube already talks to the API server exclusively
//     through a dynamic.Interface (see driver/kube/driver.go's package
//     doc: "Dynamic ... so this package has exactly one k8s dependency
//     shape"). Adding controller-runtime alongside it would give this repo
//     TWO different k8s client abstractions for the same CRD — one
//     hand-rolled (driver/kube, already reviewed and tested), one a large
//     new framework (controller-runtime: manager, cache, webhook
//     scaffolding, its own logging/metrics conventions) — for a
//     reconciliation loop that is, in Simplicity First terms, small: watch
//     one namespaced CR, materialize four objects, write a status. A
//     controller-runtime Manager buys leader election and multi-controller
//     wiring this single-controller, single-namespace operator does not
//     need yet (osiris-orchestrator already does its own bookkeeping via
//     the Lease resource RBAC grants — see rbac-operator.yaml — without
//     controller-runtime's LeaderElector, should that ever be wired up).
//   - This is reported, not buried: 70 §5.1's literal sentence is quoted
//     above, and this comment is the record that Phase D-1 intentionally
//     went a different way. The document itself is not edited by this
//     phase (that is the supervisor's call, per the work order).
package operator

import (
	"context"
	"fmt"
	"log"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// GVRs this operator touches. Four workload objects (K5's Pod/PVC/SA +
// this package's own Secret for rendered config — see objects.go's doc
// comment on why config uses Secret, not ConfigMap) plus the Agent CR
// itself, whose status subresource this operator is the intended writer of
// (agent.yaml: "status ... 드라이버/supervisor만 쓴다" — this operator IS
// that writer for the materialization half; the credentials Secret and
// SyncCredentials remain driver/kube's, per that package's doc comment).
var (
	agentGVR = schema.GroupVersionResource{Group: "osiris.dev", Version: "v1alpha1", Resource: "agents"}
	podGVR   = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	pvcGVR   = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumeclaims"}
	saGVR    = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}
	secretGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
)

// Operator watches Agent CRs in exactly one namespace (K10 — namespaced by
// construction, never a ClusterRole/cluster-wide watch) and materializes the
// workload objects K5 lists, plus writes status.
type Operator struct {
	dyn             dynamic.Interface
	namespace       string // osiris-agents
	systemNamespace string // osiris-system — for building the cross-namespace Service FQDNs the agent's env needs (see objects.go)

	queue workqueue.TypedRateLimitingInterface[string]
}

// New constructs an Operator. Like driver/kube.NewDriver, this takes a
// ready-made dynamic.Interface rather than building its own rest.Config —
// main.go remains the single approved wiring point for in-cluster vs
// kubeconfig discovery (bin/lint-driver-boundary.sh's boundary is about
// internal/driver/*, which this package is not, but the same "main.go
// decides transport" discipline is kept anyway for consistency).
func New(dyn dynamic.Interface, namespace, systemNamespace string) *Operator {
	return &Operator{
		dyn:             dyn,
		namespace:       namespace,
		systemNamespace: systemNamespace,
		queue: workqueue.NewTypedRateLimitingQueue[string](
			workqueue.DefaultTypedControllerRateLimiter[string](),
		),
	}
}

func (o *Operator) agents() dynamic.ResourceInterface { return o.dyn.Resource(agentGVR).Namespace(o.namespace) }
func (o *Operator) pods() dynamic.ResourceInterface    { return o.dyn.Resource(podGVR).Namespace(o.namespace) }
func (o *Operator) pvcs() dynamic.ResourceInterface    { return o.dyn.Resource(pvcGVR).Namespace(o.namespace) }
func (o *Operator) sas() dynamic.ResourceInterface     { return o.dyn.Resource(saGVR).Namespace(o.namespace) }
func (o *Operator) secrets() dynamic.ResourceInterface  { return o.dyn.Resource(secretGVR).Namespace(o.namespace) }

// Run starts the informer and `workers` worker goroutines, and blocks until
// ctx is cancelled. GC of a deleted Agent's children is NOT something this
// loop does — it never runs on the Delete path at all, because K10's whole
// point is that ownerReferences let the API server's own garbage collector
// cascade the delete (this operator only ever needs to set the reference
// once, at create time — see objects.go's ownerRef helper).
//
// ── D-2 JUDGMENT CALL: NO FINALIZER, RECORDED HERE ────────────────────────
//
// The work order for this phase raises finalizers explicitly: without one,
// `kubectl delete agent` removes the CR (and, via cascade GC, its Pod)
// immediately, giving the outgoing supervisor process no grace period at
// all — a real regression for a mid-task drain. A finalizer would let this
// operator intercept the delete, wait, then remove the finalizer itself.
// But a finalizer is also a standing deadlock risk: if this operator is
// down (crashed, rolled out, OOMKilled) when someone deletes an Agent, the
// CR sits forever in Terminating with no process left to strip the
// finalizer, and nothing in K8s itself will ever do it for us.
//
// DECISION: no finalizer. Reason it is not needed, not just "not worth the
// risk": the SAME mechanism this phase already had to build for the OTHER
// Recreate trigger (a SpecHash change while the Agent is NOT being deleted,
// see objects.go's ensurePod) already gives every Pod this operator creates
// a spec.terminationGracePeriodSeconds equal to spec.drainSeconds. That
// field is not operator-loop logic — it is data on the Pod object itself,
// read and enforced by (a) this operator's own ensurePod Delete call, AND
// (b) the API server's cascade GC when the owning Agent CR is deleted,
// with ZERO participation from this operator's process being alive at the
// time. So the grace period the work order is worried about losing is
// already present on the object the GC is about to delete, whether or not
// this controller is running. A finalizer would only buy the ability to
// react to the deletion request BEFORE the grace period starts (e.g. to
// flip status.phase to "Draining" one last time) — status on an object
// that is seconds away from not existing, which is not a guarantee worth
// the deadlock exposure. Verified empirically, not just argued: see this
// phase's report for a `kubectl delete agent` timing run showing Pod/PVC/
// SA/Secret all gone within drainSeconds, no finalizer, no hang.
func (o *Operator) Run(ctx context.Context, workers int) error {
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		o.dyn, 15*time.Second, o.namespace, nil,
	)
	informer := factory.ForResource(agentGVR).Informer()

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) { o.enqueue(obj) },
		UpdateFunc: func(oldObj, newObj interface{}) { o.enqueue(newObj) },
		// No DeleteFunc: a deleted Agent has nothing left for this operator
		// to do (see doc comment above) — enqueuing it would just produce a
		// syncAgent call that immediately no-ops on apierrors.IsNotFound.
	})
	if err != nil {
		return fmt.Errorf("add agent informer handler: %w", err)
	}

	go factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("agent operator: cache sync failed")
	}
	log.Printf("osiris-orchestrator: agent operator informer synced (namespace=%s)", o.namespace)

	for i := 0; i < workers; i++ {
		go o.runWorker(ctx)
	}

	<-ctx.Done()
	o.queue.ShutDown()
	return nil
}

func (o *Operator) enqueue(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Printf("osiris-orchestrator: agent operator: enqueue: %v", err)
		return
	}
	o.queue.Add(key)
}

func (o *Operator) runWorker(ctx context.Context) {
	for o.processNextItem(ctx) {
	}
}

func (o *Operator) processNextItem(ctx context.Context) bool {
	key, shutdown := o.queue.Get()
	if shutdown {
		return false
	}
	defer o.queue.Done(key)

	if err := o.syncAgent(ctx, key); err != nil {
		log.Printf("osiris-orchestrator: agent operator: sync %s: %v (requeueing)", key, err)
		o.queue.AddRateLimited(key)
		return true
	}
	o.queue.Forget(key)
	return true
}

// syncAgent is the whole reconcile function. It is deliberately NOT a diff
// engine: this phase's mandate (work order §"이번 phase의 범위") is
// materialize-once + GC, not the specHash/configHash-triggered
// Recreate/ReloadConfig state machine (that is Phase D-2, and this function
// does not implement it — see the note on status.observedSpecHash below).
func (o *Operator) syncAgent(ctx context.Context, key string) error {
	_, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return fmt.Errorf("split key %s: %w", key, err)
	}

	agent, err := o.agents().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil // deleted — nothing for this operator to do, see Run's doc comment
		}
		return fmt.Errorf("get agent %s: %w", name, err)
	}

	spec, err := parseSpec(agent)
	if err != nil {
		return fmt.Errorf("agent %s: %w", name, err)
	}
	owner := ownerRef(agent)

	if spec.desiredState != "Suspended" {
		if err := o.ensureServiceAccount(ctx, owner, spec); err != nil {
			return err
		}
		if err := o.ensurePVC(ctx, owner, spec); err != nil {
			return err
		}
		if err := o.ensureConfigSecret(ctx, owner, spec); err != nil {
			return err
		}
		if err := o.ensurePod(ctx, owner, spec); err != nil {
			return err
		}
	}

	requeue, err := o.updateStatus(ctx, agent, spec)
	if err != nil {
		return err
	}
	if requeue {
		// Status hasn't converged yet (e.g. Pod still Pending/Booting) —
		// come back shortly rather than waiting for the informer's full
		// resync period, so `kubectl get agent` reflects reality quickly.
		o.queue.AddAfter(key, 3*time.Second)
	}
	return nil
}

// ownerRef builds the ownerReference every materialized object carries.
// Same shape as driver/kube.ownerReferenceIfAgentExists, duplicated rather
// than imported: this package must not import internal/driver/kube (that
// would cross driver/kube's own package boundary the wrong direction — it
// is a WorkloadDriver implementation, not a library this controller should
// depend on), and the shape is four fields, not worth a shared package.
func ownerRef(agent *unstructured.Unstructured) metav1.OwnerReference {
	blockDeletion := true
	controller := true
	return metav1.OwnerReference{
		APIVersion:         "osiris.dev/v1alpha1",
		Kind:               "Agent",
		Name:               agent.GetName(),
		UID:                agent.GetUID(),
		BlockOwnerDeletion: &blockDeletion,
		Controller:         &controller,
	}
}
