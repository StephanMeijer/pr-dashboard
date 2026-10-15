package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/StephanMeijer/pr-dashboard/internal/gh"
)

var t0 = time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC)

func newTestModel(fetch Fetcher) (Model, *time.Time) {
	return newTestModelWithOpener(fetch, nil)
}

func newTestModelWithOpener(fetch Fetcher, open Opener) (Model, *time.Time) {
	now := t0
	m := New(fetch, open, []string{"acme/widgets", "acme/broken"}, time.Minute)
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

func twoPRs() []gh.PR {
	return []gh.PR{
		{Repo: "acme/widgets", Number: 7, Title: "Add sprockets", Author: "octocat", UpdatedAt: t0.Add(-time.Hour), CI: gh.CISuccess},
		{Repo: "acme/gadgets", Number: 12, Title: "Fix cogs", Author: "hubot", UpdatedAt: t0.Add(-2 * time.Hour), CI: gh.CIFailure},
	}
}

func TestEnterAndOOpenSelectedPR(t *testing.T) {
	keyPresses := map[string]tea.KeyMsg{
		"enter": {Type: tea.KeyEnter},
		"o":     {Type: tea.KeyRunes, Runes: []rune("o")},
	}
	for name, press := range keyPresses {
		t.Run(name, func(t *testing.T) {
			var gotRepo string
			var gotNumber int
			open := func(_ context.Context, repo string, number int) error {
				gotRepo, gotNumber = repo, number
				return nil
			}
			m, _ := newTestModelWithOpener(nil, open)
			m = update(m, resultMsg{prs: twoPRs(), at: t0})
			m = update(m, tea.KeyMsg{Type: tea.KeyDown})

			_, cmd := m.Update(press)
			if cmd == nil {
				t.Fatal("expected an open command")
			}
			msg, ok := cmd().(openMsg)
			if !ok || msg.err != nil {
				t.Fatalf("unexpected result %#v", msg)
			}
			if gotRepo != "acme/gadgets" || gotNumber != 12 {
				t.Errorf("opened %s#%d, want acme/gadgets#12", gotRepo, gotNumber)
			}
		})
	}
}

func TestFirstRowIsSelectedAfterFirstFetch(t *testing.T) {
	var gotNumber int
	m, _ := newTestModelWithOpener(nil, func(_ context.Context, _ string, number int) error {
		gotNumber = number
		return nil
	})
	// The clock tick re-sets the rows, which moves the cursor of the still
	// empty table to -1.
	m = update(m, clockMsg(t0))
	m = update(m, resultMsg{prs: twoPRs(), at: t0})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter must open the first row without moving the cursor first")
	}
	cmd()
	if gotNumber != 7 {
		t.Errorf("opened #%d, want the first row #7", gotNumber)
	}
}

func TestOpenWithNoRowsIsNoop(t *testing.T) {
	m, _ := newTestModelWithOpener(nil, func(context.Context, string, int) error {
		t.Error("opener must not be called without rows")
		return nil
	})
	m = update(m, resultMsg{at: t0})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("expected no command when there are no pull requests")
	}
}

func TestOpenErrorIsShownAndClearedOnRefresh(t *testing.T) {
	m, _ := newTestModel(nil)
	m = update(m, resultMsg{prs: twoPRs(), at: t0})
	m = update(m, openMsg{err: errors.New("open acme/widgets#7: no browser")})
	if view := m.View(); !strings.Contains(view, "open acme/widgets#7: no browser") {
		t.Error("view missing open error")
	}

	m = update(m, resultMsg{prs: twoPRs(), at: t0})
	if view := m.View(); strings.Contains(view, "no browser") {
		t.Error("open error should clear after a refresh")
	}
}

func TestHelpShowsOpenBinding(t *testing.T) {
	m, _ := newTestModel(nil)
	if view := m.View(); !strings.Contains(view, "enter/o open") {
		t.Errorf("help footer missing open binding:\n%s", view)
	}
}

func TestViewShowsCIState(t *testing.T) {
	states := map[gh.CIState]string{
		gh.CISuccess: "✓ success",
		gh.CIFailure: "✗ failure",
		gh.CIRunning: "● running",
		gh.CIPending: "○ pending",
		gh.CINone:    "– none",
	}
	for state, want := range states {
		t.Run(string(state), func(t *testing.T) {
			m, _ := newTestModel(nil)
			prs := []gh.PR{{Repo: "acme/widgets", Number: 1, Title: "t", Author: "a", UpdatedAt: t0, CI: state}}
			m = update(m, resultMsg{prs: prs, at: t0})
			view := m.View()
			if !strings.Contains(view, "CI") || !strings.Contains(view, want) {
				t.Errorf("view missing CI header or %q:\n%s", want, view)
			}
		})
	}
}

// The table truncates cells without regard for escape sequences, so colour
// must survive on a real terminal, where tests normally run without it.
func TestCIStateSurvivesColour(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m, _ := newTestModel(nil)
	prs := twoPRs()
	prs[0].Conflicting = true
	m = update(m, resultMsg{prs: prs, at: t0}) // first row is selected
	view := m.View()
	for _, want := range []string{"✓ success", "✗ failure", "✗ conflict"} {
		if !strings.Contains(view, want) {
			t.Errorf("coloured view missing %q:\n%q", want, view)
		}
	}
	if !strings.Contains(view, "\x1b[") {
		t.Error("expected the CI label to be coloured")
	}
}

// A table wider than the terminal is cut off on the right, which hides the
// last column. The columns must shrink instead.
func TestCIColumnFitsNarrowTerminals(t *testing.T) {
	const minWidth = 82
	for width := minWidth; width <= 200; width++ {
		m := New(nil, nil, []string{"stephanmeijer/meneerhenk"}, time.Minute)
		m.now = func() time.Time { return t0 }
		m = update(m, tea.WindowSizeMsg{Width: width, Height: 20})
		prs := []gh.PR{{Repo: "stephanmeijer/meneerhenk", Number: 578, Title: "feat(executor): apply_patch for GPT-family models", Author: "StephanMeijer", UpdatedAt: t0, CI: gh.CIFailure, Conflicting: true}}
		m = update(m, resultMsg{prs: prs, at: t0})

		for _, line := range strings.Split(m.View(), "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Fatalf("width %d: line is %d wide: %q", width, w, line)
			}
		}
		view := m.View()
		for _, want := range []string{"✗ failure", "✗ conflict"} {
			if !strings.Contains(view, want) {
				t.Fatalf("width %d: %q missing:\n%s", width, want, view)
			}
		}
	}
}

func TestConflictColumn(t *testing.T) {
	m, _ := newTestModel(nil)
	prs := twoPRs()
	prs[1].Conflicting = true
	m = update(m, resultMsg{prs: prs, at: t0})

	var lines []string
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "✗ conflict") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "acme/gadgets") {
		t.Errorf("want only acme/gadgets marked as conflicting, got %q", lines)
	}
	if !strings.Contains(m.View(), "Conflict") {
		t.Error("view missing Conflict header")
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
