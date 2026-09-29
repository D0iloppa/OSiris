// osiris-api — console REST/SSE surface (docs/design/10-core-architecture.md
// §3.2). This build implements the onboarding step0 endpoint contract
// (docs/design/30-onboarding-provisioning.md §1.0.8), GET /onboarding/status,
// and the D19/D20 SSE resume mechanism on GET /api/events. Deliberately out
// of scope (see docs/design/IMPLEMENTATION-NOTES.md): the assignment engine,
// org-seed/conversational (1단계) onboarding, and the console SPA.
//
// No claude binary, no LLM gateway credentials anywhere in this image (A4) —
// this service never talks to an LLM. It DOES need a Node runtime to invoke
// omniroute/operations.mjs as a subprocess (internal/omniroute) — see that
// package's doc comment for why that decision was made over reimplementing
// the omniroute wire protocol in Go, and api/Dockerfile for how the image
// stays otherwise minimal (no npm registry access at runtime, non-root,
// no docker.sock — G8).
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

	"osiris-api/internal/api"
	"osiris-api/internal/authn"
	"osiris-api/internal/authncred"
	"osiris-api/internal/authz"
	"osiris-api/internal/config"
	"osiris-api/internal/corewrite"
	"osiris-api/internal/enrollment"
	"osiris-api/internal/events"
	"osiris-api/internal/graphread"
	"osiris-api/internal/health"
	"osiris-api/internal/infra"
	"osiris-api/internal/k8sread"
	"osiris-api/internal/machineauth"
	"osiris-api/internal/masterkey"
	"osiris-api/internal/omniroute"
	"osiris-api/internal/onboarding"
	"osiris-api/internal/orgstate"
	"osiris-api/internal/people"
	"osiris-api/internal/providers"
	"osiris-api/internal/secevents"
	"osiris-api/internal/session"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(runHealthcheck())
	}
	if err := run(); err != nil {
		log.Fatalf("osiris-api: fatal: %v", err)
	}
}

// runHealthcheck mirrors osiris-core's self-healthcheck pattern exactly
// (core/cmd/osiris-core/main.go) — the Docker HEALTHCHECK instruction calls
// this instead of shelling out to curl/wget, which this image does not ship.
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

	log.Printf("osiris-api: connecting to neo4j at %s (read-only)", cfg.Neo4jURI)
	// Degraded start rather than fatal: osiris-api's onboarding step0 surface
	// does not need Neo4j at all (§1.0.5 — step0 writes nothing to the graph).
	// Only GET /onboarding/status's `org` section needs it, and that degrades
	// to zeros gracefully (see internal/onboarding.Service.orgSummary).
	// Failing the whole process over a read-only, non-essential dependency
	// would make step0 unusable before Neo4j is even relevant to it.
	//
	// But degraded must be RECOVERABLE, which it was not until 2026-09-11 —
	// the old code retried NewClient ten times and then left `graph` nil for
	// the life of the process, pinning /readyz at 503 forever even after
	// Neo4j came up (graphread.NewLazyClient's comment records the incident
	// in full). The lazy client re-dials on its own, so the boot probe below
	// only decides what the startup LOG says, never whether this process can
	// ever reach the graph again.
	graph, err := graphread.NewLazyClient(cfg.Neo4jURI, cfg.Neo4jUser, cfg.Neo4jPassword)
	if err != nil {
		// Only a malformed URI or unparseable auth config reaches here — no
		// amount of waiting fixes either, so this one IS fatal.
		return fmt.Errorf("neo4j driver: %w", err)
	}
	defer graph.Close(context.Background())
	if perr := probeNeo4jAtBoot(ctx, graph); perr != nil {
		log.Printf("osiris-api: WARNING: neo4j unreachable at boot (%v) — org summary reports zeros and /readyz stays 503 until it recovers on its own (no restart needed)", perr)
	} else {
		log.Printf("osiris-api: neo4j reachable")
	}

	// Deliberately not logging cfg.NatsURL itself — it embeds this process's
	// NATS password as userinfo (nats://osiris-api:<password>@nats:4222), and
	// a log line is exactly the kind of place a secret must never appear
	// (found and fixed here after noticing the same pattern already existed
	// in osiris-core's main.go — see IMPLEMENTATION-NOTES.md for that
	// separate, small fix).
	log.Printf("osiris-api: connecting to nats")
	nc, err := connectNatsWithRetry(cfg.NatsURL)
	if err != nil {
		return err
	}
	defer nc.Close()

	sseHub, err := events.NewHub(nc)
	if err != nil {
		return err
	}

	ops := omniroute.NewClient(cfg.OperationsScriptPath, cfg.NodeBinPath)
	ob := onboarding.NewService(ops, cfg.EnvFilePath, cfg.GatewayBaseURL, cfg.GatewayHealthPath, cfg.OrchestratorBaseURL, graph)
	ob.SetOmnirouteConsole(cfg.OmnirouteMode, cfg.OmnirouteUIURL)
	provSvc := providers.NewService(ops)

	// Browser session cookie (Task 1, docs/design/10-core-architecture.md
	// §8.4 + 31-onboarding-boundary.md §1.3 Q2b) — exchanges the setup token
	// for an opaque, HttpOnly session id exactly once per browser login. See
	// internal/session's package doc for the full Q2b walk-through and
	// internal/authn for why the raw Bearer token still also works
	// (non-browser callers).
	sessions := session.NewStore()

	// Machine principals (docs/design/60-identity-authn.md §19, design branch
	// (c) of IMPLEMENTATION-NOTES.md §102.2).
	//
	// ★ NOTHING HERE CREATES A MACHINE PRINCIPAL. Wiring the STORE is not
	// issuing a credential, and the difference is the whole of HI14 ① and of
	// §19.5's table: the default state is zero machine principals, and it stays
	// that way until a human calls POST /api/machine-principals at runtime.
	// "설치 시점에 굽는" a credential is the pattern that has recurred FOUR
	// times in this project; the lint bin/lint-identity-authn.sh checks that
	// osiris-up, docker-compose*.yml and bin/** contain no issuance call.
	//
	// ── [2026-07-27] The credential directory is REAL now. ───────────────
	//
	// The previous build wired orgstate.NoCredentialStore{} here — a named
	// type returning a MEASURED zero, placed at this line specifically so the
	// RBAC worker would have to replace it consciously rather than inherit it.
	// It has been replaced and DELETED. The H10 predicate's second conjunct
	// ("org.admin 보유자 중 자격증명 보유자 >= 1") is now the intersection of a
	// graph fact and a JetStream KV fact:
	//
	//	graph → who effectively holds org.admin and is active
	//	KV    → which of those ids have an AUTHN_CRED verifier
	//
	// Neither store learns about the other; the join happens in osiris-api,
	// the only component the design permits to know both (H1's one-way
	// reference). A failure on EITHER side is an error, never a zero — a zero
	// is what keeps the master-key window open and bootstrap machine
	// principals valid, so reading an outage as zero would resurrect a
	// bootstrap credential by unplugging a database.
	secPublisher := secevents.New(sseHub.JetStream())
	credStore := authncred.New(sseHub.JetStream())
	ticketStore := enrollment.New(sseHub.JetStream())
	permSource := authz.NewGraphPermissionSource(graphResolver(graph))
	credDir := orgstate.NewCredentialDirectory(holderSource(graph), credStore)
	orgEval := orgstate.New(graphReader(graph), credDir)

	// machineauth now receives the issuer-liveness reader, which turns on the
	// org class: §19.4's "발급자 :Person이 활성" was unevaluable in the
	// previous build (no people, no person sessions) and issuance returned
	// 501 rather than assuming the predicate true. It is evaluable now.
	// ── The master key (§4.0/H22, §4.5/H23) ─────────────────────────────────
	//
	// RESOLVED HERE, AFTER orgEval, AND THE ORDER IS LOAD-BEARING. Boot decides
	// whether to generate, delete or withhold the key by asking the H10
	// predicate, which needs BOTH the graph and the credential store — so the
	// key cannot be resolved at the top of run() where the previous
	// (setup-token) build resolved it. See masterkey.Boot's doc comment for why
	// it gates on the two-condition predicate rather than on the org revision
	// alone, and what lockout the revision-only reading causes.
	//
	// Every outcome gets its own sentence, because "no master key exists" is
	// routine after handover and alarming before it, and one generic line cannot
	// say both. The value is NEVER logged in any branch; osiris-up greps the
	// "generated" line to point the operator at bin/show-master-key.sh.
	keys, outcome, err := masterkey.Boot(ctx, cfg.MasterKeyPath, cfg.LegacySetupTokenPath, orgEval)
	if err != nil {
		return err
	}
	switch outcome {
	case masterkey.OutcomeGenerated:
		log.Printf("osiris-api: generated the OSiris master key, written to %s (value never logged — read it with bin/show-master-key.sh)", cfg.MasterKeyPath)
	case masterkey.OutcomeMigrated:
		log.Printf("osiris-api: generated the OSiris master key, written to %s, and deleted the superseded setup token at %s (H22: the old value has no osk_ prefix and can no longer be routed)", cfg.MasterKeyPath, cfg.LegacySetupTokenPath)
	case masterkey.OutcomeLoaded:
		log.Printf("osiris-api: loaded the existing OSiris master key from %s", cfg.MasterKeyPath)
	case masterkey.OutcomeWithheld:
		log.Printf("osiris-api: no master key exists and the H10 window is closed — none was generated (H23/§4.5). Administrators sign in with their own credentials; account recovery is the break-glass CLI, not a new master key")
	case masterkey.OutcomeUndeterminable:
		log.Printf("osiris-api: WARNING: could not evaluate the H10 window at boot, so no master key was generated or removed. Every master-key path will refuse until the graph and credential store are readable (fail-closed, §4.3)")
	}

	machines := machineauth.New(sseHub.JetStream(), secPublisher, orgEval, issuerLiveness(graph))
	log.Printf("osiris-api: machine principal store wired (bucket %s, bound lazily; zero principals exist until one is issued at runtime)", machineauth.BucketName)
	log.Printf("osiris-api: person credential + enrollment ticket stores wired (buckets %s, %s, bound lazily; zero records exist until a person enrolls)",
		authncred.BucketName, enrollment.BucketName)

	authGuard := authn.New(keys, sessions, machines, orgEval, permSource, credStore, ticketStore, graph, secPublisher)
	mpHandlers := machineauth.NewHandlers(machines)

	// Org role management (§20.6). The graph-write client is osiris-api's ONLY
	// write path into Neo4j (D17/G7/A5) — it holds no writing driver of its
	// own and never will. cfg.CoreBaseURL/CoreToken have existed unused since
	// the first build with a doc comment naming this exact arrival.
	coreClient := corewrite.New(cfg.CoreBaseURL, cfg.CoreToken)
	if !coreClient.Configured() {
		log.Printf("osiris-api: WARNING: CORE_API_TOKEN is not set — org role management will answer 503 (read-only surfaces are unaffected)")
	}
	peopleSvc := people.NewService(graph, coreClient, permSource, ticketStore, credStore, sessions, credDir, secPublisher)

	healthState := health.New()
	go healthState.RunProber(ctx, 10*time.Second, pingNeo4j(graph), func() error {
		if !nc.IsConnected() {
			return errNotConnected
		}
		return nil
	})

	// docs/design/90-infra-topology.md §4.3 — osiris-api's read-only k8s
	// access for the Infra topology view (A단계). Degrades to a nil client
	// (every internal/infra handler then answers 503, never panics) when
	// this process is not running in a pod, or when
	// deploy/osiris/templates/rbac-api.yaml's read-only ClusterRole has not
	// been applied yet — same discipline as the graphread/orgstate typed-nil
	// guards above.
	k8sClient, k8sErr := k8sread.NewInClusterClient()
	if k8sErr != nil {
		log.Printf("osiris-api: infra topology k8s read client not available (%v) — /api/infra/* will answer 503 until this is fixed", k8sErr)
	} else {
		log.Printf("osiris-api: infra topology k8s read client wired (in-cluster, read-only — nodes/namespaces/pods/metrics.k8s.io only, no secrets, IN4)")
	}
	// [2026-09-11, C-1] docs/design/90-infra-topology.md §11 — infra.Service
	// also gets osiris-core's graph-write client (proposals/decisions,
	// D17/G7) and the plain core-NATS connection (IN19's
	// approval.decision.<id> publish, Decide handler only) — the same
	// coreClient and nc already constructed above, reused rather than
	// duplicated.
	infraSvc := infra.NewService(k8sClient, coreClient, nc)

	server := api.NewServer(healthState, sseHub, ob, authGuard, mpHandlers, peopleSvc, infraSvc, provSvc)

	healthSrv := &http.Server{Addr: cfg.HealthAddr, Handler: server.HealthMux()}
	apiSrv := &http.Server{Addr: cfg.APIAddr, Handler: server.APIMux()}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("osiris-api: health server listening on %s (/livez /readyz)", cfg.HealthAddr)
		if err := healthSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	go func() {
		log.Printf("osiris-api: console API listening on %s (/api/onboarding/*, /api/events)", cfg.APIAddr)
		if err := apiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Printf("osiris-api: shutting down")
	case err := <-errCh:
		log.Printf("osiris-api: server error: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = healthSrv.Shutdown(shutdownCtx)
	_ = apiSrv.Shutdown(shutdownCtx)
	return nil
}

var errNotConnected = &simpleErr{"nats not connected"}

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }

// graphReader adapts a possibly-nil *graphread.Client to orgstate's interface.
//
// A typed nil pointer stored in an interface is NOT a nil interface, and that
// distinction decides an authentication outcome here: orgstate.New treats a
// nil RevisionReader as "undeterminable" and denies, which is correct, but a
// non-nil interface wrapping a nil *Client would panic on the first machine
// principal request instead. osiris-api deliberately starts degraded when
// Neo4j is unreachable at boot (see connectNeo4jWithRetry's caller), so this
// case is reachable in normal operation, not just in theory.
func graphReader(graph *graphread.Client) orgstate.RevisionReader {
	if graph == nil {
		return nil
	}
	return graph
}

// graphResolver / holderSource / issuerLiveness are the same typed-nil guard as
// graphReader above, one per interface.
//
// A typed nil pointer stored in an interface is NOT a nil interface, and every
// one of these decides an authentication or authorization outcome: the
// consumers check `== nil` and fail closed, which is correct, but a non-nil
// interface wrapping a nil *Client would panic on first use instead. osiris-api
// deliberately starts degraded when Neo4j is unreachable at boot, so this is a
// reachable state in normal operation, not a theoretical one.

func graphResolver(graph *graphread.Client) authz.EffectiveReader {
	if graph == nil {
		return nil
	}
	return graph
}

func holderSource(graph *graphread.Client) orgstate.PermissionHolders {
	if graph == nil {
		return nil
	}
	return graph
}

func issuerLiveness(graph *graphread.Client) machineauth.IssuerLiveness {
	if graph == nil {
		return nil
	}
	return graph
}

func pingNeo4j(graph *graphread.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		if graph == nil {
			return errNotConnected
		}
		return graph.Ping(ctx)
	}
}

// probeNeo4jAtBoot gives Neo4j a short grace period purely so the startup log
// tells the operator the truth about a normal co-start. It does NOT decide
// whether this process can use the graph: the client it probes is lazy and
// keeps re-dialing regardless of what this returns, and the health prober
// (RunProber, every 10s) is what actually flips /readyz once Neo4j answers.
// Returning an error here is a log line, nothing more.
func probeNeo4jAtBoot(ctx context.Context, graph *graphread.Client) error {
	var lastErr error
	for i := 0; i < 10; i++ {
		if err := graph.Ping(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return lastErr
}

func connectNatsWithRetry(url string) (*nats.Conn, error) {
	var lastErr error
	for i := 0; i < 30; i++ {
		nc, err := nats.Connect(url,
			nats.Name("osiris-api"),
			nats.MaxReconnects(-1),
			nats.ReconnectWait(2*time.Second),
			nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
				log.Printf("osiris-api: nats disconnected: %v", err)
			}),
			nats.ReconnectHandler(func(_ *nats.Conn) {
				log.Printf("osiris-api: nats reconnected")
			}),
		)
		if err == nil {
			return nc, nil
		}
		lastErr = err
		log.Printf("osiris-api: nats not ready yet (%v), retrying in 2s", err)
		time.Sleep(2 * time.Second)
	}
	return nil, lastErr
}
