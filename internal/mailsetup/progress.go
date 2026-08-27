package mailsetup

// StepStatus is the state of one step in a multi-step operation.
//
// The values are fixed rather than iota-from-zero because a zero value has to
// mean "not started yet" for frontends that pre-render the whole step list.
type StepStatus int

const (
	// StepPending is the zero value: the step has not been reached.
	StepPending StepStatus = 0
	// StepRunning means the step is in progress.
	StepRunning StepStatus = 1
	// StepDone means the step completed.
	StepDone StepStatus = 2
	// StepWarn means the step did not complete but the operation continues.
	StepWarn StepStatus = 3
	// StepFailed means the step failed and the operation stopped.
	StepFailed StepStatus = 4
)

// NoteLevel classifies a detail line emitted underneath a step.
type NoteLevel int

const (
	NoteOK NoteLevel = iota
	NoteWarn
	NoteError
)

// Reporter receives progress from a long-running operation.
//
// Implementations must not assume they are on any particular goroutine and
// must not block: the operation calls straight through while holding no locks.
// Notes carry a level rather than pre-rendered text so that the operation
// stays free of any presentation concern — a terminal maps levels to icons, a
// web frontend to CSS classes.
type Reporter interface {
	// Step reports a status change for the step at index, with an optional
	// short detail shown alongside the label.
	Step(index int, status StepStatus, detail string)
	// Note reports a detail line underneath the step at index.
	Note(index int, level NoteLevel, text string)
}

// NopReporter discards progress. Use it for callers that only care about the
// final outcome.
type NopReporter struct{}

func (NopReporter) Step(int, StepStatus, string) {}
func (NopReporter) Note(int, NoteLevel, string)  {}

// FuncReporter adapts a pair of closures to Reporter. Either may be nil.
type FuncReporter struct {
	OnStep func(index int, status StepStatus, detail string)
	OnNote func(index int, level NoteLevel, text string)
}

func (f FuncReporter) Step(index int, status StepStatus, detail string) {
	if f.OnStep != nil {
		f.OnStep(index, status, detail)
	}
}

func (f FuncReporter) Note(index int, level NoteLevel, text string) {
	if f.OnNote != nil {
		f.OnNote(index, level, text)
	}
}
