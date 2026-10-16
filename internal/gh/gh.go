// Package gh fetches pull requests by shelling out to the GitHub CLI.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// prFields are the JSON fields requested from `gh pr list`.
const prFields = "number,title,author,headRefName,baseRefName,isDraft,updatedAt,url,mergeable,statusCheckRollup"

// prLimit caps how many open pull requests are fetched per repository.
const prLimit = 100

// CIState is the aggregated state of the checks on a pull request's head commit.
type CIState string

// CI states, from a pull request without checks to one with a failing check.
const (
	CINone    CIState = "none"
	CISuccess CIState = "success"
	CIPending CIState = "pending"
	CIRunning CIState = "running"
	CIFailure CIState = "failure"
)

// PR is an open pull request in a repository.
type PR struct {
	Repo      string
	Number    int
	Title     string
	Author    string
	Branch    string
	Base      string // branch the pull request is opened against
	Draft     bool
	UpdatedAt time.Time
	URL       string
	CI        CIState

	// Conflicting is set when the branch has merge conflicts with its base.
	// GitHub computes this lazily, so it is false while still unknown.
	Conflicting bool
}

// Runner executes gh with the given arguments and returns its stdout.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// ExecRunner runs the real gh binary found on PATH.
func ExecRunner(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out, nil
}

// Client fetches pull requests using a Runner.
type Client struct {
	Run Runner
}

// New returns a Client backed by the gh binary.
func New() *Client {
	return &Client{Run: ExecRunner}
}

type rawPR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	HeadRefName string    `json:"headRefName"`
	BaseRefName string    `json:"baseRefName"`
	IsDraft     bool      `json:"isDraft"`
	UpdatedAt   time.Time `json:"updatedAt"`
	URL         string    `json:"url"`

	Mergeable         string     `json:"mergeable"`
	StatusCheckRollup []rawCheck `json:"statusCheckRollup"`
}

// rawCheck is an entry of a status check rollup: a CheckRun carries status
// and conclusion, a StatusContext carries state.
type rawCheck struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// Fetch returns the open pull requests of a single repository ("owner/name").
func (c *Client) Fetch(ctx context.Context, repo string) ([]PR, error) {
	out, err := c.Run(ctx, "pr", "list",
		"--repo", repo,
		"--state", "open",
		"--limit", fmt.Sprint(prLimit),
		"--json", prFields,
	)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", repo, err)
	}
	var raw []rawPR
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("%s: decode gh output: %w", repo, err)
	}
	prs := make([]PR, len(raw))
	for i, r := range raw {
		prs[i] = PR{
			Repo:        repo,
			Number:      r.Number,
			Title:       r.Title,
			Author:      r.Author.Login,
			Branch:      r.HeadRefName,
			Base:        r.BaseRefName,
			Draft:       r.IsDraft,
			UpdatedAt:   r.UpdatedAt,
			URL:         r.URL,
			CI:          rollup(r.StatusCheckRollup),
			Conflicting: r.Mergeable == "CONFLICTING",
		}
	}
	return prs, nil
}

// rollup aggregates checks into a single state. Failure beats running, which
// beats pending, which beats success. Skipped and neutral checks are ignored,
// and a pull request without checks has none. Values that are not recognised
// count as pending so that a new GitHub value never reads as green.
func rollup(checks []rawCheck) CIState {
	if len(checks) == 0 {
		return CINone
	}
	worst := CISuccess
	for _, c := range checks {
		if s := checkState(c); ciRank[s] > ciRank[worst] {
			worst = s
		}
	}
	return worst
}

var ciRank = map[CIState]int{CISuccess: 0, CIPending: 1, CIRunning: 2, CIFailure: 3}

func checkState(c rawCheck) CIState {
	if c.State != "" {
		switch c.State {
		case "SUCCESS":
			return CISuccess
		case "FAILURE", "ERROR":
			return CIFailure
		default: // PENDING, EXPECTED and anything new
			return CIPending
		}
	}
	switch c.Status {
	case "COMPLETED":
		switch c.Conclusion {
		case "SUCCESS", "NEUTRAL", "SKIPPED":
			return CISuccess
		case "FAILURE", "TIMED_OUT", "CANCELLED", "STARTUP_FAILURE", "ACTION_REQUIRED", "STALE":
			return CIFailure
		}
		return CIPending
	case "IN_PROGRESS":
		return CIRunning
	default: // QUEUED, WAITING, PENDING, REQUESTED and anything new
		return CIPending
	}
}

// Open shows pull request number of repo ("owner/name") in the default
// browser, leaving it to gh to launch the browser on every platform.
func (c *Client) Open(ctx context.Context, repo string, number int) error {
	if _, err := c.Run(ctx, "pr", "view", fmt.Sprint(number), "--repo", repo, "--web"); err != nil {
		return fmt.Errorf("open %s#%d: %w", repo, number, err)
	}
	return nil
}

// FetchAll fetches every repository concurrently. Pull requests from the
// repositories that succeeded are returned, most recently updated first,
// together with the joined errors of the ones that failed.
func (c *Client) FetchAll(ctx context.Context, repos []string) ([]PR, error) {
	type result struct {
		prs []PR
		err error
	}
	results := make([]result, len(repos))
	var wg sync.WaitGroup
	for i, repo := range repos {
		wg.Go(func() {
			prs, err := c.Fetch(ctx, repo)
			results[i] = result{prs, err}
		})
	}
	wg.Wait()

	var all []PR
	var errs []error
	for _, r := range results {
		all = append(all, r.prs...)
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].UpdatedAt.After(all[j].UpdatedAt)
	})
	return all, errors.Join(errs...)
}
