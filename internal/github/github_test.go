package github

import (
	"strings"
	"testing"
)

func TestBuildQuery(t *testing.T) {
	query, aliases := buildQuery(Repo{Owner: "maastrich", Name: "front"}, []string{"feat/a", `weird"name`})

	if aliases["b0"] != "feat/a" || aliases["b1"] != `weird"name` {
		t.Fatalf("aliases = %v, want b0 and b1 mapped to the branch names", aliases)
	}
	if !strings.Contains(query, `headRefName: "feat/a"`) {
		t.Errorf("query is missing the branch: %s", query)
	}
	// A branch name is user controlled text landing inside a GraphQL document.
	if !strings.Contains(query, `headRefName: "weird\"name"`) {
		t.Errorf("query does not escape quotes in branch names: %s", query)
	}
	if !strings.Contains(query, `repository(owner: "maastrich", name: "front")`) {
		t.Errorf("query is missing the repository: %s", query)
	}
}

func TestParseQueryResult(t *testing.T) {
	// A fork can open a pull request whose head branch is named like one of our
	// local branches while being unrelated work, so it must not match.
	raw := []byte(`{"data": {"repository": {
		"b0": {"nodes": [{"number": 10, "state": "MERGED", "headRepositoryOwner": {"login": "maastrich"}}]},
		"b1": {"nodes": [{"number": 11, "state": "MERGED", "headRepositoryOwner": {"login": "someone-else"}}]},
		"b2": {"nodes": [{"number": 12, "state": "OPEN", "headRepositoryOwner": {"login": "MAASTRICH"}}]}
	}}}`)
	aliases := map[string]string{"b0": "feature-a", "b1": "feature-b", "b2": "feature-c"}

	prs, err := parseQueryResult(raw, aliases, "maastrich")
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
func TestParseQueryResultPrefersMerged(t *testing.T) {
	raw := []byte(`{"data": {"repository": {
		"b0": {"nodes": [
			{"number": 20, "state": "CLOSED", "headRepositoryOwner": {"login": "maastrich"}},
			{"number": 19, "state": "MERGED", "headRepositoryOwner": {"login": "maastrich"}}
		]}
	}}}`)

	prs, err := parseQueryResult(raw, map[string]string{"b0": "feature"}, "maastrich")
	if err != nil {
		t.Fatal(err)
	}
	if pr := prs["feature"]; !pr.Merged || pr.Number != 19 {
		t.Errorf("feature = %+v, want merged PR #19", pr)
	}
}

func TestParseQueryResultKeepsMostRecentWhenNoneMerged(t *testing.T) {
	raw := []byte(`{"data": {"repository": {
		"b0": {"nodes": [
			{"number": 20, "state": "OPEN", "headRepositoryOwner": {"login": "maastrich"}},
			{"number": 19, "state": "CLOSED", "headRepositoryOwner": {"login": "maastrich"}}
		]}
	}}}`)

	prs, err := parseQueryResult(raw, map[string]string{"b0": "feature"}, "maastrich")
	if err != nil {
		t.Fatal(err)
	}
	if pr := prs["feature"]; pr.Number != 20 || pr.State != "OPEN" {
		t.Errorf("feature = %+v, want open PR #20", pr)
	}
}

func TestChunk(t *testing.T) {
	got := chunk([]string{"a", "b", "c", "d", "e"}, 2)
	if len(got) != 3 || len(got[0]) != 2 || len(got[2]) != 1 {
		t.Errorf("chunk = %v, want batches of 2, 2 and 1", got)
	}
	if chunk(nil, 2) != nil {
		t.Error("chunk of nothing should be empty")
	}
}
