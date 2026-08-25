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
		{
			name:   "open pull request",
			branch: git.Branch{Name: "feature"},
			opts: Options{Remote: "origin", Base: "main", PRs: map[string]github.PR{
				"feature": {Number: 7, State: "OPEN"},
			}},
			reason: "open PR #7",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := only(t, Analyze([]git.Branch{tc.branch}, tc.current, tc.opts))
			if v.Delete {
				t.Fatalf("branch %q should be kept, reason %q", tc.branch.Name, v.Reason)
			}
			if !strings.Contains(v.Reason, tc.reason) {
				t.Errorf("reason = %q, want it to mention %q", v.Reason, tc.reason)
			}
		})
	}
}

func TestAnalyzeAgainstRepository(t *testing.T) {
	fixture(t)

	cases := []struct {
		name      string
		branch    string
		prs       map[string]github.PR
		wantDel   bool
		wantForce bool
		reason    string
	}{
		{
			name:    "merge commit is visible to git",
			branch:  "merged",
			wantDel: true,
			reason:  "merged into origin/main",
		},
		{
			name:      "squash merge reported by GitHub",
			branch:    "squashed",
			prs:       map[string]github.PR{"squashed": {Number: 12, Merged: true, State: "MERGED"}},
			wantDel:   true,
			wantForce: true,
			reason:    "PR #12 merged (squash or rebase)",
		},
		{
			name:      "squash merge detected without a pull request",
			branch:    "squashed",
			wantDel:   true,
			wantForce: true,
			reason:    "changes already in origin/main",
		},
		{
			name:    "branch sitting on the base branch",
			branch:  "empty",
			wantDel: true,
			reason:  "merged into origin/main",
		},
		{
			name:      "branch whose commits cancel out",
			branch:    "noop",
			wantDel:   true,
			wantForce: true,
			reason:    "no changes of its own",
		},
		{
			name:   "unmerged work is kept",
			branch: "unmerged",
			reason: "has unmerged changes",
		},
		{
			name:   "closed pull request is kept",
			branch: "unmerged",
			prs:    map[string]github.PR{"unmerged": {Number: 3, State: "CLOSED"}},
			reason: "PR #3 closed without merging",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := only(t, Analyze(branches(tc.branch), "main", Options{
				Remote: "origin",
				Base:   "main",
				PRs:    tc.prs,
			}))
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

// A branch whose upstream disappeared is only a candidate: the remote branch may
// have been deleted on a pull request that was never merged.
func TestAnalyzeKeepsGoneBranchWithUnmergedWork(t *testing.T) {
	fixture(t)

	v := only(t, Analyze([]git.Branch{{Name: "unmerged", Upstream: "origin/unmerged", Gone: true}}, "main", Options{
		Remote: "origin",
		Base:   "main",
	}))
	if v.Delete {
		t.Fatalf("gone branch with unmerged work should be kept, got reason %q", v.Reason)
	}
	if !strings.Contains(v.Reason, "upstream gone") {
		t.Errorf("reason = %q, want it to mention the gone upstream", v.Reason)
	}
}

// Deploy branches such as prod/* carry no pull request and sit behind the base
// branch, which makes them look merged. Their live remote branch is what keeps
// them, and a merged pull request is what overrides that.
func TestAnalyzeKeepsBranchesWithLiveRemote(t *testing.T) {
	fixture(t)

	live := git.Branch{Name: "empty", Upstream: "origin/empty"}
	v := only(t, Analyze([]git.Branch{live}, "main", Options{Remote: "origin", Base: "main"}))
	if v.Delete {
		t.Fatalf("a branch still on the remote should be kept, got reason %q", v.Reason)
	}
	if !strings.Contains(v.Reason, "still on origin") {
		t.Errorf("reason = %q, want it to mention the live remote branch", v.Reason)
	}

	// --include-live judges it on content alone.
	v = only(t, Analyze([]git.Branch{live}, "main", Options{Remote: "origin", Base: "main", IncludeLive: true}))
	if !v.Delete {
		t.Errorf("with IncludeLive the branch should be deleted, got reason %q", v.Reason)
	}

	// A merged pull request outranks the live remote branch.
	v = only(t, Analyze([]git.Branch{{Name: "squashed", Upstream: "origin/squashed"}}, "main", Options{
		Remote: "origin",
		Base:   "main",
		PRs:    map[string]github.PR{"squashed": {Number: 4, Merged: true, State: "MERGED"}},
	}))
	if !v.Delete || !v.Force {
		t.Errorf("a merged pull request should delete the branch, got %+v", v)
	}
}

// Without tracking configuration the remote branch is looked up by name.
func TestAnalyzeKeepsUntrackedBranchWithRemoteOfSameName(t *testing.T) {
	fixture(t)

	v := only(t, Analyze(branches("merged"), "main", Options{
		Remote:       "origin",
		Base:         "main",
		RemoteExists: func(string) bool { return true },
	}))
	if v.Delete {
		t.Fatalf("branch should be kept, got reason %q", v.Reason)
	}
	if !strings.Contains(v.Reason, "still on origin") {
		t.Errorf("reason = %q, want it to mention the live remote branch", v.Reason)
	}
}

func TestDeletable(t *testing.T) {
	verdicts := []Verdict{
		{Branch: git.Branch{Name: "a"}, Delete: true},
		{Branch: git.Branch{Name: "b"}},
		{Branch: git.Branch{Name: "c"}, Delete: true},
	}
	got := Deletable(verdicts)
	if len(got) != 2 || got[0].Branch.Name != "a" || got[1].Branch.Name != "c" {
		t.Errorf("Deletable returned %v, want branches a and c", got)
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
