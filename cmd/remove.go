package cmd

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/brevo"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/spf13/cobra"
)

func isSendingDNSRecord(rec cloudflare.DNSRecord) bool {
	return strings.Contains(rec.Name, "resend") ||
		strings.Contains(rec.Content, "resend") ||
		strings.Contains(rec.Content, "amazonses") ||
		strings.Contains(rec.Name, "brevo") ||
		strings.Contains(rec.Content, "brevo") ||
		strings.Contains(rec.Name, "_domainkey") ||
		strings.Contains(rec.Name, "_dmarc")
}

var removeCmd = &cobra.Command{
	Use:     "remove [domain]",
	Aliases: []string{"rm"},
	Short:   "Tear down email setup for a domain",
	Args:    cobra.ExactArgs(1),
	RunE:    runRemove,
}

var removeForce bool

func init() {
	removeCmd.Flags().BoolVarP(&removeForce, "force", "f", false, "Skip confirmation")
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

	if !removeForce {
		var items []string
		items = append(items, ui.IconDot+" Cloudflare email routing rules")
		items = append(items, ui.IconDot+" Brevo domain")
		items = append(items, ui.IconDot+" Sending DNS records")
		items = append(items, ui.IconDot+" Config entry")

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

	stepLabels := []string{
		"Delete routing rules",
		"Delete Brevo domain",
		"Clean up DNS records",
		"Remove from config",
	}

	err = ui.RunProgress("Removing "+ui.Error.Bold(true).Render(domain), stepLabels, func(p *ui.ProgressRunner) {
		cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
		bv := brevo.NewClient(cfg.BrevoAPIKey)

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

		// Step 1: Delete Brevo domain
		p.Start(1)
		if err := bv.DeleteDomain(domain); err != nil {
			p.Warn(1, "not found or already deleted")
		} else {
			p.Done(1, "")
		}

		// Step 2: Clean up DNS records
		p.Start(2)
		count := 0
		txtRecords, err := cf.ListDNSRecords(d.CloudflareZoneID, "TXT")
		if err == nil {
			for _, rec := range txtRecords {
				if isSendingDNSRecord(rec) {
					if err := cf.DeleteDNSRecord(d.CloudflareZoneID, rec.ID); err == nil {
						p.SubRow(2, ui.IconSuccess+" "+ui.Dim.Render("TXT "+rec.Name))
						count++
					}
				}
			}
		}
		cnameRecords, err := cf.ListDNSRecords(d.CloudflareZoneID, "CNAME")
		if err == nil {
			for _, rec := range cnameRecords {
				if isSendingDNSRecord(rec) {
					if err := cf.DeleteDNSRecord(d.CloudflareZoneID, rec.ID); err == nil {
						p.SubRow(2, ui.IconSuccess+" "+ui.Dim.Render("CNAME "+rec.Name))
						count++
					}
				}
			}
		}
		p.Done(2, fmt.Sprintf("%d deleted", count))

		// Step 3: Remove from config
		p.Start(3)
		cfg.RemoveDomain(domain)
		if err := internal.SaveConfig(cfg); err != nil {
			p.Fail(3, err.Error())
			return
		}
		p.Done(3, "")
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
