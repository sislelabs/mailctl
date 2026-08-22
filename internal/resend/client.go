package resend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	// CreatedAt is when the domain was registered with Resend, which bounds
	// how much sending reputation it can possibly have.
	CreatedAt string `json:"created_at"`
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
	// Resend requires at least one of text/html and rejects an empty string
	// for both with a 422, so fail locally with a clearer message.
	if p.Text == "" && p.HTML == "" {
		return fmt.Errorf("email must have a text or html body")
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

// EmailSummary is one entry from the sent-email list. Resend returns
// references rather than full messages here; bodies require a per-id fetch.
type EmailSummary struct {
	ID        string   `json:"id"`
	MessageID string   `json:"message_id"`
	To        []string `json:"to"`
	From      string   `json:"from"`
	CreatedAt string   `json:"created_at"`
	Subject   string   `json:"subject"`
	BCC       []string `json:"bcc"`
	CC        []string `json:"cc"`
	ReplyTo   []string `json:"reply_to"`
	// LastEvent is the most recent delivery event: sent, delivered, bounced,
	// complained, opened, clicked, delivery_delayed.
	LastEvent   string  `json:"last_event"`
	ScheduledAt *string `json:"scheduled_at"`
}

type listEmailsResponse struct {
	Data    []EmailSummary `json:"data"`
	HasMore bool           `json:"has_more"`
}

// maxEmailPageSize is Resend's per-request ceiling for GET /emails.
const maxEmailPageSize = 100

// ListEmails retrieves one page of sent emails, newest first. after is a
// cursor: pass the ID of the last email from the previous page to continue.
//
// This covers outbound mail only. Resend has no view of messages *received* at
// the domain — inbound is handled entirely by Cloudflare Email Routing, which
// exposes no delivery-history API.
func (c *Client) ListEmails(limit int, after string) ([]EmailSummary, bool, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > maxEmailPageSize {
		limit = maxEmailPageSize
	}

	path := fmt.Sprintf("/emails?limit=%d", limit)
	if after != "" {
		path += "&after=" + url.QueryEscape(after)
	}

	respBody, _, err := c.do("GET", path, nil)
	if err != nil {
		return nil, false, err
	}

	var resp listEmailsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, false, fmt.Errorf("parse emails response: %w", err)
	}
	return resp.Data, resp.HasMore, nil
}

// ListEmailsN retrieves up to total sent emails, paging past Resend's
// per-request ceiling as needed.
func (c *Client) ListEmailsN(total int) ([]EmailSummary, error) {
	if total <= 0 {
		total = 20
	}

	var (
		all    []EmailSummary
		cursor string
	)
	for len(all) < total {
		page, hasMore, err := c.ListEmails(total-len(all), cursor)
		if err != nil {
			return all, err
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		if !hasMore {
			break
		}
		cursor = page[len(page)-1].ID
	}

	if len(all) > total {
		all = all[:total]
	}
	return all, nil
}

// timeLayouts are the shapes Resend timestamps arrive in.
var timeLayouts = []string{
	"2006-01-02 15:04:05.999999-07",
	"2006-01-02 15:04:05.999999+00",
	"2006-01-02T15:04:05.999Z",
	time.RFC3339,
}

// ParseTime reads a Resend timestamp, returning the zero time when it cannot
// be understood so callers can treat it as "unknown" rather than fail.
func ParseTime(raw string) time.Time {
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}
