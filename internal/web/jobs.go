package web

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/sislelabs/mailctl/internal/mailsetup"
)

// JobStep is one step of a running operation, as the browser sees it.
type JobStep struct {
	Label  string
	Status mailsetup.StepStatus
	Detail string
	Notes  []JobNote
}

// JobNote is a detail line underneath a step.
type JobNote struct {
	Level mailsetup.NoteLevel
	Text  string
}

// Job is a long-running domain operation.
//
// Setting up a domain is ten sequential API calls over several seconds, which
// does not fit a request/response cycle: the browser would sit on a blank
// connection with no idea whether anything was happening, and a dropped
// connection would leave the work half done with nobody watching. The work runs
// detached and the page reads this record.
type Job struct {
	ID     string
	Kind   string
	Domain string

	mu    sync.Mutex
	steps []JobStep
	done  bool
	err   string
}

func newJob(kind, domain string, labels []string) *Job {
	steps := make([]JobStep, len(labels))
	for i, l := range labels {
		steps[i] = JobStep{Label: l}
	}
	return &Job{ID: randomID(), Kind: kind, Domain: domain, steps: steps}
}

// Step records a status change. Job satisfies mailsetup.Reporter.
func (j *Job) Step(index int, status mailsetup.StepStatus, detail string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if index < 0 || index >= len(j.steps) {
		return
	}
	j.steps[index].Status = status
	if detail != "" {
		j.steps[index].Detail = detail
	}
}

// Note records a detail line under a step.
func (j *Job) Note(index int, level mailsetup.NoteLevel, text string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if index < 0 || index >= len(j.steps) {
		return
	}
	j.steps[index].Notes = append(j.steps[index].Notes, JobNote{Level: level, Text: text})
}

func (j *Job) finish(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.done = true
	if err != nil {
		j.err = err.Error()
	}
}

// Snapshot returns a copy safe to render while the job is still running.
func (j *Job) Snapshot() (steps []JobStep, done bool, errMsg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	steps = make([]JobStep, len(j.steps))
	copy(steps, j.steps)
	for i := range steps {
		steps[i].Notes = append([]JobNote(nil), j.steps[i].Notes...)
	}
	return steps, j.done, j.err
}

// jobs holds running and recently finished operations.
type jobs struct {
	mu sync.Mutex
	m  map[string]*Job
	// running serialises domain operations. Two of them would race on the
	// config file, and each already talks to the same two APIs about the same
	// zone, so there is nothing to gain from overlapping them.
	running bool
}

func newJobs() *jobs {
	return &jobs{m: map[string]*Job{}}
}

func (s *jobs) get(id string) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[id]
}

// start registers a job and runs fn detached. It returns false when another
// operation is already in flight.
func (s *jobs) start(job *Job, fn func() error) bool {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return false
	}
	s.running = true
	s.m[job.ID] = job
	s.reapLocked()
	s.mu.Unlock()

	go func() {
		err := fn()
		job.finish(err)
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()
	return true
}

// reapLocked drops finished jobs once the page that was watching them is long
// gone, so a long-lived server does not accumulate them.
func (s *jobs) reapLocked() {
	if len(s.m) < 32 {
		return
	}
	for id, j := range s.m {
		if _, done, _ := j.Snapshot(); done {
			delete(s.m, id)
		}
	}
}

func randomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// A collision only mixes up two progress views on one machine, so a
		// clock fallback is enough.
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000")))
	}
	return hex.EncodeToString(b)
}
