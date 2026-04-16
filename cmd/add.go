package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/brevo"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/spf13/cobra"
)

var addCmd = &cobra.Command{
	Use:   "add [domain]",
	Short: "Set up email for a domain (Cloudflare routing + Brevo sending)",
	Args:  cobra.ExactArgs(1),
	RunE:  runAdd,
}

var (
	addAliases   string
	addForwardTo string
)

func init() {
	addCmd.Flags().StringVarP(&addAliases, "aliases", "a", "hello", "Comma-separated alias names")
	addCmd.Flags().StringVarP(&addForwardTo, "forward-to", "f", "", "Override default forward-to email")
}

func runAdd(cmd *cobra.Command, args []string) error {
	domain := args[0]

	cfg, err := internal.LoadConfig()
	if err != nil {
		return err
	}

	if cfg.FindDomain(domain) != nil {
		return fmt.Errorf("domain %s is already configured — use 'mailctl remove %s' first", domain, domain)
	}

	forwardTo := cfg.DefaultForwardTo
	if addForwardTo != "" {
		forwardTo = addForwardTo
	}

	aliases := strings.Split(addAliases, ",")
	for i := range aliases {
		aliases[i] = strings.TrimSpace(aliases[i])
	}

	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	bv := brevo.NewClient(cfg.BrevoAPIKey)

	stepLabels := []string{
		"Look up Cloudflare zone",
		"Enable email routing",
		"Verify destination address",
		"Create routing rules",
		"Enable catch-all",
		"Add domain to Brevo",
		"Add DNS records",
		"Authenticate domain",
		"Create senders",
		"Save config",
	}

	var zoneID string
	var cfAliases []internal.Alias
	var afterOutput string

	err = ui.RunProgress("Setting up "+ui.Highlight.Render(domain), stepLabels, func(p *ui.ProgressRunner) {
		// Step 0: Find zone
		p.Start(0)
		zone, err := cf.GetZoneByName(domain)
		if err != nil {
			p.Fail(0, "not found — is it added to Cloudflare?")
			return
		}
		zoneID = zone.ID
		p.Done(0, ui.Dim.Render(zone.ID))

		// Step 1: Enable email routing
		// First delete any conflicting MX records that block enabling
		p.Start(1)
		mxRecords, _ := cf.ListDNSRecords(zone.ID, "MX")
		for _, mx := range mxRecords {
			if mx.Name == domain {
				cf.DeleteDNSRecord(zone.ID, mx.ID)
			}
		}
		if err := cf.EnableEmailRouting(zone.ID); err != nil {
			p.Warn(1, err.Error())
		} else {
			p.Done(1, "")
		}

		// Step 2: Verify destination address
		p.Start(2)
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
			p.Done(2, ui.Dim.Render(forwardTo+" — verified"))
		} else {
			// Try to create it
			if err := cf.CreateDestinationAddress(zone.Account.ID, forwardTo); err != nil {
				p.Warn(2, forwardTo+" — check Cloudflare Email Routing > Destination addresses")
			} else {
				p.Warn(2, forwardTo+" — verification email sent, check inbox and confirm before emails will route")
			}
		}

		// Step 3: Create routing rules
		p.Start(3)
		ruleErrors := 0
		for _, alias := range aliases {
			addr := fmt.Sprintf("%s@%s", alias, domain)
			rule := cloudflare.RoutingRule{
				Name:    fmt.Sprintf("Forward %s", addr),
				Enabled: true,
				Matchers: []cloudflare.RuleMatcher{
					{Type: "literal", Field: "to", Value: addr},
				},
				Actions: []cloudflare.RuleAction{
					{Type: "forward", Value: []string{forwardTo}},
				},
			}
			if err := cf.CreateRoutingRule(zone.ID, rule); err != nil {
				p.SubRow(3, ui.IconError+" "+ui.Error.Render(addr)+" "+ui.Dim.Render(err.Error()))
				ruleErrors++
			} else {
				p.SubRow(3, ui.IconSuccess+" "+ui.Dim.Render(addr+" → "+forwardTo))
			}
			cfAliases = append(cfAliases, internal.Alias{
				Alias:     alias,
				ForwardTo: []string{forwardTo},
			})
		}
		if ruleErrors > 0 {
			p.Warn(3, fmt.Sprintf("%d failed", ruleErrors))
		} else {
			p.Done(3, fmt.Sprintf("%d rules", len(aliases)))
		}

		// Step 4: Enable catch-all
		p.Start(4)
		catchAll := cloudflare.RoutingRule{
			Enabled: true,
			Matchers: []cloudflare.RuleMatcher{
				{Type: "all"},
			},
			Actions: []cloudflare.RuleAction{
				{Type: "forward", Value: []string{forwardTo}},
			},
		}
		if err := cf.UpdateCatchAllRule(zone.ID, catchAll); err != nil {
			p.Warn(4, err.Error())
		} else {
			p.Done(4, ui.Dim.Render("*@"+domain+" → "+forwardTo))
		}

		// Step 5: Add domain to Brevo
		p.Start(5)
		brevoDomain, err := bv.AddDomain(domain)
		if err != nil {
			p.Warn(5, "failed — receiving still works")
			p.SubRow(5, ui.Dim.Render(err.Error()))

			p.Start(6)
			p.Warn(6, "skipped")
			p.Start(7)
			p.Warn(7, "skipped")
			p.Start(8)
			p.Warn(8, "skipped")
		} else {
			p.Done(5, "")

			// Step 6: Add DNS records to Cloudflare
			p.Start(6)
			for _, rec := range brevoDomain.FlatDNSRecords() {
				name := brevo.FullRecordName(rec, domain)
				cfRec := cloudflare.DNSRecord{
					Type:    rec.Type,
					Name:    name,
					Content: rec.Value,
					TTL:     3600,
				}
				if err := cf.CreateDNSRecord(zone.ID, cfRec); err != nil {
					p.SubRow(6, ui.IconWarn+" "+ui.Dim.Render(rec.Type+" "+name+" — "+err.Error()))
				} else {
					p.SubRow(6, ui.IconSuccess+" "+ui.Dim.Render(rec.Type+" "+name))
				}
			}
			p.Done(6, "")

			// Step 7: Authenticate domain
			p.Start(7)
			time.Sleep(2 * time.Second)
			if err := bv.AuthenticateDomain(domain); err != nil {
				p.Warn(7, "pending — DNS may need time to propagate")
			} else {
				p.Done(7, "")
			}

			// Step 8: Create senders for each alias
			p.Start(8)
			for _, alias := range aliases {
				addr := fmt.Sprintf("%s@%s", alias, domain)
				name := strings.ToUpper(alias[:1]) + alias[1:]
				if err := bv.CreateSender(name, addr); err != nil {
					p.SubRow(8, ui.IconWarn+" "+ui.Dim.Render(addr+" — "+err.Error()))
				} else {
					p.SubRow(8, ui.IconSuccess+" "+ui.Dim.Render(addr))
				}
			}
			p.Done(8, "")
		}

		// Step 9: Save config
		p.Start(9)
		cfg.AddDomain(domain, zoneID, cfAliases)
		if err := internal.SaveConfig(cfg); err != nil {
			p.Fail(9, err.Error())
			return
		}
		p.Done(9, "")

		afterOutput = "\n" + ui.SuccessPanel.Render(
			ui.Success.Bold(true).Render(domain+" is set up!")+"\n\n"+
				ui.Dim.Render("To send from Gmail:")+"\n"+
				ui.Dim.Render("1. Open ")+ui.Highlight.Render("https://mail.google.com/mail/#settings/accounts")+"\n"+
				ui.Dim.Render("2. Click 'Add another email address'")+"\n"+
				ui.Dim.Render("3. Uncheck 'Treat as an alias', use these SMTP settings:")+"\n\n"+
				ui.KeyValue("Server", "smtp-relay.brevo.com")+"\n"+
				ui.KeyValue("Port", "587")+"\n"+
				ui.KeyValue("Username", cfg.BrevoSMTPLogin)+"\n"+
				ui.KeyValue("Password", cfg.BrevoSMTPKey)+"\n"+
				ui.KeyValue("Security", "TLS")+"\n\n"+
				ui.Dim.Render("4. Enter the verification code from your inbox"),
		) + "\n"
	})

	if err != nil {
		return err
	}

	if afterOutput != "" {
		fmt.Print(afterOutput)
	}

	return nil
}
