package ui

import (
	"github.com/baires/yz/internal/setup"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
)

type choiceItem struct {
	title string
	desc  string
	value string
}

func (c choiceItem) Title() string       { return c.title }
func (c choiceItem) Description() string { return c.desc }
func (c choiceItem) FilterValue() string { return c.title + " " + c.desc }

func newChoiceList(choices []setup.Choice, width, height int) list.Model {
	items := make([]list.Item, 0, len(choices))
	described := false
	for _, choice := range choices {
		if choice.Description != "" {
			described = true
		}
		items = append(items, choiceItem{
			title: SafeText(choice.Title),
			desc:  SafeText(choice.Description),
			value: choice.Value,
		})
	}
	delegate := list.NewDefaultDelegate()
	if colorEnabled() {
		delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
			Foreground(lipgloss.Color("#5CE1E6")).
			BorderForeground(lipgloss.Color("#5CE1E6")).Bold(true)
		delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
			Foreground(lipgloss.Color("#8A8F98")).
			BorderForeground(lipgloss.Color("#5CE1E6"))
	}
	delegate.ShowDescription = described
	if described {
		delegate.SetHeight(2)
	}
	chooser := list.New(items, delegate, width, height)
	chooser.Title = ""
	chooser.FilterInput.Placeholder = "Search by name…"
	chooser.SetShowTitle(false)
	chooser.SetShowStatusBar(false)
	chooser.SetShowHelp(false)
	chooser.DisableQuitKeybindings()
	return chooser
}
