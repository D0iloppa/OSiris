package machineauth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"osiris-api/internal/authz"
)

// --- HTTP surface -----------------------------------------------------------
//
// §19 names POST /machine-principals but does not fix the full REST shape, so
// the rest is decided here and recorded in IMPLEMENTATION-NOTES.md:
//
//	POST   /api/machine-principals        issue   (plaintext token, once)
//	GET    /api/machine-principals        list    (never any token or hash)
//	DELETE /api/machine-principals/{id}   revoke
//
// ALL THREE ARE REGISTERED UNDER PersonOnly IN internal/api/server.go,
// INCLUDING THE GET. That is H27 — "자기 자신이나 다른 기계 주체를 발급·회전할
// 수 없다" — and §19.6's row ④, which classifies credential-state reads as
// org.manage.people rather than org.read.*. Gating the list with a permission
// code would work too (no machine principal can hold a non-org.read.* code),
// but PersonOnly is the stronger and simpler statement and it is the one HI15
// can check statically.
//
// The 30-onboarding-boundary Q2 recursion for the issued token: a human never
// sees it on a screen in normal operation — it is copied by the operator into
// a CI secret store once — and it is a scope-limited machine credential by
// construction (org.read.* only), so it is exactly the Q2b terminal case that
// internal/session's package doc walks through for the session cookie.

// Handlers is the PersonOnly-gated management surface.
type Handlers struct {
	store *Store
}

func NewHandlers(store *Store) *Handlers { return &Handlers{store: store} }

type issueReq struct {
	PrincipalID string   `json:"principalId"`
	Permissions []string `json:"permissions"`
	// TTLSeconds is optional. §19.4 fixes bootstrap at 24h and lets org class
	// choose; allowing a SHORTER-than-default value for both is a deviation
	// noted in IMPLEMENTATION-NOTES.md. It can only ever tighten: clampTTL
	// refuses anything above the class ceiling, so this field cannot be used
	// to extend a credential's life beyond what the design permits.
	TTLSeconds int64 `json:"ttlSeconds"`
}

type issueResp struct {
	PrincipalID string    `json:"principalId"`
	Class       string    `json:"class"`
	Permissions []string  `json:"permissions"`
	IssuedAt    time.Time `json:"issuedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	IssuedBy    string    `json:"issuedBy"`

	// Token is the ONLY place this value will ever appear. Not logged, not
	// re-readable, not recoverable — the store holds sha256 of it. Losing it
	// means revoking and issuing again.
	Token string `json:"token"`

	Warning string `json:"warning"`
}

// Issue handles POST /api/machine-principals.
func (h *Handlers) Issue(w http.ResponseWriter, r *http.Request) {
	actor, ok := authz.PrincipalFrom(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing or invalid session")
		return
	}

	var req issueReq
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	codes, err := authz.ParseCodes(req.Permissions)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// §19.4: the class follows from WHO is issuing, not from a request field.
	// A caller cannot choose its own class — that would let an operator mint a
	// 90-day credential during the bootstrap window.
	class := ClassBootstrap
	issuedBy := "master-key"
	if actor.Kind == authz.KindPerson {
		class = ClassOrg
		issuedBy = actor.PartyID
	}

	if class == ClassBootstrap {
		// §19.5: bootstrap principals are issued by the master-key holder, and
		// only while that window is open. Refusing here rather than minting a
		// credential that classValid would reject on first use keeps the
		// failure at the moment a human can act on it.
		open, oerr := h.store.org.MasterWindowOpen(r.Context())
		if oerr != nil {
			writeErr(w, http.StatusServiceUnavailable,
				"cannot determine whether the bootstrap window is open; refusing to issue (fail-closed)")
			return
		}
		if !open {
			writeErr(w, http.StatusConflict,
				"the bootstrap window has closed (an org exists and an admin holds a credential); an org.admin must issue an org-class principal instead")
			return
		}
	} else {
		// [2026-07-27] org class, turned on.
		//
		// §19.4: the org-class issuer is "org.admin 보유 :Person의 세션". The
		// permission check is NOT re-implemented here — it is the same
		// authz.Authorize the rest of the service uses, over the codes the
		// guard resolved from effective(party) for this request.
		//
		// Requiring org.admin (rather than, say, org.manage.people) follows
		// §19.4's table verbatim, and MP6's audit fan-out is addressed to
		// org.admin holders, so an issuer who is not one would be issuing a
		// credential whose notifications go to a set they are not in.
		if !authz.Authorize(actor, authz.PermOrgAdmin) {
			writeErr(w, http.StatusForbidden,
				"issuing an org-class machine principal requires the top-level administrative permission")
			return
		}
		// The issuer must be live at issue time as well as at every later
		// request. classValid re-checks it per request (MP1); this check just
		// keeps the failure at the moment a human can act on it, exactly as
		// the bootstrap branch above does for its own window.
		alive, aerr := h.store.people.PersonIsActive(r.Context(), actor.PartyID)
		if aerr != nil {
			writeErr(w, http.StatusServiceUnavailable,
				"cannot determine issuer liveness; refusing to issue (fail-closed)")
			return
		}
		if !alive {
			writeErr(w, http.StatusForbidden, "issuer is not an active person")
			return
		}
	}

	token, rec, err := h.store.Issue(r.Context(), IssueRequest{
		PrincipalID: strings.TrimSpace(req.PrincipalID),
		Permissions: codes,
		TTL:         time.Duration(req.TTLSeconds) * time.Second,
		Class:       class,
		IssuedBy:    issuedBy,
		ActorRef:    actor.ActorRef(),
	})
	if err != nil {
		status := http.StatusBadRequest
		if isUnavailable(err) {
			status = http.StatusServiceUnavailable
		}
		writeErr(w, status, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, issueResp{
		PrincipalID: rec.PrincipalID,
		Class:       string(rec.Class),
		Permissions: rec.Permissions,
		IssuedAt:    rec.IssuedAt,
		ExpiresAt:   rec.ExpiresAt,
		IssuedBy:    rec.IssuedBy,
		Token:       token,
		Warning:     "This token is shown exactly once and is not recoverable. Store it in your CI secret store; do not write it to a file, a log, or a shell history.",
	})
}

type listItem struct {
	PrincipalID string     `json:"principalId"`
	Class       string     `json:"class"`
	Permissions []string   `json:"permissions"`
	IssuedAt    time.Time  `json:"issuedAt"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	IssuedBy    string     `json:"issuedBy"`
	FirstUsedAt *time.Time `json:"firstUsedAt,omitempty"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty"`
}

// List handles GET /api/machine-principals — MP6's "활성 기계 주체 수가 0이
// 아닌 동안 항상 표시된다" is a console requirement; this is the data behind
// it. The response never contains a token OR a token hash: the hash is not a
// usable credential, but it is a value derived from one, and this repository's
// incident history argues for keeping derived credential material out of
// response bodies entirely.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	recs, err := h.store.List(r.Context())
	if err != nil {
		status := http.StatusInternalServerError
		if isUnavailable(err) {
			status = http.StatusServiceUnavailable
		}
		writeErr(w, status, err.Error())
		return
	}
	items := make([]listItem, 0, len(recs))
	for _, rec := range recs {
		items = append(items, listItem{
			PrincipalID: rec.PrincipalID,
			Class:       string(rec.Class),
			Permissions: rec.Permissions,
			IssuedAt:    rec.IssuedAt,
			ExpiresAt:   rec.ExpiresAt,
			IssuedBy:    rec.IssuedBy,
			FirstUsedAt: rec.FirstUsedAt,
			LastUsedAt:  rec.LastUsedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"principals": items,
		// Stated, not hidden — §19.3's cache row and §7.4's honest cost.
		"lastUsedAtStalenessSeconds": int(lastUsedWriteInterval.Seconds()),
		"revocationLagSeconds":       int(PositiveCacheTTL.Seconds()),
	})
}

// Revoke handles DELETE /api/machine-principals/{id} (MP5).
func (h *Handlers) Revoke(w http.ResponseWriter, r *http.Request) {
	actor, ok := authz.PrincipalFrom(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing or invalid session")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "principal id is required")
		return
	}
	switch err := h.store.Revoke(r.Context(), id, actor.ActorRef()); {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{
			"revoked":              id,
			"revocationLagSeconds": int(PositiveCacheTTL.Seconds()),
		})
	case err == ErrNotFound:
		writeErr(w, http.StatusNotFound, "no such machine principal")
	case isUnavailable(err):
		writeErr(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

func isUnavailable(err error) bool {
	return err != nil && strings.Contains(err.Error(), ErrUnavailable.Error())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
