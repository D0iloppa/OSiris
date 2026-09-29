package authz

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ===========================================================================
// GraphPermissionSource — the seam §103 left open, now filled.
//
// 60-identity-authn.md §19.6's table says where each principal kind's codes
// come from, and the person row is the one that was empty until now:
//
//	kind:"person"     → effective(party)              (20 §7.1)
//	kind:"master-key" → osiris-api 고정 스코프
//	kind:"machine"    → AUTHN_MACHINE 레코드의 고정 배열
//
// This type is the person row. It resolves codes from the LIVE GRAPH through
// internal/graphread — osiris-api's single point of contact with Neo4j (G7) —
// and nothing here holds a driver, a session or a line of Cypher: it takes an
// interface, which is what keeps this package's "authorization vocabulary is
// pure" property (see the package doc) true after the graph arrived.
// ===========================================================================

// EffectiveReader is the one graph fact this package needs. Satisfied by
// *graphread.Client.
type EffectiveReader interface {
	EffectivePermissions(ctx context.Context, partyID string) ([]string, error)
}

// PermissionCacheTTL is how long a resolved code set is reused.
//
// ── THE CACHE DECISION, AND THE COST IT ACCEPTS ───────────────────────────
//
// Not caching at all means one Neo4j round trip per authenticated request,
// including every poll of an Ops screen. Caching means a REVOKED PERMISSION
// KEEPS WORKING for the length of the TTL — there is no third option, and
// pretending otherwise by "invalidating on change" would only move the
// staleness into a cache-invalidation bug.
//
// The design already priced this. §3.2: "effectivePermissions를 세션에
// 캐시하지 않는다. 권한은 그래프가 진실이고 조직 변경 즉시 반영되어야 한다...
// 비용은 osiris-api의 짧은 TTL 캐시(≤10초)로 흡수한다." §7.4 sets the OUTER
// envelope for revocation lag across this whole service at "최대 60초" and
// states it as an explicit cost rather than hiding it.
//
// So: 10 seconds — the number §3.2 names for THIS value specifically, which is
// six times tighter than the 60s envelope. Deliberately tighter than the
// machine principal's 60s positive cache (machineauth.PositiveCacheTTL),
// because a machine principal's codes are FROZEN AT ISSUE TIME and cannot
// change while the record lives, whereas a person's change the moment an
// administrator demotes them. Caching a value that cannot change costs
// nothing; caching one that can costs exactly this TTL.
//
// It also sits inside the SSE watchdog's 30s revalidation interval
// (authn.RevalidateInterval), which matters: the watchdog calls Resolve, so a
// TTL longer than 30s would let a demoted person's open stream survive a tick
// on cached data. 10 < 30 < 60 — one coherent budget, three consistent numbers.
//
// POSITIVE ONLY. A failed resolution is never cached, for §7.4's stated
// reason: caching failure delays recovery, and this path fails closed, so a
// cached failure would extend an outage instead of a privilege.
const PermissionCacheTTL = 10 * time.Second

type cachedPerms struct {
	codes []Code
	at    time.Time
}

// GraphPermissionSource implements PermissionSource against the org graph.
type GraphPermissionSource struct {
	reader EffectiveReader
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]cachedPerms
}

func NewGraphPermissionSource(reader EffectiveReader) *GraphPermissionSource {
	return &GraphPermissionSource{
		reader: reader,
		now:    time.Now,
		cache:  make(map[string]cachedPerms),
	}
}

// Resolve returns effective(party) for partyID.
//
// An error means UNDETERMINABLE and every caller denies (the guard turns it
// into a 401/503, never into an empty permission set). Conflating "the graph
// is unreachable" with "this person has no permissions" would be the safe
// direction for reads and the wrong direction for anything that later asks
// "is this actor an admin" — so the distinction is preserved at the type
// level: ([]Code, error), never a bare slice.
func (s *GraphPermissionSource) Resolve(partyID string) ([]Code, error) {
	return s.ResolveCtx(context.Background(), partyID)
}

// ResolveCtx is Resolve with the request's context, so a graph read cannot
// outlive the request that needed it.
func (s *GraphPermissionSource) ResolveCtx(ctx context.Context, partyID string) ([]Code, error) {
	if partyID == "" {
		return nil, fmt.Errorf("cannot resolve permissions for an empty party id")
	}
	if s == nil || s.reader == nil {
		return nil, fmt.Errorf("permission source unavailable: no graph connection")
	}

	now := s.now()
	s.mu.Lock()
	if e, ok := s.cache[partyID]; ok && now.Sub(e.at) < PermissionCacheTTL {
		codes := append([]Code(nil), e.codes...)
		s.mu.Unlock()
		return codes, nil
	}
	s.mu.Unlock()

	raw, err := s.reader.EffectivePermissions(ctx, partyID)
	if err != nil {
		return nil, fmt.Errorf("effective permissions undeterminable: %w", err)
	}

	// FromStrings drops codes this build does not know. A :Permission the
	// graph carries but authz.Catalog does not is version drift, and it must
	// authorize nothing here while I9 reports it where it belongs.
	codes := FromStrings(raw)

	s.mu.Lock()
	s.cache[partyID] = cachedPerms{codes: codes, at: now}
	s.mu.Unlock()

	return append([]Code(nil), codes...), nil
}

// ResolveFresh bypasses the cache. Used where the answer must be
// AUTHORITATIVE rather than recent: the delegation check (a stale actor set
// could authorize a grant the actor lost seconds ago) and the long-lived
// stream watchdog (re-reading the cache would answer the question we already
// know the answer to — the same argument machineauth.Revalidate makes).
func (s *GraphPermissionSource) ResolveFresh(ctx context.Context, partyID string) ([]Code, error) {
	if partyID == "" {
		return nil, fmt.Errorf("cannot resolve permissions for an empty party id")
	}
	if s == nil || s.reader == nil {
		return nil, fmt.Errorf("permission source unavailable: no graph connection")
	}
	raw, err := s.reader.EffectivePermissions(ctx, partyID)
	if err != nil {
		return nil, fmt.Errorf("effective permissions undeterminable: %w", err)
	}
	codes := FromStrings(raw)
	s.mu.Lock()
	s.cache[partyID] = cachedPerms{codes: codes, at: s.now()}
	s.mu.Unlock()
	return append([]Code(nil), codes...), nil
}

// Invalidate drops one party's cached set.
//
// This is an OPTIMIZATION, not a correctness mechanism, and the distinction is
// worth stating because it is the usual place this kind of code goes wrong: it
// is called after a successful role change so the operator sees the effect
// immediately instead of within the TTL, and the system is correct WITHOUT it
// (the TTL expires). Nothing anywhere may depend on Invalidate having been
// called — the measured revocation lag is the TTL, not zero.
func (s *GraphPermissionSource) Invalidate(partyID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.cache, partyID)
	s.mu.Unlock()
}

// Compile-time proof that the seam declared in authz.go is actually filled.
var _ PermissionSource = (*GraphPermissionSource)(nil)
