package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/brevo"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	step    int
	status  int
	detail  string
	subRow  string
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
		cfg:    cfg,
		inputs: []textinput.Model{domainInput, aliasInput},
		phase:  addPhaseInput,
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
				m.steps = []addStep{
					{label: "Look up Cloudflare zone"},
					{label: "Enable email routing"},
					{label: "Verify destination address"},
					{label: "Create routing rules"},
					{label: "Add domain to Brevo"},
					{label: "Add DNS records"},
					{label: "Authenticate domain"},
					{label: "Create senders"},
					{label: "Save config"},
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

func (m AddDomainModel) runAdd(domain, aliasStr string) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		send := func(step, status int, detail, subRow string) {
			// We can't send tea.Msg from here directly in a clean way,
			// but we'll use the program's Send method via a channel approach.
			// For simplicity, we'll batch all updates at the end.
		}
		_ = send

		// We need to use a channel-based approach
		// Actually, let's use a simpler approach: return a batch of commands
		return runAddDomain(cfg, domain, aliasStr)
	}
}

type addBatchMsg struct {
	msgs []tea.Msg
}

func runAddDomain(cfg *internal.Config, domain, aliasStr string) tea.Msg {
	// Since we can't easily send intermediate messages from a cmd,
	// we'll collect all progress and send it as a batch
	var updates []addProgressMsg

	p := func(step, status int, detail string) {
		updates = append(updates, addProgressMsg{step: step, status: status, detail: detail})
	}
	sub := func(step int, row string) {
		updates = append(updates, addProgressMsg{step: step, status: -1, subRow: row})
	}

	aliases := strings.Split(aliasStr, ",")
	for i := range aliases {
		aliases[i] = strings.TrimSpace(aliases[i])
	}
	forwardTo := cfg.DefaultForwardTo

	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	bv := brevo.NewClient(cfg.BrevoAPIKey)

	// Step 0: Find zone
	p(0, 1, "")
	zone, err := cf.GetZoneByName(domain)
	if err != nil {
		p(0, 4, "not found")
		return addBatchMsg{msgs: toMsgs(updates, domain, "Zone not found — is the domain in Cloudflare?")}
	}
	p(0, 2, ui.Dim.Render(zone.ID))

	// Step 1: Enable routing (delete conflicting MX records first)
	p(1, 1, "")
	mxRecords, _ := cf.ListDNSRecords(zone.ID, "MX")
	for _, mx := range mxRecords {
		if mx.Name == domain {
			cf.DeleteDNSRecord(zone.ID, mx.ID)
		}
	}
	if err := cf.EnableEmailRouting(zone.ID); err != nil {
		p(1, 3, err.Error())
	} else {
		p(1, 2, "")
	}

	// Step 2: Verify destination address
	p(2, 1, "")
	destVerified := false
	if addrs, err := cf.ListDestinationAddresses(zone.Account.ID); err == nil {
		for _, a := range addrs {
			if a.Email == forwardTo && a.Verified != "" {
				destVerified = true
				break
			}
		}
	}
	if destVerified {
		p(2, 2, ui.Dim.Render(forwardTo))
	} else {
		cf.CreateDestinationAddress(zone.Account.ID, forwardTo)
		p(2, 3, "verification email sent — check inbox")
	}

	// Step 3: Routing rules
	p(3, 1, "")
	var cfAliases []internal.Alias
	ruleErrors := 0
	for _, alias := range aliases {
		addr := fmt.Sprintf("%s@%s", alias, domain)
		rule := cloudflare.RoutingRule{
			Name: fmt.Sprintf("Forward %s", addr), Enabled: true,
			Matchers: []cloudflare.RuleMatcher{{Type: "literal", Field: "to", Value: addr}},
			Actions:  []cloudflare.RuleAction{{Type: "forward", Value: []string{forwardTo}}},
		}
		if err := cf.CreateRoutingRule(zone.ID, rule); err != nil {
			sub(3, ui.IconError+" "+ui.Error.Render(addr)+" "+ui.Dim.Render(err.Error()))
			ruleErrors++
		} else {
			sub(3, ui.IconSuccess+" "+ui.Dim.Render(addr+" → "+forwardTo))
		}
		cfAliases = append(cfAliases, internal.Alias{Alias: alias, ForwardTo: []string{forwardTo}})
	}
	if ruleErrors > 0 {
		p(3, 3, fmt.Sprintf("%d failed", ruleErrors))
	} else {
		p(3, 2, fmt.Sprintf("%d rules", len(aliases)))
	}

	// Step 4: Brevo
	p(4, 1, "")
	brevoDomain, err := bv.AddDomain(domain)
	if err != nil {
		p(4, 3, "failed")
		sub(4, ui.Dim.Render(err.Error()))
		p(5, 3, "skipped")
		p(6, 3, "skipped")
		p(7, 3, "skipped")
	} else {
		p(4, 2, "")

		// Step 5: DNS records
		p(5, 1, "")
		for _, rec := range brevoDomain.FlatDNSRecords() {
			name := brevo.FullRecordName(rec, domain)
			cfRec := cloudflare.DNSRecord{Type: rec.Type, Name: name, Content: rec.Value, TTL: 3600}
			if err := cf.CreateDNSRecord(zone.ID, cfRec); err != nil {
				sub(5, ui.IconWarn+" "+ui.Dim.Render(rec.Type+" "+name+" — "+err.Error()))
			} else {
				sub(5, ui.IconSuccess+" "+ui.Dim.Render(rec.Type+" "+name))
			}
		}
		p(5, 2, "")

		// Step 6: Authenticate
		p(6, 1, "")
		time.Sleep(2 * time.Second)
		if err := bv.AuthenticateDomain(domain); err != nil {
			p(6, 3, "pending — DNS may need time")
		} else {
			p(6, 2, "")
		}

		// Step 7: Create senders
		p(7, 1, "")
		for _, alias := range aliases {
			addr := fmt.Sprintf("%s@%s", alias, domain)
			senderName := strings.ToUpper(alias[:1]) + alias[1:]
			if err := bv.CreateSender(senderName, addr); err != nil {
				sub(7, ui.IconWarn+" "+ui.Dim.Render(addr+" — "+err.Error()))
			} else {
				sub(7, ui.IconSuccess+" "+ui.Dim.Render(addr))
			}
		}
		p(7, 2, "")
	}

	// Step 8: Save
	p(8, 1, "")
	cfg.AddDomain(domain, zone.ID, cfAliases)
	if err := internal.SaveConfig(cfg); err != nil {
		p(8, 4, err.Error())
		return addBatchMsg{msgs: toMsgs(updates, domain, err.Error())}
	}
	p(8, 2, "")

	return addBatchMsg{msgs: toMsgs(updates, domain, "")}
}

func toMsgs(updates []addProgressMsg, domain, errStr string) []tea.Msg {
	msgs := make([]tea.Msg, len(updates)+1)
	for i, u := range updates {
		msgs[i] = u
	}
	msgs[len(updates)] = addDoneMsg{domain: domain, err: errStr}
	return msgs
}

// We need a way to send batched messages. Let's handle addBatchMsg in Update.

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
			b.WriteString(ui.KeyValue("Server  ", "smtp-relay.brevo.com") + "\n")
			b.WriteString(ui.KeyValue("Port    ", "587") + "\n")
			b.WriteString(ui.KeyValue("Username", m.cfg.BrevoSMTPLogin) + "\n")
			b.WriteString(ui.KeyValue("Password", m.cfg.BrevoSMTPKey) + "\n")
		}
		b.WriteString("\n" + ui.Dim.Render("press enter or esc to continue"))
	}

	return b.String()
}
