package cmd

import (
	"fmt"
	"os"

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
			Label:       "Brevo API Key",
			Help:        "https://app.brevo.com/settings/keys/api — used for domain management",
			Placeholder: "xkeysib-...",
		},
		{
			Label:       "Brevo SMTP Key",
			Help:        "https://app.brevo.com/settings/keys/smtp — used for sending email",
			Placeholder: "xsmtpsib-...",
		},
		{
			Label:       "Brevo SMTP Login",
			Help:        "Shown on the SMTP settings page, e.g. a6df7e001@smtp-brevo.com",
			Placeholder: "xxx@smtp-brevo.com",
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
			existing.BrevoAPIKey,
			existing.BrevoSMTPKey,
			existing.BrevoSMTPLogin,
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

	cfg := &internal.Config{
		CloudflareAPIToken:  values[0],
		CloudflareAccountID: values[1],
		BrevoAPIKey:         values[2],
		BrevoSMTPKey:        values[3],
		BrevoSMTPLogin:      values[4],
		DefaultForwardTo:    values[5],
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
