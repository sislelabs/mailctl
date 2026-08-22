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

// Steps in the register flow, in order.
const (
	RegisterStepDomain = iota
	RegisterStepDNS
	RegisterStepAuthenticate
	RegisterStepSenders
	RegisterStepSave
	registerStepCount
)

// RegisterStepLabels returns the step labels for a provider, e.g. "Resend".
func RegisterStepLabels(providerLabel string) []string {
	labels := make([]string, registerStepCount)
	labels[RegisterStepDomain] = "Add domain to " + providerLabel
	labels[RegisterStepDNS] = "Add sending DNS records"
	labels[RegisterStepAuthenticate] = "Authenticate domain"
	labels[RegisterStepSenders] = "Create senders"
	labels[RegisterStepSave] = "Save config"
	return labels
}

// registerProviderSteps maps the sending stages onto the register flow, which
// starts them at zero because it runs nothing before them.
var registerProviderSteps = providerSteps{
	Domain:       RegisterStepDomain,
	DNS:          RegisterStepDNS,
	Authenticate: RegisterStepAuthenticate,
	Senders:      RegisterStepSenders,
}

// RegisterOptions describes a domain to register with the sending provider.
type RegisterOptions struct {
	Domain string
}

// RegisterResult reports what registration produced.
type RegisterResult struct {
	ResendDomainID   string
	ManagedRecordIDs []string
	// Conflicts lists records the provider wants published where a record of
	// the same name and type already exists with different content. They are
	// reported, never overwritten.
	Conflicts []RecordConflict
}

// RecordConflict is an existing DNS record that disagrees with what the
// sending provider expects.
type RecordConflict struct {
	Type     string
	Name     string
	Existing string
	Wanted   string
}

// RegisterDomain registers an already-configured domain with the sending
// provider and publishes its sending DNS.
//
// It exists for domains that receive mail correctly but cannot send — the
// state mailctl audit reports as "Not registered with Resend". Tearing the
// domain down and adding it back would achieve the same thing while destroying
// every routing rule and alias on the way, so this touches none of that: no
// routing rules, no catch-all, no apex MX, no aliases. It only adds what
// sending needs.
//
// Existing DNS records are never modified or deleted. A record that already
// matches is left alone; one that disagrees is reported as a conflict for a
// human to resolve.
func RegisterDomain(st store.Store, rep Reporter, opts RegisterOptions) (*RegisterResult, error) {
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
		return nil, fmt.Errorf("domain %s is not in config — use 'mailctl add %s' to set it up from scratch", domain, domain)
	}

	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)

	// Snapshot existing records so nothing is published on top of a record
	// that is already there.
	existing, err := cf.ListDNSRecords(d.CloudflareZoneID, "")
	if err != nil {
		return nil, fmt.Errorf("read existing DNS records: %w", err)
	}

	addResult := &AddResult{}
	result := &RegisterResult{}

	if cfg.SendingProvider() == internal.ProviderResend {
		rc := resend.NewClient(cfg.ResendAPIKey)
		if d.ResendDomainID != "" {
			if rd, err := rc.GetDomain(d.ResendDomainID); err == nil && rd != nil {
				return nil, fmt.Errorf("%s is already registered with Resend (%s)", domain, rd.Status)
			}
		}
		registerResend(cf, rc, rep, existing, d.CloudflareZoneID, domain, cfg.DefaultForwardTo, addResult, result)
	} else {
		bv := brevo.NewClient(cfg.BrevoAPIKey)
		addBrevo(cf, bv, rep, registerProviderSteps, d.CloudflareZoneID, domain, cfg.DefaultForwardTo, aliasNames(d), addResult)
	}

	result.ResendDomainID = addResult.ResendDomainID
	result.ManagedRecordIDs = addResult.ManagedRecordIDs

	rep.Step(RegisterStepSave, StepRunning, "")
	if result.ResendDomainID != "" {
		d.ResendDomainID = result.ResendDomainID
	}
	// Append rather than replace: the domain may already have tracked records
	// from an earlier run, and losing them would silently downgrade teardown
	// to skipping DNS.
	d.ManagedDNSRecordIDs = append(d.ManagedDNSRecordIDs, result.ManagedRecordIDs...)
	if err := st.Save(cfg); err != nil {
		rep.Step(RegisterStepSave, StepFailed, err.Error())
		return result, err
	}
	rep.Step(RegisterStepSave, StepDone, "")

	return result, nil
}

// registerResend adds the domain to Resend and publishes only the records that
// are not already present.
func registerResend(cf *cloudflare.Client, rc *resend.Client, rep Reporter, existing []cloudflare.DNSRecord, zoneID, domain, forwardTo string, addResult *AddResult, result *RegisterResult) {
	rep.Step(RegisterStepDomain, StepRunning, "")
	rd, err := rc.AddDomain(domain)
	if err != nil {
		rep.Step(RegisterStepDomain, StepFailed, err.Error())
		rep.Step(RegisterStepDNS, StepWarn, "skipped")
		rep.Step(RegisterStepAuthenticate, StepWarn, "skipped")
		rep.Step(RegisterStepSenders, StepDone, "n/a for Resend")
		return
	}
	addResult.ResendDomainID = rd.ID
	rep.Step(RegisterStepDomain, StepDone, rd.ID+" ("+rd.Region+")")

	rep.Step(RegisterStepDNS, StepRunning, "")
	for _, rec := range rd.Records {
		name := ResendRecordName(rec.Name, domain)

		if match := findRecord(existing, rec.Type, name); match != nil {
			if sameRecordContent(match.Content, rec.Value) {
				rep.Note(RegisterStepDNS, NoteOK, rec.Type+" "+name+" — already correct, left as is")
			} else {
				result.Conflicts = append(result.Conflicts, RecordConflict{
					Type: rec.Type, Name: name, Existing: match.Content, Wanted: rec.Value,
				})
				rep.Note(RegisterStepDNS, NoteWarn, rec.Type+" "+name+" — conflicts with an existing record, left alone")
			}
			continue
		}

		cfRec := cloudflare.DNSRecord{Type: rec.Type, Name: name, Content: rec.Value, TTL: 3600}
		if strings.EqualFold(rec.Type, "MX") && rec.Priority > 0 {
			prio := rec.Priority
			cfRec.Priority = &prio
		}
		id, err := cf.CreateDNSRecord(zoneID, cfRec)
		if err != nil {
			rep.Note(RegisterStepDNS, NoteWarn, rec.Type+" "+name+" — "+err.Error())
			continue
		}
		addResult.recordManaged(id)
		rep.Note(RegisterStepDNS, NoteOK, rec.Type+" "+name)
	}
	publishDMARC(cf, rep, RegisterStepDNS, zoneID, domain, forwardTo, addResult)
	rep.Step(RegisterStepDNS, StepDone, "")

	rep.Step(RegisterStepAuthenticate, StepRunning, "")
	if err := rc.VerifyDomain(rd.ID); err != nil {
		rep.Step(RegisterStepAuthenticate, StepWarn, "pending — DNS may need time to propagate")
	} else {
		rep.Step(RegisterStepAuthenticate, StepDone, "verification started")
	}

	rep.Step(RegisterStepSenders, StepDone, "n/a for Resend")
}

// findRecord returns the first existing record with a matching type and name.
func findRecord(records []cloudflare.DNSRecord, recType, name string) *cloudflare.DNSRecord {
	for i := range records {
		if strings.EqualFold(records[i].Type, recType) && strings.EqualFold(records[i].Name, name) {
			return &records[i]
		}
	}
	return nil
}

func aliasNames(d *internal.DomainConfig) []string {
	names := make([]string, 0, len(d.Aliases))
	for _, a := range d.Aliases {
		names = append(names, a.Alias)
	}
	return names
}

// sameRecordContent compares DNS record values ignoring the quoting Cloudflare
// applies to TXT records. Cloudflare returns a TXT value wrapped in double
// quotes while providers hand it over bare, so a byte comparison reports an
// identical SPF record as a conflict.
func sameRecordContent(existing, wanted string) bool {
	return normalizeRecordContent(existing) == normalizeRecordContent(wanted)
}

func normalizeRecordContent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	return strings.TrimSpace(s)
}
