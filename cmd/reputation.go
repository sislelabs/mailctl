package cmd

import (
	"fmt"
	"time"

	"github.com/sislelabs/mailctl/internal/mailsetup"
	"github.com/sislelabs/mailctl/internal/store"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/spf13/cobra"
)

var reputationCmd = &cobra.Command{
	Use:     "reputation [domain]",
	Aliases: []string{"rep"},
	Short:   "Show a domain's sending history and deliverability readiness",
	Long: `Report how much sending reputation a domain has had the chance to build.

Correct DNS and good inbox placement are different questions. A domain can hold
verified SPF, DKIM and DMARC and still be filtered, because authentication
proves a message is not forged while placement is decided on reputation. This
reports the half that 'mailctl check' cannot see.`,
	Args: cobra.ExactArgs(1),
	RunE: runReputation,
}

func runReputation(cmd *cobra.Command, args []string) error {
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

	rep, err := mailsetup.Reputation(cfg, d)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println(ui.Heading("  " + domain))
	fmt.Println(ui.StepResult(stageIcon(rep.Stage), stageStyle(rep.Stage).Render(rep.Stage.String())))
	fmt.Println()

	fmt.Println("  " + ui.KeyValue("Registered with Resend", humanAge(rep.RegisteredAt, time.Since(rep.RegisteredAt))))
	if !rep.FirstSent.IsZero() {
		fmt.Println("  " + ui.KeyValue("Sending since", humanAge(rep.FirstSent, rep.Age)))
	}
	fmt.Println("  " + ui.KeyValue("Sent", sentSummary(rep)))
	if !rep.LastSent.IsZero() {
		fmt.Println("  " + ui.KeyValue("Last sent", humanAge(rep.LastSent, time.Since(rep.LastSent))))
	}
	if rep.Sent > 0 {
		fmt.Println("  " + ui.KeyValue("Delivered", fmt.Sprintf("%d", rep.Delivered)))
		if rep.Bounced > 0 {
			fmt.Println("  " + ui.KeyValue("Bounced", ui.Error.Render(fmt.Sprintf("%d (%.1f%%)", rep.Bounced, rep.BounceRate()*100))))
		}
		if rep.Complained > 0 {
			fmt.Println("  " + ui.KeyValue("Complaints", ui.Error.Render(fmt.Sprintf("%d", rep.Complained))))
		}
	}
	fmt.Println("  " + ui.KeyValue("DMARC", dmarcSummary(rep)))

	if advice := rep.Advice(); len(advice) > 0 {
		fmt.Println()
		fmt.Println(ui.Heading("  What helps"))
		for _, a := range advice {
			fmt.Println(ui.StepResult(ui.IconDot, ui.Dim.Render(a)))
		}
	}
	fmt.Println()

	return nil
}

func stageIcon(s mailsetup.Stage) string {
	switch s {
	case mailsetup.StageEstablished:
		return ui.IconSuccess
	case mailsetup.StageWarming:
		return ui.IconPending
	default:
		return ui.IconWarn
	}
}

func stageStyle(s mailsetup.Stage) interface{ Render(...string) string } {
	switch s {
	case mailsetup.StageEstablished:
		return ui.Success
	case mailsetup.StageWarming:
		return ui.Info
	default:
		return ui.Warn
	}
}

func sentSummary(rep *mailsetup.Deliverability) string {
	if rep.Sent == 0 {
		return ui.Warn.Render("never")
	}
	if rep.Capped {
		return fmt.Sprintf("%d+ (recent history only)", rep.Sent)
	}
	return fmt.Sprintf("%d", rep.Sent)
}

func dmarcSummary(rep *mailsetup.Deliverability) string {
	if rep.DMARCPolicy == "" {
		return ui.Warn.Render("not published")
	}
	out := "p=" + rep.DMARCPolicy
	if rep.DMARCReports {
		return out + ui.Dim.Render(", reporting on")
	}
	return out + ui.Warn.Render(", no reporting")
}

// humanAge renders a timestamp with how long ago it was, since the elapsed
// time is the part that matters for reputation.
func humanAge(t time.Time, age time.Duration) string {
	if t.IsZero() {
		return ui.Dim.Render("unknown")
	}
	days := int(age.Hours() / 24)
	switch {
	case days < 1:
		return fmt.Sprintf("%s (%d hours ago)", t.Format("2006-01-02"), int(age.Hours()))
	case days == 1:
		return fmt.Sprintf("%s (yesterday)", t.Format("2006-01-02"))
	default:
		return fmt.Sprintf("%s (%d days ago)", t.Format("2006-01-02"), days)
	}
}
