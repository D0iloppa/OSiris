// Package enrollment is the AUTHN_TICKET store — one-time enrollment tickets
// (60-identity-authn.md §5.1, H12).
//
// ── WHAT A TICKET IS, AND WHY IT IS A DELEGATION-CONTROLLED OPERATION ─────
//
// A ticket is the ONE thing a newly created :Person receives so they can prove
// who they are the first time. §5.1's table: 32 random bytes, base64url,
// plaintext shown exactly once in the issue response, stored as
// AUTHN_TICKET[sha256(ticket)], 7-day TTL, and its scope is precisely one
// action — setting the bound person's initial credential.
//
// §5.3's 판정 ⑤ found the thing that makes issuance dangerous, and found it by
// actually walking the credential-classification procedure rather than copying
// a previous verdict: because a ticket lets its holder set a NEW password on
// the bound account, ISSUING A TICKET FOR SOMEONE IS TAKING OVER THEIR
// ACCOUNT. A manager who could issue a ticket for an administrator would own
// that administrator, and no role check anywhere would have been violated. So
// ticket issuance goes through authz.CheckDelegation under DG2 at the same
// strength as demotion (C10), and it is the reason
// ActionIssueEnrollmentTicket exists as its own action.
//
// ── DELIVERY: NO EMAIL DEPENDENCY ─────────────────────────────────────────
//
// §5.2 rejects SMTP as the default: it would require a mail host, which is a
// site-specific fact this framework must not require, and it would make "어디
// 서든 install → 루프백 UI" false. The default is out-of-band — an
// administrator reads the ticket once and hands it over. This package
// therefore SENDS NOTHING; it mints, stores a hash, and returns the plaintext
// to its caller exactly once.
package enrollment

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
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// BucketName is §3.2's AUTHN_TICKET.
const BucketName = "AUTHN_TICKET"

// TokenPrefix marks a ticket the way `osm_` marks a machine principal (§19.3's
// argument for prefixes: the guard picks a branch without trial-and-error, a
// secret scanner gets a pattern, and a leaked value identifies itself).
const TokenPrefix = "ost_"

// TTL is §5.1's 7 days. Enforced twice — the record's expiresAt (policy) and
// the bucket's own max age (backstop) — the same two-layer shape MP2 uses for
// machine principals, and for the same reason: a bug that skips the first is
// still caught by the second.
const TTL = 7 * 24 * time.Hour

var (
	ErrUnavailable = errors.New("enrollment ticket store unavailable")
	ErrNotFound    = errors.New("no such enrollment ticket")
	ErrExpired     = errors.New("enrollment ticket expired")
)

// Record is what AUTHN_TICKET holds. Keyed by hex(sha256(ticket)); the
// plaintext is NOT a field here and never will be (H3).
type Record struct {
	PersonID  string    `json:"personId"`
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`

	// IssuedBy is the issuing :Person.id. Unlike a machine principal's
	// IssuedBy this is always a real party, because ticket issuance requires
	// org.manage.people, which only a person can hold.
	IssuedBy string `json:"issuedBy"`

	// TokenHash duplicates the KV key so redemption can finish with a
	// constant-time comparison (same reasoning as machineauth.Record).
	TokenHash string `json:"tokenHash"`
}

func (r Record) Active(now time.Time) bool { return now.Before(r.ExpiresAt) }

type Store struct {
	js  nats.JetStreamContext
	now func() time.Time

	mu sync.Mutex
	kv nats.KeyValue
}

func New(js nats.JetStreamContext) *Store {
	return &Store{js: js, now: func() time.Time { return time.Now().UTC() }}
}

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
			Description: "osiris-api enrollment tickets (60-identity-authn.md §5.1). sha256-keyed; no plaintext ticket is ever stored.",
			History:     1,
			TTL:         TTL,
			Storage:     nats.FileStorage,
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

// HasTokenPrefix lets the login endpoint pick the ticket branch without
// comparing against anything.
func HasTokenPrefix(v string) bool { return strings.HasPrefix(v, TokenPrefix) }

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func mint() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate enrollment ticket: %w", err)
	}
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// Issue mints a ticket for personID and returns the plaintext EXACTLY ONCE.
//
// Any previously outstanding ticket for the same person is purged first.
// §5.1's ticket is "1회용" and bound to one person; leaving an older one alive
// would mean a re-issue silently widens the set of values that can take the
// account over, which is the opposite of what re-issuing is for.
//
// AUTHORIZATION IS NOT DONE HERE. The caller must have passed
// authz.CheckDelegation with ActionIssueEnrollmentTicket first (C10/HI16) —
// this store mints for whoever asks, exactly as machineauth.Store.Issue does,
// because putting a second copy of the delegation rules behind the storage
// layer is what H31 forbids.
func (s *Store) Issue(ctx context.Context, personID, issuedBy string) (string, Record, error) {
	var zero Record
	if personID == "" {
		return "", zero, fmt.Errorf("personId is required")
	}
	kv, err := s.bucket()
	if err != nil {
		return "", zero, err
	}
	if err := s.purgeFor(kv, personID); err != nil {
		return "", zero, err
	}

	tok, err := mint()
	if err != nil {
		return "", zero, err
	}
	now := s.now()
	rec := Record{
		PersonID:  personID,
		IssuedAt:  now,
		ExpiresAt: now.Add(TTL),
		IssuedBy:  issuedBy,
		TokenHash: hashToken(tok),
	}
	body, err := json.Marshal(rec)
	if err != nil {
		return "", zero, fmt.Errorf("marshal ticket record: %w", err)
	}
	if _, err := kv.Create(rec.TokenHash, body); err != nil {
		return "", zero, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return tok, rec, nil
}

// Redeem validates a ticket and CONSUMES it. One-time means one time: the
// record is purged before the caller is told which person it belonged to, so a
// failure after this point costs the ticket rather than leaving a second
// account-takeover primitive alive.
func (s *Store) Redeem(ctx context.Context, token string) (Record, error) {
	var zero Record
	if !HasTokenPrefix(token) {
		return zero, ErrNotFound
	}
	kv, err := s.bucket()
	if err != nil {
		return zero, err
	}
	key := hashToken(token)
	entry, err := kv.Get(key)
	if errors.Is(err, nats.ErrKeyNotFound) {
		return zero, ErrNotFound
	}
	if err != nil {
		return zero, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var rec Record
	if err := json.Unmarshal(entry.Value(), &rec); err != nil {
		return zero, fmt.Errorf("%w: malformed ticket record", ErrUnavailable)
	}
	if subtle.ConstantTimeCompare([]byte(rec.TokenHash), []byte(key)) != 1 {
		return zero, ErrNotFound
	}
	if !rec.Active(s.now()) {
		_ = kv.Purge(key)
		return zero, ErrExpired
	}
	if err := kv.Purge(key); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return rec, nil
}

// RevokeFor purges every outstanding ticket bound to a person — the
// offboarding path of §14.1 ("미상환 AUTHN_TICKET").
func (s *Store) RevokeFor(ctx context.Context, personID string) error {
	kv, err := s.bucket()
	if err != nil {
		return err
	}
	return s.purgeFor(kv, personID)
}

// Outstanding reports whether an unredeemed, unexpired ticket exists for a
// person. Used by the People list so an administrator can see who has been
// invited but has not joined — a fact the graph cannot answer (H1).
func (s *Store) Outstanding(ctx context.Context, personID string) (bool, error) {
	kv, err := s.bucket()
	if err != nil {
		return false, err
	}
	keys, err := listKeys(kv)
	if err != nil {
		return false, err
	}
	now := s.now()
	for _, k := range keys {
		rec, ok := readRecord(kv, k)
		if ok && rec.PersonID == personID && rec.Active(now) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) purgeFor(kv nats.KeyValue, personID string) error {
	keys, err := listKeys(kv)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if rec, ok := readRecord(kv, k); ok && rec.PersonID == personID {
			if err := kv.Purge(k); err != nil && !errors.Is(err, nats.ErrKeyNotFound) {
				return fmt.Errorf("%w: %v", ErrUnavailable, err)
			}
		}
	}
	return nil
}

func listKeys(kv nats.KeyValue) ([]string, error) {
	keys, err := kv.Keys()
	if errors.Is(err, nats.ErrNoKeysFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return keys, nil
}

func readRecord(kv nats.KeyValue, key string) (Record, bool) {
	entry, err := kv.Get(key)
	if err != nil {
		return Record{}, false
	}
	var rec Record
	if err := json.Unmarshal(entry.Value(), &rec); err != nil {
		return Record{}, false
	}
	return rec, true
}
