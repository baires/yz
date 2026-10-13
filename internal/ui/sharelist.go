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

// DeleteFunc removes a share's remote objects and its history entry.
type DeleteFunc func(context.Context, history.Entry) error

// RunShareList shows past shares in a scrollable interactive table. Enter
// copies the selected link to the clipboard; d deletes after confirmation;
// q or esc quits.
func RunShareList(input io.Reader, output io.Writer, entries []history.Entry, del DeleteFunc) error {
	m := newShareListModel(entries)
	m.del = del
	program := tea.NewProgram(
		m,
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
	failed      bool
	entries     []history.Entry
	del         DeleteFunc
	confirming  bool
	deleting    bool
}

type deleteDone struct {
	entry history.Entry
	err   error
}

func newShareListModel(entries []history.Entry) shareListModel {
	m := shareListModel{width: 80, height: 24}
	m.table = buildTable(entries)
	m.setEntries(entries)
	return m
}

func (m *shareListModel) setEntries(entries []history.Entry) {
	m.entries = entries
	m.urls = make([]string, len(entries))
	for i, e := range entries {
		m.urls[i] = e.URL
	}
}

func shareRows(entries []history.Entry) []table.Row {
	rows := make([]table.Row, len(entries))
	now := time.Now()
	for i, e := range entries {
		rows[i] = table.Row{
			history.FormatTime(e.CreatedAt),
			HumanSize(e.Size),
			SafeText(e.Label()),
			expiryLabel(e, now),
		}
	}
	return rows
}

func buildTable(entries []history.Entry) table.Model {
	cols := []table.Column{
		{Title: "Shared", Width: 16},
		{Title: "Size", Width: 8},
		{Title: "File", Width: 28},
		{Title: "Expires", Width: 16},
	}
	rows := shareRows(entries)
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
	return t
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
	case deleteDone:
		m.deleting = false
		if msg.err != nil {
			m.failed = true
			m.notice = "delete failed: " + SafeText(msg.err.Error())
			return m, nil
		}
		kept := make([]history.Entry, 0, len(m.entries))
		for _, e := range m.entries {
			if e.URL == msg.entry.URL && e.CreatedAt.Equal(msg.entry.CreatedAt) {
				continue
			}
			kept = append(kept, e)
		}
		m.setEntries(kept)
		m.table.SetRows(shareRows(kept))
		if c := m.table.Cursor(); c >= len(kept) && len(kept) > 0 {
			m.table.SetCursor(len(kept) - 1)
		}
		m.failed = false
		m.notice = "deleted " + SafeText(msg.entry.Label())
		if len(kept) == 0 {
			return m, tea.Quit
		}
		return m, nil
	case tea.KeyPressMsg:
		if m.deleting {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.confirming {
			m.confirming = false
			row := m.table.Cursor()
			if msg.String() != "y" || row >= len(m.entries) {
				m.failed, m.notice = false, "delete cancelled"
				return m, nil
			}
			entry := m.entries[row]
			m.deleting = true
			m.failed, m.notice = false, "deleting "+SafeText(entry.Label())+"…"
			del := m.del
			return m, func() tea.Msg {
				return deleteDone{entry: entry, err: del(context.Background(), entry)}
			}
		}
		switch msg.String() {
		case "d", "delete":
			if row := m.table.Cursor(); row < len(m.entries) && m.del != nil {
				m.confirming = true
				e := m.entries[row]
				what := "file"
				if e.IsSite() {
					what = "site and all its files"
				}
				m.failed, m.notice = true, "delete "+what+" "+SafeText(e.Label())+" from R2? y to confirm, any other key cancels"
			}
			return m, nil
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "enter":
			if row := m.table.Cursor(); row < len(m.urls) {
				m.failed = false
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
	footer := mutedStyle.Render("↑/↓ move · enter copy link · d delete · q quit")
	if m.notice != "" {
		style := successStyle
		if m.failed {
			style = errorStyle
		}
		footer = style.Render(m.notice) + "  " + footer
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
