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
