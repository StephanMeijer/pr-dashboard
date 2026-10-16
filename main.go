// Command pr-dashboard shows the open pull requests of GitHub repositories
// in an auto-refreshing terminal UI, using the gh CLI as its backend.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/StephanMeijer/pr-dashboard/internal/gh"
	"github.com/StephanMeijer/pr-dashboard/internal/ui"
)

// minInterval keeps refreshes from hammering the GitHub API.
const minInterval = 10 * time.Second

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "pr-dashboard:", err)
		os.Exit(1)
	}
}

func run() error {
	base := flag.String("base", "", "only show pull requests into this base branch, e.g. main")
	flat := flag.Bool("flat", false, "list pull requests newest first instead of grouped by repository")
	interval := flag.Duration("interval", time.Minute, "time between automatic refreshes (minimum 10s)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [--interval 60s] [--base main] [--flat] owner/repo [owner/repo ...]\n\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	repos := flag.Args()
	if len(repos) == 0 {
		flag.Usage()
		return fmt.Errorf("at least one repository is required")
	}
	for _, r := range repos {
		if !repoPattern.MatchString(r) {
			return fmt.Errorf("invalid repository %q: expected owner/name", r)
		}
	}
	if *interval < minInterval {
		return fmt.Errorf("interval %s is below the minimum of %s", *interval, minInterval)
	}

	client := gh.New()
	model := ui.New(client.FetchAll, client.Open, repos, *interval).WithBase(*base).WithGrouping(!*flat)
	_, err := tea.NewProgram(model, tea.WithAltScreen()).Run()
	return err
}
