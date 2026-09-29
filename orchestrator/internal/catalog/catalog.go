// Package catalog is the grant-catalog: the fixed, version-pinned Go map
// that derives the four permission layers (RBAC / network / Claude Code
// allowedTools / NATS ACL) from a single :Permission code
// (10-core-architecture.md §8.1, D13). Nobody edits this at runtime — it is
// versioned code, same as osiris-core's internal/graphdb/seed.go
// SystemPermissions (which this table is the mirror image of).
//
// CATALOG_VERSION must equal osiris-core's CATALOG_VERSION env
// (default "2026.08.1", see core/internal/config/config.go). This is checked
// at orchestrator startup and on every reconcile tick (internal/graphdb
// CheckCatalogVersion) against the :Permission.catalogVersion values core
// seeded — the same invariant core's own I9 checks, now actually
// cross-validated against a second, independent binary as
// IMPLEMENTATION-NOTES.md's I2-era note anticipated.
package catalog

import "sort"

// Version is intentionally a var, not a const: config.go allows an env
// override (CATALOG_VERSION) for the same reason osiris-core does — so a
// deliberate, coordinated version bump doesn't require a code change in two
// repos simultaneously. The DEFAULT below is what must match core's default.
// [2026-08-13] Bumped 2026.07.1 -> 2026.08.1 alongside this addition:
// actiontype.write (80-action-layer.md §4.5/§5.1, AD3's "새 권한 코드는
// 정확히 1개"). Following this file's own I9 precedent set by the
// 2026-07-27 org/console codes below ("adding these required bumping
// DefaultVersion above AND osiris-core's CATALOG_VERSION AND the seeded
// :Permission.catalogVersion in the same change") — CheckCatalogVersion
// itself only compares a version STRING against seeded :Permission rows and
// would not mechanically require a bump for a Table-only content change,
// but this codebase's established convention treats any grant-catalog
// content change as a version event, and diverging from that convention
// here (bumping core's default without this one, or vice versa) is exactly
// the drift I9 exists to catch. See core/internal/config/config.go,
// orchestrator/internal/config/config.go, deploy/osiris/values.yaml and
// docker-compose.yml — all four bumped together in this change.
const DefaultVersion = "2026.08.1"

// Grant is one row of the §8.1 grant-catalog table. RBAC and the [1단계]
// network-allowlist columns are recorded here for completeness/audit even
// though [1단계] enforces them elsewhere (RBAC: no-op per §1.3; network
// allowlist: already statically enforced by egress/squid.conf's ACLs, which
// predates this build — see IMPLEMENTATION-NOTES.md §1 item 2). What this
// build actually MATERIALIZES per grant is ClaudeAllowedTools (into
// settings.json) and the NATS subject lists (derived separately in
// internal/natsauth, keyed by team/isMaster rather than by grant, per §7.3 —
// NATS subjects are not a 1:1 function of Permission grants the way
// allowedTools is).
type Grant struct {
	ID                 string
	EgressAllowlist    []string // domain[:port] — documentation of the network layer; enforcement already lives in egress/squid.conf
	ClaudeAllowedTools []string
	Gate               string // "none" | "approval" — mirrors :Permission.gate

	// --- gated grants only (Gate == "approval") ---------------------------
	//
	// WHY THESE TWO COLUMNS EXIST (and why they are the design's own
	// instruction, not an invention): §8.2 flags an explicit `[미검증]` —
	// whether a `hooks` section in a settings.json passed via --settings is
	// honoured under `--bare` — and pre-commits to a fallback if it is not:
	//
	//	"로드되지 않으면 대안: (a) --bare를 빼고 --settings만 지정,
	//	 (b) MCP 툴 래핑 — git 조작을 osiris-git MCP 서버로만 노출하고
	//	 Bash에서 git을 deny. (b)가 더 견고하므로 훅이 안 되면 (b)로 간다."
	//
	// The I4 worker settled the question against the CLI itself instead of
	// guessing: `claude --help` (2.1.220) documents `--bare` as
	// "Minimal mode: skip hooks, LSP, plugin sync, ...". Hooks are NOT loaded.
	// A hooks section would therefore have been silently inert — the worst
	// possible outcome for a control that is supposed to stop a commit.
	//
	// So fallback (b) is what this catalog derives, per gated permission:
	//   MCPTools    → allowed, and routed through the supervisor's approval
	//                 gate, which publishes approval.request and waits.
	//   DeniedTools → the Bash form of the same capability, placed in
	//                 settings.json's permissions.deny so the model is told
	//                 "not this way, that way".
	//
	// Neither column is the real enforcement (§8.2's own table classifies
	// allowedTools/deny AND hooks alike as "협조" — cooperation, defeated by
	// prompt injection). The enforcement stays where §8.2 puts it: the agent's
	// git credential is read-only and osiris-core performs approved writes with
	// its own.
	MCPServers  []string // mcp.json server keys this grant unlocks
	MCPTools    []string // fully-qualified MCP tool names for --allowedTools
	DeniedTools []string // Bash forms to place in settings.json permissions.deny
}

// Table is the §8.1 excerpt, verbatim (same nine rows osiris-core seeds as
// :Permission nodes in internal/graphdb/seed.go SystemPermissions — kept as
// a literal here rather than fetched from Neo4j at runtime because §9.2 step
// 3 requires exactly this: an orchestrator-embedded catalog that the seeded
// graph is checked AGAINST, not derived FROM).
var Table = map[string]Grant{
	"fs.workspace.write": {
		ID: "fs.workspace.write", Gate: "none",
		ClaudeAllowedTools: []string{"Read", "Write", "Edit", "Glob", "Grep"},
	},
	"net.egress.pkg": {
		ID: "net.egress.pkg", Gate: "none",
		EgressAllowlist:    []string{"registry.npmjs.org", "pypi.org", "proxy.golang.org"},
		ClaudeAllowedTools: []string{"Bash(npm install:*)", "Bash(pip install:*)"},
	},
	"net.egress.github": {
		ID: "net.egress.github", Gate: "none",
		EgressAllowlist:    []string{"github.com", "api.github.com"},
		ClaudeAllowedTools: []string{"Bash(git fetch:*)", "Bash(git clone:*)"},
	},
	"llm.gateway": {
		ID: "llm.gateway", Gate: "none",
		// I5: switched from the shared public omniroute to the
		// framework-owned osiris-omniroute (osiris-llm network, reachable
		// only via osiris-egress — see docker-compose.yml and
		// IMPLEMENTATION-NOTES.md I5). This field is documentation-only in
		// [1단계] (D13 table: network layer is a no-op here, the real
		// enforcement is egress/squid.conf's static allowlist).
		EgressAllowlist: []string{"osiris-omniroute:20128"},
	},
	"git.commit": {
		ID: "git.commit", Gate: "approval",
		// `git add` stages nothing outside the workspace and has no external
		// effect, so it stays a plain allowed Bash tool — the agent needs it to
		// PREPARE a commit request. `git commit` itself is the external-effect
		// step and is denied here / routed through the gate.
		ClaudeAllowedTools: []string{"Bash(git add:*)", "Bash(git status:*)", "Bash(git diff:*)", "Bash(git log:*)"},
		MCPServers:         []string{"osiris-git"},
		MCPTools:           []string{"mcp__osiris-git__request_commit"},
		DeniedTools:        []string{"Bash(git commit:*)"},
	},
	"git.push": {
		ID: "git.push", Gate: "approval",
		MCPServers:  []string{"osiris-git"},
		MCPTools:    []string{"mcp__osiris-git__request_push"},
		DeniedTools: []string{"Bash(git push:*)"},
	},
	"msg.external.send": {
		ID: "msg.external.send", Gate: "approval",
		// §8.1's Claude Code column for this row is literally "MCP mailer.send
		// + 게이트" — this is that, wired.
		MCPServers: []string{"osiris-mailer"},
		MCPTools:   []string{"mcp__osiris-mailer__send"},
	},
	"spend.request": {
		ID: "spend.request", Gate: "approval",
		// §8.1: "MCP finance.request_spend + 게이트".
		MCPServers: []string{"osiris-finance"},
		MCPTools:   []string{"mcp__osiris-finance__request_spend"},
	},
	"k8s.read.ownteam": {
		ID: "k8s.read.ownteam", Gate: "none", // [1단계] no-op per §1.3
	},
	"exec.physical": {
		ID: "exec.physical", // human-only capability, not an execution grant — no columns
	},

	// --- [2026-07-27] 조직·콘솔 권한 코드 5개 (10 §8.1, 60 §20.4) ----------
	//
	// EVERY COLUMN IS EMPTY, AND THAT IS THE POINT. §8.1's own note: these are
	// codes osiris-api uses to authorize a CONSOLE request; the agent runtime
	// has no counterpart, so all four derivation layers are "—". The existing
	// exec.physical row above is the same shape and predates this change, so
	// this is a familiar form, not a new one.
	//
	// §8.1 states the inverse as a rule: an attempt to project any of these
	// into one of the four layers is a design error, because it would be a
	// path by which a human's console permission becomes an agent container's
	// capability. The empty rows exist ONLY so that AllowedTools/DeniedTools/
	// MCPServers skip them explicitly rather than through the "unknown grant
	// code" fallthrough — an unknown code and a deliberately-inert code must
	// not look the same to CheckCatalogVersion.
	//
	// I9: adding these required bumping DefaultVersion above AND osiris-core's
	// CATALOG_VERSION AND the seeded :Permission.catalogVersion in the same
	// change. Bumping one alone is caught at boot, which is the intent.
	"org.admin":           {ID: "org.admin"},
	"org.manage.people":   {ID: "org.manage.people"},
	"org.manage.roles":    {ID: "org.manage.roles"},
	"org.read.ops":        {ID: "org.read.ops"},
	"org.read.onboarding": {ID: "org.read.onboarding"},

	// --- [2026-08-13] actiontype.write (80-action-layer.md §4.5/§5.1) -----
	//
	// Same empty-row shape as the five rows immediately above, and for the
	// same reason: this is a DEFINITION-PLANE permission an :ActionType
	// edit route in osiris-api authorizes against, with no agent-runtime
	// counterpart. AC6 (80 §9, "HumanOnly 집합은 닫힌 열거이며, 그 원소 중
	// grant-catalog에서 ClaudeAllowedTools/MCPTools/EgressAllowlist 중
	// 하나라도 비어 있지 않은 코드가 있으면 실패") makes that emptiness a
	// hard requirement, not a convenience: actiontype.write is a HumanOnly
	// member (api/internal/authz/humanonly.go), and the day a tool/MCP
	// server/egress rule appears on this row, an :AIAgent gains a way to
	// exercise the permission through the tool layer even though DG6
	// refuses it at the RBAC layer — bin/lint-action-layer.sh's AC6 section
	// greps for exactly that.
	"actiontype.write": {ID: "actiontype.write"},
}

// BaselineAllowedTools is what EVERY agent gets regardless of its
// PermissionProfile, because without it an agent cannot read its own workspace
// or reason about it at all. It is deliberately tiny and deliberately
// side-effect-free: read-only file access plus the task-tracking tool.
//
// Note fs.workspace.write's row in §8.1 already grants Read/Write/Edit/Glob/
// Grep, so this baseline is NOT a way to smuggle write access in — an agent
// without fs.workspace.write can Read and Grep but cannot Write or Edit.
var BaselineAllowedTools = []string{"Read", "Glob", "Grep", "TodoWrite"}

// BaselineDeniedTools is the deny list every agent carries.
//
// §8.2's principle — "막히지 않으면 회사가 손해를 보는가?에 예라면 반드시 강제
// 층에서 막는다" — says a deny list is not a last line of defence. These entries
// are therefore not pretending to be one; they exist so that the model is
// steered to the MCP/approval path instead of discovering that the shell way is
// blocked three layers down (a denied Bash call produces a clear refusal the
// model can act on, whereas an egress-blocked curl produces a confusing
// timeout). The real boundaries remain the network (squid allowlist / no route
// to Neo4j), the container (read-only rootfs, cap_drop ALL), and the NATS ACL.
var BaselineDeniedTools = []string{
	// Credential exfiltration and self-modification of injected config.
	"Read(/var/run/osiris/creds/**)",
	"Write(/var/run/osiris/**)",
	"Edit(/var/run/osiris/**)",
	// Bypassing the egress proxy's audit trail by talking to the socket layer
	// directly. Squid still enforces the allowlist; this makes the intent
	// explicit and the refusal legible.
	"Bash(nc:*)",
	"Bash(ncat:*)",
	"Bash(socat:*)",
	// Rewriting one's own credentials/permissions is never a task.
	"Bash(sudo:*)",
	"Bash(su:*)",
	"Bash(chmod:*)",
	"Bash(chown:*)",
}

// AllowedTools returns the deduplicated union of the baseline plus each
// grant's ClaudeAllowedTools and MCPTools, for the given (already
// effective(party)-resolved) grant codes. Unknown grant codes are silently
// skipped rather than erroring — a :Permission the graph knows about but this
// catalog doesn't (version drift) is caught by CheckCatalogVersion, not here.
//
// This single function is the "Claude Code" column of §8.1's four-layer
// derivation, and it feeds BOTH the `--allowedTools` argument and
// settings.json's permissions.allow. Deriving them from one place is what makes
// them incapable of disagreeing.
func AllowedTools(grants []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		out = append(out, t)
	}
	for _, t := range BaselineAllowedTools {
		add(t)
	}
	for _, g := range grants {
		row, ok := Table[g]
		if !ok {
			continue
		}
		for _, t := range row.ClaudeAllowedTools {
			add(t)
		}
		for _, t := range row.MCPTools {
			add(t)
		}
	}
	sort.Strings(out)
	return out
}

// DeniedTools returns the baseline deny list plus every gated grant's denied
// Bash form. A grant the agent does NOT hold contributes nothing — there is no
// need to deny `git push` to an agent that was never told the tool exists, and
// listing it would only bloat the model's context.
func DeniedTools(grants []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		out = append(out, t)
	}
	for _, t := range BaselineDeniedTools {
		add(t)
	}
	for _, g := range grants {
		row, ok := Table[g]
		if !ok {
			continue
		}
		for _, t := range row.DeniedTools {
			add(t)
		}
	}
	sort.Strings(out)
	return out
}

// MCPServers returns the sorted set of MCP server names the grant set unlocks.
// §11 issue 9's constraint is respected structurally: this can never exceed the
// three servers in the table, well under the "에이전트당 MCP 서버 10개" review
// trigger and under the "고정 5개 이하" claim §11 relies on to justify not
// needing MCP tool search.
func MCPServers(grants []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range grants {
		row, ok := Table[g]
		if !ok {
			continue
		}
		for _, s := range row.MCPServers {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

// EgressAllowlist returns the union of the network-layer domains the grant set
// implies. [1단계] enforcement lives in egress/squid.conf (static), so this is
// reported for audit/diff purposes: a grant whose domain is NOT in squid.conf
// is a silent misconfiguration, and having the derived list in one function
// makes that comparison possible instead of theoretical.
func EgressAllowlist(grants []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range grants {
		row, ok := Table[g]
		if !ok {
			continue
		}
		for _, d := range row.EgressAllowlist {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	sort.Strings(out)
	return out
}
