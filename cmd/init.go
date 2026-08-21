package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Interactive setup of API keys and config",
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	path := internal.ConfigPath()

	// Load existing config to preserve domains and pre-fill values
	var existing *internal.Config
	if _, err := os.Stat(path); err == nil {
		existing, _ = internal.LoadConfig()
	}

	fields := []ui.WizardField{
		{
			Label:       "Cloudflare API Token",
			Help:        "Create one at https://dash.cloudflare.com/profile/api-tokens\nNeeds: Zone > DNS > Edit, Zone > Zone Settings > Edit,\nZone > Email Routing Rules > Edit, Account > Email Routing Addresses > Edit",
			Placeholder: "cfut_...",
		},
		{
			Label:       "Cloudflare Account ID",
			Help:        "The hex string in your dashboard URL: https://dash.cloudflare.com/<ACCOUNT_ID>/...",
			Placeholder: "abc123def456...",
		},
		{
			Label:       "Sending provider",
			Help:        "Which service sends email: 'resend' or 'brevo'. Leave blank for resend.",
			Placeholder: "resend",
		},
		{
			Label:       "Resend API Key",
			Help:        "https://resend.com/api-keys — used for domain management and sending (skip if using Brevo)",
			Placeholder: "re_...",
		},
		{
			Label:       "Brevo API Key",
			Help:        "https://app.brevo.com/settings/keys/api — used for domain management (skip if using Resend)",
			Placeholder: "xkeysib-...",
		},
		{
			Label:       "Brevo SMTP Key",
			Help:        "https://app.brevo.com/settings/keys/smtp — used for sending email (skip if using Resend)",
			Placeholder: "xsmtpsib-...",
		},
		{
			Label:       "Brevo SMTP Login",
			Help:        "Shown on the SMTP settings page, e.g. a6df7e001@smtp-brevo.com (skip if using Resend)",
			Placeholder: "xxx@smtp-brevo.com",
		},
		{
			Label:       "Default forward-to email",
			Help:        "Your real email (e.g. Gmail) where custom domain emails get forwarded",
			Placeholder: "you@gmail.com",
		},
		{
			Label:       "Default from address",
			Help:        "Address flows send as when they don't set one themselves.\nWithout it, 'mailctl flow run' fails with 'no sender address'.",
			Placeholder: "hello@yourdomain.com",
		},
	}

	// Pre-fill from existing config
	if existing != nil {
		var existingFrom string
		if existing.SMTP != nil {
			existingFrom = existing.SMTP.DefaultFrom
		}
		prefills := []string{
			existing.CloudflareAPIToken,
			existing.CloudflareAccountID,
			existing.SendingProvider(),
			existing.ResendAPIKey,
			existing.BrevoAPIKey,
			existing.BrevoSMTPKey,
			existing.BrevoSMTPLogin,
			existing.DefaultForwardTo,
			existingFrom,
		}
		for i, val := range prefills {
			if i < len(fields) && val != "" {
				fields[i].Value = val
			}
		}
	}

	values, completed, err := ui.RunWizard("mailctl setup", fields)
	if err != nil {
		return err
	}
	if !completed {
		fmt.Println(ui.Dim.Render("  Aborted."))
		return nil
	}

	provider := strings.ToLower(strings.TrimSpace(values[2]))
	if provider != internal.ProviderBrevo {
		provider = internal.ProviderResend
	}

	cfg := &internal.Config{
		CloudflareAPIToken:  strings.TrimSpace(values[0]),
		CloudflareAccountID: strings.TrimSpace(values[1]),
		Provider:            provider,
		ResendAPIKey:        strings.TrimSpace(values[3]),
		BrevoAPIKey:         strings.TrimSpace(values[4]),
		BrevoSMTPKey:        strings.TrimSpace(values[5]),
		BrevoSMTPLogin:      strings.TrimSpace(values[6]),
		DefaultForwardTo:    strings.TrimSpace(values[7]),
	}

	// Preserve existing data
	if existing != nil {
		cfg.Domains = existing.Domains
		cfg.SMTP = existing.SMTP
	}

	cfg.ApplySMTPDefaults(strings.TrimSpace(values[8]))

	if err := internal.SaveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println()
	fmt.Println(ui.SuccessPanel.Render(
		ui.IconSuccess + " " + ui.Success.Bold(true).Render("Config saved to "+path) + "\n\n" +
			ui.Dim.Render("Next step: ") + ui.Highlight.Render("mailctl add <yourdomain.com> -a hello,support"),
	))

	reportPreflight(cfg)

	return nil
}

// reportPreflight probes the Cloudflare token against every API surface
// `mailctl add` uses and prints what is missing. Catching a permission gap here
// beats discovering it eight steps into a domain setup, where a failure can
// leave a zone half-configured.
func reportPreflight(cfg *internal.Config) {
	reportPreflightFor(cfg, "")
}

// reportPreflightFor probes against a named zone, or the first visible zone
// when zoneName is empty.
func reportPreflightFor(cfg *internal.Config, zoneName string) {
	if cfg.CloudflareAPIToken == "" {
		return
	}

	fmt.Println()
	fmt.Println(ui.Heading("  Cloudflare token check"))

	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	results := cf.Preflight(zoneName, cfg.CloudflareAccountID)

	for _, r := range results {
		if r.OK {
			fmt.Println(ui.StepResult(ui.IconSuccess, ui.Dim.Render(r.Name)))
			continue
		}
		icon := ui.IconWarn
		if r.Fatal {
			icon = ui.IconError
		}
		fmt.Println(ui.StepResult(icon, ui.Error.Render(r.Name)))
		if r.Permission != "" {
			fmt.Println("      " + ui.Dim.Render("Add: "+r.Permission))
		}
		if r.Detail != "" {
			fmt.Println("      " + ui.Dim.Render(r.Detail))
		}
	}

	fmt.Println()
	if cloudflare.HasFatalFailure(results) {
		fmt.Println(ui.Error.Render("  'mailctl add' will fail until the permissions above are granted."))
		fmt.Println(ui.Dim.Render("  Edit the token at https://dash.cloudflare.com/profile/api-tokens"))
	} else if len(cloudflare.PreflightFailures(results)) > 0 {
		fmt.Println(ui.Warn.Render("  'mailctl add' will work, but some checks will report as warnings."))
	} else {
		fmt.Println(ui.Success.Render("  Token has everything mailctl needs."))
	}
	fmt.Println()
}
