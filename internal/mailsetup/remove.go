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
const (
	RemoveStepRules = iota
	RemoveStepCatchAll
	RemoveStepProviderDomain
	RemoveStepDNS
	RemoveStepSave
	removeStepCount
)

// RemoveStepLabels returns the step labels for a provider, e.g. "Resend".
func RemoveStepLabels(providerLabel string) []string {
	labels := make([]string, removeStepCount)
	labels[RemoveStepRules] = "Delete routing rules"
	labels[RemoveStepCatchAll] = "Disable catch-all"
	labels[RemoveStepProviderDomain] = "Delete " + providerLabel + " domain"
	labels[RemoveStepDNS] = "Delete DNS records created by mailctl"
	labels[RemoveStepSave] = "Remove from config"
	return labels
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

	// Step 0: per-alias routing rules.
	rep.Step(RemoveStepRules, StepRunning, "")
	rules, err := cf.ListRoutingRules(d.CloudflareZoneID)
	if err != nil {
		rep.Step(RemoveStepRules, StepWarn, err.Error())
	} else {
		for _, rule := range rules {
			for _, m := range rule.Matchers {
				if strings.HasSuffix(m.Value, "@"+domain) {
					if err := cf.DeleteRoutingRule(d.CloudflareZoneID, rule.ID); err != nil {
						rep.Note(RemoveStepRules, NoteError, m.Value)
					} else {
						rep.Note(RemoveStepRules, NoteOK, m.Value)
						result.RulesDeleted++
					}
					break
				}
			}
		}
		rep.Step(RemoveStepRules, StepDone, fmt.Sprintf("%d deleted", result.RulesDeleted))
	}

	// Step 1: the catch-all lives at its own endpoint and never appears in the
	// rules list, so without this every address keeps forwarding.
	rep.Step(RemoveStepCatchAll, StepRunning, "")
	if err := DisableCatchAll(cf, d.CloudflareZoneID); err != nil {
		rep.Step(RemoveStepCatchAll, StepWarn, err.Error())
	} else {
		rep.Step(RemoveStepCatchAll, StepDone, "*@"+domain+" no longer forwards")
	}

	// Step 2: the sending provider's domain record.
	rep.Step(RemoveStepProviderDomain, StepRunning, "")
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
			rep.Step(RemoveStepProviderDomain, StepWarn, "not found or already deleted")
		} else if err := rc.DeleteDomain(id); err != nil {
			rep.Step(RemoveStepProviderDomain, StepWarn, "not found or already deleted")
		} else {
			rep.Step(RemoveStepProviderDomain, StepDone, "")
		}
	} else {
		bv := brevo.NewClient(cfg.BrevoAPIKey)
		if err := bv.DeleteDomain(domain); err != nil {
			rep.Step(RemoveStepProviderDomain, StepWarn, "not found or already deleted")
		} else {
			rep.Step(RemoveStepProviderDomain, StepDone, "")
		}
	}

	// Step 3: only mailctl's own DNS records.
	rep.Step(RemoveStepDNS, StepRunning, "")
	result.DNS = TeardownDNS(cf, d.CloudflareZoneID, d.ManagedDNSRecordIDs)
	if result.DNS.Untracked {
		rep.Step(RemoveStepDNS, StepWarn, "skipped — no records tracked for this domain")
		rep.Note(RemoveStepDNS, NoteWarn, "Added before mailctl tracked record ownership.")
		rep.Note(RemoveStepDNS, NoteWarn, "Delete its SPF/DKIM records by hand so other services keep theirs.")
	} else {
		for _, line := range result.DNS.Deleted {
			rep.Note(RemoveStepDNS, NoteOK, line)
		}
		for _, line := range result.DNS.Failed {
			rep.Note(RemoveStepDNS, NoteWarn, line)
		}
		rep.Step(RemoveStepDNS, StepDone, fmt.Sprintf("%d deleted", len(result.DNS.Deleted)))
	}

	// Step 4: persist.
	rep.Step(RemoveStepSave, StepRunning, "")
	cfg.RemoveDomain(domain)
	if err := st.Save(cfg); err != nil {
		rep.Step(RemoveStepSave, StepFailed, err.Error())
		return result, err
	}
	rep.Step(RemoveStepSave, StepDone, "")

	return result, nil
}
