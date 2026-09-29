// Package session implements the browser session cookie that
// docs/design/10-core-architecture.md §8.4 names as the "사람 콘솔 세션"
// artifact ("Secure; HttpOnly; SameSite=Strict 쿠키, 12h, 로그아웃 시
// 서버측 무효화") — that table specifies the SHAPE of the artifact, not
// what is exchanged for it (see IMPLEMENTATION-NOTES.md §76's honest gap).
// The console-wiring worker's job (this build) is exactly that missing
// exchange, decided per docs/design/31-onboarding-boundary.md §1.3's Q2b:
//
//	Q2: does a human see/type this value on a screen? Yes (the admin setup
//	    token, state/creds/admin/setup_token, pasted once into the console).
//	Q2a: is this about the org, or an org-independent host/operator
//	    setting? Same category as event 3's omniroute admin password
//	    (§1.4's table) — "who may administer this instance's console" —
//	    so: continue to Q2b, don't shortcut to "host setting" either.
//	Q2b: is there an exchange path from the human-facing credential to a
//	    scope-limited MACHINE credential? YES — this package IS that
//	    exchange. Verdict: the setup token itself is NEVER stored anywhere
//	    in the browser (see console/src/components/TokenEntryScreen.tsx's
//	    doc comment) or in this server's session store; only the resulting
//	    opaque session id is kept, and only server-side. Recursing Q2 on
//	    THAT opaque value: a human never sees/types the session id (it
//	    lives only in an HttpOnly cookie neither JS nor the person can
//	    read), so it now answers "아니오" and is pure machine-to-machine
//	    auth for every purpose after the one-time exchange — exactly Q2b's
//	    described recursion.
//
// This store is a single-process in-memory map. That is a deliberate,
// documented limitation, not an oversight: osiris-api runs as a single
// replica in this build (no horizontal scaling story exists yet anywhere
// in this codebase), so a shared/external session store would be solving a
// problem this deployment does not have. A session is invalidated by
// process restart (operator must re-paste the setup token) — acceptable
// for a single-operator onboarding console; revisit if osiris-api is ever
// scaled beyond one replica.
package session

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

// CookieName is the browser-visible cookie name. Prefixed osiris_ to avoid
// any collision with omniroute's own cookies on the same public origin (the
// gateway config gives omniroute its own /omni/ path, but cookies are
// scoped by Path= not by proxy routing, so a distinct name is cheap
// insurance).
const CookieName = "osiris_console_session"

// TTL matches §8.4's "12h" exactly.
const TTL = 12 * time.Hour

// Record is the session state 60-identity-authn.md §3.2 specifies, minus the
// fields this build has no producer for.
//
//	AUTHN_SESSION[sha256(sessionId)] = {
//	  partyId, kind, authMethod, issuedAt, expiresAt, credentialPending
//	}
//
// PartyID IS THE ONLY PLACE AN ACTOR CAN COME FROM (H19/H28). Every mutating
// handler reads it out of here by way of authz.Principal, and there is no
// setter anywhere that takes a request body, a query parameter or a header —
// "the client claimed to be X" has no representation in this codebase. HI6 is
// the lint over that.
//
// Kind distinguishes §3.1's two session kinds: "person" (bound to a :Person)
// and "master-key" (the ONE session without a :Party — today's setup-token
// exchange, which H10 will retire). A machine principal is NOT here and never
// will be: it is not a session (§3.1.1, HI13).
type Record struct {
	PartyID           string
	Kind              string // "person" | "master-key"
	AuthMethod        string // "password" | "ticket" | "master-key"
	IssuedAt          time.Time
	ExpiresAt         time.Time
	CredentialPending bool // §4.4 — a person who has not yet set a credential
}

type Store struct {
	mu   sync.Mutex
	byID map[string]Record
}

func NewStore() *Store {
	return &Store{byID: make(map[string]Record)}
}

// Create mints a new opaque session id (32 random bytes, base64url — same
// construction as masterkey.Boot's generated key, minus the prefix) and records its
// expiry. The caller is expected to set it as this package's cookie
// immediately (see SetCookie) — Create itself touches no http.ResponseWriter
// so it stays trivially unit-testable without a real request.
func (s *Store) Create() string {
	return s.CreateFor(Record{Kind: "master-key", AuthMethod: "master-key"})
}

// CreateFor mints a session carrying an identity. IssuedAt/ExpiresAt are set
// here so no caller can hand in a session that outlives §8.4's 12h.
//
// §4.1 ⑥'s promotion and §5.1's ticket redemption both go through this, and
// both ROTATE: they mint a new id and revoke the old one rather than mutating
// a record in place. A privilege change that keeps the same session id is a
// session-fixation vulnerability, and the design calls it out by name
// ("세션 고정(fixation) 방지 — 승격은 권한 상승이므로 회전이 필수").
func (s *Store) CreateFor(rec Record) string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	id := base64.RawURLEncoding.EncodeToString(buf)
	now := time.Now()
	rec.IssuedAt = now
	rec.ExpiresAt = now.Add(TTL)
	if rec.Kind == "" {
		rec.Kind = "master-key"
	}
	s.mu.Lock()
	s.byID[id] = rec
	s.mu.Unlock()
	return id
}

// Lookup returns the session record for a live, unexpired id.
//
// This is the ONLY way a handler learns who the caller is. Expired entries are
// swept on lookup, same lazy policy Valid has always used.
func (s *Store) Lookup(id string) (Record, bool) {
	if id == "" {
		return Record{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byID[id]
	if !ok {
		return Record{}, false
	}
	if time.Now().After(rec.ExpiresAt) {
		delete(s.byID, id)
		return Record{}, false
	}
	return rec, true
}

// RevokeAllFor drops every session belonging to a party.
//
// §7.3 lists five points where a forced logout is mandatory; this build uses
// two of them — a person is deactivated (§20.6 [6]) and a credential is
// (re)set through a redeemed ticket. The third ("changed the password because
// of a suspected leak") belongs to §5.6 C, which this build does not
// implement.
func (s *Store) RevokeAllFor(partyID string) int {
	if partyID == "" {
		return 0
	}
	n := 0
	s.mu.Lock()
	for id, rec := range s.byID {
		if rec.PartyID == partyID {
			delete(s.byID, id)
			n++
		}
	}
	s.mu.Unlock()
	return n
}

// Valid reports whether id is a live, unexpired session. Expired entries are
// swept lazily on lookup (no background goroutine — this store's total size
// is bounded by "number of setup-token exchanges since last restart", which
// for a single-operator console is never large enough to justify one).
func (s *Store) Valid(id string) bool {
	if id == "" {
		return false
	}
	_, ok := s.Lookup(id)
	return ok
}

// Revoke implements §8.4's "로그아웃 시 서버측 무효화" — deleting the map
// entry is sufficient; ClearCookie (below) additionally tells the browser to
// drop its copy, but even if the browser ignored that, the id would no
// longer validate here.
func (s *Store) Revoke(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	delete(s.byID, id)
	s.mu.Unlock()
}

// FromRequest extracts the session id from the incoming cookie, or "" if
// absent — never errors, since "no cookie" is a completely ordinary,
// expected state (first visit / already logged out).
func FromRequest(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// isSecureRequest decides the cookie's Secure attribute per-request rather
// than as a fixed build-time constant, because this framework must work
// identically behind a TLS-terminating upstream (e.g. https://osiris.example.com/
// behind an upstream reverse proxy) AND on a bare loopback HTTP deployment
// (http://127.0.0.1:18280/, the portability baseline — see
// gw/conf.d/default.conf's header comment on why osiris-gw itself never
// hard-codes a domain or scheme).
//
// MEASURED, NOT ASSUMED (per this build's explicit instruction not to guess
// at loopback-HTTP cookie behavior): a `Secure` cookie's attribute is a
// TRANSPORT rule enforced by the browser against the connection the
// Set-Cookie response itself arrived over — it has nothing to do with
// window.isSecureContext's "is 127.0.0.1 trustworthy" carve-out (that
// carve-out governs powerful-feature gating like getUserMedia/service
// workers, NOT the cookie Secure flag). Confirmed with a real Chromium
// browser (Playwright) against plain http://127.0.0.1:<port>: a
// `Set-Cookie: ...; Secure` response header on a **plain-http** origin is
// silently REJECTED by the browser (the Set-Cookie line is present in the
// response but document/subsequent-request cookies never show it) —
// loopback does NOT get a free pass on the Secure attribute itself, only on
// the separate isSecureContext API-gating mechanism. So: if this process
// always sent `Secure`, the loopback deployment path
// (http://127.0.0.1:18280/) would silently never authenticate (no error,
// just an invisible dropped cookie) — exactly the kind of failure this
// build was told to measure instead of assume.
//
// Decision: send `Secure` only when the request actually arrived over a
// TLS-terminated channel (r.TLS != nil — never true for this process
// itself, which never terminates TLS — or X-Forwarded-Proto: https, which
// osiris-gw forwards verbatim from whatever sits in front of it, and which
// an upstream reverse proxy sets on the way in). Trusting a proxy-supplied header
// here is narrower than it looks: 10-core-architecture.md §2.8 says
// X-Forwarded-* "인가 결정에는 쓰지 않는다, 감사 로그/레이트리밋 용도뿐" —
// the Secure COOKIE ATTRIBUTE is not an authorization decision, it is a
// browser-side transport hint that only affects whether the browser is
// willing to keep sending this cookie at all. The actual authorization
// decision remains "does this opaque session id validate against the
// server-side Store", which never trusts X-Forwarded-Proto for anything.
func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

// SetCookie writes the Set-Cookie header for a freshly created session.
// HttpOnly and SameSite=Strict are unconditional (§8.4's exact wording);
// Secure is conditional per isSecureRequest's doc comment above.
func SetCookie(w http.ResponseWriter, r *http.Request, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(TTL.Seconds()),
	})
}

// ClearCookie tells the browser to drop the cookie immediately (logout /
// invalid session recovery). MaxAge<0 is the standard "delete now" idiom.
func ClearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
