package cloudflare

import (
	"errors"
	"fmt"
	"testing"
)

func TestAPIErrorMessage(t *testing.T) {
	// The wording is what users have been reading in CLI output and in issue
	// reports, so turning the error into a type must not reword it.
	withCode := &APIError{Code: 2054, Message: "Destination address is not verified", Status: 400}
	if got, want := withCode.Error(), "cloudflare API error: Destination address is not verified (code 2054)"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	noCode := &APIError{Status: 502}
	if got, want := noCode.Error(), "cloudflare API error (status 502)"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestIsDestinationUnverified(t *testing.T) {
	unverified := &APIError{Code: CodeDestinationNotVerified, Message: "Destination address is not verified"}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"the 2054 error itself", unverified, true},
		{"wrapped, because callers add context before returning", fmt.Errorf("create routing rule: %w", unverified), true},
		{"wrapped twice", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", unverified)), true},
		{"a different Cloudflare failure", &APIError{Code: 10000, Message: "Authentication error"}, false},
		{"an error with no code at all", &APIError{Status: 500}, false},
		{"a plain error that happens to mention 2054", errors.New("code 2054"), false},
		{"nil", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsDestinationUnverified(tc.err); got != tc.want {
				t.Errorf("IsDestinationUnverified(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
