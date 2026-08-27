package mailsetup

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/resend"
)

// SendingRecord is one DNS record the sending provider requires, checked
// against what the zone actually holds.
type SendingRecord struct {
	// Record is the provider's label for it, e.g. DKIM or SPF.
	Record string
	Type   string
	// Name is fully qualified against the zone apex.
	Name string
	// Status is the provider's view: verified, pending, failed.
	Status string
	// Present is true when a record of this name and type exists in the zone.
	Present bool
	// Matches is true when the existing content agrees with the provider.
	Matches bool
	// Duplicates counts how many records share this name and type. More than
	// one DKIM record at a selector is ambiguous: RFC 6376 leaves the choice
	// to the verifier, so signatures start failing intermittently.
	Duplicates int
}

// OK reports whether the record needs no attention.
func (r SendingRecord) OK() bool {
	return r.Present && r.Matches && r.Duplicates < 2 && strings.EqualFold(r.Status, "verified")
}

// Problem describes what is wrong with the record, or "" when it is fine.
func (r SendingRecord) Problem() string {
	switch {
	case !r.Present:
		return "missing from DNS"
	case r.Duplicates > 1:
		return fmt.Sprintf("%d records at this name — verifiers may pick the wrong one", r.Duplicates)
	case !r.Matches:
		return "content does not match what the provider expects"
	case !strings.EqualFold(r.Status, "verified"):
		return "provider reports " + r.Status
	}
	return ""
}

// SendingDNS reports the provider's required records for a domain, checked
// against the zone. It is read-only.
func SendingDNS(cfg *internal.Config, d *internal.DomainConfig) ([]SendingRecord, error) {
	if cfg.SendingProvider() != internal.ProviderResend {
		return nil, fmt.Errorf("sending DNS view is only implemented for Resend")
	}
	if cfg.ResendAPIKey == "" {
		return nil, fmt.Errorf("no resend_api_key in config")
	}

	rc := resend.NewClient(cfg.ResendAPIKey)

	var rd *resend.Domain
	var err error
	if d.ResendDomainID != "" {
		rd, err = rc.GetDomain(d.ResendDomainID)
	} else {
		// The list endpoint omits records, so a name lookup has to be followed
		// by a fetch before anything can be checked.
		var found *resend.Domain
		found, err = rc.FindDomainByName(d.Domain)
		if err == nil && found != nil {
			rd, err = rc.GetDomain(found.ID)
		} else if err == nil {
			return nil, fmt.Errorf("%s is not registered with Resend", d.Domain)
		}
	}
	if err != nil {
		return nil, err
	}
	if rd == nil {
		return nil, fmt.Errorf("%s is not registered with Resend", d.Domain)
	}

	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	existing, err := cf.ListDNSRecords(d.CloudflareZoneID, "")
	if err != nil {
		return nil, fmt.Errorf("read DNS records: %w", err)
	}

	out := make([]SendingRecord, 0, len(rd.Records))
	for _, rec := range rd.Records {
		name := ResendRecordName(rec.Name, d.ZoneName())
		view := SendingRecord{
			Record: rec.Record,
			Type:   rec.Type,
			Name:   name,
			Status: rec.Status,
		}
		for _, have := range existing {
			if !strings.EqualFold(have.Type, rec.Type) || !strings.EqualFold(have.Name, name) {
				continue
			}
			view.Duplicates++
			view.Present = true
			if sameRecordContent(have.Content, rec.Value) {
				view.Matches = true
			}
		}
		out = append(out, view)
	}
	return out, nil
}
