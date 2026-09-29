// Package httpapi is osiris-orchestrator's own health/status surface. It is
// deliberately tiny and mirrors osiris-core's internal/api/health.go
// discipline (§5.4 applied to a platform component, not just the agent
// supervisor): handlers only ever read atomic/mutex-guarded state a
// background prober or the reconcile loop already computed — never opening
// a Neo4j session or calling the Docker API inline from a request handler.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

type Snapshotter interface {
	Snapshot() any
}

type HealthState struct {
	neo4jOK   atomic.Bool
	natsOK    atomic.Bool
	startedAt time.Time
	snap      Snapshotter
}

func NewHealthState(snap Snapshotter) *HealthState {
	return &HealthState{startedAt: time.Now(), snap: snap}
}

func (h *HealthState) RunProber(ctx context.Context, interval time.Duration, pingNeo4j func(context.Context) error, pingNats func() error) {
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

// Mux serves /livez, /readyz, and /status (this build's stand-in for the
// :AgentRuntime Neo4j cache §6.1 describes — see IMPLEMENTATION-NOTES.md).
func (h *HealthState) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", h.livez)
	mux.HandleFunc("/readyz", h.readyz)
	mux.HandleFunc("/status", h.status)
	return mux
}

func (h *HealthState) livez(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "uptimeS": int(time.Since(h.startedAt).Seconds())})
}

func (h *HealthState) readyz(w http.ResponseWriter, r *http.Request) {
	ok := h.neo4jOK.Load() && h.natsOK.Load()
	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": map[bool]string{true: "ready", false: "not_ready"}[ok],
		"checks": map[string]bool{"neo4j": h.neo4jOK.Load(), "nats": h.natsOK.Load()},
	})
}

func (h *HealthState) status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(h.snap.Snapshot())
}
