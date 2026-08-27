package mailsetup

import (
	"strings"
	"testing"

	"github.com/sislelabs/mailctl/internal"
)

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

func TestRemoveStepLabelsByKind(t *testing.T) {
	mailbox := &internal.DomainConfig{Domain: "example.com"}
	if got := RemoveStepLabels(mailbox, "Resend"); len(got) != 5 {
		t.Fatalf("mailbox: got %d labels, want 5: %v", len(got), got)
	}

	// A sending domain has no routing to undo, and its catch-all belongs to a
	// zone that serves other mail. Those steps must not exist for it at all.
	sending := &internal.DomainConfig{
		Domain:     "info.example.com",
		Kind:       internal.KindSending,
		ZoneDomain: "example.com",
	}
	labels := RemoveStepLabels(sending, "Resend")
	if len(labels) != 3 {
		t.Fatalf("sending: got %d labels, want 3: %v", len(labels), labels)
	}
	for _, l := range labels {
		lower := strings.ToLower(l)
		if strings.Contains(lower, "routing") || strings.Contains(lower, "catch-all") {
			t.Errorf("sending teardown must not include %q", l)
		}
		if l == "" {
			t.Error("empty label")
		}
	}
}

func TestPlanRemoveIndexes(t *testing.T) {
	sending := &internal.DomainConfig{Domain: "info.example.com", Kind: internal.KindSending}
	plan := planRemove(sending, "Resend")
	if plan.rules != stepAbsent || plan.catchAll != stepAbsent {
		t.Errorf("sending plan should mark routing steps absent: %+v", plan)
	}
	if plan.provider != 0 || plan.dns != 1 || plan.save != 2 {
		t.Errorf("sending plan indexes wrong: %+v", plan)
	}

	mailbox := &internal.DomainConfig{Domain: "example.com"}
	plan = planRemove(mailbox, "Resend")
	if plan.rules != 0 || plan.catchAll != 1 || plan.provider != 2 || plan.dns != 3 || plan.save != 4 {
		t.Errorf("mailbox plan indexes wrong: %+v", plan)
	}
}

func TestSameRecordContent(t *testing.T) {
	// Cloudflare returns TXT values quoted; providers hand them over bare.
	// Treating that as a difference reported an identical SPF record as a
	// conflict and skipped publishing it.
	same := [][2]string{
		{`"v=spf1 include:amazonses.com ~all"`, "v=spf1 include:amazonses.com ~all"},
		{"v=spf1 include:amazonses.com ~all", "v=spf1 include:amazonses.com ~all"},
		{` "p=abc" `, "p=abc"},
	}
	for _, c := range same {
		if !sameRecordContent(c[0], c[1]) {
			t.Errorf("sameRecordContent(%q, %q) = false, want true", c[0], c[1])
		}
	}

	different := [][2]string{
		{"feedback-smtp.eu-west-1.amazonses.com", "feedback-smtp.us-east-1.amazonses.com"},
		{`"p=oldkey"`, "p=newkey"},
	}
	for _, c := range different {
		if sameRecordContent(c[0], c[1]) {
			t.Errorf("sameRecordContent(%q, %q) = true, want false", c[0], c[1])
		}
	}
}

func TestSameAddresses(t *testing.T) {
	same := [][2][]string{
		{{"a@x.com"}, {"a@x.com"}},
		{{"A@X.com"}, {"a@x.com"}},
		{{" a@x.com "}, {"a@x.com"}},
		{{"a@x.com", "b@x.com"}, {"b@x.com", "a@x.com"}},
		{{}, {}},
	}
	for _, c := range same {
		if !sameAddresses(c[0], c[1]) {
			t.Errorf("sameAddresses(%v, %v) = false, want true", c[0], c[1])
		}
	}

	different := [][2][]string{
		{{"a@x.com"}, {"b@x.com"}},
		{{"a@x.com"}, {}},
		{{"a@x.com"}, {"a@x.com", "b@x.com"}},
	}
	for _, c := range different {
		if sameAddresses(c[0], c[1]) {
			t.Errorf("sameAddresses(%v, %v) = true, want false", c[0], c[1])
		}
	}
}

func TestRegisterStepLabelsCoverEveryIndex(t *testing.T) {
	labels := RegisterStepLabels("Resend")
	if len(labels) != registerStepCount {
		t.Fatalf("got %d labels, want %d", len(labels), registerStepCount)
	}
	for i, l := range labels {
		if l == "" {
			t.Errorf("label %d is empty", i)
		}
	}
	// register must never show routing steps: a sending domain has none, and
	// showing them would imply teardown touches routing.
	for _, l := range labels {
		for _, forbidden := range []string{"routing", "catch-all", "Enable email"} {
			if l == forbidden {
				t.Errorf("register should not have a %q step", forbidden)
			}
		}
	}
}

func TestResendRecordNameUsesTheZoneApex(t *testing.T) {
	// Resend returns names relative to the registrable domain, so a sending
	// subdomain gets back "resend._domainkey.info". Resolving that against the
	// sending domain instead of the zone apex doubles the label and produces
	// resend._domainkey.info.info.getsaiton.com.
	got := ResendRecordName("resend._domainkey.info", "getsaiton.com")
	if got != "resend._domainkey.info.getsaiton.com" {
		t.Errorf("got %q", got)
	}
	if got := ResendRecordName("send.info", "getsaiton.com"); got != "send.info.getsaiton.com" {
		t.Errorf("got %q", got)
	}
	// Resolving against the sending domain is the bug this guards.
	if bad := ResendRecordName("resend._domainkey.info", "info.getsaiton.com"); bad != "resend._domainkey.info.info.getsaiton.com" {
		t.Errorf("expected the doubled form from the wrong base, got %q", bad)
	}
}
