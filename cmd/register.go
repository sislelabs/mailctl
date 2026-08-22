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
	Short: "Set up a domain for sending only",
	Long: `Register a domain with the sending provider and publish its sending DNS.

This does two jobs. For a domain mailctl already manages that receives mail
but cannot send — what 'mailctl check' reports as "Not registered with
Resend" — it completes the sending half without disturbing routing. For a
domain mailctl does not know, it creates a send-only entry: newsletter and
product senders belong on their own subdomain so bounce reputation stays away
from human mailboxes, and they need no routing at all.

A subdomain is resolved against the zone that holds it, so
'mailctl register info.example.com' works even though only example.com is a
Cloudflare zone.

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

	summary := ui.IconSuccess + " " + ui.Success.Bold(true).Render(domain+" registered")
	if result.Adopted {
		summary += "\n\n" + ui.Dim.Render("Already registered with the provider — adopted, not recreated.")
	}
	if result.Created {
		summary += "\n" + ui.Dim.Render("Added as a sending domain — it sends but does not receive.")
		if result.IsSubdomain {
			summary += "\n" + ui.Dim.Render("Records live in the "+result.ZoneName+" zone.")
		}
	}
	fmt.Println(ui.SuccessPanel.Render(
		summary + "\n\n" +
			ui.Dim.Render("Verification is asynchronous — check with") + "\n" +
			ui.Highlight.Render("  mailctl check "+domain),
	))
	fmt.Println()

	return nil
}
