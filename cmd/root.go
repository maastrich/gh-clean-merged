package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/maastrich/gh-clean-merged/internal/clean"
	"github.com/maastrich/gh-clean-merged/internal/git"
	"github.com/maastrich/gh-clean-merged/internal/github"
	"github.com/maastrich/gh-clean-merged/internal/ui"
	"github.com/spf13/cobra"
)

var (
	dryRun     bool
	assumeYes  bool
	base       string
	remote     string
	noFetch    bool
	protected  []string
	verbose    bool
	keepClosed bool
	colorMode  string
)

var rootCmd = &cobra.Command{
	Use:   "gh clean-merged",
	Short: "Delete the local branches whose pull request is closed or merged",
	Long: `Delete the local git branches left behind by finished pull requests.

A branch that still exists on the remote is left alone: it is shared work, and
long lived branches such as prod/*, beta/* or preprod/* live there. What remains
is local leftovers, and their pull request says what happened to them — merged
or closed means gone, open means keep. Squash and rebase merges are handled,
since the answer comes from GitHub rather than from git history.

Branches with neither a remote branch nor a pull request are never deleted, only
reported.

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
	flags.StringVarP(&base, "base", "b", "", "Base branch orphan branches are compared against (default: the repository default branch)")
	flags.StringVar(&remote, "remote", "origin", "Remote whose branches count as shared work")
	flags.BoolVar(&noFetch, "no-fetch", false, "Skip `git fetch --prune`, using the refs already on disk")
	flags.StringSliceVar(&protected, "protected", nil, "Branch names or globs that must never be deleted, e.g. `prod/*` (repeatable, or comma separated)")
	flags.BoolVar(&keepClosed, "keep-closed", false, "Keep branches whose pull request was closed without merging")
	flags.BoolVarP(&verbose, "verbose", "v", false, "Also list the branches that are kept, with the reason")
	flags.StringVar(&colorMode, "color", ui.Auto, "Colour output: auto, always or never")
}

func run(cmd *cobra.Command, args []string) error {
	out := ui.New(os.Stdout, colorMode)

	if !git.IsRepo() {
		return fmt.Errorf("not inside a git repository")
	}

	if !noFetch {
		fmt.Fprintf(os.Stderr, "Fetching %s...\n", remote)
		if err := git.Fetch(remote); err != nil {
			return fmt.Errorf("failed to fetch %s: %w", remote, err)
		}
	}

	// Pull request state is what tells a leftover branch from work in progress.
	// Without it, branches are reported instead of deleted.
	repo, repoErr := github.CurrentRepo()
	if repoErr != nil {
		warn(out, fmt.Sprintf("could not resolve the GitHub repository (%v)", repoErr))
		warn(out, "branches will be reported, not deleted")
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
		if prs, err = github.PullRequestsByBranch(repo, branchNames(branches)); err != nil {
			warn(out, fmt.Sprintf("could not look up pull requests (%v)", err))
			warn(out, "branches will be reported, not deleted")
		}
	}

	verdicts := clean.Analyze(branches, git.CurrentBranch(), clean.Options{
		Remote:     remote,
		Base:       base,
		PRs:        prs,
		Protected:  patterns(),
		KeepClosed: keepClosed,
		RemoteExists: func(branch string) bool {
			return git.HasRemoteRef(remote, branch)
		},
	})

	out.Printf("%s %s  %s\n\n",
		out.Bold("Base branch"),
		out.Cyan(remote+"/"+base),
		out.Dim(fmt.Sprintf("%d local branches", len(branches))),
	)

	if verbose {
		out.Section("Kept", rows(out, clean.Kept(verdicts), "-", out.Dim))
	}
	// Branches with neither a remote counterpart nor a pull request are never
	// touched, but they are exactly what a cleanup run should surface.
	orphans := clean.Orphans(verdicts)
	out.Section(
		fmt.Sprintf("Not on %s and no pull request, left alone", remote),
		rows(out, orphans, "?", out.Yellow),
	)

	targets := clean.Deletable(verdicts)
	if len(targets) == 0 {
		out.Printf("%s\n", out.Green("Nothing to delete."))
		return nil
	}

	out.Section("Pull request closed or merged, safe to delete", rows(out, targets, "x", out.Red))

	if dryRun {
		out.Printf("%s\n", out.Dim(fmt.Sprintf("Dry run: %s left untouched.", plural(len(targets)))))
		return nil
	}

	if !assumeYes {
		ok, err := confirm(out, len(targets))
		if err != nil {
			return err
		}
		if !ok {
			out.Printf("%s\n", out.Dim("Aborted, nothing deleted."))
			return nil
		}
	}

	return deleteBranches(out, targets)
}

func rows(out *ui.Printer, verdicts []clean.Verdict, marker string, paint func(string) string) []ui.Row {
	result := make([]ui.Row, 0, len(verdicts))
	for _, v := range verdicts {
		result = append(result, ui.Row{
			Marker: marker,
			Name:   v.Branch.Name,
			Reason: v.Reason,
			Paint:  paint,
		})
	}
	return result
}

func branchNames(branches []git.Branch) []string {
	names := make([]string, 0, len(branches))
	for _, branch := range branches {
		if branch.Name != base {
			names = append(names, branch.Name)
		}
	}
	return names
}

func patterns() []string {
	result := make([]string, 0, len(protected))
	for _, pattern := range protected {
		if pattern = strings.TrimSpace(pattern); pattern != "" {
			result = append(result, pattern)
		}
	}
	return result
}

func warn(out *ui.Printer, message string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", out.Yellow("Warning:"), message)
}

// resolveBase prefers the remote's HEAD, which reflects what the local clone
// actually tracks, and falls back to what GitHub reports.
func resolveBase(repo github.Repo) string {
	if branch, err := git.DefaultBranch(remote); err == nil && branch != "" {
		return branch
	}
	return repo.DefaultBranch
}

func confirm(out *ui.Printer, count int) (bool, error) {
	stat, err := os.Stdin.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice == 0 {
		return false, fmt.Errorf("cannot ask for confirmation without a terminal, pass --yes or --dry-run")
	}

	out.Printf("%s ", out.Bold(fmt.Sprintf("Delete %s? [y/N]", plural(count))))
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func deleteBranches(out *ui.Printer, targets []clean.Verdict) error {
	var failed int
	for _, v := range targets {
		// A squash-merged branch is invisible to `git branch -d`, so those need
		// -D. The verdict records which signal justified it.
		if err := git.Delete(v.Branch.Name, v.Force); err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s: %v\n", out.Red("failed"), v.Branch.Name, err)
			failed++
			continue
		}
		out.Printf("  %s %s\n", out.Green("deleted"), v.Branch.Name)
	}

	if failed > 0 {
		return fmt.Errorf("%d of %d branches could not be deleted", failed, len(targets))
	}
	out.Printf("\n%s\n", out.Green(fmt.Sprintf("Deleted %s.", plural(len(targets)))))
	return nil
}

func plural(count int) string {
	if count == 1 {
		return "1 branch"
	}
	return fmt.Sprintf("%d branches", count)
}
