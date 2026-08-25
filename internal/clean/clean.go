// Package clean decides, branch by branch, whether a local branch is safe to
// delete and why.
//
// The rule is deliberately simple: a branch that still exists on the remote is
// shared work and is left alone. What remains is local leftovers, and their
// pull request says what happened to them. A branch with neither a remote
// counterpart nor a pull request is nobody's business but the user's, so it is
// reported instead of deleted.
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
	// Orphan marks a branch with no remote counterpart and no pull request.
	// Nothing can vouch for it, so it is reported rather than deleted.
	Orphan bool
	// Reason explains the verdict in one line, for the user to audit.
	Reason string
}

// Options configures the analysis.
type Options struct {
	// Remote holding the base branch, usually "origin".
	Remote string
	// Base branch orphan branches are described against, e.g. "main".
	Base string
	// PRs maps head branch name to its pull request; may be nil when the
	// GitHub lookup was skipped or failed.
	PRs map[string]github.PR
	// Protected holds branch names or glob patterns that must never be deleted,
	// on top of the current and base branches.
	Protected []string
	// RemoteExists reports whether a branch of that name still exists on the
	// remote, for branches that carry no tracking configuration.
	RemoteExists func(branch string) bool
	// KeepClosed keeps branches whose pull request was closed without merging,
	// instead of deleting them along with the merged ones.
	KeepClosed bool
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

// Analyze classifies every local branch.
//
// Describing an orphan branch costs a handful of git processes and walks the
// base branch, so branches are analysed concurrently. Results stay in input
// order.
func Analyze(branches []git.Branch, current string, opts Options) []Verdict {
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
				verdicts[index] = analyzeBranch(branches[index], current, opts)
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

func analyzeBranch(branch git.Branch, current string, opts Options) Verdict {
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

	// An open pull request is the most precise thing that can be said about a
	// branch, so it is what the user is told, rather than the remote branch it
	// obviously still has.
	if hasPR && pr.State == "OPEN" {
		return keep(fmt.Sprintf("open PR #%d", pr.Number))
	}

	// A branch that still exists on the remote is shared work: someone may be
	// deploying from it, or merging the base branch into it. Long lived
	// branches such as prod/*, beta/* and preprod/* live here too.
	if liveRemote(branch, opts) {
		return keep(fmt.Sprintf("still on %s", opts.Remote))
	}

	if !hasPR {
		// No remote branch and no pull request: nothing outside this machine
		// knows about it, so the user gets told rather than obeyed.
		return Verdict{Branch: branch, Orphan: true, Reason: describe(branch.Name, opts)}
	}

	switch pr.State {
	case "MERGED":
		// git sees the merge only when it left ancestry behind; a squash or a
		// rebase did not, and needs the forced delete.
		baseRef := opts.Remote + "/" + opts.Base
		if git.IsAncestor(branch.Name, baseRef) {
			return Verdict{Branch: branch, Delete: true, Reason: fmt.Sprintf("PR #%d merged", pr.Number)}
		}
		return Verdict{
			Branch: branch,
			Delete: true,
			Force:  true,
			Reason: fmt.Sprintf("PR #%d merged (squash or rebase)", pr.Number),
		}

	default: // CLOSED
		if opts.KeepClosed {
			return keep(fmt.Sprintf("PR #%d closed without merging", pr.Number))
		}
		// The work was abandoned and the remote branch is already gone, so the
		// commits only survive here. The forced delete is the point.
		return Verdict{
			Branch: branch,
			Delete: true,
			Force:  true,
			Reason: fmt.Sprintf("PR #%d closed without merging", pr.Number),
		}
	}
}

// describe says where an orphan branch stands versus the base branch, so the
// user can tell leftovers worth deleting from work worth keeping.
func describe(branch string, opts Options) string {
	baseRef := opts.Remote + "/" + opts.Base

	if git.IsAncestor(branch, baseRef) {
		return fmt.Sprintf("no pull request, already merged into %s", baseRef)
	}
	switch state, err := git.SquashMerged(branch, baseRef); {
	case err != nil:
		return "no pull request"
	case state == git.SquashApplied:
		return fmt.Sprintf("no pull request, changes already in %s", baseRef)
	case state == git.SquashEmpty:
		return fmt.Sprintf("no pull request, no changes of its own versus %s", baseRef)
	}
	return fmt.Sprintf("no pull request, changes not in %s", baseRef)
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
	return filter(verdicts, func(v Verdict) bool { return v.Delete })
}

// Kept filters verdicts down to the branches that stay, orphans included.
func Kept(verdicts []Verdict) []Verdict {
	return filter(verdicts, func(v Verdict) bool { return !v.Delete && !v.Orphan })
}

// Orphans filters verdicts down to the branches nothing can vouch for.
func Orphans(verdicts []Verdict) []Verdict {
	return filter(verdicts, func(v Verdict) bool { return v.Orphan })
}

func filter(verdicts []Verdict, keep func(Verdict) bool) []Verdict {
	var result []Verdict
	for _, v := range verdicts {
		if keep(v) {
			result = append(result, v)
		}
	}
	return result
}
