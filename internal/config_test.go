package internal

import "testing"

func TestSendingProviderDefaultsToResend(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "empty config defaults to resend",
			cfg:  Config{},
			want: ProviderResend,
		},
		{
			name: "explicit resend",
			cfg:  Config{Provider: ProviderResend, BrevoAPIKey: "xkeysib-x"},
			want: ProviderResend,
		},
		{
			name: "explicit brevo wins over a resend key",
			cfg:  Config{Provider: ProviderBrevo, ResendAPIKey: "re_x"},
			want: ProviderBrevo,
		},
		{
			name: "legacy brevo config without a provider stays on brevo",
			cfg:  Config{BrevoAPIKey: "xkeysib-x"},
			want: ProviderBrevo,
		},
		{
			name: "legacy config with only smtp credentials stays on brevo",
			cfg:  Config{BrevoSMTPKey: "xsmtpsib-x", BrevoSMTPLogin: "x@smtp-brevo.com"},
			want: ProviderBrevo,
		},
		{
			name: "brevo credentials alongside a resend key defaults to resend",
			cfg:  Config{BrevoAPIKey: "xkeysib-x", ResendAPIKey: "re_x"},
			want: ProviderResend,
		},
		{
			name: "unrecognized provider falls back to the default",
			cfg:  Config{Provider: "mailgun"},
			want: ProviderResend,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.SendingProvider(); got != c.want {
				t.Errorf("SendingProvider() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestApplySMTPDefaults(t *testing.T) {
	// Resend: only default_from matters — flows send over the HTTP API.
	cfg := &Config{Provider: ProviderResend, ResendAPIKey: "re_x"}
	cfg.ApplySMTPDefaults("hello@example.com")
	if cfg.SMTP == nil || cfg.SMTP.DefaultFrom != "hello@example.com" {
		t.Fatalf("resend default_from not set: %+v", cfg.SMTP)
	}
	if cfg.SMTP.Host != "" {
		t.Errorf("resend should not get a relay host, got %q", cfg.SMTP.Host)
	}

	// Brevo: the relay details are derivable from credentials already collected.
	cfg = &Config{
		Provider:       ProviderBrevo,
		BrevoSMTPLogin: "abc@smtp-brevo.com",
		BrevoSMTPKey:   "xsmtpsib-x",
	}
	cfg.ApplySMTPDefaults("hello@example.com")
	if cfg.SMTP.Host != "smtp-relay.brevo.com" || cfg.SMTP.Port != 587 {
		t.Errorf("brevo relay not filled in: %+v", cfg.SMTP)
	}
	if cfg.SMTP.User != "abc@smtp-brevo.com" || cfg.SMTP.Pass != "xsmtpsib-x" {
		t.Errorf("brevo credentials not carried over: %+v", cfg.SMTP)
	}

	// An existing block is never overwritten with derived values.
	cfg = &Config{
		Provider:       ProviderBrevo,
		BrevoSMTPLogin: "abc@smtp-brevo.com",
		BrevoSMTPKey:   "xsmtpsib-x",
		SMTP:           &SMTPConfig{Host: "custom.relay", Port: 2525},
	}
	cfg.ApplySMTPDefaults("")
	if cfg.SMTP.Host != "custom.relay" || cfg.SMTP.Port != 2525 {
		t.Errorf("existing smtp block was overwritten: %+v", cfg.SMTP)
	}

	// Nothing to do, nothing created.
	cfg = &Config{Provider: ProviderResend}
	cfg.ApplySMTPDefaults("")
	if cfg.SMTP != nil {
		t.Errorf("empty input created an smtp block: %+v", cfg.SMTP)
	}
}

func TestDomainKindDefaultsToMailbox(t *testing.T) {
	// Domains written before sending domains existed carry no kind, and must
	// keep behaving exactly as they did.
	legacy := DomainConfig{Domain: "example.com"}
	if legacy.DomainKind() != KindMailbox || legacy.IsSending() {
		t.Errorf("legacy entry = %q", legacy.DomainKind())
	}

	sending := DomainConfig{Domain: "info.example.com", Kind: KindSending}
	if !sending.IsSending() {
		t.Error("explicit sending kind not honoured")
	}

	// An unrecognised value falls back to the safe default rather than
	// silently disabling routing teardown.
	odd := DomainConfig{Domain: "example.com", Kind: "whatever"}
	if odd.DomainKind() != KindMailbox {
		t.Errorf("unknown kind = %q, want mailbox", odd.DomainKind())
	}
}

func TestSubdomainAndZoneName(t *testing.T) {
	apex := DomainConfig{Domain: "example.com"}
	if apex.IsSubdomain() || apex.ZoneName() != "example.com" {
		t.Errorf("apex: sub=%v zone=%q", apex.IsSubdomain(), apex.ZoneName())
	}

	sub := DomainConfig{Domain: "info.example.com", ZoneDomain: "example.com"}
	if !sub.IsSubdomain() || sub.ZoneName() != "example.com" {
		t.Errorf("sub: sub=%v zone=%q", sub.IsSubdomain(), sub.ZoneName())
	}

	// A zone_domain equal to the domain is not a subdomain.
	same := DomainConfig{Domain: "example.com", ZoneDomain: "example.com"}
	if same.IsSubdomain() {
		t.Error("equal zone_domain should not read as a subdomain")
	}
}

func TestAddSendingDomain(t *testing.T) {
	cfg := &Config{}
	cfg.AddSendingDomain("info.example.com", "zone1", "example.com")
	cfg.AddSendingDomain("other.com", "zone2", "other.com")

	sub := cfg.FindDomain("info.example.com")
	if sub == nil || !sub.IsSending() || !sub.IsSubdomain() {
		t.Fatalf("subdomain entry wrong: %+v", sub)
	}

	// When the domain is the zone apex, zone_domain is redundant and omitted.
	apex := cfg.FindDomain("other.com")
	if apex == nil || apex.ZoneDomain != "" || apex.IsSubdomain() {
		t.Fatalf("apex entry wrong: %+v", apex)
	}
}
