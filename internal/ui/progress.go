package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Step Progress Model ─────────────────────────────────────────────────────
// Shows a list of steps with spinners for in-progress items.

type StepStatus int

const (
	StepPending StepStatus = iota
	StepRunning
	StepDone
	StepWarn
	StepFail
)

type StepItem struct {
	Label   string
	Status  StepStatus
	Detail  string
	SubRows []string
}

type ProgressModel struct {
	Steps   []StepItem
	spinner spinner.Model
	title   string
	done    bool
	width   int
}

type StepUpdateMsg struct {
	Index  int
	Status StepStatus
	Detail string
}

type StepAddSubRowMsg struct {
	Index int
	Row   string
}

type ProgressDoneMsg struct{}

func NewProgressModel(title string) ProgressModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(ColorAccent)
	return ProgressModel{
		spinner: s,
		title:   title,
		width:   80,
	}
}

func (m ProgressModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m ProgressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case StepUpdateMsg:
		if msg.Index < len(m.Steps) {
			m.Steps[msg.Index].Status = msg.Status
			if msg.Detail != "" {
				m.Steps[msg.Index].Detail = msg.Detail
			}
		}
		return m, nil

	case StepAddSubRowMsg:
		if msg.Index < len(m.Steps) {
			m.Steps[msg.Index].SubRows = append(m.Steps[msg.Index].SubRows, msg.Row)
		}
		return m, nil

	case ProgressDoneMsg:
		m.done = true
		return m, tea.Quit
	}

	return m, nil
}

func (m ProgressModel) View() string {
	var b strings.Builder

	// Title bar
	titleBar := lipgloss.NewStyle().
		Foreground(ColorAccent).
		Bold(true).
		Render(m.title)
	b.WriteString("\n " + titleBar + "\n\n")

	for _, step := range m.Steps {
		var icon string
		switch step.Status {
		case StepPending:
			icon = Dim.Render("○")
		case StepRunning:
			icon = m.spinner.View()
		case StepDone:
			icon = IconSuccess
		case StepWarn:
			icon = IconWarn
		case StepFail:
			icon = IconError
		}

		label := step.Label
		if step.Status == StepRunning {
			label = White.Render(label)
		} else if step.Status == StepDone {
			label = Success.Render(label)
		} else if step.Status == StepFail {
			label = Error.Render(label)
		} else if step.Status == StepWarn {
			label = Warn.Render(label)
		} else {
			label = Dim.Render(label)
		}

		line := fmt.Sprintf(" %s %s", icon, label)
		if step.Detail != "" {
			line += " " + Dim.Render(step.Detail)
		}
		b.WriteString(line + "\n")

		for _, sub := range step.SubRows {
			b.WriteString("     " + sub + "\n")
		}
	}

	if !m.done {
		b.WriteString("\n " + Dim.Render("ctrl+c to cancel") + "\n")
	}

	return b.String()
}

// ── Run Steps Helper ────────────────────────────────────────────────────────
// RunSteps runs a progress model with a callback that executes the actual work.

type StepFunc func(send func(tea.Msg)) error

// RunProgress renders a live step display while run does the work.
//
// Without a terminal — piped output, CI, a script — it falls back to plain
// sequential lines. Bubble Tea needs a TTY and fails outright otherwise, which
// previously made every multi-step command unusable outside an interactive
// shell.
func RunProgress(title string, steps []string, run func(p *ProgressRunner)) error {
	if !isTerminal() {
		return runProgressPlain(title, steps, run)
	}

	items := make([]StepItem, len(steps))
	for i, label := range steps {
		items[i] = StepItem{Label: label, Status: StepPending}
	}

	m := NewProgressModel(title)
	m.Steps = items

	p := tea.NewProgram(m)
	runner := &ProgressRunner{p: p}

	go func() {
		time.Sleep(100 * time.Millisecond) // let TUI render
		run(runner)
		p.Send(ProgressDoneMsg{})
	}()

	_, err := p.Run()
	return err
}

type ProgressRunner struct {
	p      *tea.Program
	plain  *plainRunner
	Result strings.Builder
}

func (r *ProgressRunner) Start(index int) {
	if r.plain != nil {
		return // the outcome line says everything a plain log needs
	}
	r.p.Send(StepUpdateMsg{Index: index, Status: StepRunning})
}

func (r *ProgressRunner) Done(index int, detail string) {
	if r.plain != nil {
		r.plain.step(index, "ok", detail)
		return
	}
	r.p.Send(StepUpdateMsg{Index: index, Status: StepDone, Detail: detail})
}

func (r *ProgressRunner) Warn(index int, detail string) {
	if r.plain != nil {
		r.plain.step(index, "warn", detail)
		return
	}
	r.p.Send(StepUpdateMsg{Index: index, Status: StepWarn, Detail: detail})
}

func (r *ProgressRunner) Fail(index int, detail string) {
	if r.plain != nil {
		r.plain.step(index, "FAIL", detail)
		return
	}
	r.p.Send(StepUpdateMsg{Index: index, Status: StepFail, Detail: detail})
}

func (r *ProgressRunner) SubRow(index int, row string) {
	if r.plain != nil {
		fmt.Fprintln(r.plain.out, "      "+stripANSI(row))
		return
	}
	r.p.Send(StepAddSubRowMsg{Index: index, Row: row})
}

func (r *ProgressRunner) Print(s string) {
	r.Result.WriteString(s)
}

// isTerminal reports whether an interactive terminal is available.
//
// It opens /dev/tty rather than only checking whether stdout is a character
// device, because that is what Bubble Tea itself needs. A controlling terminal
// can be absent even when stdout looks terminal-ish, and the mismatch surfaces
// as "could not open a new TTY" after the command has already started work.
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	_ = tty.Close()
	return true
}

// plainRunner writes step progress as plain lines. It satisfies the same shape
// as ProgressRunner's methods via the shared type below.
type plainRunner struct {
	labels []string
	out    io.Writer
}

func (r *plainRunner) step(index int, marker, detail string) {
	if index < 0 || index >= len(r.labels) {
		return
	}
	line := fmt.Sprintf("  %s %s", marker, r.labels[index])
	if detail != "" {
		line += " — " + stripANSI(detail)
	}
	fmt.Fprintln(r.out, line)
}

// runProgressPlain is the non-terminal path: same callbacks, plain output.
func runProgressPlain(title string, steps []string, run func(p *ProgressRunner)) error {
	out := os.Stdout
	fmt.Fprintln(out)
	fmt.Fprintln(out, stripANSI(title))

	pr := &plainRunner{labels: steps, out: out}
	runner := &ProgressRunner{plain: pr}
	run(runner)

	fmt.Fprintln(out)
	return nil
}

// stripANSI removes escape sequences so styled details stay readable when the
// output is a file or a pipe.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
