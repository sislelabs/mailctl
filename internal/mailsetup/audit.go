package mailsetup

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/resend"
)

// FindingLevel ranks an audit finding.
type FindingLevel int

const (
	// FindingInfo is something worth knowing that is not wrong.
	FindingInfo FindingLevel = iota
	// FindingWarn degrades the setup without breaking it.
	FindingWarn
	// FindingProblem breaks sending or receiving.
	FindingProblem
)

func (l FindingLevel) String() string {
	switch l {
	case FindingProblem:
		return "problem"
	case FindingWarn:
		return "warning"
	default:
		return "info"
	}
}

// Finding is one thing the audit noticed.
type Finding struct {
	Level  FindingLevel
	Domain string
	Title  string
	Detail string
}

// AuditReport is the reconciliation of intended state (what config says) with
// actual state (what Cloudflare and the sending provider hold).
//
// The gap between the two is where problems live: mailctl check only inspects
// domains already in config, so anything configured outside it — a Resend
// domain nobody added here, a leftover record from a deleted setup — is
// invisible until something breaks.
type AuditReport struct {
	Findings []Finding
	// Zones the token can see.
	Zones []cloudflare.Zone
	// ResendDomains verified with the sending provider.
	ResendDomains []resend.Domain
	// Managed lists domains present in config.
	Managed []string
}

// Counts returns how many findings sit at each level.
func (r *AuditReport) Counts() (problems, warnings, infos int) {
	for _, f := range r.Findings {
		switch f.Level {
		case FindingProblem:
			problems++
		case FindingWarn:
			warnings++
		default:
			infos++
		}
	}
	return
}

// Audit reconciles config against Cloudflare and the sending provider.
//
// It is read-only and tolerant: a failure to reach one API degrades that
// section into a finding rather than failing the whole report, because a
// partial picture is still more than nothing.
func Audit(cfg *internal.Config) *AuditReport {
	report := &AuditReport{}
	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)

	for _, d := range cfg.Domains {
		report.Managed = append(report.Managed, d.Domain)
	}

	if zones, err := cf.ListZones(); err == nil {
		report.Zones = zones
	} else {
		report.Findings = append(report.Findings, Finding{
			Level:  FindingWarn,
			Title:  "Could not list Cloudflare zones",
			Detail: err.Error(),
		})
	}

	usingResend := cfg.SendingProvider() == internal.ProviderResend
	verified := map[string]bool{}
	if usingResend && cfg.ResendAPIKey != "" {
		if domains, err := resend.NewClient(cfg.ResendAPIKey).ListDomains(); err == nil {
			report.ResendDomains = domains
			for _, rd := range domains {
				verified[strings.ToLower(rd.Name)] = true
			}
		} else {
			report.Findings = append(report.Findings, Finding{
				Level:  FindingWarn,
				Title:  "Could not list Resend domains",
				Detail: err.Error(),
			})
		}
	}

	managed := map[string]bool{}
	for i := range cfg.Domains {
		d := &cfg.Domains[i]
		managed[strings.ToLower(d.Domain)] = true
		report.Findings = append(report.Findings, auditDomain(cf, d, usingResend, verified)...)
	}

	// Sending domains nobody registered here. These still send mail as the
	// organisation, so they need the same DMARC and DNS hygiene, but no
	// mailctl command will ever look at them.
	for _, rd := range report.ResendDomains {
		if !managed[strings.ToLower(rd.Name)] {
			report.Findings = append(report.Findings, Finding{
				Level:  FindingInfo,
				Domain: rd.Name,
				Title:  "Sends through Resend but is not in mailctl",
				Detail: "status " + rd.Status + " — no mailctl command manages this domain",
			})
		}
	}

	return report
}

// auditDomain checks one configured domain against live state.
func auditDomain(cf *cloudflare.Client, d *internal.DomainConfig, usingResend bool, verified map[string]bool) []Finding {
	var findings []Finding

	if usingResend && len(verified) > 0 && !verified[strings.ToLower(d.Domain)] {
		findings = append(findings, Finding{
			Level:  FindingProblem,
			Domain: d.Domain,
			Title:  "Not registered with Resend",
			Detail: "nothing can send from this domain until it is added and verified",
		})
	}

	records, err := cf.ListDNSRecords(d.CloudflareZoneID, "")
	if err != nil {
		findings = append(findings, Finding{
			Level:  FindingWarn,
			Domain: d.Domain,
			Title:  "Could not read DNS records",
			Detail: err.Error(),
		})
		return findings
	}

	// DMARC. Without a published policy, receivers have no instruction for
	// messages that fail SPF or DKIM, which is the usual reason authenticated
	// mail is still filtered.
	dmarcName := DMARCName(d.Domain)
	var dmarc *cloudflare.DNSRecord
	for i := range records {
		if strings.EqualFold(records[i].Name, dmarcName) && strings.EqualFold(records[i].Type, "TXT") {
			dmarc = &records[i]
			break
		}
	}
	switch {
	case dmarc == nil:
		findings = append(findings, Finding{
			Level:  FindingWarn,
			Domain: d.Domain,
			Title:  "No DMARC policy",
			Detail: "add a TXT record at " + dmarcName,
		})
	case !strings.Contains(dmarc.Content, "rua="):
		findings = append(findings, Finding{
			Level:  FindingWarn,
			Domain: d.Domain,
			Title:  "DMARC reports nowhere",
			Detail: "policy has no rua= address, so no aggregate reports are collected",
		})
	case strings.Contains(dmarc.Content, "dmarc.brevo.com"):
		findings = append(findings, Finding{
			Level:  FindingWarn,
			Domain: d.Domain,
			Title:  "DMARC reports to Brevo",
			Detail: "aggregate reports go to a provider you may no longer use",
		})
	}

	// Return-path MX records for sending domains that no longer exist. Teardown
	// never removed MX records, so a deleted sending domain leaves one behind.
	for _, rec := range records {
		if !strings.EqualFold(rec.Type, "MX") || !strings.Contains(rec.Content, "amazonses.com") {
			continue
		}
		host := strings.TrimSuffix(rec.Name, "."+d.Domain)
		if host == "send" || rec.Name == d.Domain {
			continue
		}
		findings = append(findings, Finding{
			Level:  FindingWarn,
			Domain: d.Domain,
			Title:  "Orphaned return-path record",
			Detail: fmt.Sprintf("MX %s points at %s but no sending domain matches", rec.Name, rec.Content),
		})
	}

	if len(d.ManagedDNSRecordIDs) == 0 {
		findings = append(findings, Finding{
			Level:  FindingInfo,
			Domain: d.Domain,
			Title:  "No tracked DNS records",
			Detail: "added before record tracking — teardown will skip DNS for this domain",
		})
	}

	return findings
}
