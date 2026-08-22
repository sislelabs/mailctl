package mailsetup

import "testing"

func TestLooksLikeDomain(t *testing.T) {
	valid := []string{
		"example.com",
		"sislelabs.com",
		"mail.example.co.uk",
		"xn--80ak6aa92e.com",
		"a.io",
	}
	for _, s := range valid {
		if !LooksLikeDomain(s) {
			t.Errorf("LooksLikeDomain(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"",
		"bozhidar",        // the case that cost a round-trip to Cloudflare
		"localhost",       // no dot
		"example.",        // trailing dot
		".example.com",    // leading dot
		"example..com",    // empty label
		"example.c",       // one-character TLD
		"example.c0m",     // digit in TLD
		"you@example.com", // an address, not a domain
		"https://example.com",
		"example com",
	}
	for _, s := range invalid {
		if LooksLikeDomain(s) {
			t.Errorf("LooksLikeDomain(%q) = true, want false", s)
		}
	}
}

func TestDMARCContent(t *testing.T) {
	// p=none is deliberate: a stricter starting policy can blackhole
	// legitimate mail before the aggregate reports prove alignment.
	got := DMARCContent("you@gmail.com")
	want := "v=DMARC1; p=none; rua=mailto:you@gmail.com"
	if got != want {
		t.Errorf("DMARCContent() = %q, want %q", got, want)
	}

	got = DMARCContent("")
	want = "v=DMARC1; p=none"
	if got != want {
		t.Errorf("DMARCContent(\"\") = %q, want %q", got, want)
	}
}

func TestDMARCName(t *testing.T) {
	if got := DMARCName("example.com"); got != "_dmarc.example.com" {
		t.Errorf("DMARCName() = %q", got)
	}
}

func TestResendRecordName(t *testing.T) {
	domain := "example.com"
	cases := []struct {
		in   string
		want string
	}{
		{"send", "send.example.com"},
		{"resend._domainkey", "resend._domainkey.example.com"},
		{"", "example.com"},
		{"@", "example.com"},
		{"example.com", "example.com"},
		{"links.example.com", "links.example.com"}, // already an FQDN
		{"send.example.com.", "send.example.com"},  // trailing dot trimmed
	}
	for _, c := range cases {
		if got := ResendRecordName(c.in, domain); got != c.want {
			t.Errorf("ResendRecordName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAddStepLabels(t *testing.T) {
	labels := AddStepLabels("Resend")
	if len(labels) != addStepCount {
		t.Fatalf("got %d labels, want %d", len(labels), addStepCount)
	}
	// Every index must be populated: a gap would render a blank row and, worse,
	// means an index constant has no label behind it.
	for i, l := range labels {
		if l == "" {
			t.Errorf("label %d is empty", i)
		}
	}
	if labels[AddStepProviderDomain] != "Add domain to Resend" {
		t.Errorf("provider label not substituted: %q", labels[AddStepProviderDomain])
	}
	if labels[AddStepCatchAll] != "Enable catch-all" {
		t.Errorf("catch-all step missing: %q", labels[AddStepCatchAll])
	}
}

func TestRemoveStepLabels(t *testing.T) {
	labels := RemoveStepLabels("Resend")
	if len(labels) != removeStepCount {
		t.Fatalf("got %d labels, want %d", len(labels), removeStepCount)
	}
	for i, l := range labels {
		if l == "" {
			t.Errorf("label %d is empty", i)
		}
	}
}
