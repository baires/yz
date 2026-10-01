package ui

import (
	"strings"
	"testing"

	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/setup"

	tea "charm.land/bubbletea/v2"
)

func TestSetupCompletionIgnoresLateProgress(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	model := newSetupModel(func() {}, make(chan tea.Msg))
	model.width, model.height = 100, 40
	updated, _ := model.Update(doneMsg{cfg: &config.Config{Bucket: "photos"}})
	model = updated.(setupModel)
	for _, msg := range []tea.Msg{
		statusMsg{step: setup.StepSave, text: "Saving"},
		noticeMsg{text: "setup complete"},
		textMsg{req: setup.TextRequest{Label: "Access Key ID:"}},
	} {
		updated, _ = model.Update(msg)
		model = updated.(setupModel)
		if model.busy || model.pending != nil || model.activity != "" {
			t.Fatalf("late %T changed completed setup: busy=%v, pending=%v, activity=%q", msg, model.busy, model.pending != nil, model.activity)
		}
		if !strings.Contains(model.View().Content, "Setup complete") {
			t.Fatalf("late %T replaced completion summary", msg)
		}
	}
}
