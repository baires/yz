package ui

import (
	"context"
	"errors"
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

func deleteModel(del DeleteFunc) shareListModel {
	m := newShareListModel([]history.Entry{
		{URL: "https://x/s/AAAAAAAAAAAAAAAA/", File: "dist", Kind: history.KindSite, CreatedAt: time.Unix(2, 0)},
		{URL: "https://x/BBBBBBBBBBBBBBBB/a.png", File: "a.png", CreatedAt: time.Unix(1, 0)},
	})
	m.del = del
	return m
}

func press(m shareListModel, key string) (shareListModel, tea.Cmd) {
	var msg tea.KeyPressMsg
	if key == "delete" {
		msg = tea.KeyPressMsg{Code: tea.KeyDelete}
	} else {
		msg = tea.KeyPressMsg{Code: rune(key[0]), Text: key}
	}
	next, cmd := m.Update(msg)
	return next.(shareListModel), cmd
}

func TestShareListDeleteConfirmed(t *testing.T) {
	var got []history.Entry
	m := deleteModel(func(_ context.Context, e history.Entry) error { got = append(got, e); return nil })
	m, cmd := press(m, "d")
	if cmd != nil || !m.confirming || !strings.Contains(m.View().Content, "site and all its files") {
		t.Fatalf("no confirmation prompt:\n%s", m.View().Content)
	}
	m, cmd = press(m, "y")
	if cmd == nil || !m.deleting {
		t.Fatal("confirm did not start delete")
	}
	next, _ := m.Update(cmd())
	m = next.(shareListModel)
	if len(got) != 1 || !got[0].IsSite() || len(m.entries) != 1 || m.entries[0].File != "a.png" {
		t.Fatalf("deleted %+v, left %+v", got, m.entries)
	}
	if !strings.Contains(m.View().Content, "deleted dist/") || strings.Contains(m.View().Content, "dist/ ") {
		t.Fatalf("view not refreshed:\n%s", m.View().Content)
	}
}

func TestShareListDeleteCancelledAndFailed(t *testing.T) {
	calls := 0
	m := deleteModel(func(context.Context, history.Entry) error { calls++; return errors.New("boom") })
	m, _ = press(m, "delete")
	m, cmd := press(m, "n")
	if cmd != nil || calls != 0 || len(m.entries) != 2 || !strings.Contains(m.notice, "cancelled") {
		t.Fatal("cancel deleted something")
	}
	m, _ = press(m, "d")
	m, cmd = press(m, "y")
	next, _ := m.Update(cmd())
	m = next.(shareListModel)
	if calls != 1 || len(m.entries) != 2 || !strings.Contains(m.notice, "delete failed: boom") {
		t.Fatalf("failure handling: %d %+v %q", calls, m.entries, m.notice)
	}
}
