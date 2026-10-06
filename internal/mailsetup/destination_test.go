package mailsetup

import (
	"strings"
	"testing"
)

func TestDestinationStatePredicates(t *testing.T) {
	// Unknown and pending are both "will not deliver", but they need different
	// handling: one has never been asked, the other is waiting on a person.
	cases := []struct {
		state              DestinationState
		verified, register bool
	}{
		{DestinationUnknown, false, false},
		{DestinationPending, false, true},
		{DestinationVerified, true, true},
	}

	for _, tc := range cases {
		status := DestinationStatus{Email: "them@corp.example", State: tc.state}
		if got := status.Verified(); got != tc.verified {
			t.Errorf("state %d: Verified() = %v, want %v", tc.state, got, tc.verified)
		}
		if got := status.Registered(); got != tc.register {
			t.Errorf("state %d: Registered() = %v, want %v", tc.state, got, tc.register)
		}
	}
}

func TestDestinationPendingErrorDistinguishesAFreshAsk(t *testing.T) {
	// Whether Cloudflare has only just emailed them changes what the operator
	// should do next: wait, or chase a mail that was sent some time ago.
	fresh := (&DestinationPendingError{Email: "them@corp.example", JustRegistered: true}).Error()
	if !strings.Contains(fresh, "has sent it a confirmation email") {
		t.Errorf("fresh registration should say the mail just went out, got: %s", fresh)
	}

	stale := (&DestinationPendingError{Email: "them@corp.example"}).Error()
	if !strings.Contains(stale, "registered but not confirmed") {
		t.Errorf("existing registration should say it is still unconfirmed, got: %s", stale)
	}

	for _, msg := range []string{fresh, stale} {
		if !strings.Contains(msg, "them@corp.example") {
			t.Errorf("message must name the address that has to act, got: %s", msg)
		}
	}
}

func TestRequestVerificationRejectsNonAddresses(t *testing.T) {
	// Guard before any API call: a stray alias name here would otherwise
	// register nonsense with the account and mail nobody.
	st := &stubStore{}
	if _, err := st.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if _, err := RequestVerification(st, "example.com", "billing"); err == nil {
		t.Fatal("expected an error for an address with no @")
	}
}

func TestRequestVerificationNeedsAKnownDomain(t *testing.T) {
	st := &stubStore{}
	if _, err := RequestVerification(st, "unknown.example", "them@corp.example"); err == nil {
		t.Fatal("expected an error for a domain that is not in config")
	}
}
