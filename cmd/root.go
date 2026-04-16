package cmd

import (
	"fmt"
	"os"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/email"
	"github.com/sislelabs/mailctl/internal/flow"
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
	email.RegisterSteps(func() *email.SMTPConfig {
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
	})

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
