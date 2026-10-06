package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/mailsetup"
	"github.com/sislelabs/mailctl/internal/store"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/spf13/cobra"
)

var aliasCmd = &cobra.Command{
	Use:   "alias",
	Short: "Manage email aliases for a domain",
}

var aliasAddCmd = &cobra.Command{
	Use:   "add [domain] [alias]",
	Short: "Add an email alias to a domain",
	Args:  cobra.ExactArgs(2),
	RunE:  runAliasAdd,
}

var aliasRemoveCmd = &cobra.Command{
	Use:     "remove [domain] [alias]",
	Aliases: []string{"rm"},
	Short:   "Remove an email alias from a domain",
	Args:    cobra.ExactArgs(2),
	RunE:    runAliasRemove,
}

var aliasListCmd = &cobra.Command{
	Use:     "list [domain]",
	Aliases: []string{"ls"},
	Short:   "List all aliases for a domain",
	Args:    cobra.ExactArgs(1),
	RunE:    runAliasList,
}

var aliasCatchallCmd = &cobra.Command{
	Use:   "catchall [domain] [on|off]",
	Short: "Enable or disable catch-all forwarding for a domain",
	Long:  "Enable catch-all to forward all unmatched emails to your default address. Without on/off, shows current status.",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runAliasCatchall,
}

var aliasForwardTo string

func init() {
	aliasAddCmd.Flags().StringVarP(&aliasForwardTo, "forward-to", "f", "", "Override forward-to email")
	aliasCatchallCmd.Flags().StringVarP(&aliasForwardTo, "forward-to", "f", "", "Override forward-to email")

	aliasCmd.AddCommand(aliasAddCmd)
	aliasCmd.AddCommand(aliasRemoveCmd)
	aliasCmd.AddCommand(aliasListCmd)
	aliasCmd.AddCommand(aliasCatchallCmd)
}

func runAliasAdd(cmd *cobra.Command, args []string) error {
	domain := args[0]
	alias := args[1]

	cfg, err := internal.LoadConfig()
	if err != nil {
		return err
	}

	d := cfg.FindDomain(domain)
	if d == nil {
		return fmt.Errorf("domain %s not found in config — run 'mailctl add %s' first", domain, domain)
	}

	if d.FindAlias(alias) != nil {
		return fmt.Errorf("alias %s already exists for %s", alias, domain)
	}

	addr := fmt.Sprintf("%s@%s", alias, domain)

	// One step, because mailsetup.AddAlias is one transaction: it registers the
	// destination, writes the rule and saves config, and reports back what the
	// address needs before mail will move.
	st := store.NewYAML()
	var addErr error
	if err := ui.RunProgress("Adding "+ui.Highlight.Render(addr), []string{"Create routing rule"}, func(p *ui.ProgressRunner) {
		p.Start(0)
		addErr = mailsetup.AddAlias(st, domain, alias, aliasForwardTo)

		var pending *mailsetup.DestinationPendingError
		switch {
		case addErr == nil:
			p.SubRow(0, ui.Dim.Render(addr+" → "+addedForwardTo(st, domain, alias)))
			p.Done(0, "")
		case errors.As(addErr, &pending):
			p.Warn(0, "waiting on "+pending.Email)
		default:
			p.Fail(0, addErr.Error())
		}
	}); err != nil {
		return err
	}

	var pending *mailsetup.DestinationPendingError
	if errors.As(addErr, &pending) {
		printDestinationPending(pending, addr)
		return nil
	}
	if addErr != nil {
		return addErr
	}

	fmt.Println()
	fmt.Println(" " + ui.IconSuccess + " " + ui.Success.Render(addr+" added"))
	fmt.Println()
	return nil
}

// addedForwardTo reads back where an alias was actually pointed.
//
// An empty --forward-to lets AddAlias fall back to whatever the domain's other
// aliases use, which is not always the global default, so echoing the flag or
// the default here would sometimes print an address the rule does not use.
func addedForwardTo(st store.Store, domain, alias string) string {
	cfg, err := st.Load()
	if err != nil {
		return ""
	}
	d := cfg.FindDomain(domain)
	if d == nil {
		return ""
	}
	if a := d.FindAlias(alias); a != nil && len(a.ForwardTo) > 0 {
		return a.ForwardTo[0]
	}
	return ""
}

// printDestinationPending explains that the alias is waiting on a person, not
// on mailctl. The address may well belong to someone outside the organisation,
// so the text says who has to act and what the mail they are looking for is.
func printDestinationPending(pending *mailsetup.DestinationPendingError, addr string) {
	sent := "Cloudflare has already asked them to confirm it"
	if pending.JustRegistered {
		sent = "Cloudflare has just emailed them to confirm it"
	}

	fmt.Println()
	fmt.Println(" " + ui.IconPending + " " + ui.Info.Render(addr+" not created yet"))
	fmt.Println()
	fmt.Println("   " + ui.Dim.Render(pending.Email+" is not a confirmed destination, and Cloudflare"))
	fmt.Println("   " + ui.Dim.Render("will not create a rule pointing at one. "+sent+"."))
	fmt.Println()
	fmt.Println("   " + ui.Dim.Render("They need to click the link in the mail from Cloudflare —"))
	fmt.Println("   " + ui.Dim.Render("subject \"Verify your email address\". It often lands in spam."))
	fmt.Println("   " + ui.Dim.Render("Re-run this command once they have."))
	fmt.Println()
}

func runAliasRemove(cmd *cobra.Command, args []string) error {
	domain := args[0]
	alias := args[1]

	cfg, err := internal.LoadConfig()
	if err != nil {
		return err
	}

	d := cfg.FindDomain(domain)
	if d == nil {
		return fmt.Errorf("domain %s not found in config", domain)
	}

	if d.FindAlias(alias) == nil {
		return fmt.Errorf("alias %s not found for %s", alias, domain)
	}

	addr := fmt.Sprintf("%s@%s", alias, domain)

	stepLabels := []string{
		"Delete routing rule",
		"Save config",
	}

	err = ui.RunProgress("Removing "+ui.Error.Render(addr), stepLabels, func(p *ui.ProgressRunner) {
		cf := cloudflare.NewClient(cfg.CloudflareAPIToken)

		p.Start(0)
		rules, err := cf.ListRoutingRules(d.CloudflareZoneID)
		if err != nil {
			p.Warn(0, err.Error())
		} else {
			deleted := false
			for _, r := range rules {
				for _, m := range r.Matchers {
					if m.Value == addr {
						if err := cf.DeleteRoutingRule(d.CloudflareZoneID, r.ID); err != nil {
							p.Warn(0, err.Error())
						} else {
							deleted = true
						}
						break
					}
				}
				if deleted {
					break
				}
			}
			if !deleted {
				p.Warn(0, "no matching rule found in Cloudflare")
			}
		}
		p.Done(0, "")

		p.Start(1)
		d.RemoveAlias(alias)
		if err := internal.SaveConfig(cfg); err != nil {
			p.Fail(1, err.Error())
			return
		}
		p.Done(1, "")
	})

	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println(" " + ui.IconSuccess + " " + ui.Success.Render(addr+" removed"))
	fmt.Println()
	return nil
}

func runAliasCatchall(cmd *cobra.Command, args []string) error {
	domain := args[0]

	cfg, err := internal.LoadConfig()
	if err != nil {
		return err
	}

	d := cfg.FindDomain(domain)
	if d == nil {
		return fmt.Errorf("domain %s not found in config", domain)
	}

	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)

	rule, err := cf.GetCatchAllRule(d.CloudflareZoneID)
	if err != nil {
		return fmt.Errorf("could not get catch-all rule: %w", err)
	}

	// Show status if no action given
	if len(args) == 1 {
		status := ui.Error.Render("off")
		detail := ui.Dim.Render("(unmatched emails are dropped)")
		if rule.Enabled && len(rule.Actions) > 0 && rule.Actions[0].Type == "forward" {
			fwd := "?"
			if len(rule.Actions[0].Value) > 0 {
				fwd = ui.MaskEmail(rule.Actions[0].Value[0])
			}
			status = ui.Success.Render("on")
			detail = ui.Dim.Render("→ " + fwd)
		}
		fmt.Println()
		fmt.Printf("  %s catch-all: %s %s\n", ui.Highlight.Render(domain), status, detail)
		fmt.Println()
		return nil
	}

	action := args[1]

	switch action {
	case "on":
		forwardTo := cfg.DefaultForwardTo
		if aliasForwardTo != "" {
			forwardTo = aliasForwardTo
		}
		if forwardTo == "" {
			return fmt.Errorf("no forward-to address — set default_forward_to in config or use --forward-to")
		}

		updated := cloudflare.RoutingRule{
			Enabled: true,
			Matchers: []cloudflare.RuleMatcher{
				{Type: "all"},
			},
			Actions: []cloudflare.RuleAction{
				{Type: "forward", Value: []string{forwardTo}},
			},
		}
		if err := cf.UpdateCatchAllRule(d.CloudflareZoneID, updated); err != nil {
			return fmt.Errorf("failed to enable catch-all: %w", err)
		}

		fmt.Println()
		fmt.Printf("  %s catch-all enabled for %s %s %s\n",
			ui.IconSuccess,
			ui.Highlight.Render(domain),
			ui.Dim.Render("→"),
			ui.Success.Render(forwardTo))
		fmt.Println()

	case "off":
		updated := cloudflare.RoutingRule{
			Enabled: false,
			Matchers: []cloudflare.RuleMatcher{
				{Type: "all"},
			},
			Actions: []cloudflare.RuleAction{
				{Type: "drop", Value: []string{}},
			},
		}
		if err := cf.UpdateCatchAllRule(d.CloudflareZoneID, updated); err != nil {
			return fmt.Errorf("failed to disable catch-all: %w", err)
		}

		fmt.Println()
		fmt.Printf("  %s catch-all disabled for %s\n", ui.IconSuccess, ui.Highlight.Render(domain))
		fmt.Println()

	default:
		return fmt.Errorf("unknown action %q — use 'on' or 'off'", action)
	}

	return nil
}

func runAliasList(cmd *cobra.Command, args []string) error {
	domain := args[0]

	cfg, err := internal.LoadConfig()
	if err != nil {
		return err
	}

	d := cfg.FindDomain(domain)
	if d == nil {
		return fmt.Errorf("domain %s not found in config", domain)
	}

	if len(d.Aliases) == 0 {
		fmt.Println()
		fmt.Println(ui.InfoPanel.Render(
			ui.Info.Render("No aliases configured for "+domain) + "\n\n" +
				ui.Dim.Render("Add one: ") + ui.Highlight.Render("mailctl alias add "+domain+" hello"),
		))
		return nil
	}

	fmt.Println()
	fmt.Println(" " + ui.Highlight.Render("Aliases for "+domain))
	fmt.Println()
	for _, a := range d.Aliases {
		fmt.Printf("   %s %s %s %s\n",
			ui.IconDot,
			ui.White.Render(a.Alias+"@"+domain),
			ui.Dim.Render("→"),
			ui.Dim.Render(strings.Join(maskEmails(a.ForwardTo), ", ")))
	}
	fmt.Println()
	fmt.Printf(" %s\n\n", ui.Dim.Render(fmt.Sprintf("%d alias(es)", len(d.Aliases))))

	return nil
}

func maskEmails(emails []string) []string {
	masked := make([]string, len(emails))
	for i, e := range emails {
		masked[i] = ui.MaskEmail(e)
	}
	return masked
}
