// Package clean decides, branch by branch, whether a local branch is safe to
// delete and why.
package clean

import (
	"fmt"
	"path"
	"runtime"
	"sync"

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
	// Protected holds branch names or glob patterns that must never be deleted,
	// on top of the current and base branches.
	Protected []string
	// RemoteExists reports whether a branch of that name still exists on the
	// remote. Long lived branches such as deploy branches sit behind the base
	// branch, which makes them look merged, so a live remote counterpart keeps
	// them unless a merged pull request says otherwise.
	RemoteExists func(branch string) bool
	// IncludeLive drops that protection and judges branches with a live remote
	// counterpart on their content alone.
	IncludeLive bool
}

// protects reports whether the branch matches one of the protected patterns.
// Patterns are shell globs, so "prod/*" covers every deploy branch at once.
func (o Options) protects(branch string) bool {
	for _, pattern := range o.Protected {
		if pattern == branch {
			return true
		}
		if ok, err := path.Match(pattern, branch); err == nil && ok {
			return true
		}
	}
	return false
}

// Analyze classifies every local branch against the base branch.
//
// Judging a branch costs a handful of git processes, and the patch comparison
// walks the base branch, so branches are analysed concurrently. Results stay in
// input order.
func Analyze(branches []git.Branch, current string, opts Options) []Verdict {
	baseRef := opts.Remote + "/" + opts.Base
	verdicts := make([]Verdict, len(branches))

	workers := runtime.NumCPU()
	if workers > len(branches) {
		workers = len(branches)
	}
	if workers < 1 {
		workers = 1
	}

	indexes := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range indexes {
				verdicts[index] = analyzeBranch(branches[index], current, baseRef, opts)
			}
		}()
	}
	for index := range branches {
		indexes <- index
	}
	close(indexes)
	wg.Wait()

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
	case opts.protects(branch.Name):
		return keep("protected")
	}

	pr, hasPR := opts.PRs[branch.Name]

	// An open pull request outranks every other signal: even if the branch's
	// changes already landed in base some other way, deleting it locally while
	// review is in flight is not what the user wants.
	if hasPR && pr.State == "OPEN" {
		return keep(fmt.Sprintf("open PR #%d", pr.Number))
	}

	// A merged pull request is the one signal that survives everything else: the
	// branch did its job and GitHub says so.
	if hasPR && pr.Merged {
		if git.IsAncestor(branch.Name, baseRef) {
			return Verdict{Branch: branch, Delete: true, Reason: fmt.Sprintf("PR #%d merged", pr.Number)}
		}
		// Squash and rebase merges rewrite history, so the branch is no longer
		// an ancestor of base and git alone cannot see the merge.
		return Verdict{
			Branch: branch,
			Delete: true,
			Force:  true,
			Reason: fmt.Sprintf("PR #%d merged (squash or rebase)", pr.Number),
		}
	}

	// Long lived branches such as prod/*, beta/* or preprod/* never have a pull
	// request of their own: the base branch is merged into them, or they simply
	// track an older release commit. Both make them look merged. Their remote
	// counterpart still being there is what tells them apart from finished work.
	if !opts.IncludeLive && liveRemote(branch, opts) {
		return keep(fmt.Sprintf("still on %s", opts.Remote))
	}

	// Plain merge commit or fast-forward: git can prove containment on its own.
	if git.IsAncestor(branch.Name, baseRef) {
		return Verdict{Branch: branch, Delete: true, Reason: fmt.Sprintf("merged into %s", baseRef)}
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

// liveRemote reports whether the branch still has a counterpart on the remote.
func liveRemote(branch git.Branch, opts Options) bool {
	if branch.Upstream != "" {
		return !branch.Gone
	}
	// No tracking configuration: fall back to a branch of the same name, which
	// is what a plain `git push` would have created.
	return opts.RemoteExists != nil && opts.RemoteExists(branch.Name)
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
