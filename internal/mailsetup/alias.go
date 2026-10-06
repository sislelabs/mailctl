package mailsetup

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/store"
)

// AddAlias creates a forwarding rule for alias@domain and records it in config.
// forwardTo may be empty, in which case the domain's existing default is used.
func AddAlias(st store.Store, domain, alias, forwardTo string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return fmt.Errorf("alias cannot be empty")
	}
	if strings.ContainsAny(alias, "@ \t") {
		return fmt.Errorf("%q is not a valid alias — pass the part before the @, e.g. hello", alias)
	}

	cfg, err := st.Load()
	if err != nil {
		return err
	}
	d := cfg.FindDomain(domain)
	if d == nil {
		return fmt.Errorf("domain %s not found in config", domain)
	}
	if d.FindAlias(alias) != nil {
		return fmt.Errorf("alias %s@%s already exists", alias, domain)
	}

	if forwardTo == "" {
		forwardTo = defaultForwardFor(cfg, d)
	}
	if forwardTo == "" {
		return fmt.Errorf("no forward-to address for %s", domain)
	}

	addr := fmt.Sprintf("%s@%s", alias, domain)
	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)

	// Cloudflare refuses a rule whose target nobody has confirmed, so register
	// the target first — that is what sends the confirmation email — and stop
	// here if it is still unconfirmed. Creating the rule anyway would fail with
	// a bare API code that says nothing about whose inbox the work sits in.
	accountID := AccountFor(cf, cfg, d)
	dest, justRegistered, err := EnsureDestination(cf, accountID, forwardTo)
	if err != nil {
		return err
	}
	if !dest.Verified() {
		return &DestinationPendingError{Email: forwardTo, JustRegistered: justRegistered}
	}

	rule := cloudflare.RoutingRule{
		Name:     "Forward " + addr,
		Enabled:  true,
		Matchers: []cloudflare.RuleMatcher{{Type: "literal", Field: "to", Value: addr}},
		Actions:  []cloudflare.RuleAction{{Type: "forward", Value: []string{forwardTo}}},
	}
	if err := cf.CreateRoutingRule(d.CloudflareZoneID, rule); err != nil {
		// A confirmation can be revoked between the read above and this write.
		// Report that as the pending state it is, not as a raw API code.
		if cloudflare.IsDestinationUnverified(err) {
			return &DestinationPendingError{Email: forwardTo}
		}
		return fmt.Errorf("create routing rule: %w", err)
	}

	d.AddAlias(alias, []string{forwardTo})
	return st.Save(cfg)
}

// RemoveAlias deletes the forwarding rule for alias@domain and drops it from
// config. The Cloudflare rule is matched on the address, so a rule created
// outside mailctl is removed too — that is the same address either way.
func RemoveAlias(st store.Store, domain, alias string) error {
	cfg, err := st.Load()
	if err != nil {
		return err
	}
	d := cfg.FindDomain(domain)
	if d == nil {
		return fmt.Errorf("domain %s not found in config", domain)
	}

	addr := fmt.Sprintf("%s@%s", alias, domain)
	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	rules, err := cf.ListRoutingRules(d.CloudflareZoneID)
	if err != nil {
		return fmt.Errorf("list routing rules: %w", err)
	}

	found := false
	for _, r := range rules {
		for _, m := range r.Matchers {
			if m.Value == addr {
				if err := cf.DeleteRoutingRule(d.CloudflareZoneID, r.ID); err != nil {
					return fmt.Errorf("delete routing rule: %w", err)
				}
				found = true
				break
			}
		}
	}
	if !found && d.FindAlias(alias) == nil {
		return fmt.Errorf("alias %s not found", addr)
	}

	d.RemoveAlias(alias)
	return st.Save(cfg)
}

// defaultForwardFor picks the address new aliases on a domain should forward
// to: whatever its existing aliases use, falling back to the global default.
func defaultForwardFor(cfg *internal.Config, d *internal.DomainConfig) string {
	for _, a := range d.Aliases {
		if len(a.ForwardTo) > 0 && a.ForwardTo[0] != "" {
			return a.ForwardTo[0]
		}
	}
	return cfg.DefaultForwardTo
}

// AliasView is one address on a domain as it actually routes, joined with what
// config believes.
//
// Cloudflare is the source of truth: rules can be edited in its dashboard, and
// mailctl's config is a cache that goes stale the moment someone does. Showing
// the cached value would tell people mail goes somewhere it does not.
type AliasView struct {
	Alias   string
	Address string
	// ForwardTo is where mail actually goes, read from Cloudflare.
	ForwardTo []string
	// ConfigSays is what mailctl recorded. Empty when the rule exists in
	// Cloudflare but not in config.
	ConfigSays []string
	// RuleID identifies the Cloudflare routing rule.
	RuleID string
	// Drifted is true when config disagrees with live routing.
	Drifted bool
	// Untracked is true when Cloudflare has the rule but config does not.
	Untracked bool
	// Destination is confirmation state for the address this alias forwards
	// to. A rule can exist while its target sits unconfirmed, in which case
	// the alias looks healthy and delivers nothing.
	Destination DestinationStatus
	// DestinationChecked is false when the account read failed. Callers must
	// not draw conclusions about confirmation when it is unset — an unchecked
	// address is not the same as an unconfirmed one.
	DestinationChecked bool
}

// ListAliases returns every address routing on a domain, read live from
// Cloudflare and annotated with any disagreement against config.
func ListAliases(cfg *internal.Config, d *internal.DomainConfig) ([]AliasView, error) {
	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	rules, err := cf.ListRoutingRules(d.CloudflareZoneID)
	if err != nil {
		return nil, fmt.Errorf("list routing rules: %w", err)
	}

	suffix := "@" + d.Domain
	var views []AliasView

	for _, rule := range rules {
		for _, m := range rule.Matchers {
			if !strings.HasSuffix(m.Value, suffix) {
				continue
			}
			alias := strings.TrimSuffix(m.Value, suffix)

			view := AliasView{
				Alias:   alias,
				Address: m.Value,
				RuleID:  rule.ID,
			}
			for _, a := range rule.Actions {
				if a.Type == "forward" {
					view.ForwardTo = append(view.ForwardTo, a.Value...)
				}
			}
			if configured := d.FindAlias(alias); configured != nil {
				view.ConfigSays = configured.ForwardTo
				view.Drifted = !sameAddresses(view.ForwardTo, configured.ForwardTo)
			} else {
				view.Untracked = true
			}
			views = append(views, view)
			break
		}
	}

	annotateDestinations(cf, cfg, d, views)
	return views, nil
}

// annotateDestinations fills in confirmation state for each alias's forwarding
// target, in one account read rather than one per row.
//
// A failed read leaves DestinationChecked false on every view: routing rules
// are the answer callers asked for, and losing the whole list because a
// secondary lookup failed would be worse than showing it without badges.
func annotateDestinations(cf *cloudflare.Client, cfg *internal.Config, d *internal.DomainConfig, views []AliasView) {
	if len(views) == 0 {
		return
	}

	var targets []string
	for _, v := range views {
		if len(v.ForwardTo) > 0 {
			targets = append(targets, v.ForwardTo[0])
		}
	}
	if len(targets) == 0 {
		return
	}

	statuses, err := DestinationsFor(cf, AccountFor(cf, cfg, d), targets)
	if err != nil {
		return
	}
	for i := range views {
		if len(views[i].ForwardTo) == 0 {
			continue
		}
		if status, ok := statuses[strings.ToLower(views[i].ForwardTo[0])]; ok {
			views[i].Destination = status
			views[i].DestinationChecked = true
		}
	}
}

// RepointResult reports what repointing an alias produced.
type RepointResult struct {
	// Destination is where confirmation stands for the new target.
	Destination DestinationStatus
	// DestinationCreated is true when the target had to be registered with
	// Cloudflare as a destination address.
	DestinationCreated bool
	// DestinationVerified reports whether the target can receive forwarded
	// mail yet. Cloudflare will not deliver to an unconfirmed address.
	DestinationVerified bool
}

// RepointAlias changes where alias@domain forwards.
//
// Cloudflare only delivers to destination addresses that have been confirmed
// by their owner, so an unknown target is registered and a confirmation email
// sent — until someone clicks it, mail to this alias goes nowhere.
func RepointAlias(st store.Store, domain, alias, forwardTo string) (*RepointResult, error) {
	forwardTo = strings.TrimSpace(forwardTo)
	if !strings.Contains(forwardTo, "@") {
		return nil, fmt.Errorf("%q is not an email address", forwardTo)
	}

	cfg, err := st.Load()
	if err != nil {
		return nil, err
	}
	d := cfg.FindDomain(domain)
	if d == nil {
		return nil, fmt.Errorf("domain %s not found in config", domain)
	}

	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	result := &RepointResult{}

	// Cloudflare rejects a rule pointing at an address nobody has confirmed, so
	// register it first — which sends the confirmation email — and stop if it is
	// still unconfirmed. Attempting the update anyway would fail and leave the
	// caller with an API code instead of "we are waiting on this person".
	accountID := AccountFor(cf, cfg, d)
	dest, justRegistered, err := EnsureDestination(cf, accountID, forwardTo)
	if err != nil {
		return nil, err
	}
	result.Destination = dest
	result.DestinationCreated = justRegistered
	result.DestinationVerified = dest.Verified()
	if !dest.Verified() {
		// The existing rule is left pointing where it was. Repointing it at an
		// address that cannot receive would silently black-hole the alias.
		return result, &DestinationPendingError{Email: forwardTo, JustRegistered: justRegistered}
	}

	address := fmt.Sprintf("%s@%s", alias, domain)
	rules, err := cf.ListRoutingRules(d.CloudflareZoneID)
	if err != nil {
		return nil, fmt.Errorf("list routing rules: %w", err)
	}

	var target *cloudflare.RoutingRule
	for i := range rules {
		for _, m := range rules[i].Matchers {
			if m.Value == address {
				target = &rules[i]
				break
			}
		}
		if target != nil {
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("no routing rule for %s", address)
	}

	updated := cloudflare.RoutingRule{
		Name:     target.Name,
		Enabled:  true,
		Matchers: target.Matchers,
		Actions:  []cloudflare.RuleAction{{Type: "forward", Value: []string{forwardTo}}},
	}
	if err := cf.UpdateRoutingRule(d.CloudflareZoneID, target.ID, updated); err != nil {
		return nil, fmt.Errorf("update routing rule: %w", err)
	}

	if a := d.FindAlias(alias); a != nil {
		a.ForwardTo = []string{forwardTo}
	} else {
		d.AddAlias(alias, []string{forwardTo})
	}
	if err := st.Save(cfg); err != nil {
		return result, err
	}

	return result, nil
}

// sameAddresses compares two forward-to sets ignoring order and case.
func sameAddresses(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[strings.ToLower(strings.TrimSpace(s))]++
	}
	for _, s := range b {
		seen[strings.ToLower(strings.TrimSpace(s))]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

// CatchAll describes a domain's catch-all rule: where mail to any address not
// matched by a specific rule ends up.
type CatchAll struct {
	Enabled bool
	// DeliversTo is the destination when enabled.
	DeliversTo string
}

// CatchAllFor reads a domain's catch-all rule from Cloudflare.
//
// It lives at its own endpoint and never appears in the routing-rules list, so
// it has to be asked for separately — which is also why teardown has to
// disable it explicitly.
func CatchAllFor(cfg *internal.Config, d *internal.DomainConfig) (*CatchAll, error) {
	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	rule, err := cf.GetCatchAllRule(d.CloudflareZoneID)
	if err != nil {
		return nil, err
	}

	out := &CatchAll{}
	if !rule.Enabled {
		return out, nil
	}
	for _, a := range rule.Actions {
		if a.Type == "forward" && len(a.Value) > 0 {
			out.Enabled = true
			out.DeliversTo = a.Value[0]
			return out, nil
		}
	}
	return out, nil
}
