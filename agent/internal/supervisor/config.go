package supervisor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RenderedConfig is the driver-rendered configuration the supervisor re-reads
// from disk. It is re-read at the START OF EVERY TASK, never mutated
// mid-turn — §5.6: "supervisor가 재렌더 후 다음 태스크부터 적용(진행 중 턴은
// 건드리지 않는다)". That single rule is why `agent.<id>.config.reload` needs
// no in-flight coordination at all: the reload notification only has to make
// the NEXT read happen, and the next read happens anyway.
type RenderedConfig struct {
	Prompt       string // CLAUDE.md contents
	SettingsPath string
	MCPPath      string
	AllowedTools []string
	Hash         string
}

// loadConfig reads and validates the three rendered files plus the derived
// allowedTools list. Validation is real, not nominal: settings.json and
// mcp.json must PARSE as JSON here, because §5.4's readyz clause is "렌더된
// 설정 유효 (파싱 성공, configHash 계산됨)" and because `claude -p` silently
// ignores a settings file that fails validation (its own --help says so) —
// which would turn a broken render into an agent running with NO permission
// restrictions instead of a visibly unready one.
func loadConfig() (RenderedConfig, error) {
	var rc RenderedConfig

	promptPath := filepath.Join(ConfigDir, ConfigPrompt)
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		return rc, fmt.Errorf("read %s: %w", promptPath, err)
	}
	rc.Prompt = string(prompt)

	rc.SettingsPath = filepath.Join(ConfigDir, ConfigSettings)
	settings, err := os.ReadFile(rc.SettingsPath)
	if err != nil {
		return rc, fmt.Errorf("read %s: %w", rc.SettingsPath, err)
	}
	var probe any
	if err := json.Unmarshal(settings, &probe); err != nil {
		return rc, fmt.Errorf("%s is not valid JSON: %w", rc.SettingsPath, err)
	}

	rc.MCPPath = filepath.Join(ConfigDir, ConfigMCP)
	mcp, err := os.ReadFile(rc.MCPPath)
	if err != nil {
		return rc, fmt.Errorf("read %s: %w", rc.MCPPath, err)
	}
	if err := json.Unmarshal(mcp, &probe); err != nil {
		return rc, fmt.Errorf("%s is not valid JSON: %w", rc.MCPPath, err)
	}

	toolsPath := filepath.Join(ConfigDir, ConfigAllowedTools)
	tools, err := os.ReadFile(toolsPath)
	if err != nil {
		return rc, fmt.Errorf("read %s: %w", toolsPath, err)
	}
	for _, line := range strings.Split(string(tools), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			rc.AllowedTools = append(rc.AllowedTools, line)
		}
	}

	sum := sha256.New()
	sum.Write(prompt)
	sum.Write([]byte{0})
	sum.Write(settings)
	sum.Write([]byte{0})
	sum.Write(mcp)
	sum.Write([]byte{0})
	sum.Write(tools)
	rc.Hash = hex.EncodeToString(sum.Sum(nil))[:16]
	return rc, nil
}

// Creds is the credential set, ALWAYS read fresh from disk — see
// loadCreds' comment for why re-reading rather than caching is the whole
// mechanism behind §8.4's "재시작 없이 반영".
type Creds struct {
	NATSUser          string
	NATSPassword      string
	CoreToken         string
	GatewayToken      string
	GatewayBaseURL    string
	GatewayHealthPath string
	GitToken          string

	// Fingerprint changes iff any credential VALUE changed. The supervisor
	// compares fingerprints (not mtimes) so that an orchestrator tick that
	// rewrites an unchanged file — or a bind-mount whose mtime granularity
	// is coarse — never triggers a spurious NATS reconnect.
	Fingerprint string
}

// loadCreds reads every credential file from CredsDir on every call.
//
// THIS IS THE CREDENTIAL-ROTATION DELIVERY MECHANISM (§8.4), and it is
// deliberately the dumbest possible one:
//
//	게이트웨이 토큰 ... "exec 시점에 파일을 읽으므로 재시작 없이 반영"
//
// The design already specifies read-at-exec for the gateway token; this
// function generalizes it to all credentials. Because the credential
// directory is a bind mount (or a projected Secret in [2단계]) of a host path
// the orchestrator owns, the orchestrator overwriting a file there is
// INSTANTLY visible inside the running container — no signal, no restart, no
// hash change, no config-reload event. That is exactly the property §8.4
// demands and exactly why AgentWorkloadSpec.Credentials is excluded from both
// SpecHash and ConfigHash (that exclusion is correct; what was missing was a
// writer that keeps pushing fresh files at an ALREADY-RUNNING container —
// see orchestrator's WorkloadDriver.SyncCredentials).
//
// Missing optional files (git_token) are not an error. Missing required ones
// (nats_user/nats_password) fail readiness rather than crashing the process:
// a credential directory that has not been populated yet is a normal boot
// race, and §5.4 already has the right place to express it (readyz=false with
// reason CREDENTIALS_UNAVAILABLE → the assignment engine's UNAVAILABLE hard
// filter, no new tasks, no restart loop).
func loadCreds() (Creds, error) {
	var c Creds
	read := func(name string, required bool) (string, error) {
		b, err := os.ReadFile(filepath.Join(CredsDir, name))
		if err != nil {
			if os.IsNotExist(err) && !required {
				return "", nil
			}
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}

	var err error
	if c.NATSUser, err = read(CredNATSUser, true); err != nil {
		return c, err
	}
	if c.NATSPassword, err = read(CredNATSPassword, true); err != nil {
		return c, err
	}
	if c.CoreToken, err = read(CredCoreToken, false); err != nil {
		return c, err
	}
	if c.GatewayToken, err = read(CredGatewayToken, true); err != nil {
		return c, err
	}
	if c.GatewayBaseURL, err = read(CredGatewayBaseURL, true); err != nil {
		return c, err
	}
	if c.GatewayHealthPath, err = read(CredGatewayHealthPh, false); err != nil {
		return c, err
	}
	if c.GitToken, err = read(CredGitToken, false); err != nil {
		return c, err
	}

	if c.NATSUser == "" || c.NATSPassword == "" {
		return c, fmt.Errorf("nats_user/nats_password are empty")
	}
	if c.GatewayBaseURL == "" {
		return c, fmt.Errorf("gateway_base_url is empty")
	}
	if c.GatewayHealthPath == "" {
		// §9.2 contract item 4: "GET /healthz 무인증, 100ms 이내". Kept as
		// the code default even though this host's omniroute build does not
		// expose it (see IMPLEMENTATION-NOTES.md) — the deviation belongs in
		// configuration, not in the code's idea of the contract.
		c.GatewayHealthPath = "/healthz"
	}

	sum := sha256.Sum256([]byte(strings.Join([]string{
		c.NATSUser, c.NATSPassword, c.CoreToken, c.GatewayToken,
		c.GatewayBaseURL, c.GatewayHealthPath, c.GitToken,
	}, "\x00")))
	c.Fingerprint = hex.EncodeToString(sum[:])[:16]
	return c, nil
}
