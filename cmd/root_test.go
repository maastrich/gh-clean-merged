package cmd

import (
	"reflect"
	"testing"

	"github.com/maastrich/gh-clean-merged/internal/clean"
	"github.com/maastrich/gh-clean-merged/internal/git"
)

// A branch still on the remote is kept whatever its pull request says, so its
// pull request is only fetched when --verbose will print the reason.
func TestBranchNamesSkipsLiveRemoteBranchesUnlessVerbose(t *testing.T) {
	branches := []git.Branch{
		{Name: "master"},
		{Name: "prod/api", Upstream: "origin/prod/api"},
		{Name: "feat/done", Upstream: "origin/feat/done", Gone: true},
		{Name: "feat/local"},
	}
	opts := clean.Options{Remote: "origin", Base: "master", RemoteExists: func(string) bool { return false }}

	previousBase, previousVerbose := base, verbose
	t.Cleanup(func() { base, verbose = previousBase, previousVerbose })
	base = "master"

	verbose = false
	got := branchNames(branches, opts)
	want := []string{"feat/done", "feat/local"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("branchNames = %v, want %v", got, want)
	}

	verbose = true
	got = branchNames(branches, opts)
	want = []string{"prod/api", "feat/done", "feat/local"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("verbose branchNames = %v, want %v", got, want)
	}
}
