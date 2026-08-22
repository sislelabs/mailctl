package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/mailsetup"
	"github.com/sislelabs/mailctl/internal/store"
	"github.com/sislelabs/mailctl/internal/ui"
)

// ── Aliases List View ───────────────────────────────────────────────────────

type AliasesModel struct {
	cfg    *internal.Config
	dc     *internal.DomainConfig
	domain string
	cursor int
}

func NewAliasesModel(cfg *internal.Config, dc *internal.DomainConfig) AliasesModel {
	return AliasesModel{
		cfg:    cfg,
		dc:     dc,
		domain: dc.Domain,
	}
}

func (m AliasesModel) Init() tea.Cmd { return nil }

func (m AliasesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return SwitchViewMsg{View: ViewList} }
		case "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.dc.Aliases)-1 {
				m.cursor++
			}
		case "a":
			return m, func() tea.Msg { return SwitchViewMsg{View: ViewAddAlias} }
		case "d":
			if len(m.dc.Aliases) > 0 && m.cursor < len(m.dc.Aliases) {
				alias := m.dc.Aliases[m.cursor].Alias
				domain := m.domain
				cfg := m.cfg
				return m, func() tea.Msg {
					return deleteAlias(cfg, domain, alias)
				}
			}
		}
	}
	return m, nil
}

func deleteAlias(cfg *internal.Config, domain, alias string) tea.Msg {
	d := cfg.FindDomain(domain)
	if d == nil {
		return StatusMsg{Text: "Domain not found"}
	}

	addr := fmt.Sprintf("%s@%s", alias, domain)
	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)

	// Delete CF rule
	rules, err := cf.ListRoutingRules(d.CloudflareZoneID)
	if err == nil {
		for _, r := range rules {
			for _, matcher := range r.Matchers {
				if matcher.Value == addr {
					cf.DeleteRoutingRule(d.CloudflareZoneID, r.ID)
					break
				}
			}
		}
	}

	d.RemoveAlias(alias)
	internal.SaveConfig(cfg)

	return AliasDeletedMsg{Domain: domain, Alias: alias}
}

func (m AliasesModel) View() string {
	var b strings.Builder

	if len(m.dc.Aliases) == 0 {
		b.WriteString("\n\n")
		b.WriteString("  " + ui.InfoPanel.Render(
			ui.Info.Render("No aliases for "+m.domain)+"\n\n"+
				ui.Dim.Render("Press ")+ui.Highlight.Render("a")+ui.Dim.Render(" to add one"),
		))
		return b.String()
	}

	b.WriteString("\n")

	for i, a := range m.dc.Aliases {
		selected := i == m.cursor

		cursor := "  "
		if selected {
			cursor = ui.Highlight.Render("▸ ")
		}

		addr := a.Alias + "@" + m.domain
		var maskedFwd []string
		for _, e := range a.ForwardTo {
			maskedFwd = append(maskedFwd, ui.MaskEmail(e))
		}
		fwd := strings.Join(maskedFwd, ", ")

		addrStyle := ui.White
		if selected {
			addrStyle = ui.Highlight
		}

		b.WriteString(fmt.Sprintf("%s%s %s %s\n",
			cursor,
			addrStyle.Render(addr),
			ui.Dim.Render("→"),
			ui.Dim.Render(fwd)))
	}

	b.WriteString(fmt.Sprintf("\n  %s\n", ui.Dim.Render(fmt.Sprintf("%d alias(es)", len(m.dc.Aliases)))))

	return b.String()
}

// ── Add Alias View ──────────────────────────────────────────────────────────

type AddAliasModel struct {
	cfg    *internal.Config
	domain string
	input  textinput.Model
}

func NewAddAliasModel(cfg *internal.Config, domain string) AddAliasModel {
	ti := textinput.New()
	ti.Placeholder = "alias name (e.g. billing)"
	ti.CharLimit = 100
	ti.Width = 30
	ti.Focus()

	return AddAliasModel{
		cfg:    cfg,
		domain: domain,
		input:  ti,
	}
}

func (m AddAliasModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m AddAliasModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return SwitchViewMsg{View: ViewAliases} }
		case "enter":
			alias := strings.TrimSpace(m.input.Value())
			if alias == "" {
				return m, nil
			}
			cfg := m.cfg
			domain := m.domain
			return m, func() tea.Msg {
				return addAlias(cfg, domain, alias)
			}
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func addAlias(cfg *internal.Config, domain, alias string) tea.Msg {
	d := cfg.FindDomain(domain)
	if d == nil {
		return StatusMsg{Text: "Domain not found"}
	}

	addr := fmt.Sprintf("%s@%s", alias, domain)
	forwardTo := cfg.DefaultForwardTo

	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	rule := cloudflare.RoutingRule{
		Name: fmt.Sprintf("Forward %s", addr), Enabled: true,
		Matchers: []cloudflare.RuleMatcher{{Type: "literal", Field: "to", Value: addr}},
		Actions:  []cloudflare.RuleAction{{Type: "forward", Value: []string{forwardTo}}},
	}

	if err := cf.CreateRoutingRule(d.CloudflareZoneID, rule); err != nil {
		return StatusMsg{Text: "Failed: " + err.Error()}
	}

	d.AddAlias(alias, []string{forwardTo})
	internal.SaveConfig(cfg)

	return AliasAddedMsg{Domain: domain, Alias: alias}
}

func (m AddAliasModel) View() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + ui.White.Bold(true).Render("New alias for "+m.domain) + "\n\n")
	b.WriteString("  " + m.input.View() + "\n\n")
	b.WriteString("  " + ui.Dim.Render("Will forward to: "+m.cfg.DefaultForwardTo) + "\n")
	return b.String()
}

// ── Delete Confirm View ─────────────────────────────────────────────────────

type DeleteConfirmModel struct {
	cfg    *internal.Config
	domain string
	input  textinput.Model
}

func NewDeleteConfirmModel(cfg *internal.Config, domain string) DeleteConfirmModel {
	ti := textinput.New()
	ti.Placeholder = domain
	ti.CharLimit = 100
	ti.Width = 40
	ti.Focus()

	return DeleteConfirmModel{
		cfg:    cfg,
		domain: domain,
		input:  ti,
	}
}

// providerName is the display name of the configured sending provider.
func (m DeleteConfirmModel) providerName() string {
	if m.cfg.SendingProvider() == internal.ProviderResend {
		return "Resend"
	}
	return "Brevo"
}

func (m DeleteConfirmModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m DeleteConfirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return SwitchViewMsg{View: ViewList} }
		case "enter":
			if strings.TrimSpace(m.input.Value()) == m.domain {
				cfg := m.cfg
				domain := m.domain
				return m, func() tea.Msg {
					return deleteDomain(cfg, domain)
				}
			}
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// deleteDomain tears the domain down through the shared flow. The dashboard
// has nowhere to show step-by-step progress here, so it discards it and
// reports only the outcome.
func deleteDomain(cfg *internal.Config, domain string) tea.Msg {
	if cfg.FindDomain(domain) == nil {
		return StatusMsg{Text: "Domain not found"}
	}

	if _, err := mailsetup.RemoveDomain(store.NewYAML(), mailsetup.NopReporter{}, mailsetup.RemoveOptions{
		Domain: domain,
	}); err != nil {
		return StatusMsg{Text: "Error: " + err.Error()}
	}

	// The caller holds its own copy of the config; keep it in step with what
	// was just written.
	cfg.RemoveDomain(domain)

	return DomainDeletedMsg{Domain: domain}
}

func (m DeleteConfirmModel) View() string {
	var b strings.Builder
	b.WriteString("\n")

	box := ui.ErrorPanel.Render(
		ui.Error.Bold(true).Render("Delete "+m.domain) + "\n\n" +
			ui.Dim.Render("This will remove:") + "\n" +
			ui.Dim.Render("  "+ui.IconDot+" Cloudflare routing rules") + "\n" +
			ui.Dim.Render("  "+ui.IconDot+" Catch-all forwarding") + "\n" +
			ui.Dim.Render("  "+ui.IconDot+" "+m.providerName()+" domain") + "\n" +
			ui.Dim.Render("  "+ui.IconDot+" "+m.dnsScope()) + "\n" +
			ui.Dim.Render("  "+ui.IconDot+" Config entry") + "\n\n" +
			"Type " + ui.Error.Bold(true).Render(m.domain) + " to confirm:\n\n" +
			m.input.View(),
	)
	b.WriteString("  " + box)
	return b.String()
}

// dnsScope describes exactly which DNS records deletion will touch, so the
// confirmation prompt cannot imply a broader sweep than actually happens.
func (m DeleteConfirmModel) dnsScope() string {
	d := m.cfg.FindDomain(m.domain)
	if d == nil || len(d.ManagedDNSRecordIDs) == 0 {
		return "No DNS records (none tracked)"
	}
	return fmt.Sprintf("%d DNS records created by mailctl", len(d.ManagedDNSRecordIDs))
}
