// osiris-core — the only process allowed to write to Neo4j (A5/G7/D17).
//
// Deliberately absent from this binary (A4, D12): no `claude` binary, no LLM
// gateway credentials, no code path that calls an LLM. Everything here is
// deterministic: schema migration, catalog seeding, validated graph-write
// operations, and a read-only invariant checker.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"osiris-core/internal/action/effect"
	"osiris-core/internal/action/predicate"
	"osiris-core/internal/api"
	"osiris-core/internal/config"
	"osiris-core/internal/events"
	"osiris-core/internal/genesis"
	"osiris-core/internal/graphdb"
)

// toNameSet converts a sorted name slice (predicate.Names()/effect.Names())
// into the map[string]struct{} shape graphdb.ReconcileActionTypes expects.
// Kept in main.go, not graphdb, so graphdb never needs to know the shape
// its callers build these sets from — see ReconcileActionTypes' doc comment
// on the deliberate one-directional dependency arrow.
func toNameSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

// orgRevisionAdapter satisfies genesis.OrgRevisionReader with
// *graphdb.Client.ReadOrgSummary — kept here, in main.go, rather than in
// internal/genesis itself, so that package stays free of a graphdb import
// (its own doc comment on OrgRevisionReader explains why: pulling in a Neo4j
// driver dependency for one int64 would widen a package whose entire point is
// staying small).
type orgRevisionAdapter struct{ db *graphdb.Client }

func (a orgRevisionAdapter) CurrentOrgRevision(ctx context.Context) (int64, error) {
	summary, err := a.db.ReadOrgSummary(ctx)
	if err != nil {
		return 0, err
	}
	return summary.CurrentOrgRevision, nil
}

// The runtime image is FROM scratch (A4: no shell, no curl/wget, no package
// manager — nothing an LLM binary or its credentials could hide behind
// either). Docker HEALTHCHECK still needs an executable to run, so the
// binary itself doubles as its own healthcheck client: `-healthcheck` makes
// it do a single plain HTTP GET to its own /livez and exit 0/1, without
// starting the server or touching Neo4j/NATS.
func main() {
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(runHealthcheck())
	}
	if err := run(); err != nil {
		log.Fatalf("osiris-core: fatal: %v", err)
	}
}

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

	log.Printf("osiris-core: connecting to neo4j at %s", cfg.Neo4jURI)
	db, err := connectNeo4jWithRetry(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close(context.Background())

	// Not logging cfg.NatsURL itself — it embeds osiris-core's NATS password
	// as userinfo (nats://osiris-core:<password>@nats:4222). Found while
	// building osiris-api (whose main.go used to have the identical bug —
	// see IMPLEMENTATION-NOTES.md's osiris-api section for the full note);
	// fixed here too rather than left in place now that it's been noticed.
	log.Printf("osiris-core: connecting to nats")
	pub, err := connectNatsWithRetry(cfg.NatsURL)
	if err != nil {
		return err
	}
	defer pub.Close()

	health := api.NewHealthState()

	log.Printf("osiris-core: applying schema migrations (constraints/indexes)")
	if err := graphdb.ApplyMigrations(ctx, db); err != nil {
		return err
	}

	log.Printf("osiris-core: seeding catalog (capabilities/permissions/profiles) — idempotent")
	seedResult, err := graphdb.SeedCatalog(ctx, db, cfg.CatalogVersion, cfg.OrgName, cfg.OrgLegalName, cfg.OrgTimezone)
	if err != nil {
		return err
	}
	log.Printf("osiris-core: seed complete: %+v", seedResult)
	health.SetSeedDone(true)

	// [2026-09-11, C-1] docs/design/90-infra-topology.md §11 IN17 — seed the
	// three infra :ActionType rows (infra.apply/scale/delete). This is the
	// first CreateActionType call this binary's boot path has ever made —
	// actiontype_gitcommit.go's git.commit row was deliberately left
	// unseeded ("seeding a real row ... is a separate decision this narrow
	// stage does not make"); C-1 is exactly that decision, made for the
	// infra rows only (git.commit stays unseeded — untouched by this task).
	//
	// Idempotent by construction: CreateActionType refuses with
	// ErrValidation ("already exists — use UpdateActionType") on a second
	// boot, which this loop treats as success rather than a fatal error —
	// the same idempotency contract graphdb.SeedCatalog already documents
	// for capabilities/permissions/profiles, applied here to :ActionType
	// rows for the first time.
	knownPredicatesForSeed := toNameSet(predicate.Names())
	knownEffectsForSeed := toNameSet(effect.Names())
	for _, in := range []graphdb.ActionTypeInput{
		graphdb.InfraApplyActionType(),
		graphdb.InfraScaleActionType(),
		graphdb.InfraDeleteActionType(),
	} {
		in.ByPartyID = "system:boot-seed"
		_, err := db.CreateActionType(ctx, in, cfg.CatalogVersion, knownPredicatesForSeed, knownEffectsForSeed)
		switch {
		case err == nil:
			log.Printf("osiris-core: seeded :ActionType %s", in.ID)
		case errors.Is(err, graphdb.ErrValidation) && strings.Contains(err.Error(), "already exists"):
			log.Printf("osiris-core: :ActionType %s already seeded, skipping", in.ID)
		default:
			return fmt.Errorf("seed infra :ActionType %s: %w", in.ID, err)
		}
	}

	// [2026-08-12, P2 stage ②] AC20 boot check
	// (docs/design/80-action-layer.md §8.1/AD13, §9 AC20) — placed right
	// after seeding, same tier as I9 (checkI9_PermissionCatalogVersion):
	// a non-nil error here is fatal, matching AC20's "부팅 실패". See
	// graphdb.CheckActionTypePermissionMirror's doc comment. This check
	// DOES compute the gate derivation formula (80 G-A11, resolved 2026-08-13):
	// :Permission.gate = min(gateClass of all :ActionType rows requiring that
	// permission), collapsed to :Permission's two-valued domain ("none"/"approval").
	// Boot fails only if the stored gate mismatches the derived value (AD13's
	// fail-closed guarantee, same grade as I9). With zero :ActionType rows
	// claiming any :Permission via REQUIRES_PERMISSION edges today (normal until
	// stage ④ adds those edges), this check returns nil — not because gate is
	// not computed, but because there are no rows to derive from (see
	// CheckActionTypePermissionMirror's doc comment, "EMPTY-SET CASE").
	if err := graphdb.CheckActionTypePermissionMirror(ctx, db); err != nil {
		return err
	}

	// [2026-08-12] AC2/AC3 fail-closed boot reconciliation
	// (docs/design/80-action-layer.md §5.3.1, §9) — deliberately placed
	// HERE: after seeding (nothing to reconcile against before catalogs
	// exist) and BEFORE the readyz prober goes live (nothing has been told
	// "ready" yet, so no traffic can observe a stale, un-reconciled
	// :ActionType row). See graphdb.ReconcileActionTypes' doc comment for
	// the full fail-closed rationale and why this is not folded into
	// CheckInvariants' I1-I17 request-path checks.
	//
	// knownActionIDs [2026-08-12, P2 stage ②]: now sourced from
	// graphdb.SystemActionTypeIDs (80 §4.6's worked-example catalog, id
	// column, transcribed verbatim — see that file's doc comment on why
	// this is a constant list only, with no rows actually seeded yet).
	// AC2's boot-time inert-degrade sweep is now wired against the real
	// allowlist; it still matches zero rows today because stage ② adds no
	// :ActionType rows either — that gap is stage ③'s "git.commit 한 행"
	// and beyond.
	knownPredicates := toNameSet(predicate.Names())
	knownEffects := toNameSet(effect.Names())
	knownActionIDs := toNameSet(graphdb.SystemActionTypeIDs)
	reconcileReport, err := graphdb.ReconcileActionTypes(ctx, db, pub, knownActionIDs, knownPredicates, knownEffects)
	if err != nil {
		return err
	}
	switch {
	case reconcileReport.PublishErr != nil:
		// See graphdb.ReconcileActionTypes' doc comment: the graph write
		// (the safety-relevant part) already succeeded — only the
		// notification failed. Logged loudly, boot proceeds.
		log.Printf("osiris-core: WARNING: degraded %d :ActionType row(s) to status:'inert' (AC2/AC3: %v) but failed to publish security.actiontype.unknown: %v",
			len(reconcileReport.DegradedIDs), reconcileReport.DegradedIDs, reconcileReport.PublishErr)
	case len(reconcileReport.DegradedIDs) > 0:
		log.Printf("osiris-core: degraded %d :ActionType row(s) to status:'inert' (AC2/AC3, unknown id/predicate/effect reference): %v",
			len(reconcileReport.DegradedIDs), reconcileReport.DegradedIDs)
	default:
		log.Printf("osiris-core: action-type reconcile: no unknown references (predicates=%d, effects=%d known)",
			len(knownPredicates), len(knownEffects))
	}

	// Background prober for /readyz — never invoked from the request path.
	go health.RunProber(ctx, 10*time.Second, db.Ping, pub.Ping)

	// The genesis principal's verifier (§8.5, re-wired by K7 — see
	// internal/genesis's package doc comment). Constructing this creates
	// NOTHING and mints no credential: it is a TokenReview client plus the
	// two identity strings K7 pins ("system:serviceaccount:<namespace>:
	// <serviceaccount>"), wired via in-cluster config exactly the way any
	// controller-runtime-style component authenticates to its own API
	// server (70 §7.1: this grants osiris-core `create` on
	// `tokenreviews.authentication.k8s.io` ONLY — not workload creation).
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("osiris-core: load in-cluster k8s config for TokenReview: %w", err)
	}
	k8sClient, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("osiris-core: build k8s clientset for TokenReview: %w", err)
	}
	genGuard := genesis.NewGuard(
		genesis.NewK8sTokenReviewer(k8sClient),
		orgRevisionAdapter{db: db},
		cfg.GenesisNamespace, cfg.GenesisServiceAccount,
	)
	log.Printf("osiris-core: genesis principal guard wired (scope: %d allowlisted route(s); expects system:serviceaccount:%s:%s)",
		len(genesis.AllowedRoutes), cfg.GenesisNamespace, cfg.GenesisServiceAccount)

	server := api.NewServer(cfg, db, pub, health, genGuard)

	healthSrv := &http.Server{Addr: cfg.HealthAddr, Handler: server.HealthMux()}
	apiSrv := &http.Server{Addr: cfg.APIAddr, Handler: server.APIMux()}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("osiris-core: health server listening on %s (/livez /readyz)", cfg.HealthAddr)
		if err := healthSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	go func() {
		log.Printf("osiris-core: graph-write API listening on %s", cfg.APIAddr)
		if err := apiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Printf("osiris-core: shutting down")
	case err := <-errCh:
		log.Printf("osiris-core: server error: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = healthSrv.Shutdown(shutdownCtx)
	_ = apiSrv.Shutdown(shutdownCtx)
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
		log.Printf("osiris-core: neo4j not ready yet (%v), retrying in 2s", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, lastErr
}

func connectNatsWithRetry(url string) (*events.Publisher, error) {
	var lastErr error
	for i := 0; i < 30; i++ {
		pub, err := events.Connect(url)
		if err == nil {
			return pub, nil
		}
		lastErr = err
		log.Printf("osiris-core: nats not ready yet (%v), retrying in 2s", err)
		time.Sleep(2 * time.Second)
	}
	return nil, lastErr
}
