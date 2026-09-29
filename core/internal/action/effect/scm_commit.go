// effect.scm.commit — the effect registry's FIRST registered entry.
// docs/design/80-action-layer.md §4.6 (git.commit's effects column) and P2
// stage ③, this task's item 3.
//
// *** os/exec IS NEW TO THE core MODULE ***. Before this file, `grep -rn
// "os/exec" core/` returns zero hits anywhere in osiris-core (api and agent
// already use os/exec — api/internal/omniroute/ops.go, and three files
// under agent/internal/supervisor — so this is not a first for the
// repository as a whole, only for the core module specifically). Executing
// `git` as a subprocess is the only way this codebase can produce a real
// commit: git's object model, index, and ref updates are not reimplemented
// in Go anywhere in this repository (no go-git or similar dependency exists
// in core/go.mod, and adding one is a bigger surface than this narrow
// stage's brief calls for). The alternative — shelling out via a package
// that itself wraps os/exec — would still add the same dependency one
// layer removed, with no safety benefit: every argument below is passed as
// a discrete exec.Command argv element, never through a shell, so there is
// no command-injection surface regardless of which layer issues the
// syscall.
//
// REGISTRATION REVIEW CHECKLIST (effect.go's package doc comment) —
// answered in order:
//
//  1. PARAMETER DOMAIN, restricted IN CODE (not just by the caller's
//     :ActionType.parametersSchemaJson, which only bounds string
//     length/pattern — see validateRepoPath/validateBranch/validateMessage
//     below):
//     - repoPath: must be an absolute, existing directory that already
//     contains a .git entry (verified via `git rev-parse
//     --is-inside-work-tree`, not just os.Stat, so a bare repo or a
//     non-git directory that merely happens to have a file named
//     ".git" is also rejected) — AND must not be, or be an ancestor
//     of, this OSiris workspace checkout itself (forbiddenRepoRoots
//     below). The workspace is itself a git repository (published
//     framework source), so the git-worktree check alone would accept
//     it — the denylist is what refuses it; set OSIRIS_FORBIDDEN_REPO_ROOTS
//     wherever osiris-core can see the checkout.
//     - branch: must match a closed filename-safe grammar (no "..", no
//     leading "-", not literally "HEAD") AND must already be the
//     repository's CURRENTLY CHECKED-OUT branch — this effect never
//     runs `git checkout`/`git switch`. That removes an entire class
//     of param-driven side effects (an attacker-authored :ActionType
//     row cannot make this effect silently switch branches out from
//     under a workspace).
//     - message: bounded length (redundant with, but not reliant on,
//     the schema's maxLength) and rejects embedded NUL bytes.
//     None of the three is trusted as "the caller already validated it" —
//     each is re-checked here even though :ActionType.parametersSchemaJson
//     (actiontype_gitcommit.go) already constrains shape, because (per
//     effect.go's own checklist item 1) a predicate/schema can be skipped
//     by a mis-authored or future :ActionType row; this effect's own checks
//     cannot be.
//  2. CREDENTIALS: this effect never reads a caller-supplied git identity.
//     The commit author/committer is hardcoded to osiris-core's own
//     identity (gitCommitterName/gitCommitterEmail below), passed via `git
//     -c user.name=... -c user.email=...` — scoped to this one invocation,
//     never touching the process-wide or repository-wide git config. This
//     is the same boundary orchestrator/internal/catalog/catalog.go
//     documents for git: agents hold read-only git credentials; only
//     osiris-core performs the write, with its own identity.
//  3. IDEMPOTENCY: NOT idempotent in general (two calls with the same
//     staged changes and message create two distinct commits — git commits
//     are content- AND parent-addressed, not request-ID-addressed). The
//     ONE case this function treats specially: if `git commit` reports
//     nothing-to-commit (the working tree already matches what would have
//     been committed — e.g., a retry racing a prior partial success that
//     actually landed), this function returns the CURRENT HEAD sha as the
//     Artifact instead of propagating an error, because in that specific
//     case double-applying is structurally impossible (there is nothing
//     left to apply) and failing the retry would misreport a no-op as a
//     failure. This is NOT a general idempotency guarantee for retries that
//     change staged content between attempts — 80 §5.4.1 AC22's deferred
//     queue (which would be the actual caller of a retry) is P4/out of this
//     stage's scope, so this function does only the narrow, safe thing a
//     future caller can rely on without this stage inventing the queue
//     itself. [갭] — see this task's final report.
//  4. RENAME: effect.scm.commit, once registered, must not be renamed —
//     see effect.go's package doc comment, item 4. Nothing in this file
//     changes that policy; noted here as acknowledgment, not a new rule.
package effect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	gitCommitterName  = "osiris-core"
	gitCommitterEmail = "core@osiris.local"
)

// forbiddenRepoRoots is the explicit, code-level denylist mentioned in this
// file's REGISTRATION REVIEW CHECKLIST item 1: the OSiris workspace checkout
// itself must never be the target of this effect. Its location is
// deployment-specific, so it comes from OSIRIS_FORBIDDEN_REPO_ROOTS
// (os.PathListSeparator-separated, e.g. "/srv/osiris:/home/me/osiris" to
// cover a symlinked alias too) instead of being hardcoded. This list is
// defense in depth next to the other checks in validateRepoPath.
var forbiddenRepoRoots = splitRoots(os.Getenv("OSIRIS_FORBIDDEN_REPO_ROOTS"))

func splitRoots(v string) []string {
	var out []string
	for _, p := range filepath.SplitList(v) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, filepath.Clean(p))
		}
	}
	return out
}

var branchNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$`)

// scmCommit implements effect.scm.commit — git.commit's registered effect
// (80 §4.6). Reads exactly three params: repoPath, branch, message (the
// same key names :ActionType.parametersSchemaJson declares for git.commit,
// actiontype_gitcommit.go — see action/dispatch's doc comment for why this
// stage does not add a per-action parameter remapping layer). Any other
// keys present in params (e.g. "max"/"size", which the registered
// validation predicates read, not this effect) are ignored, not rejected —
// this effect only ever reads its own three keys.
func scmCommit(ctx context.Context, params map[string]any) (Artifact, error) {
	repoPath, err := stringParam(params, "repoPath")
	if err != nil {
		return Artifact{}, fmt.Errorf("effect.scm.commit: %w", err)
	}
	branch, err := stringParam(params, "branch")
	if err != nil {
		return Artifact{}, fmt.Errorf("effect.scm.commit: %w", err)
	}
	message, err := stringParam(params, "message")
	if err != nil {
		return Artifact{}, fmt.Errorf("effect.scm.commit: %w", err)
	}

	if err := validateRepoPath(repoPath); err != nil {
		return Artifact{}, fmt.Errorf("effect.scm.commit: repoPath rejected: %w", err)
	}
	if err := validateBranch(branch); err != nil {
		return Artifact{}, fmt.Errorf("effect.scm.commit: branch rejected: %w", err)
	}
	if err := validateMessage(message); err != nil {
		return Artifact{}, fmt.Errorf("effect.scm.commit: message rejected: %w", err)
	}

	if _, err := exec.LookPath("git"); err != nil {
		return Artifact{}, fmt.Errorf("effect.scm.commit: git binary not found in PATH: %w", err)
	}

	if err := requireGitWorktree(ctx, repoPath); err != nil {
		return Artifact{}, fmt.Errorf("effect.scm.commit: %w", err)
	}
	if err := requireCurrentBranch(ctx, repoPath, branch); err != nil {
		return Artifact{}, fmt.Errorf("effect.scm.commit: %w", err)
	}

	if _, _, err := runGit(ctx, repoPath, "add", "-A"); err != nil {
		return Artifact{}, fmt.Errorf("effect.scm.commit: git add -A failed: %w", err)
	}

	stdout, stderr, commitErr := runGit(ctx, repoPath,
		"-c", "user.name="+gitCommitterName,
		"-c", "user.email="+gitCommitterEmail,
		"-c", "commit.gpgsign=false",
		"commit", "-m", message,
	)
	if commitErr != nil {
		combined := stdout + stderr
		if strings.Contains(combined, "nothing to commit") {
			// See this file's REGISTRATION REVIEW CHECKLIST item 3: a
			// no-op retry, not a failure. Report the existing HEAD.
			sha, shaErr := currentHeadSHA(ctx, repoPath)
			if shaErr != nil {
				return Artifact{}, fmt.Errorf("effect.scm.commit: nothing to commit, and could not resolve existing HEAD: %w", shaErr)
			}
			return Artifact{Kind: "commit", URI: sha}, nil
		}
		return Artifact{}, fmt.Errorf("effect.scm.commit: git commit failed: %w (stderr: %s)", commitErr, strings.TrimSpace(stderr))
	}

	sha, err := currentHeadSHA(ctx, repoPath)
	if err != nil {
		return Artifact{}, fmt.Errorf("effect.scm.commit: commit succeeded but could not resolve HEAD: %w", err)
	}
	return Artifact{Kind: "commit", URI: sha}, nil
}

func validateRepoPath(repoPath string) error {
	if !filepath.IsAbs(repoPath) {
		return errors.New("repoPath must be an absolute path")
	}
	clean := filepath.Clean(repoPath)
	for _, forbidden := range forbiddenRepoRoots {
		if clean == forbidden || strings.HasPrefix(clean, forbidden+string(filepath.Separator)) {
			return fmt.Errorf("repoPath %q is inside a forbidden root (%q) — this effect refuses to touch the OSiris workspace checkout itself", repoPath, forbidden)
		}
	}
	info, err := os.Stat(clean)
	if err != nil {
		return fmt.Errorf("repoPath does not exist or is not accessible: %w", err)
	}
	if !info.IsDir() {
		return errors.New("repoPath must be a directory")
	}
	return nil
}

func validateBranch(branch string) error {
	if !branchNamePattern.MatchString(branch) {
		return fmt.Errorf("branch %q does not match the allowed grammar", branch)
	}
	if strings.Contains(branch, "..") {
		return errors.New("branch must not contain '..'")
	}
	if strings.EqualFold(branch, "HEAD") {
		return errors.New(`branch must not be "HEAD"`)
	}
	return nil
}

func validateMessage(message string) error {
	if len(message) == 0 {
		return errors.New("message must not be empty")
	}
	if len(message) > 2000 {
		return errors.New("message exceeds 2000 characters (matches :ActionType.parametersSchemaJson's maxLength)")
	}
	if strings.ContainsRune(message, 0) {
		return errors.New("message must not contain a NUL byte")
	}
	return nil
}

func requireGitWorktree(ctx context.Context, repoPath string) error {
	stdout, stderr, err := runGit(ctx, repoPath, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return fmt.Errorf("repoPath is not a git worktree: %w (stderr: %s)", err, strings.TrimSpace(stderr))
	}
	if strings.TrimSpace(stdout) != "true" {
		return fmt.Errorf("repoPath is not a git worktree (git rev-parse --is-inside-work-tree returned %q)", strings.TrimSpace(stdout))
	}
	return nil
}

func requireCurrentBranch(ctx context.Context, repoPath, wantBranch string) error {
	stdout, stderr, err := runGit(ctx, repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("could not resolve current branch: %w (stderr: %s)", err, strings.TrimSpace(stderr))
	}
	got := strings.TrimSpace(stdout)
	if got != wantBranch {
		// Deliberately fail closed rather than `git checkout wantBranch` —
		// see this file's REGISTRATION REVIEW CHECKLIST item 1: this effect
		// never switches branches on the caller's behalf.
		return fmt.Errorf("repoPath's currently checked-out branch is %q, not the requested %q — this effect refuses to switch branches", got, wantBranch)
	}
	return nil
}

func currentHeadSHA(ctx context.Context, repoPath string) (string, error) {
	stdout, stderr, err := runGit(ctx, repoPath, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("%w (stderr: %s)", err, strings.TrimSpace(stderr))
	}
	return strings.TrimSpace(stdout), nil
}

// runGit executes `git <args...>` with cwd=repoPath via -C, as discrete
// argv elements — never through a shell (exec.Command never interprets
// shell metacharacters in its argument list), so no argument here, however
// attacker-influenced its origin :ActionType row might be, can escape into
// a second command.
func runGit(ctx context.Context, repoPath string, args ...string) (stdout, stderr string, err error) {
	full := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

func stringParam(params map[string]any, key string) (string, error) {
	v, ok := params[key]
	if !ok {
		return "", fmt.Errorf("missing required parameter %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("parameter %q must be a string, got %T", key, v)
	}
	return s, nil
}
