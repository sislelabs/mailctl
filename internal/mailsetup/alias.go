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
	rule := cloudflare.RoutingRule{
		Name:     "Forward " + addr,
		Enabled:  true,
		Matchers: []cloudflare.RuleMatcher{{Type: "literal", Field: "to", Value: addr}},
		Actions:  []cloudflare.RuleAction{{Type: "forward", Value: []string{forwardTo}}},
	}
	if err := cf.CreateRoutingRule(d.CloudflareZoneID, rule); err != nil {
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

	return views, nil
}

// RepointResult reports what repointing an alias produced.
type RepointResult struct {
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

	// Cloudflare rejects a rule pointing at an unregistered destination, so
	// register it first and report whether it is usable yet.
	accountID := cfg.CloudflareAccountID
	if addrs, err := cf.ListDestinationAddresses(accountID); err == nil {
		for _, a := range addrs {
			if strings.EqualFold(a.Email, forwardTo) {
				result.DestinationVerified = a.Verified != ""
				goto haveDestination
			}
		}
		if err := cf.CreateDestinationAddress(accountID, forwardTo); err != nil {
			return nil, fmt.Errorf("register %s as a destination: %w", forwardTo, err)
		}
		result.DestinationCreated = true
	}
haveDestination:

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
