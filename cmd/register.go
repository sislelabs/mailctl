package cmd

import (
	"fmt"

	"github.com/sislelabs/mailctl/internal/mailsetup"
	"github.com/sislelabs/mailctl/internal/store"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/spf13/cobra"
)

var registerCmd = &cobra.Command{
	Use:   "register [domain]",
	Short: "Register an already-configured domain with the sending provider",
	Long: `Register a domain that mailctl already manages with the sending provider.

For a domain that receives mail correctly but cannot send — what 'mailctl
audit' and 'mailctl check' report as "Not registered with Resend". Removing and
re-adding the domain would fix it while destroying every routing rule and alias
on the way; this adds only what sending needs.

It never deletes or modifies anything. Routing rules, aliases, catch-all and
apex MX records are untouched, and a DNS record that already exists is left
exactly as it is — reported as a conflict if it disagrees with what the
provider expects.`,
	Args: cobra.ExactArgs(1),
	RunE: runRegister,
}

func runRegister(cmd *cobra.Command, args []string) error {
	domain := args[0]

	st := store.NewYAML()
	cfg, err := st.Load()
	if err != nil {
		return err
	}
	if cfg.FindDomain(domain) == nil {
		return fmt.Errorf("domain %s is not in config — use 'mailctl add %s' to set it up from scratch", domain, domain)
	}

	labels := mailsetup.RegisterStepLabels(mailsetup.ProviderLabel(cfg))

	var (
		result *mailsetup.RegisterResult
		runErr error
	)
	uiErr := ui.RunProgress("Registering "+ui.Highlight.Render(domain), labels, func(p *ui.ProgressRunner) {
		result, runErr = mailsetup.RegisterDomain(st, progressReporter(p), mailsetup.RegisterOptions{Domain: domain})
	})
	if uiErr != nil {
		return uiErr
	}
	if runErr != nil {
		return runErr
	}

	fmt.Println()
	if len(result.Conflicts) > 0 {
		fmt.Println(ui.Heading("  Conflicting DNS records — left untouched"))
		for _, c := range result.Conflicts {
			fmt.Println(ui.StepResult(ui.IconWarn, ui.White.Render(c.Type+" "+c.Name)))
			fmt.Println("      " + ui.Dim.Render("currently: "+c.Existing))
			fmt.Println("      " + ui.Dim.Render("expected:  "+c.Wanted))
		}
		fmt.Println()
		fmt.Println(ui.Dim.Render("  Resolve these by hand in Cloudflare, then run ") +
			ui.Highlight.Render("mailctl check "+domain))
		fmt.Println()
		return nil
	}

	fmt.Println(ui.SuccessPanel.Render(
		ui.IconSuccess + " " + ui.Success.Bold(true).Render(domain+" registered") + "\n\n" +
			ui.Dim.Render("Verification is asynchronous — check with") + "\n" +
			ui.Highlight.Render("  mailctl check "+domain),
	))
	fmt.Println()

	return nil
}
