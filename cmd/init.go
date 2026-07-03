package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/sislelabs/mailctl/internal"
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
			Help:        "Create one at https://dash.cloudflare.com/profile/api-tokens\nNeeds: Zone > DNS > Edit, Email Routing Rules > Edit, Email Routing Addresses > Edit",
			Placeholder: "cfut_...",
		},
		{
			Label:       "Cloudflare Account ID",
			Help:        "The hex string in your dashboard URL: https://dash.cloudflare.com/<ACCOUNT_ID>/...",
			Placeholder: "abc123def456...",
		},
		{
			Label:       "Sending provider",
			Help:        "Which service sends email: 'brevo' or 'resend'. Leave blank for brevo.",
			Placeholder: "brevo",
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
			Label:       "Resend API Key",
			Help:        "https://resend.com/api-keys — used for domain management and sending (skip if using Brevo)",
			Placeholder: "re_...",
		},
		{
			Label:       "Default forward-to email",
			Help:        "Your real email (e.g. Gmail) where custom domain emails get forwarded",
			Placeholder: "you@gmail.com",
		},
	}

	// Pre-fill from existing config
	if existing != nil {
		prefills := []string{
			existing.CloudflareAPIToken,
			existing.CloudflareAccountID,
			existing.SendingProvider(),
			existing.BrevoAPIKey,
			existing.BrevoSMTPKey,
			existing.BrevoSMTPLogin,
			existing.ResendAPIKey,
			existing.DefaultForwardTo,
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
	if provider != internal.ProviderResend {
		provider = internal.ProviderBrevo
	}

	cfg := &internal.Config{
		CloudflareAPIToken:  values[0],
		CloudflareAccountID: values[1],
		Provider:            provider,
		BrevoAPIKey:         values[3],
		BrevoSMTPKey:        values[4],
		BrevoSMTPLogin:      values[5],
		ResendAPIKey:        values[6],
		DefaultForwardTo:    values[7],
	}

	// Preserve existing data
	if existing != nil {
		cfg.Domains = existing.Domains
		cfg.SMTP = existing.SMTP
	}

	if err := internal.SaveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println()
	fmt.Println(ui.SuccessPanel.Render(
		ui.IconSuccess + " " + ui.Success.Bold(true).Render("Config saved to "+path) + "\n\n" +
			ui.Dim.Render("Next step: ") + ui.Highlight.Render("mailctl add <yourdomain.com> -a hello,support"),
	))

	return nil
}
