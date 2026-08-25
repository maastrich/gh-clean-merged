// Package github reads pull request state through the gh CLI, which is already
// authenticated when we run as a gh extension.
package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Repo identifies the repository the current directory points at.
type Repo struct {
	Owner         string
	Name          string
	DefaultBranch string
}

// PR is the pull request state relevant to a local branch.
type PR struct {
	Number int
	Merged bool
	State  string // OPEN, CLOSED or MERGED
}

func run(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("gh %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}

// CurrentRepo resolves the repository for the working directory.
func CurrentRepo() (Repo, error) {
	out, err := run("repo", "view", "--json", "owner,name,defaultBranchRef")
	if err != nil {
		return Repo{}, err
	}

	var payload struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name             string `json:"name"`
		DefaultBranchRef struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return Repo{}, fmt.Errorf("failed to parse repo info: %w", err)
	}
	return Repo{
		Owner:         payload.Owner.Login,
		Name:          payload.Name,
		DefaultBranch: payload.DefaultBranchRef.Name,
	}, nil
}

// PullRequestsByBranch maps head branch name -> pull request, in one API round
// trip rather than one per branch.
//
// Pull requests opened from a fork can carry the same head branch name as a
// local branch of ours while being an entirely different line of work, so only
// PRs whose head repository is this repository are kept.
func PullRequestsByBranch(repo Repo, limit int) (map[string]PR, error) {
	out, err := run(
		"pr", "list",
		"--state", "all",
		"--limit", fmt.Sprint(limit),
		"--json", "number,headRefName,headRepositoryOwner,state",
	)
	if err != nil {
		return nil, err
	}
	return parsePullRequests(out, repo.Owner)
}

func parsePullRequests(raw []byte, owner string) (map[string]PR, error) {
	var payload []struct {
		Number              int    `json:"number"`
		HeadRefName         string `json:"headRefName"`
		HeadRepositoryOwner struct {
			Login string `json:"login"`
		} `json:"headRepositoryOwner"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse pull request list: %w", err)
	}

	byBranch := map[string]PR{}
	for _, item := range payload {
		if !strings.EqualFold(item.HeadRepositoryOwner.Login, owner) {
			continue
		}
		pr := PR{
			Number: item.Number,
			Merged: item.State == "MERGED",
			State:  item.State,
		}
		// Several pull requests can share a head branch over time. A merged one
		// is the only decisive answer, so it always wins; otherwise keep the
		// most recent, which is the first entry gh returns.
		if existing, ok := byBranch[item.HeadRefName]; ok && (existing.Merged || !pr.Merged) {
			continue
		}
		byBranch[item.HeadRefName] = pr
	}
	return byBranch, nil
}
