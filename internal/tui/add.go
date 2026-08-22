package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/mailsetup"
	"github.com/sislelabs/mailctl/internal/store"
	"github.com/sislelabs/mailctl/internal/ui"
)

type addPhase int

const (
	addPhaseInput addPhase = iota
	addPhaseRunning
	addPhaseDone
)

type AddDomainModel struct {
	cfg     *internal.Config
	inputs  []textinput.Model
	current int
	phase   addPhase
	spinner spinner.Model
	steps   []addStep
	err     string
	domain  string
}

type addStep struct {
	label   string
	status  int // 0=pending, 1=running, 2=done, 3=warn, 4=fail
	detail  string
	subRows []string
}

type addProgressMsg struct {
	step   int
	status int
	detail string
	subRow string
}

type addDoneMsg struct {
	domain string
	err    string
}

func NewAddDomainModel(cfg *internal.Config) AddDomainModel {
	domainInput := textinput.New()
	domainInput.Placeholder = "yourdomain.com"
	domainInput.CharLimit = 100
	domainInput.Width = 40
	domainInput.Focus()

	aliasInput := textinput.New()
	aliasInput.Placeholder = "hello,support,team"
	aliasInput.CharLimit = 200
	aliasInput.Width = 40
	aliasInput.SetValue("hello")

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(ui.ColorAccent)

	return AddDomainModel{
		cfg:     cfg,
		inputs:  []textinput.Model{domainInput, aliasInput},
		phase:   addPhaseInput,
		spinner: s,
	}
}

func (m AddDomainModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m AddDomainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.phase == addPhaseInput {
			switch msg.String() {
			case "esc":
				return m, func() tea.Msg { return SwitchViewMsg{View: ViewList} }
			case "enter":
				if m.current == 0 {
					m.inputs[0].Blur()
					m.current = 1
					m.inputs[1].Focus()
					return m, textinput.Blink
				}
				// Start the add process
				m.domain = strings.TrimSpace(m.inputs[0].Value())
				aliases := strings.TrimSpace(m.inputs[1].Value())
				if m.domain == "" {
					return m, nil
				}
				m.phase = addPhaseRunning
				// Steps come from the shared flow so the dashboard cannot
				// drift out of step with what actually runs.
				labels := mailsetup.AddStepLabels(mailsetup.ProviderLabel(m.cfg))
				m.steps = make([]addStep, len(labels))
				for i, label := range labels {
					m.steps[i] = addStep{label: label}
				}
				return m, tea.Batch(m.spinner.Tick, m.runAdd(m.domain, aliases))
			case "shift+tab":
				if m.current > 0 {
					m.inputs[m.current].Blur()
					m.current--
					m.inputs[m.current].Focus()
					return m, textinput.Blink
				}
			}
			var cmd tea.Cmd
			m.inputs[m.current], cmd = m.inputs[m.current].Update(msg)
			return m, cmd
		}

		if m.phase == addPhaseDone {
			switch msg.String() {
			case "esc", "enter":
				return m, func() tea.Msg { return DomainAddedMsg{Domain: m.domain} }
			}
		}

	case spinner.TickMsg:
		if m.phase == addPhaseRunning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}

	case addProgressMsg:
		if msg.step < len(m.steps) {
			if msg.status >= 0 {
				m.steps[msg.step].status = msg.status
			}
			if msg.detail != "" {
				m.steps[msg.step].detail = msg.detail
			}
			if msg.subRow != "" {
				m.steps[msg.step].subRows = append(m.steps[msg.step].subRows, msg.subRow)
			}
		}
		return m, nil

	case addDoneMsg:
		m.phase = addPhaseDone
		if msg.err != "" {
			m.err = msg.err
		}
		return m, nil
	}

	return m, nil
}

// runAdd drives the shared setup flow. Progress is collected and delivered as
// one batch: a tea.Cmd cannot emit intermediate messages, so the steps render
// on completion rather than live. The adapter shape means the flow itself is
// unaware of that limitation.
func (m AddDomainModel) runAdd(domain, aliasStr string) tea.Cmd {
	return func() tea.Msg {
		var updates []addProgressMsg

		rep := mailsetup.FuncReporter{
			OnStep: func(index int, status mailsetup.StepStatus, detail string) {
				updates = append(updates, addProgressMsg{
					step:   index,
					status: int(status),
					detail: ui.Dim.Render(detail),
				})
			},
			OnNote: func(index int, level mailsetup.NoteLevel, text string) {
				updates = append(updates, addProgressMsg{
					step:   index,
					status: -1,
					subRow: noteRowTUI(level, text),
				})
			},
		}

		_, err := mailsetup.AddDomain(store.NewYAML(), rep, mailsetup.AddOptions{
			Domain:  domain,
			Aliases: strings.Split(aliasStr, ","),
		})

		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		return addBatchMsg{msgs: toMsgs(updates, domain, errStr)}
	}
}

// noteRowTUI styles a detail line. mailsetup emits levels rather than styled
// text, so every icon and colour choice lives here.
func noteRowTUI(level mailsetup.NoteLevel, text string) string {
	switch level {
	case mailsetup.NoteOK:
		return ui.IconSuccess + " " + ui.Dim.Render(text)
	case mailsetup.NoteWarn:
		return ui.IconWarn + " " + ui.Dim.Render(text)
	default:
		return ui.IconError + " " + ui.Error.Render(text)
	}
}

type addBatchMsg struct {
	msgs []tea.Msg
}

func toMsgs(updates []addProgressMsg, domain, errStr string) []tea.Msg {
	msgs := make([]tea.Msg, len(updates)+1)
	for i, u := range updates {
		msgs[i] = u
	}
	msgs[len(updates)] = addDoneMsg{domain: domain, err: errStr}
	return msgs
}

func (m AddDomainModel) View() string {
	var b strings.Builder

	if m.phase == addPhaseInput {
		labels := []string{"Domain", "Aliases (comma-separated)"}
		b.WriteString("\n")

		for i := 0; i < m.current; i++ {
			b.WriteString(fmt.Sprintf("  %s %s %s\n",
				ui.IconSuccess,
				ui.Dim.Render(labels[i]),
				ui.Dim.Render(m.inputs[i].Value())))
		}

		if m.current < len(m.inputs) {
			label := ui.White.Bold(true).Render("  " + labels[m.current])
			b.WriteString("\n" + label + "\n\n")
			b.WriteString("  " + m.inputs[m.current].View() + "\n")
		}
		return b.String()
	}

	// Running / Done phase
	b.WriteString("\n")
	for _, step := range m.steps {
		var icon string
		switch step.status {
		case 0:
			icon = ui.Dim.Render("○")
		case 1:
			icon = m.spinner.View()
		case 2:
			icon = ui.IconSuccess
		case 3:
			icon = ui.IconWarn
		case 4:
			icon = ui.IconError
		}

		label := step.label
		switch step.status {
		case 0:
			label = ui.Dim.Render(label)
		case 1:
			label = ui.White.Render(label)
		case 2:
			label = ui.Success.Render(label)
		case 3:
			label = ui.Warn.Render(label)
		case 4:
			label = ui.Error.Render(label)
		}

		line := fmt.Sprintf("  %s %s", icon, label)
		if step.detail != "" {
			line += " " + step.detail
		}
		b.WriteString(line + "\n")

		for _, sub := range step.subRows {
			b.WriteString("      " + sub + "\n")
		}
	}

	if m.phase == addPhaseDone {
		b.WriteString("\n")
		if m.err != "" {
			b.WriteString(ui.IconError + " " + ui.Error.Render(m.err) + "\n")
		} else {
			b.WriteString(ui.Success.Bold(true).Render(m.domain+" is set up!") + "\n\n")
			b.WriteString(ui.Muted.Render("Gmail Send-As") + "\n")
			b.WriteString(ui.Dim.Render("Open ") + ui.Accent.Render("mail.google.com/mail/#settings/accounts") + "\n\n")
			b.WriteString(ui.Muted.Render("SMTP Settings") + "\n")
			if m.cfg.SendingProvider() == internal.ProviderResend {
				b.WriteString(ui.KeyValue("Server  ", "smtp.resend.com") + "\n")
				b.WriteString(ui.KeyValue("Port    ", "587") + "\n")
				b.WriteString(ui.KeyValue("Username", "resend") + "\n")
				b.WriteString(ui.KeyValue("Password", ui.Dim.Render("your Resend API key")) + "\n")
			} else {
				b.WriteString(ui.KeyValue("Server  ", "smtp-relay.brevo.com") + "\n")
				b.WriteString(ui.KeyValue("Port    ", "587") + "\n")
				b.WriteString(ui.KeyValue("Username", m.cfg.BrevoSMTPLogin) + "\n")
				b.WriteString(ui.KeyValue("Password", m.cfg.BrevoSMTPKey) + "\n")
			}
		}
		b.WriteString("\n" + ui.Dim.Render("press enter or esc to continue"))
	}

	return b.String()
}

// addDMARCTUI publishes a starter DMARC policy as part of the DNS step, matching
// what the CLI's add path does. Without a DMARC record, receivers have no
// published policy to evaluate SPF and DKIM against, which is the usual reason
// authenticated mail still lands in spam.
