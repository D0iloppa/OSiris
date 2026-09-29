// Package machineauth implements 60-identity-authn.md §19's verification-only
// machine principal — design branch (c) of IMPLEMENTATION-NOTES.md §102.2.
//
// WHAT THIS IS FOR. When H10 retires the master key, non-interactive callers
// (the §81 completion-condition curls, CI) lose every way to authenticate:
// they have no cookie jar and no browser. §19.2 rejected three alternatives —
// keeping the master key alive for CI (§4.6), logging CI in as a person
// (a human's credential in a CI secret store, with full privileges), and
// minting a ghost :Person (organizational pollution, D1) — and adopted a
// credential that is deliberately NOT a :Party at all.
//
// WHAT MAKES IT NOT A BACKDOOR (§19.8's MP1~MP6, in the order the code
// enforces them):
//
//	MP1  Validity is a predicate over the WORLD, re-evaluated per request, not
//	     a property of the token. A bootstrap-class token dies the instant an
//	     org exists and an admin can prove who they are — see resolveClassValid
//	     and internal/orgstate.
//	MP2  Hard TTL, enforced twice: the record's expiresAt (policy) and the
//	     AUTHN_MACHINE bucket's own 90-day max age (backstop). A bug that skips
//	     the first is still caught by the second.
//	MP3  Capability ceiling: the scope allowlist is authz.MachineGrantableCodes
//	     (org.read.* only) and it is applied AT ISSUE TIME, so a record that
//	     violates it never reaches storage.
//	MP4  Never a :Party: this package has no partyId field, produces
//	     authz.Principal{Kind: KindMachine, PartyID: ""}, and touches no graph
//	     write path.
//	MP5  Revocation deletes the KV key (immediate, modulo the ≤60s positive
//	     cache §7.4 already accepts for sessions).
//	MP6  Issue/first-use/revoke/expire publish security.machine.* and issuance
//	     is FAIL-CLOSED on that publish.
//
// AND WHAT IT IS NOT (H27, §19.6's most important line): a machine principal
// cannot issue, rotate, or revoke a machine principal — including itself.
// 31-onboarding-boundary.md §2.2 recorded what happens when a credential CAN
// re-issue itself ("한 번 유출되면 회수 자체가 경쟁"). That property is
// refused here structurally: every /api/machine-principals route is registered
// under the router's PersonOnly middleware (internal/api/server.go), which
// rejects Kind == KindMachine before the handler runs.
//
// SECRET HYGIENE (this repository has had two log leaks and one plaintext
// credential file leak): the plaintext token exists in exactly two places for
// exactly two instants — the CSPRNG buffer in Issue, and the single issue
// response body. It is never logged, never returned twice, never written to a
// file, and never stored: AUTHN_MACHINE holds sha256(token) only.
package machineauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"osiris-api/internal/authz"
	"osiris-api/internal/orgstate"
	"osiris-api/internal/secevents"
)

// TokenPrefix is §19.3's `osm_` (machine). It is not decoration: both the
// setup-token path and this one arrive as `Authorization: Bearer`, so the
// prefix is what lets the guard pick a branch without trial-and-error
// comparison against every credential it knows. It also gives secret scanners
// a pattern and makes an accidentally-leaked value self-identifying.
//
// The corresponding master-key rename to `osk_` is H10's, i.e. the NEXT task —
// this build must keep the existing unprefixed setup token working verbatim.
const TokenPrefix = "osm_"

// BucketName is the AUTHN_MACHINE bucket of §3.2. §14.1: "AUTHN_* (JetStream
// KV) — osiris-api 단독 소유". HI9 checks that no other NATS user is granted
// $KV.AUTHN_* rights.
const BucketName = "AUTHN_MACHINE"

// BucketMaxAge is MP2's second enforcement layer. Deliberately equal to the
// org class's TTL CEILING, not to its default: the bucket is a backstop
// against a code bug that forgets expiresAt, so it must not expire anything a
// correct implementation would still consider valid.
const BucketMaxAge = 90 * 24 * time.Hour

// Class TTLs, §19.4's table.
const (
	// BootstrapTTL is 24h — "W2와 같은 값·같은 이유". §19.9 explains the
	// deliberate alignment with the genesis token's hard TTL: both bound the
	// same bootstrap window and different ceilings would just be confusing.
	BootstrapTTL = 24 * time.Hour

	// OrgDefaultTTL / OrgMaxTTL are §19.4's "기본 30일, 최대 90일".
	OrgDefaultTTL = 30 * 24 * time.Hour
	OrgMaxTTL     = 90 * 24 * time.Hour
)

// PositiveCacheTTL is §7.4's ≤60s positive cache, applied to machine
// principals per §19.3's cache row — INCLUDING the same honestly-stated cost:
// "리보크가 최대 60초 지연된다". No negative cache, for §7.4's reason (caching
// failures delays recovery).
const PositiveCacheTTL = 60 * time.Second

// RateLimitPerMinute is §19.8's per-principal ceiling. Purpose, in the design's
// own order of likelihood: (ii) stop a badly written CI loop from hammering
// the stack, and (i) slow bulk collection with a stolen read token.
const RateLimitPerMinute = 60

// lastUsedWriteInterval throttles the lastUsedAt write-back.
//
// §19.7 requires that reads NOT emit an event each ("SSE 구독 하나가 이벤트
// 폭풍을 만들고, TASKS 90일 보존을 잡음으로 채운다") and that lastUsedAt be
// updated instead. Taken literally, a KV write per request would reintroduce
// the same storm one layer down — 60 writes/minute/principal into a
// replicated stream, to move a timestamp by one second. Coalescing to one
// write per minute preserves what the field is FOR ("사용 여부는 항상 알 수
// 있다", plus the Ops screen's "마지막 사용 시각") at 1/60th the cost. The
// staleness is bounded by this constant and is stated in the API response.
const lastUsedWriteInterval = time.Minute

// principalIDPattern constrains caller-supplied ids. Machine principal ids
// appear in event payloads and in KV-adjacent identifiers, and §19.7 shows
// them rendered into an actorRef string ("machine:mp-ci-verify"); an id
// containing a colon, a wildcard or whitespace would make that namespace
// ambiguous. Note this is an id, not a secret — it is meant to be readable in
// the Ops list.
var principalIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)

// Sentinel errors. The guard maps every one of them except ErrUnavailable to
// the SAME generic 401 — §8.2's "응답은 성공/실패 모두 동일한 일반 메시지".
// ErrUnavailable is distinguished (503) because "the credential store is down"
// is an operational fact the caller must be able to tell apart from "your
// credential is bad", and hiding it teaches operators to retry forever.
var (
	ErrUnavailable  = errors.New("machine principal store unavailable")
	ErrNotFound     = errors.New("machine principal not found")
	ErrExpired      = errors.New("machine principal expired")
	ErrClassInvalid = errors.New("machine principal class predicate no longer holds")
	ErrRateLimited  = errors.New("machine principal rate limit exceeded")
)

// Class is §19.4's two classes. The design is emphatic that this is a VALUE
// selecting a validity predicate and a TTL ceiling, not a second code path:
// "클래스는 유효 술어와 TTL 상한을 고르는 값일 뿐 별도 구현 경로가 아니다."
type Class string

const (
	ClassBootstrap Class = "bootstrap"
	ClassOrg       Class = "org"
)

// Record is §3.2's AUTHN_MACHINE value, verbatim plus one field.
//
// The one addition is TokenHash. It is redundant with the KV key (which IS the
// hash) and exists solely so the lookup can finish with a constant-time
// comparison — see Resolve. Storing it costs 64 bytes and buys the property
// the task brief asked for explicitly.
type Record struct {
	PrincipalID string    `json:"principalId"`
	Class       Class     `json:"class"`
	Permissions []string  `json:"permissions"`
	IssuedAt    time.Time `json:"issuedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`

	// IssuedBy is the :Person.id for org class, or the literal "master-key"
	// for bootstrap class (§3.2/§19.7). It is NOT an actorPartyId and must
	// never be written into a graph attribution field.
	IssuedBy string `json:"issuedBy"`

	FirstUsedAt *time.Time `json:"firstUsedAt,omitempty"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty"`

	// TokenHash is hex(sha256(token)) — the same value as the KV key.
	// NEVER the token.
	TokenHash string `json:"tokenHash"`
}

// Active reports whether the record has not yet hit its policy expiry.
func (r Record) Active(now time.Time) bool { return now.Before(r.ExpiresAt) }

// hashToken is §19.3's `sha256(token)`.
//
// WHY SHA-256 AND NOT ARGON2ID — the design specifies sha256 here (§19.3,
// §3.2) while specifying Argon2id for passwords (§5.4), and the asymmetry is
// correct rather than an oversight:
//
//  1. A password is low-entropy and human-chosen, so an attacker who steals
//     the store can enumerate the plausible space. Argon2id exists to make
//     each of those guesses expensive.
//  2. This token is 32 bytes of crypto/rand — 256 bits, uniform. There is no
//     plausible space to enumerate; the stolen-store attack Argon2id defends
//     against does not exist for it. A slow hash would buy exactly zero
//     additional bits.
//  3. It would cost something real, though. Unlike a password hash, which is
//     computed once per LOGIN, this hash is computed once per REQUEST on the
//     hot path of a CI loop rated at 60 req/min/principal. §8.2 L3 already
//     names Argon2id's CPU cost as a self-inflicted DoS vector and puts a
//     concurrency ceiling in front of it for that reason; putting it on every
//     authenticated read would move that vector from the login endpoint to
//     the whole API surface.
//
// Constant-time comparison is still required and still performed (Resolve) —
// that is a different property from hash hardness and it does not follow from
// the entropy argument.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// mintToken produces `osm_` + base64url(32 CSPRNG bytes) (§19.3).
func mintToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// The error text must not, and does not, contain the buffer.
		return "", fmt.Errorf("generate machine principal token: %w", err)
	}
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// HasTokenPrefix is the guard's branch selector (§19.3's verification order
// step 3).
func HasTokenPrefix(bearer string) bool { return strings.HasPrefix(bearer, TokenPrefix) }

// positiveCache is §7.4's ≤60s positive cache. Named exactly this because
// HI5's rule for the session store carves out "≤60초 양성 캐시(§7.4)는 별도
// 타입명(positiveCache)으로만 허용" — following the same naming here keeps a
// future reader (and a future lint) from mistaking it for a credential store.
type positiveCache struct {
	entries map[string]cacheEntry // key: hex(sha256(token))
}

type cacheEntry struct {
	rec       Record
	validated time.Time
}

// rateLimiter is the per-principal 60/min ceiling. A sliding window (timestamp
// ring) rather than a fixed window, because a fixed window lets a caller burst
// 120 requests across a boundary and, more practically, makes the limit's
// observed behavior depend on wall-clock phase — which is exactly the kind of
// thing that makes a verification run flaky.
//
// In-memory, single process. Same documented constraint as internal/session's
// store: osiris-api runs as one replica in this build. §8.2's AUTHN_THROTTLE
// bucket is about the LOGIN throttle layers L1~L3, which are §8's scope, not
// this task's; when a second replica or those layers arrive, this moves.
type rateLimiter struct {
	hits map[string][]time.Time
}

func (l *rateLimiter) allow(id string, now time.Time) bool {
	cutoff := now.Add(-time.Minute)
	kept := l.hits[id][:0]
	for _, t := range l.hits[id] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= RateLimitPerMinute {
		l.hits[id] = kept
		return false
	}
	l.hits[id] = append(kept, now)
	return true
}

// IssuerLiveness is the org-class validity predicate's graph half — 60 §19.4's
// "발급자 :Person이 활성(closedAtRevision IS NULL)". Satisfied by
// *graphread.Client.
//
// It is an interface rather than a concrete dependency for the reason HI2
// states for the authentication packages: this package must not acquire a
// Neo4j driver or a line of Cypher. The graph read lives in
// internal/graphread, osiris-api's single point of contact with Neo4j (G7).
type IssuerLiveness interface {
	PersonIsActive(ctx context.Context, id string) (bool, error)
}

// Store is the AUTHN_MACHINE accessor.
type Store struct {
	js     nats.JetStreamContext
	events *secevents.Publisher
	org    *orgstate.Evaluator
	people IssuerLiveness
	now    func() time.Time

	mu            sync.Mutex
	kv            nats.KeyValue
	cache         positiveCache
	limiter       rateLimiter
	lastUsedWrite map[string]time.Time
}

func New(js nats.JetStreamContext, events *secevents.Publisher, org *orgstate.Evaluator, people IssuerLiveness) *Store {
	return &Store{
		js:            js,
		events:        events,
		org:           org,
		people:        people,
		now:           func() time.Time { return time.Now().UTC() },
		cache:         positiveCache{entries: make(map[string]cacheEntry)},
		limiter:       rateLimiter{hits: make(map[string][]time.Time)},
		lastUsedWrite: make(map[string]time.Time),
	}
}

// bucket binds (or creates) AUTHN_MACHINE lazily.
//
// LAZILY, AND RETRYING, BECAUSE OF A REAL BOOT ORDERING FACT: the NATS
// permissions that let osiris-api touch $KV.AUTHN_MACHINE live in
// nats/generated/authorization.conf, which osiris-orchestrator rewrites and
// SIGHUPs on its reconcile tick. On a cold `docker compose up`, osiris-api can
// win the race and find itself unauthorized for a few seconds. Binding at
// construction time and failing the process over that would make osiris-api's
// startup depend on another service's tick — so instead every call retries,
// and until it succeeds every machine-principal request fails CLOSED
// (ErrUnavailable → 503). The two pre-existing auth paths are unaffected
// because they never reach this code.
func (s *Store) bucket() (nats.KeyValue, error) {
	s.mu.Lock()
	kv := s.kv
	s.mu.Unlock()
	if kv != nil {
		return kv, nil
	}
	if s.js == nil {
		return nil, ErrUnavailable
	}

	kv, err := s.js.KeyValue(BucketName)
	if errors.Is(err, nats.ErrBucketNotFound) {
		kv, err = s.js.CreateKeyValue(&nats.KeyValueConfig{
			Bucket:      BucketName,
			Description: "osiris-api machine principals (60-identity-authn.md §19). Values are sha256-keyed records; no plaintext token is ever stored.",
			// History 1: a credential store has no use for prior versions, and
			// keeping them would keep revoked records readable in the stream.
			History: 1,
			// MP2's second layer.
			TTL:     BucketMaxAge,
			Storage: nats.FileStorage,
		})
	}
	if err != nil {
		// Deliberately not logging err at call frequency — this can be hit on
		// every request while NATS is down. The caller logs once per state
		// change if it wants to.
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	s.mu.Lock()
	s.kv = kv
	s.mu.Unlock()
	return kv, nil
}

// IssueRequest is what a PersonOnly caller submits.
type IssueRequest struct {
	PrincipalID string
	Permissions []authz.Code
	TTL         time.Duration // zero = class default
	Class       Class
	IssuedBy    string
	ActorRef    string
}

// Issue mints a machine principal and returns the plaintext token EXACTLY
// ONCE. The returned string is the only copy that will ever exist outside the
// caller's memory; the store keeps sha256 of it and nothing else.
//
// Order of operations is deliberate and is MP6's fail-closed rule:
// validate → mint → publish security.machine.issued → THEN persist. If the
// audit publish fails, nothing has been written and no credential exists.
// (Persisting first and publishing second would leave a live credential with
// no audit record if the publish failed — precisely the "조용히 존재하는
// 자격증명" MP6 forbids.)
func (s *Store) Issue(ctx context.Context, req IssueRequest) (string, Record, error) {
	var zero Record

	if !principalIDPattern.MatchString(req.PrincipalID) {
		return "", zero, fmt.Errorf("principalId must match %s", principalIDPattern.String())
	}
	if len(req.Permissions) == 0 {
		return "", zero, fmt.Errorf("at least one permission code is required")
	}
	// MP3, enforced AT ISSUE TIME. Enforcing only at decision time would let a
	// malformed record reach storage and then depend forever on every future
	// reader remembering to re-check it.
	for _, c := range req.Permissions {
		if !authz.MachineGrantable(c) {
			return "", zero, fmt.Errorf("permission %q may not be granted to a machine principal (allowed: %v)",
				c, authz.Strings(authz.MachineGrantableCodes()))
		}
	}

	ttl, err := clampTTL(req.Class, req.TTL)
	if err != nil {
		return "", zero, err
	}

	kv, err := s.bucket()
	if err != nil {
		return "", zero, err
	}

	// Reject a duplicate id up front so the Ops list stays unambiguous. This
	// is a usability check, not a security one — the credential is the token,
	// not the id.
	if existing, lerr := s.List(ctx); lerr == nil {
		for _, r := range existing {
			if r.PrincipalID == req.PrincipalID {
				return "", zero, fmt.Errorf("a machine principal with id %q already exists", req.PrincipalID)
			}
		}
	}

	token, err := mintToken()
	if err != nil {
		return "", zero, err
	}
	now := s.now()
	rec := Record{
		PrincipalID: req.PrincipalID,
		Class:       req.Class,
		Permissions: authz.Strings(req.Permissions),
		IssuedAt:    now,
		ExpiresAt:   now.Add(ttl),
		IssuedBy:    req.IssuedBy,
		TokenHash:   hashToken(token),
	}

	// MP6 fail-closed.
	if err := s.events.Publish(ctx, secevents.MachineIssued, secevents.Event{
		ActorRef:    req.ActorRef,
		PrincipalID: rec.PrincipalID,
		Class:       string(rec.Class),
		Permissions: rec.Permissions,
		ExpiresAt:   rec.ExpiresAt,
		At:          now,
	}); err != nil {
		return "", zero, fmt.Errorf("refusing to issue: audit event could not be published (MP6 fail-closed): %w", err)
	}

	body, err := json.Marshal(rec)
	if err != nil {
		return "", zero, fmt.Errorf("marshal machine principal record: %w", err)
	}
	if _, err := kv.Create(rec.TokenHash, body); err != nil {
		return "", zero, fmt.Errorf("persist machine principal: %w", err)
	}
	return token, rec, nil
}

func clampTTL(class Class, requested time.Duration) (time.Duration, error) {
	var def, max time.Duration
	switch class {
	case ClassBootstrap:
		def, max = BootstrapTTL, BootstrapTTL
	case ClassOrg:
		def, max = OrgDefaultTTL, OrgMaxTTL
	default:
		return 0, fmt.Errorf("unknown machine principal class %q", class)
	}
	if requested <= 0 {
		return def, nil
	}
	if requested > max {
		return 0, fmt.Errorf("ttl %s exceeds the %s class ceiling of %s", requested, class, max)
	}
	return requested, nil
}

// Resolve turns a bearer token into an authorized principal, or refuses.
//
// This is §19.3's step 3 plus MP1, MP2 and the rate limit, in the order that
// makes each failure cheapest:
//
//	prefix → hash → cache → KV → constant-time compare → expiry → class
//	predicate → rate limit → lastUsedAt/first-use
//
// The class predicate deliberately sits INSIDE the cached path's miss branch
// and the cache TTL is what bounds its staleness: re-querying Neo4j on every
// request would put an authentication-blocking graph round-trip on a 60/min
// hot loop, and §19.3 already fixes the acceptable staleness at ≤60s for this
// exact reason.
func (s *Store) Resolve(ctx context.Context, token string) (authz.Principal, Record, error) {
	var zeroP authz.Principal
	var zeroR Record

	if !HasTokenPrefix(token) {
		return zeroP, zeroR, ErrNotFound
	}
	key := hashToken(token)
	now := s.now()

	rec, cached := s.cacheGet(key, now)
	if !cached {
		kv, err := s.bucket()
		if err != nil {
			return zeroP, zeroR, err
		}
		entry, err := kv.Get(key)
		if err != nil {
			if errors.Is(err, nats.ErrKeyNotFound) {
				return zeroP, zeroR, ErrNotFound
			}
			return zeroP, zeroR, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		if err := json.Unmarshal(entry.Value(), &rec); err != nil {
			return zeroP, zeroR, fmt.Errorf("%w: malformed record", ErrUnavailable)
		}
		// Constant-time comparison of the presented token's hash against the
		// stored one. The KV lookup itself is a keyed fetch and is not
		// constant-time, but it reveals only whether a 256-bit hash exists;
		// this comparison is what the brief asked for and it is free.
		if subtle.ConstantTimeCompare([]byte(rec.TokenHash), []byte(key)) != 1 {
			return zeroP, zeroR, ErrNotFound
		}
		if !rec.Active(now) {
			// MP2 layer 1. Emit once and drop the record so the bucket's own
			// max-age backstop is not the only thing that ever cleans up.
			s.emit(ctx, secevents.MachineExpired, secevents.Event{
				ActorRef:    "system",
				PrincipalID: rec.PrincipalID,
				Class:       string(rec.Class),
				Reason:      "ttl",
				At:          now,
			})
			_ = kv.Purge(key)
			return zeroP, zeroR, ErrExpired
		}
		ok, err := s.classValid(ctx, rec)
		if err != nil {
			// UNDETERMINABLE → deny. §8.5's standing rule, restated for this
			// path by the task brief: "판정 불가 시 거부(fail-closed)".
			return zeroP, zeroR, fmt.Errorf("%w: %v", ErrClassInvalid, err)
		}
		if !ok {
			s.emit(ctx, secevents.MachineDenied, secevents.Event{
				ActorRef:    "machine:" + rec.PrincipalID,
				PrincipalID: rec.PrincipalID,
				Class:       string(rec.Class),
				Reason:      "class-predicate-false",
				At:          now,
			})
			return zeroP, zeroR, ErrClassInvalid
		}
		s.cachePut(key, rec, now)
	}

	if !s.rateAllow(rec.PrincipalID, now) {
		s.emit(ctx, secevents.MachineRateExceeded, secevents.Event{
			ActorRef:    "machine:" + rec.PrincipalID,
			PrincipalID: rec.PrincipalID,
			Class:       string(rec.Class),
			Reason:      fmt.Sprintf("%d/min", RateLimitPerMinute),
			At:          now,
		})
		return zeroP, rec, ErrRateLimited
	}

	s.noteUse(ctx, key, rec, now)

	return authz.Principal{
		Kind:        authz.KindMachine,
		MachineID:   rec.PrincipalID,
		Permissions: authz.FromStrings(rec.Permissions),
	}, rec, nil
}

// Revalidate re-checks a token that ALREADY authenticated, for a connection
// that is still open. It is Resolve minus the three things that would be wrong
// to repeat, and minus the one thing that would make it useless:
//
//	minus the positive cache  — this check must be AUTHORITATIVE. Re-reading a
//	                            cached entry would answer "was it valid ≤60s
//	                            ago", which is the question we already know the
//	                            answer to. It goes to the KV every time.
//	minus the rate limiter    — a watchdog tick is the server's own doing, not
//	                            the caller's; charging the caller's 60/min
//	                            budget for it would let a long SSE connection
//	                            throttle the CI loop that opened it.
//	minus lastUsedAt/first-use — a heartbeat is not a use. Counting it would
//	                            make "마지막 사용 시각" report activity for an
//	                            idle connection, which is exactly the fact an
//	                            operator is looking at that field to learn.
//
// WHY THIS EXISTS. SSE connections are long-lived, so authentication at
// connect time is not authentication for the life of the connection. Without
// this, MP5 ("즉시 리보크") would be silently false for the one endpoint where
// a stale credential keeps RECEIVING data — a revoked token would go on
// streaming org events until the client chose to hang up. The same hole
// existed (and was measured, see IMPLEMENTATION-NOTES.md §104) for revoked
// PERSON sessions, which is why the guard's watchdog re-checks every principal
// kind, not just this one.
func (s *Store) Revalidate(ctx context.Context, token string) error {
	if !HasTokenPrefix(token) {
		return ErrNotFound
	}
	key := hashToken(token)
	now := s.now()

	kv, err := s.bucket()
	if err != nil {
		return err
	}
	entry, err := kv.Get(key)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var rec Record
	if err := json.Unmarshal(entry.Value(), &rec); err != nil {
		return fmt.Errorf("%w: malformed record", ErrUnavailable)
	}
	if subtle.ConstantTimeCompare([]byte(rec.TokenHash), []byte(key)) != 1 {
		return ErrNotFound
	}
	if !rec.Active(now) {
		return ErrExpired
	}
	ok, err := s.classValid(ctx, rec)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrClassInvalid, err)
	}
	if !ok {
		return ErrClassInvalid
	}
	return nil
}

// classValid is MP1 — §19.4's "유효 조건" column.
//
//	bootstrap : ¬( currentOrgRevision >= 1 ∧ org.admin 보유자 중 자격증명 보유자 >= 1 )
//	            — literally the same predicate as H10's, negated
//	org       : issuer :Person active ∧ granted codes still in the catalog
//
// Neither branch is a hardcoded constant, and the bootstrap branch in
// particular is NOT `return true`: it delegates to internal/orgstate, which
// reads the live graph and returns an error when it cannot. When §20's RBAC
// and §5's credential store land, orgstate's second conjunct starts returning
// a real count and this function becomes correct with no edit here.
func (s *Store) classValid(ctx context.Context, rec Record) (bool, error) {
	switch rec.Class {
	case ClassBootstrap:
		return s.org.MasterWindowOpen(ctx)

	case ClassOrg:
		// "부여된 코드가 여전히 카탈로그에 존재" — a code the graph once knew
		// and this build no longer does must not silently keep authorizing.
		if len(authz.FromStrings(rec.Permissions)) != len(rec.Permissions) {
			return false, nil
		}
		// "발급자 :Person이 활성(closedAtRevision IS NULL)".
		//
		// [2026-07-27] TURNED ON. The previous build returned an error here —
		// deny, not "assume true" — because there were no :Person sessions and
		// therefore no way to evaluate the predicate; §103.4 recorded that as
		// the deliberate choice over the forbidden alternative ("판정할 수
		// 없으면 참으로 친다"). The RBAC task supplies the missing half, so the
		// branch now evaluates against the live graph.
		//
		// This is also MP5's automatic half: when a person is deactivated,
		// every org-class principal they issued stops validating on the NEXT
		// REQUEST — no cleanup job, no subscription, no orphan window beyond
		// the ≤60s positive cache. The predicate is over the world, not over a
		// flag someone remembered to flip (MP1).
		if s.people == nil {
			return false, fmt.Errorf("issuer liveness undeterminable: no graph connection")
		}
		if rec.IssuedBy == "" {
			return false, fmt.Errorf("org-class record has no issuer")
		}
		alive, err := s.people.PersonIsActive(ctx, rec.IssuedBy)
		if err != nil {
			return false, fmt.Errorf("issuer liveness undeterminable: %w", err)
		}
		return alive, nil

	default:
		return false, fmt.Errorf("unknown class %q", rec.Class)
	}
}

// noteUse implements §19.7's last line: reads do NOT emit an event each; the
// record's lastUsedAt is updated instead. First use is the one exception and
// emits security.machine.first_used exactly once.
func (s *Store) noteUse(ctx context.Context, key string, rec Record, now time.Time) {
	first := rec.FirstUsedAt == nil

	s.mu.Lock()
	last, seen := s.lastUsedWrite[rec.PrincipalID]
	due := !seen || now.Sub(last) >= lastUsedWriteInterval
	if due {
		s.lastUsedWrite[rec.PrincipalID] = now
	}
	s.mu.Unlock()

	if first {
		s.emit(ctx, secevents.MachineFirstUsed, secevents.Event{
			ActorRef:    "machine:" + rec.PrincipalID,
			PrincipalID: rec.PrincipalID,
			Class:       string(rec.Class),
			At:          now,
		})
	}
	if !first && !due {
		return
	}

	kv, err := s.bucket()
	if err != nil {
		return
	}
	updated := rec
	t := now
	updated.LastUsedAt = &t
	if first {
		updated.FirstUsedAt = &t
	}
	body, err := json.Marshal(updated)
	if err != nil {
		return
	}
	if _, err := kv.Put(key, body); err != nil {
		// Non-fatal: lastUsedAt is observability, not authorization. Never log
		// the key (it is the token's hash) — only the public principal id.
		log.Printf("machineauth: could not update lastUsedAt for %s: %v", updated.PrincipalID, err)
		return
	}
	s.cachePut(key, updated, now)
}

// List returns every live record. Used by the PersonOnly Ops listing and by
// Revoke (which needs id → key, and the key is a hash it cannot compute
// without the token).
func (s *Store) List(ctx context.Context) ([]Record, error) {
	kv, err := s.bucket()
	if err != nil {
		return nil, err
	}
	keys, err := kv.Keys()
	if err != nil {
		if errors.Is(err, nats.ErrNoKeysFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	now := s.now()
	out := make([]Record, 0, len(keys))
	for _, k := range keys {
		entry, err := kv.Get(k)
		if err != nil {
			continue
		}
		var rec Record
		if json.Unmarshal(entry.Value(), &rec) != nil {
			continue
		}
		if !rec.Active(now) {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// Revoke is MP5. Deleting the KV key is the revocation; the ≤60s positive
// cache is also dropped here so a revoke issued by this process takes effect
// immediately for this process (the ≤60s ceiling in §19.3 covers the
// multi-replica case this build does not have yet — it is stated, not hidden).
//
// Best-effort audit, unlike Issue: see secevents' package doc for why refusing
// to revoke because the bus is down would be the wrong trade.
func (s *Store) Revoke(ctx context.Context, principalID, actorRef string) error {
	kv, err := s.bucket()
	if err != nil {
		return err
	}
	keys, err := kv.Keys()
	if err != nil {
		if errors.Is(err, nats.ErrNoKeysFound) {
			return ErrNotFound
		}
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	now := s.now()
	for _, k := range keys {
		entry, gerr := kv.Get(k)
		if gerr != nil {
			continue
		}
		var rec Record
		if json.Unmarshal(entry.Value(), &rec) != nil {
			continue
		}
		if rec.PrincipalID != principalID {
			continue
		}
		if perr := kv.Purge(k); perr != nil {
			return fmt.Errorf("revoke %s: %w", principalID, perr)
		}
		s.cacheDrop(k)
		s.emit(ctx, secevents.MachineRevoked, secevents.Event{
			ActorRef:    actorRef,
			PrincipalID: rec.PrincipalID,
			Class:       string(rec.Class),
			At:          now,
		})
		return nil
	}
	return ErrNotFound
}

// emit is the best-effort audit path (everything except issuance).
func (s *Store) emit(ctx context.Context, subject string, ev secevents.Event) {
	if err := s.events.Publish(ctx, subject, ev); err != nil {
		log.Printf("machineauth: audit event %s could not be published: %v", subject, err)
	}
}

func (s *Store) cacheGet(key string, now time.Time) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache.entries[key]
	if !ok {
		return Record{}, false
	}
	if now.Sub(e.validated) > PositiveCacheTTL {
		delete(s.cache.entries, key)
		return Record{}, false
	}
	if !e.rec.Active(now) {
		delete(s.cache.entries, key)
		return Record{}, false
	}
	return e.rec, true
}

func (s *Store) cachePut(key string, rec Record, now time.Time) {
	s.mu.Lock()
	s.cache.entries[key] = cacheEntry{rec: rec, validated: now}
	s.mu.Unlock()
}

func (s *Store) cacheDrop(key string) {
	s.mu.Lock()
	delete(s.cache.entries, key)
	s.mu.Unlock()
}

func (s *Store) rateAllow(id string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limiter.allow(id, now)
}
