package mailsetup

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal/cloudflare"
)

// ZoneMatch is the Cloudflare zone that holds a domain name.
type ZoneMatch struct {
	Zone *cloudflare.Zone
	// Name is the domain that was looked up.
	Name string
	// IsSubdomain reports whether Name sits below the zone's apex.
	IsSubdomain bool
}

// ZoneName is the apex of the matched zone.
func (m *ZoneMatch) ZoneName() string {
	if m.Zone == nil {
		return ""
	}
	return m.Zone.Name
}

// ResolveZone finds the Cloudflare zone holding a domain name, walking up the
// labels when the name is not a zone itself.
//
// A sending subdomain like info.example.com is not registered as its own zone;
// its records live in example.com. Looking up only the full name reports "no
// zone found" for a domain that is perfectly well hosted, which is what made
// sending subdomains impossible to set up.
//
// The walk stops at the last two labels: nothing shorter can be a registrable
// domain, and querying a bare TLD is pointless.
func ResolveZone(cf *cloudflare.Client, name string) (*ZoneMatch, error) {
	name = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(name, ".")))
	if name == "" {
		return nil, fmt.Errorf("no domain given")
	}

	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return nil, fmt.Errorf("%q is not a domain name", name)
	}

	for i := 0; i+2 <= len(labels); i++ {
		candidate := strings.Join(labels[i:], ".")
		zone, err := cf.GetZoneByName(candidate)
		if err == nil {
			return &ZoneMatch{
				Zone:        zone,
				Name:        name,
				IsSubdomain: !strings.EqualFold(candidate, name),
			}, nil
		}
		// Only a missing zone justifies trying a shorter name. An auth or
		// network failure would otherwise be retried once per label and then
		// reported as "not found", hiding the real cause.
		if !errors.Is(err, cloudflare.ErrZoneNotFound) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("no Cloudflare zone holds %s — is the domain added to Cloudflare?", name)
}

// ZoneCandidates lists the names ResolveZone would try, in order. Exposed for
// tests and for explaining a lookup failure.
func ZoneCandidates(name string) []string {
	name = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(name, ".")))
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return nil
	}

	var out []string
	for i := 0; i+2 <= len(labels); i++ {
		out = append(out, strings.Join(labels[i:], "."))
	}
	return out
}
