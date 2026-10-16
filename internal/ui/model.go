// Package ui implements the Bubble Tea dashboard of open pull requests.
package ui

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/StephanMeijer/pr-dashboard/internal/gh"
)

// Fetcher returns the open pull requests of the given repositories. It may
// return partial results together with an error.
type Fetcher func(ctx context.Context, repos []string) ([]gh.PR, error)

// Opener shows a pull request in the browser.
type Opener func(ctx context.Context, repo string, number int) error

// fetchTimeout bounds a single refresh of all repositories.
const fetchTimeout = 30 * time.Second

// openTimeout bounds launching the browser for a pull request.
const openTimeout = 10 * time.Second

// clockInterval is how often the countdown is redrawn and the refresh
// deadline is checked.
const clockInterval = time.Second

// Fixed column widths; the title column takes the remaining space.
const (
	numberWidth   = 6
	authorWidth   = 18
	updatedWidth  = 8
	draftWidth    = 5
	conflictWidth = 10
	ciWidth       = 9
	maxRepoWidth  = 30
	minTitle      = 20
	cellPadding   = 2 // table cells are padded one column on each side
)

// On a narrow terminal the Author, Repo and Title columns shrink, in that
// order, down to these widths so that the columns on the right (Draft, CI)
// stay on screen.
const (
	shrunkAuthor = 8
	shrunkRepo   = 12
	shrunkTitle  = 8
)

// chromeLines is the number of lines used around the table: title bar,
// status line and help line.
const chromeLines = 3

type (
	clockMsg  time.Time
	resultMsg struct {
		prs []gh.PR
		err error
		at  time.Time
	}
	openMsg struct{ err error }
)

type keyMap struct {
	Up      key.Binding
	Down    key.Binding
	Open    key.Binding
	Base    key.Binding
	Refresh key.Binding
	Quit    key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Open, k.Base, k.Refresh, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding { return [][]key.Binding{k.ShortHelp()} }

var keys = keyMap{
	Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	Open:    key.NewBinding(key.WithKeys("enter", "o"), key.WithHelp("enter/o", "open")),
	Base:    key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "base")),
	Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}

var (
	accent     = lipgloss.AdaptiveColor{Light: "#5A4FCF", Dark: "#9D8CFF"}
	muted      = lipgloss.AdaptiveColor{Light: "#6B6B6B", Dark: "#8A8A8A"}
	danger     = lipgloss.AdaptiveColor{Light: "#C0392B", Dark: "#FF6B6B"}
	selectedBg = lipgloss.AdaptiveColor{Light: "#E4E4E4", Dark: "#3A3A3A"}
	success    = lipgloss.AdaptiveColor{Light: "#1E8449", Dark: "#5FD38D"}
	warning    = lipgloss.AdaptiveColor{Light: "#B9770E", Dark: "#F5B041"}
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(accent).Padding(0, 1)
	infoStyle  = lipgloss.NewStyle().Foreground(muted)
	errStyle   = lipgloss.NewStyle().Foreground(danger)
)

// Model is the dashboard state.
type Model struct {
	fetch    Fetcher
	open     Opener
	repos    []string
	interval time.Duration
	now      func() time.Time

	table   table.Model
	spinner spinner.Model
	help    help.Model

	loading     bool
	fetched     []gh.PR // every pull request of the last refresh
	prs         []gh.PR // the ones shown: fetched narrowed down to base
	base        string  // only show pull requests into this branch; "" shows all
	err         error
	openErr     error
	lastUpdated time.Time
	nextAt      time.Time
	width       int
	height      int
}

// New returns a dashboard for repos that refreshes every interval.
func New(fetch Fetcher, open Opener, repos []string, interval time.Duration) Model {
	t := table.New(table.WithFocused(true))
	styles := table.DefaultStyles()
	styles.Header = styles.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(muted).
		BorderBottom(true).
		Bold(true)
	// Keep the terminal's own text colour and only tint the background.
	styles.Selected = lipgloss.NewStyle().Background(selectedBg)
	t.SetStyles(styles)

	s := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(lipgloss.NewStyle().Foreground(accent)))

	m := Model{
		fetch:    fetch,
		open:     open,
		repos:    repos,
		interval: interval,
		now:      time.Now,
		table:    t,
		spinner:  s,
		help:     help.New(),
		loading:  true, // Init starts the first fetch
	}
	m.layout()
	return m
}

// Init starts the first fetch and the countdown clock.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.fetchCmd(), clock())
}

func clock() tea.Cmd {
	return tea.Tick(clockInterval, func(t time.Time) tea.Msg { return clockMsg(t) })
}

// startFetch marks the model as loading and returns the fetch command.
func (m *Model) startFetch() tea.Cmd {
	m.loading = true
	return m.fetchCmd()
}

func (m Model) fetchCmd() tea.Cmd {
	fetch, repos, now := m.fetch, m.repos, m.now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		prs, err := fetch(ctx, repos)
		return resultMsg{prs: prs, err: err, at: now()}
	}
}

func (m Model) openCmd(pr gh.PR) tea.Cmd {
	open := m.open
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
		defer cancel()
		return openMsg{err: open(ctx, pr.Repo, pr.Number)}
	}
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, keys.Open):
			cursor := m.table.Cursor()
			if cursor < 0 || cursor >= len(m.prs) {
				return m, nil
			}
			m.openErr = nil
			m.layout()
			return m, m.openCmd(m.prs[cursor])
		case key.Matches(msg, keys.Base):
			m.base = nextBase(m.base, m.fetched)
			m.applyFilter()
			m.table.SetCursor(0)
			return m, nil
		case key.Matches(msg, keys.Refresh):
			if m.loading {
				return m, nil
			}
			return m, m.startFetch()
		}

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.Width = msg.Width
		m.layout()
		return m, nil

	case clockMsg:
		m.table.SetRows(m.rows()) // keep the "Updated" ages current
		if !m.loading && !m.nextAt.IsZero() && !m.now().Before(m.nextAt) {
			return m, tea.Batch(clock(), m.startFetch())
		}
		return m, clock()

	case resultMsg:
		m.loading = false
		m.err = msg.err
		m.openErr = nil
		m.fetched = msg.prs
		m.lastUpdated = msg.at
		m.nextAt = msg.at.Add(m.interval)
		m.applyFilter()
		return m, nil

	case openMsg:
		m.openErr = msg.err
		m.layout()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

// WithBase returns the model showing only pull requests into the given base
// branch, or all of them for "".
func (m Model) WithBase(base string) Model {
	m.base = base
	m.applyFilter()
	return m
}

// applyFilter narrows the fetched pull requests down to the base branch and
// refreshes the table.
func (m *Model) applyFilter() {
	m.prs = m.prs[:0:0]
	for _, pr := range m.fetched {
		if m.base == "" || pr.Base == m.base {
			m.prs = append(m.prs, pr)
		}
	}
	m.table.SetRows(m.rows())
	if m.table.Cursor() < 0 {
		// SetRows leaves the cursor at -1 after the table was empty.
		m.table.SetCursor(0)
	}
	m.layout()
}

// nextBase cycles the base branch filter: all, then each base branch that
// occurs in prs in alphabetical order, then all again. A current value that
// occurs nowhere (say a mistyped flag) is followed by all.
func nextBase(current string, prs []gh.PR) string {
	seen := map[string]bool{}
	var bases []string
	for _, pr := range prs {
		if pr.Base != "" && !seen[pr.Base] {
			seen[pr.Base] = true
			bases = append(bases, pr.Base)
		}
	}
	slices.Sort(bases)

	cycle := append([]string{""}, bases...)
	i := slices.Index(cycle, current)
	if i < 0 {
		return ""
	}
	return cycle[(i+1)%len(cycle)]
}

func (m Model) rows() []table.Row {
	now := m.now()
	rows := make([]table.Row, len(m.prs))
	for i, pr := range m.prs {
		draft := ""
		if pr.Draft {
			draft = "draft"
		}
		rows[i] = table.Row{
			pr.Repo,
			fmt.Sprintf("#%d", pr.Number),
			pr.Title,
			pr.Author,
			age(now.Sub(pr.UpdatedAt)),
			draft,
			conflictCell(pr.Conflicting),
			ciCell(pr.CI),
		}
	}
	return rows
}

// layout sizes the table columns and height to the terminal.
func (m *Model) layout() {
	repoWidth := len("Repo")
	for _, r := range m.repos {
		repoWidth = max(repoWidth, len(r))
	}
	repoWidth = min(repoWidth, maxRepoWidth)

	author := authorWidth
	fixed := repoWidth + numberWidth + author + updatedWidth + draftWidth + conflictWidth + ciWidth
	const columns = 8
	titleWidth := m.width - fixed - columns*cellPadding
	if deficit := minTitle - titleWidth; deficit > 0 {
		titleWidth = minTitle
		shrink := func(w *int, floor int) {
			take := min(deficit, max(0, *w-floor))
			*w -= take
			deficit -= take
		}
		shrink(&author, shrunkAuthor)
		shrink(&repoWidth, shrunkRepo)
		shrink(&titleWidth, shrunkTitle)
	}

	m.table.SetColumns([]table.Column{
		{Title: "Repo", Width: repoWidth},
		{Title: "#", Width: numberWidth},
		{Title: "Title", Width: titleWidth},
		{Title: "Author", Width: author},
		{Title: "Updated", Width: updatedWidth},
		{Title: "Draft", Width: draftWidth},
		{Title: "Conflict", Width: conflictWidth},
		{Title: "CI", Width: ciWidth},
	})
	m.table.SetWidth(m.width)
	m.table.SetHeight(max(1, m.height-chromeLines-len(m.errLines())))
}

// ciLook is how a CI state is shown: a symbol and a word, so it stays readable
// without colour, and the style the label is coloured with.
type ciLook struct {
	label string
	style lipgloss.Style
}

var ciLooks = map[gh.CIState]ciLook{
	gh.CISuccess: {"✓ success", lipgloss.NewStyle().Foreground(success)},
	gh.CIFailure: {"✗ failure", lipgloss.NewStyle().Foreground(danger)},
	gh.CIRunning: {"● running", lipgloss.NewStyle().Foreground(warning)},
	gh.CIPending: {"○ pending", infoStyle},
	gh.CINone:    {"– none", infoStyle},
}

// ciCell is the plain-text table cell of a CI state. It must not contain
// escape sequences: the table truncates cells with a width function that
// counts them as visible characters. See colorStatus.
func ciCell(s gh.CIState) string {
	if look, ok := ciLooks[s]; ok {
		return look.label
	}
	return ciLooks[gh.CINone].label
}

// conflictLabel marks a pull request whose branch has merge conflicts.
const conflictLabel = "✗ conflict"

// conflictCell is the plain-text table cell for merge conflicts.
func conflictCell(conflicting bool) string {
	if conflicting {
		return conflictLabel
	}
	return ""
}

// statusTail matches the end of a rendered table line: the optional conflict
// label and the CI label, which are the last two columns, followed by padding
// and the reset sequence of a styled (selected) row. Anchoring to the end
// keeps a title that happens to contain such text from being coloured.
var statusTail = regexp.MustCompile(
	`(?:(` + conflictLabel + `)( +))?` +
		`(✓ success|✗ failure|● running|○ pending|– none)` +
		`((?: |\x1b\[[0-9;]*m)*)$`)

// paint colours a label with a foreground colour only, so that the background
// of a selected row stays intact behind it.
func paint(style lipgloss.Style, label string) string {
	return strings.ReplaceAll(style.Render(label), "\x1b[0m", "\x1b[39m")
}

// colorStatus colours the conflict and CI labels in the rendered table. They
// are coloured after rendering because the table truncates cells with a width
// function that counts escape sequences as visible characters.
func colorStatus(rendered string) string {
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		lines[i] = statusTail.ReplaceAllStringFunc(line, func(match string) string {
			sub := statusTail.FindStringSubmatch(match)
			conflict, gap, ci, trail := sub[1], sub[2], sub[3], sub[4]

			out := ""
			if conflict != "" {
				out = paint(lipgloss.NewStyle().Foreground(danger), conflict) + gap
			}
			for _, look := range ciLooks {
				if look.label == ci {
					return out + paint(look.style, ci) + trail
				}
			}
			return match
		})
	}
	return strings.Join(lines, "\n")
}

func (m Model) errLines() []string {
	var lines []string
	for _, err := range []error{m.err, m.openErr} {
		if err != nil {
			lines = append(lines, strings.Split(err.Error(), "\n")...)
		}
	}
	return lines
}

// View renders the dashboard.
func (m Model) View() string {
	var b strings.Builder

	title := titleStyle.Render("PR Dashboard")
	scope := infoStyle.Render(m.scope())
	b.WriteString(title + scope + "\n")

	b.WriteString(colorStatus(m.table.View()) + "\n")

	for _, line := range m.errLines() {
		b.WriteString(errStyle.Render("✗ "+line) + "\n")
	}

	b.WriteString(m.status() + "\n")
	b.WriteString(m.help.View(keys))
	return b.String()
}

func (m Model) scope() string {
	if m.base == "" {
		return fmt.Sprintf(" %d open across %d repos", len(m.prs), len(m.repos))
	}
	return fmt.Sprintf(" %d of %d open across %d repos · base: %s",
		len(m.prs), len(m.fetched), len(m.repos), m.base)
}

func (m Model) status() string {
	if m.loading {
		return m.spinner.View() + infoStyle.Render(" Refreshing…")
	}
	if m.lastUpdated.IsZero() {
		return ""
	}
	next := max(0, m.nextAt.Sub(m.now()).Round(time.Second))
	return infoStyle.Render(fmt.Sprintf("Updated %s · next refresh in %s",
		m.lastUpdated.Format("15:04:05"), next))
}

// age renders a duration compactly, e.g. "45s", "12m", "3h", "5d".
func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
