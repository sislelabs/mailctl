package flow

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sislelabs/mailctl/internal/ui"
)

// StepFunc is the signature for all step implementations.
type StepFunc func(ctx *StepContext, args map[string]interface{}) (interface{}, error)

// StepContext is the runtime context available to every step.
type StepContext struct {
	Config  interface{}            // *internal.Config
	Outputs map[string]interface{} // named outputs from previous steps
	Args    map[string]string      // CLI positional args
	Flags   map[string]string      // CLI flags
	DryRun  bool
}

// stepRegistry holds all registered step functions by name.
var stepRegistry = map[string]StepFunc{}

// RegisterStep adds a step function to the registry.
func RegisterStep(name string, fn StepFunc) {
	stepRegistry[name] = fn
}

// GetStep looks up a step function by name.
func GetStep(name string) StepFunc {
	return stepRegistry[name]
}

// StepArg is a helper to get a string arg from the args map.
func StepArg(args map[string]interface{}, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	// Template renders missing values as "<no value>"
	if s == "<no value>" || s == "<nil>" {
		return ""
	}
	return s
}

// StepArgInt is a helper to get an int arg.
func StepArgInt(args map[string]interface{}, key string) int {
	v, ok := args[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	default:
		return 0
	}
}

// registerGenericSteps registers the built-in generic steps.
func registerGenericSteps() {
	RegisterStep("print", stepPrint)
	RegisterStep("exit", stepExit)
	RegisterStep("confirm", stepConfirm)
	RegisterStep("prompt", stepPrompt)
	RegisterStep("config.load", stepConfigLoad)
}

// ErrFlowExit is returned by the exit step to cleanly stop flow execution.
var ErrFlowExit = fmt.Errorf("flow exit")

func stepPrint(ctx *StepContext, args map[string]interface{}) (interface{}, error) {
	message := StepArg(args, "message")
	style := StepArg(args, "style")

	switch style {
	case "success":
		fmt.Println(ui.SuccessPanel.Render(message))
	case "warn":
		fmt.Println(ui.WarnPanel.Render(message))
	case "error":
		fmt.Println(ui.ErrorPanel.Render(message))
	case "dim":
		fmt.Println(ui.Dim.Render(message))
	case "info":
		fmt.Println(ui.InfoPanel.Render(message))
	default:
		fmt.Println(message)
	}
	return nil, nil
}

func stepExit(ctx *StepContext, args map[string]interface{}) (interface{}, error) {
	message := StepArg(args, "message")
	if message != "" {
		fmt.Println(message)
	}
	return nil, ErrFlowExit
}

func stepConfirm(ctx *StepContext, args map[string]interface{}) (interface{}, error) {
	message := StepArg(args, "message")
	expected := StepArg(args, "expected")
	if expected == "" {
		expected = "yes"
	}
	confirmed, err := ui.RunConfirm(message, expected)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"confirmed": confirmed}, nil
}

func stepPrompt(ctx *StepContext, args map[string]interface{}) (interface{}, error) {
	title := StepArg(args, "title")
	fieldsRaw, ok := args["fields"]
	if !ok {
		return nil, fmt.Errorf("prompt step requires 'fields' arg")
	}

	fieldSlice, ok := fieldsRaw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("prompt 'fields' must be a list")
	}

	var wizardFields []ui.WizardField
	for _, f := range fieldSlice {
		fm, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		wf := ui.WizardField{}
		if v, ok := fm["label"].(string); ok {
			wf.Label = v
		}
		if v, ok := fm["placeholder"].(string); ok {
			wf.Placeholder = v
		}
		if v, ok := fm["value"].(string); ok {
			wf.Value = v
		}
		if v, ok := fm["password"].(bool); ok {
			wf.Password = v
		}
		wizardFields = append(wizardFields, wf)
	}

	values, completed, err := ui.RunWizard(title, wizardFields)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"values":    values,
		"completed": completed,
	}, nil
}

func stepConfigLoad(ctx *StepContext, args map[string]interface{}) (interface{}, error) {
	return ctx.Config, nil
}


// InitSteps registers all generic steps. Called once at startup.
func InitSteps() {
	registerGenericSteps()
}

// OutputString is a helper to extract a string from step output.
func OutputString(output interface{}, key string) string {
	m, ok := output.(map[string]interface{})
	if !ok {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// OutputBool is a helper to extract a bool from step output.
func OutputBool(output interface{}, key string) bool {
	m, ok := output.(map[string]interface{})
	if !ok {
		return false
	}
	v, ok := m[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// OutputSlice is a helper to extract a slice from step output.
func OutputSlice(output interface{}) []interface{} {
	if s, ok := output.([]interface{}); ok {
		return s
	}
	return nil
}

// OutputStringSlice is a helper to extract []string from step output.
func OutputStringSlice(output interface{}, key string) []string {
	m, ok := output.(map[string]interface{})
	if !ok {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	raw, ok := v.([]interface{})
	if !ok {
		if ss, ok := v.([]string); ok {
			return ss
		}
		return nil
	}
	var result []string
	for _, item := range raw {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// trimAndLower is used for condition evaluation.
func isTruthy(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && s != "false" && s != "0" && s != "<nil>" && s != "<no value>"
}
