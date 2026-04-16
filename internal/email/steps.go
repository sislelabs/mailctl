package email

import (
	"fmt"

	"github.com/sislelabs/mailctl/internal/flow"
	"github.com/sislelabs/mailctl/internal/ui"
)

func RegisterSteps(smtpCfgProvider func() *SMTPConfig) {
	flow.RegisterStep("email.send", func(ctx *flow.StepContext, args map[string]interface{}) (interface{}, error) {
		smtpCfg := smtpCfgProvider()
		if smtpCfg == nil {
			msg := ui.Error.Bold(true).Render("SMTP not configured.") + "\n\n" +
				"Add the following to " + ui.Accent.Render("~/.mailctl.yaml") + ":\n\n" +
				ui.Dim.Render("  smtp:\n    host: \"smtp.example.com\"\n    port: 587\n    username: \"you@example.com\"\n    password: \"your-password\"\n    default_from: \"you@example.com\"")
			return nil, fmt.Errorf("%s", ui.WarnPanel.Render(msg))
		}

		to := flow.StepArg(args, "to")
		subject := flow.StepArg(args, "subject")
		body := flow.StepArg(args, "body")
		from := flow.StepArg(args, "from")
		// Fall back to flow-level from address
		if from == "" {
			if flowFrom, ok := ctx.Outputs["flow_from"].(string); ok {
				from = flowFrom
			}
		}

		msg := &Message{
			From:    from,
			To:      []string{to},
			Subject: subject,
			Body:    body,
		}

		// Handle attachments
		if attRaw, ok := args["attachments"]; ok {
			if attList, ok := attRaw.([]interface{}); ok {
				for _, a := range attList {
					if am, ok := a.(map[string]interface{}); ok {
						att := Attachment{
							Filename: flow.StepArg(am, "filename"),
							MIMEType: flow.StepArg(am, "mime"),
						}
						if path := flow.StepArg(am, "path"); path != "" {
							att.Filename = path
						}
						// Direct data
						if data, ok := am["data"].([]byte); ok {
							att.Data = data
						}
						// Resolve from named output (e.g. from_output: pdf -> ctx.Outputs["pdf"]["data"])
						if outputName := flow.StepArg(am, "from_output"); outputName != "" {
							if output, ok := ctx.Outputs[outputName]; ok {
								if om, ok := output.(map[string]interface{}); ok {
									if data, ok := om["data"].([]byte); ok {
										att.Data = data
									}
									if att.Filename == "" {
										if fn, ok := om["filename"].(string); ok {
											att.Filename = fn
										}
									}
								}
							}
						}
						if len(att.Data) > 0 {
							msg.Attachments = append(msg.Attachments, att)
						}
					}
				}
			}
		}

		if ctx.DryRun {
			fmt.Printf("DRY RUN: would send email to %s: %s\n", to, subject)
			return nil, nil
		}

		return nil, Send(smtpCfg, msg)
	})
}
