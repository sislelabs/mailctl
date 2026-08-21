package cmd

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/brevo"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/mailsetup"
	"github.com/sislelabs/mailctl/internal/resend"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/spf13/cobra"
)

var removeCmd = &cobra.Command{
	Use:     "remove [domain]",
	Aliases: []string{"rm"},
	Short:   "Tear down email setup for a domain",
	Args:    cobra.ExactArgs(1),
	RunE:    runRemove,
}

var (
	removeForce  bool
	removeDryRun bool
)

func init() {
	removeCmd.Flags().BoolVarP(&removeForce, "force", "f", false, "Skip confirmation")
	removeCmd.Flags().BoolVar(&removeDryRun, "dry-run", false, "Show what would be removed without changing anything")
}

func runRemove(cmd *cobra.Command, args []string) error {
	domain := args[0]

	cfg, err := internal.LoadConfig()
	if err != nil {
		return err
	}

	d := cfg.FindDomain(domain)
	if d == nil {
		return fmt.Errorf("domain %s not found in config", domain)
	}

	providerName := "Brevo"
	if cfg.SendingProvider() == internal.ProviderResend {
		providerName = "Resend"
	}

	if removeDryRun {
		return previewRemove(cfg, d, providerName)
	}

	if !removeForce {
		items := []string{
			ui.IconDot + " Cloudflare routing rules for @" + domain,
			ui.IconDot + " Catch-all forwarding",
			ui.IconDot + " " + providerName + " domain",
			ui.IconDot + " " + dnsScopeSummary(d),
			ui.IconDot + " Config entry",
		}

		message := fmt.Sprintf("This will remove all email setup for %s:\n\n%s",
			ui.Error.Bold(true).Render(domain),
			strings.Join(items, "\n"))

		confirmed, err := ui.RunConfirm(message, domain)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println(ui.Dim.Render("  Aborted."))
			return nil
		}
	}

	provider := cfg.SendingProvider()

	stepLabels := []string{
		"Delete routing rules",
		"Disable catch-all",
		"Delete " + providerName + " domain",
		"Delete DNS records created by mailctl",
		"Remove from config",
	}

	err = ui.RunProgress("Removing "+ui.Error.Bold(true).Render(domain), stepLabels, func(p *ui.ProgressRunner) {
		cf := cloudflare.NewClient(cfg.CloudflareAPIToken)

		// Step 0: Delete routing rules
		p.Start(0)
		rules, err := cf.ListRoutingRules(d.CloudflareZoneID)
		if err != nil {
			p.Warn(0, err.Error())
		} else {
			count := 0
			for _, rule := range rules {
				for _, m := range rule.Matchers {
					if strings.HasSuffix(m.Value, "@"+domain) {
						if err := cf.DeleteRoutingRule(d.CloudflareZoneID, rule.ID); err != nil {
							p.SubRow(0, ui.IconError+" "+ui.Dim.Render(m.Value))
						} else {
							p.SubRow(0, ui.IconSuccess+" "+ui.Dim.Render(m.Value))
							count++
						}
						break
					}
				}
			}
			p.Done(0, fmt.Sprintf("%d deleted", count))
		}

		// Step 1: Disable catch-all. The catch-all lives at its own endpoint and
		// never appears in the rules list, so without this every address on the
		// domain keeps forwarding after teardown.
		p.Start(1)
		if err := mailsetup.DisableCatchAll(cf, d.CloudflareZoneID); err != nil {
			p.Warn(1, err.Error())
		} else {
			p.Done(1, ui.Dim.Render("*@"+domain+" no longer forwards"))
		}

		// Step 2: Delete the sending-provider domain
		p.Start(2)
		if provider == internal.ProviderResend {
			rc := resend.NewClient(cfg.ResendAPIKey)
			id := d.ResendDomainID
			if id == "" {
				// Fall back to a name lookup for domains added before the ID
				// was persisted.
				if rd, err := rc.FindDomainByName(domain); err == nil && rd != nil {
					id = rd.ID
				}
			}
			if id == "" {
				p.Warn(2, "not found or already deleted")
			} else if err := rc.DeleteDomain(id); err != nil {
				p.Warn(2, "not found or already deleted")
			} else {
				p.Done(2, "")
			}
		} else {
			bv := brevo.NewClient(cfg.BrevoAPIKey)
			if err := bv.DeleteDomain(domain); err != nil {
				p.Warn(2, "not found or already deleted")
			} else {
				p.Done(2, "")
			}
		}

		// Step 3: Delete only the DNS records mailctl created.
		p.Start(3)
		result := mailsetup.TeardownDNS(cf, d.CloudflareZoneID, d.ManagedDNSRecordIDs)
		if result.Untracked {
			p.Warn(3, "skipped — no records tracked for this domain")
			p.SubRow(3, ui.Dim.Render("Added before mailctl tracked record ownership."))
			p.SubRow(3, ui.Dim.Render("Delete its SPF/DKIM records by hand so other services on"))
			p.SubRow(3, ui.Dim.Render("this zone keep theirs."))
		} else {
			for _, line := range result.Deleted {
				p.SubRow(3, ui.IconSuccess+" "+ui.Dim.Render(line))
			}
			for _, line := range result.Failed {
				p.SubRow(3, ui.IconWarn+" "+ui.Dim.Render(line))
			}
			p.Done(3, fmt.Sprintf("%d deleted", len(result.Deleted)))
		}

		// Step 4: Remove from config
		p.Start(4)
		cfg.RemoveDomain(domain)
		if err := internal.SaveConfig(cfg); err != nil {
			p.Fail(4, err.Error())
			return
		}
		p.Done(4, "")
	})

	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println(ui.SuccessPanel.Render(
		ui.IconSuccess + " " + ui.Success.Bold(true).Render(domain+" removed"),
	))
	fmt.Println()

	return nil
}

// dnsScopeSummary describes, for the confirmation prompt, exactly which DNS
// records teardown will touch.
func dnsScopeSummary(d *internal.DomainConfig) string {
	n := len(d.ManagedDNSRecordIDs)
	if n == 0 {
		return "No DNS records (none tracked — nothing will be deleted)"
	}
	return fmt.Sprintf("%d DNS records created by mailctl", n)
}

// previewRemove reports what a teardown would do without changing anything.
func previewRemove(cfg *internal.Config, d *internal.DomainConfig, providerName string) error {
	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	domain := d.Domain

	fmt.Println()
	fmt.Println(ui.Highlight.Render("  Dry run — nothing will be changed"))
	fmt.Println()

	fmt.Println(ui.Heading("  Routing rules to delete"))
	rules, err := cf.ListRoutingRules(d.CloudflareZoneID)
	if err != nil {
		fmt.Println(ui.StepResult(ui.IconWarn, ui.Dim.Render(err.Error())))
	} else {
		found := 0
		for _, rule := range rules {
			for _, m := range rule.Matchers {
				if strings.HasSuffix(m.Value, "@"+domain) {
					fmt.Println(ui.StepResult(ui.IconDot, ui.Dim.Render(m.Value)))
					found++
					break
				}
			}
		}
		if found == 0 {
			fmt.Println(ui.StepResult(ui.IconDot, ui.Dim.Render("none")))
		}
	}
	fmt.Println()

	fmt.Println(ui.Heading("  Catch-all"))
	fmt.Println(ui.StepResult(ui.IconDot, ui.Dim.Render("*@"+domain+" would be disabled")))
	fmt.Println()

	fmt.Println(ui.Heading("  " + providerName + " domain"))
	fmt.Println(ui.StepResult(ui.IconDot, ui.Dim.Render(domain+" would be deleted")))
	fmt.Println()

	fmt.Println(ui.Heading("  DNS records to delete"))
	if len(d.ManagedDNSRecordIDs) == 0 {
		fmt.Println(ui.StepResult(ui.IconWarn, ui.Dim.Render("none tracked — DNS would be left untouched")))
	} else {
		labels := map[string]string{}
		if all, err := cf.ListDNSRecords(d.CloudflareZoneID, ""); err == nil {
			for _, rec := range all {
				labels[rec.ID] = rec.Type + " " + rec.Name
			}
		}
		for _, id := range d.ManagedDNSRecordIDs {
			if label, ok := labels[id]; ok {
				fmt.Println(ui.StepResult(ui.IconDot, ui.Dim.Render(label)))
			} else {
				fmt.Println(ui.StepResult(ui.IconDot, ui.Dim.Render(id+" (already gone)")))
			}
		}
	}
	fmt.Println()

	return nil
}
