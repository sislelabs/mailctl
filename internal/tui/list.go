package tui

import (
	"strings"
	"time"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/brevo"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Domain status cache ─────────────────────────────────────────────────────

var (
	cachedRows    []domainRow
	cachedAt      time.Time
	cacheTTL      = 5 * time.Minute
)

func domainCacheValid() bool {
	return len(cachedRows) > 0 && time.Since(cachedAt) < cacheTTL
}

type domainRow struct {
	domain       string
	cfStatus     string
	cfIcon       string
	brevoStatus string
	brevoIcon   string
	aliases      string
}

type ListModel struct {
	cfg       *internal.Config
	rows      []domainRow
	cursor    int
	filter    textinput.Model
	filtering bool
	loading   bool
	width     int
}

type domainStatusMsg struct {
	rows []domainRow
}

func NewListModel(cfg *internal.Config) ListModel {
	fi := textinput.New()
	fi.Placeholder = "filter..."
	fi.CharLimit = 100
	fi.Width = 20

	return ListModel{
		cfg:     cfg,
		loading: true,
		filter:  fi,
		width:   120,
	}
}

func (m ListModel) Init() tea.Cmd {
	if domainCacheValid() {
		return func() tea.Msg { return domainStatusMsg{rows: cachedRows} }
	}
	return m.fetchStatuses()
}

func (m ListModel) fetchStatuses() tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
		bv := brevo.NewClient(cfg.BrevoAPIKey)

		var rows []domainRow
		for _, d := range cfg.Domains {
			row := domainRow{domain: d.Domain}

			row.cfIcon = ui.IconWarn
			row.cfStatus = ui.Dim.Render("unknown")
			if status, err := cf.GetEmailRoutingStatus(d.CloudflareZoneID); err == nil {
				if status.Enabled {
					row.cfIcon = ui.IconSuccess
					row.cfStatus = ui.Success.Render("enabled")
				} else {
					row.cfIcon = ui.IconError
					row.cfStatus = ui.Error.Render("disabled")
				}
			}

			row.brevoIcon = ui.Dim.Render("·")
			row.brevoStatus = ui.Dim.Render("n/a")
			if bDomain, err := bv.GetDomain(d.Domain); err == nil {
				if bDomain.Authenticated {
					row.brevoIcon = ui.IconSuccess
					row.brevoStatus = ui.Success.Render("authenticated")
				} else if bDomain.Verified {
					row.brevoIcon = ui.IconPending
					row.brevoStatus = ui.Info.Render("verified")
				} else {
					row.brevoIcon = ui.IconPending
					row.brevoStatus = ui.Info.Render("pending")
				}
			}

			var als []string
			for _, a := range d.Aliases {
				als = append(als, a.Alias+"@")
			}
			row.aliases = strings.Join(als, " ")

			rows = append(rows, row)
		}
		cachedRows = rows
		cachedAt = time.Now()
		return domainStatusMsg{rows: rows}
	}
}

func (m ListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case domainStatusMsg:
		m.rows = msg.rows
		m.loading = false
		return m, nil

	case tea.KeyMsg:
		if m.filtering {
			switch msg.String() {
			case "esc":
				m.filtering = false
				m.filter.Blur()
				m.filter.SetValue("")
				return m, nil
			case "enter":
				m.filtering = false
				m.filter.Blur()
				return m, nil
			}
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.cursor = 0
			return m, cmd
		}

		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "/":
			m.filtering = true
			m.filter.Focus()
			return m, textinput.Blink
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			filtered := m.filteredRows()
			if m.cursor < len(filtered)-1 {
				m.cursor++
			}
		case "enter":
			domain := m.SelectedDomain()
			if domain != "" {
				return m, func() tea.Msg { return DomainSelectedMsg{Domain: domain} }
			}
		case "a":
			return m, func() tea.Msg { return SwitchViewMsg{View: ViewAddDomain} }
		case "d":
			domain := m.SelectedDomain()
			if domain != "" {
				return m, func() tea.Msg { return SwitchViewMsg{View: ViewDeleteConfirm} }
			}
		case "tab":
			domain := m.SelectedDomain()
			if domain != "" {
				return m, func() tea.Msg { return SwitchViewMsg{View: ViewAliases} }
			}
		case "r":
			cachedRows = nil
			m.loading = true
			return m, m.fetchStatuses()
		case "f":
			return m, func() tea.Msg { return SwitchViewMsg{View: ViewFlows} }
		}
	}

	return m, nil
}

func (m ListModel) filteredRows() []domainRow {
	filter := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if filter == "" {
		return m.rows
	}
	var filtered []domainRow
	for _, r := range m.rows {
		if strings.Contains(strings.ToLower(r.domain), filter) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func (m ListModel) SelectedDomain() string {
	filtered := m.filteredRows()
	if m.cursor >= 0 && m.cursor < len(filtered) {
		return filtered[m.cursor].domain
	}
	return ""
}

func (m ListModel) View() string {
	var b strings.Builder

	// Logo — always shown
	logo := lipgloss.NewStyle().Foreground(ui.ColorAccent).Bold(true).Render(
		"███╗   ███╗ █████╗ ██╗██╗      ██████╗████████╗██╗\n" +
			"████╗ ████║██╔══██╗██║██║     ██╔════╝╚══██╔══╝██║\n" +
			"██╔████╔██║███████║██║██║     ██║        ██║   ██║\n" +
			"██║╚██╔╝██║██╔══██║██║██║     ██║        ██║   ██║\n" +
			"██║ ╚═╝ ██║██║  ██║██║███████╗╚██████╗   ██║   ███████╗\n" +
			"╚═╝     ╚═╝╚═╝  ╚═╝╚═╝╚══════╝ ╚═════╝   ╚═╝   ╚══════╝")

	b.WriteString("\n\n" + logo + "\n\n")

	if len(m.cfg.Domains) == 0 {
		b.WriteString(ui.Muted.Render("No domains configured.") + "\n\n")
		b.WriteString(ui.Dim.Render("Press ") + ui.Accent.Render("a") + ui.Dim.Render(" to add your first domain.") + "\n")
		return b.String()
	}

	// Filter
	if m.filtering {
		b.WriteString(ui.Accent.Render("/") + " " + m.filter.View() + "\n\n")
	} else if m.filter.Value() != "" {
		b.WriteString(ui.Dim.Render("filter: ") + ui.Accent.Render(m.filter.Value()) + "\n\n")
	}

	// Table
	colDomain := 28
	colCF := 16
	colBrevo := 16

	header := lipgloss.JoinHorizontal(lipgloss.Top,
		ui.TableHeader.Width(colDomain).Render("DOMAIN"),
		ui.TableHeader.Width(colCF).Render("ROUTING"),
		ui.TableHeader.Width(colBrevo).Render("SENDING"),
		ui.TableHeader.Render("ALIASES"),
	)
	b.WriteString(header + "\n")
	b.WriteString(ui.Dim.Render(strings.Repeat("─", colDomain+colCF+colBrevo+10)) + "\n")

	if m.loading {
		// Show domain names with loading indicators
		for i, d := range m.cfg.Domains {
			selected := i == m.cursor

			domainStyle := ui.Muted
			if selected {
				domainStyle = ui.White.Bold(true)
			}

			cursor := "  "
			if selected {
				cursor = ui.Accent.Render("▸ ")
			}

			var als []string
			for _, a := range d.Aliases {
				als = append(als, a.Alias+"@")
			}

			line := lipgloss.JoinHorizontal(lipgloss.Top,
				domainStyle.Width(colDomain).Render(d.Domain),
				lipgloss.NewStyle().Width(colCF).Render(ui.IconPending+" "+ui.Dim.Render("···")),
				lipgloss.NewStyle().Width(colBrevo).Render(ui.IconPending+" "+ui.Dim.Render("···")),
				ui.Dim.Render(strings.Join(als, " ")),
			)
			b.WriteString(cursor + line + "\n")
		}
		return b.String()
	}

	filtered := m.filteredRows()
	for i, row := range filtered {
		selected := i == m.cursor

		domainStyle := ui.Muted
		if selected {
			domainStyle = ui.White.Bold(true)
		}

		cursor := "  "
		if selected {
			cursor = ui.Accent.Render("▸ ")
		}

		line := lipgloss.JoinHorizontal(lipgloss.Top,
			domainStyle.Width(colDomain).Render(row.domain),
			lipgloss.NewStyle().Width(colCF).Render(row.cfIcon+" "+row.cfStatus),
			lipgloss.NewStyle().Width(colBrevo).Render(row.brevoIcon+" "+row.brevoStatus),
			ui.Dim.Render(row.aliases),
		)

		b.WriteString(cursor + line + "\n")
	}

	return b.String()
}
