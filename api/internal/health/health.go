// Package health implements /livez and /readyz on their own listener,
// following the exact discipline osiris-core's internal/api/health.go
// already established for this codebase (docs/design/10-core-architecture.md
// §5.4): handlers read ONLY atomic state that a background prober updates on
// its own schedule. No mutex, no inline Neo4j/NATS call, no exec.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

type State struct {
	neo4jOK   atomic.Bool
	natsOK    atomic.Bool
	startedAt time.Time
}

func New() *State {
	return &State{startedAt: time.Now()}
}

// RunProber periodically checks dependencies and updates atomic flags. Never
// called from a request handler.
func (h *State) RunProber(ctx context.Context, interval time.Duration, pingNeo4j func(context.Context) error, pingNats func() error) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	probe := func() {
		pctx, cancel := context.WithTimeout(ctx, interval/2)
		defer cancel()
		h.neo4jOK.Store(pingNeo4j(pctx) == nil)
		h.natsOK.Store(pingNats() == nil)
	}
	probe()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			probe()
		}
	}
}

// Livez answers "is this process alive at all" — no dependency is consulted.
func (h *State) Livez(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"uptimeS": int(time.Since(h.startedAt).Seconds()),
	})
}

// Readyz answers "can this instance safely serve console REST/SSE traffic
// right now" — Neo4j (read-only) and NATS (SSE resume) both reachable.
// omniroute reachability is deliberately NOT a readyz input: osiris-api stays
// ready even while omniroute/step0 is unconfigured — that is a normal
// pre-onboarding state, not a degraded osiris-api.
func (h *State) Readyz(w http.ResponseWriter, r *http.Request) {
	neo4jOK := h.neo4jOK.Load()
	natsOK := h.natsOK.Load()
	ok := neo4jOK && natsOK

	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": map[bool]string{true: "ready", false: "not_ready"}[ok],
		"checks": map[string]bool{
			"neo4j": neo4jOK,
			"nats":  natsOK,
		},
	})
}
