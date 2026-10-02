package ui

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/baires/yz/internal/desktop"
	"github.com/baires/yz/internal/history"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/charmbracelet/x/ansi"
)

const sharesTitle = "yz  /  shares"

// RunShareList shows past shares in a scrollable interactive table. Enter
// copies the selected link to the clipboard; q or esc quits.
func RunShareList(input io.Reader, output io.Writer, entries []history.Entry) error {
	program := tea.NewProgram(
		newShareListModel(entries),
		tea.WithInput(input),
		tea.WithOutput(output),
	)
	_, err := program.Run()
	return err
}

type shareListModel struct {
	table       table.Model
	urls        []string
	width       int
	height      int
	mascotFrame int
	notice      string
}

func newShareListModel(entries []history.Entry) shareListModel {
	cols := []table.Column{
		{Title: "Shared", Width: 16},
		{Title: "Size", Width: 8},
		{Title: "File", Width: 28},
		{Title: "Expires", Width: 16},
	}
	rows := make([]table.Row, len(entries))
	urls := make([]string, len(entries))
	now := time.Now()
	for i, e := range entries {
		rows[i] = table.Row{
			history.FormatTime(e.CreatedAt),
			HumanSize(e.Size),
			SafeText(e.File),
			expiryLabel(e, now),
		}
		urls[i] = e.URL
	}
	// WithHeight counts the header row, so add one to show height data rows.
	height := min(len(rows), 10) + 1
	t := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(height),
		table.WithWidth(80),
	)
	styles := table.DefaultStyles()
	styles.Header = styles.Header.Bold(true).Foreground(lipgloss.Color("#5CE1E6"))
	styles.Selected = styles.Selected.Bold(true).Foreground(lipgloss.Color("#5CE1E6"))
	t.SetStyles(styles)
	return shareListModel{table: t, urls: urls, width: 80, height: 24}
}

func expiryLabel(e history.Entry, now time.Time) string {
	if e.ExpiresAt == nil {
		return "never"
	}
	if e.Expired(now) {
		return "expired"
	}
	return history.FormatTime(*e.ExpiresAt)
}

func (m shareListModel) Init() tea.Cmd {
	return scheduleMascot(800 * time.Millisecond)
}

func (m shareListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.table.SetWidth(msg.Width)
		return m, nil
	case mascotTick:
		m.mascotFrame++
		return m, scheduleMascot(800 * time.Millisecond)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "enter":
			if row := m.table.Cursor(); row < len(m.urls) {
				if err := desktop.CopyURL(context.Background(), m.urls[row]); err != nil {
					m.notice = "copy failed: " + SafeText(err.Error())
				} else {
					m.notice = "link copied to clipboard"
				}
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m shareListModel) View() tea.View {
	selected := ""
	if row := m.table.Cursor(); row < len(m.urls) {
		selected = ansi.Truncate(m.urls[row], m.width-4, "…")
	}
	footer := mutedStyle.Render("↑/↓ move · enter copy link · q quit")
	if m.notice != "" {
		footer = successStyle.Render(m.notice) + "  " + footer
	}
	return tea.NewView(fmt.Sprintf("%s\n\n%s\n\n%s\n\n%s\n",
		m.header(), m.table.View(), mutedStyle.Render(selected), footer))
}

func (m shareListModel) header() string {
	subtitle := "Previous shares"
	if m.width < 30 || m.height < 8 {
		subtitle = ""
	}
	return terminalHeader(sharesTitle, subtitle, mascotIdle, m.mascotFrame, m.width, m.height)
}

// HumanSize formats a byte count as 1.2 MB style text.
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
