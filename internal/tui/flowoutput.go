package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/flow"
	"github.com/sislelabs/mailctl/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// FlowRunRequestMsg is sent when a flow should be run from the TUI.
type FlowRunRequestMsg struct {
	Name string
}

// FlowRunDoneMsg is sent when an interactive flow subprocess finishes.
type FlowRunDoneMsg struct {
	Name string
	Err  error
}

// FlowRunWithArgsMsg is sent when a flow should run with pre-collected args.
type FlowRunWithArgsMsg struct {
	Name       string
	Args       map[string]string
	Subprocess bool // true for interactive flows that need a real terminal
}

// flowOutputMsg carries captured output from the running flow.
type flowOutputMsg struct {
	output string
}

// flowDoneMsg signals the flow has finished.
type flowDoneMsg struct {
	err error
}

type FlowOutputModel struct {
	flowName string
	cfg      *internal.Config
	args     map[string]string
	output   string
	done     bool
	err      error
	scroll   int
	height   int
}

func NewFlowOutputModel(name string, cfg *internal.Config) FlowOutputModel {
	return FlowOutputModel{
		flowName: name,
		cfg:      cfg,
		args:     map[string]string{},
	}
}

func NewFlowOutputModelWithArgs(name string, cfg *internal.Config, args map[string]string) FlowOutputModel {
	return FlowOutputModel{
		flowName: name,
		cfg:      cfg,
		args:     args,
	}
}

func (m FlowOutputModel) Init() tea.Cmd {
	return m.runFlow()
}

func (m FlowOutputModel) runFlow() tea.Cmd {
	name := m.flowName
	cfg := m.cfg
	args := m.args
	return func() tea.Msg {
		// Capture stdout by redirecting it to a pipe
		oldStdout := os.Stdout
		r, w, err := os.Pipe()
		if err != nil {
			return flowDoneMsg{err: err}
		}
		os.Stdout = w

		// Run the flow in this goroutine (stdout goes to pipe)
		flowErr := flow.RunFlow(name, cfg, args, map[string]string{})

		// Restore stdout and close writer
		os.Stdout = oldStdout
		w.Close()

		// Read all captured output
		var buf bytes.Buffer
		io.Copy(&buf, r)
		r.Close()

		captured := buf.String()

		// Strip ANSI-unfriendly characters but keep the content
		if flowErr != nil {
			captured += "\n" + fmt.Sprintf("Error: %s", flowErr)
		}

		return flowOutputMsg{output: captured}
	}
}

func (m FlowOutputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		return m, nil

	case flowOutputMsg:
		m.output = msg.output
		m.done = true
		return m, nil

	case flowDoneMsg:
		m.done = true
		m.err = msg.err
		return m, nil

	case tea.KeyMsg:
		if m.done {
			switch msg.String() {
			case "esc":
				return m, func() tea.Msg { return SwitchViewMsg{View: ViewFlows} }
			case "q":
				return m, tea.Quit
			case "up", "k":
				if m.scroll > 0 {
					m.scroll--
				}
			case "down", "j":
				m.scroll++
			}
		}
	}
	return m, nil
}

func (m FlowOutputModel) View() string {
	var b strings.Builder

	b.WriteString("\n")

	titleStyle := lipgloss.NewStyle().Foreground(ui.ColorAccent).Bold(true)
	b.WriteString("  " + titleStyle.Render(m.flowName) + "\n")

	if !m.done {
		b.WriteString("\n")
		b.WriteString("  " + ui.Dim.Render("Running...") + "\n")
		return b.String()
	}

	// Show output in a panel
	if m.output != "" {
		lines := strings.Split(m.output, "\n")

		// Scrollable viewport
		maxVisible := 20
		if m.height > 10 {
			maxVisible = m.height - 10
		}

		start := m.scroll
		if start > len(lines)-maxVisible {
			start = len(lines) - maxVisible
		}
		if start < 0 {
			start = 0
		}
		end := start + maxVisible
		if end > len(lines) {
			end = len(lines)
		}

		visible := strings.Join(lines[start:end], "\n")

		outputPanel := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ui.ColorBorder).
			Padding(0, 1).
			Width(70).
			Render(visible)

		b.WriteString("\n" + outputPanel + "\n")

		if len(lines) > maxVisible {
			b.WriteString("  " + ui.Dim.Render(fmt.Sprintf("↑↓ scroll (%d/%d lines)", start+1, len(lines))) + "\n")
		}
	}

	if m.err != nil {
		b.WriteString("\n  " + ui.Error.Render("Error: "+m.err.Error()) + "\n")
	}

	b.WriteString("\n  " + ui.Success.Render("Done") + "\n")

	return b.String()
}
