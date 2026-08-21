package cloudflare

import (
	"encoding/json"
	"fmt"
)

// Permission names as they appear in the Cloudflare API token editor. These are
// the strings users have to match in the dashboard, so they are spelled exactly
// as the UI spells them.
const (
	PermZoneRead         = "Zone > Zone > Read"
	PermZoneDNS          = "Zone > DNS > Edit"
	PermZoneSettings     = "Zone > Zone Settings > Edit"
	PermRoutingRules     = "Zone > Email Routing Rules > Edit"
	PermRoutingAddresses = "Account > Email Routing Addresses > Edit"
)

// CheckResult is the outcome of probing one API surface that mailctl depends on.
type CheckResult struct {
	// Name is what the check covers, in user terms.
	Name string
	// OK reports whether the probe succeeded.
	OK bool
	// Detail carries the API error when OK is false.
	Detail string
	// Permission is the token permission that grants this surface.
	Permission string
	// Fatal marks surfaces without which `mailctl add` cannot work at all.
	// Non-fatal failures degrade the run but leave email working.
	Fatal bool
}

// VerifyToken checks that the API token itself is valid and active, independent
// of what it is allowed to do.
func (c *Client) VerifyToken() error {
	resp, err := c.do("GET", "/user/tokens/verify", nil)
	if err != nil {
		return err
	}

	var status struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		return nil // Token verified; we just could not read the status field.
	}
	if status.Status != "" && status.Status != "active" {
		return fmt.Errorf("token status is %q, expected \"active\"", status.Status)
	}
	return nil
}

// ListZones returns every zone the token can see.
func (c *Client) ListZones() ([]Zone, error) {
	resp, err := c.do("GET", "/zones?per_page=50", nil)
	if err != nil {
		return nil, err
	}

	var zones []Zone
	if err := json.Unmarshal(resp.Result, &zones); err != nil {
		return nil, fmt.Errorf("parse zones: %w", err)
	}
	return zones, nil
}

// Preflight probes each Cloudflare API surface mailctl uses and reports which
// are reachable with the current token. Probes are read-only: a successful read
// proves the token holds at least Read on that surface, and Cloudflare's Edit
// implies Read, so a failure is conclusive while a success is a strong signal.
//
// zoneName may be empty, in which case the first visible zone is used for the
// zone-scoped probes. accountID may be empty, in which case it is taken from
// the probed zone.
func (c *Client) Preflight(zoneName, accountID string) []CheckResult {
	var results []CheckResult

	// 1. Is the token itself valid?
	tokenCheck := CheckResult{
		Name:       "API token is valid",
		Permission: "",
		Fatal:      true,
	}
	if err := c.VerifyToken(); err != nil {
		tokenCheck.Detail = err.Error()
		results = append(results, tokenCheck)
		// Nothing else can succeed if the token itself is rejected.
		return results
	}
	tokenCheck.OK = true
	results = append(results, tokenCheck)

	// 2. Can we see zones? Everything zone-scoped depends on this.
	zoneCheck := CheckResult{
		Name:       "Read zones",
		Permission: PermZoneRead,
		Fatal:      true,
	}
	var zone *Zone
	if zoneName != "" {
		z, err := c.GetZoneByName(zoneName)
		if err != nil {
			zoneCheck.Detail = err.Error()
		} else {
			zone = z
			zoneCheck.OK = true
		}
	} else {
		zones, err := c.ListZones()
		if err != nil {
			zoneCheck.Detail = err.Error()
		} else if len(zones) == 0 {
			zoneCheck.Detail = "token can see no zones — check Zone Resources on the token"
		} else {
			zone = &zones[0]
			zoneCheck.OK = true
		}
	}
	results = append(results, zoneCheck)

	if zone == nil {
		return results
	}
	if accountID == "" {
		accountID = zone.Account.ID
	}

	// 3. Zone-scoped surfaces.
	results = append(results, c.probe(
		"Read DNS records",
		PermZoneDNS,
		true,
		fmt.Sprintf("/zones/%s/dns_records?per_page=1", zone.ID),
	))
	results = append(results, c.probe(
		"Read email routing settings",
		PermZoneSettings,
		false, // add() warns and continues when routing is already enabled
		fmt.Sprintf("/zones/%s/email/routing", zone.ID),
	))
	results = append(results, c.probe(
		"Read email routing rules",
		PermRoutingRules,
		true,
		fmt.Sprintf("/zones/%s/email/routing/rules", zone.ID),
	))

	// 4. Account-scoped surface. Destination addresses live on the account, not
	// the zone — the permission is not offered in the Zone dropdown at all.
	addrCheck := CheckResult{
		Name:       "Read destination addresses",
		Permission: PermRoutingAddresses,
		Fatal:      true,
	}
	if accountID == "" {
		addrCheck.Detail = "no account ID available to probe"
	} else if _, err := c.do("GET", fmt.Sprintf("/accounts/%s/email/routing/addresses", accountID), nil); err != nil {
		addrCheck.Detail = err.Error()
	} else {
		addrCheck.OK = true
	}
	results = append(results, addrCheck)

	return results
}

func (c *Client) probe(name, permission string, fatal bool, path string) CheckResult {
	result := CheckResult{Name: name, Permission: permission, Fatal: fatal}
	if _, err := c.do("GET", path, nil); err != nil {
		result.Detail = err.Error()
		return result
	}
	result.OK = true
	return result
}

// PreflightFailures returns only the checks that did not pass.
func PreflightFailures(results []CheckResult) []CheckResult {
	var failed []CheckResult
	for _, r := range results {
		if !r.OK {
			failed = append(failed, r)
		}
	}
	return failed
}

// HasFatalFailure reports whether any failed check blocks `mailctl add`.
func HasFatalFailure(results []CheckResult) bool {
	for _, r := range results {
		if !r.OK && r.Fatal {
			return true
		}
	}
	return false
}
