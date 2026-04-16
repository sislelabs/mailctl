package tui

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/brevo"
	"github.com/sislelabs/mailctl/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type DetailModel struct {
	cfg     *internal.Config
	domain  string
	width   int
	height  int
	loading bool
	data    *detailData
}

type detailData struct {
	cfEnabled    *bool
	cfError      string
	rules        []ruleInfo
	rulesError   string
	brevoAuthenticated *bool
	brevoVerified      *bool
	brevoError         string
	brevoDNS           []brevo.DNSRecord
	dnsRecords   []dnsRecordInfo
	mxRecords    []mxRecordInfo
	mxError      string
	issues       int
}

type ruleInfo struct {
	address   string
	forwardTo string
	found     bool
}

type dnsRecordInfo struct {
	recType string
	name    string
	status  string
}

type mxRecordInfo struct {
	name     string
	content  string
	priority int
}

type detailFetchedMsg struct{ data *detailData }

func NewDetailModel(cfg *internal.Config, domain string, width, height int) DetailModel {
	return DetailModel{
		cfg:     cfg,
		domain:  domain,
		width:   width,
		height:  height,
		loading: true,
	}
}

func (m DetailModel) Init() tea.Cmd {
	return m.fetch()
}

func (m DetailModel) fetch() tea.Cmd {
	cfg := m.cfg
	domain := m.domain
	return func() tea.Msg {
		d := cfg.FindDomain(domain)
		if d == nil {
			return detailFetchedMsg{data: &detailData{}}
		}

		cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
		bv := brevo.NewClient(cfg.BrevoAPIKey)
		data := &detailData{}

		// CF routing status — if rules exist, routing works even if status check fails
		status, err := cf.GetEmailRoutingStatus(d.CloudflareZoneID)
		if err != nil {
			// Don't count as issue — this endpoint often fails due to token permissions
			// but routing still works if rules exist
			data.cfError = "could not check (token may lack permission)"
		} else {
			data.cfEnabled = &status.Enabled
			if !status.Enabled {
				data.issues++
			}
		}

		// Routing rules
		rules, err := cf.ListRoutingRules(d.CloudflareZoneID)
		if err != nil {
			data.rulesError = err.Error()
			data.issues++
		} else {
			for _, a := range d.Aliases {
				addr := fmt.Sprintf("%s@%s", a.Alias, d.Domain)
				ri := ruleInfo{address: addr}
				for _, r := range rules {
					for _, matcher := range r.Matchers {
						if matcher.Value == addr {
							ri.found = true
							if len(r.Actions) > 0 && len(r.Actions[0].Value) > 0 {
								ri.forwardTo = ui.MaskEmail(r.Actions[0].Value[0])
							}
							break
						}
					}
					if ri.found {
						break
					}
				}
				if !ri.found {
					data.issues++
				}
				data.rules = append(data.rules, ri)
			}
		}

		// Brevo domain
		bDomain, err := bv.GetDomain(d.Domain)
		if err != nil {
			data.brevoError = err.Error()
		} else {
			data.brevoAuthenticated = &bDomain.Authenticated
			data.brevoVerified = &bDomain.Verified
			data.brevoDNS = bDomain.FlatDNSRecords()
		}

		// MX records
		mxRecords, err := cf.ListDNSRecords(d.CloudflareZoneID, "MX")
		if err != nil {
			data.mxError = err.Error()
			data.issues++
		} else {
			for _, mx := range mxRecords {
				pri := 0
				if mx.Priority != nil {
					pri = *mx.Priority
				}
				data.mxRecords = append(data.mxRecords, mxRecordInfo{
					name:     mx.Name,
					content:  mx.Content,
					priority: pri,
				})
			}
		}

		return detailFetchedMsg{data: data}
	}
}

func (m DetailModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height - 4
		return m, nil

	case detailFetchedMsg:
		m.data = msg.data
		m.loading = false
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return SwitchViewMsg{View: ViewList} }
		case "q":
			return m, tea.Quit
		case "r":
			m.loading = true
			return m, m.fetch()
		}
	}

	return m, nil
}

func (m DetailModel) View() string {
	if m.loading {
		return "\n  " + ui.IconPending + " " + ui.Dim.Render("Loading health check for "+m.domain+"...")
	}

	if m.data == nil {
		return "\n  " + ui.IconError + " " + ui.Error.Render("No data")
	}

	var sections []string

	// ── CF Routing ────
	{
		var rows []string
		if m.data.cfError != "" {
			rows = append(rows, ui.IconWarn+" "+ui.Dim.Render(m.data.cfError))
		} else if m.data.cfEnabled != nil {
			if *m.data.cfEnabled {
				rows = append(rows, ui.IconSuccess+" "+ui.Success.Render("Enabled"))
			} else {
				rows = append(rows, ui.IconError+" "+ui.Error.Render("Disabled"))
			}
		}
		sections = append(sections, renderSection("Email Routing", rows))
	}

	// ── Routing Rules ────
	{
		var rows []string
		if m.data.rulesError != "" {
			rows = append(rows, ui.IconError+" "+ui.Error.Render("Error: ")+ui.Dim.Render(m.data.rulesError))
		} else {
			for _, r := range m.data.rules {
				if r.found {
					rows = append(rows, ui.IconSuccess+" "+ui.White.Render(r.address)+" "+ui.Dim.Render("→ "+r.forwardTo))
				} else {
					rows = append(rows, ui.IconError+" "+ui.Error.Render(r.address)+" "+ui.Dim.Render("— no rule found"))
				}
			}
		}
		sections = append(sections, renderSection("Routing Rules", rows))
	}

	// ── Brevo ────
	{
		var rows []string
		if m.data.brevoError != "" {
			rows = append(rows, ui.IconWarn+" "+ui.Dim.Render("Not configured in Brevo"))
		} else if m.data.brevoAuthenticated != nil {
			if *m.data.brevoAuthenticated {
				rows = append(rows, ui.IconSuccess+" "+ui.Success.Render("Authenticated")+" "+ui.Dim.Render("— sending ready"))
			} else if m.data.brevoVerified != nil && *m.data.brevoVerified {
				rows = append(rows, ui.IconPending+" "+ui.Info.Render("Verified")+" "+ui.Dim.Render("— DKIM pending"))
			} else {
				rows = append(rows, ui.IconPending+" "+ui.Info.Render("Pending")+" "+ui.Dim.Render("— waiting for DNS"))
			}
			for _, rec := range m.data.brevoDNS {
				icon := ui.IconPending
				statusText := "pending"
				if rec.Status {
					icon = ui.IconSuccess
					statusText = "verified"
				}
				name := brevo.FullRecordName(rec, m.domain)
				rows = append(rows, "  "+icon+" "+ui.Dim.Render(rec.Type)+" "+ui.White.Render(name)+" "+ui.Dim.Render(statusText))
			}
		} else {
			rows = append(rows, ui.IconWarn+" "+ui.Dim.Render("Not configured"))
		}
		sections = append(sections, renderSection("Brevo", rows))
	}

	// ── MX Records ────
	{
		var rows []string
		if m.data.mxError != "" {
			rows = append(rows, ui.IconError+" "+ui.Error.Render("Error: ")+ui.Dim.Render(m.data.mxError))
		} else if len(m.data.mxRecords) == 0 {
			rows = append(rows, ui.IconWarn+" "+ui.Warn.Render("No MX records"))
		} else {
			for _, mx := range m.data.mxRecords {
				rows = append(rows, ui.IconSuccess+" "+ui.White.Render(mx.name)+" "+
					ui.Dim.Render("→ "+fmt.Sprintf("%s (pri %d)", mx.content, mx.priority)))
			}
		}
		sections = append(sections, renderSection("MX Records", rows))
	}

	// Summary
	var summary string
	if m.data.issues == 0 {
		summary = ui.IconSuccess + " " + ui.Success.Bold(true).Render("All healthy")
	} else {
		summary = ui.IconError + " " + ui.Error.Bold(true).Render(fmt.Sprintf("%d issue(s)", m.data.issues))
	}

	content := strings.Join(sections, "\n") + "\n" + summary

	borderColor := ui.ColorGreen
	if m.data.issues > 0 {
		borderColor = ui.ColorRed
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Render(content)

	return "\n" + box
}

func renderSection(title string, rows []string) string {
	t := ui.SectionTitle.Render(title)
	return t + "\n" + strings.Join(rows, "\n") + "\n"
}
