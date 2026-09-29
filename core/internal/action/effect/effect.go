// Package effect is osiris-core's side-effect registry —
// docs/design/80-action-layer.md §5.3.1's registry table:
//
//	effect.*  →  func(ctx, params) (Artifact, error) — has side effects,
//	              executed only with osiris-core's OWN credentials.
//
// Like internal/action/predicate, this is a fixed, version-pinned,
// runtime-immutable Go map (same discipline as
// orchestrator/internal/catalog/catalog.go's Table) — the SET of effects a
// running osiris-core binary can invoke is closed at build time, and no
// :ActionType graph data can add, remove, or redefine an entry (AD2(c)).
//
// [2026-08-12, P2 stage ③] NO LONGER AN EMPTY SKELETON: this build
// registers the registry's first entry, effect.scm.commit (git.commit's
// row, §4.6) — see scm_commit.go for the implementation and its own
// REGISTRATION REVIEW CHECKLIST answers. Stage ① (this package's original
// skeleton, quoted below for history) deliberately left the registry empty
// until stage ②'s :ActionType schema and graph-write whitelist existed, so
// that real, validated parameters — not arbitrary ones — are what an
// effect function ever receives; that precondition is now satisfied
// (core/internal/graphdb/actiontype.go + actiontype_gitcommit.go).
//
// Original stage-① note: "이 패키지는 이 빌드에서 빈 스켈레톤이다 (P2 stage
// ①, §11's '구현 워커' ordering: '① 이펙트/술어 레지스트리 골격 + AC3의
// fail-closed, ... ③ git.commit 한 행을 데이터로 옮겨 ... 실증')."
//
// REGISTRATION REVIEW CHECKLIST (§5.3.3's first "정직한 한계" — G-A7 in this
// task's brief). §5.3.2 proves the *vocabulary/composition/representation*
// ceilings hold: a fully-compromised :ActionType can only choose WHICH
// registered effect runs and in what ORDER, never invent a new
// computation. That proof says nothing about an individual effect's own
// PARAMETER domain — §5.3.3 names this gap explicitly ("파라미터를 통한
// 이펙트 오용" — e.g. an :ActionType whose effect.mail.send row carries an
// attacker-chosen recipient default is representable and would not be
// caught by the ceilings alone). Closing that gap is each effect's own
// responsibility, not this registry's. Before adding a row to `registry`
// below:
//
//  1. Document, in the new function's own doc comment, EVERY param it
//     reads and the validation each one gets before use — not "trusts the
//     caller resolved it correctly". §5.3.3's own cited fix for
//     effect.mail.send is a worked example: constrain the recipient to
//     values that already exist as some :Person.email, via a predicate
//     (predicate.mail.recipient_in_directory) AND/OR a check inside the
//     effect itself. A predicate on the :ActionType side is a gate on
//     whether the call proceeds; it is not a substitute for the effect
//     also refusing to act on a param outside its own understood domain —
//     the predicate can be skipped by a mis-authored or stage-②-era
//     :ActionType row, the effect's own check cannot.
//  2. The effect MUST authenticate as osiris-core's own credential, never
//     reuse or forward a caller-supplied one (§5.3.1's registry table:
//     "osiris-core의 자격증명으로만 실행"). This is the same boundary
//     orchestrator/internal/catalog/catalog.go's MCPServers/MCPTools
//     column documents for git: "the agent's git credential is read-only
//     and osiris-core performs approved writes with its own."
//  3. State whether the effect is idempotent, or — if not — why a replay
//     from §5.4.1's AC22 deferred queue is still safe (each retry there
//     re-runs AD6's full re-evaluation first, but the effect call itself
//     must not double-apply on a retry that races a prior partial
//     success).
//  4. Renames are breaking changes to data already living in some
//     org's :ActionType.effects array (§5.3.3's second, UNRESOLVED
//     "정직한 한계" — "레지스트리 이름의 안정성" is explicitly [미검증], no
//     deprecation-alias policy exists yet). Until one does: don't rename
//     an id once shipped; add a new one and mark the old function's doc
//     comment deprecated instead.
package effect

import (
	"context"
	"sort"
)

// Artifact is the minimal record an effect returns on success — the Go-side
// shape of a 20-ontology-schema.md §3.4 :Artifact this package hands the
// caller to persist. This package does not write it to the graph itself
// (that would violate writeops.go's single-writer chokepoint, A5/G7/D17);
// persisting the returned Artifact is the execution engine's job.
type Artifact struct {
	Kind string // e.g. "commit", "document" — one of 20 §3.4's :Artifact.kind values
	URI  string // opaque locator for what was produced (e.g. a commit SHA reference)
}

// Func is the effect registry's value signature, verbatim from §5.3.1's
// table.
type Func func(ctx context.Context, params map[string]any) (Artifact, error)

// registry is the fixed, version-pinned map — same "REGISTRATION REVIEW
// CHECKLIST" discipline as this file's doc comment, and the same
// runtime-immutability discipline as internal/action/predicate's registry.
//
// [2026-08-12, P2 stage ③] effect.scm.commit (scm_commit.go) is this
// registry's first entry — see that file's doc comment for its
// REGISTRATION REVIEW CHECKLIST answers.
var registry = map[string]Func{
	"effect.scm.commit": scmCommit,
}

// Lookup returns the effect function registered under name, or ok=false if
// name is not registered.
//
// UNLIKE orchestrator/internal/catalog's grant lookups (AllowedTools/
// DeniedTools/MCPServers/EgressAllowlist all silently `continue` past an
// unrecognized grant code — a stale grant safely contributes nothing), a
// caller here MUST treat ok=false as a hard error, never as "no effect, so
// do nothing". That silent-skip vs. fail-closed contrast is §5.3.1's own
// argument for why the action layer cannot reuse the grant-catalog's
// idiom: a skipped grant makes an agent slightly less capable, but a
// skipped effect on an :ActionType row that claims to have one would make
// the row's declared behavior a lie.
func Lookup(name string) (Func, bool) {
	fn, ok := registry[name]
	return fn, ok
}

// Names returns the sorted list of registered effect ids — the reference
// set graphdb.ReconcileActionTypes checks :ActionType.effects against
// (80 §9 AC2/AC3).
func Names() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
