# gh-clean-merged

A [GitHub CLI](https://cli.github.com) extension that deletes the local branches whose work already landed on the base branch — **including branches merged by squash or rebase**, which `git branch --merged` cannot see.

## Why

`git branch -d` refuses to delete a squash-merged branch: squashing creates a brand new commit on the base branch with no ancestry link to the branch, so git has no way to tell that the work landed. Repositories with "Squash and merge" enabled therefore accumulate stale local branches forever, and clearing them by hand means checking each one against GitHub.

This extension does that check for you, and only deletes what it can justify.

## Install

```sh
gh extension install maastrich/gh-clean-merged
```

Upgrade with `gh extension upgrade clean-merged`.

## Usage

```sh
gh clean-merged              # show what would go, ask before deleting
gh clean-merged --dry-run    # show what would go, delete nothing
gh clean-merged --yes        # delete without asking
gh clean-merged --verbose    # also list the kept branches and why
```

### Flags

| Flag | Description |
| --- | --- |
| `-n`, `--dry-run` | List what would be deleted and exit |
| `-y`, `--yes` | Delete without asking for confirmation |
| `-b`, `--base` | Base branch to compare against (default: the repository default branch) |
| `--remote` | Remote holding the base branch (default `origin`) |
| `--no-fetch` | Skip `git fetch --prune` and use the refs already on disk |
| `--protected` | Branch names or globs that must never be deleted, e.g. `prod/*` (repeatable, or comma separated) |
| `--include-live` | Also consider branches whose remote branch still exists, instead of keeping them |
| `-v`, `--verbose` | Also list the branches that are kept, with the reason |

## How a branch is judged

A branch is deleted only when one of these proves its changes are on the base branch:

1. **Its pull request is merged on GitHub** — the decisive signal for squash and rebase merges, since GitHub knows the merge happened even though git does not. Pull requests are looked up by branch name, in batches, so a branch merged long ago resolves the same as one merged this morning.
2. **Contained in the base branch** — the ordinary merge-commit or fast-forward case, which git can prove on its own.
3. **Its diff is already applied** — for branches that never had a pull request, the branch's cumulative change is compared against the patches in the base branch by patch id.

Signals 2 and 3 only apply to branches whose remote counterpart is gone, because a live remote branch means the branch is still shared work — see below.

Everything else is kept, with the reason printed under `--verbose`. In particular:

- **A branch that still exists on the remote is kept** unless a merged pull request says otherwise. Long lived branches — `prod/*`, `beta/*`, `preprod/*` and friends — never have a pull request of their own and sit behind the base branch, either because the base branch is merged into them or because they track an older release commit. That makes them look merged to any content comparison, and their live remote branch is what tells them apart from finished work. Pass `--include-live` to judge those on content alone, and `--protected 'prod/*'` to keep specific ones regardless.
- **A deleted remote branch is not proof.** `git fetch --prune` marking an upstream as gone happens on abandoned pull requests too, so a `gone` branch with changes of its own is kept.
- **An open pull request outranks every other signal**, even if the changes already reached the base branch some other way.
- **A pull request from a fork never matches a local branch**, even when the branch names are identical — that match would delete unrelated local work.
- **The current branch, the base branch, and branches checked out in another worktree** are never touched.

Branches proved by signal 1 are removed with `git branch -d`. Signals 2 and 3 need `git branch -D`, because git itself cannot see those merges — which is why the output states the signal that justified each deletion.

## Speed

Judging a branch costs a handful of git processes, and the patch comparison walks the base branch, so branches are analysed in parallel across the available cores. The pull request lookup is a handful of batched GraphQL requests rather than one per branch. A repository with ~250 local branches takes a couple of seconds, plus the `git fetch` — pass `--no-fetch` to skip that when the refs are fresh.

## Development

```sh
make build       # build ./gh-clean-merged
make install     # install the local build as a gh extension
make test        # run the test suite
```

The tests build throwaway repositories containing a merge-commit branch, a squash-merged branch, a branch whose commits cancel out and an unmerged branch, then assert the verdict for each.

## Prior art

Inspired by [davidraviv/gh-clean-branches](https://github.com/davidraviv/gh-clean-branches), rewritten in Go with squash- and rebase-merge detection.

## License

MIT
