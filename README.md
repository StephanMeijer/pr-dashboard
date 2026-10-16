# pr-dashboard

An auto-refreshing terminal dashboard of the open pull requests in the GitHub
repositories you name. It uses the [GitHub CLI](https://cli.github.com/) (`gh`)
to fetch data, so it works with whatever account `gh` is logged in to.

```
 PR Dashboard  179 open across 2 repos
 Repo                     #       Title                                     Author          Updated   Draft  Conflict    CI
───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
 charmbracelet/bubbletea  #1868   fix(renderer): erase through rows a nar…  yanmxa          20m                          ✓ success
 cli/cli                  #14678  `gh issue artifact` stack 12/12: Add ar…  BagToad         50m              ✗ conflict  ● running
 cli/cli                  #14679  Reject terminal stdin for `gh auth logi…  BagToad         1d        draft              ✗ failure
Updated 18:04:05 · next refresh in 42s
↑/k up • ↓/j down • enter/o open • b base • r refresh • q quit
```

## Requirements

- Go 1.25+
- [`gh`](https://cli.github.com/), logged in (`gh auth login`)

## Install

```sh
go install github.com/StephanMeijer/pr-dashboard@latest
```

## Usage

```sh
pr-dashboard [--interval 60s] [--base main] owner/repo [owner/repo ...]
```

| Flag         | Default | Description                                  |
| ------------ | ------- | -------------------------------------------- |
| `--interval` | `1m`    | Time between automatic refreshes (min `10s`) |
| `--base`     | all     | Only show pull requests into this base branch |

Pull requests from all repositories are shown in one list, most recently
updated first. If a repository can't be fetched, its error is shown under the
table and the other repositories still appear.

### Filtering by base branch

Start with `--base main` to only list pull requests that target `main`, or
press `b` while running to cycle through the base branches that occur in the
current list: all, then each branch alphabetically, then all again. The title
bar shows the active filter, e.g. `53 of 67 open across 1 repos · base: main`.
The filter is kept across refreshes. A branch nobody targets shows an empty
list; press `b` to get back to all.

At most 100 open pull requests per repository are fetched, and the filter is
applied to those.

### Conflict column

`✗ conflict` means the branch has merge conflicts with its base branch and
needs to be rebased or merged before the pull request can be merged. The cell
stays empty when there are no conflicts, and also while GitHub has not worked
it out yet (it computes this lazily, so it can take a refresh or two).

### CI column

The CI column summarises the checks on each pull request's latest commit. When
several checks disagree the worst one wins: failure, then running, then pending,
then success.

| State       | Meaning                                                              |
| ----------- | -------------------------------------------------------------------- |
| `✓ success` | All checks passed (skipped and neutral checks are ignored)           |
| `● running` | At least one check is in progress                                    |
| `○ pending` | Checks are queued or waiting, or GitHub reported an unknown status   |
| `✗ failure` | A check failed, errored, timed out, was cancelled or needs action    |
| `– none`    | The pull request has no checks                                       |

### Keys

| Key          | Action                       |
| ------------ | ---------------------------- |
| `↑`/`k`      | Move up                      |
| `↓`/`j`      | Move down                    |
| `enter`/`o`  | Open PR in the browser       |
| `b`          | Cycle the base branch filter |
| `r`          | Refresh now                  |
| `q`/`ctrl+c` | Quit                         |

## Development

```sh
go test ./...
go run . cli/cli
```

Commits follow [Conventional Commits](https://www.conventionalcommits.org/).

## License

MIT
