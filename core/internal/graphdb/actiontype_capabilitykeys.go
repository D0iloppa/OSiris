// AC8/CA8's static capability-key registry — docs/design/80-action-layer.md
// §4.7 CA8 and §9 AC21, consuming docs/design/81-capability-stacks.md
// §2.3 C-1/C-2 and CS4.
//
// WHY THIS LIVES INSIDE graphdb, NOT A SEPARATE core/internal/action/*
// PACKAGE (unlike predicate/effect): the predicate/effect registries hold
// actual Go function VALUES (behavior) that graphdb.ReconcileActionTypes
// deliberately does not import — see that file's doc comment on the
// one-directional dependency arrow (main → {graphdb, action/predicate,
// action/effect}, never graphdb → action/*). A capability key registry is
// not behavior — it is a closed list of strings plus one regex, no
// different in kind from the enum lists (gateClass, taintClass, ...)
// already defined next to ActionTypeInput's validation. Colocating it here
// keeps CA8's "정의 시점 하드 오류(쓰기 거부)" enforcement in the one place
// that already IS the graph-write chokepoint (A5/G7/D17), with no import
// indirection needed to reach it.
//
// SOURCE OF TRUTH: this list is copied VERBATIM from 81 §2.3 C-1's key
// grammar example block and cross-checked against §4.1 (mail) and §5.1
// (SCM/CI) — no key below was invented for this task. 81 §2.3 C-2 says the
// authoritative registry lives in "차트/이미지에 실린 정적 레지스트리" (a
// chart/image artifact outside this document's — and this task's — scope);
// the ten keys below are simply every key 81 itself names anywhere in the
// text as of the 2026-08-12 revision. If 81's chart-based registry
// (whenever it is built) ever diverges from this list, THAT is the
// authoritative source and this list must be updated to match it — not the
// reverse.
package graphdb

import (
	"regexp"
	"sort"
)

// capabilityKeyPattern is 81 §2.3 C-1's key grammar, verbatim:
// `^stk\.[a-z0-9-]+(\.[a-z0-9-]+){1,2}$`. Go's regexp is RE2 (linear time,
// no catastrophic backtracking possible) — the same property §4.4 relies on
// for parametersSchemaJson's `pattern` keyword.
var capabilityKeyPattern = regexp.MustCompile(`^stk\.[a-z0-9-]+(\.[a-z0-9-]+){1,2}$`)

// staticCapabilityKeyRegistry is 81 §2.3 C-2's "closed" list, transcribed
// (not invented) from 81 §2.3 C-1's grammar example block, §4.1's mail-stack
// key table, and §5.1's SCM/CI key table. Unexported, package-level,
// literal — same runtime-immutability discipline as
// internal/action/predicate and internal/action/effect's `registry` maps
// (CS4/CA8 read "정의 시점 하드 오류" the same way AC3 reads "fail-closed":
// nothing may add to this set at runtime).
var staticCapabilityKeyRegistry = map[string]struct{}{
	// 81 §4.1 — mail stack
	"stk.mail.send":              {},
	"stk.mail.receive":           {},
	"stk.mail.address.per-party": {},
	// 81 §5.1 — SCM/CI stack
	"stk.scm.repo.read":         {},
	"stk.scm.repo.write":        {},
	"stk.scm.webhook":           {},
	"stk.scm.credential.rotate": {}, // 81 §2.3 C-1's "[2026-08-12 추가]" note; closes 80 G-A1
	"stk.ci.run":                {},
	// 81 §2.3 C-1's grammar block also names these two (ingress), not tied
	// to a specific §4/§5 worked example in this revision of 81, but named
	// nowhere else either — included because C-1 lists them as valid
	// examples of the registry, and CS10 already implies stk.scm.webhook
	// depends on one of them (§2.5: "stk.scm.webhook ← stk.ingress.public").
	"stk.ingress.public": {},
	"stk.ingress.tls":    {},
}

// StaticCapabilityKeys returns the sorted list of registered capability
// keys — exposed for bin/lint-action-layer.sh and any future caller that
// needs to enumerate rather than just validate (e.g. a console catalog
// picker, out of this task's scope).
func StaticCapabilityKeys() []string {
	out := make([]string, 0, len(staticCapabilityKeyRegistry))
	for k := range staticCapabilityKeyRegistry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// validCapabilityKey enforces CA8/AC21 in one place: the key must match 81
// C-1's grammar AND exist in the closed registry (both — CS1's namespace
// exclusivity is a corollary: no `cap.*` string can ever match
// capabilityKeyPattern, so it is rejected by the regex step alone, but a
// mismatched-but-'stk.'-prefixed string not in the chart's real list would
// pass a regex-only check, hence the second, registry lookup).
func validCapabilityKey(key string) bool {
	if !capabilityKeyPattern.MatchString(key) {
		return false
	}
	_, ok := staticCapabilityKeyRegistry[key]
	return ok
}
