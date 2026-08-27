package mailsetup

import (
	"fmt"
	"strings"
	"time"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/cloudflare"
	"github.com/sislelabs/mailctl/internal/resend"
)

// Stage is how much sending reputation a domain has had the chance to build.
type Stage int

const (
	// StageNoHistory means the domain has never sent. Correct DNS proves a
	// message is not forged; it says nothing about whether a receiver wants
	// it, and a domain with no history is judged on nothing else.
	StageNoHistory Stage = iota
	// StageWarming means it has started sending but too recently, or in too
	// little volume, for receivers to have formed a view.
	StageWarming
	// StageEstablished means it has a track record.
	StageEstablished
)

func (s Stage) String() string {
	switch s {
	case StageNoHistory:
		return "no sending history"
	case StageWarming:
		return "warming up"
	default:
		return "established"
	}
}

// scanLimit bounds how far back the send history is counted. Enough to tell a
// cold domain from a busy one without paging through everything.
const scanLimit = 200

// establishedSends and establishedAge are the thresholds past which a domain
// has plausibly built a reputation. They are judgement calls, not thresholds
// any provider publishes.
const (
	establishedSends = 50
	establishedAge   = 14 * 24 * time.Hour
)

// Deliverability is the reputation picture for one domain: what it has sent,
// for how long, and how that mail fared.
type Deliverability struct {
	Domain string
	Stage  Stage

	// RegisteredAt is when the sending provider first knew the domain.
	RegisteredAt time.Time
	// Age is how long the domain has been sending, falling back to how long the
	// provider has known it when there is nothing sent to measure.
	Age time.Duration

	Sent       int
	Delivered  int
	Bounced    int
	Complained int
	// FirstSent and LastSent bound the span over which the domain has actually
	// been sending, which is what receivers have had to judge. The provider
	// registration date is not: re-registering a domain resets it while the
	// reputation at receivers carries on.
	FirstSent time.Time
	LastSent  time.Time
	// Capped reports that the scan hit its limit, so Sent is a floor.
	Capped bool

	// DMARCPolicy is none, quarantine or reject; empty when unpublished.
	DMARCPolicy  string
	DMARCReports bool
}

// BounceRate is the share of scanned mail that bounced.
func (d *Deliverability) BounceRate() float64 {
	if d.Sent == 0 {
		return 0
	}
	return float64(d.Bounced) / float64(d.Sent)
}

// Advice is what to do next, in priority order. Empty when nothing is wrong.
//
// It derives the stage rather than reading the field, so advice cannot
// disagree with the numbers it is drawn from just because a caller built the
// struct without computing Stage first.
func (d *Deliverability) Advice() []string {
	var out []string

	switch stageFor(d) {
	case StageNoHistory:
		out = append(out,
			"This domain has never sent mail. Its first messages are judged with no reputation behind them, so filtering is normal — expect spam placement until it has a track record.",
			"Send low volume to people who will open and reply. A reply from a recipient is the strongest positive signal there is.",
			"Use an established domain for anything that must land while this one warms up.")
	case StageWarming:
		out = append(out,
			"Too little history for receivers to have formed a view. Keep volume low and steady rather than sending in bursts.")
	}

	if d.DMARCPolicy == "" {
		out = append(out, "No DMARC policy published — receivers have no instruction for messages that fail authentication.")
	} else if !d.DMARCReports {
		out = append(out, "DMARC publishes no rua address, so no aggregate reports are collected and real-world pass rates stay invisible.")
	}

	if d.Sent > 20 && d.BounceRate() > 0.02 {
		out = append(out, fmt.Sprintf("Bounce rate is %.1f%% of recent mail. Above about 2%% damages reputation directly — clean the recipient list.", d.BounceRate()*100))
	}
	if d.Complained > 0 {
		out = append(out, fmt.Sprintf("%d recipient(s) marked mail as spam. Complaints weigh far more than volume.", d.Complained))
	}

	return out
}

// Reputation gathers the deliverability picture for a domain.
//
// It exists because correct DNS and good placement are different questions.
// A domain can hold verified SPF, DKIM and DMARC and still be filtered, and
// reporting it as healthy on the strength of its records alone is how a
// setup looks finished while its mail goes to spam.
func Reputation(cfg *internal.Config, d *internal.DomainConfig) (*Deliverability, error) {
	if cfg.SendingProvider() != internal.ProviderResend {
		return nil, fmt.Errorf("reputation is only implemented for Resend")
	}
	if cfg.ResendAPIKey == "" {
		return nil, fmt.Errorf("no resend_api_key in config")
	}

	out := &Deliverability{Domain: d.Domain}
	rc := resend.NewClient(cfg.ResendAPIKey)

	var rd *resend.Domain
	var err error
	if d.ResendDomainID != "" {
		rd, err = rc.GetDomain(d.ResendDomainID)
	} else {
		rd, err = rc.FindDomainByName(d.Domain)
	}
	if err != nil {
		return nil, err
	}
	if rd == nil {
		return nil, fmt.Errorf("%s is not registered with Resend", d.Domain)
	}
	if t := resend.ParseTime(rd.CreatedAt); !t.IsZero() {
		out.RegisteredAt = t
	}

	emails, err := rc.ListEmailsN(scanLimit)
	if err != nil {
		return nil, err
	}
	out.Capped = len(emails) >= scanLimit

	suffix := "@" + strings.ToLower(d.Domain)
	for _, e := range emails {
		if !strings.Contains(strings.ToLower(e.From), suffix) {
			continue
		}
		out.Sent++
		switch strings.ToLower(e.LastEvent) {
		case "delivered", "opened", "clicked":
			out.Delivered++
		case "bounced", "failed":
			out.Bounced++
		case "complained":
			out.Complained++
		}
		if t := resend.ParseTime(e.CreatedAt); !t.IsZero() {
			if t.After(out.LastSent) {
				out.LastSent = t
			}
			if out.FirstSent.IsZero() || t.Before(out.FirstSent) {
				out.FirstSent = t
			}
		}
	}

	// Prefer the span of real sending. Fall back to the registration date only
	// when there is nothing sent to measure.
	switch {
	case !out.FirstSent.IsZero():
		out.Age = time.Since(out.FirstSent)
	case !out.RegisteredAt.IsZero():
		out.Age = time.Since(out.RegisteredAt)
	}

	out.DMARCPolicy, out.DMARCReports = readDMARC(cfg, d)
	out.Stage = stageFor(out)

	return out, nil
}

func stageFor(d *Deliverability) Stage {
	switch {
	case d.Sent == 0:
		return StageNoHistory
	case d.Sent < establishedSends || (d.Age > 0 && d.Age < establishedAge):
		return StageWarming
	default:
		return StageEstablished
	}
}

// readDMARC returns the domain's DMARC policy and whether it collects reports.
func readDMARC(cfg *internal.Config, d *internal.DomainConfig) (policy string, reports bool) {
	cf := cloudflare.NewClient(cfg.CloudflareAPIToken)
	records, err := cf.ListDNSRecords(d.CloudflareZoneID, "TXT")
	if err != nil {
		return "", false
	}

	name := DMARCName(d.Domain)
	for _, rec := range records {
		if !strings.EqualFold(rec.Name, name) {
			continue
		}
		content := strings.ToLower(rec.Content)
		reports = strings.Contains(content, "rua=")
		for _, p := range []string{"reject", "quarantine", "none"} {
			if strings.Contains(content, "p="+p) {
				return p, reports
			}
		}
		return "unknown", reports
	}
	return "", false
}
