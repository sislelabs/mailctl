package cmd

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sislelabs/mailctl/internal/dmarc"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/spf13/cobra"
)

var dmarcCmd = &cobra.Command{
	Use:   "dmarc [file...]",
	Short: "Read a DMARC aggregate report",
	Long: `Summarise the DMARC aggregate reports receivers send to your rua address.

They arrive daily as gzipped or zipped XML and are the only feedback anyone
gets on whether mail authenticates at the receiver rather than merely looking
correct in DNS. Reading them here keeps your sending IPs and volumes off
third-party analysers.

Pass the attachment straight from your downloads; .gz, .zip and plain .xml all
work, and a directory reads every report in it.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runDmarc,
}

var dmarcNoLookup bool

func init() {
	dmarcCmd.Flags().BoolVar(&dmarcNoLookup, "no-lookup", false, "Skip reverse DNS on sending IPs")
}

func runDmarc(cmd *cobra.Command, args []string) error {
	files, err := expandReportPaths(args)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no report files found")
	}

	for _, path := range files {
		if err := renderReport(path); err != nil {
			fmt.Println()
			fmt.Println(ui.StepResult(ui.IconWarn, ui.Warn.Render(filepath.Base(path))+" "+ui.Dim.Render(err.Error())))
		}
	}
	fmt.Println()
	return nil
}

// expandReportPaths turns arguments into a list of report files, reading a
// directory's contents when given one.
func expandReportPaths(args []string) ([]string, error) {
	var out []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			out = append(out, arg)
			continue
		}
		entries, err := os.ReadDir(arg)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := strings.ToLower(e.Name())
			if strings.HasSuffix(name, ".gz") || strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".xml") {
				out = append(out, filepath.Join(arg, e.Name()))
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func renderReport(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	report, err := dmarc.Parse(data)
	if err != nil {
		return err
	}

	total, passed, failed := report.Totals()

	fmt.Println()
	fmt.Println(ui.Heading("  " + report.Policy.Domain + " — reported by " + report.Metadata.OrgName))
	fmt.Println("  " + ui.Dim.Render(fmt.Sprintf("%s → %s · %s",
		report.Begin().Local().Format("2006-01-02 15:04"),
		report.End().Local().Format("2006-01-02 15:04"),
		report.PolicySummary())))
	fmt.Println()

	if total == 0 {
		fmt.Println(ui.StepResult(ui.IconDot, ui.Dim.Render("no messages reported in this period")))
		return nil
	}

	for _, rec := range report.Records {
		icon, style := ui.IconSuccess, ui.Success
		if !rec.Passed() {
			icon, style = ui.IconError, ui.Error
		}

		source := rec.Row.SourceIP
		if host := reverseLookup(rec.Row.SourceIP); host != "" {
			source += ui.Dim.Render(" (" + host + ")")
		}

		fmt.Println(ui.StepResult(icon, fmt.Sprintf("%s  %s",
			ui.White.Render(fmt.Sprintf("%d msg", rec.Row.Count)), source)))
		fmt.Println("      " + ui.Dim.Render(fmt.Sprintf("aligned: dkim=%s spf=%s → %s",
			rec.Row.PolicyEvaluated.DKIM, rec.Row.PolicyEvaluated.SPF,
			style.Render(dispositionText(rec.Row.PolicyEvaluated.Disposition, rec.Passed())))))

		// SPF breaks whenever mail is forwarded, because the forwarding server is
		// not in the original sender's SPF record. DKIM survives, which is the
		// whole reason DMARC accepts either. Saying so keeps a healthy report
		// from reading as a half-failure.
		if rec.Passed() && rec.Row.PolicyEvaluated.DKIM == "pass" && rec.Row.PolicyEvaluated.SPF != "pass" {
			fmt.Println("      " + ui.Dim.Render("SPF does not survive forwarding; DKIM carried the alignment"))
		}

		// The raw results explain a failure the aligned verdict only reports:
		// a DKIM signature can pass while signing the wrong domain.
		if !rec.Passed() {
			for _, d := range rec.AuthResults.DKIM {
				fmt.Println("      " + ui.Dim.Render(fmt.Sprintf("dkim %s selector=%s → %s", d.Domain, d.Selector, d.Result)))
			}
			for _, s := range rec.AuthResults.SPF {
				fmt.Println("      " + ui.Dim.Render(fmt.Sprintf("spf  %s → %s", s.Domain, s.Result)))
			}
			if from := rec.Identifiers.HeaderFrom; from != "" && !strings.EqualFold(from, report.Policy.Domain) {
				fmt.Println("      " + ui.Warn.Render("header from: "+from))
			}
		}
	}

	fmt.Println()
	switch {
	case failed == 0:
		fmt.Println(ui.StepResult(ui.IconSuccess,
			ui.Success.Render(fmt.Sprintf("all %d message(s) passed DMARC", total))))
	default:
		fmt.Println(ui.StepResult(ui.IconError,
			ui.Error.Render(fmt.Sprintf("%d of %d message(s) failed DMARC", failed, total))+
				" "+ui.Dim.Render(fmt.Sprintf("(%.0f%% passed)", report.PercentPassed()))))
		fmt.Println("      " + ui.Dim.Render("A failing source is either mail you send through something unaligned,"))
		fmt.Println("      " + ui.Dim.Render("or someone else sending as your domain."))
	}
	_ = passed

	return nil
}

func dispositionText(disposition string, passed bool) string {
	if passed {
		return "pass"
	}
	if disposition == "" || disposition == "none" {
		return "fail (delivered anyway — policy is p=none)"
	}
	return "fail (" + disposition + ")"
}

// reverseLookup names a sending IP so an unexpected source is recognisable.
func reverseLookup(ip string) string {
	if dmarcNoLookup || ip == "" {
		return ""
	}
	names, err := net.LookupAddr(ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(names[0], ".")
}
