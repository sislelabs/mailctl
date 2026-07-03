package resend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const baseURL = "https://api.resend.com"

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

// DNSRecord is one DNS entry Resend requires for domain authentication.
// Resend returns records with a "record" label (SPF/DKIM/…), a DNS "type"
// (TXT/MX/CNAME), a host "name", a "value", and an optional MX "priority".
type DNSRecord struct {
	Record   string `json:"record"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	Priority int    `json:"priority"`
	Status   string `json:"status"`
	TTL      string `json:"ttl"`
}

// Domain mirrors Resend's domain resource. Resend identifies domains by an
// opaque ID (UUID), not by name.
type Domain struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Status  string      `json:"status"`
	Region  string      `json:"region"`
	Records []DNSRecord `json:"records"`
}

type listDomainsResponse struct {
	Data []Domain `json:"data"`
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
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
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
		return nil, resp.StatusCode, fmt.Errorf("resend API error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return respBody, resp.StatusCode, nil
}

// AddDomain registers a domain with Resend and returns it, including the DNS
// records that must be created for authentication.
func (c *Client) AddDomain(domain string) (*Domain, error) {
	body := map[string]string{"name": domain}
	respBody, _, err := c.do("POST", "/domains", body)
	if err != nil {
		return nil, err
	}

	var d Domain
	if err := json.Unmarshal(respBody, &d); err != nil {
		return nil, fmt.Errorf("parse domain response: %w", err)
	}
	return &d, nil
}

// GetDomain fetches a domain by its Resend ID, including current record status.
func (c *Client) GetDomain(id string) (*Domain, error) {
	respBody, _, err := c.do("GET", "/domains/"+id, nil)
	if err != nil {
		return nil, err
	}

	var d Domain
	if err := json.Unmarshal(respBody, &d); err != nil {
		return nil, fmt.Errorf("parse domain response: %w", err)
	}
	return &d, nil
}

// VerifyDomain triggers Resend's DNS verification check for a domain ID.
func (c *Client) VerifyDomain(id string) error {
	_, _, err := c.do("POST", "/domains/"+id+"/verify", nil)
	return err
}

// ListDomains lists all registered domains.
func (c *Client) ListDomains() ([]Domain, error) {
	respBody, _, err := c.do("GET", "/domains", nil)
	if err != nil {
		return nil, err
	}

	var resp listDomainsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("parse domains response: %w", err)
	}
	return resp.Data, nil
}

// DeleteDomain deletes a domain by its Resend ID.
func (c *Client) DeleteDomain(id string) error {
	_, _, err := c.do("DELETE", "/domains/"+id, nil)
	return err
}

// FindDomainByName looks up a domain by name via the list endpoint, since
// Resend's config stores the ID but some callers only know the name.
func (c *Client) FindDomainByName(name string) (*Domain, error) {
	domains, err := c.ListDomains()
	if err != nil {
		return nil, err
	}
	for i := range domains {
		if strings.EqualFold(domains[i].Name, name) {
			return &domains[i], nil
		}
	}
	return nil, nil
}

// Authenticated reports whether the domain has completed verification.
func (d *Domain) Authenticated() bool {
	return strings.EqualFold(d.Status, "verified")
}

// Attachment is a file to include in a sent email.
type Attachment struct {
	Filename    string
	Content     []byte
	ContentType string
}

// SendParams describes an email to send via Resend's HTTP API.
type SendParams struct {
	From        string
	To          []string
	Subject     string
	Text        string
	HTML        string
	Attachments []Attachment
}

type sendAttachment struct {
	Filename    string `json:"filename"`
	Content     string `json:"content"`
	ContentType string `json:"content_type,omitempty"`
}

// SendEmail sends an email through Resend's HTTP API.
func (c *Client) SendEmail(p SendParams) error {
	body := map[string]interface{}{
		"from":    p.From,
		"to":      p.To,
		"subject": p.Subject,
	}
	if p.Text != "" {
		body["text"] = p.Text
	}
	if p.HTML != "" {
		body["html"] = p.HTML
	}
	// Resend requires at least one of text/html.
	if p.Text == "" && p.HTML == "" {
		body["text"] = ""
	}
	if len(p.Attachments) > 0 {
		atts := make([]sendAttachment, 0, len(p.Attachments))
		for _, a := range p.Attachments {
			atts = append(atts, sendAttachment{
				Filename:    a.Filename,
				Content:     base64Encode(a.Content),
				ContentType: a.ContentType,
			})
		}
		body["attachments"] = atts
	}

	_, _, err := c.do("POST", "/emails", body)
	return err
}
