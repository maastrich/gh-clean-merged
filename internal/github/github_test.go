package github

import "testing"

func TestParsePullRequests(t *testing.T) {
	// A fork can open a pull request whose head branch is named like one of our
	// local branches while being unrelated work, so it must not match.
	raw := []byte(`[
		{"number": 10, "headRefName": "feature-a", "headRepositoryOwner": {"login": "maastrich"}, "state": "MERGED"},
		{"number": 11, "headRefName": "feature-b", "headRepositoryOwner": {"login": "someone-else"}, "state": "MERGED"},
		{"number": 12, "headRefName": "feature-c", "headRepositoryOwner": {"login": "MAASTRICH"}, "state": "OPEN"}
	]`)

	prs, err := parsePullRequests(raw, "maastrich")
	if err != nil {
		t.Fatal(err)
	}

	if pr, ok := prs["feature-a"]; !ok || !pr.Merged || pr.Number != 10 {
		t.Errorf("feature-a = %+v, want merged PR #10", pr)
	}
	if _, ok := prs["feature-b"]; ok {
		t.Error("a fork pull request must not be matched to a local branch")
	}
	// The owner comparison is case insensitive, GitHub logins are.
	if pr, ok := prs["feature-c"]; !ok || pr.State != "OPEN" {
		t.Errorf("feature-c = %+v, want open PR", pr)
	}
}

// The newest pull request for a branch may be a closed retry of work that was
// merged earlier; the merged one is what decides whether the branch is stale.
func TestParsePullRequestsPrefersMerged(t *testing.T) {
	raw := []byte(`[
		{"number": 20, "headRefName": "feature", "headRepositoryOwner": {"login": "maastrich"}, "state": "CLOSED"},
		{"number": 19, "headRefName": "feature", "headRepositoryOwner": {"login": "maastrich"}, "state": "MERGED"}
	]`)

	prs, err := parsePullRequests(raw, "maastrich")
	if err != nil {
		t.Fatal(err)
	}
	if pr := prs["feature"]; !pr.Merged || pr.Number != 19 {
		t.Errorf("feature = %+v, want merged PR #19", pr)
	}
}

func TestParsePullRequestsKeepsMostRecentWhenNoneMerged(t *testing.T) {
	raw := []byte(`[
		{"number": 20, "headRefName": "feature", "headRepositoryOwner": {"login": "maastrich"}, "state": "OPEN"},
		{"number": 19, "headRefName": "feature", "headRepositoryOwner": {"login": "maastrich"}, "state": "CLOSED"}
	]`)

	prs, err := parsePullRequests(raw, "maastrich")
	if err != nil {
		t.Fatal(err)
	}
	if pr := prs["feature"]; pr.Number != 20 || pr.State != "OPEN" {
		t.Errorf("feature = %+v, want open PR #20", pr)
	}
}
