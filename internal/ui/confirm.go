package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Confirm Model ───────────────────────────────────────────────────────────
// Danger confirmation prompt (type to confirm).

type ConfirmModel struct {
	message   string
	expected  string
	input     textinput.Model
	confirmed bool
	cancelled bool
}

func NewConfirm(message, expected string) ConfirmModel {
	ti := textinput.New()
	ti.Placeholder = expected
	ti.CharLimit = 256
	ti.Width = 40
	ti.Focus()

	return ConfirmModel{
		message:  message,
		expected: expected,
		input:    ti,
	}
}

func (m ConfirmModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m ConfirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			m.confirmed = strings.TrimSpace(m.input.Value()) == m.expected
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m ConfirmModel) View() string {
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorRed).
		Padding(1, 2)

	var content strings.Builder
	content.WriteString(Error.Bold(true).Render("⚠ DANGER") + "\n\n")
	content.WriteString(m.message + "\n\n")
	content.WriteString(fmt.Sprintf("Type %s to confirm:\n\n", Bold.Foreground(ColorRed).Render(m.expected)))
	content.WriteString(m.input.View() + "\n\n")
	content.WriteString(Dim.Render("esc to cancel"))

	return "\n" + border.Render(content.String()) + "\n"
}

func (m ConfirmModel) Confirmed() bool {
	return m.confirmed && !m.cancelled
}

// RunConfirm shows a danger confirmation prompt and returns whether the user confirmed.
func RunConfirm(message, expected string) (bool, error) {
	m := NewConfirm(message, expected)
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return false, err
	}
	return result.(ConfirmModel).Confirmed(), nil
}
