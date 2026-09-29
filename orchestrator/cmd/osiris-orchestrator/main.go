// osiris-orchestrator — the [1단계] convergence loop and DockerDriver
// (docs/design/10-core-architecture.md §1.4, §6.2, D3/D4/A11).
//
// Deliberately absent from this binary (A4, D12, mirroring osiris-core):
// no `claude` binary, no LLM gateway credentials, no code path that calls an
// LLM. It holds /var/run/docker.sock (host-root-equivalent, §1.4) and
// nothing else this trusted — it does not attach to osiris-agents (agents
// cannot reach it) and it is the only component in the compose file allowed
// to import a Docker-specific type (that boundary lives in
// internal/driver/docker, G3).
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"osiris-orchestrator/internal/config"
	"osiris-orchestrator/internal/driver/docker"
	"osiris-orchestrator/internal/driver/kube"
	"osiris-orchestrator/internal/genesisboot"
	"osiris-orchestrator/internal/graphdb"
	"osiris-orchestrator/internal/httpapi"
	"osiris-orchestrator/internal/jetstream"
	"osiris-orchestrator/internal/natsauth"
	"osiris-orchestrator/internal/operator"
	"osiris-orchestrator/internal/reconcile"
	"osiris-orchestrator/internal/workload"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(runHealthcheck())
	}
	if err := run(); err != nil {
		log.Fatalf("osiris-orchestrator: fatal: %v", err)
	}
}

// runHealthcheck mirrors osiris-core's self-healthcheck trick (the runtime
// image has no shell/curl — see Dockerfile) so Docker HEALTHCHECK has an
// executable to run without needing curl/wget baked into the image.
func runHealthcheck() int {
	addr := os.Getenv("HEALTH_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1" + addr + "/livez")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("osiris-orchestrator: connecting to neo4j at %s (read-only)", cfg.Neo4jURI)
	db, err := connectNeo4jWithRetry(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close(context.Background())

	// ── driver selection (Phase D-1, config.Config.WorkloadDriver's doc
	// comment) — "docker" (default, compose path, byte-for-byte the same as
	// before this phase) or "kube" (driver/kube + internal/operator, wired
	// below). This is the ONE branch point; reconcile.NewLoop below still
	// only ever sees workload.WorkloadDriver (G3/D3) — see
	// bin/lint-driver-boundary.sh's comment on why this file, and only this
	// file, is allowed to import both internal/driver/docker and
	// internal/driver/kube.
	var driver workload.WorkloadDriver
	var natsSignaler natsauth.Signaler
	var genesisDrv genesisboot.ContainerDriver

	switch cfg.WorkloadDriver {
	case "kube":
		dyn, err := buildDynamicClient()
		if err != nil {
			return fmt.Errorf("kube driver: %w", err)
		}
		log.Printf("osiris-orchestrator: workload driver = kube (namespace=%s)", cfg.AgentsNamespace)
		kubeDriver := kube.NewDriver(dyn, cfg.AgentsNamespace)
		driver = kubeDriver

		// The Agent-CR-materializing operator (internal/operator) — see that
		// package's doc comment for the controller-runtime deviation this
		// phase records. It runs as its own goroutine below, independent of
		// the reconcile loop's tick (it has its own informer+workqueue).
		agentOperator := operator.New(dyn, cfg.AgentsNamespace, cfg.SystemNamespace)
		go func() {
			log.Printf("osiris-orchestrator: agent operator starting (namespace=%s)", cfg.AgentsNamespace)
			if err := agentOperator.Run(ctx, 2); err != nil {
				log.Printf("osiris-orchestrator: agent operator stopped: %v", err)
			}
		}()

		// natsauth's Signaler (SIGHUP-reload-by-container-name) and the
		// bootstrap-master genesis container flow are BOTH docker-container
		// concepts with no kube equivalent built in this phase (bootstrap-
		// master's k8s realization and the NATS config-reload path are each
		// their own, separate pieces of work — not this phase's scope, and
		// explicitly NOT touched here per the work order's NATS-ACL
		// exclusion). Stubs make main.go compile and fail LOUD, not
		// silently, if either path is ever actually exercised under
		// WORKLOAD_DRIVER=kube — see unsupportedUnderKube below.
		natsSignaler = unsupportedSignaler{}
		genesisDrv = unsupportedGenesisDriver{}
	default:
		dockerCli := docker.NewClient(cfg.DockerSocketPath)
		dockerDriver := docker.NewDriver(dockerCli, cfg.HostProjectDir, cfg.StateDir)
		driver = dockerDriver
		natsSignaler = dockerDriver
		genesisDrv = genesisDriver{dockerDriver}
	}

	natsGen := natsauth.NewGenerator(cfg.NatsAuthConfPath, cfg.NatsContainerName, cfg.NatsCorePassword, cfg.NatsOrchestratorPassword, cfg.NatsAPIPassword, natsSignaler)

	// BEFORE connecting: make sure this process's own NATS user is allowed to
	// administer JetStream. See natsauth.EnsureBootstrap for why the order is
	// load-bearing (a denied _INBOX subscription poisons the connection's whole
	// request/reply path for its lifetime, so fixing the ACL afterwards does not
	// help). No-op on a system already carrying the right grants.
	if err := natsGen.EnsureBootstrap(ctx); err != nil {
		log.Printf("osiris-orchestrator: nats authorization bootstrap: %v", err)
	}

	log.Printf("osiris-orchestrator: connecting to nats")
	nc, err := connectNatsWithRetry(cfg.NatsURL)
	if err != nil {
		return err
	}
	defer nc.Close()

	// JetStream provisioner: §7.2's streams and every per-agent durable pull
	// consumer. Kept out of the WorkloadDriver boundary on purpose — NATS is
	// identical in both deployment stages, so there is nothing here for a
	// [2단계] driver to substitute (G3 concerns Docker types, not the bus).
	jsp, err := jetstream.New(nc)
	if err != nil {
		return err
	}

	loop := reconcile.NewLoop(cfg, db, driver, natsGen, jsp, nc)

	// ── [2026-07-28] bootstrap-master bring-up (10 §8.5 steps 2-7, 30 §1.0.7) ──
	//
	// CONSTRUCTING THIS CREATES NOTHING. The manager starts idle and stays idle
	// until osiris-api posts to /bootstrap-master after onboarding step 0 passes —
	// the same "wiring a store is not issuing a credential" discipline
	// osiris-core applies to its genesis store and osiris-api to its machine
	// principals.
	//
	// The env below is intentionally minimal: 30 §1.3 gives bootstrap-master no
	// team, no inbox and no NATS user, so it gets the two hostnames it may know
	// (§1.2 invariant 10) and nothing else. The genesis token is NOT here — it
	// arrives as a file, which is the whole point of §8.5 steps 6-7 (G5).
	genesisMgr := genesisboot.New(
		genesisDrv,
		genesisboot.NewCoreIssuer(cfg.CoreBaseURL, cfg.CoreAPIToken),
		cfg.BootstrapMasterImage,
		[]string{
			"OSIRIS_EMPLOYEE_ID=bootstrap-master",
			"OSIRIS_CORE_URL=" + cfg.CoreBaseURL,
			"OSIRIS_EGRESS_PROXY=http://osiris-egress:3128",
			"OSIRIS_NO_PROXY=osiris-core,osiris-egress,nats,localhost,127.0.0.1",
			"OSIRIS_GENESIS=true",
		},
		log.Printf,
	)
	if cfg.CoreAPIToken == "" {
		log.Printf("osiris-orchestrator: WARNING: CORE_API_TOKEN is not set — bootstrap-master bring-up will refuse (fail-closed, §8.5)")
	}
	log.Printf("osiris-orchestrator: bootstrap-master manager wired (image %s; nothing is created until POST /bootstrap-master)", cfg.BootstrapMasterImage)

	// /status must report BOTH the reconcile snapshot and the bootstrap-master
	// phase, because the POST /bootstrap-master response tells its caller that
	// progress is published there. A 202 that points at a field which does not
	// exist is a contract this code would be breaking on its own behalf.
	health := httpapi.NewHealthState(statusSnapshot{loop: loop, boot: genesisMgr})
	go health.RunProber(ctx, 10*time.Second, db.Ping, func() error {
		if !nc.IsConnected() {
			return nats.ErrConnectionClosed
		}
		return nil
	})

	mux := health.Mux()
	httpapi.RegisterBootstrapMaster(mux, genesisMgr, cfg.CoreAPIToken, log.Printf)

	healthSrv := &http.Server{Addr: cfg.HealthAddr, Handler: mux}
	errCh := make(chan error, 2)
	go func() {
		log.Printf("osiris-orchestrator: health/status server listening on %s (/livez /readyz /status)", cfg.HealthAddr)
		if err := healthSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	go func() {
		log.Printf("osiris-orchestrator: reconcile loop starting (interval=%s, maxResidentAgents=%d, catalogVersion=%s)", cfg.ReconcileInterval, cfg.MaxResidentAgents, cfg.CatalogVersion)
		if err := loop.Run(ctx); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Printf("osiris-orchestrator: shutting down")
	case err := <-errCh:
		log.Printf("osiris-orchestrator: fatal component error: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = healthSrv.Shutdown(shutdownCtx)
	return nil
}

func connectNeo4jWithRetry(ctx context.Context, cfg config.Config) (*graphdb.Client, error) {
	var lastErr error
	for i := 0; i < 30; i++ {
		db, err := graphdb.NewClient(ctx, cfg.Neo4jURI, cfg.Neo4jUser, cfg.Neo4jPassword)
		if err == nil {
			return db, nil
		}
		lastErr = err
		log.Printf("osiris-orchestrator: neo4j not ready yet (%v), retrying in 2s", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, lastErr
}

func connectNatsWithRetry(url string) (*nats.Conn, error) {
	var lastErr error
	for i := 0; i < 30; i++ {
		nc, err := nats.Connect(url,
			nats.Name("osiris-orchestrator"),
			nats.MaxReconnects(-1),
			nats.ReconnectWait(2*time.Second),
		)
		if err == nil {
			return nc, nil
		}
		lastErr = err
		log.Printf("osiris-orchestrator: nats not ready yet (%v), retrying in 2s", err)
		time.Sleep(2 * time.Second)
	}
	return nil, lastErr
}

// buildDynamicClient is the ONE place (this file — see this package's own
// doc comment and bin/lint-driver-boundary.sh) that decides in-cluster vs
// kubeconfig discovery for the kube driver + operator, mirroring
// driver/kube.NewDriver's doc comment ("main.go ... owns in-cluster vs
// kubeconfig discovery"). In-cluster is the real deployment path
// (orchestrator-deployment.yaml runs this binary as a Pod with the
// osiris-orchestrator ServiceAccount, rbac-operator.yaml); the KUBECONFIG
// fallback exists only so this binary is not impossible to exercise
// out-of-cluster during development.
func buildDynamicClient() (dynamic.Interface, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			return nil, fmt.Errorf("not running in-cluster and KUBECONFIG is not set: %w", err)
		}
		restCfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("build kubeconfig-based rest.Config from %s: %w", kubeconfig, err)
		}
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	return dyn, nil
}

// unsupportedSignaler/unsupportedGenesisDriver are WORKLOAD_DRIVER=kube's
// stand-ins for the two docker-specific paths this phase does not port (see
// the doc comment at their call site in run()). Both fail loud rather than
// being silently unreachable, so a future caller that DOES exercise either
// path gets a clear "not implemented under kube" instead of a nil-pointer
// panic or, worse, a call that quietly does nothing.
type unsupportedSignaler struct{}

func (unsupportedSignaler) KillContainerByExactName(ctx context.Context, name, signal string) error {
	return fmt.Errorf("WORKLOAD_DRIVER=kube: nats container signaling (%s -> %s) is not implemented — this is the NATS config-reload path, out of this phase's scope", name, signal)
}

type unsupportedGenesisDriver struct{}

func (unsupportedGenesisDriver) CreateGenesisContainer(ctx context.Context, spec genesisboot.CreateSpec) (string, error) {
	return "", fmt.Errorf("WORKLOAD_DRIVER=kube: bootstrap-master genesis container is not implemented — out of this phase's scope (Phase D-1 is Agent CR materialization only)")
}
func (unsupportedGenesisDriver) WriteGenesisToken(token string) error {
	return fmt.Errorf("WORKLOAD_DRIVER=kube: bootstrap-master genesis container is not implemented")
}
func (unsupportedGenesisDriver) StartGenesisContainer(ctx context.Context, id string) error {
	return fmt.Errorf("WORKLOAD_DRIVER=kube: bootstrap-master genesis container is not implemented")
}
func (unsupportedGenesisDriver) TeardownGenesisContainer(ctx context.Context, waitSeconds int) error {
	return fmt.Errorf("WORKLOAD_DRIVER=kube: bootstrap-master genesis container is not implemented")
}

// genesisDriver adapts *docker.Driver to genesisboot.ContainerDriver.
//
// THE ADAPTER EXISTS SO genesisboot IMPORTS NO DOCKER TYPES. That keeps the
// ordering guarantees of §8.5 testable against a fake with no Docker types in
// sight, which matters because the live happy path is unreachable on any
// deployment past revision 0 (W1) — the fake is the only place the sequence can
// be asserted end to end.
type genesisDriver struct{ d *docker.Driver }

func (g genesisDriver) CreateGenesisContainer(ctx context.Context, spec genesisboot.CreateSpec) (string, error) {
	return g.d.CreateGenesisContainer(ctx, docker.GenesisSpec{Image: spec.Image, Env: spec.Env})
}
func (g genesisDriver) WriteGenesisToken(token string) error { return g.d.WriteGenesisToken(token) }
func (g genesisDriver) StartGenesisContainer(ctx context.Context, id string) error {
	return g.d.StartGenesisContainer(ctx, id)
}
func (g genesisDriver) TeardownGenesisContainer(ctx context.Context, waitSeconds int) error {
	return g.d.TeardownGenesisContainer(ctx, waitSeconds)
}

// statusSnapshot merges the reconcile loop's snapshot with the bootstrap-master
// phase for GET /status.
//
// A MAP, NOT A STRUCT, deliberately: the reconcile Snapshot type is owned by
// internal/reconcile and this composition must not force a change there every
// time the orchestrator gains another reportable subsystem. The cost is that the
// key names live here — which is why the bootstrapMaster key is named in the
// POST /bootstrap-master response too, so the two cannot drift silently.
type statusSnapshot struct {
	loop *reconcile.Loop
	boot *genesisboot.Manager
}

func (s statusSnapshot) Snapshot() any {
	return map[string]any{
		"reconcile":       s.loop.Snapshot(),
		"bootstrapMaster": s.boot.Snapshot(),
	}
}
