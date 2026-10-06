package cloudflare

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const baseURL = "https://api.cloudflare.com/client/v4"

type Client struct {
	token      string
	httpClient *http.Client
}

func NewClient(token string) *Client {
	return &Client{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// API response wrapper
type apiResponse struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Errors  []apiError      `json:"errors"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ErrZoneNotFound reports that no zone exists for a name. It is distinguishable
// so callers walking up a subdomain can tell "this label is not a zone" apart
// from "the API rejected us", and stop walking on the latter.
var ErrZoneNotFound = errors.New("no such zone")

// CodeDestinationNotVerified is Cloudflare refusing a routing rule because the
// address it forwards to has not been confirmed by its owner. Confirmation is a
// link Cloudflare mails to that address; there is no API to click it for them.
const CodeDestinationNotVerified = 2054

// APIError is a failed Cloudflare API call. It carries the machine-readable
// code so callers can branch on which failure this is, rather than matching on
// message text that Cloudflare is free to reword.
type APIError struct {
	// Code is Cloudflare's error code, or zero when the response carried none.
	Code int
	// Message is Cloudflare's human-readable explanation.
	Message string
	// Status is the HTTP status the call came back with.
	Status int
}

func (e *APIError) Error() string {
	if e.Code == 0 {
		return fmt.Sprintf("cloudflare API error (status %d)", e.Status)
	}
	return fmt.Sprintf("cloudflare API error: %s (code %d)", e.Message, e.Code)
}

// IsDestinationUnverified reports whether err is Cloudflare rejecting a rule
// whose destination address is still unconfirmed.
func IsDestinationUnverified(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == CodeDestinationNotVerified
}

// Zone types
type ZoneAccount struct {
	ID string `json:"id"`
}

type Zone struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Account ZoneAccount `json:"account"`
}

// Email routing types
type EmailRoutingStatus struct {
	Enabled bool `json:"enabled"`
}

type RoutingRule struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Enabled  bool          `json:"enabled"`
	Matchers []RuleMatcher `json:"matchers"`
	Actions  []RuleAction  `json:"actions"`
}

type RuleMatcher struct {
	Type  string `json:"type"`
	Field string `json:"field"`
	Value string `json:"value"`
}

type RuleAction struct {
	Type  string   `json:"type"`
	Value []string `json:"value"`
}

// DNS types
type DNSRecord struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Priority *int   `json:"priority,omitempty"`
}

func (c *Client) do(method, path string, body interface{}) (*apiResponse, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, baseURL+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response (status %d): %s", resp.StatusCode, string(respBody))
	}

	if !apiResp.Success {
		if len(apiResp.Errors) > 0 {
			return &apiResp, &APIError{
				Code:    apiResp.Errors[0].Code,
				Message: apiResp.Errors[0].Message,
				Status:  resp.StatusCode,
			}
		}
		return &apiResp, &APIError{Status: resp.StatusCode}
	}

	return &apiResp, nil
}

// GetZoneByName finds a zone by domain name.
func (c *Client) GetZoneByName(domain string) (*Zone, error) {
	resp, err := c.do("GET", "/zones?name="+url.QueryEscape(domain), nil)
	if err != nil {
		return nil, err
	}

	var zones []Zone
	if err := json.Unmarshal(resp.Result, &zones); err != nil {
		return nil, fmt.Errorf("parse zones: %w", err)
	}

	if len(zones) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrZoneNotFound, domain)
	}

	return &zones[0], nil
}

// EnableEmailRouting enables email routing for a zone.
// Returns nil on success or if already enabled.
func (c *Client) EnableEmailRouting(zoneID string) error {
	_, err := c.do("POST", fmt.Sprintf("/zones/%s/email/routing/enable", zoneID), nil)
	if err != nil {
		// Check if it's an "already enabled" type error — those are fine
		errStr := err.Error()
		if strings.Contains(errStr, "already") {
			return nil
		}
		return err
	}
	return nil
}

// GetEmailRoutingStatus checks if email routing is enabled.
func (c *Client) GetEmailRoutingStatus(zoneID string) (*EmailRoutingStatus, error) {
	resp, err := c.do("GET", fmt.Sprintf("/zones/%s/email/routing", zoneID), nil)
	if err != nil {
		return nil, err
	}

	var status EmailRoutingStatus
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		return nil, fmt.Errorf("parse routing status: %w", err)
	}
	return &status, nil
}

// ListRoutingRules lists all email routing rules for a zone.
func (c *Client) ListRoutingRules(zoneID string) ([]RoutingRule, error) {
	resp, err := c.do("GET", fmt.Sprintf("/zones/%s/email/routing/rules", zoneID), nil)
	if err != nil {
		return nil, err
	}

	var rules []RoutingRule
	if err := json.Unmarshal(resp.Result, &rules); err != nil {
		return nil, fmt.Errorf("parse rules: %w", err)
	}
	return rules, nil
}

// CreateRoutingRule creates an email routing rule.
func (c *Client) CreateRoutingRule(zoneID string, rule RoutingRule) error {
	_, err := c.do("POST", fmt.Sprintf("/zones/%s/email/routing/rules", zoneID), rule)
	return err
}

// UpdateRoutingRule updates an existing email routing rule.
func (c *Client) UpdateRoutingRule(zoneID, ruleID string, rule RoutingRule) error {
	_, err := c.do("PUT", fmt.Sprintf("/zones/%s/email/routing/rules/%s", zoneID, ruleID), rule)
	return err
}

// GetCatchAllRule gets the catch-all email routing rule.
func (c *Client) GetCatchAllRule(zoneID string) (*RoutingRule, error) {
	resp, err := c.do("GET", fmt.Sprintf("/zones/%s/email/routing/rules/catch_all", zoneID), nil)
	if err != nil {
		return nil, err
	}

	var rule RoutingRule
	if err := json.Unmarshal(resp.Result, &rule); err != nil {
		return nil, fmt.Errorf("parse catch-all rule: %w", err)
	}
	return &rule, nil
}

// UpdateCatchAllRule updates the catch-all email routing rule.
func (c *Client) UpdateCatchAllRule(zoneID string, rule RoutingRule) error {
	_, err := c.do("PUT", fmt.Sprintf("/zones/%s/email/routing/rules/catch_all", zoneID), rule)
	return err
}

// DeleteRoutingRule deletes an email routing rule.
func (c *Client) DeleteRoutingRule(zoneID, ruleID string) error {
	_, err := c.do("DELETE", fmt.Sprintf("/zones/%s/email/routing/rules/%s", zoneID, ruleID), nil)
	return err
}

// ListDNSRecords lists DNS records, optionally filtered by type.
func (c *Client) ListDNSRecords(zoneID, recordType string) ([]DNSRecord, error) {
	path := fmt.Sprintf("/zones/%s/dns_records", zoneID)
	if recordType != "" {
		path += "?type=" + url.QueryEscape(recordType)
	}

	resp, err := c.do("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var records []DNSRecord
	if err := json.Unmarshal(resp.Result, &records); err != nil {
		return nil, fmt.Errorf("parse DNS records: %w", err)
	}
	return records, nil
}

// CreateDNSRecord creates a DNS record and returns its Cloudflare record ID.
// The ID lets callers record exactly which records they created, so teardown
// can delete only those rather than guessing from record names.
func (c *Client) CreateDNSRecord(zoneID string, record DNSRecord) (string, error) {
	resp, err := c.do("POST", fmt.Sprintf("/zones/%s/dns_records", zoneID), record)
	if err != nil {
		return "", err
	}

	var created DNSRecord
	if err := json.Unmarshal(resp.Result, &created); err != nil {
		// The record exists; we just could not read its ID back.
		return "", nil
	}
	return created.ID, nil
}

// DeleteDNSRecord deletes a DNS record.
func (c *Client) DeleteDNSRecord(zoneID, recordID string) error {
	_, err := c.do("DELETE", fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID), nil)
	return err
}

// DestinationAddress represents an email routing destination.
type DestinationAddress struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Verified string `json:"verified"` // "2024-01-01T..." or empty
}

// ListDestinationAddresses lists verified destination addresses for an account.
func (c *Client) ListDestinationAddresses(accountID string) ([]DestinationAddress, error) {
	resp, err := c.do("GET", fmt.Sprintf("/accounts/%s/email/routing/addresses", accountID), nil)
	if err != nil {
		return nil, err
	}

	var addrs []DestinationAddress
	if err := json.Unmarshal(resp.Result, &addrs); err != nil {
		return nil, fmt.Errorf("parse destination addresses: %w", err)
	}
	return addrs, nil
}

// CreateDestinationAddress adds a destination email for email routing.
func (c *Client) CreateDestinationAddress(accountID, email string) error {
	body := map[string]string{"email": email}
	_, err := c.do("POST", fmt.Sprintf("/accounts/%s/email/routing/addresses", accountID), body)
	return err
}

// DeleteDestinationAddress removes a destination address from an account.
//
// Cloudflare exposes no endpoint for re-sending a verification email, so
// deleting an unconfirmed address and creating it again is how a resend is
// done. Deleting a confirmed address throws away a confirmation nobody can
// redo on the owner's behalf, so callers must check status before reaching
// for this.
func (c *Client) DeleteDestinationAddress(accountID, addressID string) error {
	_, err := c.do("DELETE", fmt.Sprintf("/accounts/%s/email/routing/addresses/%s", accountID, addressID), nil)
	return err
}
