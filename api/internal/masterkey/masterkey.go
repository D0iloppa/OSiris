// Package masterkey owns the OSiris master key: the credential the initial
// administrator presents to log into the console for the very first time, and
// nothing else.
//
// ── IT REPLACES internal/adminauth, AND THE RENAME IS THE POINT ────────────
//
// 60-identity-authn.md §4.0 (H22) retires the name "관리 설정 토큰" and, in as
// many words, says the rename is not cosmetic: "이름만 바꾸는 것이 아니라 개념이
// 바뀐다."
//
//	                구 "설정 토큰"                     신 master key
//	정체     온보딩 0단계 화면을 가리는 정적 bearer   초기 관리자의 최초 로그인 자격증명
//	사람     없음(익명)                              장차 초기 관리자가 될 그 사람
//	수명     무기한                                   비밀번호를 세우는 순간 끝 (H23)
//
// §4.0's own diagnosis of why the old name mattered: the value was ALREADY
// behaving as a login credential ("사람이 콘솔 화면에 붙여넣어 세션을 얻는 유일한
// 진입점"), and calling it a "token" kept suggesting machine-to-machine auth,
// which "계속 흐렸다" the fact that it needed a human credential's discipline —
// bounded lifetime, rotation, revocation. The old package's doc comment is the
// evidence: it argued at length that it deliberately does NOT re-check org
// revision, because it was gating an ops surface rather than carrying an
// identity. That argument was correct for what the value then was and is wrong
// for what it now is, which is why this is a new package and not an edit.
//
// ── WHAT THIS PACKAGE DOES NOT DECIDE ─────────────────────────────────────
//
// It does not evaluate H10. Whether the master key is still usable is a
// question about the state of the WORLD (does an organization exist, can any
// administrator log in), and that predicate lives in exactly one place —
// internal/orgstate — because four design sections write it down and it must be
// the same predicate in all four (§4.3, §19.4, §19.5, MP1). This package answers
// only "is this byte string the master key", and internal/authn asks orgstate
// whether that still means anything.
//
// The split is what keeps §4.5's two dimensions independent:
//
//	판정 차원  the predicate refuses the login          → internal/orgstate
//	자료 차원  the file is deleted                       → Purge, here
//
// §4.5 states the safety property that independence buys: "삭제 실패는 ... 로그인
// 실패가 아니다 — 판정 차원이 이미 닫았으므로 파일이 남아도 그 값은 아무것도 열지
// 못한다." A build that closed the window BY deleting the file would have one
// dimension, and an undeletable file would then be an open door.
package masterkey

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeyPrefix is §19.3's `osk_`.
//
// It exists so the authentication guard can SELECT A BRANCH rather than try
// comparisons in sequence — §19.3: "가드가 시행착오 비교 없이 경로를 고를 수
// 있고". The three credential shapes a bearer header can carry are now mutually
// exclusive by construction:
//
//	osk_…   master key       (this package)
//	osm_…   machine principal (internal/machineauth)
//	other   nothing          → 401
//
// This is also why §4.0's migration table REGENERATES rather than renames the
// old file: an unprefixed legacy value cannot be routed by a prefix guard, so
// carrying it forward would mean keeping the trial-comparison shape the prefix
// was introduced to remove.
const KeyPrefix = "osk_"

// HasKeyPrefix reports whether a bearer value claims to be a master key.
//
// Claiming is not being: this is a routing decision, not an authentication one.
// It says which branch must judge the value, and that branch then refuses most
// of what reaches it.
func HasKeyPrefix(v string) bool { return strings.HasPrefix(v, KeyPrefix) }

// Guard holds the master key for comparison. The value is never returned,
// logged, or included in an error — the operator reads it with
// bin/show-master-key.sh, from the file, on the host (§9.1's argument: whoever
// can read a 0400 file on that host is already host-root-equivalent).
type Guard struct {
	key  string
	path string
}

// Outcome is what Boot decided, so that main() can log the right sentence
// without needing to re-derive the reasoning — and so that the one outcome that
// is a WARNING (Undeterminable) cannot be silently reported as routine.
type Outcome string

const (
	// OutcomeLoaded — an existing master key was read.
	OutcomeLoaded Outcome = "loaded"
	// OutcomeGenerated — no key existed and the organization does not exist
	// yet, so one was created.
	OutcomeGenerated Outcome = "generated"
	// OutcomeMigrated — a legacy unprefixed setup_token was found before any
	// organization existed; it was deleted and a prefixed key generated
	// (§4.0's migration row).
	OutcomeMigrated Outcome = "migrated"
	// OutcomeWithheld — no key exists and an organization does, so none was
	// created. §4.5: "조직이 있는데 파일이 없으면 만들지 않는다."
	OutcomeWithheld Outcome = "withheld"
	// OutcomeUndeterminable — the graph could not be read at boot, so the
	// decision was deferred rather than guessed. No key is generated and no
	// file is deleted.
	OutcomeUndeterminable Outcome = "undeterminable"
)

// Window is the H10 predicate, injected rather than computed. Satisfied by
// *orgstate.Evaluator, which is the single place those two conditions are
// written down.
type Window interface {
	Closed(ctx context.Context) (bool, error)
}

// Boot resolves the master key at process start.
//
//	                        │ H10 open            │ H10 closed      │ undeterminable
//	────────────────────────┼─────────────────────┼─────────────────┼────────────────
//	master_key present      │ load                │ load            │ load
//	only legacy setup_token │ delete legacy, gen  │ delete legacy   │ leave both
//	neither                 │ generate            │ withhold        │ withhold
//
// ── ★ DEVIATION FROM §4.0/§4.5, AND WHY IT IS THE SAFE READING ────────────
//
// The design writes these file rules against ONE condition, not two:
//
//	§4.5  "osiris-api는 currentOrgRevision == 0일 때만 없는 master key를 새로
//	      만든다 ... 조직이 있는데 파일이 없으면 만들지 않는다"
//	§4.0  migration row: "조직이 이미 있으면 삭제"
//
// THIS BUILD GATES BOTH ON THE FULL H10 PREDICATE INSTEAD, and the reason is
// that §4.3 already rejected the one-condition form for exactly this failure:
//
//	"조건 1만으로 닫으면 §4.1 ⑥이 실패한 경우 아무도 못 들어가는 잠금이 된다."
//
// The one-condition file rule reintroduces that lockout through the data
// dimension. Walk §4.1's own re-entry case: the org commits (rev = 1), then ⑥
// or ⑦ does not complete — the browser was closed, the operator went home.
// §4.1 ⑧'s note is explicit that this must be recoverable: "H10의 둘째 조건이
// 아직 거짓이므로 master key가 여전히 유효하고, 다시 들어와 ⑦부터 이어서 하면
// 된다." Now let osiris-api restart in that window. Under the literal rule it
// sees rev >= 1, so it deletes the legacy file and refuses to generate one —
// and the re-entry path the design just promised is gone, with no administrator
// credential in existence to replace it. The organization is locked out by a
// container restart.
//
// MEASURED, NOT HYPOTHETICAL: this repository's own deployment was in precisely
// that state when H10 was implemented — :OrgRevision.n = 38 from write-path
// testing, zero active :Person, one legacy setup_token. The literal rule would
// have deleted the only credential that could reach the console.
//
// Gating on the predicate makes the two dimensions of §4.5 track each other
// exactly, which is what that section's own framing asks for: the 자료 차원 is
// the 판정 차원 catching up. The file exists while the credential can open
// something and is removed once it cannot. In the state the design was
// describing (a handed-over organization) both readings agree, because there
// the predicate IS closed — this reading only differs where the literal one
// would lock the door on a half-finished handover.
//
// It does NOT weaken §4.6's rejection of a permanent root credential: the key
// is still generated only while H10 is open, still refused the instant it
// closes, and still never regenerated afterwards. §17 F-series reports this to
// the supervisor as a design-text correction rather than a silent local choice.
//
// ── WHY AN UNREADABLE GRAPH GENERATES NOTHING AND DELETES NOTHING ─────────
//
// Reading "I cannot see the graph" as "the window is open" would mint a
// bootstrap credential by unplugging a database — §8.5's forbidden inversion,
// and the same reasoning orgstate.New states for its nil RevisionReader. The
// inverse error is equally refused: deleting a legacy file we cannot classify
// would destroy the operator's only way in if the window turns out to be open.
// Undeterminable therefore means DO NOTHING, loudly.
func Boot(ctx context.Context, path, legacyPath string, window Window) (*Guard, Outcome, error) {
	g := &Guard{path: path}

	// An existing master key wins unconditionally: it is the credential an
	// operator may already hold, and no state of the world makes it correct to
	// swap it out from under them. H10 decides whether it still OPENS anything.
	if b, err := os.ReadFile(path); err == nil {
		g.key = strings.TrimSpace(string(b))
		// A legacy file alongside a live master key is dead weight that reads
		// as usable. Same argument as the migration row; failure to remove it
		// is not fatal, because nothing routes an unprefixed bearer anymore.
		_ = removeIfPresent(legacyPath)
		return g, OutcomeLoaded, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("read master key %s: %w", path, err)
	}

	legacyExists := fileExists(legacyPath)

	closed, known := windowClosed(ctx, window)
	if !known {
		return g, OutcomeUndeterminable, nil
	}
	if closed {
		// §4.0: "조직이 이미 있으면 삭제" — the legacy value is one H10 would
		// refuse, and leaving it invites someone to paste it and be told 401
		// with no explanation of why the file exists at all.
		if legacyExists {
			if err := removeIfPresent(legacyPath); err != nil {
				return nil, "", fmt.Errorf("delete superseded setup token %s: %w", legacyPath, err)
			}
		}
		return g, OutcomeWithheld, nil
	}

	key, err := generate(path)
	if err != nil {
		return nil, "", err
	}
	g.key = key
	if legacyExists {
		if err := removeIfPresent(legacyPath); err != nil {
			return nil, "", fmt.Errorf("delete superseded setup token %s: %w", legacyPath, err)
		}
		return g, OutcomeMigrated, nil
	}
	return g, OutcomeGenerated, nil
}

// windowClosed reports (closed, known). A nil evaluator is "not known", not
// "open" — osiris-api starts in a degraded mode when Neo4j is unreachable at
// boot, and that mode must not be a credential-minting mode.
func windowClosed(ctx context.Context, window Window) (bool, bool) {
	if window == nil {
		return false, false
	}
	closed, err := window.Closed(ctx)
	if err != nil {
		return false, false
	}
	return closed, true
}

func generate(path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir master key dir: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate master key: %w", err)
	}
	key := KeyPrefix + base64.RawURLEncoding.EncodeToString(buf)
	// 0600 is the intent. This repo's WSL2 dev host cannot always honor the bit
	// precisely (DrvFs) — the same caveat driver/docker's writeConfigFiles
	// records — so it is best-effort here too. The enforced boundary is the
	// constant-time comparison plus H10, never the mode bits alone.
	if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
		return "", fmt.Errorf("persist master key: %w", err)
	}
	return key, nil
}

// Valid reports whether candidate is this process's master key.
//
// It requires the prefix as well as the value: a stored key without the prefix
// is a legacy file that Boot should have replaced, and accepting it here would
// re-open the trial-comparison path §19.3 removed. Constant-time comparison,
// because this is a long-lived bearer secret and string equality leaks its
// length and its first differing byte.
//
// Valid says NOTHING about whether the key still authorizes anything — that is
// H10's question and internal/authn asks it separately, on every request
// (§4.3: "매 요청마다 위 두 조건을 다시 평가한다").
func (g *Guard) Valid(candidate string) bool {
	if g == nil || g.key == "" || candidate == "" {
		return false
	}
	if !HasKeyPrefix(candidate) || !HasKeyPrefix(g.key) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(g.key)) == 1
}

// Present reports whether this process holds a master key at all. Used only for
// log lines and for the console's "is the master-key tab worth showing"
// question — never as an authorization input.
func (g *Guard) Present() bool { return g != nil && g.key != "" }

// Path is the file the operator reads with bin/show-master-key.sh.
func (g *Guard) Path() string {
	if g == nil {
		return ""
	}
	return g.path
}

// Purge deletes the master key file — §4.5's 자료 차원 of H23.
//
// IDEMPOTENT, AND "ALREADY GONE" IS SUCCESS. The caller invokes this whenever it
// observes that the H10 window has closed, which happens on many requests, and a
// missing file is the desired end state rather than an error to report.
//
// IT DOES NOT CLEAR THE IN-MEMORY KEY, AND THAT IS DELIBERATE. §4.3: "폐쇄는
// 상태에서 파생하며 소각 플래그가 아니다 ... 매 요청마다 위 두 조건을 다시
// 평가한다." Zeroing the field would install exactly the burn flag the design
// refuses, and it would break the one resurrection path the design says must
// work: if the graph is restored to revision 0 (§4.5, §9.5), the predicate
// re-opens the window on its own, and the operator gets a freshly generated key
// on the next boot. Nothing about the closure is remembered anywhere.
func (g *Guard) Purge() error {
	if g == nil || g.path == "" {
		return nil
	}
	return removeIfPresent(g.path)
}

func removeIfPresent(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
