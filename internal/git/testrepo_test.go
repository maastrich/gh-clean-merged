package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fixture builds a throwaway repository and chdirs into it for the duration of
// the test, since every helper in this package runs git in the working directory.
//
// Layout:
//
//	main            base branch, mirrored to refs/remotes/origin/main
//	merged          merged into main with a merge commit
//	squashed        its change is on main under a different commit
//	unmerged        carries a change main does not have
//	empty           points at the same commit as main
//	noop            has commits of its own but the same tree as main
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

	git := func(args ...string) {
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

	git("init", "-b", "main")
	write("README", "base\n")
	git("add", ".")
	git("commit", "-m", "base")

	// A branch merged the classic way.
	git("checkout", "-b", "merged")
	write("merged.txt", "merged\n")
	git("add", ".")
	git("commit", "-m", "merged work")
	git("checkout", "main")
	git("merge", "--no-ff", "-m", "merge merged", "merged")

	// A branch whose change lands on main as an unrelated commit, which is what
	// a squash merge produces.
	git("checkout", "-b", "squashed", "main")
	write("squashed.txt", "squashed\n")
	git("add", ".")
	git("commit", "-m", "squashed work part 1")
	write("squashed.txt", "squashed\nmore\n")
	git("add", ".")
	git("commit", "-m", "squashed work part 2")
	git("checkout", "main")
	write("squashed.txt", "squashed\nmore\n")
	git("add", ".")
	git("commit", "-m", "squashed work (#1)")

	git("checkout", "-b", "unmerged", "main")
	write("unmerged.txt", "unmerged\n")
	git("add", ".")
	git("commit", "-m", "unmerged work")

	git("checkout", "-b", "empty", "main")

	// A branch that adds a file and takes it back: it has commits of its own
	// but its tree matches the merge base, so it carries no change at all.
	git("checkout", "-b", "noop", "main")
	write("noop.txt", "noop\n")
	git("add", ".")
	git("commit", "-m", "add noop")
	os.Remove(filepath.Join(dir, "noop.txt"))
	git("add", "-A")
	git("commit", "-m", "remove noop")

	git("checkout", "main")
	git("update-ref", "refs/remotes/origin/main", "main")
	git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	return dir
}
