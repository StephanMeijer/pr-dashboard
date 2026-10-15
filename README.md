# pr-dashboard

An auto-refreshing terminal dashboard of the open pull requests in the GitHub
repositories you name. It uses the [GitHub CLI](https://cli.github.com/) (`gh`)
to fetch data, so it works with whatever account `gh` is logged in to.

```
 PR Dashboard  179 open across 2 repos
 Repo                     #       Title                                     Author          Updated   Draft
───────────────────────────────────────────────────────────────────────────────────────────────────────────
 charmbracelet/bubbletea  #1868   fix(renderer): erase through rows a nar…  yanmxa          20m
 cli/cli                  #14678  `gh issue artifact` stack 12/12: Add ar…  BagToad         50m
 cli/cli                  #14679  Reject terminal stdin for `gh auth logi…  BagToad         1d        draft
Updated 18:04:05 · next refresh in 42s
↑/k up • ↓/j down • enter/o open • r refresh • q quit
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
pr-dashboard [--interval 60s] owner/repo [owner/repo ...]
```

| Flag         | Default | Description                                  |
| ------------ | ------- | -------------------------------------------- |
| `--interval` | `1m`    | Time between automatic refreshes (min `10s`) |

Pull requests from all repositories are shown in one list, most recently
updated first. If a repository can't be fetched, its error is shown under the
table and the other repositories still appear.

### Keys

| Key          | Action                  |
| ------------ | ----------------------- |
| `↑`/`k`      | Move up                 |
| `↓`/`j`      | Move down               |
| `enter`/`o`  | Open PR in the browser  |
| `r`          | Refresh now             |
| `q`/`ctrl+c` | Quit                    |

## Development

```sh
go test ./...
go run . cli/cli
```

Commits follow [Conventional Commits](https://www.conventionalcommits.org/).

## License

MIT
