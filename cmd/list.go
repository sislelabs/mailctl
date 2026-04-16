package cmd

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/brevo"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all domains with live status",
	RunE:    runList,
}

func runList(cmd *cobra.Command, args []string) error {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return err
	}

	if len(cfg.Domains) == 0 {
		fmt.Println()
		fmt.Println(ui.InfoPanel.Render(
			ui.Info.Render("No domains configured") + "\n\n" +
				ui.Dim.Render("Get started: ") + ui.Highlight.Render("mailctl add <domain> -a hello,support"),
		))
		return nil
	}

	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	bv := brevo.NewClient(cfg.BrevoAPIKey)

	colDomain := 28
	colCF := 16
	colBrevo := 16

	header := lipgloss.JoinHorizontal(lipgloss.Top,
		ui.TableHeader.Width(colDomain).Render("DOMAIN"),
		ui.TableHeader.Width(colCF).Render("ROUTING"),
		ui.TableHeader.Width(colBrevo).Render("SENDING"),
		ui.TableHeader.Render("ALIASES"),
	)

	sep := ui.Dim.Render(strings.Repeat("─", 80))

	var rows []string
	for _, d := range cfg.Domains {
		cfStatus := ui.IconWarn + " " + ui.Dim.Render("unknown")
		if status, err := cf.GetEmailRoutingStatus(d.CloudflareZoneID); err == nil {
			if status.Enabled {
				cfStatus = ui.IconSuccess + " " + ui.Success.Render("enabled")
			} else {
				cfStatus = ui.IconError + " " + ui.Error.Render("disabled")
			}
		}

		brevoStatus := ui.Dim.Render("—")
		if bDomain, err := bv.GetDomain(d.Domain); err == nil {
			if bDomain.Authenticated {
				brevoStatus = ui.IconSuccess + " " + ui.Success.Render("authenticated")
			} else if bDomain.Verified {
				brevoStatus = ui.IconPending + " " + ui.Info.Render("verified")
			} else {
				brevoStatus = ui.IconPending + " " + ui.Info.Render("pending")
			}
		}

		var aliasList []string
		for _, a := range d.Aliases {
			aliasList = append(aliasList, ui.Dim.Render(a.Alias+"@"))
		}
		aliasStr := strings.Join(aliasList, ui.Dim.Render(", "))

		row := lipgloss.JoinHorizontal(lipgloss.Top,
			ui.White.Bold(true).Width(colDomain).Render(d.Domain),
			lipgloss.NewStyle().Width(colCF).Render(cfStatus),
			lipgloss.NewStyle().Width(colBrevo).Render(brevoStatus),
			lipgloss.NewStyle().Render(aliasStr),
		)
		rows = append(rows, row)
	}

	fmt.Println()
	fmt.Println(" " + ui.Banner())
	fmt.Println()
	fmt.Println(" " + header)
	fmt.Println(" " + sep)
	for _, row := range rows {
		fmt.Println(" " + row)
	}
	fmt.Println()
	fmt.Printf(" %s\n\n", ui.Dim.Render(fmt.Sprintf("%d domain(s)", len(cfg.Domains))))

	return nil
}
