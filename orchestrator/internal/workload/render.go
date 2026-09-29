package workload

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RoleContext is everything RenderPrompt needs to substitute
// :Role.promptTemplateMd's placeholders (20-ontology-schema.md §3.2.1). This
// is the ONLY LLM-authored text in the whole system (the template itself,
// authored once at :Role creation time by onboarding's LLM) — everything
// this function does to it is plain deterministic string substitution,
// never a model call (compute-over-inference, A4).
type RoleContext struct {
	RoleTitle        string
	Responsibilities string
	RoleLevel        int
	TeamName         string
	TeamSlug         string
	Capabilities     []CapabilityRef // {{capabilities}}
	Grants           []string        // {{effectivePermissions}}
	IsMaster         bool
	IsLead           bool
}

type CapabilityRef struct {
	Code  string
	Label string
	Level int
}

var placeholderRe = regexp.MustCompile(`\{\{([a-zA-Z0-9_.]+)\}\}`)

// allowedPlaceholders is §3.2.1's exact whitelist. Anything outside this
// list is rejected rather than silently left in the rendered file or
// silently substituted with "" — a template is not allowed to become
// arbitrary code, and a typo'd placeholder must fail loudly, not render a
// CLAUDE.md with a literal "{{typo}}" in it.
var allowedPlaceholders = map[string]bool{
	"role.title": true, "role.responsibilitiesSummary": true, "role.level": true,
	"team.name": true, "team.slug": true,
	"capabilities": true, "effectivePermissions": true,
	"isMaster": true, "isLead": true,
}

// RenderPrompt performs §3.2.1's "결정적 문자열 치환" step. It never calls an
// LLM — the only LLM authorship happened once, earlier, when promptTemplateMd
// itself was written (by onboarding's agent-master/bootstrap-master), which
// is out of this build's scope (see IMPLEMENTATION-NOTES.md: onboarding
// conversation is excluded).
func RenderPrompt(templateMd string, rc RoleContext) (string, error) {
	var badTokens []string
	seenBad := map[string]bool{}
	for _, m := range placeholderRe.FindAllStringSubmatch(templateMd, -1) {
		if !allowedPlaceholders[m[1]] && !seenBad[m[1]] {
			seenBad[m[1]] = true
			badTokens = append(badTokens, m[1])
		}
	}
	if len(badTokens) > 0 {
		return "", fmt.Errorf("promptTemplateMd references disallowed placeholder(s) %v (allowed: %v)", badTokens, sortedKeys(allowedPlaceholders))
	}

	values := map[string]string{
		"role.title":                   rc.RoleTitle,
		"role.responsibilitiesSummary": rc.Responsibilities,
		"role.level":                   strconv.Itoa(rc.RoleLevel),
		"team.name":                    rc.TeamName,
		"team.slug":                    rc.TeamSlug,
		"capabilities":                 renderCapabilities(rc.Capabilities),
		"effectivePermissions":         strings.Join(SortedGrants(rc.Grants), ", "),
		"isMaster":                     strconv.FormatBool(rc.IsMaster),
		"isLead":                       strconv.FormatBool(rc.IsLead),
	}

	return placeholderRe.ReplaceAllStringFunc(templateMd, func(tok string) string {
		key := placeholderRe.FindStringSubmatch(tok)[1]
		return values[key]
	}), nil
}

func renderCapabilities(caps []CapabilityRef) string {
	if len(caps) == 0 {
		return "(none declared)"
	}
	sorted := append([]CapabilityRef(nil), caps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Code < sorted[j].Code })
	parts := make([]string, 0, len(sorted))
	for _, c := range sorted {
		parts = append(parts, fmt.Sprintf("%s(%s)@%d", c.Label, c.Code, c.Level))
	}
	return strings.Join(parts, ", ")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// claudeSettings mirrors the subset of Claude Code's settings.json OSiris
// derives from the graph. §8.1's third layer, materialized.
//
// WHAT IS DELIBERATELY ABSENT: a `hooks` section. The I3 worker's hand-off note
// expected I4 to "확장해서 채울 자리" for the §8.2 PreToolUse gate. I4 checked
// the CLI instead of assuming: `claude --help` (2.1.220) documents `--bare` as
// "Minimal mode: skip hooks, LSP, plugin sync, ...", and §5.2's command line is
// built on `--bare`. A hooks section here would be loaded by nothing and
// enforce nothing while LOOKING like a security control — strictly worse than
// its absence. §8.2 anticipated exactly this and named the fallback: "(b) MCP
// 툴 래핑 ... (b)가 더 견고하므로 훅이 안 되면 (b)로 간다." That is what is
// rendered instead (see RenderMCP and internal/catalog's MCPTools/DeniedTools).
type claudeSettings struct {
	Permissions claudePermissions `json:"permissions"`
	Env         map[string]string `json:"env,omitempty"`
	// Reported so an operator reading the file inside a container can tell
	// which OrgRevision-derived grant set produced it without cross-checking
	// the graph. Ignored by the CLI (unknown keys are tolerated) and included
	// in ConfigHash, which is correct: a grant-set change SHOULD trigger a
	// no-downtime ReloadConfig (A12).
	OsirisDerivedFrom osirisProvenance `json:"$osiris,omitempty"`
}

type claudePermissions struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
	// "deny" wins over "allow" in Claude Code's permission resolution, which
	// is what makes the gated-Bash-form denial in §8.2's fallback (b)
	// meaningful even though the same grant also allows `Bash(git add:*)`.
	DefaultMode string `json:"defaultMode,omitempty"`
}

type osirisProvenance struct {
	Grants          []string `json:"grants"`
	MCPServers      []string `json:"mcpServers,omitempty"`
	EgressAllowlist []string `json:"egressAllowlist,omitempty"`
	CatalogVersion  string   `json:"catalogVersion"`
	Note            string   `json:"note"`
}

// SettingsInput is the fully-resolved four-layer derivation result the caller
// (reconcile.buildSpec, via internal/catalog) hands to the renderer. This
// package stays catalog-agnostic on purpose: the grant-catalog is
// version-pinned code that the graph is validated AGAINST (§9.2 step 3), and
// mixing it into the renderer would make "which catalog version produced this
// file" unanswerable.
type SettingsInput struct {
	AllowedTools    []string
	DeniedTools     []string
	MCPServers      []string
	EgressAllowlist []string
	CatalogVersion  string

	grants []string // set via WithGrants; provenance only
}

// RenderSettings derives settings.json from the effective grant set.
func RenderSettings(in SettingsInput) (string, error) {
	allow := dedupeSorted(in.AllowedTools)
	deny := dedupeSorted(in.DeniedTools)

	s := claudeSettings{
		Permissions: claudePermissions{
			Allow: allow,
			Deny:  deny,
			// §5.2 passes --permission-mode dontAsk on the command line; naming
			// it here as well means a future invocation that forgets the flag
			// still cannot sit waiting for an interactive answer that nobody is
			// there to give (a non-interactive agent blocked on a permission
			// prompt would burn its whole ack_wait and look like a hang).
			DefaultMode: "dontAsk",
		},
		OsirisDerivedFrom: osirisProvenance{
			Grants:          SortedGrants(in.Grants()),
			MCPServers:      in.MCPServers,
			EgressAllowlist: in.EgressAllowlist,
			CatalogVersion:  in.CatalogVersion,
			Note: "Derived by osiris-orchestrator from Neo4j intent via the version-pinned " +
				"grant-catalog (10-core-architecture.md §8.1 D13). Do not edit: it is " +
				"re-rendered on every OrgRevision change and your edits will be overwritten.",
		},
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

// Grants is a tiny accessor so the provenance block can record the grant codes
// without SettingsInput growing a duplicate field the caller has to keep in
// sync with AllowedTools.
func (in SettingsInput) Grants() []string { return in.grants }

// WithGrants attaches the grant codes for the provenance block.
func (in SettingsInput) WithGrants(g []string) SettingsInput { in.grants = g; return in }

// RenderAllowedTools renders the `--allowedTools` argument list as a file
// (one tool per line) at /var/run/osiris/config/allowed_tools.
//
// Why a FILE and not an env var or a compose-side argument: §5.2 passes
// --allowedTools on the command line, but the list is derived from the graph
// and therefore changes with OrgRevision. Putting it in the environment would
// (a) require a container recreate to change it, defeating A12's whole point,
// and (b) put a permission decision in a place G5 already teaches us not to
// use. As a rendered file it lands in ConfigHash next to CLAUDE.md and
// settings.json, so a permission change is a no-downtime ReloadConfig that the
// supervisor picks up on the next task (§5.6).
func RenderAllowedTools(allowedTools []string) string {
	var b strings.Builder
	b.WriteString("# GENERATED by osiris-orchestrator from Neo4j intent via the version-pinned\n")
	b.WriteString("# grant-catalog (10-core-architecture.md §8.1). One --allowedTools entry per\n")
	b.WriteString("# line; '#' comments and blank lines are ignored by the supervisor.\n")
	for _, t := range dedupeSorted(allowedTools) {
		b.WriteString(t)
		b.WriteString("\n")
	}
	return b.String()
}

// mcpServerSpec is one entry of mcp.json's mcpServers map (stdio transport).
type mcpServerSpec struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type mcpConfig struct {
	MCPServers map[string]mcpServerSpec `json:"mcpServers"`
}

// SupervisorBinaryPath is where the agent image places the supervisor binary.
// The MCP servers are the SAME binary re-invoked with -mcp-server, so the agent
// image needs no additional runtime and the tool implementations cannot drift
// away from the gate they call.
const SupervisorBinaryPath = "/osiris-supervisor"

// RenderMCP renders mcp.json for the given MCP server names (already derived
// from the grant set by internal/catalog).
//
// This replaces the I3-era placeholder that always emitted `{"mcpServers": {}}`.
// Each server is `osiris-supervisor -mcp-server <name>` over stdio; the server
// exposes only the tools §8.1's Claude Code column names for that grant
// (mailer.send, finance.request_spend, and the git commit/push request pair),
// and every one of those tools does exactly one thing: publish
// `approval.request` and report back what the human decided. §8.2: "★ 실제
// 커밋/푸시는 osiris-core가 자기 자격증명으로 수행한다. 에이전트에게는
// '승인되어 반영됨'만 통보된다."
//
// An agent with no gated grants gets `{"mcpServers": {}}` — same bytes as
// before, so nothing about the no-gates case changed.
func RenderMCP(servers []string) string {
	cfg := mcpConfig{MCPServers: map[string]mcpServerSpec{}}
	for _, name := range dedupeSorted(servers) {
		cfg.MCPServers[name] = mcpServerSpec{
			Command: SupervisorBinaryPath,
			Args:    []string{"-mcp-server", name},
			// No credentials here. The MCP subprocess talks ONLY to the
			// supervisor's loopback gate; it never holds the gateway token, the
			// core token or the git PAT.
			Env: map[string]string{"OSIRIS_MCP_SERVER": name},
		}
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		// Cannot happen for this shape; returning the empty-but-valid config is
		// still better than an unparseable file, which would fail the
		// supervisor's readiness with CONFIG_INVALID.
		return "{\n  \"mcpServers\": {}\n}\n"
	}
	return string(b) + "\n"
}

func dedupeSorted(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range in {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
