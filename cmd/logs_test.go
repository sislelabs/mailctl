package cmd

import (
	"testing"

	"github.com/sislelabs/mailctl/internal/resend"
)

func TestFilterEmails(t *testing.T) {
	emails := []resend.EmailSummary{
		{From: "hello@sislelabs.com", LastEvent: "delivered"},
		{From: "Support <support@sislelabs.com>", LastEvent: "bounced"},
		{From: "noreply@other.com", LastEvent: "delivered"},
	}

	if got := filterEmails(emails, "", ""); len(got) != 3 {
		t.Errorf("no filters: got %d, want 3", len(got))
	}
	if got := filterEmails(emails, "sislelabs.com", ""); len(got) != 2 {
		t.Errorf("domain filter: got %d, want 2", len(got))
	}
	// The display-name form "Support <support@…>" must still match its domain.
	if got := filterEmails(emails, "SISLELABS.COM", ""); len(got) != 2 {
		t.Errorf("domain filter is case-sensitive: got %d, want 2", len(got))
	}
	if got := filterEmails(emails, "", "bounced"); len(got) != 1 {
		t.Errorf("status filter: got %d, want 1", len(got))
	}
	if got := filterEmails(emails, "sislelabs.com", "delivered"); len(got) != 1 {
		t.Errorf("both filters: got %d, want 1", len(got))
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("hello world", 8); got != "hello w…" {
		t.Errorf("truncate long = %q", got)
	}
}
