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
