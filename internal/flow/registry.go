package flow

import (
	"fmt"
	"sort"
)

// Flow represents a loaded flow definition, ready to execute.
type Flow struct {
	Def    *FlowDef
	Name   string
	Source string // "builtin" or "user"
}

var registry = map[string]*Flow{}

// LoadAndRegisterAll discovers YAML flows and populates the registry.
func LoadAndRegisterAll() error {
	defs, err := LoadAll()
	if err != nil {
		return fmt.Errorf("load flows: %w", err)
	}
	for _, def := range defs {
		registry[def.Name] = &Flow{
			Def:    def,
			Name:   def.Name,
			Source: def.Source,
		}
	}
	return nil
}

func Get(name string) *Flow {
	return registry[name]
}

func All() []*Flow {
	var flows []*Flow
	for _, f := range registry {
		flows = append(flows, f)
	}
	sort.Slice(flows, func(i, j int) bool {
		return flows[i].Name < flows[j].Name
	})
	return flows
}

func Groups() []string {
	seen := map[string]bool{}
	for _, f := range registry {
		if f.Def.Group != "" {
			seen[f.Def.Group] = true
		}
	}
	var groups []string
	for g := range seen {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	return groups
}

func ByGroup(group string) []*Flow {
	var flows []*Flow
	for _, f := range registry {
		if f.Def.Group == group {
			flows = append(flows, f)
		}
	}
	sort.Slice(flows, func(i, j int) bool {
		return flows[i].Name < flows[j].Name
	})
	return flows
}

func RunFlow(name string, config interface{}, args map[string]string, flags map[string]string) error {
	f := Get(name)
	if f == nil {
		return fmt.Errorf("unknown flow: %s", name)
	}
	engine := NewEngine(config)
	return engine.Run(f.Def, args, flags)
}
