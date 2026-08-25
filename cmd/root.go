package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/maastrich/gh-clean-merged/internal/clean"
	"github.com/maastrich/gh-clean-merged/internal/git"
	"github.com/maastrich/gh-clean-merged/internal/github"
	"github.com/spf13/cobra"
)

var (
	dryRun      bool
	assumeYes   bool
	base        string
	remote      string
	noFetch     bool
	protected   []string
	verbose     bool
	includeLive bool
)

var rootCmd = &cobra.Command{
	Use:   "gh clean-merged",
	Short: "Delete local branches whose work already landed on the base branch",
	Long: `Delete local git branches whose changes are already on the base branch.

Unlike ` + "`git branch --merged`" + `, squash-merged and rebase-merged branches are
detected too: the pull request state is read from GitHub, and branches without a
pull request are compared against the base branch by patch content.

Nothing is deleted without confirmation unless --yes is passed.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          run,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	flags := rootCmd.Flags()
	flags.BoolVarP(&dryRun, "dry-run", "n", false, "List what would be deleted and exit")
	flags.BoolVarP(&assumeYes, "yes", "y", false, "Delete without asking for confirmation")
	flags.StringVarP(&base, "base", "b", "", "Base branch to compare against (default: the repository default branch)")
	flags.StringVar(&remote, "remote", "origin", "Remote holding the base branch")
	flags.BoolVar(&noFetch, "no-fetch", false, "Skip `git fetch --prune`, comparing against the refs already on disk")
	flags.StringSliceVar(&protected, "protected", nil, "Branch names or globs that must never be deleted, e.g. `prod/*` (repeatable, or comma separated)")
	flags.BoolVar(&includeLive, "include-live", false, "Also consider branches whose remote branch still exists, instead of keeping them")
	flags.BoolVarP(&verbose, "verbose", "v", false, "Also list the branches that are kept, with the reason")
}

func run(cmd *cobra.Command, args []string) error {
	if !git.IsRepo() {
		return fmt.Errorf("not inside a git repository")
	}

	if !noFetch {
		fmt.Fprintf(os.Stderr, "Fetching %s...\n", remote)
		if err := git.Fetch(remote); err != nil {
			return fmt.Errorf("failed to fetch %s: %w", remote, err)
		}
	}

	// The GitHub lookup is what makes squash-merged pull requests visible, but
	// it needs a remote repository. Without it we still run on git signals only.
	repo, repoErr := github.CurrentRepo()
	if repoErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not resolve the GitHub repository (%v)\n", repoErr)
		fmt.Fprintln(os.Stderr, "Warning: falling back to local comparison only")
	}

	if base == "" {
		base = resolveBase(repo)
	}
	if base == "" {
		return fmt.Errorf("could not determine the base branch, pass --base")
	}
	if !git.HasRemoteRef(remote, base) {
		return fmt.Errorf("%s/%s does not exist, pass --base or --remote", remote, base)
	}

	branches, err := git.LocalBranches()
	if err != nil {
		return err
	}

	var prs map[string]github.PR
	if repoErr == nil {
		names := make([]string, 0, len(branches))
		for _, branch := range branches {
			if branch.Name != base {
				names = append(names, branch.Name)
			}
		}
		if prs, err = github.PullRequestsByBranch(repo, names); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not look up pull requests (%v)\n", err)
			fmt.Fprintln(os.Stderr, "Warning: falling back to local comparison only")
		}
	}

	patterns := make([]string, 0, len(protected))
	for _, pattern := range protected {
		if pattern = strings.TrimSpace(pattern); pattern != "" {
			patterns = append(patterns, pattern)
		}
	}

	verdicts := clean.Analyze(branches, git.CurrentBranch(), clean.Options{
		Remote:      remote,
		Base:        base,
		PRs:         prs,
		Protected:   patterns,
		IncludeLive: includeLive,
		RemoteExists: func(branch string) bool {
			return git.HasRemoteRef(remote, branch)
		},
	})

	if verbose {
		printKept(verdicts)
	}

	targets := clean.Deletable(verdicts)
	if len(targets) == 0 {
		fmt.Printf("No local branch is fully merged into %s/%s.\n", remote, base)
		return nil
	}

	fmt.Printf("Branches merged into %s/%s:\n", remote, base)
	for _, v := range targets {
		fmt.Printf("  %s  (%s)\n", v.Branch.Name, v.Reason)
	}

	if dryRun {
		fmt.Printf("\nDry run: %s left untouched.\n", plural(len(targets)))
		return nil
	}

	if !assumeYes {
		ok, err := confirm(len(targets))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Aborted, nothing deleted.")
			return nil
		}
	}

	return deleteBranches(targets)
}

// resolveBase prefers the remote's HEAD, which reflects what the local clone
// actually tracks, and falls back to what GitHub reports.
func resolveBase(repo github.Repo) string {
	if branch, err := git.DefaultBranch(remote); err == nil && branch != "" {
		return branch
	}
	return repo.DefaultBranch
}

func printKept(verdicts []clean.Verdict) {
	var kept []clean.Verdict
	for _, v := range verdicts {
		if !v.Delete {
			kept = append(kept, v)
		}
	}
	if len(kept) == 0 {
		return
	}
	fmt.Println("Kept:")
	for _, v := range kept {
		fmt.Printf("  %s  (%s)\n", v.Branch.Name, v.Reason)
	}
	fmt.Println()
}

func confirm(count int) (bool, error) {
	stat, err := os.Stdin.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice == 0 {
		return false, fmt.Errorf("cannot ask for confirmation without a terminal, pass --yes or --dry-run")
	}

	fmt.Printf("\nDelete %s? [y/N] ", plural(count))
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func deleteBranches(targets []clean.Verdict) error {
	var failed int
	for _, v := range targets {
		// A squash-merged branch is invisible to `git branch -d`, so those need
		// -D. The verdict records which signal justified it.
		if err := git.Delete(v.Branch.Name, v.Force); err != nil {
			fmt.Fprintf(os.Stderr, "  failed to delete %s: %v\n", v.Branch.Name, err)
			failed++
			continue
		}
		fmt.Printf("  deleted %s\n", v.Branch.Name)
	}

	if failed > 0 {
		return fmt.Errorf("%d of %d branches could not be deleted", failed, len(targets))
	}
	fmt.Printf("\nDeleted %s.\n", plural(len(targets)))
	return nil
}

func plural(count int) string {
	if count == 1 {
		return "1 branch"
	}
	return fmt.Sprintf("%d branches", count)
}
