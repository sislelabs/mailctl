package tui

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal/flow"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type flowsState int

const (
	flowsBrowsing flowsState = iota
	flowsArgInput
)

type FlowsModel struct {
	flows    []*flow.Flow
	groups   []string
	cursor   int
	width    int
	showInfo bool

	// Arg input modal state
	state      flowsState
	targetFlow *flow.Flow
	argInputs  []textinput.Model
	argNames   []string
	argCursor  int
}

type FlowSelectedMsg struct {
	Name string
}

func NewFlowsModel() FlowsModel {
	return FlowsModel{
		flows:  flow.All(),
		groups: flow.Groups(),
	}
}

func (m FlowsModel) Init() tea.Cmd {
	return nil
}

func (m FlowsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tea.KeyMsg:
		if m.state == flowsArgInput {
			return m.updateArgInput(msg)
		}
		return m.updateBrowsing(msg)
	}
	return m, nil
}

func (m FlowsModel) updateBrowsing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "esc":
		if m.showInfo {
			m.showInfo = false
			return m, nil
		}
		return m, func() tea.Msg { return SwitchViewMsg{View: ViewList} }
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.showInfo = false
		}
	case "down", "j":
		if m.cursor < len(m.flows)-1 {
			m.cursor++
			m.showInfo = false
		}
	case "enter", " ":
		m.showInfo = !m.showInfo
	case "r":
		if m.cursor >= 0 && m.cursor < len(m.flows) {
			f := m.flows[m.cursor]

			// If flow needs args, open the input modal
			if len(f.Def.Args) > 0 {
				m.state = flowsArgInput
				m.targetFlow = f
				m.argCursor = 0
				m.argInputs = nil
				m.argNames = nil
				for _, a := range f.Def.Args {
					ti := textinput.New()
					ti.Placeholder = a.Name
					ti.CharLimit = 200
					ti.Width = 40
					if a.Description != "" {
						ti.Placeholder = a.Description
					}
					m.argInputs = append(m.argInputs, ti)
					m.argNames = append(m.argNames, a.Name)
				}
				m.argInputs[0].Focus()
				return m, textinput.Blink
			}

			// If flow is interactive (prompt/confirm), use subprocess
			if f.Def.IsInteractive() {
				return m, func() tea.Msg {
					return FlowRunRequestMsg{Name: f.Name}
				}
			}

			// Otherwise run inline
			return m, func() tea.Msg {
				return FlowRunRequestMsg{Name: f.Name}
			}
		}
	}
	return m, nil
}

func (m FlowsModel) updateArgInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.state = flowsBrowsing
		m.targetFlow = nil
		m.argInputs = nil
		return m, nil

	case "enter":
		// Move to next arg, or submit if last
		if m.argCursor < len(m.argInputs)-1 {
			m.argInputs[m.argCursor].Blur()
			m.argCursor++
			m.argInputs[m.argCursor].Focus()
			return m, textinput.Blink
		}

		// All args collected — build the request
		args := map[string]string{}
		for i, name := range m.argNames {
			args[name] = m.argInputs[i].Value()
		}

		name := m.targetFlow.Name
		isInteractive := m.targetFlow.Def.IsInteractive()

		m.state = flowsBrowsing
		m.targetFlow = nil
		m.argInputs = nil

		if isInteractive {
			return m, func() tea.Msg {
				return FlowRunWithArgsMsg{Name: name, Args: args, Subprocess: true}
			}
		}
		return m, func() tea.Msg {
			return FlowRunWithArgsMsg{Name: name, Args: args, Subprocess: false}
		}

	case "tab", "shift+tab":
		if msg.String() == "tab" && m.argCursor < len(m.argInputs)-1 {
			m.argInputs[m.argCursor].Blur()
			m.argCursor++
			m.argInputs[m.argCursor].Focus()
		} else if msg.String() == "shift+tab" && m.argCursor > 0 {
			m.argInputs[m.argCursor].Blur()
			m.argCursor--
			m.argInputs[m.argCursor].Focus()
		}
		return m, textinput.Blink

	default:
		var cmd tea.Cmd
		m.argInputs[m.argCursor], cmd = m.argInputs[m.argCursor].Update(msg)
		return m, cmd
	}
}

func (m FlowsModel) View() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(ui.Highlight.Render("  Flows") + "\n\n")

	idx := 0
	for _, group := range m.groups {
		b.WriteString("  " + ui.Bold.Render(strings.ToUpper(group)) + "\n")

		for _, f := range flow.ByGroup(group) {
			cursor := "    "
			nameStyle := ui.Accent
			descStyle := ui.Dim
			if idx == m.cursor {
				cursor = "  " + ui.Accent.Render("▸ ")
				nameStyle = ui.White.Bold(true)
			}

			src := ""
			if f.Source == "user" {
				src = " " + lipgloss.NewStyle().Foreground(ui.ColorPurple).Render("[user]")
			}

			name := nameStyle.Render(fmt.Sprintf("%-22s", f.Name))
			desc := descStyle.Render(f.Def.Description)
			b.WriteString(cursor + name + desc + src + "\n")
			idx++
		}
		b.WriteString("\n")
	}

	if len(m.flows) == 0 {
		b.WriteString(ui.Dim.Render("  No flows registered.") + "\n")
	}

	// Arg input modal overlay
	if m.state == flowsArgInput && m.targetFlow != nil {
		b.WriteString(m.renderArgModal())
		return b.String()
	}

	// Detail panel for selected flow
	if m.showInfo && m.cursor >= 0 && m.cursor < len(m.flows) {
		f := m.flows[m.cursor]
		b.WriteString(m.renderFlowDetail(f))
	}

	return b.String()
}

func (m FlowsModel) renderArgModal() string {
	var modal strings.Builder

	modal.WriteString(ui.Bold.Render("Run "+m.targetFlow.Name) + "\n\n")

	for i, name := range m.argNames {
		label := ui.Muted.Render(name + ": ")
		if i == m.argCursor {
			label = ui.Accent.Render(name + ": ")
		}
		modal.WriteString("  " + label + m.argInputs[i].View() + "\n")
	}

	modal.WriteString("\n" + ui.Dim.Render("  enter submit  tab next  esc cancel") + "\n")

	return "\n" + ui.Panel.Render(modal.String()) + "\n"
}

func (m FlowsModel) renderFlowDetail(f *flow.Flow) string {
	var detail strings.Builder

	detail.WriteString(ui.Bold.Render(f.Name) + "\n")
	detail.WriteString(ui.Dim.Render(f.Def.Description) + "\n")

	if len(f.Def.Args) > 0 {
		detail.WriteString("\n" + ui.Muted.Render("Args:") + "\n")
		for _, a := range f.Def.Args {
			req := ""
			if a.Required {
				req = ui.Warn.Render(" *")
			}
			detail.WriteString("  " + ui.Accent.Render(a.Name) + req + "\n")
		}
	}

	if len(f.Def.Flags) > 0 {
		detail.WriteString("\n" + ui.Muted.Render("Flags:") + "\n")
		for _, fl := range f.Def.Flags {
			desc := ""
			if fl.Description != "" {
				desc = "  " + ui.Dim.Render(fl.Description)
			}
			detail.WriteString("  " + ui.Accent.Render("--"+fl.Name) + desc + "\n")
		}
	}

	detail.WriteString("\n" + ui.Dim.Render(fmt.Sprintf("Steps: %d | Source: %s", len(f.Def.Steps), f.Source)) + "\n")
	detail.WriteString("\n" + ui.Accent.Render("r") + ui.Dim.Render(" to run") + "  " + ui.Accent.Render("esc") + ui.Dim.Render(" to close") + "\n")

	return "\n" + ui.InfoPanel.Render(detail.String()) + "\n"
}

func (m FlowsModel) SelectedFlow() string {
	if m.cursor >= 0 && m.cursor < len(m.flows) {
		return m.flows[m.cursor].Name
	}
	return ""
}
