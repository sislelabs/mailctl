package flow

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// ── YAML Flow Definition Types ──────────────────────────────────────────────

type FlowArg struct {
	Name        string `yaml:"name"`
	Required    bool   `yaml:"required"`
	Description string `yaml:"description"`
}

// FlowFlagDef is the YAML-parsed flag definition (renamed from FlowFlag to
// avoid conflict with the FlowFlag type in registry.go).
type FlowFlagDef struct {
	Name        string `yaml:"name"`
	Short       string `yaml:"short"`
	Description string `yaml:"description"`
	Default     string `yaml:"default"`
	Required    bool   `yaml:"required"`
	Type        string `yaml:"type"`
}

type FlowDef struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Group       string       `yaml:"group"`
	Args        []FlowArg    `yaml:"args"`
	Flags       []FlowFlagDef `yaml:"flags"`
	Config      []string     `yaml:"config"`
	From        string       `yaml:"from"`
	Steps       []StepEntry  `yaml:"steps"`
	Source      string       `yaml:"-"` // "builtin" or "user"
}

// IsInteractive returns true if the flow contains steps that need user input (prompt, confirm).
func (d *FlowDef) IsInteractive() bool {
	return hasInteractiveStep(d.Steps)
}

func hasInteractiveStep(steps []StepEntry) bool {
	for _, s := range steps {
		if s.Step == "prompt" || s.Step == "confirm" {
			return true
		}
		if hasInteractiveStep(s.Steps) || hasInteractiveStep(s.Else) {
			return true
		}
	}
	return false
}

type StepEntry struct {
	// Normal step
	Step   string                 `yaml:"step"`
	Args   map[string]interface{} `yaml:"args"`
	Output string                 `yaml:"output"`

	// Conditional
	If   string      `yaml:"if"`
	Else []StepEntry `yaml:"else"`

	// Loop
	ForEach string `yaml:"for_each"`
	As      string `yaml:"as"`

	// Nested steps (used by if and for_each)
	Steps []StepEntry `yaml:"steps"`
}

// ── Engine ──────────────────────────────────────────────────────────────────

type Engine struct {
	config interface{}
}

func NewEngine(config interface{}) *Engine {
	return &Engine{config: config}
}

func (e *Engine) Run(flowDef *FlowDef, args map[string]string, flags map[string]string) error {
	ctx := &StepContext{
		Config:  e.config,
		Outputs: map[string]interface{}{},
		Args:    args,
		Flags:   flags,
		DryRun:  flags["dry-run"] == "true",
	}

	// Make args, flags, and flow metadata available in template context
	ctx.Outputs["args"] = args
	ctx.Outputs["flags"] = flags
	ctx.Outputs["config"] = e.config
	if flowDef.From != "" {
		ctx.Outputs["flow_from"] = flowDef.From
	}

	err := e.executeSteps(ctx, flowDef.Steps)
	if errors.Is(err, ErrFlowExit) {
		return nil // clean exit
	}
	return err
}

func (e *Engine) executeSteps(ctx *StepContext, steps []StepEntry) error {
	for _, entry := range steps {
		switch {
		case entry.If != "":
			if err := e.executeIf(ctx, entry); err != nil {
				return err
			}
		case entry.ForEach != "":
			if err := e.executeForEach(ctx, entry); err != nil {
				return err
			}
		case entry.Step != "":
			if err := e.executeStep(ctx, entry); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) executeStep(ctx *StepContext, entry StepEntry) error {
	fn := GetStep(entry.Step)
	if fn == nil {
		return fmt.Errorf("unknown step: %s", entry.Step)
	}

	// Evaluate template expressions in args
	evaluatedArgs, err := e.evaluateArgs(ctx, entry.Args)
	if err != nil {
		return fmt.Errorf("step %s: evaluate args: %w", entry.Step, err)
	}

	// Execute step
	output, err := fn(ctx, evaluatedArgs)
	if err != nil {
		return err
	}

	// Store named output
	if entry.Output != "" && output != nil {
		ctx.Outputs[entry.Output] = output
	}

	return nil
}

func (e *Engine) executeIf(ctx *StepContext, entry StepEntry) error {
	result, err := e.evaluateTemplate(ctx, entry.If)
	if err != nil {
		return fmt.Errorf("if condition: %w", err)
	}

	if isTruthy(result) {
		return e.executeSteps(ctx, entry.Steps)
	} else if len(entry.Else) > 0 {
		return e.executeSteps(ctx, entry.Else)
	}
	return nil
}

func (e *Engine) executeForEach(ctx *StepContext, entry StepEntry) error {
	// Resolve the collection directly from outputs (skip template evaluation —
	// templates convert typed slices to strings which we can't iterate)
	collection := e.resolveCollection(ctx, entry.ForEach)
	if collection == nil {
		return fmt.Errorf("for_each: could not resolve collection from %q", entry.ForEach)
	}

	asName := entry.As
	if asName == "" {
		asName = "item"
	}

	for _, item := range collection {
		ctx.Outputs[asName] = item
		if err := e.executeSteps(ctx, entry.Steps); err != nil {
			return err
		}
	}

	return nil
}

func (e *Engine) resolveCollection(ctx *StepContext, expr string) []interface{} {
	// Strip template delimiters to get the variable name
	name := expr
	name = stripTemplateDelims(name)

	// Look up in outputs
	if v, ok := ctx.Outputs[name]; ok {
		return toSlice(v)
	}

	// Try dot notation: .products -> outputs["products"]
	if len(name) > 0 && name[0] == '.' {
		name = name[1:]
	}
	if v, ok := ctx.Outputs[name]; ok {
		return toSlice(v)
	}

	return nil
}

func stripTemplateDelims(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "{{")
	s = strings.TrimSuffix(s, "}}")
	s = strings.TrimSpace(s)
	return s
}

func toSlice(v interface{}) []interface{} {
	if v == nil {
		return nil
	}
	if s, ok := v.([]interface{}); ok {
		return s
	}
	// Use reflection for typed slices
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice {
		result := make([]interface{}, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			result[i] = rv.Index(i).Interface()
		}
		return result
	}
	return nil
}

// ── Template Evaluation ─────────────────────────────────────────────────────

func (e *Engine) evaluateTemplate(ctx *StepContext, tmplStr string) (string, error) {
	if !strings.Contains(tmplStr, "{{") {
		return tmplStr, nil
	}

	tmpl, err := template.New("").Option("missingkey=zero").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", tmplStr, err)
	}

	data := e.buildTemplateData(ctx)
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template %q: %w", tmplStr, err)
	}

	return buf.String(), nil
}

func (e *Engine) evaluateArgs(ctx *StepContext, args map[string]interface{}) (map[string]interface{}, error) {
	if args == nil {
		return map[string]interface{}{}, nil
	}

	result := map[string]interface{}{}
	for k, v := range args {
		evaluated, err := e.evaluateValue(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("arg %q: %w", k, err)
		}
		result[k] = evaluated
	}
	return result, nil
}

func (e *Engine) evaluateValue(ctx *StepContext, v interface{}) (interface{}, error) {
	switch val := v.(type) {
	case string:
		if strings.Contains(val, "{{") {
			// For simple output references like "{{.product}}", return the raw object
			if isOutputReference(val) {
				name := stripTemplateDelims(val)
				if len(name) > 0 && name[0] == '.' {
					name = name[1:]
				}
				if obj, ok := ctx.Outputs[name]; ok {
					return obj, nil
				}
			}
			// Otherwise evaluate as template string
			evaluated, err := e.evaluateTemplate(ctx, val)
			if err != nil {
				return nil, err
			}
			return evaluated, nil
		}
		return val, nil
	case map[string]interface{}:
		result := map[string]interface{}{}
		for mk, mv := range val {
			ev, err := e.evaluateValue(ctx, mv)
			if err != nil {
				return nil, err
			}
			result[mk] = ev
		}
		return result, nil
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			ev, err := e.evaluateValue(ctx, item)
			if err != nil {
				return nil, err
			}
			result[i] = ev
		}
		return result, nil
	default:
		return v, nil
	}
}

// isOutputReference checks if a template string is a simple variable reference like "{{.product}}"
func isOutputReference(tmplStr string) bool {
	s := strings.TrimSpace(tmplStr)
	return strings.HasPrefix(s, "{{.") && strings.HasSuffix(s, "}}") &&
		strings.Count(s, "{{") == 1 && !strings.Contains(s, " ")
}

func (e *Engine) buildTemplateData(ctx *StepContext) map[string]interface{} {
	data := map[string]interface{}{}
	// Copy all outputs
	for k, v := range ctx.Outputs {
		data[k] = v
	}
	// Ensure args and flags are always accessible
	// Sanitize keys: replace hyphens with underscores for Go template compatibility
	data["args"] = sanitizeKeys(ctx.Args)
	data["flags"] = sanitizeKeys(ctx.Flags)
	data["config"] = ctx.Config
	return data
}

func sanitizeKeys(m map[string]string) map[string]interface{} {
	result := map[string]interface{}{}
	for k, v := range m {
		// Keep original key
		result[k] = v
		// Also add underscore version for template access
		sanitized := strings.ReplaceAll(k, "-", "_")
		if sanitized != k {
			result[sanitized] = v
		}
	}
	return result
}

// ── Validation ──────────────────────────────────────────────────────────────

// ValidateFlow checks that all step names in a flow definition are registered.
func ValidateFlow(def *FlowDef) []string {
	var warnings []string
	validateSteps(def.Steps, &warnings)
	return warnings
}

func validateSteps(steps []StepEntry, warnings *[]string) {
	for _, entry := range steps {
		if entry.Step != "" {
			if GetStep(entry.Step) == nil {
				*warnings = append(*warnings, fmt.Sprintf("unknown step: %s", entry.Step))
			}
		}
		if len(entry.Steps) > 0 {
			validateSteps(entry.Steps, warnings)
		}
		if len(entry.Else) > 0 {
			validateSteps(entry.Else, warnings)
		}
	}
}

// ── YAML Parsing ────────────────────────────────────────────────────────────

func ParseFlowYAML(data []byte) (*FlowDef, error) {
	var def FlowDef
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parse flow YAML: %w", err)
	}
	if def.Name == "" {
		return nil, fmt.Errorf("flow YAML missing required 'name' field")
	}
	return &def, nil
}
