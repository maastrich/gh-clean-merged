// Package clean decides, branch by branch, whether a local branch is safe to
// delete and why.
package clean

import (
	"fmt"

	"github.com/maastrich/gh-clean-merged/internal/git"
	"github.com/maastrich/gh-clean-merged/internal/github"
)

// Verdict is the outcome of analysing one local branch.
type Verdict struct {
	Branch git.Branch
	// Delete reports whether the branch is safe to remove.
	Delete bool
	// Force reports whether removal needs `git branch -D`, which is the case
	// whenever git itself cannot see the merge (squash and rebase merges).
	Force bool
	// Reason explains the verdict in one line, for the user to audit.
	Reason string
}

// Options configures the analysis.
type Options struct {
	// Remote holding the base branch, usually "origin".
	Remote string
	// Base branch every candidate is compared against, e.g. "main".
	Base string
	// PRs maps head branch name to its pull request; may be nil when the
	// GitHub lookup was skipped or failed.
	PRs map[string]github.PR
	// Protected branches that must never be deleted, on top of the current and
	// base branches.
	Protected map[string]bool
}

// Analyze classifies every local branch against the base branch.
func Analyze(branches []git.Branch, current string, opts Options) []Verdict {
	baseRef := opts.Remote + "/" + opts.Base

	verdicts := make([]Verdict, 0, len(branches))
	for _, branch := range branches {
		verdicts = append(verdicts, analyzeBranch(branch, current, baseRef, opts))
	}
	return verdicts
}

func analyzeBranch(branch git.Branch, current, baseRef string, opts Options) Verdict {
	keep := func(reason string) Verdict {
		return Verdict{Branch: branch, Reason: reason}
	}

	switch {
	case branch.Name == opts.Base:
		return keep("base branch")
	case branch.Name == current:
		return keep("currently checked out")
	case branch.Worktree != "":
		return keep(fmt.Sprintf("checked out in worktree %s", branch.Worktree))
	case opts.Protected[branch.Name]:
		return keep("protected")
	}

	pr, hasPR := opts.PRs[branch.Name]

	// An open pull request outranks every other signal: even if the branch's
	// changes already landed in base some other way, deleting it locally while
	// review is in flight is not what the user wants.
	if hasPR && pr.State == "OPEN" {
		return keep(fmt.Sprintf("open PR #%d", pr.Number))
	}

	// Plain merge commit or fast-forward: git can prove containment on its own.
	if git.IsAncestor(branch.Name, baseRef) {
		reason := fmt.Sprintf("merged into %s", baseRef)
		if hasPR && pr.Merged {
			reason = fmt.Sprintf("PR #%d merged", pr.Number)
		}
		return Verdict{Branch: branch, Delete: true, Reason: reason}
	}

	// Squash and rebase merges rewrite history, so the branch is no longer an
	// ancestor of base and only GitHub knows the merge happened.
	if hasPR && pr.Merged {
		return Verdict{
			Branch: branch,
			Delete: true,
			Force:  true,
			Reason: fmt.Sprintf("PR #%d merged (squash or rebase)", pr.Number),
		}
	}

	// No pull request, or one we could not read: fall back to comparing the
	// branch's cumulative diff against the patches already in base.
	state, err := git.SquashMerged(branch.Name, baseRef)
	if err != nil {
		return keep(fmt.Sprintf("could not compare with %s: %v", baseRef, err))
	}
	switch state {
	case git.SquashApplied:
		return Verdict{
			Branch: branch,
			Delete: true,
			Force:  true,
			Reason: fmt.Sprintf("changes already in %s (squash or rebase)", baseRef),
		}
	case git.SquashEmpty:
		return Verdict{
			Branch: branch,
			Delete: true,
			Force:  true,
			Reason: fmt.Sprintf("no changes of its own versus %s", baseRef),
		}
	}

	if hasPR && pr.State == "CLOSED" {
		return keep(fmt.Sprintf("PR #%d closed without merging", pr.Number))
	}
	if branch.Gone {
		// The remote branch was deleted but nothing proves the work landed, so
		// this is exactly the case where an unconditional sweep loses commits.
		return keep("upstream gone but changes not in " + baseRef)
	}
	return keep("has unmerged changes")
}

// Deletable filters verdicts down to the branches that will be removed.
func Deletable(verdicts []Verdict) []Verdict {
	var result []Verdict
	for _, v := range verdicts {
		if v.Delete {
			result = append(result, v)
		}
	}
	return result
}
