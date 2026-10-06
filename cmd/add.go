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
	var result *mailsetup.AddResult
	uiErr := ui.RunProgress("Setting up "+ui.Highlight.Render(opts.Domain), labels, func(p *ui.ProgressRunner) {
		result, runErr = mailsetup.AddDomain(st, progressReporter(p), opts)
	})
	if uiErr != nil {
		return uiErr
	}
	if runErr != nil {
		return runErr
	}

	// Sending is set up either way, but nothing receives until the forwarding
	// address is confirmed. Claiming the domain "is set up" here would be a lie
	// the operator only discovers when mail goes missing.
	if result != nil && result.PendingDestination != "" {
		fmt.Print("\n" + pendingPanel(opts.Domain, result.PendingDestination) + "\n")
		return nil
	}

	fmt.Print("\n" + successPanel(provider, opts.Domain, cfg) + "\n")
	return nil
}

// pendingPanel replaces the success panel when the forwarding address is still
// unconfirmed, because the domain is not finished and no routing rules exist.
//
// The address often belongs to someone outside the operator's organisation, so
// the text is written to be forwarded to them.
func pendingPanel(domain, forwardTo string) string {
	return ui.WarnPanel.Render(
		ui.Warn.Bold(true).Render(domain+" is not receiving yet") + "\n\n" +
			ui.Dim.Render("No routing rules were created: Cloudflare will not point one") + "\n" +
			ui.Dim.Render("at an address nobody has confirmed.") + "\n\n" +
			ui.Dim.Render("Waiting on ") + ui.Highlight.Render(forwardTo) + "\n\n" +
			ui.Dim.Render("Its owner has to click the link in the email from Cloudflare,") + "\n" +
			ui.Dim.Render("subject \"Verify your email address\". It often lands in spam,") + "\n" +
			ui.Dim.Render("and nobody can click it on their behalf.") + "\n\n" +
			ui.Dim.Render("Then re-run ") + ui.Highlight.Render("mailctl add "+domain) + "\n" +
			ui.Dim.Render("Check progress with ") + ui.Highlight.Render("mailctl check "+domain),
	)
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
