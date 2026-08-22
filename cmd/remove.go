package cmd

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/mailsetup"
	"github.com/sislelabs/mailctl/internal/store"
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

	st := store.NewYAML()
	cfg, err := st.Load()
	if err != nil {
		return err
	}

	d := cfg.FindDomain(domain)
	if d == nil {
		return fmt.Errorf("domain %s not found in config", domain)
	}

	providerName := mailsetup.ProviderLabel(cfg)

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

	labels := mailsetup.RemoveStepLabels(providerName)

	var runErr error
	uiErr := ui.RunProgress("Removing "+ui.Error.Bold(true).Render(domain), labels, func(p *ui.ProgressRunner) {
		_, runErr = mailsetup.RemoveDomain(st, progressReporter(p), mailsetup.RemoveOptions{Domain: domain})
	})
	if uiErr != nil {
		return uiErr
	}
	if runErr != nil {
		return runErr
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
