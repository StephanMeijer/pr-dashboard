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
const prFields = "number,title,author,headRefName,isDraft,updatedAt,url"

// prLimit caps how many open pull requests are fetched per repository.
const prLimit = 100

// PR is an open pull request in a repository.
type PR struct {
	Repo      string
	Number    int
	Title     string
	Author    string
	Branch    string
	Draft     bool
	UpdatedAt time.Time
	URL       string
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
	IsDraft     bool      `json:"isDraft"`
	UpdatedAt   time.Time `json:"updatedAt"`
	URL         string    `json:"url"`
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
			Repo:      repo,
			Number:    r.Number,
			Title:     r.Title,
			Author:    r.Author.Login,
			Branch:    r.HeadRefName,
			Draft:     r.IsDraft,
			UpdatedAt: r.UpdatedAt,
			URL:       r.URL,
		}
	}
	return prs, nil
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
