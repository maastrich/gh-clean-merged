# gh-clean-merged

A [GitHub CLI](https://cli.github.com) extension that deletes the local branches left behind by finished pull requests — **including branches merged by squash or rebase**, which `git branch --merged` cannot see.

## Why

`git branch -d` refuses to delete a squash-merged branch: squashing creates a brand new commit on the base branch with no ancestry link to the branch, so git has no way to tell that the work landed. Repositories with "Squash and merge" enabled therefore accumulate stale local branches forever, and clearing them by hand means checking each one against GitHub.

This extension asks GitHub instead. A branch that still exists on the remote is shared work and is left alone; what remains is judged on its pull request, and anything with no pull request at all is reported rather than deleted.

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
| `-b`, `--base` | Base branch orphan branches are compared against (default: the repository default branch) |
| `--remote` | Remote whose branches count as shared work (default `origin`) |
| `--keep-closed` | Keep branches whose pull request was closed without merging |
| `--protected` | Branch names or globs that must never be deleted, e.g. `prod/*` (repeatable, or comma separated) |
| `--no-fetch` | Skip `git fetch --prune` and use the refs already on disk |
| `--no-config` | Ignore the configuration files |
| `-v`, `--verbose` | Also list the branches that are kept, with the reason |
| `--color` | `auto`, `always` or `never` (default `auto`) |

## Configuration

Two optional JSON files hold the flags you would otherwise retype, most usefully `protected`:

| File | Where |
| --- | --- |
| Global | `$XDG_CONFIG_HOME/gh-clean-merged/config.json`, or `~/.config/gh-clean-merged/config.json` |
| Local | `.gh-clean-merged.json` at the root of the repository |

They are merged, and the command line wins over both: **defaults → global → local → flags**. Scalars are replaced by whatever comes last, while `protected` accumulates, so a repository adds its deploy branches to what you protect everywhere rather than repeating them.

```jsonc
{
  "$schema": "https://raw.githubusercontent.com/maastrich/gh-clean-merged/main/schema.json",
  "base": "main",
  "remote": "origin",
  "protected": ["prod/*", "beta/*", "release"],
  "keepClosed": true,
  "noFetch": false,
  "dryRun": false,
  "verbose": false,
  "color": "auto"
}
```

Unknown keys are an error rather than a silent no-op, and [`schema.json`](schema.json) is published from the repository so editors complete and validate the file.

### Editing from the command line

Subcommands work on the repository's file, or on the global one with `-g`:

```sh
gh clean-merged config add protected 'prod/*' 'beta/*'   # append to this repository's list
gh clean-merged config set -g protected 'release'        # replace the global list
gh clean-merged config set -g keepClosed true
gh clean-merged config remove protected 'beta/*'
gh clean-merged config unset base                        # let the next file up decide again
gh clean-merged config list                              # resolved values, and where they came from
gh clean-merged config get protected
gh clean-merged config path -g
```

`--no-config` ignores both files for one run.

## How a branch is judged

Every local branch goes through the same three questions.

**1. Does it still exist on the remote?** Then it is shared work — someone may be reviewing it, deploying from it, or merging the base branch into it — and it is left alone. Long lived branches such as `prod/*`, `beta/*` and `preprod/*` live here: they carry no pull request of their own and sit behind the base branch, so any content comparison would wrongly call them merged.

**2. Otherwise, what does its pull request say?**

| Pull request | Outcome |
| --- | --- |
| Merged | Deleted. Squash and rebase merges included: GitHub knows the merge happened even though git does not. |
| Closed without merging | Deleted, since the work was abandoned and the remote branch is already gone. Pass `--keep-closed` to hold on to them. |
| Open | Kept |

Pull requests are looked up by branch name, in batches, so a branch merged long ago resolves the same as one merged this morning. Pull requests opened from a fork never match a local branch, even when the branch names are identical.

**3. No remote branch and no pull request?** Then nothing outside this machine knows about it, so it is never deleted — only listed, with where it stands versus the base branch, for you to decide:

```
Not on origin and no pull request, left alone
  ? wip/experiment            no pull request, changes not in origin/main
  ? review/pr-1234            no pull request, changes already in origin/main
```

The current branch, the base branch, branches checked out in another worktree and anything matching `--protected` are never touched.

Branches whose merge left ancestry behind are removed with `git branch -d`. Squash merges, rebase merges and abandoned branches need `git branch -D`, because git itself cannot see those merges — which is why the output states the pull request behind each deletion.

## Output

Sections are colour coded, and the colours come from the sixteen ANSI colours rather than fixed RGB values, so they follow whatever theme the terminal is set to instead of glaring on a light background or disappearing on a dark one. Colour turns itself off when the output is piped, when `TERM=dumb`, and when [`NO_COLOR`](https://no-color.org) is set; `--color=always` forces it back on.

## Speed

Branches are analysed in parallel across the available cores, and the pull request lookup is a handful of batched GraphQL requests rather than one per branch. Branches that still exist on the remote are kept whatever their pull request says, so their pull request is only looked up under `--verbose`, where the reason is actually printed. A repository with a few hundred local branches takes a couple of seconds, plus the `git fetch` — pass `--no-fetch` to skip that when the refs are fresh.

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
