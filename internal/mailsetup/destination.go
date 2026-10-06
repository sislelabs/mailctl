package mailsetup

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/store"
)

// DestinationState is how far a forwarding target has got through Cloudflare's
// confirmation, which is the gate on whether anything can route to it.
type DestinationState int

const (
	// DestinationUnknown means the account has never heard of the address.
	DestinationUnknown DestinationState = iota
	// DestinationPending means the address is registered but its owner has not
	// clicked Cloudflare's confirmation link. Cloudflare refuses to create a
	// rule pointing at it and would not deliver there if one existed.
	DestinationPending
	// DestinationVerified means Cloudflare will deliver to the address.
	DestinationVerified
)

// DestinationStatus is one forwarding target as Cloudflare sees it.
//
// The address does not have to belong to the account holder, or to anyone in
// their organisation — it is any mailbox whose owner confirmed it once. That
// confirmation is the only gate, and no API clicks it on their behalf.
type DestinationStatus struct {
	// Email is the address itself.
	Email string
	// State is how far it has got through confirmation.
	State DestinationState
	// ID is Cloudflare's identifier, empty when the address is unknown.
	ID string
}

// Verified reports whether Cloudflare will deliver to this address.
func (s DestinationStatus) Verified() bool { return s.State == DestinationVerified }

// Registered reports whether the account knows the address at all.
func (s DestinationStatus) Registered() bool { return s.State != DestinationUnknown }

// DestinationPendingError reports that an operation could not complete because
// its forwarding target is still waiting on confirmation from whoever owns it.
//
// This is a distinct failure from a rejected token or a bad address: nothing is
// wrong with the request, and re-running it after the owner clicks will work.
type DestinationPendingError struct {
	// Email is the address whose owner has to click Cloudflare's link.
	Email string
	// JustRegistered is true when this call registered the address, meaning
	// Cloudflare has only now sent the confirmation email.
	JustRegistered bool
}

func (e *DestinationPendingError) Error() string {
	if e.JustRegistered {
		return fmt.Sprintf("%s is not a confirmed destination — Cloudflare has sent it a confirmation email, "+
			"and mail will route once its owner clicks the link", e.Email)
	}
	return fmt.Sprintf("%s is registered but not confirmed — mail will route once its owner clicks "+
		"the link Cloudflare sent them", e.Email)
}

// AccountFor resolves the Cloudflare account holding a domain's zone.
//
// The account comes from the zone rather than from config: destination
// addresses are verified per account, and asking the wrong one reports every
// address as unregistered while quietly creating it somewhere it will never be
// used. Config is the fallback for when the zone read fails.
func AccountFor(cf *cloudflare.Client, cfg *internal.Config, d *internal.DomainConfig) string {
	if zone, err := cf.GetZoneByName(d.ZoneName()); err == nil {
		return zone.Account.ID
	}
	return cfg.CloudflareAccountID
}

// LookupDestination reports how the account sees email as a forwarding target.
func LookupDestination(cf *cloudflare.Client, accountID, email string) (DestinationStatus, error) {
	status := DestinationStatus{Email: email, State: DestinationUnknown}

	addrs, err := cf.ListDestinationAddresses(accountID)
	if err != nil {
		return status, fmt.Errorf("list destination addresses: %w", err)
	}
	for _, a := range addrs {
		if !strings.EqualFold(a.Email, email) {
			continue
		}
		status.ID = a.ID
		if a.Verified != "" {
			status.State = DestinationVerified
		} else {
			status.State = DestinationPending
		}
		break
	}
	return status, nil
}

// UnverifiedDestinations returns every address the account has registered that
// nobody has confirmed yet.
//
// These are account-wide rather than per-domain because that is how Cloudflare
// scopes them: one confirmation covers every zone in the account.
func UnverifiedDestinations(cf *cloudflare.Client, accountID string) ([]DestinationStatus, error) {
	addrs, err := cf.ListDestinationAddresses(accountID)
	if err != nil {
		return nil, fmt.Errorf("list destination addresses: %w", err)
	}

	var pending []DestinationStatus
	for _, a := range addrs {
		if a.Verified == "" {
			pending = append(pending, DestinationStatus{Email: a.Email, State: DestinationPending, ID: a.ID})
		}
	}
	return pending, nil
}

// EnsureDestination registers email with the account if it is not known, and
// reports where confirmation stands afterwards. The second return is true when
// this call did the registering, meaning Cloudflare has only now sent its
// confirmation email.
//
// Registering is what triggers that email, so this is both the check and the
// ask. It never returns an error for an unconfirmed address: that is a state to
// report, not a failure.
func EnsureDestination(cf *cloudflare.Client, accountID, email string) (DestinationStatus, bool, error) {
	status, err := LookupDestination(cf, accountID, email)
	if err != nil {
		return status, false, err
	}
	if status.Registered() {
		return status, false, nil
	}

	if err := cf.CreateDestinationAddress(accountID, email); err != nil {
		return status, false, fmt.Errorf("register %s as a destination: %w", email, err)
	}
	// Cloudflare has just mailed the owner; nothing routes until they click.
	return DestinationStatus{Email: email, State: DestinationPending}, true, nil
}

// RequestVerification asks Cloudflare to send its confirmation email for a
// forwarding address, whether or not the address is already registered.
//
// Cloudflare has no resend endpoint, so re-asking for an address it already
// holds means deleting and recreating it. That is safe only while the address
// is unconfirmed — a confirmed one is left alone, since deleting it would throw
// away a confirmation that only its owner can give back.
func RequestVerification(st store.Store, domain, email string) (DestinationStatus, error) {
	email = strings.TrimSpace(email)
	status := DestinationStatus{Email: email, State: DestinationUnknown}
	if !strings.Contains(email, "@") {
		return status, fmt.Errorf("%q is not an email address", email)
	}

	cfg, err := st.Load()
	if err != nil {
		return status, err
	}
	d := cfg.FindDomain(domain)
	if d == nil {
		return status, fmt.Errorf("domain %s not found in config", domain)
	}

	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	accountID := AccountFor(cf, cfg, d)

	status, err = LookupDestination(cf, accountID, email)
	if err != nil {
		return status, err
	}
	if status.Verified() {
		return status, nil
	}

	if status.Registered() {
		// Drop the unconfirmed registration so creating it again re-sends.
		if err := cf.DeleteDestinationAddress(accountID, status.ID); err != nil {
			return status, fmt.Errorf("clear the unconfirmed registration for %s: %w", email, err)
		}
	}
	if err := cf.CreateDestinationAddress(accountID, email); err != nil {
		return DestinationStatus{Email: email, State: DestinationUnknown},
			fmt.Errorf("register %s as a destination: %w", email, err)
	}
	return DestinationStatus{Email: email, State: DestinationPending}, nil
}

// DestinationsFor reports confirmation state for a set of forwarding addresses
// in one account read, keyed by lowercased address.
//
// Callers rendering a list of aliases need this per row; doing it with one call
// each would be a Cloudflare request per alias for data that arrives whole.
func DestinationsFor(cf *cloudflare.Client, accountID string, emails []string) (map[string]DestinationStatus, error) {
	addrs, err := cf.ListDestinationAddresses(accountID)
	if err != nil {
		return nil, fmt.Errorf("list destination addresses: %w", err)
	}

	known := map[string]DestinationStatus{}
	for _, a := range addrs {
		state := DestinationPending
		if a.Verified != "" {
			state = DestinationVerified
		}
		known[strings.ToLower(a.Email)] = DestinationStatus{Email: a.Email, State: state, ID: a.ID}
	}

	out := make(map[string]DestinationStatus, len(emails))
	for _, e := range emails {
		key := strings.ToLower(strings.TrimSpace(e))
		if key == "" {
			continue
		}
		if status, ok := known[key]; ok {
			out[key] = status
			continue
		}
		out[key] = DestinationStatus{Email: e, State: DestinationUnknown}
	}
	return out, nil
}
