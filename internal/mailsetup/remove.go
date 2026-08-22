package mailsetup

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/brevo"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/resend"
	"github.com/sislelabs/mailctl/internal/store"
)

// Steps in the remove flow, in order.
// removePlan is the set of steps a teardown will run and the progress index of
// each. A step that does not apply carries stepAbsent.
//
// A sending domain has no routing to undo, and its records usually sit in a
// zone that carries unrelated mail — the whole reason a newsletter sender gets
// its own subdomain. Running the routing steps for one would disable the
// catch-all on that parent zone, which is a different domain's mail.
type removePlan struct {
	labels   []string
	rules    int
	catchAll int
	provider int
	dns      int
	save     int
}

// stepAbsent marks a step that does not apply to this teardown.
const stepAbsent = -1

func planRemove(d *internal.DomainConfig, providerLabel string) removePlan {
	if d.IsSending() {
		return removePlan{
			labels: []string{
				"Delete " + providerLabel + " domain",
				"Delete DNS records created by mailctl",
				"Remove from config",
			},
			rules:    stepAbsent,
			catchAll: stepAbsent,
			provider: 0,
			dns:      1,
			save:     2,
		}
	}
	return removePlan{
		labels: []string{
			"Delete routing rules",
			"Disable catch-all",
			"Delete " + providerLabel + " domain",
			"Delete DNS records created by mailctl",
			"Remove from config",
		},
		rules:    0,
		catchAll: 1,
		provider: 2,
		dns:      3,
		save:     4,
	}
}

// RemoveStepLabels returns the labels for tearing down a domain, which differ
// by kind: a sending domain skips the routing steps entirely.
func RemoveStepLabels(d *internal.DomainConfig, providerLabel string) []string {
	return planRemove(d, providerLabel).labels
}

// RemoveOptions describes a domain to tear down.
type RemoveOptions struct {
	Domain string
}

// RemoveResult reports what teardown did.
type RemoveResult struct {
	RulesDeleted int
	DNS          TeardownDNSResult
}

// RemoveDomain tears down a domain's email setup and persists the result.
//
// DNS teardown is limited to the records mailctl recorded creating. A domain
// added before that tracking existed has none, and its DNS is left alone
// rather than guessed at — guessing is what previously destroyed DKIM keys
// belonging to unrelated services sharing the zone.
func RemoveDomain(st store.Store, rep Reporter, opts RemoveOptions) (*RemoveResult, error) {
	if rep == nil {
		rep = NopReporter{}
	}

	cfg, err := st.Load()
	if err != nil {
		return nil, err
	}

	domain := strings.TrimSpace(opts.Domain)
	d := cfg.FindDomain(domain)
	if d == nil {
		return nil, fmt.Errorf("domain %s not found in config", domain)
	}

	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	result := &RemoveResult{}
	plan := planRemove(d, ProviderLabel(cfg))

	// Routing steps run only for a mailbox domain. A sending domain has none to
	// undo, and its zone usually carries unrelated mail.
	if plan.rules != stepAbsent {
		removeRouting(cf, rep, plan, d, domain, result)
	}

	// The sending provider's domain record.
	rep.Step(plan.provider, StepRunning, "")
	if cfg.SendingProvider() == internal.ProviderResend {
		rc := resend.NewClient(cfg.ResendAPIKey)
		id := d.ResendDomainID
		if id == "" {
			// Domains added before the ID was persisted only have a name.
			if rd, err := rc.FindDomainByName(domain); err == nil && rd != nil {
				id = rd.ID
			}
		}
		if id == "" {
			rep.Step(plan.provider, StepWarn, "not found or already deleted")
		} else if err := rc.DeleteDomain(id); err != nil {
			rep.Step(plan.provider, StepWarn, "not found or already deleted")
		} else {
			rep.Step(plan.provider, StepDone, "")
		}
	} else {
		bv := brevo.NewClient(cfg.BrevoAPIKey)
		if err := bv.DeleteDomain(domain); err != nil {
			rep.Step(plan.provider, StepWarn, "not found or already deleted")
		} else {
			rep.Step(plan.provider, StepDone, "")
		}
	}

	// Step 3: only mailctl's own DNS records.
	rep.Step(plan.dns, StepRunning, "")
	result.DNS = TeardownDNS(cf, d.CloudflareZoneID, d.ManagedDNSRecordIDs)
	if result.DNS.Untracked {
		rep.Step(plan.dns, StepWarn, "skipped — no records tracked for this domain")
		rep.Note(plan.dns, NoteWarn, "Added before mailctl tracked record ownership.")
		rep.Note(plan.dns, NoteWarn, "Delete its SPF/DKIM records by hand so other services keep theirs.")
	} else {
		for _, line := range result.DNS.Deleted {
			rep.Note(plan.dns, NoteOK, line)
		}
		for _, line := range result.DNS.Failed {
			rep.Note(plan.dns, NoteWarn, line)
		}
		rep.Step(plan.dns, StepDone, fmt.Sprintf("%d deleted", len(result.DNS.Deleted)))
	}

	// Step 4: persist.
	rep.Step(plan.save, StepRunning, "")
	cfg.RemoveDomain(domain)
	if err := st.Save(cfg); err != nil {
		rep.Step(plan.save, StepFailed, err.Error())
		return result, err
	}
	rep.Step(plan.save, StepDone, "")

	return result, nil
}

// removeRouting deletes a mailbox domain's forwarding rules and disables its
// catch-all. It runs only for mailbox domains: the catch-all is a property of
// the whole zone, so doing this for a sending subdomain would stop mail for
// every other address the zone serves.
func removeRouting(cf *cloudflare.Client, rep Reporter, plan removePlan, d *internal.DomainConfig, domain string, result *RemoveResult) {
	rep.Step(plan.rules, StepRunning, "")
	rules, err := cf.ListRoutingRules(d.CloudflareZoneID)
	if err != nil {
		rep.Step(plan.rules, StepWarn, err.Error())
	} else {
		for _, rule := range rules {
			for _, m := range rule.Matchers {
				if strings.HasSuffix(m.Value, "@"+domain) {
					if err := cf.DeleteRoutingRule(d.CloudflareZoneID, rule.ID); err != nil {
						rep.Note(plan.rules, NoteError, m.Value)
					} else {
						rep.Note(plan.rules, NoteOK, m.Value)
						result.RulesDeleted++
					}
					break
				}
			}
		}
		rep.Step(plan.rules, StepDone, fmt.Sprintf("%d deleted", result.RulesDeleted))
	}

	// The catch-all lives at its own endpoint and never appears in the rules
	// list, so without this every address keeps forwarding.
	rep.Step(plan.catchAll, StepRunning, "")
	if err := DisableCatchAll(cf, d.CloudflareZoneID); err != nil {
		rep.Step(plan.catchAll, StepWarn, err.Error())
	} else {
		rep.Step(plan.catchAll, StepDone, "*@"+domain+" no longer forwards")
	}
}
