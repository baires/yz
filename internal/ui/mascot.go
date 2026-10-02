package ui

import (
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type mascotState int

const (
	mascotIdle mascotState = iota
	mascotWorking
	mascotSuccess
	mascotError
)

type mascotTick struct{}

func colorEnabled() bool {
	_, disabled := os.LookupEnv("NO_COLOR")
	return !disabled && !dumb()
}

func mascotVisible(width, height int) bool {
	return width >= 60 && height >= 24
}

func mascot(state mascotState, frame int) string {
	if !colorEnabled() {
		frame = 0
	}
	accent := titleStyle
	eyes, signal, feet := "● ●", "         ", "   ▀  ▀  "
	switch state {
	case mascotIdle:
		if frame%4 == 2 {
			eyes = "─ ─"
		}
		if frame%4 == 3 {
			feet = "  ▄▀  ▀▄ "
		}
	case mascotWorking:
		signal = []string{"    ▲    ", "   ∷ ∷   ", "   ↑ ↑   ", "    ·    "}[frame%4]
	case mascotSuccess:
		accent = successStyle
		eyes, signal = "★ ★", "    ✓    "
	case mascotError:
		accent = errorStyle
		eyes, signal = "× ×", "    !    "
	}
	return strings.Join([]string{
		styled(accent, signal),
		styled(accent, "   ▄▀▀▄  "),
		"  █ " + styled(accent, eyes) + " █",
		"  █▄▄▄▄█ ",
		styled(accent, feet),
	}, "\n")
}

func terminalHeader(title, subtitle string, state mascotState, frame, width, height int) string {
	text := styled(titleStyle, title)
	if subtitle != "" {
		text += "\n" + styled(mutedStyle, wrapNotice(subtitle, width))
	}
	if !mascotVisible(width, height) {
		return text
	}
	text = styled(titleStyle, title)
	if subtitle != "" {
		text += "\n" + styled(mutedStyle, wrapNotice(subtitle, width-13))
	}
	return lipgloss.JoinHorizontal(lipgloss.Center, mascot(state, frame), "   ", text)
}

func scheduleMascot(delay time.Duration) tea.Cmd {
	if !colorEnabled() {
		return nil
	}
	return tea.Tick(delay, func(time.Time) tea.Msg { return mascotTick{} })
}

func (m setupModel) mascotTick() tea.Cmd {
	if m.summary != "" {
		return nil
	}
	delay := 800 * time.Millisecond
	if m.busy && mascotVisible(m.width, m.height) {
		delay = 120 * time.Millisecond
	}
	return scheduleMascot(delay)
}
