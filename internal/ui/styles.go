package ui

import (
	"io"
	"os"
	"sync"

	"github.com/charmbracelet/x/term"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

const shareTitle = "yz  /  share"

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#5CE1E6"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#8BD49C"))
	mutedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8A8F98"))
	warningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E9B866"))

	linkOnce sync.Once
)

func WriteShareHelp(w io.Writer, usage string) {
	width, height := 80, 24
	if f, ok := w.(*os.File); ok {
		if cols, rows, err := term.GetSize(f.Fd()); err == nil {
			width, height = cols, rows
		}
	}
	_, _ = lipgloss.Fprintln(w, terminalHeader(shareTitle,
		"Share files as links, straight from your terminal.",
		mascotIdle, 0, width, height))
	_, _ = lipgloss.Fprintln(w)
	_, _ = lipgloss.Fprintln(w, styled(mutedStyle, usage))
}

func WriteShareError(w io.Writer, msg string) {
	writeShare(w, errorStyle, SafeText(msg))
}

func writeShare(w io.Writer, body lipgloss.Style, text string) {
	linkComponents()
	_, _ = lipgloss.Fprintln(w, titleStyle.Render(shareTitle))
	_, _ = lipgloss.Fprintln(w)
	_, _ = lipgloss.Fprintln(w, body.Render(text))
}

func linkComponents() {
	linkOnce.Do(func() {
		_ = list.New(nil, list.NewDefaultDelegate(), 1, 1)
		_ = textinput.New()
		_ = spinner.New()
		_ = progress.New()
		_ = help.New()
	})
}
