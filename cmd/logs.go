package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/resend"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show recently sent emails and their delivery status",
	Long: `Show recently sent emails and their delivery status.

Covers outbound mail only. Messages received at your domains are forwarded by
Cloudflare Email Routing, which exposes no delivery-history API, so they cannot
be listed here — check the destination inbox for those.`,
	Args: cobra.NoArgs,
	RunE: runLogs,
}

var (
	logsLimit  int
	logsDomain string
	logsStatus string
	logsJSON   bool
)

func init() {
	logsCmd.Flags().IntVarP(&logsLimit, "limit", "n", 20, "Number of emails to show")
	logsCmd.Flags().StringVarP(&logsDomain, "domain", "d", "", "Only show mail sent from this domain")
	logsCmd.Flags().StringVarP(&logsStatus, "status", "s", "", "Only show this delivery status (delivered, bounced, complained, ...)")
	logsCmd.Flags().BoolVar(&logsJSON, "json", false, "Output raw JSON")
}

func runLogs(cmd *cobra.Command, args []string) error {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return err
	}

	if cfg.SendingProvider() != internal.ProviderResend {
		return fmt.Errorf("mailctl logs requires the Resend provider — Brevo exposes no equivalent send log")
	}
	if cfg.ResendAPIKey == "" {
		return fmt.Errorf("no resend_api_key in config — run 'mailctl init'")
	}

	rc := resend.NewClient(cfg.ResendAPIKey)
	emails, err := rc.ListEmailsN(logsLimit)
	if err != nil {
		return err
	}

	emails = filterEmails(emails, logsDomain, logsStatus)

	if logsJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(emails)
	}

	renderLogs(emails)
	return nil
}

// filterEmails narrows the list by sending domain and delivery status. Both
// filters are applied client-side; Resend's list endpoint takes neither.
func filterEmails(emails []resend.EmailSummary, domain, status string) []resend.EmailSummary {
	if domain == "" && status == "" {
		return emails
	}

	var out []resend.EmailSummary
	for _, e := range emails {
		if domain != "" && !strings.Contains(strings.ToLower(e.From), "@"+strings.ToLower(domain)) {
			continue
		}
		if status != "" && !strings.EqualFold(e.LastEvent, status) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func renderLogs(emails []resend.EmailSummary) {
	fmt.Println()

	if len(emails) == 0 {
		fmt.Println(ui.Dim.Render("  No sent emails match."))
		fmt.Println()
		return
	}

	var b strings.Builder
	for _, e := range emails {
		b.WriteString(logEventBadge(e.LastEvent))
		b.WriteString("  ")
		b.WriteString(ui.Dim.Render(formatLogTime(e.CreatedAt)))
		b.WriteString("  ")
		b.WriteString(truncate(e.Subject, 40))
		b.WriteString("\n")
		b.WriteString("            ")
		b.WriteString(ui.Dim.Render(truncate(e.From, 32) + " → " + truncate(strings.Join(e.To, ", "), 32)))
		b.WriteString("\n")
	}

	fmt.Println(ui.Heading(fmt.Sprintf("  Sent email (%d)", len(emails))))
	fmt.Println()
	fmt.Print(b.String())
	fmt.Println()
	fmt.Println(ui.Dim.Render("  Outbound only — received mail is forwarded by Cloudflare and is not logged."))
	fmt.Println()
}

// logEventBadge colours a delivery event by whether it needs attention.
// Bounces and complaints are the ones that damage sending reputation.
func logEventBadge(event string) string {
	if event == "" {
		event = "unknown"
	}

	var style lipgloss.Style
	switch strings.ToLower(event) {
	case "delivered", "opened", "clicked":
		style = ui.Success
	case "bounced", "complained", "failed":
		style = ui.Error
	case "delivery_delayed":
		style = ui.Warn
	default:
		style = ui.Dim
	}

	return "  " + style.Render(fmt.Sprintf("%-10s", truncate(event, 10)))
}

// formatLogTime renders Resend's timestamps compactly, falling back to the raw
// string when the format is not what we expect.
func formatLogTime(raw string) string {
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999-07",
		"2006-01-02 15:04:05.999999+00",
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Local().Format("Jan 02 15:04")
		}
	}
	if len(raw) >= 16 {
		return raw[:16]
	}
	return raw
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}
