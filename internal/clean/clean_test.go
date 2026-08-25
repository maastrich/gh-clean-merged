package clean

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maastrich/gh-clean-merged/internal/git"
	"github.com/maastrich/gh-clean-merged/internal/github"
)

func branches(names ...string) []git.Branch {
	result := make([]git.Branch, 0, len(names))
	for _, name := range names {
		result = append(result, git.Branch{Name: name})
	}
	return result
}

func only(t *testing.T, verdicts []Verdict) Verdict {
	t.Helper()
	if len(verdicts) != 1 {
		t.Fatalf("expected 1 verdict, got %d", len(verdicts))
	}
	return verdicts[0]
}

// These branches are answered before any git command runs, so they need no repository.
// These branches are answered before any git command runs, so they need no repository.
func TestAnalyzeKeepsProtectedBranches(t *testing.T) {
	cases := []struct {
		name    string
		branch  git.Branch
		current string
		opts    Options
		reason  string
	}{
		{
			name:   "base branch",
			branch: git.Branch{Name: "main"},
			opts:   Options{Remote: "origin", Base: "main"},
			reason: "base branch",
		},
		{
			name:    "current branch",
			branch:  git.Branch{Name: "feature"},
			current: "feature",
			opts:    Options{Remote: "origin", Base: "main"},
			reason:  "currently checked out",
		},
		{
			name:   "other worktree",
			branch: git.Branch{Name: "feature", Worktree: "/tmp/wt"},
			opts:   Options{Remote: "origin", Base: "main"},
			reason: "worktree /tmp/wt",
		},
		{
			name:   "matched by a protected glob",
			branch: git.Branch{Name: "prod/lcm"},
			opts:   Options{Remote: "origin", Base: "main", Protected: []string{"prod/*"}},
			reason: "protected",
		},
		{
			name:   "explicitly protected",
			branch: git.Branch{Name: "release"},
			opts:   Options{Remote: "origin", Base: "main", Protected: []string{"release"}},
			reason: "protected",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := only(t, Analyze([]git.Branch{tc.branch}, tc.current, tc.opts))
			if v.Delete || v.Orphan {
				t.Fatalf("branch %q should be kept, got %+v", tc.branch.Name, v)
			}
			if !strings.Contains(v.Reason, tc.reason) {
				t.Errorf("reason = %q, want it to mention %q", v.Reason, tc.reason)
			}
		})
	}
}

// A branch still on the remote is shared work, whatever its content says.
// Deploy branches such as prod/* live here: they carry no pull request and sit
// behind the base branch, so any content comparison would call them merged.
func TestAnalyzeKeepsBranchesStillOnTheRemote(t *testing.T) {
	cases := []struct {
		name   string
		branch git.Branch
		opts   Options
	}{
		{
			name:   "tracked branch whose upstream is alive",
			branch: git.Branch{Name: "prod/lcm", Upstream: "origin/prod/lcm"},
			opts:   Options{Remote: "origin", Base: "main"},
		},
		{
			name:   "untracked branch with a remote branch of the same name",
			branch: git.Branch{Name: "prod/lcm"},
			opts: Options{Remote: "origin", Base: "main",
				RemoteExists: func(string) bool { return true }},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A merged pull request does not change this: the branch is still there.
			tc.opts.PRs = map[string]github.PR{"prod/lcm": {Number: 9, Merged: true, State: "MERGED"}}
			v := only(t, Analyze([]git.Branch{tc.branch}, "main", tc.opts))
			if v.Delete {
				t.Fatalf("branch should be kept, got reason %q", v.Reason)
			}
			if !strings.Contains(v.Reason, "still on origin") {
				t.Errorf("reason = %q, want it to mention the remote branch", v.Reason)
			}
		})
	}
}

// Once the remote branch is gone, the pull request decides.
func TestAnalyzePullRequestDecidesGoneBranches(t *testing.T) {
	fixture(t)

	cases := []struct {
		name      string
		branch    string
		pr        github.PR
		opts      Options
		wantDel   bool
		wantForce bool
		reason    string
	}{
		{
			name:    "merge commit is visible to git",
			branch:  "merged",
			pr:      github.PR{Number: 1, Merged: true, State: "MERGED"},
			wantDel: true,
			reason:  "PR #1 merged",
		},
		{
			name:      "squash merge needs the forced delete",
			branch:    "squashed",
			pr:        github.PR{Number: 12, Merged: true, State: "MERGED"},
			wantDel:   true,
			wantForce: true,
			reason:    "PR #12 merged (squash or rebase)",
		},
		{
			name:      "closed without merging",
			branch:    "unmerged",
			pr:        github.PR{Number: 3, State: "CLOSED"},
			wantDel:   true,
			wantForce: true,
			reason:    "PR #3 closed without merging",
		},
		{
			name:   "closed without merging, kept on request",
			branch: "unmerged",
			pr:     github.PR{Number: 3, State: "CLOSED"},
			opts:   Options{KeepClosed: true},
			reason: "PR #3 closed without merging",
		},
		{
			name:   "open pull request",
			branch: "unmerged",
			pr:     github.PR{Number: 4, State: "OPEN"},
			reason: "open PR #4",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.opts
			opts.Remote, opts.Base = "origin", "main"
			opts.PRs = map[string]github.PR{tc.branch: tc.pr}
			// The remote branch is gone: no upstream, and no branch of that name.
			opts.RemoteExists = func(string) bool { return false }

			v := only(t, Analyze(branches(tc.branch), "main", opts))
			if v.Delete != tc.wantDel {
				t.Fatalf("Delete = %v, want %v (reason %q)", v.Delete, tc.wantDel, v.Reason)
			}
			if v.Force != tc.wantForce {
				t.Errorf("Force = %v, want %v", v.Force, tc.wantForce)
			}
			if !strings.Contains(v.Reason, tc.reason) {
				t.Errorf("reason = %q, want it to mention %q", v.Reason, tc.reason)
			}
		})
	}
}

// A branch with no remote branch and no pull request is nobody else's business:
// it is reported so the user can decide, never deleted.
func TestAnalyzeReportsOrphans(t *testing.T) {
	fixture(t)

	cases := []struct {
		branch string
		reason string
	}{
		{"merged", "no pull request, already merged into origin/main"},
		{"squashed", "no pull request, changes already in origin/main"},
		{"noop", "no pull request, no changes of its own versus origin/main"},
		{"unmerged", "no pull request, changes not in origin/main"},
	}

	for _, tc := range cases {
		t.Run(tc.branch, func(t *testing.T) {
			v := only(t, Analyze(branches(tc.branch), "main", Options{
				Remote:       "origin",
				Base:         "main",
				RemoteExists: func(string) bool { return false },
			}))
			if v.Delete {
				t.Fatalf("an orphan branch must never be deleted, got reason %q", v.Reason)
			}
			if !v.Orphan {
				t.Errorf("branch %q should be reported as an orphan", tc.branch)
			}
			if v.Reason != tc.reason {
				t.Errorf("reason = %q, want %q", v.Reason, tc.reason)
			}
		})
	}
}

// A branch whose upstream is marked gone counts as having no remote branch.
func TestAnalyzeTreatsGoneUpstreamAsNoRemote(t *testing.T) {
	fixture(t)

	v := only(t, Analyze([]git.Branch{{Name: "squashed", Upstream: "origin/squashed", Gone: true}}, "main", Options{
		Remote: "origin",
		Base:   "main",
		PRs:    map[string]github.PR{"squashed": {Number: 8, Merged: true, State: "MERGED"}},
	}))
	if !v.Delete || !v.Force {
		t.Errorf("a gone branch with a merged pull request should be force deleted, got %+v", v)
	}
}

func TestFilters(t *testing.T) {
	verdicts := []Verdict{
		{Branch: git.Branch{Name: "a"}, Delete: true},
		{Branch: git.Branch{Name: "b"}},
		{Branch: git.Branch{Name: "c"}, Orphan: true},
	}
	if got := Deletable(verdicts); len(got) != 1 || got[0].Branch.Name != "a" {
		t.Errorf("Deletable = %v, want branch a", got)
	}
	if got := Orphans(verdicts); len(got) != 1 || got[0].Branch.Name != "c" {
		t.Errorf("Orphans = %v, want branch c", got)
	}
	if got := Kept(verdicts); len(got) != 1 || got[0].Branch.Name != "b" {
		t.Errorf("Kept = %v, want branch b", got)
	}
}

// fixture builds the same throwaway repository the git package tests use and
// chdirs into it, since the analysis shells out to git in the working directory.
func fixture(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(previous) })

	// Hide the developer's git configuration, so the tests exercise the same
	// identity-less environment CI runs in.
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(dir, "nonexistent-gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(dir, "nonexistent-gitconfig"))

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_CONFIG_GLOBAL="+filepath.Join(dir, "nonexistent-gitconfig"),
			"GIT_CONFIG_SYSTEM="+filepath.Join(dir, "nonexistent-gitconfig"),
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-b", "main")
	write("README", "base\n")
	run("add", ".")
	run("commit", "-m", "base")

	run("checkout", "-b", "merged")
	write("merged.txt", "merged\n")
	run("add", ".")
	run("commit", "-m", "merged work")
	run("checkout", "main")
	run("merge", "--no-ff", "-m", "merge merged", "merged")

	run("checkout", "-b", "squashed", "main")
	write("squashed.txt", "squashed\n")
	run("add", ".")
	run("commit", "-m", "squashed work part 1")
	write("squashed.txt", "squashed\nmore\n")
	run("add", ".")
	run("commit", "-m", "squashed work part 2")
	run("checkout", "main")
	write("squashed.txt", "squashed\nmore\n")
	run("add", ".")
	run("commit", "-m", "squashed work (#1)")

	run("checkout", "-b", "unmerged", "main")
	write("unmerged.txt", "unmerged\n")
	run("add", ".")
	run("commit", "-m", "unmerged work")

	run("checkout", "-b", "empty", "main")

	// A branch that adds a file and takes it back: it has commits of its own
	// but its tree matches the merge base, so it carries no change at all.
	run("checkout", "-b", "noop", "main")
	write("noop.txt", "noop\n")
	run("add", ".")
	run("commit", "-m", "add noop")
	os.Remove(filepath.Join(dir, "noop.txt"))
	run("add", "-A")
	run("commit", "-m", "remove noop")

	run("checkout", "main")
	run("update-ref", "refs/remotes/origin/main", "main")

	return dir
}
