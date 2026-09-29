// Package omniroute is osiris-api's ONLY caller of omniroute/operations.mjs
// — the 6-operation contract docs/design/10-core-architecture.md §9.6 and
// docs/design/30-onboarding-provisioning.md §1.0 hand to "the next worker"
// (this build) to invoke.
//
// DECISION — subprocess (Node), not a Go reimplementation. Recorded in full
// in docs/design/IMPLEMENTATION-NOTES.md; summarized here because the choice
// shapes every function in this file:
//
//   - operations.mjs is not a throwaway script. It went through multiple
//     rounds of real HTTP experimentation against a live omniroute instance
//     (the alias-convergence bug fix in IMPLEMENTATION-NOTES §50, the
//     manage-key exchange added by THIS build per 30-onboarding-
//     provisioning.md §1.0.4) — the omniroute wire contract's single source
//     of truth already lives there, in a file explicitly designed with a
//     "CLI dispatcher" section for exactly this cross-language call
//     (see operations.mjs's own header comment, "CLI(교차 언어 호출용)").
//   - Reimplementing the same HTTP calls in Go would create a second,
//     independently-maintained copy of that contract. The two WILL diverge
//     the next time someone fixes a bug in one and forgets the other — this
//     already happened once in this codebase in a different shape (I2/I3's
//     writeops.go MERGE bugs) and the design brief for THIS build explicitly
//     names divergence as the risk of the Go-reimplementation branch.
//
// COST of this choice, and how it is mitigated:
//   - The image needs a Node runtime, which breaks the core/orchestrator
//     "FROM scratch" convention (chosen there specifically to make A4
//     structurally impossible to violate — no shell, no LLM binary can hide
//     in a scratch image). Node itself is not an A4 violation: A4 forbids
//     the `claude` CLI and LLM gateway credentials, not a general-purpose
//     runtime. operations.mjs has ZERO npm dependencies (only node:fs,
//     node:url, node:path and the built-in fetch) — so the image needs a
//     bare Node binary and this one file, nothing from a package registry
//     at runtime (see api/Dockerfile's comment for how this is verified at
//     build time).
//   - Mitigations kept in place: non-root user, read-only rootfs where the
//     container runtime allows it, no docker.sock (G8 — unrelated to this
//     decision but worth restating: this package never touches Docker),
//     process spawned with a hard timeout and a byte cap on stdout (see
//     run() below) so a hung or runaway operations.mjs invocation cannot
//     wedge an HTTP handler or exhaust memory.
//
// The OTHER risk this file is responsible for containing: operations.mjs
// receives secrets (the omniroute admin password, provider API keys) as
// JSON on stdin. Nothing here logs the input payload; only the operation
// name and outcome fields are ever logged (see the log line in run()).
package omniroute

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const defaultTimeout = 15 * time.Second

// maxOutput caps how much stdout we will buffer from the child, defending
// against a misbehaving/compromised operations.mjs trying to exhaust
// osiris-api's memory rather than returning its one-line JSON contract.
const maxOutput = 1 << 20 // 1 MiB

type Client struct {
	scriptPath string
	nodeBin    string
}

func NewClient(scriptPath, nodeBin string) *Client {
	return &Client{scriptPath: scriptPath, nodeBin: nodeBin}
}

// Result is the generic {ok, ...} shape every operations.mjs export returns
// (see that file's header doc). Callers that need extra fields re-decode
// Raw themselves rather than this package growing a bespoke struct per op —
// operations.mjs is the single source of truth for field names, and a
// hand-maintained Go struct per op is exactly the divergence risk this
// package's whole design tries to avoid.
type Result struct {
	Raw json.RawMessage
}

func (r Result) Decode(v any) error { return json.Unmarshal(r.Raw, v) }

func (r Result) OK() bool {
	var probe struct {
		OK bool `json:"ok"`
	}
	_ = r.Decode(&probe)
	return probe.OK
}

// run invokes `node <script> <op>` with input marshaled to stdin, and parses
// the single-line JSON result from stdout. A non-zero exit or invalid JSON
// is a Go error (operations.mjs's own contract: "종료 코드는 치명적 오류에서만 1").
func (c *Client) run(ctx context.Context, op string, input any) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	payload, err := json.Marshal(input)
	if err != nil {
		return Result{}, fmt.Errorf("marshal input for %s: %w", op, err)
	}

	cmd := exec.CommandContext(ctx, c.nodeBin, c.scriptPath, op)
	// Environment is snapshotted FRESH on every call, deliberately not
	// cached at construction time: operations.mjs resolves
	// OSIRIS_OMNIROUTE_MANAGE_KEY from its own process env (resolveOpts),
	// and the onboarding handler updates THIS process's env via os.Setenv
	// the moment a fresh manage key is exchanged (see
	// internal/onboarding/handlers.go's omniroute-admin handler) — a cached
	// snapshot here would make that update invisible until the next
	// container restart, silently falling back to a password login path
	// that may no longer be configured.
	cmd.Env = os.Environ()
	cmd.Stdin = bytes.NewReader(payload)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, max: maxOutput}
	cmd.Stderr = &limitedWriter{w: &stderr, max: maxOutput}

	runErr := cmd.Run()
	// Never log `payload` — it may contain a provider apiKey or the
	// omniroute admin password (§1.0.4). Only the operation name and outcome.
	if runErr != nil {
		return Result{}, fmt.Errorf("operations.mjs %s failed: %v (stderr=%q)", op, runErr, stderr.String())
	}
	out := bytes.TrimSpace(stdout.Bytes())
	if !json.Valid(out) {
		return Result{}, fmt.Errorf("operations.mjs %s: non-JSON stdout (stderr=%q)", op, stderr.String())
	}
	return Result{Raw: out}, nil
}

type limitedWriter struct {
	w   *bytes.Buffer
	max int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	remaining := l.max - l.w.Len()
	if remaining <= 0 {
		return len(p), nil // silently drop past the cap, do not error the child
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	_, err := l.w.Write(p)
	return len(p), err
}

// --- typed wrappers over the 6 operations ----------------------------------
// (docs/design/10-core-architecture.md §9.6, docs/design/
// 30-onboarding-provisioning.md §1.0.8's endpoint table)

func (c *Client) RegisterProviderCredential(ctx context.Context, name, provider, apiKey string) (Result, error) {
	return c.run(ctx, "register-provider-credential", map[string]string{
		"name": name, "provider": provider, "apiKey": apiKey,
	})
}

type AliasEntry struct {
	Name         string `json:"name"`
	ProviderName string `json:"providerName"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
}

func (c *Client) UpsertAliases(ctx context.Context, entries []AliasEntry) (Result, error) {
	return c.run(ctx, "upsert-aliases", map[string]any{"entries": entries})
}

func (c *Client) IssueOrGetGatewayToken(ctx context.Context, name string, scopes []string) (Result, error) {
	return c.run(ctx, "issue-or-get-gateway-token", map[string]any{"name": name, "scopes": scopes})
}

func (c *Client) ApplyGatewayTokenToEnv(ctx context.Context, token, envFile string) (Result, error) {
	return c.run(ctx, "apply-gateway-token-to-env", map[string]string{"token": token, "envFile": envFile})
}

// ExchangeAdminPasswordForManageKey is §1.0.4's new operation: the password
// is used exactly once, in-memory, inside operations.mjs's login() call, and
// never returned, logged, or written to any file by this package or that
// script (see operations.mjs's own comment on the function for the
// grep-verifiable claim). Only the resulting manage-scoped key crosses back.
func (c *Client) ExchangeAdminPasswordForManageKey(ctx context.Context, password, name string) (Result, error) {
	return c.run(ctx, "exchange-admin-password-for-manage-key", map[string]string{
		"password": password, "name": name,
	})
}

func (c *Client) ApplyManageKeyToEnv(ctx context.Context, key, envFile string) (Result, error) {
	return c.run(ctx, "apply-manage-key-to-env", map[string]string{"key": key, "envFile": envFile})
}

func (c *Client) GetConfigurationStatus(ctx context.Context) (Result, error) {
	return c.run(ctx, "status", map[string]any{})
}

func (c *Client) GetDefaultAliasMapping(ctx context.Context) (Result, error) {
	return c.run(ctx, "default-alias-mapping", map[string]any{})
}

// --- [2026-09-11 추가] 콘솔 "프로바이더 연결" 탭 CRUD + OAuth 대리 -----------
// operations.mjs의 listProviders/createProvider/updateProvider/deleteProvider/
// oauthAction 5개에 대한 타입 있는 래퍼. 그 함수들의 doc comment가 wire
// contract의 단일 소스이고, 이 struct들은 Go 호출자(internal/providers)가
// 필드를 실수로 오타 내지 않게 하는 얇은 장식일 뿐이다.

func (c *Client) ListProviders(ctx context.Context) (Result, error) {
	return c.run(ctx, "list-providers", map[string]any{})
}

// CreateProviderInput mirrors createProvider's destructured argument.
// `omitempty` matters here: operations.mjs only forwards a field to
// omniroute's POST /api/providers when the key is present at all (`apiKey
// !== undefined` etc.), so a zero-value Priority must not be sent as 0 when
// the caller meant "unset" — callers that want priority 0 pass a *int.
type CreateProviderInput struct {
	Provider string `json:"provider"`
	APIKey   string `json:"apiKey,omitempty"`
	Name     string `json:"name"`
	URL      string `json:"url,omitempty"`
	Priority *int   `json:"priority,omitempty"`
}

func (c *Client) CreateProvider(ctx context.Context, in CreateProviderInput) (Result, error) {
	return c.run(ctx, "create-provider", in)
}

// UpdateProviderInput mirrors updateProvider — every field but ID is a
// pointer so "field omitted" (leave as-is) is distinguishable from "field
// set to its zero value" (e.g. isActive:false, priority:0).
type UpdateProviderInput struct {
	ID           string  `json:"id"`
	Name         *string `json:"name,omitempty"`
	Priority     *int    `json:"priority,omitempty"`
	IsActive     *bool   `json:"isActive,omitempty"`
	DefaultModel *string `json:"defaultModel,omitempty"`
	APIKey       *string `json:"apiKey,omitempty"`
}

func (c *Client) UpdateProvider(ctx context.Context, in UpdateProviderInput) (Result, error) {
	return c.run(ctx, "update-provider", in)
}

func (c *Client) DeleteProvider(ctx context.Context, id string) (Result, error) {
	return c.run(ctx, "delete-provider", map[string]string{"id": id})
}

// OAuthAction proxies exactly one of the whitelisted omniroute OAuth steps
// (authorize|exchange|device-code|poll|import-token — see operations.mjs's
// OAUTH_ACTIONS map, the single source of truth for the whitelist). Callers
// in internal/providers only ever pass a literal action string hard-coded at
// each of the five distinct route handlers — there is no path in osiris-api
// that lets a console request choose the action string, so this is not an
// open proxy even though operations.mjs itself also re-validates it.
func (c *Client) OAuthAction(ctx context.Context, provider, action string, query map[string]string, body any) (Result, error) {
	return c.run(ctx, "oauth-action", map[string]any{
		"provider": provider,
		"action":   action,
		"query":    query,
		"body":     body,
	})
}
