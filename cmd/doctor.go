package cmd

import (
	"fmt"

	"github.com/sislelabs/mailctl/internal"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor [domain]",
	Short: "Check the Cloudflare token against everything mailctl needs",
	Long: `Probe the configured Cloudflare API token against every endpoint mailctl uses
and report which permissions are missing.

Run this before setting up a domain: a missing permission surfaces here as a
named fix rather than as a warning partway through 'mailctl add', where a
failure can leave a zone half-configured.

Pass a domain to probe against that specific zone instead of the first one the
token can see.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDoctor,
}

func runDoctor(cmd *cobra.Command, args []string) error {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return err
	}
	if cfg.CloudflareAPIToken == "" {
		return fmt.Errorf("no cloudflare_api_token in config — run 'mailctl init'")
	}

	var zoneName string
	if len(args) == 1 {
		zoneName = args[0]
	}

	reportPreflightFor(cfg, zoneName)
	return nil
}
