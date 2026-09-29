package supervisor

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ============================================================================
// PROBES. Read this whole file with §5.4's last invariant in hand:
//
//	"어떤 probe도 LLM을 호출하지 않는다. 불변식으로 못 박는다:
//	 /livez·/readyz 처리 경로에서 claude 프로세스를 생성하는 코드는 존재하지
//	 않는다. 게이트웨이 확인도 /healthz(무인증, 모델 호출 없음)만 친다."
//
// Nothing in this file runs on an HTTP handler's goroutine. Every function
// here is called from a BACKGROUND TICKER, writes its verdict into an atomic
// in health.State, and returns. The handlers then read only that atomic. So
// even the one legitimate `claude` invocation in the whole supervisor's
// readiness story (`claude --version`, run ONCE at boot per §5.4) is
// structurally outside the probe's request path: by the time /readyz is asked
// anything, the answer is already a boolean sitting in memory.
// ============================================================================

// verifyClaudeVersion is §5.4's "부팅 시 `claude --version` 1회 성공".
// Called exactly once, from boot, NOT from the /readyz handler. It is not an
// LLM call: --version prints a string and exits, no API request is made
// (verified empirically — see IMPLEMENTATION-NOTES.md).
func verifyClaudeVersion(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	// A deliberately minimal env: no ANTHROPIC_* at all, so this cannot even
	// accidentally reach the gateway.
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + WorkspaceDir,
		"TMPDIR=/tmp",
		"CLAUDE_CONFIG_DIR=" + ClaudeConfigDir,
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s --version: %w (output: %s)", bin, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// probeWorkspaceWritable is §5.4's "/workspace 쓰기 가능 (60초 주기 touch)".
func probeWorkspaceWritable() error {
	if err := os.MkdirAll(OsirisDir, 0o755); err != nil {
		return err
	}
	stamp := []byte(time.Now().UTC().Format(time.RFC3339Nano) + "\n")
	if err := os.WriteFile(WriteProbeFile, stamp, 0o644); err != nil {
		return err
	}
	return nil
}

// gatewayProbe implements §5.4's "gatewayHealthy (omniroute /healthz 60초
// 주기, 연속 2회 실패까지 관용)".
//
// It hits ONLY the health path. It never posts to /v1/messages, never sends a
// model name, and never spends a token. The request goes through the egress
// proxy exactly like the child's LLM traffic does (A3) so that a probe success
// actually proves the path the child will use — a probe that bypassed the
// proxy would happily report "healthy" while every real call was being denied
// by squid.
type gatewayProbe struct {
	client        *http.Client
	consecFail    int
	tolerate      int
	lastErr       string
	lastCheckedAt time.Time
}

func newGatewayProbe(proxyURL, noProxy string) *gatewayProbe {
	transport := &http.Transport{
		DisableKeepAlives:   false,
		MaxIdleConns:        2,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = proxyExcept(u, noProxy)
		}
	}
	return &gatewayProbe{
		client:   &http.Client{Timeout: gatewayProbeTimeout, Transport: transport},
		tolerate: gatewayFailTolerate,
	}
}

// proxyExcept mirrors, for the supervisor's OWN probe, the NO_PROXY semantics
// the child process gets through its environment.
//
// This must match or the probe lies. The child reaches the gateway according to
// HTTP_PROXY + NO_PROXY; if the probe ignored NO_PROXY it would try to CONNECT
// through squid to a host squid has no allowlist entry for (because it is an
// internal one that needs no proxy), report the gateway as unhealthy, and take
// the agent out of the assignment engine's candidate set — for a gateway that
// was working perfectly. The reverse mistake (probe direct, child proxied) hides
// a squid misconfiguration until the first real turn. Same list, same decision,
// one place.
func proxyExcept(proxy *url.URL, noProxy string) func(*http.Request) (*url.URL, error) {
	var hosts []string
	for _, h := range strings.Split(noProxy, ",") {
		if h = strings.TrimSpace(strings.ToLower(h)); h != "" {
			hosts = append(hosts, h)
		}
	}
	return func(r *http.Request) (*url.URL, error) {
		host := strings.ToLower(r.URL.Hostname())
		for _, h := range hosts {
			if host == h || strings.HasSuffix(host, "."+strings.TrimPrefix(h, ".")) {
				return nil, nil // direct
			}
		}
		return proxy, nil
	}
}

// check returns (healthy, changed). `healthy` is false only after
// tolerate+1 CONSECUTIVE failures, so one blip does not knock the agent out
// of the assignment engine's candidate set.
func (g *gatewayProbe) check(ctx context.Context, baseURL, healthPath string) bool {
	g.lastCheckedAt = time.Now()
	target := strings.TrimSuffix(baseURL, "/") + healthPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		g.fail(err.Error())
		return g.healthy()
	}
	resp, err := g.client.Do(req)
	if err != nil {
		g.fail(err.Error())
		return g.healthy()
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		g.fail(fmt.Sprintf("HTTP %d from %s", resp.StatusCode, target))
		return g.healthy()
	}
	if g.consecFail > 0 {
		log.Printf("supervisor: gateway health recovered (%s)", target)
	}
	g.consecFail = 0
	g.lastErr = ""
	return true
}

func (g *gatewayProbe) fail(reason string) {
	g.consecFail++
	g.lastErr = reason
	log.Printf("supervisor: gateway health check failed (%d consecutive, tolerating %d): %s", g.consecFail, g.tolerate, reason)
}

func (g *gatewayProbe) healthy() bool { return g.consecFail <= g.tolerate }
