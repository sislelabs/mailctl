package mailsetup

import (
	"strings"
	"testing"

	"github.com/sislelabs/mailctl/internal"
)

func TestGmailSendAsRejectsSendingDomains(t *testing.T) {
	cfg := &internal.Config{Provider: internal.ProviderResend, ResendAPIKey: "re_x"}
	cfg.AddSendingDomain("info.example.com", "zone1", "example.com")

	_, err := GmailSendAsFor(cfg, cfg.FindDomain("info.example.com"))
	if err == nil {
		t.Fatal("expected an error for a sending domain")
	}
	if !strings.Contains(err.Error(), "cannot receive") {
		t.Errorf("error should explain why: %v", err)
	}
}

func TestGmailSendAsRequiresCredentials(t *testing.T) {
	cfg := &internal.Config{Provider: internal.ProviderResend}
	cfg.AddDomain("example.com", "zone1", nil)

	if _, err := GmailSendAsFor(cfg, cfg.FindDomain("example.com")); err == nil {
		t.Error("expected an error when no API key is configured")
	}
}
