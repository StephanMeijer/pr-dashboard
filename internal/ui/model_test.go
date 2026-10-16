package ui

import (
	"context"
	"errors"
	"slices"
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
	if got := len(m.table.Rows()); got != 2 {
		t.Fatalf("got %d rows, want a group header and 1 pull request", got)
	}
	if row := m.table.Rows()[1]; row[0] != "#7" || row[3] != "3h" {
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
	for _, want := range []string{"✓ success", "✗ failure", "✗ conflict", "▾ acme/widgets · 1 open"} {
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
		for _, grouped := range []bool{true, false} {
			testCIColumnFits(t, width, grouped)
		}
	}
}

func testCIColumnFits(t *testing.T, width int, grouped bool) {
	t.Helper()
	{
		m := New(nil, nil, []string{"stephanmeijer/meneerhenk"}, time.Minute).WithGrouping(grouped)
		m.now = func() time.Time { return t0 }
		m = update(m, tea.WindowSizeMsg{Width: width, Height: 20})
		prs := []gh.PR{{Repo: "stephanmeijer/meneerhenk", Number: 578, Title: "feat(executor): apply_patch for GPT-family models", Author: "StephanMeijer", UpdatedAt: t0, CI: gh.CIFailure, Conflicting: true}}
		m = update(m, resultMsg{prs: prs, at: t0})

		for _, line := range strings.Split(m.View(), "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Fatalf("width %d, grouped %v: line is %d wide: %q", width, grouped, w, line)
			}
		}
		view := m.View()
		for _, want := range []string{"✗ failure", "✗ conflict"} {
			if !strings.Contains(view, want) {
				t.Fatalf("width %d, grouped %v: %q missing:\n%s", width, grouped, want, view)
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
	if len(lines) != 1 || !strings.Contains(lines[0], "Fix cogs") {
		t.Errorf("want only the Fix cogs pull request marked as conflicting, got %q", lines)
	}
	if !strings.Contains(m.View(), "Conflict") {
		t.Error("view missing Conflict header")
	}
}

func basePRs() []gh.PR {
	return []gh.PR{
		{Repo: "acme/widgets", Number: 1, Title: "One", Base: "main", UpdatedAt: t0},
		{Repo: "acme/widgets", Number: 2, Title: "Two", Base: "release/1.x", UpdatedAt: t0},
		{Repo: "acme/gadgets", Number: 3, Title: "Three", Base: "main", UpdatedAt: t0},
		{Repo: "acme/gadgets", Number: 4, Title: "Four", Base: "develop", UpdatedAt: t0},
	}
}

func press(m Model, k string) Model {
	return update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
}

func TestBaseKeyCyclesThroughBranches(t *testing.T) {
	m, _ := newTestModel(nil)
	m = update(m, resultMsg{prs: basePRs(), at: t0})

	want := []struct {
		base  string
		shown int
	}{{"develop", 1}, {"main", 2}, {"release/1.x", 1}, {"", 4}}
	for _, w := range want {
		m = press(m, "b")
		if m.base != w.base || len(m.prs) != w.shown {
			t.Fatalf("after b: base %q showing %d, want %q showing %d", m.base, len(m.prs), w.base, w.shown)
		}
	}
}

func TestBaseFilterHidesOtherBranches(t *testing.T) {
	m, _ := newTestModel(nil)
	m = update(m, resultMsg{prs: basePRs(), at: t0})
	m = press(press(m, "b"), "b") // main

	view := m.View()
	if !strings.Contains(view, "2 of 4 open") || !strings.Contains(view, "base: main") {
		t.Errorf("title does not describe the filter:\n%s", view)
	}
	for _, hidden := range []string{"Two", "Four"} {
		if strings.Contains(view, hidden) {
			t.Errorf("view shows %q, which does not target main", hidden)
		}
	}
	for _, shown := range []string{"One", "Three"} {
		if !strings.Contains(view, shown) {
			t.Errorf("view hides %q, which targets main", shown)
		}
	}
}

func TestWithBaseAppliesToTheFirstFetch(t *testing.T) {
	m, _ := newTestModel(nil)
	m = m.WithBase("develop")
	m = update(m, resultMsg{prs: basePRs(), at: t0})
	if len(m.prs) != 1 || m.prs[0].Number != 4 {
		t.Errorf("shown = %+v, want only #4", m.prs)
	}
}

func TestBaseFilterSurvivesRefresh(t *testing.T) {
	m, _ := newTestModel(nil)
	m = m.WithBase("main")
	m = update(m, resultMsg{prs: basePRs(), at: t0})
	m = update(m, resultMsg{prs: basePRs()[1:], at: t0}) // #1 merged meanwhile
	if len(m.prs) != 1 || m.prs[0].Number != 3 {
		t.Errorf("shown = %+v, want only #3", m.prs)
	}
}

func TestUnknownBaseShowsNothingAndBCyclesBackToAll(t *testing.T) {
	m, _ := newTestModel(nil)
	m = m.WithBase("mian")
	m = update(m, resultMsg{prs: basePRs(), at: t0})
	if len(m.prs) != 0 {
		t.Fatalf("shown %d, want none", len(m.prs))
	}
	m = press(m, "b")
	if m.base != "" || len(m.prs) != 4 {
		t.Errorf("after b: base %q showing %d, want all 4", m.base, len(m.prs))
	}
}

func TestOpenUsesTheFilteredRow(t *testing.T) {
	var got int
	m, _ := newTestModelWithOpener(nil, func(_ context.Context, _ string, number int) error {
		got = number
		return nil
	})
	m = update(m, resultMsg{prs: basePRs(), at: t0})
	m = press(m, "b") // develop: only #4
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected an open command")
	}
	cmd()
	if got != 4 {
		t.Errorf("opened #%d, want #4", got)
	}
}

func groupPRs() []gh.PR {
	// Most recently updated first, as the fetcher returns them.
	return []gh.PR{
		{Repo: "acme/widgets", Number: 1, Title: "W-new", UpdatedAt: t0.Add(-1 * time.Hour)},
		{Repo: "acme/gadgets", Number: 2, Title: "G-new", UpdatedAt: t0.Add(-2 * time.Hour)},
		{Repo: "acme/widgets", Number: 3, Title: "W-old", UpdatedAt: t0.Add(-3 * time.Hour)},
		{Repo: "acme/gadgets", Number: 4, Title: "G-old", UpdatedAt: t0.Add(-4 * time.Hour)},
	}
}

func titles(m Model) []string {
	col := 1 // after the number
	if !m.grouped {
		col = 2 // after the repository and the number
	}
	var out []string
	for _, row := range m.table.Rows() {
		out = append(out, row[col])
	}
	return out
}

func TestGroupsFollowRepoOrderWithHeaders(t *testing.T) {
	m := New(nil, nil, []string{"acme/gadgets", "acme/broken", "acme/widgets"}, time.Minute)
	m.now = func() time.Time { return t0 }
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 20})
	m = update(m, resultMsg{prs: groupPRs(), at: t0})

	want := []string{
		"▾ acme/gadgets · 2 open", "G-new", "G-old",
		"▾ acme/widgets · 2 open", "W-new", "W-old",
	}
	if got := titles(m); !slices.Equal(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
	if strings.Contains(m.View(), "acme/broken") {
		t.Error("a repository without pull requests must not get a header")
	}
}

func TestGroupHeaderCountsOnlyShownPullRequests(t *testing.T) {
	m, _ := newTestModel(nil)
	prs := groupPRs()
	prs[0].Base, prs[1].Base, prs[2].Base, prs[3].Base = "main", "main", "dev", "dev"
	m = update(m, resultMsg{prs: prs, at: t0})
	m = m.WithBase("main")
	if got, want := titles(m), []string{"▾ acme/widgets · 1 open", "W-new", "▾ acme/gadgets · 1 open", "G-new"}; !slices.Equal(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

func TestCursorSkipsGroupHeaders(t *testing.T) {
	var opened []int
	m, _ := newTestModelWithOpener(nil, func(_ context.Context, _ string, number int) error {
		opened = append(opened, number)
		return nil
	})
	m = update(m, resultMsg{prs: groupPRs(), at: t0})
	// widgets: #1 #3, gadgets: #2 #4
	openSelected := func() {
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("cursor is on a group header")
		}
		cmd()
	}

	openSelected() // starts on the first pull request, not the header
	m = press(m, "j")
	openSelected()
	m = press(m, "j") // crosses the gadgets header
	openSelected()
	m = press(m, "k") // back over it
	openSelected()
	m = press(m, "k")
	m = press(m, "k") // stays on the first pull request
	openSelected()

	if want := []int{1, 3, 2, 3, 1}; !slices.Equal(opened, want) {
		t.Errorf("opened %v, want %v", opened, want)
	}
}

func TestOpenOnHeaderDoesNothing(t *testing.T) {
	m, _ := newTestModelWithOpener(nil, func(context.Context, string, int) error {
		t.Error("opener must not be called for a header")
		return nil
	})
	m = update(m, resultMsg{prs: groupPRs(), at: t0})
	m.table.SetCursor(0) // the header
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("expected no command on a group header")
	}
}

func TestGKeyTogglesFlatList(t *testing.T) {
	m, _ := newTestModel(nil)
	m = update(m, resultMsg{prs: groupPRs(), at: t0})

	m = press(m, "g")
	if got, want := titles(m), []string{"W-new", "G-new", "W-old", "G-old"}; !slices.Equal(got, want) {
		t.Errorf("flat rows = %q, want %q", got, want)
	}
	view := m.View()
	if !strings.Contains(view, "Repo") || !strings.Contains(view, "acme/gadgets") || strings.Contains(view, "▾") {
		t.Errorf("flat view should have a Repo column and no group headers:\n%s", view)
	}
	if _, ok := m.selected(); !ok {
		t.Error("a pull request should be selected after toggling")
	}

	m = press(m, "g")
	if !strings.Contains(m.View(), "▾ acme/widgets · 2 open") {
		t.Error("toggling again should group again")
	}
}

func TestFlatModeFromWithGrouping(t *testing.T) {
	m, _ := newTestModel(nil)
	m = m.WithGrouping(false)
	m = update(m, resultMsg{prs: groupPRs(), at: t0})
	if got, want := titles(m), []string{"W-new", "G-new", "W-old", "G-old"}; !slices.Equal(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
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
