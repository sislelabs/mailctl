package brevo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const baseURL = "https://api.brevo.com/v3"

type Client struct {
	apiKey     string
	httpClient *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type DNSRecord struct {
	Type     string `json:"type"`
	HostName string `json:"host_name"`
	Value    string `json:"value"`
	Status   bool   `json:"status"`
}

type DNSRecords struct {
	DKIM1Record DNSRecord `json:"dkim1Record"`
	DKIM2Record DNSRecord `json:"dkim2Record"`
	BrevoCode   DNSRecord `json:"brevo_code"`
	DMARCRecord DNSRecord `json:"dmarc_record"`
}

type Domain struct {
	ID             string     `json:"id"`
	DomainName     string     `json:"domain_name"`
	DomainProvider string     `json:"domain_provider"`
	Verified       bool       `json:"verified"`
	Authenticated  bool       `json:"authenticated"`
	DNSRecords     DNSRecords `json:"dns_records"`
	Message        string     `json:"message"`
}

type listDomainsResponse struct {
	Domains []Domain `json:"domains"`
}

func (c *Client) do(method, path string, body interface{}) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, baseURL+path, reqBody)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, fmt.Errorf("brevo API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, resp.StatusCode, nil
}

// AddDomain registers a domain with Brevo.
func (c *Client) AddDomain(domain string) (*Domain, error) {
	body := map[string]string{"name": domain}
	respBody, _, err := c.do("POST", "/senders/domains", body)
	if err != nil {
		return nil, err
	}

	var d Domain
	if err := json.Unmarshal(respBody, &d); err != nil {
		return nil, fmt.Errorf("parse domain response: %w", err)
	}
	return &d, nil
}

// GetDomain gets domain details including DNS record verification status.
func (c *Client) GetDomain(domainName string) (*Domain, error) {
	respBody, _, err := c.do("GET", fmt.Sprintf("/senders/domains/%s", domainName), nil)
	if err != nil {
		return nil, err
	}

	var d Domain
	if err := json.Unmarshal(respBody, &d); err != nil {
		return nil, fmt.Errorf("parse domain response: %w", err)
	}
	return &d, nil
}

// AuthenticateDomain triggers domain authentication check.
func (c *Client) AuthenticateDomain(domainName string) error {
	_, _, err := c.do("PUT", fmt.Sprintf("/senders/domains/%s/authenticate", domainName), nil)
	return err
}

// ListDomains lists all registered domains.
func (c *Client) ListDomains() ([]Domain, error) {
	respBody, _, err := c.do("GET", "/senders/domains", nil)
	if err != nil {
		return nil, err
	}

	var resp listDomainsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("parse domains response: %w", err)
	}
	return resp.Domains, nil
}

// DeleteDomain deletes a domain.
func (c *Client) DeleteDomain(domainName string) error {
	_, _, err := c.do("DELETE", fmt.Sprintf("/senders/domains/%s", domainName), nil)
	return err
}

// CreateSender registers a sender email in Brevo.
func (c *Client) CreateSender(name, email string) error {
	body := map[string]string{"name": name, "email": email}
	_, _, err := c.do("POST", "/senders", body)
	return err
}

// FlatDNSRecords returns the DNS records as a flat slice for easier iteration.
func (d *Domain) FlatDNSRecords() []DNSRecord {
	return []DNSRecord{
		d.DNSRecords.DKIM1Record,
		d.DNSRecords.DKIM2Record,
		d.DNSRecords.BrevoCode,
		d.DNSRecords.DMARCRecord,
	}
}

// FullRecordName returns the full DNS record name for a given domain.
// Brevo uses "@" for root, and short names like "brevo1._domainkey".
func FullRecordName(rec DNSRecord, domain string) string {
	h := rec.HostName
	if h == "" || h == "@" {
		return domain
	}
	return h + "." + domain
}
