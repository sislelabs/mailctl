package cmd

import (
	"fmt"

	"github.com/sislelabs/mailctl/internal/mailsetup"
	"github.com/sislelabs/mailctl/internal/store"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync [domain]",
	Short: "Adopt live Cloudflare routing into config",
	Long: `Reconcile mailctl's config with the routing rules Cloudflare actually has.

Rules can be edited in the Cloudflare dashboard, and mailctl's config is a cache
that goes stale as soon as they are. sync adopts the live state: it records
where each address really delivers, picks up addresses added outside mailctl,
and drops entries with no routing rule behind them.

It only writes to config. Nothing in Cloudflare is created, changed or deleted,
so it is always safe to run.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSync,
}

var syncDryRun bool

func init() {
	syncCmd.Flags().BoolVar(&syncDryRun, "dry-run", false, "Show what would change without writing config")
}

func runSync(cmd *cobra.Command, args []string) error {
	results, err := mailsetup.Sync(store.NewYAML(), args, syncDryRun)
	if err != nil {
		return err
	}

	fmt.Println()
	if syncDryRun {
		fmt.Println(ui.Highlight.Render("  Dry run — config will not be written"))
		fmt.Println()
	}

	total := 0
	for _, r := range results {
		fmt.Println(ui.Heading("  " + r.Domain))
		if r.Err != nil {
			fmt.Println(ui.StepResult(ui.IconWarn, ui.Warn.Render("could not read routing: ")+ui.Dim.Render(r.Err.Error())))
			fmt.Println()
			continue
		}
		if len(r.Changes) == 0 {
			fmt.Println(ui.StepResult(ui.IconSuccess, ui.Dim.Render("already in sync")))
			fmt.Println()
			continue
		}
		for _, c := range r.Changes {
			total++
			fmt.Println(ui.StepResult(syncIcon(c.Kind), ui.White.Render(c.Address)+" "+ui.Dim.Render(c.Kind.String())))
			switch c.Kind {
			case mailsetup.SyncRepointed:
				fmt.Println("      " + ui.Dim.Render("config said: "+joinAddrs(c.Was)))
				fmt.Println("      " + ui.Dim.Render("actually:    "+joinAddrs(c.Now)))
			case mailsetup.SyncAdopted:
				fmt.Println("      " + ui.Dim.Render("delivers to: "+joinAddrs(c.Now)))
			case mailsetup.SyncDropped:
				fmt.Println("      " + ui.Dim.Render("no routing rule in Cloudflare — removed from config only"))
			}
		}
		fmt.Println()
	}

	if total == 0 {
		fmt.Println(ui.Dim.Render("  Nothing to reconcile."))
	} else if syncDryRun {
		fmt.Println(ui.Dim.Render(fmt.Sprintf("  %d change(s) — run without --dry-run to apply.", total)))
	} else {
		fmt.Println(ui.Success.Render(fmt.Sprintf("  %d change(s) written to config.", total)))
	}
	fmt.Println()

	return nil
}

func syncIcon(k mailsetup.SyncChangeKind) string {
	switch k {
	case mailsetup.SyncDropped:
		return ui.IconWarn
	default:
		return ui.IconDot
	}
}

func joinAddrs(addrs []string) string {
	if len(addrs) == 0 {
		return "(none)"
	}
	out := addrs[0]
	for _, a := range addrs[1:] {
		out += ", " + a
	}
	return out
}
