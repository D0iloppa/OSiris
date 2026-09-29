package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// ===========================================================================
// POST /bootstrap-master and DELETE /bootstrap-master — the trigger surface for
// 30-onboarding-provisioning.md §1.0.7 step 1 ("osiris-api가 0단계 통과를 감지 →
// osiris-orchestrator에 bootstrap-master 생성을 요청한다") and §1.5's teardown.
//
// ── THIS FILE HONORS THIS PACKAGE'S EXISTING DISCIPLINE, IT DOES NOT BEND IT ─
//
// The package doc says handlers "only ever read atomic/mutex-guarded state a
// background prober or the reconcile loop already computed — never opening a
// Neo4j session or calling the Docker API inline from a request handler."
// Creating a container is a Docker API call, so the handler DOES NOT MAKE IT:
// it starts the sequence in a goroutine and returns 202 with the current phase.
// The caller learns the outcome from /status, which is where this package already
// publishes computed state.
//
// That is not a workaround for the rule — it is what the rule is for. A
// synchronous handler here would hold a request open across ContainerCreate, an
// HTTP round trip to osiris-core, a file write and ContainerStart, and a slow
// Docker daemon would then make the orchestrator's own health surface queue
// behind onboarding.
//
// ── WHY THIS ENDPOINT IS AUTHENTICATED WHEN /status IS NOT ────────────────
//
// /livez, /readyz and /status are reads of non-secret state on an internal
// network. This one CREATES A CONTAINER and causes a credential to be issued, so
// it is gated by the platform token (the same CORE_API_TOKEN this service now
// holds for §8.5 steps 4-5). No new .env variable, and no new trust boundary:
// osiris-api already holds that token.
// ===========================================================================

// Bringer is the genesisboot.Manager, as an interface so this package keeps its
// no-Docker-types property.
type Bringer interface {
	Bring(ctx context.Context) error
	Teardown(ctx context.Context, waitSeconds int) error
	Phase() string
}

// RegisterBootstrapMaster wires the two routes onto an existing mux.
//
// Takes the mux rather than returning one so that /livez, /readyz and /status
// keep sharing a single listener — §5.4's separation is between HEALTH and the
// heavy API, and this endpoint is neither hot nor blocking (it returns 202
// immediately).
func RegisterBootstrapMaster(mux *http.ServeMux, b Bringer, platformToken string, logf func(string, ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	auth := func(r *http.Request) bool {
		if platformToken == "" {
			// Fail closed. An unset token must not mean "anyone may create a
			// genesis container" — the same explicit refusal osiris-core's
			// s.auth makes.
			return false
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		return subtle.ConstantTimeCompare([]byte(got), []byte(platformToken)) == 1
	}

	mux.HandleFunc("/bootstrap-master", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid platform token"})
			return
		}
		switch r.Method {
		case http.MethodPost:
			// Detached context, deliberately: the sequence must not be cancelled
			// halfway because the HTTP caller hung up. A bring-up abandoned
			// between ContainerCreate and the token write is precisely the state
			// §8.5 is built to avoid, and its own teardown path can only run if
			// the sequence is allowed to finish.
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				if err := b.Bring(ctx); err != nil {
					logf("osiris-orchestrator: bootstrap-master bring-up returned: %v", err)
				}
			}()
			writeJSON(w, http.StatusAccepted, map[string]string{
				"accepted": "true",
				"phase":    b.Phase(),
				"note":     "progress is reported on GET /status under bootstrapMaster",
			})
		case http.MethodDelete:
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
				defer cancel()
				if err := b.Teardown(ctx, 10); err != nil {
					logf("osiris-orchestrator: bootstrap-master teardown returned: %v", err)
				}
			}()
			writeJSON(w, http.StatusAccepted, map[string]string{
				"accepted": "true",
				"phase":    b.Phase(),
			})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST or DELETE"})
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
