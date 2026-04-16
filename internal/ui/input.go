package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Wizard Model ────────────────────────────────────────────────────────────
// Multi-step text input wizard (for mailctl init).

type WizardField struct {
	Label       string
	Help        string
	Placeholder string
	Password    bool
	Value       string
}

type WizardModel struct {
	fields  []WizardField
	inputs  []textinput.Model
	current int
	done    bool
	title   string
	width   int
}

func NewWizard(title string, fields []WizardField) WizardModel {
	inputs := make([]textinput.Model, len(fields))
	for i, f := range fields {
		ti := textinput.New()
		ti.Placeholder = f.Placeholder
		ti.CharLimit = 256
		ti.Width = 60
		if f.Password {
			ti.EchoMode = textinput.EchoPassword
		}
		if f.Value != "" {
			ti.SetValue(f.Value)
		}
		if i == 0 {
			ti.Focus()
		}
		inputs[i] = ti
	}
	return WizardModel{
		fields: fields,
		inputs: inputs,
		title:  title,
		width:  80,
	}
}

func (m WizardModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m WizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.done = false
			return m, tea.Quit

		case "enter":
			if m.current < len(m.inputs)-1 {
				m.inputs[m.current].Blur()
				m.current++
				m.inputs[m.current].Focus()
				return m, textinput.Blink
			}
			m.done = true
			return m, tea.Quit

		case "shift+tab":
			if m.current > 0 {
				m.inputs[m.current].Blur()
				m.current--
				m.inputs[m.current].Focus()
				return m, textinput.Blink
			}
		}
	}

	var cmd tea.Cmd
	m.inputs[m.current], cmd = m.inputs[m.current].Update(msg)
	return m, cmd
}

func (m WizardModel) View() string {
	var b strings.Builder

	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(ColorAccent).
		Bold(true)
	b.WriteString("\n " + titleStyle.Render(m.title) + "\n")

	// Progress dots
	dots := " "
	for i := range m.fields {
		if i < m.current {
			dots += Success.Render("●") + " "
		} else if i == m.current {
			dots += Highlight.Render("●") + " "
		} else {
			dots += Dim.Render("○") + " "
		}
	}
	b.WriteString(dots + "\n\n")

	for i, field := range m.fields {
		if i < m.current {
			// Completed field — show as dimmed
			val := m.inputs[i].Value()
			if field.Password && len(val) > 8 {
				val = val[:4] + "****" + val[len(val)-4:]
			}
			b.WriteString(fmt.Sprintf(" %s %s %s\n",
				IconSuccess,
				Dim.Render(field.Label),
				Dim.Render(val),
			))
		} else if i == m.current {
			// Current field — show input
			label := lipgloss.NewStyle().
				Foreground(ColorWhite).
				Bold(true).
				Render(field.Label)
			b.WriteString("\n " + label + "\n")
			if field.Help != "" {
				b.WriteString(" " + Subtle.Render(field.Help) + "\n")
			}
			b.WriteString("\n " + m.inputs[i].View() + "\n\n")
		}
		// Future fields are not shown
	}

	// Footer
	nav := Dim.Render(" enter") + White.Render(" next")
	if m.current > 0 {
		nav += Dim.Render("  shift+tab") + White.Render(" back")
	}
	nav += Dim.Render("  esc") + White.Render(" cancel")
	b.WriteString("\n" + nav + "\n")

	return b.String()
}

func (m WizardModel) Values() []string {
	vals := make([]string, len(m.inputs))
	for i, input := range m.inputs {
		vals[i] = strings.TrimSpace(input.Value())
	}
	return vals
}

func (m WizardModel) Completed() bool {
	return m.done
}

// RunWizard runs a wizard and returns the values.
func RunWizard(title string, fields []WizardField) ([]string, bool, error) {
	m := NewWizard(title, fields)
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return nil, false, err
	}
	final := result.(WizardModel)
	return final.Values(), final.Completed(), nil
}
