package mailsetup

import "testing"

func TestFuncReporterNilFuncs(t *testing.T) {
	// A partially-populated reporter must not panic: frontends that only care
	// about steps should be able to leave OnNote nil.
	r := FuncReporter{}
	r.Step(0, StepDone, "detail")
	r.Note(0, NoteOK, "text")
}

func TestFuncReporterForwards(t *testing.T) {
	var steps []StepStatus
	var notes []NoteLevel

	r := FuncReporter{
		OnStep: func(_ int, s StepStatus, _ string) { steps = append(steps, s) },
		OnNote: func(_ int, l NoteLevel, _ string) { notes = append(notes, l) },
	}
	r.Step(1, StepRunning, "")
	r.Step(1, StepWarn, "careful")
	r.Note(1, NoteError, "bad")

	if len(steps) != 2 || steps[1] != StepWarn {
		t.Errorf("steps = %v", steps)
	}
	if len(notes) != 1 || notes[0] != NoteError {
		t.Errorf("notes = %v", notes)
	}
}

func TestStepPendingIsZero(t *testing.T) {
	// Frontends pre-render the full step list, so the zero value has to mean
	// "not started" rather than "running".
	var s StepStatus
	if s != StepPending {
		t.Errorf("zero StepStatus = %v, want StepPending", s)
	}
}

var _ Reporter = NopReporter{}
var _ Reporter = FuncReporter{}
