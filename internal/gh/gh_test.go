package gh

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func fixtureRunner(t *testing.T) Runner {
	t.Helper()
	data, err := os.ReadFile("testdata/pr_list.json")
	if err != nil {
		t.Fatal(err)
	}
	return func(_ context.Context, args ...string) ([]byte, error) {
		repo := args[slices.Index(args, "--repo")+1]
		if repo == "acme/broken" {
			return nil, errors.New("could not resolve repository")
		}
		return data, nil
	}
}

func TestFetchParsesGhOutput(t *testing.T) {
	c := &Client{Run: fixtureRunner(t)}
	prs, err := c.Fetch(context.Background(), "acme/widgets")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 {
		t.Fatalf("got %d prs, want 2", len(prs))
	}
	got := prs[1]
	if got.Repo != "acme/widgets" || got.Number != 2 || got.Author != "dependabot" ||
		got.Branch != "deps/bump" || !got.Draft || got.UpdatedAt.IsZero() {
		t.Errorf("unexpected pr: %+v", got)
	}
	if prs[0].Base != "main" || got.Base != "release/1.x" {
		t.Errorf("Base = %q, %q; want main, release/1.x", prs[0].Base, got.Base)
	}
	if prs[0].Conflicting || !got.Conflicting {
		t.Errorf("Conflicting = %v, %v; want false, true", prs[0].Conflicting, got.Conflicting)
	}
	if prs[0].CI != CISuccess || got.CI != CIFailure {
		t.Errorf("CI = %q, %q; want success, failure", prs[0].CI, got.CI)
	}
}

func TestFetchAllKeepsPartialResultsAndSorts(t *testing.T) {
	c := &Client{Run: fixtureRunner(t)}
	prs, err := c.FetchAll(context.Background(), []string{"acme/widgets", "acme/broken"})
	if err == nil || !strings.Contains(err.Error(), "acme/broken") {
		t.Fatalf("expected error naming acme/broken, got %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("got %d prs, want 2", len(prs))
	}
	if prs[0].Number != 2 {
		t.Errorf("want most recently updated first, got #%d", prs[0].Number)
	}
}

func TestOpenRunsPrViewWeb(t *testing.T) {
	var got []string
	c := &Client{Run: func(_ context.Context, args ...string) ([]byte, error) {
		got = args
		return nil, nil
	}}
	if err := c.Open(context.Background(), "acme/widgets", 7); err != nil {
		t.Fatal(err)
	}
	want := []string{"pr", "view", "7", "--repo", "acme/widgets", "--web"}
	if !slices.Equal(got, want) {
		t.Errorf("args = %v, want %v", got, want)
	}
}

func TestOpenWrapsRunnerError(t *testing.T) {
	cause := errors.New("no browser")
	c := &Client{Run: func(context.Context, ...string) ([]byte, error) { return nil, cause }}
	err := c.Open(context.Background(), "acme/widgets", 7)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "acme/widgets#7") {
		t.Errorf("unexpected error %v", err)
	}
}

func TestRollup(t *testing.T) {
	run := func(status, conclusion string) rawCheck { return rawCheck{Status: status, Conclusion: conclusion} }
	ok := run("COMPLETED", "SUCCESS")
	cases := []struct {
		name   string
		checks []rawCheck
		want   CIState
	}{
		{"no checks", nil, CINone},
		{"all success", []rawCheck{ok, ok}, CISuccess},
		{"skipped and neutral only", []rawCheck{run("COMPLETED", "SKIPPED"), run("COMPLETED", "NEUTRAL")}, CISuccess},
		{"failure among successes", []rawCheck{ok, run("COMPLETED", "FAILURE"), ok}, CIFailure},
		{"timed out", []rawCheck{ok, run("COMPLETED", "TIMED_OUT")}, CIFailure},
		{"cancelled", []rawCheck{run("COMPLETED", "CANCELLED")}, CIFailure},
		{"running beats success", []rawCheck{ok, run("IN_PROGRESS", "")}, CIRunning},
		{"running beats pending", []rawCheck{run("QUEUED", ""), run("IN_PROGRESS", "")}, CIRunning},
		{"failure beats running", []rawCheck{run("IN_PROGRESS", ""), run("COMPLETED", "FAILURE")}, CIFailure},
		{"pending beats success", []rawCheck{ok, run("QUEUED", "")}, CIPending},
		{"waiting is pending", []rawCheck{run("WAITING", "")}, CIPending},
		{"status context success", []rawCheck{{State: "SUCCESS"}}, CISuccess},
		{"status context error", []rawCheck{ok, {State: "ERROR"}}, CIFailure},
		{"status context expected", []rawCheck{{State: "EXPECTED"}}, CIPending},
		{"unknown conclusion is not green", []rawCheck{run("COMPLETED", "SOMETHING_NEW")}, CIPending},
		{"unknown status is not green", []rawCheck{run("SOMETHING_NEW", "")}, CIPending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rollup(tc.checks); got != tc.want {
				t.Errorf("rollup = %q, want %q", got, tc.want)
			}
		})
	}
}
