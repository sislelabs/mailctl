package cmd

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal/mailsetup"
	"github.com/sislelabs/mailctl/internal/store"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/spf13/cobra"
)

var gmailCmd = &cobra.Command{
	Use:   "gmail [domain]",
	Short: "Show how to send from this domain's addresses in Gmail",
	Long: `Print the steps for adding a domain's addresses to Gmail's "Send mail as",
so you can send from them without leaving Gmail.

The SMTP password is masked by default. Pass --show-password to print it.`,
	Args: cobra.ExactArgs(1),
	RunE: runGmail,
}

var gmailShowPassword bool

func init() {
	gmailCmd.Flags().BoolVar(&gmailShowPassword, "show-password", false, "Print the SMTP password instead of masking it")
}

func runGmail(cmd *cobra.Command, args []string) error {
	domain := args[0]

	st := store.NewYAML()
	cfg, err := st.Load()
	if err != nil {
		return err
	}
	d := cfg.FindDomain(domain)
	if d == nil {
		return fmt.Errorf("domain %s not found in config", domain)
	}

	g, err := mailsetup.GmailSendAsFor(cfg, d)
	if err != nil {
		return err
	}

	password := g.MaskedPassword()
	if gmailShowPassword {
		password = g.Password
	}

	fmt.Println()
	fmt.Println(ui.Heading("  Send from " + domain + " in Gmail"))
	fmt.Println()

	switch {
	case g.AddressesError != "":
		fmt.Println(ui.StepResult(ui.IconWarn, ui.Dim.Render("could not read addresses: "+g.AddressesError)))
	case len(g.Addresses) == 0:
		fmt.Println(ui.StepResult(ui.IconWarn, ui.Dim.Render("no addresses on this domain yet — add one with 'mailctl alias add'")))
	default:
		fmt.Println(ui.Dim.Render("  Addresses you can add:"))
	}
	for _, a := range g.Addresses {
		fmt.Println("    " + ui.White.Render(a.Address) + " " +
			ui.Dim.Render("→ "+strings.Join(a.DeliversTo, ", ")))
	}
	fmt.Println()

	steps := []string{
		"Open " + ui.Highlight.Render("https://mail.google.com/mail/#settings/accounts"),
		"Under " + ui.White.Render("Send mail as") + ", click " + ui.White.Render("Add another email address"),
		"Enter the name recipients should see, and one of the addresses above",
		ui.Error.Render("Uncheck") + " " + ui.White.Render("Treat as an alias") + ui.Dim.Render(" — leaving it on makes replies come from your Gmail address instead"),
		"Click Next, then fill in the SMTP server below",
		"Gmail emails a confirmation link and code to that address; it forwards to the inbox shown above",
		"Click the link, or paste the code, to finish",
	}
	for i, s := range steps {
		fmt.Printf("  %s %s\n", ui.Dim.Render(fmt.Sprintf("%d.", i+1)), s)
	}

	fmt.Println()
	fmt.Println(ui.Heading("  SMTP server"))
	fmt.Println("  " + ui.KeyValue("Server", g.Host))
	fmt.Println("  " + ui.KeyValue("Port", fmt.Sprintf("%d", g.Port)))
	fmt.Println("  " + ui.KeyValue("Username", g.Username))
	fmt.Println("  " + ui.KeyValue("Password", password))
	fmt.Println("  " + ui.KeyValue("Security", "TLS"))
	if !gmailShowPassword {
		fmt.Println()
		fmt.Println(ui.Dim.Render("  Password is " + g.PasswordHint + " — re-run with --show-password to print it."))
	}
	fmt.Println()

	return nil
}
