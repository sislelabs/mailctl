package web

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sislelabs/mailctl/internal/mailsetup"
)

func TestJobRecordsStepsAndNotes(t *testing.T) {
	job := newJob("add", "example.com", []string{"one", "two"})

	job.Step(0, mailsetup.StepRunning, "")
	job.Step(0, mailsetup.StepDone, "zone1")
	job.Note(0, mailsetup.NoteOK, "a note")
	job.Note(1, mailsetup.NoteWarn, "careful")

	// Out-of-range indexes must be ignored rather than panic: the step list is
	// built from labels, and a mismatch would take down the whole server.
	job.Step(99, mailsetup.StepDone, "nope")
	job.Note(-1, mailsetup.NoteOK, "nope")

	steps, done, errMsg := job.Snapshot()
	if done || errMsg != "" {
		t.Errorf("job should not be finished yet: done=%v err=%q", done, errMsg)
	}
	if steps[0].Status != mailsetup.StepDone || steps[0].Detail != "zone1" {
		t.Errorf("step 0 = %+v", steps[0])
	}
	if len(steps[0].Notes) != 1 || len(steps[1].Notes) != 1 {
		t.Errorf("notes not recorded: %+v", steps)
	}
}

func TestJobSnapshotIsACopy(t *testing.T) {
	job := newJob("add", "example.com", []string{"one"})
	job.Note(0, mailsetup.NoteOK, "first")

	steps, _, _ := job.Snapshot()
	steps[0].Detail = "mutated"
	steps[0].Notes = append(steps[0].Notes, JobNote{Text: "injected"})

	// A template rendering a snapshot while the job runs must not be able to
	// corrupt the job's own state.
	again, _, _ := job.Snapshot()
	if again[0].Detail == "mutated" || len(again[0].Notes) != 1 {
		t.Errorf("snapshot shares memory with the job: %+v", again[0])
	}
}

func TestJobFinishRecordsError(t *testing.T) {
	job := newJob("remove", "example.com", []string{"one"})
	job.finish(errors.New("boom"))

	_, done, errMsg := job.Snapshot()
	if !done || errMsg != "boom" {
		t.Errorf("done=%v err=%q", done, errMsg)
	}
}

func TestJobsRunOneAtATime(t *testing.T) {
	s := newJobs()

	release := make(chan struct{})
	first := newJob("add", "a.com", []string{"one"})
	if !s.start(first, func() error { <-release; return nil }) {
		t.Fatal("first job should start")
	}

	// Two domain operations would race on the config file and on the same
	// zone, so the second must be refused rather than queued silently.
	second := newJob("add", "b.com", []string{"one"})
	if s.start(second, func() error { return nil }) {
		t.Error("second job should be refused while one is running")
	}

	close(release)
	waitUntil(t, func() bool { _, done, _ := first.Snapshot(); return done })

	third := newJob("add", "c.com", []string{"one"})
	if !s.start(third, func() error { return nil }) {
		t.Error("a job should start once the previous one finished")
	}
}

func TestJobsConcurrentReporting(t *testing.T) {
	// The work goroutine writes while the HTTP handler reads; the race
	// detector covers this when tests run with -race.
	job := newJob("add", "example.com", []string{"one", "two", "three"})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			job.Step(i%3, mailsetup.StepDone, "d")
			job.Note(i%3, mailsetup.NoteOK, "n")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			job.Snapshot()
		}
	}()
	wg.Wait()
}

func TestRandomIDIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := randomID()
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
