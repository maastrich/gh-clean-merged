// Package github reads pull request state through the gh CLI, which is already
// authenticated when we run as a gh extension.
package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// batchSize is how many branches are looked up per GraphQL request. Asking for
// every branch at once would build a query large enough to be rejected, and one
// request per branch would be hundreds of round trips.
const batchSize = 50

// prsPerBranch caps how many pull requests are read per branch. Branch names get
// reused over time, but never so often that the merged one falls outside this.
const prsPerBranch = 10

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

// PullRequestsByBranch maps branch name -> pull request for the given branches.
//
// The lookup asks for those exact branches rather than listing the N most
// recent pull requests, so a branch merged long ago is still resolved. Branches
// are batched into a handful of GraphQL requests, run concurrently.
func PullRequestsByBranch(repo Repo, names []string) (map[string]PR, error) {
	batches := chunk(names, batchSize)

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		result  = make(map[string]PR, len(names))
		firstEr error
	)
	for _, batch := range batches {
		wg.Add(1)
		go func(batch []string) {
			defer wg.Done()

			found, err := pullRequestBatch(repo, batch)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstEr == nil {
					firstEr = err
				}
				return
			}
			for name, pr := range found {
				result[name] = pr
			}
		}(batch)
	}
	wg.Wait()

	if firstEr != nil {
		return nil, firstEr
	}
	return result, nil
}

func pullRequestBatch(repo Repo, names []string) (map[string]PR, error) {
	query, aliases := buildQuery(repo, names)
	out, err := run("api", "graphql", "-f", "query="+query)
	if err != nil {
		return nil, err
	}
	return parseQueryResult(out, aliases, repo.Owner)
}

// buildQuery asks for every branch of the batch in one request, one aliased
// field per branch, and returns the alias -> branch name mapping needed to read
// the answer back.
func buildQuery(repo Repo, names []string) (string, map[string]string) {
	aliases := make(map[string]string, len(names))

	var b strings.Builder
	fmt.Fprintf(&b, "query { repository(owner: %s, name: %s) {",
		strconv.Quote(repo.Owner), strconv.Quote(repo.Name))
	for i, name := range names {
		// Branch names are not valid GraphQL identifiers, so fields are aliased
		// positionally and mapped back through `aliases`.
		alias := fmt.Sprintf("b%d", i)
		aliases[alias] = name
		fmt.Fprintf(&b, " %s: pullRequests(headRefName: %s, first: %d, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { number state headRepositoryOwner { login } } }",
			alias, strconv.Quote(name), prsPerBranch)
	}
	b.WriteString(" } }")

	return b.String(), aliases
}

// parseQueryResult turns the aliased response into branch name -> pull request.
//
// A pull request opened from a fork can carry the same head branch name as a
// local branch of ours while being an entirely different line of work, so only
// pull requests whose head repository is this repository are kept.
func parseQueryResult(raw []byte, aliases map[string]string, owner string) (map[string]PR, error) {
	var payload struct {
		Data struct {
			Repository map[string]struct {
				Nodes []struct {
					Number              int    `json:"number"`
					State               string `json:"state"`
					HeadRepositoryOwner struct {
						Login string `json:"login"`
					} `json:"headRepositoryOwner"`
				} `json:"nodes"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse pull request lookup: %w", err)
	}

	result := map[string]PR{}
	for alias, field := range payload.Data.Repository {
		name, ok := aliases[alias]
		if !ok {
			continue
		}
		for _, node := range field.Nodes {
			if !strings.EqualFold(node.HeadRepositoryOwner.Login, owner) {
				continue
			}
			pr := PR{Number: node.Number, Merged: node.State == "MERGED", State: node.State}
			// A branch name can carry several pull requests over time. A merged
			// one is the only decisive answer, so it always wins; otherwise the
			// most recent is kept, which is the first node returned.
			if existing, seen := result[name]; seen && (existing.Merged || !pr.Merged) {
				continue
			}
			result[name] = pr
		}
	}
	return result, nil
}

func chunk(names []string, size int) [][]string {
	var batches [][]string
	for start := 0; start < len(names); start += size {
		end := start + size
		if end > len(names) {
			end = len(names)
		}
		batches = append(batches, names[start:end])
	}
	return batches
}
