// Package git wraps the git plumbing needed to decide whether a local branch
// is safe to delete.
package git

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Branch is a local branch with the bits of state we need to judge it.
type Branch struct {
	Name     string
	Upstream string // e.g. "origin/feature-x", empty when the branch tracks nothing
	Gone     bool   // upstream is configured but no longer exists on the remote
	Worktree string // path of another worktree holding this branch, empty otherwise
}

func run(args ...string) (string, error) {
	return runWithEnv(nil, args...)
}

func runWithEnv(env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// IsRepo reports whether the working directory is inside a git work tree.
func IsRepo() bool {
	out, err := run("rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// Fetch updates the remote refs and prunes the ones that disappeared upstream.
// Every other check reads refs, so stale data here means wrong answers later.
func Fetch(remote string) error {
	_, err := run("fetch", "--prune", remote)
	return err
}

// CurrentBranch returns the checked out branch, or "" when HEAD is detached.
func CurrentBranch() string {
	out, err := run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || out == "HEAD" {
		return ""
	}
	return out
}

// DefaultBranch resolves the remote's HEAD, e.g. "main" for origin/HEAD -> origin/main.
func DefaultBranch(remote string) (string, error) {
	out, err := run("symbolic-ref", "--quiet", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(out, "refs/remotes/"+remote+"/"), nil
}

// HasRemoteRef reports whether <remote>/<branch> exists locally after a fetch.
func HasRemoteRef(remote, branch string) bool {
	_, err := run("rev-parse", "--verify", "--quiet", "refs/remotes/"+remote+"/"+branch)
	return err == nil
}

// LocalBranches lists every local branch with its upstream tracking state.
func LocalBranches() ([]Branch, error) {
	const format = "%(refname:short)%09%(upstream:short)%09%(upstream:track)"
	out, err := run("for-each-ref", "--format="+format, "refs/heads")
	if err != nil {
		return nil, err
	}

	worktrees, err := worktreeBranches()
	if err != nil {
		return nil, err
	}

	var branches []Branch
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		for len(fields) < 3 {
			fields = append(fields, "")
		}
		branches = append(branches, Branch{
			Name:     fields[0],
			Upstream: fields[1],
			Gone:     strings.Contains(fields[2], "gone"),
			Worktree: worktrees[fields[0]],
		})
	}
	return branches, nil
}

// worktreeBranches maps branch name -> worktree path for branches checked out
// somewhere other than the main working directory.
func worktreeBranches() (map[string]string, error) {
	out, err := run("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	var path string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			name := strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
			result[name] = path
		}
	}
	// The main worktree is the one we run in; its branch is handled separately
	// as "current branch", so drop it here to avoid a misleading message.
	if main, err := run("rev-parse", "--show-toplevel"); err == nil {
		for name, p := range result {
			if p == main {
				delete(result, name)
			}
		}
	}
	return result, nil
}

// Root is the top level directory of the repository, where a local
// configuration file would sit.
func Root() (string, error) {
	return run("rev-parse", "--show-toplevel")
}

// IsAncestor reports whether every commit of branch is already contained in ref.
// This is the plain merge-commit / fast-forward case.
func IsAncestor(branch, ref string) bool {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", branch, ref)
	return cmd.Run() == nil
}

// SquashState is the verdict of the patch-id comparison against the base branch.
type SquashState int

const (
	// SquashUnknown means the branch still carries changes not present in base.
	SquashUnknown SquashState = iota
	// SquashApplied means the branch's diff is already in base under a different
	// commit — the signature of a squash or rebase merge.
	SquashApplied
	// SquashEmpty means the branch introduces no changes at all versus base.
	SquashEmpty
)

// SquashMerged detects squash- and rebase-merged branches.
//
// git cannot see such a merge: the squashed commit on base is a brand new
// commit with no ancestry link to the branch. So we build a synthetic commit
// carrying the branch's whole tree parented on the merge base — that commit's
// diff is exactly the branch's cumulative change — and ask `git cherry` whether
// base already contains an equivalent patch.
func SquashMerged(branch, base string) (SquashState, error) {
	mergeBase, err := run("merge-base", base, branch)
	if err != nil {
		return SquashUnknown, err
	}

	// Both trees in one call: this runs once per branch, and a repository with
	// a few hundred branches feels every extra process.
	trees, err := run("rev-parse", branch+"^{tree}", mergeBase+"^{tree}")
	if err != nil {
		return SquashUnknown, err
	}
	tree, baseTree, ok := cut(trees, "\n")
	if !ok {
		return SquashUnknown, fmt.Errorf("unexpected rev-parse output for %s: %q", branch, trees)
	}
	if tree == baseTree {
		// Empty diff: commit-tree would yield a patch-less commit that git cherry
		// cannot classify, so answer here instead of guessing from its output.
		return SquashEmpty, nil
	}

	// commit-tree needs an author, and this probe commit is dangling and never
	// published, so supply an identity rather than fail where git is unconfigured.
	probeIdentity := []string{
		"GIT_AUTHOR_NAME=gh-clean-merged",
		"GIT_AUTHOR_EMAIL=gh-clean-merged@localhost",
		"GIT_COMMITTER_NAME=gh-clean-merged",
		"GIT_COMMITTER_EMAIL=gh-clean-merged@localhost",
	}
	dangling, err := runWithEnv(probeIdentity, "commit-tree", tree, "-p", mergeBase, "-m", "gh-clean-merged probe")
	if err != nil {
		return SquashUnknown, err
	}

	out, err := run("cherry", base, dangling)
	if err != nil {
		return SquashUnknown, err
	}
	// "- <sha>" means base already has an equivalent patch, "+ <sha>" means it does not.
	if strings.HasPrefix(out, "-") {
		return SquashApplied, nil
	}
	return SquashUnknown, nil
}

// cut splits s around the first occurrence of sep, like strings.Cut, which is
// not available in the Go version this module targets.
func cut(s, sep string) (before, after string, found bool) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

// Delete removes a local branch. force uses -D, required for squash-merged
// branches because git -d refuses what it cannot prove is merged.
func Delete(branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := run("branch", flag, branch)
	return err
}
