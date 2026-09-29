package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"osiris-core/internal/genesis"
	"osiris-core/internal/onboarding"
)

// ===========================================================================
// The genesis principal's surface — docs/design/10-core-architecture.md §8.5,
// as amended by §8.5.1 (F14) and re-wired by
// docs/design/70-kubernetes-deployment.md §4.1 (K7).
//
// TWO AUTH SCHEMES MEET HERE AND MUST NOT BE CONFUSED:
//
//	s.auth        the platform token (CORE_API_TOKEN). osiris-orchestrator and
//	              osiris-api.
//	s.genesisAuth the genesis principal's K8s bound ServiceAccount token —
//	              bootstrap-master, i.e. the onboarding consultant Pod.
//	              Allowlisted to three routes, authenticated via TokenReview.
//
// ★ K7: THERE IS NO ISSUE OR REVOKE ROUTE ANYMORE. §8.5's build had the
// platform token issue/revoke a core-minted credential because only
// orchestrator could truthfully claim "the real id of a container I just
// created" (G8: it alone holds docker.sock). K8s removes that whole
// argument: the bootstrap-master Pod's ServiceAccount token is minted BY K8s
// and verified BY asking K8s (TokenReview) — osiris-core is never in the
// business of minting or remembering one. See internal/genesis's package doc
// comment for the full rewrite rationale.
// ===========================================================================

// genesisAuth authenticates the genesis principal's K8s token AND enforces
// the allowlist.
//
// BOTH IN ONE MIDDLEWARE, DELIBERATELY. A guard that only authenticated would
// leave "what may this subject reach" to the route table, and route tables grow.
// Here the check is: does this token verify AS THE EXPECTED bootstrap-master
// ServiceAccount (genesis.Guard.Verify — TokenReview + identity match + W1, in
// that order), and is THIS (method, path) in genesis.AllowedRoutes. A new
// handler wired to this middleware without a list entry gets 403 — default
// deny, which is the property §8.5.1 asks for in as many words ("허용목록
// (allowlist)이어야 하며 ... 새 엔드포인트가 추가될 때 기본이 거부여야 하기
// 때문").
func (s *Server) genesisAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.gen == nil {
			writeErr(w, http.StatusServiceUnavailable, "genesis principal not available")
			return
		}
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		ident, err := s.gen.Verify(r.Context(), tok)
		if err != nil {
			// One message for every reason (no token / wrong identity / org
			// already exists / graph unreadable). The distinction goes to the
			// log, not to the caller.
			log.Printf("osiris-core: genesis auth refused for %s %s: %v", r.Method, r.URL.Path, err)
			writeErr(w, http.StatusUnauthorized, "missing or invalid genesis token")
			return
		}
		if !genesis.Allows(r.Method, r.URL.Path) {
			// The refusal that makes F14 real: POST /v1/onboarding/commit lands
			// here if anyone ever points it at this middleware.
			log.Printf("osiris-core: genesis scope refused %s %s (serviceaccount %s/%s) — not in the allowlist",
				r.Method, r.URL.Path, ident.Namespace, ident.ServiceAccount)
			writeErr(w, http.StatusForbidden,
				"the genesis principal's scope does not include this endpoint (10-core-architecture.md §8.5)")
			return
		}
		next(w, r)
	}
}

// --- POST /v1/onboarding/draft (genesis token) -----------------------------

// handleValidateDraft is §3.3's early validation. IT WRITES NOTHING.
//
// The draft is not persisted here — 30 §8 puts its storage in the chat
// transcript, which is osiris-api's side. What this endpoint owns is the
// question the consultant cannot be trusted to answer about itself: does every
// code in this draft exist in the system-owned catalog (O3)?
//
// A 200 with problems is the normal, expected answer, not an error: §3.3's loop
// is "위반 시 LLM에게 되돌려 재생성을 요구한다". Returning 400 would make an
// ordinary conversational turn look like a client bug.
func (s *Server) handleValidateDraft(w http.ResponseWriter, r *http.Request) {
	var d onboarding.Draft
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid draft body")
		return
	}
	cat, err := s.db.ReadCatalog(r.Context(), s.cfg.CatalogVersion)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot read the catalog; refusing to validate against an unknown catalog")
		return
	}
	if len(cat.Capabilities) == 0 && len(cat.PermissionProfiles) == 0 {
		// Refusing to pass vacuously: an unseeded catalog would make every
		// membership test trivially fail, and reporting that as "your draft is
		// wrong" would send the consultant into a retry loop over a problem it
		// cannot fix.
		writeErr(w, http.StatusServiceUnavailable,
			"the catalog is empty; osiris-core has not seeded it yet, so a draft cannot be validated")
		return
	}
	writeJSON(w, http.StatusOK, onboarding.Validate(d, cat))
}

// --- GET /v1/onboarding/status (genesis token) -----------------------------

// handleGenesisStatus is the consultant's view of where things stand.
//
// COUNTS ONLY (graphdb.OrgSummary) — no names, no emails, no ids. §8.5 grants
// this subject catalog reads and status, not the organization's contents, and
// the shape of this response is where that distinction is either kept or lost.
//
// NO MORE "genesis.live/expiresAt" FIELD (K7): §8.5's build could report
// whether ITS OWN in-memory token was still live because it held the only
// copy. Under K7 there is no token for core to hold an opinion about — K8s
// owns that lifecycle entirely (expirationSeconds, ServiceAccount existence).
// A caller reaching this handler AT ALL already proved its own token verifies
// right now (genesisAuth ran first); there is nothing further for core to add
// about liveness.
func (s *Server) handleGenesisStatus(w http.ResponseWriter, r *http.Request) {
	summary, err := s.db.ReadOrgSummary(r.Context())
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot read org summary")
		return
	}
	out := map[string]any{
		"org":   summary,
		"scope": scopeStrings(),
		// Stated explicitly so the consultant's prompt does not have to infer it:
		// the commit is not this subject's to call (§8.5.1/F14).
		"commitCaller": "the session of the human who approves the draft (30-onboarding-provisioning.md §3.4.3)",
	}
	writeJSON(w, http.StatusOK, out)
}

// --- GET /v1/onboarding/catalog (genesis token) ----------------------------

// handleGenesisCatalog serves the read-only catalog. This is what makes O3
// workable: the consultant selects from these lists and invents nothing.
func (s *Server) handleGenesisCatalog(w http.ResponseWriter, r *http.Request) {
	cat, err := s.db.ReadCatalog(r.Context(), s.cfg.CatalogVersion)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot read the catalog")
		return
	}
	writeJSON(w, http.StatusOK, cat)
}

func scopeStrings() []string {
	out := make([]string, 0, len(genesis.AllowedRoutes))
	for _, r := range genesis.AllowedRoutes {
		out = append(out, r.String())
	}
	return out
}
