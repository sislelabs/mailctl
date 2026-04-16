package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/flow"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/spf13/cobra"
)

var flowCmd = &cobra.Command{
	Use:   "flow",
	Short: "Manage and run flows",
}

var flowListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available flows",
	Args:  cobra.NoArgs,
	RunE:  runFlowList,
}

var flowRunCmd = &cobra.Command{
	Use:                "run <flow-name> [args...] [--flags...]",
	Short:              "Run a flow by name",
	DisableFlagParsing: true,
	RunE:               runFlowRun,
}

var flowNewCmd = &cobra.Command{
	Use:   "new",
	Short: "Scaffold a new flow YAML file",
	Args:  cobra.NoArgs,
	RunE:  runFlowNew,
}

func init() {
	flowCmd.AddCommand(flowListCmd)
	flowCmd.AddCommand(flowRunCmd)
	flowCmd.AddCommand(flowNewCmd)

	flowRunCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, f := range flow.All() {
			names = append(names, f.Name+"\t"+f.Def.Description)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

func runFlowList(cmd *cobra.Command, args []string) error {
	fmt.Println()
	fmt.Println(ui.Highlight.Render("Available Flows"))
	fmt.Println(ui.Dim.Render(strings.Repeat("─", 66)))

	for _, group := range flow.Groups() {
		fmt.Println()
		fmt.Println("  " + ui.Bold.Render(strings.ToUpper(group)))
		for _, f := range flow.ByGroup(group) {
			name := ui.Accent.Render(fmt.Sprintf("%-22s", f.Name))
			src := ""
			if f.Source == "user" {
				src = " " + ui.Dim.Render("[user]")
			}
			fmt.Println("    " + name + ui.Dim.Render(f.Def.Description) + src)
		}
	}

	fmt.Println()
	fmt.Println(ui.Dim.Render(strings.Repeat("─", 66)))
	fmt.Println()
	return nil
}

func runFlowRun(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mailctl flow run <flow-name> [args...] [--flags...]")
	}

	// Check for --help
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return runFlowDescribe(args[0])
		}
	}

	name := args[0]
	f := flow.Get(name)
	if f == nil {
		return fmt.Errorf("unknown flow: %s\nRun 'mailctl flow list' to see available flows", name)
	}

	cfg, err := internal.LoadConfig()
	if err != nil {
		return err
	}

	// Parse remaining args: positional args first, then flags
	flowArgs := map[string]string{}
	flags := map[string]string{}
	positionalIdx := 0
	remaining := args[1:]

	for i := 0; i < len(remaining); i++ {
		arg := remaining[i]
		if strings.HasPrefix(arg, "--") {
			flagName := strings.TrimPrefix(arg, "--")
			// Check for --flag=value
			if eqIdx := strings.Index(flagName, "="); eqIdx >= 0 {
				flags[flagName[:eqIdx]] = flagName[eqIdx+1:]
			} else {
				// Check if it's a bool flag (no next arg or next arg is also a flag)
				isBool := i+1 >= len(remaining) || strings.HasPrefix(remaining[i+1], "--")
				if isBool {
					flags[flagName] = "true"
				} else {
					flags[flagName] = remaining[i+1]
					i++
				}
			}
		} else {
			// Positional arg
			if positionalIdx < len(f.Def.Args) {
				flowArgs[f.Def.Args[positionalIdx].Name] = arg
				positionalIdx++
			}
		}
	}

	return flow.RunFlow(name, cfg, flowArgs, flags)
}

func runFlowNew(cmd *cobra.Command, args []string) error {
	values, ok, err := ui.RunWizard("New Flow", []ui.WizardField{
		{
			Label:       "Flow name",
			Help:        "Use group:action format, e.g. invoicing:send-reminder",
			Placeholder: "group:action",
		},
		{
			Label:       "Description",
			Help:        "Short description of what this flow does",
			Placeholder: "What does this flow do?",
		},
	})
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	name := strings.TrimSpace(values[0])
	description := strings.TrimSpace(values[1])

	if name == "" {
		return fmt.Errorf("flow name cannot be empty")
	}

	// Extract group from name (everything before ':', or "custom" if no ':')
	group := "custom"
	if idx := strings.Index(name, ":"); idx >= 0 {
		group = name[:idx]
	}

	// Error if a flow with that name already exists
	if existing := flow.Get(name); existing != nil {
		return fmt.Errorf("flow %q already exists (source: %s)", name, existing.Source)
	}

	// Determine output path
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not determine home directory: %w", err)
	}
	flowsDir := filepath.Join(home, ".mailctl", "flows")
	if err := os.MkdirAll(flowsDir, 0755); err != nil {
		return fmt.Errorf("could not create flows directory: %w", err)
	}

	// Use the part after ':' as the filename, or the full name if no ':'
	filename := name
	if idx := strings.Index(name, ":"); idx >= 0 {
		filename = name[idx+1:]
	}
	outPath := filepath.Join(flowsDir, group+"-"+filename+".yaml")

	if _, err := os.Stat(outPath); err == nil {
		return fmt.Errorf("file already exists: %s", outPath)
	}

	template := fmt.Sprintf(`name: "%s"
description: "%s"
group: "%s"
steps:
  - step: print
    args:
      message: "Hello from %s!"

  # Add your steps here. Available step types:
  #   print          — Print a message (style: success/warn/error/dim)
  #   exit           — Stop flow execution
  #   confirm        — Ask user to type a confirmation word
  #   prompt         — Interactive wizard with multiple fields
  #   config.load    — Load mailctl config
  #   email.send     — Send email with optional attachments
  #
  # Flow control:
  #   if: "{{<condition>}}"
  #     steps: [...]
  #     else: [...]
  #   for_each: "{{.collection}}"
  #     as: item
  #     steps: [...]
`, name, description, group, name)

	if err := os.WriteFile(outPath, []byte(template), 0644); err != nil {
		return fmt.Errorf("failed to write flow file: %w", err)
	}

	fmt.Println()
	fmt.Println(ui.IconSuccess + " " + ui.Success.Render("Flow scaffolded successfully"))
	fmt.Println(ui.Dim.Render("  "+outPath))
	fmt.Println()
	fmt.Println(ui.Dim.Render("  Run it with: ") + ui.Accent.Render("mailctl flow run "+name))
	fmt.Println()
	return nil
}

// runFlowDescribe shows flow details when --help is used with flow run.
func runFlowDescribe(name string) error {
	f := flow.Get(name)
	if f == nil {
		return fmt.Errorf("unknown flow: %s", name)
	}

	fmt.Println()
	fmt.Println(ui.Highlight.Render(f.Name))
	fmt.Println(ui.Dim.Render(f.Def.Description))
	fmt.Printf("  Source: %s\n", f.Source)

	if len(f.Def.Args) > 0 {
		fmt.Println()
		fmt.Println(ui.Bold.Render("  Arguments:"))
		for _, a := range f.Def.Args {
			req := ""
			if a.Required {
				req = " (required)"
			}
			desc := a.Description
			if desc == "" {
				desc = a.Name
			}
			fmt.Printf("    %-16s %s%s\n", a.Name, desc, ui.Dim.Render(req))
		}
	}

	if len(f.Def.Flags) > 0 {
		fmt.Println()
		fmt.Println(ui.Bold.Render("  Flags:"))
		for _, fl := range f.Def.Flags {
			desc := fl.Description
			if desc == "" {
				desc = fl.Name
			}
			fmt.Printf("    --%-14s %s\n", fl.Name, desc)
		}
	}

	if len(f.Def.Steps) > 0 {
		fmt.Println()
		fmt.Printf("  Steps: %d\n", len(f.Def.Steps))
	}

	fmt.Println()
	return nil
}
