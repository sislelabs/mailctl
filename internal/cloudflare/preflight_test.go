package cloudflare

import "testing"

func TestPreflightHelpers(t *testing.T) {
	results := []CheckResult{
		{Name: "a", OK: true},
		{Name: "b", OK: false, Fatal: false},
		{Name: "c", OK: false, Fatal: true},
	}
	if got := len(PreflightFailures(results)); got != 2 {
		t.Errorf("PreflightFailures = %d, want 2", got)
	}
	if !HasFatalFailure(results) {
		t.Error("a fatal failure should be reported")
	}

	// A non-fatal failure alone must not read as fatal: an account-ID mismatch
	// degrades destination lookups without stopping a domain being set up.
	nonFatal := []CheckResult{{Name: "b", OK: false, Fatal: false}}
	if HasFatalFailure(nonFatal) {
		t.Error("a non-fatal failure should not be fatal")
	}
	if len(PreflightFailures(nil)) != 0 {
		t.Error("no results means no failures")
	}
}
