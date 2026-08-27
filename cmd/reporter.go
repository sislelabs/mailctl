package cmd

import (
	"github.com/sislelabs/mailctl/internal/mailsetup"
	"github.com/sislelabs/mailctl/internal/ui"
)

// progressReporter adapts mailsetup's transport-agnostic progress onto the
// terminal progress display. mailsetup emits note levels rather than styled
// text, so all icon and colour choices live here.
func progressReporter(p *ui.ProgressRunner) mailsetup.Reporter {
	return mailsetup.FuncReporter{
		OnStep: func(index int, status mailsetup.StepStatus, detail string) {
			switch status {
			case mailsetup.StepRunning:
				p.Start(index)
			case mailsetup.StepDone:
				p.Done(index, ui.Dim.Render(detail))
			case mailsetup.StepWarn:
				p.Warn(index, detail)
			case mailsetup.StepFailed:
				p.Fail(index, detail)
			}
		},
		OnNote: func(index int, level mailsetup.NoteLevel, text string) {
			p.SubRow(index, noteRow(level, text))
		},
	}
}

func noteRow(level mailsetup.NoteLevel, text string) string {
	switch level {
	case mailsetup.NoteOK:
		return ui.IconSuccess + " " + ui.Dim.Render(text)
	case mailsetup.NoteWarn:
		return ui.IconWarn + " " + ui.Dim.Render(text)
	default:
		return ui.IconError + " " + ui.Error.Render(text)
	}
}
