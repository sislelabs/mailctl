package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/brevo"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/mailsetup"
	"github.com/sislelabs/mailctl/internal/resend"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check [domain]",
	Short: "Deep health check for DNS, routing, and sending-provider status",
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

	var tally checkTally
	var sections []string

	header := lipgloss.NewStyle().
		Foreground(ui.ColorAccent).
		Bold(true).
		Render("  " + d.Domain)

	// Receiving sections apply only to mailbox domains. A sending domain does
	// not receive by design, and its zone is usually shared with unrelated
	// mail, so reporting that zone's routing as this domain's problem is wrong
	// twice over.
	if !d.IsSending() {
		{
			// ── Cloudflare Email Routing ────────────────────────────────────
			title := ui.SectionTitle.Render("Cloudflare Email Routing")
			var rows []string
			status, err := cf.GetEmailRoutingStatus(d.CloudflareZoneID)
			if err != nil {
				rows = append(rows, ui.StepResult(ui.IconWarn, ui.Warn.Render("Could not check")+" "+ui.Dim.Render("— token may lack Zone Settings")))
				tally.warnings++
			} else if status.Enabled {
				rows = append(rows, ui.StepResult(ui.IconSuccess, ui.Success.Render("Enabled")))
			} else {
				rows = append(rows, ui.StepResult(ui.IconError, ui.Error.Render("Disabled")))
				tally.problems++
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
				tally.problems++
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
						tally.problems++
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
				rows = append(rows, ui.StepResult(ui.IconWarn, ui.Warn.Render("Could not check")))
				tally.warnings++
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
				tally.warnings++
			}
			sections = append(sections, title+"\n"+strings.Join(rows, "\n"))
		}
	}

	// ── Sending Domain (provider-specific) ─────────────────────────
	if cfg.SendingProvider() == internal.ProviderResend {
		section, n := checkResendDomain(cfg, d)
		sections = append(sections, section)
		tally.problems += n
	} else {
		section, n := checkBrevoDomain(cfg, d)
		sections = append(sections, section)
		tally.problems += n
	}

	// ── MX Records ──────────────────────────────────────────────────
	{
		title := ui.SectionTitle.Render("MX Records")
		var rows []string
		mxRecords, err := cf.ListDNSRecords(d.CloudflareZoneID, "MX")
		if err != nil {
			rows = append(rows, ui.StepResult(ui.IconError, ui.Error.Render("Could not list: ")+ui.Dim.Render(err.Error())))
			tally.problems++
		} else if len(mxRecords) == 0 {
			rows = append(rows, ui.StepResult(ui.IconError, ui.Error.Render("No MX records")+" "+ui.Dim.Render("— nothing can be received")))
			tally.problems++
		} else {
			// Any MX record satisfied the old check, so a domain carrying only
			// the provider's return-path MX on a send. subdomain reported
			// healthy while nothing could actually be delivered to it.
			apexRouting := false
			for _, mx := range mxRecords {
				pri := 0
				if mx.Priority != nil {
					pri = *mx.Priority
				}
				if strings.EqualFold(mx.Name, d.Domain) &&
					strings.Contains(strings.ToLower(mx.Content), mailsetup.RoutingMXHost) {
					apexRouting = true
				}
				rows = append(rows, ui.StepResult(ui.IconSuccess,
					ui.White.Render(mx.Name)+" "+ui.Dim.Render("→")+" "+
						ui.Dim.Render(fmt.Sprintf("%s (priority %d)", mx.Content, pri))))
			}
			if !apexRouting && !d.IsSending() {
				rows = append(rows, ui.StepResult(ui.IconError,
					ui.Error.Render("No routing MX at "+d.Domain)+" "+
						ui.Dim.Render("— nothing can be received; Email Routing is not enabled")))
				tally.problems++
			}
		}
		sections = append(sections, title+"\n"+strings.Join(rows, "\n"))
	}

	summary := tally.summary()

	content := strings.Join(sections, "\n")
	borderColor := tally.borderColor()

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

// checkBrevoDomain renders the Brevo sending-domain section and reports how
// many problems it found.
func checkBrevoDomain(cfg *internal.Config, d *internal.DomainConfig) (string, int) {
	bv := brevo.NewClient(cfg.BrevoAPIKey)
	title := ui.SectionTitle.Render("Brevo Domain")
	var rows []string
	bDomain, err := bv.GetDomain(d.Domain)
	if err != nil {
		rows = append(rows, ui.StepResult(ui.IconError, ui.Error.Render("Not configured in Brevo")+" "+ui.Dim.Render("— cannot send from this domain")))
		return title + "\n" + strings.Join(rows, "\n"), 1
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
	return title + "\n" + strings.Join(rows, "\n"), 0
}

// checkResendDomain renders the Resend sending-domain section and reports how
// many problems it found.
func checkResendDomain(cfg *internal.Config, d *internal.DomainConfig) (string, int) {
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
		rows = append(rows, ui.StepResult(ui.IconError, ui.Error.Render("Not configured in Resend")+" "+ui.Dim.Render("— cannot send from this domain")))
		return title + "\n" + strings.Join(rows, "\n"), 1
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
		name := mailsetup.ResendRecordName(rec.Name, d.ZoneName())
		rows = append(rows, fmt.Sprintf("    %s %s %s %s",
			icon,
			ui.Dim.Render(rec.Type),
			ui.White.Render(name),
			ui.Dim.Render(statusText)))
	}
	return title + "\n" + strings.Join(rows, "\n"), 0
}

// titleCase upper-cases the first rune of s (ASCII), used for status labels.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// checkTally separates findings that are definitely wrong from ones that are
// merely unverifiable or degraded. Collapsing the two meant a domain that
// could not send still reported "All healthy", because only hard errors were
// counted and everything else rendered as an uncounted warning row.
type checkTally struct {
	// problems are findings that break sending or receiving.
	problems int
	// warnings are findings that degrade the setup or could not be verified.
	warnings int
}

func (t checkTally) summary() string {
	switch {
	case t.problems > 0 && t.warnings > 0:
		return ui.IconError + " " + ui.Error.Bold(true).Render(
			fmt.Sprintf("%d issue(s), %d warning(s)", t.problems, t.warnings))
	case t.problems > 0:
		return ui.IconError + " " + ui.Error.Bold(true).Render(
			fmt.Sprintf("%d issue(s) found", t.problems))
	case t.warnings > 0:
		return ui.IconWarn + " " + ui.Warn.Bold(true).Render(
			fmt.Sprintf("%d warning(s) — nothing broken", t.warnings))
	default:
		return ui.IconSuccess + " " + ui.Success.Bold(true).Render("All healthy")
	}
}

func (t checkTally) borderColor() lipgloss.Color {
	switch {
	case t.problems > 0:
		return ui.ColorRed
	case t.warnings > 0:
		return ui.ColorYellow
	default:
		return ui.ColorGreen
	}
}
