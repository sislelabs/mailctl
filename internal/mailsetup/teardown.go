package mailsetup

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal/cloudflare"
)

// TeardownDNSResult reports what a DNS teardown did and, just as importantly,
// what it deliberately left alone.
type TeardownDNSResult struct {
	// Deleted holds a human-readable line per record removed.
	Deleted []string
	// Failed holds a line per record that could not be removed.
	Failed []string
	// Untracked is true when the domain carries no record of what mailctl
	// created. Older domains predate ownership tracking, and guessing from
	// record names risks deleting DKIM keys belonging to unrelated services on
	// the same zone, so teardown declines to touch DNS at all in that case.
	Untracked bool
}

// TeardownDNS deletes only the DNS records whose IDs mailctl recorded when it
// created them. Anything else on the zone — another provider's DKIM keys, a
// pre-existing DMARC policy, unrelated TXT records — is left untouched by
// construction rather than by heuristic.
func TeardownDNS(cf *cloudflare.Client, zoneID string, recordIDs []string) TeardownDNSResult {
	if len(recordIDs) == 0 {
		return TeardownDNSResult{Untracked: true}
	}

	// Resolve IDs to names so the output says what was removed rather than
	// printing opaque hashes. A lookup failure is not fatal: the delete is
	// keyed on the ID, and the label is cosmetic.
	labels := make(map[string]string, len(recordIDs))
	if all, err := cf.ListDNSRecords(zoneID, ""); err == nil {
		for _, rec := range all {
			labels[rec.ID] = rec.Type + " " + rec.Name
		}
	}

	var result TeardownDNSResult
	for _, id := range recordIDs {
		label, ok := labels[id]
		if !ok {
			// Already gone — deleted by hand, or removed with the zone.
			continue
		}
		if err := cf.DeleteDNSRecord(zoneID, id); err != nil {
			result.Failed = append(result.Failed, fmt.Sprintf("%s — %s", label, err.Error()))
			continue
		}
		result.Deleted = append(result.Deleted, label)
	}
	return result
}

// DisableCatchAll turns off the catch-all forwarding rule. Teardown has to do
// this explicitly: the catch-all lives at its own API endpoint and is not
// returned by the routing-rules list, so deleting every per-alias rule still
// leaves every address on the domain forwarding to the old destination.
func DisableCatchAll(cf *cloudflare.Client, zoneID string) error {
	return cf.UpdateCatchAllRule(zoneID, cloudflare.RoutingRule{
		Enabled: false,
		Matchers: []cloudflare.RuleMatcher{
			{Type: "all"},
		},
		Actions: []cloudflare.RuleAction{
			{Type: "drop"},
		},
	})
}

// LooksLikeDomain reports whether s is plausibly a registered domain name.
// It is a cheap local guard so `mailctl add acme` fails immediately with a
// clear message instead of after a round-trip to the Cloudflare API.
func LooksLikeDomain(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t/@:") {
		return false
	}
	if strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	// The TLD must be alphabetic and at least two characters.
	tld := labels[len(labels)-1]
	if len(tld) < 2 {
		return false
	}
	for _, r := range tld {
		if r < 'a' || r > 'z' {
			if r < 'A' || r > 'Z' {
				return false
			}
		}
	}
	for _, label := range labels {
		if label == "" {
			return false
		}
	}
	return true
}
