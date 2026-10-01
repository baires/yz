package ui

import (
	"fmt"
	"unicode/utf8"

	"charm.land/bubbles/v2/list"
)

func (m setupModel) inputFeedback() string {
	if m.inputError != "" {
		return styled(errorStyle, wrapNotice(m.inputError, m.width))
	}
	value := m.input.Value()
	if value != "" && m.pending.validate != nil && m.pending.validate(value) == nil {
		return styled(successStyle, "✓ Ready · enter to continue")
	}
	if m.pending.text.Length > 0 {
		return styled(mutedStyle, fmt.Sprintf("%d/%d characters · input hidden",
			utf8.RuneCountInString(value), m.pending.text.Length))
	}
	return styled(mutedStyle, "enter to continue")
}

func (m setupModel) keyboardHelp() string {
	if m.summary != "" {
		return ""
	}
	if m.pending == nil || m.tiny() {
		return "ctrl+c cancel"
	}
	if m.pending.kind == promptText {
		return "enter continue · ctrl+u clear · ctrl+c cancel"
	}
	if m.list.FilterState() == list.Filtering {
		return "type to filter · enter apply · esc clear · ctrl+c cancel"
	}
	if m.list.FilterState() == list.FilterApplied {
		return "↑↓ move · / edit filter · esc clear · enter select · ctrl+c cancel"
	}
	if m.pending.kind == promptConfirm || m.pending.kind == promptRetry {
		return "↑↓ move · enter select · ctrl+c cancel"
	}
	return "↑↓ move · / filter · enter select · ctrl+c cancel"
}
