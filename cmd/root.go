package cmd

import (
	"fmt"
	"os"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/email"
	"github.com/sislelabs/mailctl/internal/flow"
	"github.com/sislelabs/mailctl/internal/resend"
	"github.com/sislelabs/mailctl/internal/tui"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "mailctl",
	Short: "Automate custom domain email setup with Cloudflare + Brevo",
	Long:  "mailctl automates custom domain email setup using Cloudflare Email Routing (receiving) and Brevo (sending).",
	RunE: func(cmd *cobra.Command, args []string) error {
		return tui.Run()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	// Register step functions
	flow.InitSteps()
	email.RegisterSteps(
		func() *email.SMTPConfig {
			cfg, err := internal.LoadConfig()
			if err != nil || cfg.SMTP == nil {
				return nil
			}
			return &email.SMTPConfig{
				Host:        cfg.SMTP.Host,
				Port:        cfg.SMTP.Port,
				User:        cfg.SMTP.User,
				Pass:        cfg.SMTP.Pass,
				DefaultFrom: cfg.SMTP.DefaultFrom,
			}
		},
		func() email.Sender {
			cfg, err := internal.LoadConfig()
			if err != nil || cfg.SendingProvider() != internal.ProviderResend || cfg.ResendAPIKey == "" {
				return nil
			}
			rc := resend.NewClient(cfg.ResendAPIKey)
			// Prefer an explicit default_from from the SMTP block if present,
			// so the same field configures both providers.
			var defaultFrom string
			if cfg.SMTP != nil {
				defaultFrom = cfg.SMTP.DefaultFrom
			}
			return func(msg *email.Message) error {
				from := msg.From
				if from == "" {
					from = defaultFrom
				}
				if from == "" {
					return fmt.Errorf("no sender address: set smtp.default_from in config or provide From in the flow")
				}
				atts := make([]resend.Attachment, 0, len(msg.Attachments))
				for _, a := range msg.Attachments {
					atts = append(atts, resend.Attachment{
						Filename:    a.Filename,
						Content:     a.Data,
						ContentType: a.MIMEType,
					})
				}
				return rc.SendEmail(resend.SendParams{
					From:        from,
					To:          msg.To,
					Subject:     msg.Subject,
					Text:        msg.Body,
					Attachments: atts,
				})
			}
		},
	)

	// Load YAML flows
	if err := flow.LoadAndRegisterAll(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to load flows: %v\n", err)
	}

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(removeCmd)
	rootCmd.AddCommand(aliasCmd)
	rootCmd.AddCommand(flowCmd)
}
