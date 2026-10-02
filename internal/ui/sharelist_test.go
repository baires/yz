package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/baires/yz/internal/history"

	tea "charm.land/bubbletea/v2"
)

func TestShareListHeaderMascot(t *testing.T) {
	m := newShareListModel([]history.Entry{{
		URL:       "https://files.example.com/a",
		File:      "a.png",
		CreatedAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC),
	}})
	if !strings.Contains(m.View().Content, "▄▀▀▄") {
		t.Fatalf("default view missing mascot:\n%s", m.View().Content)
	}
	if !strings.Contains(m.View().Content, "yz  /  shares") {
		t.Fatalf("default view missing title:\n%s", m.View().Content)
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	small := updated.(shareListModel)
	if strings.Contains(small.View().Content, "▄▀▀▄") {
		t.Fatalf("mascot shown on a small terminal:\n%s", small.View().Content)
	}
}
