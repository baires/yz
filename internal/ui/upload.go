package ui

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/baires/yz/internal/desktop"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type uploadCounters struct {
	sent  atomic.Int64
	total atomic.Int64
	ready atomic.Bool
}

func (c *uploadCounters) set(sent, total int64) {
	for {
		cur := c.sent.Load()
		if sent < cur {
			return
		}
		if c.sent.CompareAndSwap(cur, sent) {
			c.total.Store(total)
			c.ready.Store(true)
			return
		}
	}
}

type uploadDone struct {
	err     error
	url     string
	copyErr error
}

type browserDone struct{ err error }

type uploadModel struct {
	cancel          context.CancelFunc
	work            tea.Cmd
	counters        *uploadCounters
	name            string
	bucket          string
	spinner         spinner.Model
	bar             progress.Model
	width           int
	height          int
	started         time.Time
	ticks           int
	sent            int64
	total           int64
	errText         string
	finished        bool
	ready           bool
	hold            bool
	url             string
	clipboardNotice string
	copyFailed      bool
	opening         bool
	browserNotice   string
	browserFailed   bool
	ctx             context.Context
}

func RunUpload(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	path, bucket string,
	hold bool,
	work func(context.Context, func(int64, int64)) (string, error),
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	counters := &uploadCounters{}
	finished := make(chan struct{})
	var outcome struct {
		mu  sync.Mutex
		err error
	}
	model := newUploadModel(cancel, counters, path, bucket)
	model.hold = hold
	model.ctx = ctx
	model.work = func() tea.Msg {
		defer close(finished)
		target, err := work(ctx, counters.set)
		outcome.mu.Lock()
		outcome.err = err
		outcome.mu.Unlock()
		result := uploadDone{err: err, url: target}
		if err == nil {
			result.copyErr = desktop.CopyURL(ctx, target)
		}
		return result
	}
	program := tea.NewProgram(
		model,
		tea.WithInput(input),
		tea.WithOutput(output),
		tea.WithContext(ctx),
		tea.WithoutSignalHandler(),
	)
	_, err := program.Run()
	cancel()
	<-finished
	outcome.mu.Lock()
	runErr := outcome.err
	outcome.mu.Unlock()
	if errors.Is(runErr, context.Canceled) || errors.Is(err, tea.ErrInterrupted) || errors.Is(err, tea.ErrProgramKilled) {
		if runErr != nil {
			return runErr
		}
		return context.Canceled
	}
	return runErr
}

func newUploadModel(cancel context.CancelFunc, counters *uploadCounters, path, bucket string) uploadModel {
	spin := spinner.New()
	spin.Spinner = spinner.MiniDot
	bar := progress.New()
	if colorEnabled() {
		spin.Style = titleStyle
		bar = progress.New(progress.WithColors(lipgloss.Color("#5CE1E6"), lipgloss.Color("#8BD49C")))
	}
	return uploadModel{
		cancel:   cancel,
		counters: counters,
		name:     SafeText(filepath.Base(path)),
		bucket:   SafeText(bucket),
		spinner:  spin,
		bar:      bar,
		width:    80,
		height:   24,
		started:  time.Now(),
	}
}

func (m uploadModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.sample(), m.work)
}

func (m uploadModel) sample() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg {
		return uploadSample{}
	})
}

type uploadSample struct{}

func (m uploadModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.width > 4 {
			m.bar.SetWidth(m.width - 4)
		}
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.finished {
			return m, nil
		}
		return m, cmd
	case uploadSample:
		m.sampleCounters()
		m.ticks++
		if m.finished {
			return m, nil
		}
		return m, m.sample()
	case uploadDone:
		m.sampleCounters()
		m.finished = true
		if errors.Is(msg.err, context.Canceled) {
			m.errText = "Upload cancelled"
		} else if msg.err != nil {
			m.errText = SafeText(msg.err.Error())
		}
		if msg.err == nil {
			m.url = msg.url
			m.clipboardNotice = desktop.ClipboardNotice(msg.copyErr)
			m.copyFailed = msg.copyErr != nil
			if m.hold {
				return m, nil
			}
		}
		return m, tea.Quit
	case browserDone:
		m.opening = false
		m.browserFailed = msg.err != nil
		m.browserNotice = "✓ Opened in browser"
		if msg.err != nil {
			m.browserNotice = "Couldn't open browser · use the printed link"
		}
		return m, nil
	case tea.KeyPressMsg:
		if m.finished && m.errText == "" {
			switch msg.String() {
			case "enter", "esc", "q", "ctrl+c":
				return m, tea.Quit
			case "o":
				if m.opening || m.url == "" {
					return m, nil
				}
				m.opening = true
				m.browserNotice = "Opening browser…"
				return m, func() tea.Msg { return browserDone{err: desktop.OpenURL(m.ctx, m.url)} }
			}
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			m.cancel()
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *uploadModel) sampleCounters() {
	m.sent = m.counters.sent.Load()
	m.total = m.counters.total.Load()
	m.ready = m.counters.ready.Load()
}

func (m uploadModel) View() tea.View {
	var b strings.Builder
	state := mascotWorking
	if m.finished {
		state = mascotSuccess
		if m.errText != "" {
			state = mascotError
		}
	}
	b.WriteString(terminalHeader("yz  /  share", m.name, state, m.ticks/3, m.width, m.height))
	b.WriteString("\n")
	b.WriteString(styled(mutedStyle, "Bucket: "))
	b.WriteString(styled(titleStyle, m.bucket))
	b.WriteString("\n\n")
	if m.finished {
		if m.errText != "" {
			b.WriteString(styled(errorStyle, wrapNotice(m.errText, m.width)))
		} else {
			b.WriteString(styled(successStyle, "✓ Uploaded · your link is ready"))
			b.WriteString("\n")
			b.WriteString(m.counts() + " bytes\n")
			if m.hold {
				b.WriteString("\n" + styled(titleStyle, wrapNotice(SafeText(m.url), m.width)) + "\n")
			} else {
				b.WriteString(styled(mutedStyle, "Share the link printed below.") + "\n")
			}
			style := successStyle
			if m.copyFailed {
				style = warningStyle
			}
			b.WriteString(styled(style, wrapNotice(m.clipboardNotice, m.width)) + "\n")
			if m.browserNotice != "" {
				style = mutedStyle
				if m.browserFailed {
					style = warningStyle
				}
				b.WriteString(styled(style, wrapNotice(m.browserNotice, m.width)) + "\n")
			}
			if m.hold {
				b.WriteString("\n" + styled(mutedStyle, wrapNotice("o open in browser · enter finish", m.width)))
			}
		}
		b.WriteString("\n")
		return tea.NewView(b.String())
	}
	b.WriteString(m.spinner.View())
	b.WriteString(" ")
	b.WriteString(m.counts())
	b.WriteString("\n")
	if m.total > 0 {
		b.WriteString(m.bar.ViewAs(float64(m.sent) / float64(m.total)))
		b.WriteString("\n")
	}
	if m.ready && m.sent == m.total {
		b.WriteString("Confirming upload\n")
	} else if !m.ready {
		b.WriteString("Preparing upload\n")
	} else {
		b.WriteString("Uploading · your link will be ready shortly\n")
	}
	if m.ticks > 0 && m.total > 0 && m.sent > 0 {
		elapsed := time.Since(m.started)
		if speed := formatSpeed(m.sent, elapsed); speed != "" {
			b.WriteString(speed)
			b.WriteString("\n")
		}
	}
	if m.errText != "" {
		b.WriteString(styled(errorStyle, m.errText))
		b.WriteString("\n")
	}
	if m.tiny() {
		b.WriteString("resize the terminal to continue\n")
	}
	b.WriteString(styled(mutedStyle, "ctrl+c cancel"))
	b.WriteString("\n")
	return tea.NewView(b.String())
}

func (m uploadModel) counts() string {
	return itoa(m.sent) + "/" + itoa(m.total)
}

func (m uploadModel) tiny() bool {
	return m.width < 30 || m.height < 8
}

func formatSpeed(sent int64, elapsed time.Duration) string {
	seconds := elapsed.Seconds()
	if seconds <= 0 {
		return ""
	}
	return itoa(int64(float64(sent)/seconds)) + " B/s"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
