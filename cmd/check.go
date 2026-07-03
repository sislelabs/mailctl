package cmd

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/brevo"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/resend"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check [domain]",
	Short: "Deep health check for DNS, routing, and Brevo status",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runCheck,
}

func runCheck(cmd *cobra.Command, args []string) error {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return err
	}

	if len(args) == 1 {
		d := cfg.FindDomain(args[0])
		if d == nil {
			return fmt.Errorf("domain %s not found in config", args[0])
		}
		checkDomain(cfg, d)
		return nil
	}

	if len(cfg.Domains) == 0 {
		fmt.Println(ui.Dim.Render("  No domains configured."))
		return nil
	}

	for i := range cfg.Domains {
		checkDomain(cfg, &cfg.Domains[i])
	}
	return nil
}

func checkDomain(cfg *internal.Config, d *internal.DomainConfig) {
	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)

	issues := 0
	var sections []string

	header := lipgloss.NewStyle().
		Foreground(ui.ColorAccent).
		Bold(true).
		Render("  " + d.Domain)

	// ── Cloudflare Email Routing ────────────────────────────────────
	{
		title := ui.SectionTitle.Render("Cloudflare Email Routing")
		var rows []string
		status, err := cf.GetEmailRoutingStatus(d.CloudflareZoneID)
		if err != nil {
			rows = append(rows, ui.StepResult(ui.IconWarn, ui.Dim.Render("Could not check (token may lack permission)")))
		} else if status.Enabled {
			rows = append(rows, ui.StepResult(ui.IconSuccess, ui.Success.Render("Enabled")))
		} else {
			rows = append(rows, ui.StepResult(ui.IconError, ui.Error.Render("Disabled")))
			issues++
		}
		sections = append(sections, title+"\n"+strings.Join(rows, "\n"))
	}

	// ── Routing Rules ───────────────────────────────────────────────
	{
		title := ui.SectionTitle.Render("Routing Rules")
		var rows []string
		rules, err := cf.ListRoutingRules(d.CloudflareZoneID)
		if err != nil {
			rows = append(rows, ui.StepResult(ui.IconError, ui.Error.Render("Could not list rules: ")+ui.Dim.Render(err.Error())))
			issues++
		} else {
			for _, a := range d.Aliases {
				addr := fmt.Sprintf("%s@%s", a.Alias, d.Domain)
				found := false
				for _, r := range rules {
					for _, m := range r.Matchers {
						if m.Value == addr {
							fwd := "?"
							if len(r.Actions) > 0 && len(r.Actions[0].Value) > 0 {
								fwd = ui.MaskEmail(r.Actions[0].Value[0])
							}
							rows = append(rows, ui.StepResult(ui.IconSuccess,
								ui.White.Render(addr)+" "+ui.Dim.Render("→")+" "+ui.Dim.Render(fwd)))
							found = true
							break
						}
					}
					if found {
						break
					}
				}
				if !found {
					rows = append(rows, ui.StepResult(ui.IconError,
						ui.Error.Render(addr)+" "+ui.Dim.Render("— no routing rule found")))
					issues++
				}
			}
		}
		sections = append(sections, title+"\n"+strings.Join(rows, "\n"))
	}

	// ── Catch-All ──────────────────────────────────────────────────
	{
		title := ui.SectionTitle.Render("Catch-All")
		var rows []string
		catchAll, err := cf.GetCatchAllRule(d.CloudflareZoneID)
		if err != nil {
			rows = append(rows, ui.StepResult(ui.IconWarn, ui.Dim.Render("Could not check")))
		} else if catchAll.Enabled && len(catchAll.Actions) > 0 && catchAll.Actions[0].Type == "forward" {
			fwd := "?"
			if len(catchAll.Actions[0].Value) > 0 {
				fwd = ui.MaskEmail(catchAll.Actions[0].Value[0])
			}
			rows = append(rows, ui.StepResult(ui.IconSuccess,
				ui.Success.Render("Enabled")+" "+ui.Dim.Render("→ "+fwd)))
		} else {
			rows = append(rows, ui.StepResult(ui.IconWarn,
				ui.Warn.Render("Disabled")+" "+ui.Dim.Render("— unmatched emails will be dropped")))
		}
		sections = append(sections, title+"\n"+strings.Join(rows, "\n"))
	}

	// ── Sending Domain (provider-specific) ─────────────────────────
	if cfg.SendingProvider() == internal.ProviderResend {
		sections = append(sections, checkResendDomain(cfg, d))
	} else {
		sections = append(sections, checkBrevoDomain(cfg, d))
	}

	// ── MX Records ──────────────────────────────────────────────────
	{
		title := ui.SectionTitle.Render("MX Records")
		var rows []string
		mxRecords, err := cf.ListDNSRecords(d.CloudflareZoneID, "MX")
		if err != nil {
			rows = append(rows, ui.StepResult(ui.IconError, ui.Error.Render("Could not list: ")+ui.Dim.Render(err.Error())))
			issues++
		} else if len(mxRecords) == 0 {
			rows = append(rows, ui.StepResult(ui.IconWarn, ui.Warn.Render("No MX records found")))
		} else {
			for _, mx := range mxRecords {
				pri := 0
				if mx.Priority != nil {
					pri = *mx.Priority
				}
				rows = append(rows, ui.StepResult(ui.IconSuccess,
					ui.White.Render(mx.Name)+" "+ui.Dim.Render("→")+" "+
						ui.Dim.Render(fmt.Sprintf("%s (priority %d)", mx.Content, pri))))
			}
		}
		sections = append(sections, title+"\n"+strings.Join(rows, "\n"))
	}

	var summary string
	if issues == 0 {
		summary = ui.IconSuccess + " " + ui.Success.Bold(true).Render("All healthy")
	} else {
		summary = ui.IconError + " " + ui.Error.Bold(true).Render(fmt.Sprintf("%d issue(s) found", issues))
	}

	content := strings.Join(sections, "\n")
	borderColor := ui.ColorGreen
	if issues > 0 {
		borderColor = ui.ColorRed
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Render(content + "\n\n" + summary)

	fmt.Println()
	fmt.Println(header)
	fmt.Println(box)
	fmt.Println()
}

// checkBrevoDomain renders the Brevo sending-domain section for the health check.
func checkBrevoDomain(cfg *internal.Config, d *internal.DomainConfig) string {
	bv := brevo.NewClient(cfg.BrevoAPIKey)
	title := ui.SectionTitle.Render("Brevo Domain")
	var rows []string
	bDomain, err := bv.GetDomain(d.Domain)
	if err != nil {
		rows = append(rows, ui.StepResult(ui.IconWarn, ui.Dim.Render("Not configured in Brevo")))
	} else {
		if bDomain.Authenticated {
			rows = append(rows, ui.StepResult(ui.IconSuccess, ui.Success.Render("Authenticated")+" "+ui.Dim.Render("— sending ready")))
		} else if bDomain.Verified {
			rows = append(rows, ui.StepResult(ui.IconPending, ui.Info.Render("Verified")+" "+ui.Dim.Render("— DKIM not yet authenticated")))
		} else {
			rows = append(rows, ui.StepResult(ui.IconPending, ui.Info.Render("Pending")+" "+ui.Dim.Render("— waiting for DNS verification")))
		}

		rows = append(rows, "")
		rows = append(rows, "  "+ui.Dim.Render("DNS Records:"))
		for _, rec := range bDomain.FlatDNSRecords() {
			icon := ui.IconPending
			statusText := "pending"
			if rec.Status {
				icon = ui.IconSuccess
				statusText = "verified"
			}
			name := brevo.FullRecordName(rec, d.Domain)
			rows = append(rows, fmt.Sprintf("    %s %s %s %s",
				icon,
				ui.Dim.Render(rec.Type),
				ui.White.Render(name),
				ui.Dim.Render(statusText)))
		}
	}
	return title + "\n" + strings.Join(rows, "\n")
}

// checkResendDomain renders the Resend sending-domain section for the health check.
func checkResendDomain(cfg *internal.Config, d *internal.DomainConfig) string {
	rc := resend.NewClient(cfg.ResendAPIKey)
	title := ui.SectionTitle.Render("Resend Domain")
	var rows []string

	// Prefer the stored ID; fall back to a name lookup for domains added
	// before the ID was persisted.
	var rd *resend.Domain
	var err error
	if d.ResendDomainID != "" {
		rd, err = rc.GetDomain(d.ResendDomainID)
	} else {
		rd, err = rc.FindDomainByName(d.Domain)
	}

	if err != nil || rd == nil {
		rows = append(rows, ui.StepResult(ui.IconWarn, ui.Dim.Render("Not configured in Resend")))
		return title + "\n" + strings.Join(rows, "\n")
	}

	if rd.Authenticated() {
		rows = append(rows, ui.StepResult(ui.IconSuccess, ui.Success.Render("Verified")+" "+ui.Dim.Render("— sending ready")))
	} else {
		rows = append(rows, ui.StepResult(ui.IconPending, ui.Info.Render(titleCase(rd.Status))+" "+ui.Dim.Render("— waiting for DNS verification")))
	}

	rows = append(rows, "")
	rows = append(rows, "  "+ui.Dim.Render("DNS Records:"))
	for _, rec := range rd.Records {
		icon := ui.IconPending
		statusText := "pending"
		if strings.EqualFold(rec.Status, "verified") {
			icon = ui.IconSuccess
			statusText = "verified"
		}
		name := resendRecordName(rec.Name, d.Domain)
		rows = append(rows, fmt.Sprintf("    %s %s %s %s",
			icon,
			ui.Dim.Render(rec.Type),
			ui.White.Render(name),
			ui.Dim.Render(statusText)))
	}
	return title + "\n" + strings.Join(rows, "\n")
}

// titleCase upper-cases the first rune of s (ASCII), used for status labels.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
