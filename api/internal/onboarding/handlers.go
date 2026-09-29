// Package onboarding implements the osiris-api endpoint contract
// docs/design/30-onboarding-provisioning.md §1.0.8 defines for onboarding
// step0 ("결정적 스택 배선" — no LLM anywhere in this package, by design: the
// whole point of step0 is that it happens BEFORE any agent, including
// bootstrap-master, can be talked to).
//
// Every handler here is a thin wrapper around one of the 6 operations in
// omniroute/operations.mjs (invoked via internal/omniroute's subprocess
// client) — this package holds no omniroute wire-protocol knowledge of its
// own, exactly as designed (§9.6: "이 워커가 오퍼레이션들을 호출한다").
package onboarding

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"osiris-api/internal/graphread"
	"osiris-api/internal/omniroute"
)

type Service struct {
	ops           *omniroute.Client
	envFilePath   string
	graph         *graphread.Client // may be nil if Neo4j is unreachable at boot; handlers degrade gracefully
	gatewayURL    string            // GatewayBaseURL + GatewayHealthPath, pre-joined
	orchStatusURL string
	httpClient    *http.Client
	omniMode      string // "embedded" | "external" — SetOmnirouteConsole
	omniUIURL     string
}

// SetOmnirouteConsole sets where the console should open omniroute's own admin UI.
func (s *Service) SetOmnirouteConsole(mode, uiURL string) {
	s.omniMode, s.omniUIURL = mode, uiURL
}

// OmnirouteConsole — GET /api/omniroute/console. The console embeds/links omniroute's
// admin UI from this instead of hardcoding `/omni/` (external mode serves it elsewhere).
func (s *Service) OmnirouteConsole(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"mode": s.omniMode, "url": s.omniUIURL})
}

func NewService(ops *omniroute.Client, envFilePath, gatewayBaseURL, gatewayHealthPath, orchestratorBaseURL string, graph *graphread.Client) *Service {
	return &Service{
		ops:           ops,
		envFilePath:   envFilePath,
		graph:         graph,
		gatewayURL:    strings.TrimRight(gatewayBaseURL, "/") + gatewayHealthPath,
		orchStatusURL: strings.TrimRight(orchestratorBaseURL, "/") + "/status",
		httpClient:    &http.Client{Timeout: 5 * time.Second},
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return false
	}
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

// --- POST /onboarding/step0/omniroute-admin --------------------------------
//
// §1.0.8: "password는 저장하지 않고 교환에만 사용 — 요청 처리 완료 즉시
// 메모리에서 폐기, 파일/로그 어디에도 쓰지 않는다. 교환으로 얻은 키는
// 저장. 응답에는 password도 key도 에코하지 않는다 — exchanged:true만."

type omniAdminReq struct {
	Password string `json:"password"`
}

const manageKeyName = "osiris-manage"

func (s *Service) OmniouteAdmin(w http.ResponseWriter, r *http.Request) {
	var req omniAdminReq
	if !decode(w, r, &req) {
		return
	}
	if req.Password == "" {
		writeErr(w, http.StatusBadRequest, "password is required")
		return
	}
	res, err := s.ops.ExchangeAdminPasswordForManageKey(r.Context(), req.Password, manageKeyName)
	// req.Password goes out of scope here; nothing in this function writes
	// it anywhere. Go does not let us force-zero a string's backing array,
	// but we also never copy it into a longer-lived variable, a log line,
	// or a struct field — the request body itself is the only place it
	// lived, and that buffer is GC'd once the handler returns.
	if err != nil {
		writeErr(w, http.StatusBadGateway, "omniroute unreachable")
		return
	}
	var body struct {
		OK    bool   `json:"ok"`
		Key   string `json:"key"`
		Error string `json:"error"`
	}
	if derr := res.Decode(&body); derr != nil || !body.OK {
		msg := body.Error
		if msg == "" {
			msg = "exchange failed"
		}
		writeErr(w, http.StatusBadGateway, msg)
		return
	}
	// Persist the MANAGE KEY (never the password) to .env, and make it
	// visible to this process's own env immediately — see
	// internal/omniroute/ops.go's comment on why run() snapshots os.Environ()
	// fresh on every call instead of caching it at construction.
	if _, err := s.ops.ApplyManageKeyToEnv(r.Context(), body.Key, s.envFilePath); err != nil {
		log.Printf("onboarding: WARNING: manage key exchanged but .env update failed: %v", err)
	}
	_ = os.Setenv("OSIRIS_OMNIROUTE_MANAGE_KEY", body.Key)
	writeJSON(w, http.StatusOK, map[string]bool{"exchanged": true})
}

// --- POST /onboarding/step0/providers --------------------------------------

type providerReq struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	APIKey   string `json:"apiKey"`
}

func (s *Service) Providers(w http.ResponseWriter, r *http.Request) {
	var req providerReq
	if !decode(w, r, &req) {
		return
	}
	if req.Name == "" || req.Provider == "" || req.APIKey == "" {
		writeErr(w, http.StatusBadRequest, "name, provider and apiKey are all required")
		return
	}
	res, err := s.ops.RegisterProviderCredential(r.Context(), req.Name, req.Provider, req.APIKey)
	// req.APIKey is never logged or persisted by this handler — operations.mjs
	// forwards it straight to omniroute's own API and this package never
	// touches the value again (see internal/omniroute/ops.go's package doc).
	if err != nil {
		writeErr(w, http.StatusBadGateway, "omniroute unreachable")
		return
	}
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = res.Decode(&body)
	if !body.OK {
		msg := body.Error
		if msg == "" {
			msg = "provider registration failed"
		}
		writeErr(w, http.StatusBadGateway, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- GET /onboarding/step0/alias-defaults -----------------------------------

func (s *Service) AliasDefaults(w http.ResponseWriter, r *http.Request) {
	res, err := s.ops.GetDefaultAliasMapping(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "cannot read default alias mapping")
		return
	}
	var entries []omniroute.AliasEntry
	if derr := res.Decode(&entries); derr != nil {
		writeErr(w, http.StatusInternalServerError, "malformed default alias mapping")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// --- POST /onboarding/step0/aliases -----------------------------------------

type aliasesReq struct {
	Entries []omniroute.AliasEntry `json:"entries"`
}

func (s *Service) Aliases(w http.ResponseWriter, r *http.Request) {
	var req aliasesReq
	if !decode(w, r, &req) {
		return
	}
	res, err := s.ops.UpsertAliases(r.Context(), req.Entries)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "omniroute unreachable")
		return
	}
	// Same non-nullable-array contract as step0Status above
	// (UpsertAliasesResponse.results: AliasApplyResult[] in types.ts) — start
	// from an empty slice, not nil, so a decode failure or an empty upstream
	// result set still marshals as `[]`.
	results := []map[string]any{}
	if derr := res.Decode(&results); derr != nil {
		results = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// --- POST /onboarding/step0/gateway-token -----------------------------------

func (s *Service) GatewayToken(w http.ResponseWriter, r *http.Request) {
	res, err := s.ops.IssueOrGetGatewayToken(r.Context(), "osiris-agents", []string{})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "omniroute unreachable")
		return
	}
	var body struct {
		OK    bool   `json:"ok"`
		Token string `json:"token"`
		Error string `json:"error"`
	}
	if derr := res.Decode(&body); derr != nil || !body.OK {
		msg := body.Error
		if msg == "" {
			msg = "gateway token issuance failed"
		}
		writeErr(w, http.StatusBadGateway, msg)
		return
	}
	if _, err := s.ops.ApplyGatewayTokenToEnv(r.Context(), body.Token, s.envFilePath); err != nil {
		writeErr(w, http.StatusInternalServerError, "token issued but .env update failed")
		return
	}
	// §1.0.8: response never echoes the token value.
	writeJSON(w, http.StatusOK, map[string]any{
		"issued":                   true,
		"needsOrchestratorRestart": true,
		"restartCommand":           "docker compose up -d osiris-orchestrator",
	})
}

// --- POST /onboarding/step0/verify-gateway ----------------------------------
//
// §1.0.3 step 0-5: no LLM call, no cost. Checks (a) gateway health path 200,
// (b) all declared aliases inSync, (c) orchestrator confirms it holds a
// gateway token (via the boolean-only orchestrator/status field added for
// exactly this check — see orchestrator/internal/reconcile/reconcile.go's
// GatewayTokenConfigured doc comment).

type verifyResult struct {
	GatewayHealthOK          bool `json:"gatewayHealthOk"`
	AliasesInSync            bool `json:"aliasesInSync"`
	PropagatedToOrchestrator bool `json:"propagatedToOrchestrator"`
	Ready                    bool `json:"ready"`
}

func (s *Service) verifyGateway(ctx context.Context) verifyResult {
	var out verifyResult

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.gatewayURL, nil)
	if resp, err := s.httpClient.Do(req); err == nil {
		out.GatewayHealthOK = resp.StatusCode >= 200 && resp.StatusCode < 300
		resp.Body.Close()
	}

	if res, err := s.ops.GetConfigurationStatus(ctx); err == nil {
		var body struct {
			Aliases []struct {
				InSync bool `json:"inSync"`
			} `json:"aliases"`
		}
		if res.Decode(&body) == nil && len(body.Aliases) > 0 {
			all := true
			for _, a := range body.Aliases {
				if !a.InSync {
					all = false
					break
				}
			}
			out.AliasesInSync = all
		}
	}

	if orch, err := s.orchestratorStatus(ctx); err == nil {
		out.PropagatedToOrchestrator = orch.GatewayTokenConfigured
	}

	out.Ready = out.GatewayHealthOK && out.AliasesInSync && out.PropagatedToOrchestrator
	return out
}

func (s *Service) VerifyGateway(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.verifyGateway(r.Context()))
}

type orchestratorStatus struct {
	GatewayTokenConfigured bool `json:"gatewayTokenConfigured"`
}

func (s *Service) orchestratorStatus(ctx context.Context) (orchestratorStatus, error) {
	var out orchestratorStatus
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.orchStatusURL, nil)
	if err != nil {
		return out, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, nil
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out, nil
}

// --- GET /onboarding/step0/status and GET /onboarding/status ---------------

var envKeyPattern = func(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `=(.*)$`)
}

// envHasValue reports whether KEY= in the .env file has a non-empty value,
// without ever returning the value itself to a caller (every caller of this
// helper only needs the boolean — see Step0Status below).
func envHasValue(path, key string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	m := envKeyPattern(key).FindSubmatch(b)
	return len(m) == 2 && strings.TrimSpace(string(m[1])) != ""
}

type step0Status struct {
	Omniroute struct {
		Reachable          bool `json:"reachable"`
		ManageKeyExchanged bool `json:"manageKeyExchanged"`
	} `json:"omniroute"`
	Providers []map[string]string `json:"providers"`
	Aliases   []map[string]any    `json:"aliases"`
	Gateway   struct {
		Issued                   bool `json:"issued"`
		PropagatedToOrchestrator bool `json:"propagatedToOrchestrator"`
	} `json:"gatewayToken"`
	Ready bool `json:"ready"`
}

func (s *Service) step0Status(ctx context.Context) step0Status {
	// Providers/Aliases start as (non-nil) empty slices, never as the zero
	// value of a slice (nil), because console/src/api/types.ts declares
	// OnboardingStep0Status.providers/aliases as non-nullable arrays
	// (`ProviderCredentialSummary[]`/`AliasSyncStatus[]`) and Go's
	// encoding/json marshals a nil slice as JSON `null`, not `[]` — which
	// crashes step0Logic.ts's unconditional `.length` reads the moment
	// there are zero providers/aliases (i.e. always, before onboarding has
	// registered any). Every return path below must preserve this
	// invariant, including the early-return ones.
	out := step0Status{
		Providers: []map[string]string{},
		Aliases:   []map[string]any{},
	}
	out.Omniroute.ManageKeyExchanged = envHasValue(s.envFilePath, "OSIRIS_OMNIROUTE_MANAGE_KEY")
	out.Gateway.Issued = envHasValue(s.envFilePath, "OSIRIS_GATEWAY_TOKEN")

	res, err := s.ops.GetConfigurationStatus(ctx)
	if err != nil {
		return out
	}
	var body struct {
		Reachable bool                `json:"reachable"`
		Providers []map[string]string `json:"providers"`
		Aliases   []map[string]any    `json:"aliases"`
	}
	if res.Decode(&body) != nil {
		return out
	}
	out.Omniroute.Reachable = body.Reachable
	// body.Providers/body.Aliases may themselves be nil if omniroute's own
	// response had zero entries — only overwrite the empty-slice defaults
	// above when there is actually something to put in them.
	if body.Providers != nil {
		out.Providers = body.Providers
	}
	if body.Aliases != nil {
		out.Aliases = body.Aliases
	}

	if orch, err := s.orchestratorStatus(ctx); err == nil {
		out.Gateway.PropagatedToOrchestrator = orch.GatewayTokenConfigured
	}

	v := s.verifyGateway(ctx)
	out.Ready = out.Omniroute.Reachable && out.Omniroute.ManageKeyExchanged && v.Ready
	return out
}

func (s *Service) Step0Status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.step0Status(r.Context()))
}

// Status implements the aggregate GET /onboarding/status (§1.0.8's JSON
// shape). `phase` and the conversational (1단계) parts of `org` are
// deliberately minimal in this build: the assignment engine, org-seed, and
// bootstrap-master conversational onboarding are explicitly out of scope
// (see IMPLEMENTATION-NOTES.md) — so phase can only ever be "NotStarted"
// here, honestly, rather than simulating a state machine this build does
// not implement.
func (s *Service) Status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	step0 := s.step0Status(ctx)

	resp := map[string]any{
		"step0": step0,
		"phase": "NotStarted",
		"org":   s.orgSummary(ctx),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) orgSummary(ctx context.Context) map[string]any {
	out := map[string]any{
		"teams":                  0,
		"roles":                  0,
		"capabilityMappingRatio": nil,
		"approvalPolicyDefined":  false,
	}
	if s.graph == nil {
		return out
	}
	sum, err := s.graph.OrgSummary(ctx)
	if err != nil {
		log.Printf("onboarding: org summary query failed: %v", err)
		return out
	}
	out["teams"] = sum.Teams
	out["roles"] = sum.Roles
	out["approvalPolicyDefined"] = sum.AssignmentPolicyDefined
	return out
}
