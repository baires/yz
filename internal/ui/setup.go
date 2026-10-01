package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/setup"

	"github.com/charmbracelet/x/ansi"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

type promptKind int

const (
	promptNone promptKind = iota
	promptChoose
	promptText
	promptConfirm
	promptRetry
)

type pendingPrompt struct {
	kind     promptKind
	prompt   string
	reply    chan choiceResult
	validate func(string) error
	text     setup.TextRequest
}

type setupModel struct {
	cancel      context.CancelFunc
	events      <-chan tea.Msg
	step        setup.Step
	activity    string
	width       int
	height      int
	spinner     spinner.Model
	list        list.Model
	input       textinput.Model
	pending     *pendingPrompt
	busy        bool
	inputError  string
	history     []string
	copy        string
	work        tea.Cmd
	result      *config.Config
	err         error
	summary     string
	mascotFrame int
}

func RunSetup(ctx context.Context, opts setup.Options, input io.Reader, output io.Writer) (*config.Config, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan tea.Msg, 32)
	ad := &adapter{events: events, ctx: ctx}
	finished := make(chan struct{})
	var outcome struct {
		mu  sync.Mutex
		cfg *config.Config
		err error
	}
	model := newSetupModel(cancel, events)
	model.work = func() tea.Msg {
		defer close(finished)
		cfg, err := setup.Run(ctx, opts, ad)
		outcome.mu.Lock()
		outcome.cfg, outcome.err = cfg, err
		outcome.mu.Unlock()
		return doneMsg{cfg: cfg, err: err}
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
	cfg, runErr := outcome.cfg, outcome.err
	outcome.mu.Unlock()
	if errors.Is(runErr, context.Canceled) || errors.Is(err, tea.ErrInterrupted) || errors.Is(err, tea.ErrProgramKilled) {
		if runErr != nil {
			return nil, runErr
		}
		return nil, context.Canceled
	}
	return cfg, runErr
}

func newSetupModel(cancel context.CancelFunc, events <-chan tea.Msg) setupModel {
	spin := spinner.New()
	spin.Spinner = spinner.MiniDot
	if colorEnabled() {
		spin.Style = titleStyle
	}
	field := textinput.New()
	field.CharLimit = 4096
	field.EchoCharacter = '*'
	return setupModel{
		cancel:  cancel,
		events:  events,
		step:    setup.StepLogin,
		width:   80,
		height:  24,
		spinner: spin,
		input:   field,
		busy:    true,
	}
}

func (m setupModel) Init() tea.Cmd {
	return tea.Batch(m.listen(), m.spinner.Tick, m.mascotTick(), m.work)
}

func (m setupModel) listen() tea.Cmd {
	events := m.events
	return func() tea.Msg {
		msg, ok := <-events
		if !ok {
			return nil
		}
		return msg
	}
}

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Progress already in flight must not overwrite the final frame before quit.
	if m.summary != "" {
		return m, nil
	}
	switch msg := msg.(type) {
	case mascotTick:
		m.mascotFrame++
		return m, m.mascotTick()
	case nil:
		return m, nil
	case statusMsg:
		wasBusy := m.busy
		if m.step != msg.step {
			m.copy = ""
		}
		m.step = msg.step
		m.activity = msg.text
		m.busy = m.pending == nil
		if m.busy && !wasBusy {
			return m, tea.Batch(m.listen(), m.spinner.Tick)
		}
		return m, m.listen()
	case noticeMsg:
		if m.step == setup.StepSave {
			return m, m.listen()
		}
		text := safeLines(msg.text)
		if strings.Contains(text, "http://") || strings.Contains(text, "https://") {
			m.copy = strings.TrimSpace(m.copy + "\n" + text)
		} else if text != "" {
			m.remember(text)
		}
		return m, m.listen()
	case chooseMsg:
		m.openList(promptChoose, msg.prompt, msg.choices, msg.reply)
		return m, m.listen()
	case confirmMsg:
		m.openList(promptConfirm, "Who should be able to open your links?", []setup.Choice{
			{Value: "private", Title: "Private", Description: "Recommended · signed links with an expiry"},
			{Value: "public", Title: "Public r2.dev", Description: "Enable public access · anyone with the URL can read"},
		}, msg.reply)
		return m, m.listen()
	case retryMsg:
		m.remember(SafeText(msg.text))
		m.openList(promptRetry, "Couldn't connect to R2. Try again?", []setup.Choice{
			{Value: "no", Title: "Cancel", Description: "Exit setup and try again later"},
			{Value: "yes", Title: "Retry", Description: "Enter your credentials and check again"},
		}, msg.reply)
		return m, m.listen()
	case tea.PasteMsg:
		if m.pending == nil || m.pending.kind != promptText {
			return m, nil
		}
		if pasteRejected(msg.Content) {
			m.remember("paste must be a single line within 4096 characters")
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.inputError = ""
		return m, cmd
	case textMsg:
		m.pending = &pendingPrompt{
			kind: promptText, prompt: msg.req.Label, reply: msg.reply,
			validate: msg.req.Validate, text: msg.req,
		}
		m.inputError = ""
		m.activity = msg.req.Label
		m.busy = false
		m.input.SetValue("")
		m.input.EchoMode = textinput.EchoNormal
		if msg.req.Secret {
			m.input.EchoMode = textinput.EchoPassword
		}
		m.input.Prompt = "> "
		m.input.Placeholder = msg.req.Placeholder
		m.resize()
		return m, tea.Batch(m.listen(), m.input.Focus())
	case doneMsg:
		m.busy = false
		m.pending = nil
		m.activity = ""
		m.history = nil
		m.copy = ""
		m.result = msg.cfg
		m.err = msg.err
		m.summary = completionSummary(msg.cfg, msg.err)
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.busy {
			return m, cmd
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	default:
		if m.pending != nil && (m.pending.kind == promptChoose || m.pending.kind == promptConfirm || m.pending.kind == promptRetry) {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}
		if m.pending != nil && m.pending.kind == promptText {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m setupModel) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.finish(choiceResult{err: context.Canceled})
		m.cancel()
		return m, tea.Quit
	}
	if m.pending != nil && m.pending.kind == promptText && msg.String() == "ctrl+u" {
		m.input.SetValue("")
		m.inputError = ""
		return m, nil
	}
	if m.pending != nil && (m.pending.kind == promptChoose || m.pending.kind == promptConfirm || m.pending.kind == promptRetry) && m.list.FilterState() == list.Filtering && msg.String() != "ctrl+c" {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	if m.pending == nil || m.tiny() {
		if m.pending != nil && (m.pending.kind == promptChoose || m.pending.kind == promptConfirm || m.pending.kind == promptRetry) {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}
		if m.pending != nil && m.pending.kind == promptText && msg.String() != "enter" {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	switch m.pending.kind {
	case promptChoose, promptConfirm, promptRetry:
		if m.list.FilterState() == list.Filtering {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}
		if msg.String() == "enter" {
			item, ok := m.list.SelectedItem().(choiceItem)
			if !ok {
				return m, nil
			}
			m.finish(choiceResult{value: item.value})
			return m, nil
		}
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	case promptText:
		if msg.String() == "enter" {
			value := m.input.Value()
			if m.pending.validate != nil {
				if err := m.pending.validate(value); err != nil {
					m.inputError = SafeText(err.Error())
					return m, nil
				}
			}
			m.input.SetValue("")
			m.finish(choiceResult{value: value})
			return m, nil
		}
		var cmd tea.Cmd
		previous := m.input.Value()
		m.input, cmd = m.input.Update(msg)
		if m.input.Value() != previous {
			m.inputError = ""
		}
		return m, cmd
	}
	return m, nil
}

func (m *setupModel) openList(kind promptKind, prompt string, choices []setup.Choice, reply chan choiceResult) {
	prompt = strings.TrimSuffix(prompt, " number:")
	m.pending = &pendingPrompt{kind: kind, prompt: prompt, reply: reply}
	m.activity = prompt
	m.busy = false
	m.list = newChoiceList(choices, m.width, m.height)
	if kind != promptChoose {
		m.list.SetFilteringEnabled(false)
	}
	m.resize()
}

func (m *setupModel) finish(result choiceResult) {
	if m.pending == nil {
		return
	}
	replyOnce(m.pending.reply, result)
	m.pending = nil
}

func (m *setupModel) resize() {
	width := max(m.width, 1)
	m.input.SetWidth(max(1, width-2))
	if m.pending == nil || (m.pending.kind != promptChoose && m.pending.kind != promptConfirm && m.pending.kind != promptRetry) {
		return
	}
	height := 6
	if m.height > 16 {
		height = 8
	}
	m.list.SetSize(width, height)
}

func (m setupModel) tiny() bool {
	return m.width < 30 || m.height < 8
}

func (m setupModel) View() tea.View {
	var b strings.Builder
	state := mascotIdle
	if m.busy {
		state = mascotWorking
	} else if m.summary != "" {
		state = mascotSuccess
		if m.err != nil {
			state = mascotError
		}
	}
	subtitle := ""
	if m.summary == "" && !m.tiny() {
		subtitle = "Share files as links, straight from your terminal."
	}
	b.WriteString(terminalHeader("yz  /  setup", subtitle, state, m.mascotFrame, m.width, m.height))
	b.WriteString("\n")
	b.WriteString(m.trail())
	b.WriteString("\n\n")
	if m.step == setup.StepLogin && !m.tiny() && m.summary == "" {
		b.WriteString(wrapNotice("You'll need a Cloudflare account, an R2 bucket, and an R2 token\nwith Object Read & Write permission. We'll guide you through each step.", m.width))
		b.WriteString("\n\n")
	}
	if m.busy {
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
	}
	if m.activity != "" {
		b.WriteString(styled(titleStyle, wrapNotice(SafeText(m.activity), m.width)))
		b.WriteString("\n")
	}
	for _, line := range m.history {
		b.WriteString(styled(mutedStyle, wrapNotice(line, m.width)))
		b.WriteString("\n")
	}
	if m.copy != "" {
		b.WriteString(styled(titleStyle, wrapNotice(m.copy, m.width)))
		b.WriteString("\n")
	}
	if m.summary != "" {
		b.WriteString(wrapNotice(m.summary, m.width))
		b.WriteString("\n")
	}
	if m.tiny() && m.pending != nil {
		b.WriteString("\nresize the terminal to continue\n")
	} else if m.pending != nil && (m.pending.kind == promptChoose || m.pending.kind == promptConfirm || m.pending.kind == promptRetry) {
		b.WriteString("\n")
		b.WriteString(m.list.View())
		b.WriteString("\n")
		if item, ok := m.list.SelectedItem().(choiceItem); ok {
			b.WriteString("> ")
			b.WriteString(item.title)
			if m.pending.kind == promptChoose {
				fmt.Fprintf(&b, " · %d of %d", m.list.Index()+1, len(m.list.VisibleItems()))
			}
			b.WriteString("\n")
		} else if m.list.FilterValue() != "" {
			b.WriteString(styled(mutedStyle, wrapNotice("No matches. Try a shorter search or press esc to clear.", m.width)))
			b.WriteString("\n")
		}
	} else if m.pending != nil && m.pending.kind == promptText {
		b.WriteString("\n")
		b.WriteString(wrapNotice(m.pending.text.Hint, m.width))
		b.WriteString("\n")
		b.WriteString(m.input.View())
		b.WriteString("\n")
		b.WriteString(m.inputFeedback())
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(styled(mutedStyle, wrapNotice(m.keyboardHelp(), m.width)))
	b.WriteString("\n")
	return tea.NewView(b.String())
}

func (m setupModel) trail() string {
	steps := []struct {
		id    setup.Step
		label string
	}{
		{setup.StepLogin, "Login"},
		{setup.StepAccount, "Account"},
		{setup.StepBucket, "Bucket"},
		{setup.StepCredentials, "Credentials"},
		{setup.StepAccess, "Access"},
	}
	current := 0
	if m.step == setup.StepSave {
		current = len(steps)
	}
	for i, step := range steps {
		if step.id == m.step {
			current = i
		}
	}
	if m.width < 70 {
		if current == len(steps) {
			return styled(successStyle, "✓ All steps complete")
		}
		return styled(titleStyle, fmt.Sprintf("Step %d/%d · %s", current+1, len(steps), steps[current].label))
	}
	parts := make([]string, 0, len(steps))
	for i, step := range steps {
		mark := "·"
		style := mutedStyle
		switch {
		case i < current:
			mark = "✓"
			style = successStyle
		case i == current:
			mark = "●"
			style = titleStyle
		}
		parts = append(parts, styled(style, mark+" "+step.label))
	}
	return strings.Join(parts, "   ")
}

func styled(style lipglossStyle, text string) string {
	if text == "" {
		return ""
	}
	if !colorEnabled() {
		return text
	}
	return style.Render(text)
}

type lipglossStyle interface {
	Render(...string) string
}

func completionSummary(cfg *config.Config, err error) string {
	if err != nil {
		return styled(errorStyle, SafeText(err.Error()))
	}
	if cfg == nil {
		return ""
	}
	access := styled(successStyle, "Private") + " · signed links expire after 24h by default"
	if cfg.URLBase != "" {
		access = styled(warningStyle, "Public · anyone with the link can read") + "\n" + styled(titleStyle, SafeText(cfg.URLBase))
	}
	return strings.Join([]string{
		styled(successStyle, "✓ Setup complete · ready to share"),
		styled(mutedStyle, "Account: ") + SafeText(cfg.AccountID),
		styled(mutedStyle, "Bucket: ") + styled(titleStyle, SafeText(cfg.Bucket)),
		styled(mutedStyle, "Links: ") + access,
		"",
		"Try: " + styled(titleStyle, "yz screenshot.png"),
		"Set a link expiry: " + styled(titleStyle, "yz --expires=1h screenshot.png"),
	}, "\n")
}

func (m *setupModel) remember(text string) {
	m.history = append(m.history, text)
	if len(m.history) > 2 {
		m.history = m.history[len(m.history)-2:]
	}
}

func pasteRejected(content string) bool {
	return len(content) > 4096 || strings.ContainsAny(content, "\r\n")
}

func wrapNotice(s string, width int) string {
	return ansi.Hardwrap(s, max(8, width), true)
}

func safeLines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = SafeText(line)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}
