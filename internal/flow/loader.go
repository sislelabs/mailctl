package flow

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sislelabs/mailctl/internal/flows"
)

func LoadAll() ([]*FlowDef, error) {
	byName := map[string]*FlowDef{}

	// 1. Load embedded builtin flows
	entries, err := fs.ReadDir(flows.BuiltinFS, "builtin")
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
				continue
			}
			data, err := fs.ReadFile(flows.BuiltinFS, "builtin/"+entry.Name())
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to read builtin flow %s: %v\n", entry.Name(), err)
				continue
			}
			def, err := ParseFlowYAML(data)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to parse builtin flow %s: %v\n", entry.Name(), err)
				continue
			}
			if def.Name == "_placeholder" {
				continue
			}
			if warnings := ValidateFlow(def); len(warnings) > 0 {
				for _, w := range warnings {
					fmt.Fprintf(os.Stderr, "warning: flow %s: %s\n", def.Name, w)
				}
			}
			def.Source = "builtin"
			byName[def.Name] = def
		}
	}

	// 2. Load user flows from ~/.mailctl/flows/
	userDir := userFlowsDir()
	if entries, err := os.ReadDir(userDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(userDir, entry.Name()))
			if err != nil {
				continue
			}
			def, err := ParseFlowYAML(data)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to parse %s: %v\n", entry.Name(), err)
				continue
			}
			if warnings := ValidateFlow(def); len(warnings) > 0 {
				for _, w := range warnings {
					fmt.Fprintf(os.Stderr, "warning: flow %s: %s\n", def.Name, w)
				}
			}
			def.Source = "user"
			byName[def.Name] = def // user overrides builtin
		}
	}

	var result []*FlowDef
	for _, def := range byName {
		result = append(result, def)
	}
	return result, nil
}

func userFlowsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".mailctl/flows"
	}
	return filepath.Join(home, ".mailctl", "flows")
}
