package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/StephanMeijer/pr-dashboard/internal/gh"
)

var t0 = time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC)

func newTestModel(fetch Fetcher) (Model, *time.Time) {
	now := t0
	m := New(fetch, []string{"acme/widgets", "acme/broken"}, time.Minute)
	m.now = func() time.Time { return now }
	return update(m, tea.WindowSizeMsg{Width: 120, Height: 20}), &now
}

func update(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func samplePRs() []gh.PR {
	return []gh.PR{{Repo: "acme/widgets", Number: 7, Title: "Add sprockets", Author: "octocat", UpdatedAt: t0.Add(-3 * time.Hour)}}
}

func TestResultPopulatesTableAndSchedulesRefresh(t *testing.T) {
	m, _ := newTestModel(nil)
	m = update(m, resultMsg{prs: samplePRs(), err: errors.New("acme/broken: not found"), at: t0})

	if m.loading {
		t.Error("still loading after result")
	}
	if got := len(m.table.Rows()); got != 1 {
		t.Fatalf("got %d rows, want 1", got)
	}
	if row := m.table.Rows()[0]; row[1] != "#7" || row[4] != "3h" {
		t.Errorf("unexpected row %v", row)
	}
	if !m.nextAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("nextAt = %v", m.nextAt)
	}
	view := m.View()
	for _, want := range []string{"Add sprockets", "acme/broken: not found", "next refresh in 1m0s"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestClockTriggersRefreshOnlyWhenDue(t *testing.T) {
	calls := 0
	fetch := func(context.Context, []string) ([]gh.PR, error) {
		calls++
		return samplePRs(), nil
	}
	m, now := newTestModel(fetch)
	m = update(m, resultMsg{prs: samplePRs(), at: t0})

	*now = t0.Add(30 * time.Second)
	m = update(m, clockMsg(*now))
	if m.loading {
		t.Fatal("refreshed before interval elapsed")
	}

	*now = t0.Add(time.Minute)
	next, cmd := m.Update(clockMsg(*now))
	m = next.(Model)
	if !m.loading || cmd == nil {
		t.Fatal("expected refresh once interval elapsed")
	}
}

func TestManualRefreshIgnoredWhileLoading(t *testing.T) {
	m, _ := newTestModel(nil)
	if !m.loading {
		t.Fatal("model should start loading")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd != nil {
		t.Error("refresh should be ignored while a fetch is in flight")
	}

	m = update(m, resultMsg{at: t0})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if !next.(Model).loading || cmd == nil {
		t.Error("expected manual refresh to start a fetch")
	}
}

func TestAge(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second: "45s",
		12 * time.Minute: "12m",
		5 * time.Hour:    "5h",
		72 * time.Hour:   "3d",
	}
	for d, want := range cases {
		if got := age(d); got != want {
			t.Errorf("age(%v) = %q, want %q", d, got, want)
		}
	}
}
