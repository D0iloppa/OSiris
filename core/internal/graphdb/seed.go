package graphdb

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// --- capability catalog (docs/design/20-ontology-schema.md §5.2) ----------

type capSeed struct {
	Code    string
	Label   string
	Kind    string // technical | domain | physical | soft
	Broader string // "" for the root
}

// SystemCapabilities is the fixed taxonomy from §5.2, catalogVersion
// "2026.07". This is system-owned: users cannot add to it (§9.2 step 2),
// so it lives in code, not in a human-editable file.
var SystemCapabilities = []capSeed{
	{Code: "cap.root", Label: "전체", Kind: "domain", Broader: ""},

	{Code: "cap.tech", Label: "기술", Kind: "domain", Broader: "cap.root"},
	{Code: "cap.code", Label: "코드 작성", Kind: "technical", Broader: "cap.tech"},
	{Code: "cap.code.python", Label: "Python", Kind: "technical", Broader: "cap.code"},
	{Code: "cap.code.go", Label: "Go", Kind: "technical", Broader: "cap.code"},
	{Code: "cap.code.ts", Label: "TypeScript", Kind: "technical", Broader: "cap.code"},
	{Code: "cap.code.review", Label: "코드 리뷰", Kind: "technical", Broader: "cap.code"},
	{Code: "cap.infra", Label: "인프라", Kind: "technical", Broader: "cap.tech"},
	{Code: "cap.infra.k8s", Label: "쿠버네티스", Kind: "technical", Broader: "cap.infra"},
	{Code: "cap.infra.network", Label: "네트워크", Kind: "technical", Broader: "cap.infra"},
	{Code: "cap.infra.db", Label: "데이터베이스", Kind: "technical", Broader: "cap.infra"},
	{Code: "cap.data", Label: "데이터", Kind: "technical", Broader: "cap.tech"},
	{Code: "cap.data.analysis", Label: "데이터 분석", Kind: "technical", Broader: "cap.data"},
	{Code: "cap.data.pipeline", Label: "데이터 파이프라인", Kind: "technical", Broader: "cap.data"},

	{Code: "cap.biz", Label: "경영·회계", Kind: "domain", Broader: "cap.root"},
	{Code: "cap.biz.accounting", Label: "회계", Kind: "domain", Broader: "cap.biz"},
	{Code: "cap.biz.budgeting", Label: "예산", Kind: "domain", Broader: "cap.biz"},
	{Code: "cap.biz.contract", Label: "계약", Kind: "domain", Broader: "cap.biz"},
	{Code: "cap.biz.procurement", Label: "구매", Kind: "domain", Broader: "cap.biz"},

	{Code: "cap.mkt", Label: "마케팅", Kind: "domain", Broader: "cap.root"},
	{Code: "cap.mkt.research", Label: "시장 조사", Kind: "domain", Broader: "cap.mkt"},
	{Code: "cap.mkt.copy", Label: "카피라이팅", Kind: "domain", Broader: "cap.mkt"},
	{Code: "cap.mkt.creative", Label: "크리에이티브", Kind: "domain", Broader: "cap.mkt"},
	{Code: "cap.mkt.media_buying", Label: "미디어 바잉", Kind: "domain", Broader: "cap.mkt"},
	{Code: "cap.mkt.reporting", Label: "리포팅", Kind: "domain", Broader: "cap.mkt"},

	{Code: "cap.ops", Label: "총무·운영", Kind: "domain", Broader: "cap.root"},
	{Code: "cap.ops.facility", Label: "시설 관리", Kind: "physical", Broader: "cap.ops"},
	{Code: "cap.ops.equipment", Label: "장비 관리", Kind: "physical", Broader: "cap.ops"},
	{Code: "cap.ops.vendor", Label: "벤더 관리", Kind: "domain", Broader: "cap.ops"},

	{Code: "cap.exec", Label: "실행 양태", Kind: "soft", Broader: "cap.root"},
	{Code: "cap.exec.physical", Label: "물리 실행", Kind: "physical", Broader: "cap.exec"},
	{Code: "cap.exec.digital", Label: "디지털 실행", Kind: "soft", Broader: "cap.exec"},
	{Code: "cap.exec.decision", Label: "의사결정 실행", Kind: "soft", Broader: "cap.exec"},
}

// --- permission catalog (§7.2, §8.1 grant-catalog excerpt) -----------------

type permSeed struct {
	Code         string
	Gate         string // none | approval
	ApproverRole string // "" | team-lead | admin
	LimitJSON    string // "" if none
}

// SystemPermissions mirrors the grant-catalog table in
// docs/design/10-core-architecture.md §8.1. The full grant-catalog (RBAC /
// network / Claude Code / NATS ACL derivation) lives in osiris-orchestrator,
// which does not exist yet in this build (compose placeholder only). Until
// it does, osiris-core is the sole keeper of catalogVersion — see
// IMPLEMENTATION-NOTES.md for why I9 is checked against a constant in this
// binary rather than a second running service.
var SystemPermissions = []permSeed{
	{Code: "fs.workspace.write", Gate: "none"},
	{Code: "net.egress.pkg", Gate: "none"},
	{Code: "net.egress.github", Gate: "none"},
	{Code: "llm.gateway", Gate: "none"},
	{Code: "git.commit", Gate: "approval", ApproverRole: "team-lead"},
	{Code: "git.push", Gate: "approval", ApproverRole: "team-lead"},
	{Code: "msg.external.send", Gate: "approval", ApproverRole: "team-lead"},
	{Code: "spend.request", Gate: "approval", ApproverRole: "admin", LimitJSON: `{"currency":"KRW","max":1000000}`},
	{Code: "k8s.read.ownteam", Gate: "none"},

	// --- [2026-07-27] 조직·콘솔 권한 코드 5개 (60-identity-authn.md §20.4,
	// 20-ontology-schema.md §9.2 step 3, 10-core-architecture.md §8.1) ------
	//
	// These five are consumed ONLY by osiris-api when it authorizes a console
	// request. Their four-layer projection (RBAC / network / Claude Code /
	// NATS ACL) is deliberately all-no-op, exactly like the existing
	// exec.physical row — §8.1 says in as many words that projecting them into
	// the agent runtime would be a design error, because a human's console
	// permission would then leak into an agent container's capabilities.
	//
	// I9 REQUIREMENT — the reason CATALOG_VERSION moved with this change:
	// :Permission.catalogVersion and the orchestrator's embedded grant-catalog
	// (orchestrator/internal/catalog) must carry the SAME version. Adding
	// rows here without bumping both is exactly the mismatch I9 exists to
	// catch, and it fails at boot rather than silently.
	//
	// None of them is the target of any :Capability's REQUIRES_PERMISSION
	// edge (see requiresPermission below — deliberately untouched), so I7
	// (INERT_CAPABILITY) is unaffected.
	{Code: "org.admin", Gate: "none"},
	{Code: "org.manage.people", Gate: "none"},
	{Code: "org.manage.roles", Gate: "none"},
	{Code: "org.read.ops", Gate: "none"},
	{Code: "org.read.onboarding", Gate: "none"},

	// --- [2026-08-13] actiontype.write (80-action-layer.md §4.5/§5.1,
	// AD3's "새 권한 코드는 정확히 1개") ------------------------------------
	//
	// Gate: "none" here mirrors the five rows immediately above, not a new
	// judgment — same reasoning: this :Permission.gate field is never read
	// by any consumer today (orchestrator's Grant.Gate mirror is unread,
	// 80 §10 G-A11), and the org/console codes already established "none"
	// as this build's convention for a permission whose real gate is "you
	// must hold the code at all", not an approval workflow. The orchestrator
	// grant-catalog row (catalog.go) is likewise the same empty-row shape as
	// those five — see that file's comment for AC6's reason this MUST stay
	// empty. I9 REQUIREMENT, same as 2026-07-27's precedent: this addition
	// bumps CatalogVersion's default (config.go) and orchestrator's
	// DefaultVersion (catalog.go) together in this change.
	{Code: "actiontype.write", Gate: "none"},
}

// requiresPermission is the §7.2 bridge (:Capability)-[:REQUIRES_PERMISSION]->(:Permission).
// The design states the bridge exists but does not enumerate every edge for
// the full §5.2 taxonomy; this subset is this worker's judgment call
// (documented in IMPLEMENTATION-NOTES.md) chosen to make I7/I9 and the
// assignment hard-filter NOT_PERMITTED meaningful without inventing
// permissions the design never named.
var requiresPermission = map[string][]string{
	"cap.code.python":      {"git.commit"},
	"cap.code.go":          {"git.commit"},
	"cap.code.ts":          {"git.commit"},
	"cap.biz.procurement":  {"spend.request"},
	"cap.mkt.media_buying": {"spend.request"},
	"cap.infra.k8s":        {"k8s.read.ownteam"},
}

// --- permission profiles (§9.2 step 5) -------------------------------------

type profileSeed struct {
	ID     string
	Name   string
	Grants []string
}

var SystemPermissionProfiles = []profileSeed{
	{ID: "pp-readonly", Name: "readonly", Grants: []string{"llm.gateway", "net.egress.pkg", "net.egress.github"}},
	{ID: "pp-builder", Name: "builder", Grants: []string{"llm.gateway", "net.egress.pkg", "net.egress.github", "fs.workspace.write", "git.commit", "git.push"}},
	{ID: "pp-finance", Name: "finance", Grants: []string{"llm.gateway", "spend.request"}},

	// --- [2026-07-27] 조직 역할 프로파일 3종 (60 §20.5, 20 §9.2 step 5) ----
	//
	// Same character as the three above: SYSTEM SEED, not fixed. An org may
	// clone and vary them (31 §2.4's "프레임워크 기본값"). Authorization never
	// looks at these ids — only at the :Permission codes they GRANT (H30,
	// checked by HI17), which is what makes renaming a role or a profile a
	// cosmetic act rather than a security event.
	//
	// pp-org-member GRANTS NOTHING, and that is not an omission (§20.5): what
	// an ordinary employee does in the console derives from existing facts
	// ("is anything assigned to me"), not from permission codes. The member
	// role exists so that "this person was explicitly given the minimum org
	// role" is a fact in the graph, distinguishable from "this person has no
	// org role at all" — a distinction I14 and DG4 compute against.
	{ID: "pp-org-admin", Name: "org-admin", Grants: []string{"org.admin", "org.manage.people", "org.manage.roles", "org.read.ops", "org.read.onboarding"}},
	{ID: "pp-org-manager", Name: "org-manager", Grants: []string{"org.manage.people", "org.manage.roles", "org.read.ops", "org.read.onboarding"}},
	{ID: "pp-org-member", Name: "org-member", Grants: []string{}},
}

// --- org roles (§9.2 step 8, 60 §20.5) -------------------------------------

type orgRoleSeed struct {
	ID        string
	Title     string
	Level     int
	ProfileID string
}

// SystemOrgRoles are the three default :Role{scope:'org'} instances.
//
// WHY INSTANCES AND NOT A Go ENUM (H30): an enum would create a SECOND
// authorization vocabulary standing next to the :Permission catalog, and
// nothing would ever catch the two drifting apart. It would also mean a code
// deployment every time an organization wants a role this table did not
// anticipate ("감사역", "재무담당", "팀장 대행"). The cost of instances is that
// ids and titles are mutable, which is why the rule "authorization reads
// :Permission.code and NOTHING else" is a lint (HI17) rather than a habit.
//
// I17: these carry NO REQUIRES_CAPABILITY and null defaultTeamId/isLead, which
// is what keeps them out of I4/I6/I11 and out of the assignment engine's
// candidate set (§8.3's IN_TEAM restriction is the other half of that lock).
var SystemOrgRoles = []orgRoleSeed{
	{ID: "role-org-admin", Title: "관리자", Level: 5, ProfileID: "pp-org-admin"},
	{ID: "role-org-manager", Title: "매니저", Level: 4, ProfileID: "pp-org-manager"},
	{ID: "role-org-member", Title: "구성원", Level: 1, ProfileID: "pp-org-member"},
}

// --- resource profiles (§9.2 step 6, §3.7 :RoleResourceProfile) ------------

type resourceProfileSeed struct {
	ID         string
	CPUMilli   int64
	MemMiB     int64
	StorageGiB int64
	Label      string
}

var SystemResourceProfiles = []resourceProfileSeed{
	{ID: "light", CPUMilli: 500, MemMiB: 512, StorageGiB: 5, Label: "경량 에이전트"},
	{ID: "standard", CPUMilli: 1000, MemMiB: 2048, StorageGiB: 10, Label: "표준 에이전트"},
	{ID: "heavy", CPUMilli: 2000, MemMiB: 4096, StorageGiB: 20, Label: "고성능 에이전트"},
}

// SeedResult reports what a seed pass did, for logging/observability. It is
// not itself written to the graph.
type SeedResult struct {
	CapabilitiesSeeded int
	PermissionsSeeded  int
	ProfilesSeeded     int
	ResourceProfiles   int
	OrgRolesSeeded     int
	OrgPresent         bool // organization + genesis OrgRevision{n:0} exist (idempotent MERGE — true on every run, not just first)
}

// SeedCatalog upserts steps 1-6 of §9.2 (:Organization + :OrgRevision{n:0},
// capability catalog, permission catalog, REQUIRES_PERMISSION bridge, base
// PermissionProfiles, RoleResourceProfiles). Every write is MERGE ... ON
// CREATE/ON MATCH (§9.1) so repeated calls — e.g. every osiris-core boot —
// are idempotent: node/relationship counts do not grow on re-run.
//
// This does NOT bump :OrgRevision. Revision 0 is the graph's genesis state,
// not a "change" — only operations after step 6 (team/role/person/agent/
// membership/policy, §9.2 steps 7-12) increment it, one at a time (C8).
func SeedCatalog(ctx context.Context, c *Client, catalogVersion, orgName, orgLegalName, orgTimezone string) (SeedResult, error) {
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	result := SeedResult{}

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		// Step 1: Organization + OrgRevision{n:0}
		orgPresent, err := seedOrganization(ctx, tx, orgName, orgLegalName, orgTimezone)
		if err != nil {
			return nil, fmt.Errorf("seed organization: %w", err)
		}
		result.OrgPresent = orgPresent

		// Step 2: capability catalog
		if err := seedCapabilities(ctx, tx, catalogVersion); err != nil {
			return nil, fmt.Errorf("seed capabilities: %w", err)
		}
		result.CapabilitiesSeeded = len(SystemCapabilities)

		// Step 3: permission catalog
		if err := seedPermissions(ctx, tx, catalogVersion); err != nil {
			return nil, fmt.Errorf("seed permissions: %w", err)
		}
		result.PermissionsSeeded = len(SystemPermissions)

		// Step 4: REQUIRES_PERMISSION bridge
		if err := seedRequiresPermission(ctx, tx); err != nil {
			return nil, fmt.Errorf("seed requires_permission: %w", err)
		}

		// Step 5: base PermissionProfiles
		if err := seedPermissionProfiles(ctx, tx); err != nil {
			return nil, fmt.Errorf("seed permission profiles: %w", err)
		}
		result.ProfilesSeeded = len(SystemPermissionProfiles)

		// Step 6: RoleResourceProfiles
		if err := seedResourceProfiles(ctx, tx); err != nil {
			return nil, fmt.Errorf("seed resource profiles: %w", err)
		}
		result.ResourceProfiles = len(SystemResourceProfiles)

		// Step 8 (partial): the three system :Role{scope:'org'} instances.
		//
		// §9.2 numbers this step 8 ("직무"), which onboarding otherwise fills
		// with the org's own job roles. The THREE ORG ROLES are different in
		// kind: they are framework defaults with no organization-specific
		// content, exactly like the permission profiles of step 5, so they are
		// seeded by the same code-owned, idempotent pass rather than waiting
		// for a conversation that may never define them. Without them the
		// first commit cannot satisfy I14 (an org.admin holder must exist),
		// because there would be no org-scoped role to hold.
		if err := seedOrgRoles(ctx, tx); err != nil {
			return nil, fmt.Errorf("seed org roles: %w", err)
		}
		result.OrgRolesSeeded = len(SystemOrgRoles)

		return nil, nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func seedOrganization(ctx context.Context, tx neo4j.ManagedTransaction, name, legalName, timezone string) (bool, error) {
	res, err := tx.Run(ctx, `
		MERGE (rev:OrgRevision {n: 0})
		ON CREATE SET rev.at = datetime(), rev.summary = "genesis", rev.changeKind = "bootstrap"
		WITH rev
		MERGE (o:Organization:Party {id: "org-root"})
		ON CREATE SET o.name = $name, o.legalName = $legalName, o.timezone = $timezone,
		               o.createdAtRevision = 0
		ON MATCH SET  o.name = $name, o.legalName = $legalName, o.timezone = $timezone
		RETURN o.id IS NOT NULL AS present
	`, map[string]any{"name": name, "legalName": legalName, "timezone": timezone})
	if err != nil {
		return false, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return false, err
	}
	present, _ := rec.Get("present")
	b, _ := present.(bool)
	return b, nil
}

func seedCapabilities(ctx context.Context, tx neo4j.ManagedTransaction, catalogVersion string) error {
	for _, cp := range SystemCapabilities {
		_, err := tx.Run(ctx, `
			MERGE (c:Capability {code: $code})
			ON CREATE SET c.id = $code, c.label = $label, c.kind = $kind, c.catalogVersion = $catalogVersion
			ON MATCH SET  c.label = $label, c.kind = $kind, c.catalogVersion = $catalogVersion
		`, map[string]any{"code": cp.Code, "label": cp.Label, "kind": cp.Kind, "catalogVersion": catalogVersion})
		if err != nil {
			return err
		}
	}
	for _, cp := range SystemCapabilities {
		if cp.Broader == "" {
			continue
		}
		_, err := tx.Run(ctx, `
			MATCH (specific:Capability {code: $specific})
			MATCH (general:Capability {code: $general})
			MERGE (specific)-[:BROADER]->(general)
		`, map[string]any{"specific": cp.Code, "general": cp.Broader})
		if err != nil {
			return err
		}
	}
	return nil
}

func seedPermissions(ctx context.Context, tx neo4j.ManagedTransaction, catalogVersion string) error {
	for _, p := range SystemPermissions {
		_, err := tx.Run(ctx, `
			MERGE (p:Permission {code: $code})
			ON CREATE SET p.id = $code, p.gate = $gate, p.approverRole = $approverRole,
			              p.limitJson = $limitJson, p.catalogVersion = $catalogVersion
			ON MATCH SET  p.gate = $gate, p.approverRole = $approverRole,
			              p.limitJson = $limitJson, p.catalogVersion = $catalogVersion
		`, map[string]any{
			"code": p.Code, "gate": p.Gate, "approverRole": p.ApproverRole,
			"limitJson": p.LimitJSON, "catalogVersion": catalogVersion,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func seedRequiresPermission(ctx context.Context, tx neo4j.ManagedTransaction) error {
	for capCode, perms := range requiresPermission {
		for _, permCode := range perms {
			_, err := tx.Run(ctx, `
				MATCH (c:Capability {code: $capCode})
				MATCH (p:Permission {code: $permCode})
				MERGE (c)-[:REQUIRES_PERMISSION]->(p)
			`, map[string]any{"capCode": capCode, "permCode": permCode})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func seedPermissionProfiles(ctx context.Context, tx neo4j.ManagedTransaction) error {
	for _, pr := range SystemPermissionProfiles {
		_, err := tx.Run(ctx, `
			MERGE (pp:PermissionProfile {id: $id})
			ON CREATE SET pp.name = $name, pp.version = 1
			ON MATCH SET  pp.name = $name
		`, map[string]any{"id": pr.ID, "name": pr.Name})
		if err != nil {
			return err
		}

		// ★ CONVERGENCE, NOT JUST CREATION. The previous version of this
		// function only ever MERGEd the wanted GRANTS edges, which makes a
		// re-run idempotent in the trivial sense (nothing grows) but NOT
		// convergent: remove a code from the table above and redeploy, and the
		// graph keeps granting it forever, because nothing ever deletes an
		// edge. IMPLEMENTATION-NOTES.md §50 records this exact class of bug
		// occurring in this repository once already.
		//
		// For a PERMISSION profile the consequence is a silent privilege
		// retention, so the desired set is stated positively and everything
		// outside it is removed. This is the same "explicit DELETE for the no
		// longer wanted edge" pattern writeops.go already applies to
		// USES_PROFILE and base HAS_PROFILE.
		//
		// This deletes only GRANTS edges of a SYSTEM-SEEDED profile — a
		// user-cloned profile is not in SystemPermissionProfiles and is never
		// touched by this loop.
		if _, err := tx.Run(ctx, `
			MATCH (pp:PermissionProfile {id: $id})-[g:GRANTS]->(perm:Permission)
			WHERE NOT perm.code IN $codes
			DELETE g
		`, map[string]any{"id": pr.ID, "codes": pr.Grants}); err != nil {
			return err
		}

		for _, grantCode := range pr.Grants {
			_, err := tx.Run(ctx, `
				MATCH (pp:PermissionProfile {id: $id})
				MATCH (perm:Permission {code: $code})
				MERGE (pp)-[:GRANTS]->(perm)
			`, map[string]any{"id": pr.ID, "code": grantCode})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// seedOrgRoles upserts the three :Role{scope:'org'} instances and their base
// permission profile edges (§9.2 step 8, 60 §20.5).
//
// Every property that I17 constrains is written EXPLICITLY, including the two
// nulls. Leaving defaultTeamId/isLead absent would work for a fresh graph but
// not for one where a role id was previously used as a job role, and "the
// invariant holds because nobody happened to set the field" is not something a
// checker can rely on.
func seedOrgRoles(ctx context.Context, tx neo4j.ManagedTransaction) error {
	for _, r := range SystemOrgRoles {
		if _, err := tx.Run(ctx, `
			MERGE (r:Role {id: $id})
			ON CREATE SET r.title = $title, r.level = $level, r.scope = 'org',
			              r.defaultTeamId = null, r.isLead = null,
			              r.responsibilitiesSummary = $summary,
			              r.promptTemplateMd = '', r.promptTemplateVersion = 1,
			              r.promptAuthoredAtRevision = 0
			ON MATCH SET  r.title = $title, r.level = $level, r.scope = 'org',
			              r.defaultTeamId = null, r.isLead = null,
			              r.responsibilitiesSummary = $summary
		`, map[string]any{
			"id": r.ID, "title": r.Title, "level": r.Level,
			"summary": "시스템 기본 조직 역할 (60-identity-authn.md §20.5)",
		}); err != nil {
			return err
		}

		// I17: an org-scoped role holds no capability requirements. Converge
		// rather than assume — a role id reused from a previous job role would
		// otherwise keep its REQUIRES_CAPABILITY edges and break I4/I6/I11
		// reasoning about org roles.
		if _, err := tx.Run(ctx, `
			MATCH (r:Role {id: $id})-[rc:REQUIRES_CAPABILITY]->()
			DELETE rc
		`, map[string]any{"id": r.ID}); err != nil {
			return err
		}

		if _, err := tx.Run(ctx, `
			MATCH (r:Role {id: $id})
			OPTIONAL MATCH (r)-[stale:HAS_PROFILE {kind: 'base'}]->(staleP:PermissionProfile)
			  WHERE staleP.id <> $profileId
			DELETE stale
			WITH r
			MATCH (p:PermissionProfile {id: $profileId})
			MERGE (r)-[:HAS_PROFILE {kind: 'base'}]->(p)
		`, map[string]any{"id": r.ID, "profileId": r.ProfileID}); err != nil {
			return err
		}
	}
	return nil
}

func seedResourceProfiles(ctx context.Context, tx neo4j.ManagedTransaction) error {
	for _, rp := range SystemResourceProfiles {
		_, err := tx.Run(ctx, `
			MERGE (rp:RoleResourceProfile {id: $id})
			ON CREATE SET rp.cpuMilli = $cpu, rp.memMiB = $mem, rp.storageGiB = $storage, rp.label = $label
			ON MATCH SET  rp.cpuMilli = $cpu, rp.memMiB = $mem, rp.storageGiB = $storage, rp.label = $label
		`, map[string]any{"id": rp.ID, "cpu": rp.CPUMilli, "mem": rp.MemMiB, "storage": rp.StorageGiB, "label": rp.Label})
		if err != nil {
			return err
		}
	}
	return nil
}
