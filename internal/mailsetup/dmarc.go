// Package mailsetup holds the domain setup and teardown logic shared by the
// CLI commands and the TUI, so the two cannot drift apart on anything
// destructive or deliverability-critical.
package mailsetup

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal/cloudflare"
)

// DMARCName is the host a domain's DMARC policy lives on.
func DMARCName(domain string) string {
	return "_dmarc." + domain
}

// DMARCContent builds a starter DMARC policy. The policy is p=none: it asks
// receivers to report on failures without acting on them, which is what
// Resend's own guidance recommends adopting first. Tightening to quarantine or
// reject is a deliberate follow-up once the aggregate reports look clean —
// starting strict can silently blackhole legitimate mail.
//
// reportTo receives the aggregate XML reports. It may be empty, in which case
// the record carries a policy but requests no reports.
func DMARCContent(reportTo string) string {
	policy := "v=DMARC1; p=none"
	if reportTo != "" {
		policy += "; rua=mailto:" + reportTo
	}
	return policy
}

// DMARCStatus describes what EnsureDMARC did.
type DMARCStatus int

const (
	// DMARCCreated means mailctl added a new policy record.
	DMARCCreated DMARCStatus = iota
	// DMARCExists means the domain already had a DMARC record, which was left
	// exactly as it was. An existing policy is assumed deliberate.
	DMARCExists
	// DMARCFailed means the record could not be created.
	DMARCFailed
)

// EnsureDMARC publishes a starter DMARC policy for domain unless one already
// exists. Missing DMARC is the single most common reason mail from a correctly
// authenticated domain still lands in spam: SPF and DKIM prove the message is
// authentic, but without DMARC many receivers have no published policy to
// evaluate them against.
//
// It returns the created record's Cloudflare ID (empty unless status is
// DMARCCreated) so the caller can record ownership.
func EnsureDMARC(cf *cloudflare.Client, zoneID, domain, reportTo string) (recordID string, status DMARCStatus, detail string) {
	name := DMARCName(domain)

	existing, err := cf.ListDNSRecords(zoneID, "TXT")
	if err == nil {
		for _, rec := range existing {
			if strings.EqualFold(rec.Name, name) {
				return "", DMARCExists, rec.Content
			}
		}
	}

	id, err := cf.CreateDNSRecord(zoneID, cloudflare.DNSRecord{
		Type:    "TXT",
		Name:    name,
		Content: DMARCContent(reportTo),
		TTL:     3600,
	})
	if err != nil {
		return "", DMARCFailed, err.Error()
	}
	return id, DMARCCreated, fmt.Sprintf("p=none, reports to %s", reportTo)
}
