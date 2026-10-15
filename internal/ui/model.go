// Package ui implements the Bubble Tea dashboard of open pull requests.
package ui

import (
	"context"
	"fmt"
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
	numberWidth  = 6
	authorWidth  = 18
	updatedWidth = 8
	draftWidth   = 5
	maxRepoWidth = 30
	minTitle     = 20
	cellPadding  = 2 // table cells are padded one column on each side
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
	Refresh key.Binding
	Quit    key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Open, k.Refresh, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding { return [][]key.Binding{k.ShortHelp()} }

var keys = keyMap{
	Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	Open:    key.NewBinding(key.WithKeys("enter", "o"), key.WithHelp("enter/o", "open")),
	Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}

var (
	accent     = lipgloss.AdaptiveColor{Light: "#5A4FCF", Dark: "#9D8CFF"}
	muted      = lipgloss.AdaptiveColor{Light: "#6B6B6B", Dark: "#8A8A8A"}
	danger     = lipgloss.AdaptiveColor{Light: "#C0392B", Dark: "#FF6B6B"}
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
	prs         []gh.PR
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
	styles.Selected = styles.Selected.
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(accent).
		Bold(false)
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
		m.prs = msg.prs
		m.lastUpdated = msg.at
		m.nextAt = msg.at.Add(m.interval)
		m.table.SetRows(m.rows())
		m.layout()
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

	fixed := repoWidth + numberWidth + authorWidth + updatedWidth + draftWidth
	const columns = 6
	titleWidth := max(minTitle, m.width-fixed-columns*cellPadding)

	m.table.SetColumns([]table.Column{
		{Title: "Repo", Width: repoWidth},
		{Title: "#", Width: numberWidth},
		{Title: "Title", Width: titleWidth},
		{Title: "Author", Width: authorWidth},
		{Title: "Updated", Width: updatedWidth},
		{Title: "Draft", Width: draftWidth},
	})
	m.table.SetWidth(m.width)
	m.table.SetHeight(max(1, m.height-chromeLines-len(m.errLines())))
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
	scope := infoStyle.Render(fmt.Sprintf(" %d open across %d repos", len(m.prs), len(m.repos)))
	b.WriteString(title + scope + "\n")

	b.WriteString(m.table.View() + "\n")

	for _, line := range m.errLines() {
		b.WriteString(errStyle.Render("✗ "+line) + "\n")
	}

	b.WriteString(m.status() + "\n")
	b.WriteString(m.help.View(keys))
	return b.String()
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
