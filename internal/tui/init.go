package tui

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type initField struct {
	label       string
	help        string
	placeholder string
}

var initFields = []initField{
	{
		label:       "Cloudflare API Token",
		help:        "https://dash.cloudflare.com/profile/api-tokens — needs DNS Edit + Email Routing Edit",
		placeholder: "cfut_...",
	},
	{
		label:       "Cloudflare Account ID",
		help:        "Hex string in your dashboard URL: dash.cloudflare.com/<ID>/...",
		placeholder: "abc123...",
	},
	{
		label:       "Brevo API Key",
		help:        "https://app.brevo.com/settings/keys/api — for domain management",
		placeholder: "xkeysib-...",
	},
	{
		label:       "Brevo SMTP Key",
		help:        "https://app.brevo.com/settings/keys/smtp — for sending email",
		placeholder: "xsmtpsib-...",
	},
	{
		label:       "Brevo SMTP Login",
		help:        "Shown on the SMTP settings page, e.g. xxx@smtp-brevo.com",
		placeholder: "xxx@smtp-brevo.com",
	},
	{
		label:       "Default forward-to email",
		help:        "Your real email where custom domain mail gets forwarded",
		placeholder: "you@gmail.com",
	},
}

type InitModel struct {
	inputs  []textinput.Model
	current int
	done    bool
}

func NewInitModel() InitModel {
	inputs := make([]textinput.Model, len(initFields))
	for i, f := range initFields {
		ti := textinput.New()
		ti.Placeholder = f.placeholder
		ti.CharLimit = 256
		ti.Width = 50
		if i == 0 {
			ti.Focus()
		}
		inputs[i] = ti
	}
	return InitModel{inputs: inputs}
}

func (m InitModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m InitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if m.current < len(m.inputs)-1 {
				m.inputs[m.current].Blur()
				m.current++
				m.inputs[m.current].Focus()
				return m, textinput.Blink
			}
			// Done — save config
			m.done = true
			return m, m.saveConfig()
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

func (m InitModel) saveConfig() tea.Cmd {
	return func() tea.Msg {
		cfg := &internal.Config{
			CloudflareAPIToken:  strings.TrimSpace(m.inputs[0].Value()),
			CloudflareAccountID: strings.TrimSpace(m.inputs[1].Value()),
			BrevoAPIKey:         strings.TrimSpace(m.inputs[2].Value()),
			BrevoSMTPKey:        strings.TrimSpace(m.inputs[3].Value()),
			BrevoSMTPLogin:      strings.TrimSpace(m.inputs[4].Value()),
			DefaultForwardTo:    strings.TrimSpace(m.inputs[5].Value()),
		}
		if err := internal.SaveConfig(cfg); err != nil {
			return StatusMsg{Text: "Error: " + err.Error()}
		}
		return ConfigSavedMsg{Cfg: cfg}
	}
}

func (m InitModel) View() string {
	var b strings.Builder

	title := lipgloss.NewStyle().
		Foreground(ui.ColorAccent).
		Bold(true).
		Render("  First-time setup")
	b.WriteString("\n" + title + "\n")

	subtitle := ui.Dim.Render("  Configure your API keys to get started\n")
	b.WriteString(subtitle)

	// Progress dots
	dots := "  "
	for i := range initFields {
		if i < m.current {
			dots += ui.Success.Render("●") + " "
		} else if i == m.current {
			dots += ui.Highlight.Render("●") + " "
		} else {
			dots += ui.Dim.Render("○") + " "
		}
	}
	b.WriteString("\n" + dots + "\n")

	// Completed fields
	for i := 0; i < m.current; i++ {
		val := m.inputs[i].Value()
		if len(val) > 20 {
			val = val[:8] + "..." + val[len(val)-4:]
		}
		b.WriteString(fmt.Sprintf("\n  %s %s %s",
			ui.IconSuccess,
			ui.Dim.Render(initFields[i].label),
			ui.Dim.Render(val)))
	}

	// Current field
	if m.current < len(initFields) {
		f := initFields[m.current]
		b.WriteString("\n\n")
		label := ui.White.Bold(true).Render("  " + f.label)
		b.WriteString(label + "\n")
		b.WriteString("  " + ui.Subtle.Render(f.help) + "\n\n")
		b.WriteString("  " + m.inputs[m.current].View())
	}

	b.WriteString("\n")
	return b.String()
}
