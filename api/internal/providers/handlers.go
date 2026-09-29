// Package providers implements osiris-api's console-facing "프로바이더
// 연결" surface: list/create/update/delete of omniroute provider
// connections, plus a thin OAuth proxy for the redirect/device-code/
// import-token registration flows (authorize, exchange, device-code, poll,
// import-token).
//
// This package holds no omniroute wire-protocol knowledge of its own —
// exactly like internal/onboarding, every handler here is a thin wrapper
// around one of the operations in omniroute/operations.mjs (invoked via
// internal/omniroute's subprocess client). The five OAuth actions are NOT a
// generic proxy: each is its own route, registered with its own literal
// action string in api/internal/api/server.go, so a console request can
// never choose an arbitrary omniroute path — only one of the five wired
// here.
//
// Secrets never cross back to the browser. Every response that carries a
// provider connection object is passed through redact(), which walks the
// JSON tree and replaces any key that looks like a credential (apiKey,
// token, secret, password, ...) with a boolean "is this set" — never the
// value itself. omniroute is the sole owner of the actual secret.
package providers

import (
	"encoding/json"
	"net/http"
	"regexp"

	"osiris-api/internal/omniroute"
)

type Service struct {
	ops *omniroute.Client
}

func NewService(ops *omniroute.Client) *Service {
	return &Service{ops: ops}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

// secretKeyPattern matches JSON field names that must never reach the
// browser with their real value — deliberately broad (case-insensitive,
// substring match via regex, not an exact allowlist of known omniroute field
// names) because this package does not control omniroute's response schema
// and a new secret-shaped field appearing there in the future must be caught
// by default, not by remembering to update a list.
var secretKeyPattern = regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password|credential)`)

// redact walks an arbitrary decoded-JSON value (map[string]any / []any /
// scalars, as produced by encoding/json's default unmarshal into `any`) and
// replaces the VALUE of any key matching secretKeyPattern with a boolean
// "present" flag. Nested objects (e.g. providerSpecificData holding OAuth
// tokens) are covered because the walk recurses into every map and slice.
func redact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if secretKeyPattern.MatchString(k) {
				out[k] = present(val)
				continue
			}
			out[k] = redact(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = redact(e)
		}
		return out
	default:
		return v
	}
}

func present(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	default:
		return true
	}
}

func redactConnection(c map[string]any) map[string]any {
	out, _ := redact(c).(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

// --- GET /api/providers -----------------------------------------------------

func (s *Service) List(w http.ResponseWriter, r *http.Request) {
	res, err := s.ops.ListProviders(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "omniroute unreachable")
		return
	}
	var body struct {
		OK          bool             `json:"ok"`
		Connections []map[string]any `json:"connections"`
		Error       string           `json:"error"`
	}
	if derr := res.Decode(&body); derr != nil || !body.OK {
		msg := body.Error
		if msg == "" {
			msg = "list providers failed"
		}
		writeErr(w, http.StatusBadGateway, msg)
		return
	}
	connections := make([]map[string]any, 0, len(body.Connections))
	for _, c := range body.Connections {
		connections = append(connections, redactConnection(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": connections})
}

// --- POST /api/providers ----------------------------------------------------

type createReq struct {
	Provider string `json:"provider"`
	APIKey   string `json:"apiKey,omitempty"`
	Name     string `json:"name"`
	URL      string `json:"url,omitempty"`
	Priority *int   `json:"priority,omitempty"`
}

func (s *Service) Create(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Provider == "" || req.Name == "" {
		writeErr(w, http.StatusBadRequest, "provider and name are required")
		return
	}
	res, err := s.ops.CreateProvider(r.Context(), omniroute.CreateProviderInput{
		Provider: req.Provider,
		APIKey:   req.APIKey,
		Name:     req.Name,
		URL:      req.URL,
		Priority: req.Priority,
	})
	// req.APIKey is never logged — see internal/omniroute/ops.go's run().
	if err != nil {
		writeErr(w, http.StatusBadGateway, "omniroute unreachable")
		return
	}
	var body struct {
		OK         bool           `json:"ok"`
		Connection map[string]any `json:"connection"`
		Error      string         `json:"error"`
	}
	if derr := res.Decode(&body); derr != nil || !body.OK {
		msg := body.Error
		if msg == "" {
			msg = "provider creation failed"
		}
		writeErr(w, http.StatusBadGateway, msg)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"connection": redactConnection(body.Connection)})
}

// --- PUT /api/providers/{id} -------------------------------------------------

type updateReq struct {
	Name         *string `json:"name,omitempty"`
	Priority     *int    `json:"priority,omitempty"`
	IsActive     *bool   `json:"isActive,omitempty"`
	DefaultModel *string `json:"defaultModel,omitempty"`
	APIKey       *string `json:"apiKey,omitempty"`
}

func (s *Service) Update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id is required")
		return
	}
	var req updateReq
	if !decodeBody(w, r, &req) {
		return
	}
	res, err := s.ops.UpdateProvider(r.Context(), omniroute.UpdateProviderInput{
		ID:           id,
		Name:         req.Name,
		Priority:     req.Priority,
		IsActive:     req.IsActive,
		DefaultModel: req.DefaultModel,
		APIKey:       req.APIKey,
	})
	// req.APIKey (when set — an OAuth connection cannot have its token
	// changed via this field; omniroute itself enforces that, this handler
	// only forwards) is never logged.
	if err != nil {
		writeErr(w, http.StatusBadGateway, "omniroute unreachable")
		return
	}
	var body struct {
		OK         bool           `json:"ok"`
		Connection map[string]any `json:"connection"`
		Error      string         `json:"error"`
	}
	if derr := res.Decode(&body); derr != nil || !body.OK {
		msg := body.Error
		if msg == "" {
			msg = "provider update failed"
		}
		writeErr(w, http.StatusBadGateway, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connection": redactConnection(body.Connection)})
}

// --- DELETE /api/providers/{id} ---------------------------------------------

func (s *Service) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id is required")
		return
	}
	res, err := s.ops.DeleteProvider(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "omniroute unreachable")
		return
	}
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if derr := res.Decode(&body); derr != nil || !body.OK {
		msg := body.Error
		if msg == "" {
			msg = "provider deletion failed"
		}
		writeErr(w, http.StatusBadGateway, msg)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

// --- OAuth proxy -------------------------------------------------------------
//
// Each of the five methods below calls OAuthAction with its OWN literal
// action string — never one derived from the request. The route table in
// api/internal/api/server.go registers exactly these five handlers at
// exactly these five paths; there is no handler that accepts an action
// parameter from the client. That is what keeps this a whitelisted proxy
// instead of an open one.

func (s *Service) oauthRespond(w http.ResponseWriter, res omniroute.Result, err error) {
	if err != nil {
		writeErr(w, http.StatusBadGateway, "omniroute unreachable")
		return
	}
	var body struct {
		OK     bool           `json:"ok"`
		Status int            `json:"status"`
		Data   map[string]any `json:"data"`
	}
	if derr := res.Decode(&body); derr != nil {
		writeErr(w, http.StatusBadGateway, "malformed omniroute response")
		return
	}
	if !body.OK {
		msg := "oauth step failed"
		if em, ok := body.Data["error"].(string); ok && em != "" {
			msg = em
		}
		status := http.StatusBadGateway
		if body.Status >= 400 && body.Status < 500 {
			status = body.Status
		}
		writeErr(w, status, msg)
		return
	}
	writeJSON(w, http.StatusOK, redact(body.Data))
}

// GET /api/oauth/{provider}/authorize?redirect_uri=...
func (s *Service) Authorize(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	redirectURI := r.URL.Query().Get("redirect_uri")
	query := map[string]string{}
	if redirectURI != "" {
		query["redirect_uri"] = redirectURI
	}
	res, err := s.ops.OAuthAction(r.Context(), provider, "authorize", query, nil)
	s.oauthRespond(w, res, err)
}

// GET /api/oauth/{provider}/device-code
func (s *Service) DeviceCode(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	res, err := s.ops.OAuthAction(r.Context(), provider, "device-code", nil, nil)
	s.oauthRespond(w, res, err)
}

// POST /api/oauth/{provider}/exchange {code, redirectUri, codeVerifier, state}
func (s *Service) Exchange(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	var body map[string]any
	if !decodeBody(w, r, &body) {
		return
	}
	res, err := s.ops.OAuthAction(r.Context(), provider, "exchange", nil, body)
	// body may carry an authorization `code` — never logged (see run()'s
	// doc comment; this handler does not log it either).
	s.oauthRespond(w, res, err)
}

// POST /api/oauth/{provider}/poll — payload shape is provider-defined
// (device_code, codeVerifier, ...); this handler forwards it opaquely.
func (s *Service) Poll(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	var body map[string]any
	if !decodeBody(w, r, &body) {
		return
	}
	res, err := s.ops.OAuthAction(r.Context(), provider, "poll", nil, body)
	s.oauthRespond(w, res, err)
}

// POST /api/oauth/{provider}/import-token — payload shape is
// provider-defined and, per the console's provider catalog, only wired for
// a subset of providers; unsupported ones are refused by the console before
// this ever gets called, and omniroute itself is the final authority.
func (s *Service) ImportToken(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	var body map[string]any
	if !decodeBody(w, r, &body) {
		return
	}
	res, err := s.ops.OAuthAction(r.Context(), provider, "import-token", nil, body)
	s.oauthRespond(w, res, err)
}
