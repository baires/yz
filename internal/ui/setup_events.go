package ui

import (
	"context"

	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/setup"

	tea "charm.land/bubbletea/v2"
)

type choiceResult struct {
	value string
	err   error
}

type statusMsg struct {
	step setup.Step
	text string
}

type noticeMsg struct {
	text string
}

type chooseMsg struct {
	prompt  string
	choices []setup.Choice
	reply   chan choiceResult
}

type textMsg struct {
	req   setup.TextRequest
	reply chan choiceResult
}

type confirmMsg struct {
	reply chan choiceResult
}

type retryMsg struct {
	text  string
	reply chan choiceResult
}

type doneMsg struct {
	cfg *config.Config
	err error
}

type adapter struct {
	events chan tea.Msg
	ctx    context.Context
}

func (a *adapter) Status(step setup.Step, text string) {
	a.send(statusMsg{step: step, text: text})
}

func (a *adapter) Notice(text string) {
	a.send(noticeMsg{text: text})
}

func (a *adapter) Write(p []byte) (int, error) {
	a.send(noticeMsg{text: string(p)})
	return len(p), nil
}

func (a *adapter) Choose(ctx context.Context, prompt string, choices []setup.Choice) (string, error) {
	reply := make(chan choiceResult, 1)
	a.send(chooseMsg{prompt: prompt, choices: choices, reply: reply})
	result, err := a.wait(ctx, reply)
	return result.value, err
}

func (a *adapter) Text(ctx context.Context, req setup.TextRequest) (string, error) {
	reply := make(chan choiceResult, 1)
	a.send(textMsg{req: req, reply: reply})
	result, err := a.wait(ctx, reply)
	return result.value, err
}

func (a *adapter) ConfirmPublic(ctx context.Context) (bool, error) {
	reply := make(chan choiceResult, 1)
	a.send(confirmMsg{reply: reply})
	result, err := a.wait(ctx, reply)
	if err != nil {
		return false, err
	}
	return result.value == "public", nil
}

func (a *adapter) Retry(ctx context.Context, msg string) (bool, error) {
	reply := make(chan choiceResult, 1)
	a.send(retryMsg{text: msg, reply: reply})
	result, err := a.wait(ctx, reply)
	if err != nil {
		return false, err
	}
	return result.value == "yes", nil
}

func (a *adapter) send(msg tea.Msg) {
	select {
	case a.events <- msg:
	case <-a.ctx.Done():
	}
}

func (a *adapter) wait(ctx context.Context, reply <-chan choiceResult) (choiceResult, error) {
	select {
	case <-ctx.Done():
		return choiceResult{}, ctx.Err()
	case <-a.ctx.Done():
		return choiceResult{}, a.ctx.Err()
	case result := <-reply:
		if result.err != nil {
			return choiceResult{}, result.err
		}
		return result, nil
	}
}

func replyOnce(ch chan choiceResult, result choiceResult) {
	select {
	case ch <- result:
	default:
	}
}
