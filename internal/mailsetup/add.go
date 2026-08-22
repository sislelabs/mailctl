package mailsetup

import (
	"fmt"
	"strings"
	"time"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/brevo"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/resend"
	"github.com/sislelabs/mailctl/internal/store"
)

// Steps in the add flow, in order. Frontends render labels from
// AddStepLabels and address rows by these indexes.
const (
	AddStepZone = iota
	AddStepRouting
	AddStepDestination
	AddStepRules
	AddStepCatchAll
	AddStepProviderDomain
	AddStepDNS
	AddStepAuthenticate
	AddStepSenders
	AddStepSave
	addStepCount
)

// AddStepLabels returns the step labels for a provider, e.g. "Resend".
func AddStepLabels(providerLabel string) []string {
	labels := make([]string, addStepCount)
	labels[AddStepZone] = "Look up Cloudflare zone"
	labels[AddStepRouting] = "Enable email routing"
	labels[AddStepDestination] = "Verify destination address"
	labels[AddStepRules] = "Create routing rules"
	labels[AddStepCatchAll] = "Enable catch-all"
	labels[AddStepProviderDomain] = "Add domain to " + providerLabel
	labels[AddStepDNS] = "Add DNS records"
	labels[AddStepAuthenticate] = "Authenticate domain"
	labels[AddStepSenders] = "Create senders"
	labels[AddStepSave] = "Save config"
	return labels
}

// ProviderLabel is the display name of a config's sending provider.
func ProviderLabel(cfg *internal.Config) string {
	if cfg.SendingProvider() == internal.ProviderResend {
		return "Resend"
	}
	return "Brevo"
}

// AddOptions describes a domain to set up.
type AddOptions struct {
	Domain  string
	Aliases []string
	// ForwardTo overrides the config's default forward-to address.
	ForwardTo string
}

// AddResult reports what setting up a domain produced.
type AddResult struct {
	ZoneID           string
	ResendDomainID   string
	ManagedRecordIDs []string
	Aliases          []internal.Alias
}

// AddDomain sets up receiving and sending for a domain and persists the result.
//
// It is the single implementation behind every frontend: progress goes to rep,
// which decides how to present it. Errors returned here are the ones that stop
// the run; anything recoverable is reported as a warning step and the flow
// continues, because receiving keeps working even when the sending provider
// rejects the domain.
func AddDomain(st store.Store, rep Reporter, opts AddOptions) (*AddResult, error) {
	if rep == nil {
		rep = NopReporter{}
	}

	domain := strings.TrimSpace(opts.Domain)

	cfg, err := st.Load()
	if err != nil {
		return nil, err
	}
	if err := ValidateAdd(cfg, opts); err != nil {
		return nil, err
	}

	forwardTo := cfg.DefaultForwardTo
	if opts.ForwardTo != "" {
		forwardTo = opts.ForwardTo
	}

	aliases := make([]string, 0, len(opts.Aliases))
	for _, a := range opts.Aliases {
		if a = strings.TrimSpace(a); a != "" {
			aliases = append(aliases, a)
		}
	}

	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	result := &AddResult{}

	// Step 0: locate the zone. Nothing else can proceed without it.
	rep.Step(AddStepZone, StepRunning, "")
	match, err := ResolveZone(cf, domain)
	if err != nil {
		rep.Step(AddStepZone, StepFailed, "not found — is it added to Cloudflare?")
		return nil, err
	}
	// Cloudflare Email Routing operates on a whole zone, so a subdomain cannot
	// receive mail. Setting one up as a mailbox domain would create rules and a
	// catch-all on the parent zone instead.
	if match.IsSubdomain {
		rep.Step(AddStepZone, StepFailed, "subdomain of "+match.ZoneName())
		return nil, fmt.Errorf("%s is a subdomain of the zone %s, so it cannot receive mail — use 'mailctl register %s' to set it up for sending only", domain, match.ZoneName(), domain)
	}
	zone := match.Zone
	result.ZoneID = zone.ID
	rep.Step(AddStepZone, StepDone, zone.ID)

	// Step 1: enable routing, clearing apex MX records that would block it.
	rep.Step(AddStepRouting, StepRunning, "")
	mxRecords, _ := cf.ListDNSRecords(zone.ID, "MX")
	for _, mx := range mxRecords {
		if mx.Name == domain {
			cf.DeleteDNSRecord(zone.ID, mx.ID)
		}
	}
	if err := cf.EnableEmailRouting(zone.ID); err != nil {
		rep.Step(AddStepRouting, StepWarn, err.Error())
	} else {
		rep.Step(AddStepRouting, StepDone, "")
	}

	// Step 2: the destination address must be verified before anything routes.
	rep.Step(AddStepDestination, StepRunning, "")
	destVerified := false
	if addrs, err := cf.ListDestinationAddresses(zone.Account.ID); err == nil {
		for _, a := range addrs {
			if a.Email == forwardTo && a.Verified != "" {
				destVerified = true
				break
			}
		}
	}
	if destVerified {
		rep.Step(AddStepDestination, StepDone, forwardTo+" — verified")
	} else if err := cf.CreateDestinationAddress(zone.Account.ID, forwardTo); err != nil {
		rep.Step(AddStepDestination, StepWarn, forwardTo+" — check Cloudflare Email Routing > Destination addresses")
	} else {
		rep.Step(AddStepDestination, StepWarn, forwardTo+" — verification email sent, confirm it before mail will route")
	}

	// Step 3: one forwarding rule per alias.
	rep.Step(AddStepRules, StepRunning, "")
	ruleErrors := 0
	for _, alias := range aliases {
		addr := fmt.Sprintf("%s@%s", alias, domain)
		rule := cloudflare.RoutingRule{
			Name:     fmt.Sprintf("Forward %s", addr),
			Enabled:  true,
			Matchers: []cloudflare.RuleMatcher{{Type: "literal", Field: "to", Value: addr}},
			Actions:  []cloudflare.RuleAction{{Type: "forward", Value: []string{forwardTo}}},
		}
		if err := cf.CreateRoutingRule(zone.ID, rule); err != nil {
			rep.Note(AddStepRules, NoteError, addr+" — "+err.Error())
			ruleErrors++
		} else {
			rep.Note(AddStepRules, NoteOK, addr+" → "+forwardTo)
		}
		result.Aliases = append(result.Aliases, internal.Alias{
			Alias:     alias,
			ForwardTo: []string{forwardTo},
		})
	}
	if ruleErrors > 0 {
		rep.Step(AddStepRules, StepWarn, fmt.Sprintf("%d failed", ruleErrors))
	} else {
		rep.Step(AddStepRules, StepDone, fmt.Sprintf("%d rules", len(aliases)))
	}

	// Step 4: catch-all, so mail to any address on the domain still arrives.
	rep.Step(AddStepCatchAll, StepRunning, "")
	catchAll := cloudflare.RoutingRule{
		Enabled:  true,
		Matchers: []cloudflare.RuleMatcher{{Type: "all"}},
		Actions:  []cloudflare.RuleAction{{Type: "forward", Value: []string{forwardTo}}},
	}
	if err := cf.UpdateCatchAllRule(zone.ID, catchAll); err != nil {
		rep.Step(AddStepCatchAll, StepWarn, err.Error())
	} else {
		rep.Step(AddStepCatchAll, StepDone, "*@"+domain+" → "+forwardTo)
	}

	// Steps 5–8: register with the sending provider and publish its DNS.
	if cfg.SendingProvider() == internal.ProviderResend {
		addResend(cf, resend.NewClient(cfg.ResendAPIKey), rep, addProviderSteps, zone.ID, domain, forwardTo, result)
	} else {
		addBrevo(cf, brevo.NewClient(cfg.BrevoAPIKey), rep, addProviderSteps, zone.ID, domain, forwardTo, aliases, result)
	}

	// Step 9: persist.
	rep.Step(AddStepSave, StepRunning, "")
	cfg.AddDomain(domain, result.ZoneID, result.Aliases)
	if d := cfg.FindDomain(domain); d != nil {
		d.ResendDomainID = result.ResendDomainID
		d.ManagedDNSRecordIDs = result.ManagedRecordIDs
	}
	if err := st.Save(cfg); err != nil {
		rep.Step(AddStepSave, StepFailed, err.Error())
		return result, err
	}
	rep.Step(AddStepSave, StepDone, "")

	return result, nil
}

// addResend registers the domain with Resend, publishes its DNS records, and
// triggers verification. Resend has no per-sender objects — any address on a
// verified domain can send — so the senders step is a no-op.
func addResend(cf *cloudflare.Client, rc *resend.Client, rep Reporter, steps providerSteps, zoneID, domain, forwardTo string, result *AddResult) {
	rep.Step(steps.Domain, StepRunning, "")
	rd, err := rc.AddDomain(domain)
	if err != nil {
		rep.Step(steps.Domain, StepWarn, "failed — receiving still works")
		rep.Note(steps.Domain, NoteWarn, err.Error())
		rep.Step(steps.DNS, StepWarn, "skipped")
		rep.Step(steps.Authenticate, StepWarn, "skipped")
		rep.Step(steps.Senders, StepDone, "n/a for Resend")
		return
	}
	result.ResendDomainID = rd.ID
	rep.Step(steps.Domain, StepDone, rd.ID)

	rep.Step(steps.DNS, StepRunning, "")
	for _, rec := range rd.Records {
		// A mailbox domain is always its own zone apex — add rejects subdomains —
		// so the domain is the right base for record names here.
		name := ResendRecordName(rec.Name, domain)
		cfRec := cloudflare.DNSRecord{
			Type:    rec.Type,
			Name:    name,
			Content: rec.Value,
			TTL:     3600,
		}
		if strings.EqualFold(rec.Type, "MX") && rec.Priority > 0 {
			prio := rec.Priority
			cfRec.Priority = &prio
		}
		id, err := cf.CreateDNSRecord(zoneID, cfRec)
		if err != nil {
			rep.Note(steps.DNS, NoteWarn, rec.Type+" "+name+" — "+err.Error())
			continue
		}
		result.recordManaged(id)
		rep.Note(steps.DNS, NoteOK, rec.Type+" "+name)
	}
	publishDMARC(cf, rep, steps.DNS, zoneID, domain, forwardTo, result)
	rep.Step(steps.DNS, StepDone, "")

	rep.Step(steps.Authenticate, StepRunning, "")
	time.Sleep(2 * time.Second)
	if err := rc.VerifyDomain(rd.ID); err != nil {
		rep.Step(steps.Authenticate, StepWarn, "pending — DNS may need time to propagate")
	} else {
		rep.Step(steps.Authenticate, StepDone, "")
	}

	rep.Step(steps.Senders, StepDone, "n/a for Resend")
}

// addBrevo registers the domain with Brevo, publishes its DNS records,
// authenticates it, and creates one sender per alias.
func addBrevo(cf *cloudflare.Client, bv *brevo.Client, rep Reporter, steps providerSteps, zoneID, domain, forwardTo string, aliases []string, result *AddResult) {
	rep.Step(steps.Domain, StepRunning, "")
	brevoDomain, err := bv.AddDomain(domain)
	if err != nil {
		rep.Step(steps.Domain, StepWarn, "failed — receiving still works")
		rep.Note(steps.Domain, NoteWarn, err.Error())
		rep.Step(steps.DNS, StepWarn, "skipped")
		rep.Step(steps.Authenticate, StepWarn, "skipped")
		rep.Step(steps.Senders, StepWarn, "skipped")
		return
	}
	rep.Step(steps.Domain, StepDone, "")

	rep.Step(steps.DNS, StepRunning, "")
	for _, rec := range brevoDomain.FlatDNSRecords() {
		name := brevo.FullRecordName(rec, domain)
		id, err := cf.CreateDNSRecord(zoneID, cloudflare.DNSRecord{
			Type:    rec.Type,
			Name:    name,
			Content: rec.Value,
			TTL:     3600,
		})
		if err != nil {
			rep.Note(steps.DNS, NoteWarn, rec.Type+" "+name+" — "+err.Error())
			continue
		}
		result.recordManaged(id)
		rep.Note(steps.DNS, NoteOK, rec.Type+" "+name)
	}
	publishDMARC(cf, rep, steps.DNS, zoneID, domain, forwardTo, result)
	rep.Step(steps.DNS, StepDone, "")

	rep.Step(steps.Authenticate, StepRunning, "")
	time.Sleep(2 * time.Second)
	if err := bv.AuthenticateDomain(domain); err != nil {
		rep.Step(steps.Authenticate, StepWarn, "pending — DNS may need time to propagate")
	} else {
		rep.Step(steps.Authenticate, StepDone, "")
	}

	rep.Step(steps.Senders, StepRunning, "")
	for _, alias := range aliases {
		addr := fmt.Sprintf("%s@%s", alias, domain)
		name := strings.ToUpper(alias[:1]) + alias[1:]
		if err := bv.CreateSender(name, addr); err != nil {
			rep.Note(steps.Senders, NoteWarn, addr+" — "+err.Error())
		} else {
			rep.Note(steps.Senders, NoteOK, addr)
		}
	}
	rep.Step(steps.Senders, StepDone, "")
}

// publishDMARC adds a starter DMARC policy as part of the DNS step.
func publishDMARC(cf *cloudflare.Client, rep Reporter, dnsStep int, zoneID, domain, forwardTo string, result *AddResult) {
	id, status, detail := EnsureDMARC(cf, zoneID, domain, forwardTo)
	name := DMARCName(domain)
	switch status {
	case DMARCCreated:
		result.recordManaged(id)
		rep.Note(dnsStep, NoteOK, "TXT "+name+" — "+detail)
	case DMARCExists:
		rep.Note(dnsStep, NoteOK, "TXT "+name+" — already set, left as is")
	case DMARCFailed:
		rep.Note(dnsStep, NoteWarn, "TXT "+name+" — "+detail)
	}
}

// recordManaged notes a Cloudflare record ID as mailctl's own, ignoring empty
// IDs from creates whose response could not be parsed.
func (r *AddResult) recordManaged(id string) {
	if id == "" {
		return
	}
	r.ManagedRecordIDs = append(r.ManagedRecordIDs, id)
}

// ResendRecordName normalizes a Resend DNS record name into a fully-qualified
// name for Cloudflare. Resend returns host-relative names (e.g. "send",
// "resend._domainkey"), but occasionally the FQDN; handle both.
func ResendRecordName(name, domain string) string {
	name = strings.TrimSuffix(name, ".")
	if name == "" || name == "@" {
		return domain
	}
	if name == domain || strings.HasSuffix(name, "."+domain) {
		return name
	}
	return name + "." + domain
}

// ValidateAdd checks everything that can be rejected without calling an API.
// Frontends call it before opening a progress display so input errors surface
// as plain messages rather than a failed first step.
func ValidateAdd(cfg *internal.Config, opts AddOptions) error {
	domain := strings.TrimSpace(opts.Domain)
	if !LooksLikeDomain(domain) {
		return fmt.Errorf("%q is not a domain name — pass the registered domain, e.g. %s.com", domain, domain)
	}
	if cfg.FindDomain(domain) != nil {
		return fmt.Errorf("domain %s is already configured — use 'mailctl remove %s' first", domain, domain)
	}
	if cfg.DefaultForwardTo == "" && opts.ForwardTo == "" {
		return fmt.Errorf("no forward-to address: set default_forward_to in config or pass one with --forward-to")
	}
	return nil
}

// providerSteps maps the four sending-provider stages onto progress indexes,
// so the same helpers can run inside the full add flow or standalone in
// register, where they start at zero.
type providerSteps struct {
	Domain       int
	DNS          int
	Authenticate int
	Senders      int
}

// addProviderSteps is the mapping used by AddDomain.
var addProviderSteps = providerSteps{
	Domain:       AddStepProviderDomain,
	DNS:          AddStepDNS,
	Authenticate: AddStepAuthenticate,
	Senders:      AddStepSenders,
}
