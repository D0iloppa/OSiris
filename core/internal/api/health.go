// Health endpoints. Principle borrowed deliberately from
// docs/design/10-core-architecture.md §5.4 (written for the agent
// supervisor, but the same discipline applies to any long-lived process):
// handlers must be light and must never block on a lock that a slow path
// (here: a Neo4j write transaction) might be holding. So /livez and /readyz
// read only atomic state that a background prober updates on its own
// schedule — they never open a session or run a query inline.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

type HealthState struct {
	neo4jOK   atomic.Bool
	natsOK    atomic.Bool
	seedDone  atomic.Bool
	startedAt time.Time
}

func NewHealthState() *HealthState {
	return &HealthState{startedAt: time.Now()}
}

func (h *HealthState) SetSeedDone(ok bool) { h.seedDone.Store(ok) }

// RunProber periodically checks Neo4j/NATS connectivity and updates atomic
// flags. It never touches the write path and never blocks a request
// handler — the handler only reads the flags this goroutine last wrote.
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

// LivezHandler answers "is this process alive and able to serve HTTP at
// all" — it does not consult Neo4j or NATS. A process that can answer this
// at all is, by definition, not wedged.
func (h *HealthState) LivezHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"uptimeS": int(time.Since(h.startedAt).Seconds()),
	})
}

// ReadyzHandler answers "can this instance safely accept graph-write
// requests right now" — dependencies + catalog seed must all be good.
func (h *HealthState) ReadyzHandler(w http.ResponseWriter, r *http.Request) {
	neo4jOK := h.neo4jOK.Load()
	natsOK := h.natsOK.Load()
	seedOK := h.seedDone.Load()
	ok := neo4jOK && natsOK && seedOK

	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": map[bool]string{true: "ready", false: "not_ready"}[ok],
		"checks": map[string]bool{
			"neo4j":         neo4jOK,
			"nats":          natsOK,
			"catalogSeeded": seedOK,
		},
	})
}
