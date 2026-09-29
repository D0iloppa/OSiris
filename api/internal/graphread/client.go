// Package graphread is osiris-api's ONLY point of contact with Neo4j, and it
// opens read-only sessions exclusively (neo4j.AccessModeRead). This is not a
// violation of D1/G7/A5 ("Neo4j 쓰기는 osiris-core의 graph-write API 만") —
// those decisions govern WRITES. §12.1 of 10-core-architecture.md explicitly
// lists direct Neo4j reads as osiris-api's job for several console routes
// ("GET /orgs/tree ✅ | Neo4j"). Every query here is a fixed, parameterless
// Cypher literal — there is no handler anywhere in this service that accepts
// and runs caller-supplied Cypher, matching osiris-core's own rule for the
// same reason (A5/G7's spirit: no component should have an arbitrary-query
// surface, reader or writer).
package graphread

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

type Client struct {
	driver neo4j.DriverWithContext
}

// NewClient opens the driver and verifies connectivity once before returning.
// Use it only where a caller genuinely cannot proceed without a reachable
// graph; osiris-api's boot path uses NewLazyClient instead — see below.
func NewClient(ctx context.Context, uri, user, password string) (*Client, error) {
	c, err := NewLazyClient(uri, user, password)
	if err != nil {
		return nil, err
	}
	if err := c.driver.VerifyConnectivity(ctx); err != nil {
		_ = c.driver.Close(ctx)
		return nil, fmt.Errorf("verify neo4j connectivity: %w", err)
	}
	return c, nil
}

// NewLazyClient opens the driver WITHOUT verifying connectivity, so a Neo4j
// that is merely slow to start does not permanently decide this process's
// fate.
//
// WHY THIS EXISTS (2026-09-11 실측 — a live incident, not a hypothetical).
// osiris-api boots "degraded" rather than fatal when Neo4j is unreachable
// (see its caller in cmd/osiris-api/main.go, and the comment there on why
// failing the whole process over a read-only dependency would make onboarding
// step0 unusable). The defect was that degraded had no way BACK: the boot
// path retried NewClient ten times, gave up, and left `graph` nil forever.
// pingNeo4j then answered errNotConnected on every /readyz probe for as long
// as the process lived — so a pod that lost a startup race with Neo4j stayed
// NotReady permanently, the Ingress answered 503 to every /_api/* request,
// and the console's AuthGate read that failure as "logged out" and showed the
// login screen. Found after osiris-api had been NotReady for 7 days 15 hours
// (readiness probe failed x74648) while Neo4j itself was perfectly healthy.
// A pod restart was the only recovery, and nothing in the system said so.
//
// The neo4j driver is already lazy and pooled — it dials on first use and
// re-dials on every subsequent one, so a Client built here starts working by
// itself the moment Neo4j comes up, with no reconnect logic of our own.
// Construction only fails on a malformed URI or an unparseable auth config,
// which no amount of waiting would fix.
//
// SAFETY OF ALWAYS-NON-NIL: every consumer that used to check `graph == nil`
// and fail closed reaches an equivalent error path when a query fails against
// a non-nil client instead — orgstate.GraphCredentialDirectory returns an
// error from EVERY failure path and never 0 (its own comment explains why a
// graph outage must not read as "zero credentialed admins"), authn's
// passwordLogin answers the same 503 errStoreDown either way, and
// onboarding.orgSummary degrades to zeros on a query error exactly as it did
// on a nil client. Nothing gets MORE permissive by this change; the only
// difference is that the degraded state can now end.
func NewLazyClient(uri, user, password string) (*Client, error) {
	driver, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(user, password, ""))
	if err != nil {
		return nil, fmt.Errorf("create neo4j driver: %w", err)
	}
	return &Client{driver: driver}, nil
}

func (c *Client) Close(ctx context.Context) error { return c.driver.Close(ctx) }

// Ping is used by /readyz — must stay light. VerifyConnectivity does not
// start a session or take any graph-level lock.
func (c *Client) Ping(ctx context.Context) error { return c.driver.VerifyConnectivity(ctx) }

func (c *Client) session(ctx context.Context) neo4j.SessionWithContext {
	return c.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
}

// OrgSummary is the `org` section of GET /onboarding/status (§1.0.8's JSON
// shape). This build does not implement the assignment engine or the
// conversational (1단계) onboarding pipeline, so in practice these counts
// read 0 until a future worker's org-seed writes land — that is expected and
// correct, not a bug: the graph really is empty until then.
type OrgSummary struct {
	Teams                   int64
	Roles                   int64
	AssignmentPolicyDefined bool
	CapabilityMappingRatio  *float64 // always nil in this build — see doc comment below
}

// capabilityMappingRatio ("required capability -> mapped permission" ratio,
// §12.4 M1's console-facing concept) needs the full grant-catalog bridge
// (20-ontology-schema.md §7.2) walked against every :Role's
// RequiredCapability set — that traversal belongs to the org-seed/assignment
// work this build explicitly excludes. Reporting null (rather than
// inventing a fake 0/1) is the honest value: "not computed by this build",
// not "computed as zero".
func (c *Client) OrgSummary(ctx context.Context) (OrgSummary, error) {
	sess := c.session(ctx)
	defer sess.Close(ctx)

	res, err := sess.Run(ctx, `
		OPTIONAL MATCH (t:Team)
		WITH count(DISTINCT t) AS teams
		OPTIONAL MATCH (r:Role)
		WITH teams, count(DISTINCT r) AS roles
		OPTIONAL MATCH (p:AssignmentPolicy)
		RETURN teams, roles, count(p) > 0 AS policyDefined
	`, nil)
	if err != nil {
		return OrgSummary{}, fmt.Errorf("query org summary: %w", err)
	}
	if !res.Next(ctx) {
		if err := res.Err(); err != nil {
			return OrgSummary{}, fmt.Errorf("read org summary: %w", err)
		}
		return OrgSummary{}, nil
	}
	rec := res.Record()
	teams, _ := rec.Get("teams")
	roles, _ := rec.Get("roles")
	policyDefined, _ := rec.Get("policyDefined")
	return OrgSummary{
		Teams:                   asInt64(teams),
		Roles:                   asInt64(roles),
		AssignmentPolicyDefined: asBool(policyDefined),
	}, nil
}

// CurrentOrgRevision reads §8.5's `currentOrgRevision` — the highest
// :OrgRevision.n. Mirrors osiris-core's own CurrentRevision query
// (core/internal/graphdb/writeops.go) exactly; kept as a separate read here
// rather than proxied through core because osiris-api already owns a
// read-only Neo4j session for the same reason OrgSummary does, and because
// this value gates AUTHENTICATION (60 §19.4's bootstrap-class validity
// predicate) — routing it through another service's HTTP surface would make
// every machine-principal request depend on core being up, when the graph
// itself is the authority.
//
// AN ERROR HERE MEANS UNDETERMINABLE, NEVER ZERO. Callers must fail closed
// (§8.5's standing rule). Note that a graph with no :OrgRevision at all is a
// DIFFERENT and legitimate state (revision 0 has not been seeded yet) and is
// reported as 0 with a nil error — "the org does not exist" is a determinate
// answer, not a failure.
func (c *Client) CurrentOrgRevision(ctx context.Context) (int64, error) {
	sess := c.session(ctx)
	defer sess.Close(ctx)

	res, err := sess.Run(ctx, `MATCH (r:OrgRevision) RETURN max(r.n) AS n`, nil)
	if err != nil {
		return 0, fmt.Errorf("query current org revision: %w", err)
	}
	if !res.Next(ctx) {
		if err := res.Err(); err != nil {
			return 0, fmt.Errorf("read current org revision: %w", err)
		}
		return 0, nil
	}
	n, _ := res.Record().Get("n")
	if n == nil {
		// max() over an empty match returns null — no :OrgRevision exists yet.
		return 0, nil
	}
	return asInt64(n), nil
}

func asInt64(v any) int64 {
	if n, ok := v.(int64); ok {
		return n
	}
	return 0
}

func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// ===========================================================================
// [2026-07-27] The org role model's read surface — 60-identity-authn.md §20,
// 20-ontology-schema.md §7.1.
//
// osiris-api NEVER writes to Neo4j (D1/G7/A5); every org-role MUTATION goes
// through osiris-core's graph-write API (internal/corewrite). What lives here
// is the READ half, and it is all of it: effective(party), the people list the
// console renders, and the org.admin holder set that DG4's API-layer
// precondition is computed from.
//
// WHY effective(party) IS EVALUATED HERE AND NOT ASKED OF osiris-core: the
// same reason CurrentOrgRevision is (see its doc comment). This value gates
// AUTHORIZATION on every request; routing it through another service's HTTP
// surface would make every authenticated request depend on core being up,
// when the graph itself is the authority and osiris-api already holds a
// read-only session to it. §12.1's REST table already assigns direct Neo4j
// reads to osiris-api for exactly this class of query.
// ===========================================================================

// effectivePartyCypher is 20-ontology-schema.md §7.1, for ONE party:
//
//	effective(party) = (∪ grants(base)) ∪ (∪ grants(additive))
//	                   − denies(party) − (∪ denies(profile))
//
// It is a byte-for-byte structural twin of osiris-core's effectiveHoldersCypher
// (core/internal/graphdb/invariants.go) with a single-party MATCH instead of an
// all-people one. The duplication is deliberate and bounded, and 60 §21.4-1
// authorizes exactly this shape: core keeps the INVARIANT (I14 — is anyone
// still an admin), osiris-api keeps the DECISION (who may do what). If the two
// ever disagree the safe direction is the one that happens: core refuses the
// commit.
//
// base_profiles deliberately does NOT filter on IN_TEAM. §7.1 defines it as
// "the :Role of a currently valid :Membership" and never mentions teams, which
// is precisely why IN_ORG memberships flow in with zero formula changes
// (60 §20.2's fourth argument). A person with one job role and one org role
// has two base profiles and gets their union — the same property multi-team
// assignment (allocationPct) already assumed.
const effectivePartyCypher = `
	MATCH (p:Party {id: $partyId})
	WHERE p.closedAtRevision IS NULL
	OPTIONAL MATCH (p)-[:HAS_MEMBERSHIP]->(mb:Membership)-[:AS_ROLE]->(:Role)
	                 -[:HAS_PROFILE {kind: 'base'}]->(:PermissionProfile)-[:GRANTS]->(bg:Permission)
	  WHERE mb.validTo IS NULL
	WITH p, collect(DISTINCT bg.code) AS baseGrants
	OPTIONAL MATCH (p)-[:HAS_MEMBERSHIP]->(md:Membership)-[:AS_ROLE]->(:Role)
	                 -[:HAS_PROFILE {kind: 'base'}]->(:PermissionProfile)-[:DENIES]->(bd:Permission)
	  WHERE md.validTo IS NULL
	WITH p, baseGrants, collect(DISTINCT bd.code) AS baseDenies
	OPTIONAL MATCH (p)-[:HAS_PROFILE {kind: 'additive'}]->(:PermissionProfile)-[:GRANTS]->(ag:Permission)
	WITH p, baseGrants, baseDenies, collect(DISTINCT ag.code) AS addGrants
	OPTIONAL MATCH (p)-[:HAS_PROFILE {kind: 'additive'}]->(:PermissionProfile)-[:DENIES]->(ad:Permission)
	WITH p, baseGrants, baseDenies, addGrants, collect(DISTINCT ad.code) AS addDenies
	OPTIONAL MATCH (p)-[:DENIES]->(pd:Permission)
	WITH baseGrants, baseDenies, addGrants, addDenies, collect(DISTINCT pd.code) AS partyDenies
	WITH baseGrants + addGrants AS granted, baseDenies + addDenies + partyDenies AS denied
	RETURN [c IN granted WHERE NOT c IN denied] AS effective
`

// EffectivePermissions returns effective(party) for one :Party id.
//
// A party that does not exist, or that is closed, yields an EMPTY set with a
// nil error — that is a determinate answer ("this person has no permissions"),
// not a failure, and it is the correct one for a deactivated account. An error
// means UNDETERMINABLE and every caller must fail closed on it.
func (c *Client) EffectivePermissions(ctx context.Context, partyID string) ([]string, error) {
	sess := c.session(ctx)
	defer sess.Close(ctx)

	res, err := sess.Run(ctx, effectivePartyCypher, map[string]any{"partyId": partyID})
	if err != nil {
		return nil, fmt.Errorf("query effective permissions: %w", err)
	}
	if !res.Next(ctx) {
		if err := res.Err(); err != nil {
			return nil, fmt.Errorf("read effective permissions: %w", err)
		}
		return []string{}, nil
	}
	v, _ := res.Record().Get("effective")
	return asStringSlice(v), nil
}

// OrgAdminHolders returns the ids of every ACTIVE :Person effectively holding
// code — the graph side of DG4's API-layer precondition and of the H10
// predicate's second conjunct.
//
// Callers pair it with the credential store: the graph says who is an admin,
// AUTHN_CRED says who can log in, and neither store knows about the other
// (H1's one-way reference). That intersection is computed in
// internal/orgstate, where both halves are already interfaces.
func (c *Client) EffectiveHolders(ctx context.Context, code string) ([]string, error) {
	sess := c.session(ctx)
	defer sess.Close(ctx)

	res, err := sess.Run(ctx, `
		MATCH (p:Person)
		WHERE p.closedAtRevision IS NULL
		OPTIONAL MATCH (p)-[:HAS_MEMBERSHIP]->(mb:Membership)-[:AS_ROLE]->(:Role)
		                 -[:HAS_PROFILE {kind: 'base'}]->(:PermissionProfile)-[:GRANTS]->(bg:Permission)
		  WHERE mb.validTo IS NULL
		WITH p, collect(DISTINCT bg.code) AS baseGrants
		OPTIONAL MATCH (p)-[:HAS_MEMBERSHIP]->(md:Membership)-[:AS_ROLE]->(:Role)
		                 -[:HAS_PROFILE {kind: 'base'}]->(:PermissionProfile)-[:DENIES]->(bd:Permission)
		  WHERE md.validTo IS NULL
		WITH p, baseGrants, collect(DISTINCT bd.code) AS baseDenies
		OPTIONAL MATCH (p)-[:HAS_PROFILE {kind: 'additive'}]->(:PermissionProfile)-[:GRANTS]->(ag:Permission)
		WITH p, baseGrants, baseDenies, collect(DISTINCT ag.code) AS addGrants
		OPTIONAL MATCH (p)-[:HAS_PROFILE {kind: 'additive'}]->(:PermissionProfile)-[:DENIES]->(ad:Permission)
		WITH p, baseGrants, baseDenies, addGrants, collect(DISTINCT ad.code) AS addDenies
		OPTIONAL MATCH (p)-[:DENIES]->(pd:Permission)
		WITH p, baseGrants, baseDenies, addGrants, addDenies, collect(DISTINCT pd.code) AS partyDenies
		WITH p, baseGrants + addGrants AS granted, baseDenies + addDenies + partyDenies AS denied
		WITH p, [x IN granted WHERE NOT x IN denied] AS effective
		WITH p, effective WHERE $code IN effective
		RETURN p.id AS id
	`, map[string]any{"code": code})
	if err != nil {
		return nil, fmt.Errorf("query permission holders: %w", err)
	}
	var out []string
	for res.Next(ctx) {
		if v, ok := res.Record().Get("id"); ok {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	if err := res.Err(); err != nil {
		return nil, fmt.Errorf("read permission holders: %w", err)
	}
	return out, nil
}

// Person is the console-facing projection of a :Person plus its org role.
// It carries NO credential state — that comes from AUTHN_CRED and is joined by
// the handler, because the graph does not know that store exists (H1).
type Person struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Email     string   `json:"email"`
	Active    bool     `json:"active"`
	OrgRoleID string   `json:"orgRoleId,omitempty"`
	OrgRole   string   `json:"orgRole,omitempty"`
	OrgMember string   `json:"orgMembershipId,omitempty"`
	Teams     []string `json:"teams"`
	JobRoles  []string `json:"jobRoles"`
}

const personProjection = `
	OPTIONAL MATCH (p)-[:HAS_MEMBERSHIP]->(om:Membership)-[:IN_ORG]->(:Organization)
	  WHERE om.validTo IS NULL
	OPTIONAL MATCH (om)-[:AS_ROLE]->(orole:Role)
	WITH p, om, orole
	OPTIONAL MATCH (p)-[:HAS_MEMBERSHIP]->(tm:Membership)-[:IN_TEAM]->(t:Team)
	  WHERE tm.validTo IS NULL
	WITH p, om, orole, collect(DISTINCT t.name) AS teams, collect(DISTINCT tm) AS tms
	OPTIONAL MATCH (p)-[:HAS_MEMBERSHIP]->(tm2:Membership)-[:IN_TEAM]->(:Team)
	OPTIONAL MATCH (tm2)-[:AS_ROLE]->(jr:Role)
	  WHERE tm2.validTo IS NULL
	RETURN p.id AS id, p.name AS name, p.email AS email,
	       p.closedAtRevision IS NULL AS active,
	       orole.id AS orgRoleId, orole.title AS orgRole, om.id AS orgMembershipId,
	       teams AS teams, collect(DISTINCT jr.title) AS jobRoles
`

// ListPeople returns every :Person, active or not (the console's People &
// Roles screen shows deactivated accounts too — an audit surface that hiding
// them would remove).
func (c *Client) ListPeople(ctx context.Context) ([]Person, error) {
	sess := c.session(ctx)
	defer sess.Close(ctx)
	res, err := sess.Run(ctx, `MATCH (p:Person) WITH p ORDER BY p.id `+personProjection, nil)
	if err != nil {
		return nil, fmt.Errorf("query people: %w", err)
	}
	var out []Person
	for res.Next(ctx) {
		out = append(out, personFromRecord(res.Record()))
	}
	if err := res.Err(); err != nil {
		return nil, fmt.Errorf("read people: %w", err)
	}
	return out, nil
}

// GetPerson returns one person, or ok == false when no such :Person exists.
func (c *Client) GetPerson(ctx context.Context, id string) (Person, bool, error) {
	sess := c.session(ctx)
	defer sess.Close(ctx)
	res, err := sess.Run(ctx, `MATCH (p:Person {id: $id}) WITH p `+personProjection,
		map[string]any{"id": id})
	if err != nil {
		return Person{}, false, fmt.Errorf("query person: %w", err)
	}
	if !res.Next(ctx) {
		if err := res.Err(); err != nil {
			return Person{}, false, fmt.Errorf("read person: %w", err)
		}
		return Person{}, false, nil
	}
	return personFromRecord(res.Record()), true, nil
}

// PersonByEmail resolves the console login identifier (20 §3.1) to a person.
// Uniqueness is enforced by the person_email constraint (OD1), so at most one
// row can come back.
func (c *Client) PersonByEmail(ctx context.Context, email string) (Person, bool, error) {
	sess := c.session(ctx)
	defer sess.Close(ctx)
	res, err := sess.Run(ctx, `MATCH (p:Person) WHERE p.email = $email WITH p `+personProjection,
		map[string]any{"email": email})
	if err != nil {
		return Person{}, false, fmt.Errorf("query person by email: %w", err)
	}
	if !res.Next(ctx) {
		if err := res.Err(); err != nil {
			return Person{}, false, fmt.Errorf("read person by email: %w", err)
		}
		return Person{}, false, nil
	}
	return personFromRecord(res.Record()), true, nil
}

// OrgRole is a :Role{scope:'org'} as the console offers it for assignment,
// with the permission codes its base profile grants.
//
// grants IS THE FIELD THE DELEGATION RULES READ. DG1 compares grants(R) with
// eff(A); it never looks at id or title (H30/HI17), which is why a role rename
// cannot change an authorization outcome.
type OrgRole struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Level  int64    `json:"level"`
	Grants []string `json:"grants"`
}

// ListOrgRoles returns every :Role{scope:'org'} with its base profile's grants.
func (c *Client) ListOrgRoles(ctx context.Context) ([]OrgRole, error) {
	sess := c.session(ctx)
	defer sess.Close(ctx)
	res, err := sess.Run(ctx, `
		MATCH (r:Role) WHERE r.scope = 'org'
		OPTIONAL MATCH (r)-[:HAS_PROFILE {kind: 'base'}]->(:PermissionProfile)-[:GRANTS]->(perm:Permission)
		WITH r, collect(DISTINCT perm.code) AS grants
		RETURN r.id AS id, r.title AS title, coalesce(r.level, 0) AS level, grants AS grants
		ORDER BY level DESC, id
	`, nil)
	if err != nil {
		return nil, fmt.Errorf("query org roles: %w", err)
	}
	var out []OrgRole
	for res.Next(ctx) {
		rec := res.Record()
		id, _ := rec.Get("id")
		title, _ := rec.Get("title")
		level, _ := rec.Get("level")
		grants, _ := rec.Get("grants")
		out = append(out, OrgRole{
			ID: asString(id), Title: asString(title), Level: asInt64(level),
			Grants: asStringSlice(grants),
		})
	}
	if err := res.Err(); err != nil {
		return nil, fmt.Errorf("read org roles: %w", err)
	}
	return out, nil
}

// OrgRoleGrants returns the permission codes a single org role's base profile
// grants — DG1's grants(R).
//
// ok == false means the role does not exist or is not org-scoped. Callers must
// treat that as a refusal, never as "grants nothing" — an empty grant set
// trivially satisfies DG1, so conflating the two would let a typo'd role id
// pass the very check that is supposed to stop it.
func (c *Client) OrgRoleGrants(ctx context.Context, roleID string) ([]string, bool, error) {
	sess := c.session(ctx)
	defer sess.Close(ctx)
	res, err := sess.Run(ctx, `
		MATCH (r:Role {id: $id}) WHERE r.scope = 'org'
		OPTIONAL MATCH (r)-[:HAS_PROFILE {kind: 'base'}]->(:PermissionProfile)-[:GRANTS]->(perm:Permission)
		RETURN collect(DISTINCT perm.code) AS grants
	`, map[string]any{"id": roleID})
	if err != nil {
		return nil, false, fmt.Errorf("query org role grants: %w", err)
	}
	if !res.Next(ctx) {
		if err := res.Err(); err != nil {
			return nil, false, fmt.Errorf("read org role grants: %w", err)
		}
		return nil, false, nil
	}
	v, _ := res.Record().Get("grants")
	return asStringSlice(v), true, nil
}

// ProfileGrants returns the permission codes a :PermissionProfile grants —
// DG1's grants(R) when R is an additive profile rather than a role.
func (c *Client) ProfileGrants(ctx context.Context, profileID string) ([]string, bool, error) {
	sess := c.session(ctx)
	defer sess.Close(ctx)
	res, err := sess.Run(ctx, `
		MATCH (pp:PermissionProfile {id: $id})
		OPTIONAL MATCH (pp)-[:GRANTS]->(perm:Permission)
		RETURN collect(DISTINCT perm.code) AS grants
	`, map[string]any{"id": profileID})
	if err != nil {
		return nil, false, fmt.Errorf("query profile grants: %w", err)
	}
	if !res.Next(ctx) {
		if err := res.Err(); err != nil {
			return nil, false, fmt.Errorf("read profile grants: %w", err)
		}
		return nil, false, nil
	}
	v, _ := res.Record().Get("grants")
	return asStringSlice(v), true, nil
}

func personFromRecord(rec *neo4j.Record) Person {
	id, _ := rec.Get("id")
	name, _ := rec.Get("name")
	email, _ := rec.Get("email")
	active, _ := rec.Get("active")
	orgRoleID, _ := rec.Get("orgRoleId")
	orgRole, _ := rec.Get("orgRole")
	orgMembership, _ := rec.Get("orgMembershipId")
	teams, _ := rec.Get("teams")
	jobRoles, _ := rec.Get("jobRoles")
	return Person{
		ID: asString(id), Name: asString(name), Email: asString(email),
		Active:    asBool(active),
		OrgRoleID: asString(orgRoleID), OrgRole: asString(orgRole),
		OrgMember: asString(orgMembership),
		Teams:     asStringSlice(teams), JobRoles: asStringSlice(jobRoles),
	}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asStringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// PersonIsActive reports whether id names a :Person with closedAtRevision IS
// NULL.
//
// This is the org-class machine principal's validity predicate (60 §19.4:
// "발급자 :Person이 활성"). The previous build could not evaluate it — there
// were no people and no person sessions — and refused org-class issuance with
// 501 rather than assuming the answer, which was the right call. It is
// evaluable now.
//
// An error means UNDETERMINABLE and the caller denies (machineauth's
// classValid turns it into ErrClassInvalid → 401). "Cannot check the issuer"
// must never read as "the issuer is fine".
func (c *Client) PersonIsActive(ctx context.Context, id string) (bool, error) {
	sess := c.session(ctx)
	defer sess.Close(ctx)
	res, err := sess.Run(ctx, `MATCH (p:Person {id: $id}) WHERE p.closedAtRevision IS NULL RETURN count(p) AS n`,
		map[string]any{"id": id})
	if err != nil {
		return false, fmt.Errorf("query person liveness: %w", err)
	}
	if !res.Next(ctx) {
		if err := res.Err(); err != nil {
			return false, fmt.Errorf("read person liveness: %w", err)
		}
		return false, nil
	}
	n, _ := res.Record().Get("n")
	return asInt64(n) > 0, nil
}
