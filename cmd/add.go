package cmd

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/mailsetup"
	"github.com/sislelabs/mailctl/internal/store"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/spf13/cobra"
)

var addCmd = &cobra.Command{
	Use:   "add [domain]",
	Short: "Set up email for a domain (Cloudflare routing + Resend or Brevo sending)",
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
	st := store.NewYAML()
	cfg, err := st.Load()
	if err != nil {
		return err
	}

	opts := mailsetup.AddOptions{
		Domain:    args[0],
		Aliases:   strings.Split(addAliases, ","),
		ForwardTo: addForwardTo,
	}

	// Reject bad input before opening the progress display, so it reads as a
	// plain error rather than a failed first step.
	if err := mailsetup.ValidateAdd(cfg, opts); err != nil {
		return err
	}

	provider := cfg.SendingProvider()
	labels := mailsetup.AddStepLabels(mailsetup.ProviderLabel(cfg))

	var runErr error
	uiErr := ui.RunProgress("Setting up "+ui.Highlight.Render(opts.Domain), labels, func(p *ui.ProgressRunner) {
		_, runErr = mailsetup.AddDomain(st, progressReporter(p), opts)
	})
	if uiErr != nil {
		return uiErr
	}
	if runErr != nil {
		return runErr
	}

	fmt.Print("\n" + successPanel(provider, opts.Domain, cfg) + "\n")
	return nil
}

// successPanel renders the post-setup instructions, tailored to the provider.
func successPanel(provider, domain string, cfg *internal.Config) string {
	if provider == internal.ProviderResend {
		return ui.SuccessPanel.Render(
			ui.Success.Bold(true).Render(domain+" is set up!") + "\n\n" +
				ui.Dim.Render("Sending is handled through Resend's API.") + "\n" +
				ui.Dim.Render("Once DNS propagates, the domain verifies automatically —") + "\n" +
				ui.Dim.Render("check status with ") + ui.Highlight.Render("mailctl check "+domain) + "\n\n" +
				ui.Dim.Render("Send from any address on this domain via flows:") + "\n" +
				ui.Highlight.Render("  mailctl flow run welcome:send you@example.com") + "\n\n" +
				ui.Dim.Render("To send from Gmail, use Resend SMTP in ") +
				ui.Highlight.Render("Gmail > Settings > Accounts") + "\n" +
				ui.KeyValue("Server", "smtp.resend.com") + "\n" +
				ui.KeyValue("Port", "587") + "\n" +
				ui.KeyValue("Username", "resend") + "\n" +
				ui.KeyValue("Password", ui.Dim.Render("your Resend API key")) + "\n" +
				ui.KeyValue("Security", "TLS"),
		)
	}
	return ui.SuccessPanel.Render(
		ui.Success.Bold(true).Render(domain+" is set up!") + "\n\n" +
			ui.Dim.Render("To send from Gmail:") + "\n" +
			ui.Dim.Render("1. Open ") + ui.Highlight.Render("https://mail.google.com/mail/#settings/accounts") + "\n" +
			ui.Dim.Render("2. Click 'Add another email address'") + "\n" +
			ui.Dim.Render("3. Uncheck 'Treat as an alias', use these SMTP settings:") + "\n\n" +
			ui.KeyValue("Server", "smtp-relay.brevo.com") + "\n" +
			ui.KeyValue("Port", "587") + "\n" +
			ui.KeyValue("Username", cfg.BrevoSMTPLogin) + "\n" +
			ui.KeyValue("Password", cfg.BrevoSMTPKey) + "\n" +
			ui.KeyValue("Security", "TLS") + "\n\n" +
			ui.Dim.Render("4. Enter the verification code from your inbox"),
	)
}
