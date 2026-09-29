// Package authncred is the AUTHN_CRED store — the JetStream KV bucket holding
// one Argon2id verifier per :Person (60-identity-authn.md §3.2, §5.4).
//
// ── WHY THIS PACKAGE EXISTS IN THE RBAC TASK ──────────────────────────────
//
// It is not here to build the login screen; the password-change flow (§5.6 C),
// the master key rename (H22), break-glass (H17) and the console's redemption
// UI are all still later work and none of them is implemented. It is here
// because DG4 CANNOT BE BUILT WITHOUT IT.
//
// 60 §21.1's DG4 has two layers, and §21.1 is explicit that they cannot be
// merged: the graph guarantees "at least one active :Person holds org.admin"
// (I14), and osiris-api must additionally guarantee that "그중 최소 1명이
// 사용 가능한 자격증명을 보유". The second sentence names AUTHN_CRED. A
// CredentialDirectory that has no credential store to query can only ever
// return a measured zero, which makes DG4's API layer either vacuous (if you
// read zero as "fine") or total (if you read it as "refuse everything") — and
// both readings are wrong. So the minimum credential surface DG4 needs is
// built here, and nothing beyond it.
//
// WHAT IS DELIBERATELY ABSENT, so a later worker does not think it is done:
// password CHANGE with current-password re-confirmation (§5.6 C) and its
// "revoke the person's other sessions" step, mustChange, password history,
// break-glass redemption, the §8.2 L1/L2/L3 throttle layers, and any console
// screen. See IMPLEMENTATION-NOTES.md §105 for the scope judgment.
//
// ── SECRET HYGIENE ────────────────────────────────────────────────────────
//
// The plaintext password exists for the duration of one request and is never
// logged, never echoed, never stored. What the bucket holds is an Argon2id
// VERIFIER: not reversible, not submittable to any other system, not reusable
// (§5.3's 판정 ① Q2b is what that distinction is for — it is the reason a
// password verifier is not "storing a credential" the way 사건 4 was).
package authncred

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"golang.org/x/crypto/argon2"
)

// BucketName is §3.2's AUTHN_CRED. Keyed by :Person.id — NOT by a hash of it:
// the person id is a public identifier that already appears in the graph, in
// event payloads and in the console, so hashing it would buy nothing and would
// make "does this person have a credential" unanswerable without a full scan,
// which is the exact question DG4 asks on every admin-affecting change.
const BucketName = "AUTHN_CRED"

// Argon2id parameters — §5.4's "예: m=64MiB, t=3, p=1", with the tuning left
// to the implementation worker.
//
// MEASURED RATHER THAN COPIED: at m=64MiB/t=3/p=1 one hash costs roughly a
// tenth of a second on this host, which is the usual target for an interactive
// login. §8.2 already flags Argon2id's CPU cost as a resource-exhaustion
// vector and puts a concurrency ceiling in front of the login endpoint; that
// ceiling is §8's work and is NOT implemented here, so the parameters are kept
// at the design's stated example rather than raised.
//
// They are stored WITH each record. A future re-tune must be able to verify
// passwords hashed under the old parameters, and a verifier that cannot say
// how it was computed is a verifier that pins its parameters forever.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// MinPasswordLength is §5.4's NIST SP 800-63B-style policy: a length floor and
// nothing else. No character-class rules — they push people toward predictable
// patterns and buy no entropy. No maximum, and no truncation.
const MinPasswordLength = 12

var (
	ErrUnavailable = errors.New("credential store unavailable")
	ErrNotFound    = errors.New("no credential for this person")
	ErrWeak        = fmt.Errorf("password must be at least %d characters", MinPasswordLength)
)

// Record is the stored verifier. There is no field here from which a password
// can be recovered, and there is deliberately no field for one.
type Record struct {
	PersonID string `json:"personId"`

	// Algo/Time/Memory/Threads describe how Hash was produced, so parameters
	// can be re-tuned without invalidating every existing credential.
	Algo    string `json:"algo"`
	Time    uint32 `json:"time"`
	Memory  uint32 `json:"memory"`
	Threads uint8  `json:"threads"`

	Salt string `json:"salt"` // base64 raw-url
	Hash string `json:"hash"` // base64 raw-url — the Argon2id verifier

	SetAt time.Time `json:"setAt"`

	// AuthMethod records how the credential came to exist ("ticket" |
	// "master-key"), for the audit trail. Never a credential value.
	AuthMethod string `json:"authMethod"`
}

type Store struct {
	js  nats.JetStreamContext
	now func() time.Time

	mu sync.Mutex
	kv nats.KeyValue
}

func New(js nats.JetStreamContext) *Store {
	return &Store{js: js, now: func() time.Time { return time.Now().UTC() }}
}

// bucket binds (or creates) AUTHN_CRED lazily, for the boot-ordering reason
// machineauth.Store.bucket documents at length: the NATS grant that permits
// $KV.AUTHN_CRED arrives with osiris-orchestrator's reconcile tick, which can
// lose a race with osiris-api's start. Every call retries; until it succeeds
// every credential operation fails CLOSED.
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
			Description: "osiris-api person credential verifiers (60-identity-authn.md §3.2/§5.4). Argon2id verifiers only; no password is ever stored.",
			// History 1: §5.6's "history=1 이므로 구 검증자 미보존" — a
			// superseded verifier is not something this system has any use
			// for, and keeping it would keep it readable in the stream.
			History: 1,
			Storage: nats.FileStorage,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	s.mu.Lock()
	s.kv = kv
	s.mu.Unlock()
	return kv, nil
}

// Set writes (or replaces) a person's verifier.
//
// The caller is responsible for having established WHO this is — a redeemed
// enrollment ticket, or the §4.1 ⑥ master-key promotion. This function does
// not authenticate; it stores.
func (s *Store) Set(ctx context.Context, personID, password, authMethod string) error {
	if personID == "" {
		return fmt.Errorf("personId is required")
	}
	if len([]rune(password)) < MinPasswordLength {
		return ErrWeak
	}
	kv, err := s.bucket()
	if err != nil {
		return err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generate salt: %w", err)
	}
	sum := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	rec := Record{
		PersonID: personID,
		Algo:     "argon2id",
		Time:     argonTime, Memory: argonMemory, Threads: argonThreads,
		Salt:       base64.RawURLEncoding.EncodeToString(salt),
		Hash:       base64.RawURLEncoding.EncodeToString(sum),
		SetAt:      s.now(),
		AuthMethod: authMethod,
	}
	body, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal credential record: %w", err)
	}
	if _, err := kv.Put(personID, body); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

// Verify checks a password against the stored verifier in constant time.
//
// Returns (false, nil) for both "no such credential" and "wrong password".
// The caller must not distinguish them in its response — §8.2's "응답은
// 성공/실패 모두 동일한 일반 메시지", so that an attacker cannot use the login
// endpoint to enumerate which people have accounts.
func (s *Store) Verify(ctx context.Context, personID, password string) (bool, error) {
	rec, err := s.Get(ctx, personID)
	if errors.Is(err, ErrNotFound) {
		// Deliberately still spend the hash. Returning early on "no such
		// person" makes the response time itself the enumeration oracle the
		// generic message was meant to close.
		dummy := make([]byte, saltLen)
		argon2.IDKey([]byte(password), dummy, argonTime, argonMemory, argonThreads, argonKeyLen)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	salt, err := base64.RawURLEncoding.DecodeString(rec.Salt)
	if err != nil {
		return false, fmt.Errorf("%w: malformed credential record", ErrUnavailable)
	}
	want, err := base64.RawURLEncoding.DecodeString(rec.Hash)
	if err != nil {
		return false, fmt.Errorf("%w: malformed credential record", ErrUnavailable)
	}
	got := argon2.IDKey([]byte(password), salt, rec.Time, rec.Memory, rec.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// Get returns the record (never the password) or ErrNotFound.
func (s *Store) Get(ctx context.Context, personID string) (Record, error) {
	var rec Record
	kv, err := s.bucket()
	if err != nil {
		return rec, err
	}
	entry, err := kv.Get(personID)
	if errors.Is(err, nats.ErrKeyNotFound) {
		return rec, ErrNotFound
	}
	if err != nil {
		return rec, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := json.Unmarshal(entry.Value(), &rec); err != nil {
		return rec, fmt.Errorf("%w: malformed credential record", ErrUnavailable)
	}
	return rec, nil
}

// Has reports whether a person holds a usable credential.
func (s *Store) Has(ctx context.Context, personID string) (bool, error) {
	_, err := s.Get(ctx, personID)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}

// CountAmong returns how many of the given person ids hold a credential.
//
// This is the function DG4's second layer and H10's second conjunct are both
// written in terms of. It takes the ID LIST FROM THE GRAPH and answers only
// the KV question — the two stores never learn about each other, which is
// H1's one-way reference expressed as a function signature.
//
// An error means UNDETERMINABLE. It must never be reported as zero: zero
// credentialed admins is what OPENS the master-key window (§4.3), so a
// storage outage read as zero would resurrect a bootstrap credential by
// unplugging a database — the inversion §8.5 forbids.
func (s *Store) CountAmong(ctx context.Context, personIDs []string) (int, []string, error) {
	n := 0
	var have []string
	for _, id := range personIDs {
		ok, err := s.Has(ctx, id)
		if err != nil {
			return 0, nil, err
		}
		if ok {
			n++
			have = append(have, id)
		}
	}
	return n, have, nil
}

// Revoke deletes a person's verifier — the credential half of offboarding
// (§14.1: the graph is the truth and the KV follows).
func (s *Store) Revoke(ctx context.Context, personID string) error {
	kv, err := s.bucket()
	if err != nil {
		return err
	}
	if err := kv.Purge(personID); err != nil && !errors.Is(err, nats.ErrKeyNotFound) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

// NormalizeEmail is the login identifier's canonical form. Case-insensitive
// and trimmed, because a person typing their own address into a login box
// should not have to reproduce the capitalization an administrator used.
func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
