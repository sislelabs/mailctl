package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Alias struct {
	Alias     string   `yaml:"alias"`
	ForwardTo []string `yaml:"forward_to"`
}

type DomainConfig struct {
	Domain           string  `yaml:"domain"`
	CloudflareZoneID string  `yaml:"cloudflare_zone_id"`
	Aliases          []Alias `yaml:"aliases,omitempty"`
	AddedAt          string  `yaml:"added_at"`
	// ResendDomainID is Resend's opaque domain identifier (UUID). Resend
	// addresses domains by ID rather than name, so we persist it here for
	// later check/remove operations. Empty for Brevo-managed domains.
	ResendDomainID string `yaml:"resend_domain_id,omitempty"`
	// ManagedDNSRecordIDs are the Cloudflare record IDs mailctl itself created
	// for this domain. Teardown deletes exactly these, so DKIM and DMARC
	// records belonging to other mail services on the same zone are never
	// touched. Empty for domains added before mailctl tracked ownership.
	ManagedDNSRecordIDs []string `yaml:"managed_dns_record_ids,omitempty"`
}

type SMTPConfig struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	User        string `yaml:"user"`
	Pass        string `yaml:"pass"`
	DefaultFrom string `yaml:"default_from"`
}

// Sending providers supported for domain authentication and email sending.
const (
	ProviderBrevo  = "brevo"
	ProviderResend = "resend"
)

type Config struct {
	CloudflareAPIToken  string `yaml:"cloudflare_api_token"`
	CloudflareAccountID string `yaml:"cloudflare_account_id"`
	// Provider selects the sending provider: "resend" (default) or "brevo".
	Provider         string         `yaml:"provider,omitempty"`
	BrevoAPIKey      string         `yaml:"brevo_api_key,omitempty"`
	BrevoSMTPKey     string         `yaml:"brevo_smtp_key,omitempty"`
	BrevoSMTPLogin   string         `yaml:"brevo_smtp_login,omitempty"`
	ResendAPIKey     string         `yaml:"resend_api_key,omitempty"`
	DefaultForwardTo string         `yaml:"default_forward_to"`
	Domains          []DomainConfig `yaml:"domains,omitempty"`
	SMTP             *SMTPConfig    `yaml:"smtp,omitempty"`
}

// SendingProvider returns the configured provider. Resend is the default, but
// a config that names no provider while carrying Brevo credentials and no
// Resend key was written before Resend support existed, so it stays on Brevo
// rather than being switched to a provider it has no key for.
func (c *Config) SendingProvider() string {
	switch c.Provider {
	case ProviderResend:
		return ProviderResend
	case ProviderBrevo:
		return ProviderBrevo
	}
	if c.ResendAPIKey == "" && c.hasBrevoCredentials() {
		return ProviderBrevo
	}
	return ProviderResend
}

func (c *Config) hasBrevoCredentials() bool {
	return c.BrevoAPIKey != "" || c.BrevoSMTPKey != "" || c.BrevoSMTPLogin != ""
}

func ConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".mailctl.yaml"
	}
	return filepath.Join(home, ".mailctl.yaml")
}

func LoadConfig() (*Config, error) {
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config at %s: %w\nRun 'mailctl init' to create one", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	path := ConfigPath()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

func (c *Config) FindDomain(domain string) *DomainConfig {
	for i := range c.Domains {
		if c.Domains[i].Domain == domain {
			return &c.Domains[i]
		}
	}
	return nil
}

func (c *Config) AddDomain(domain, zoneID string, aliases []Alias) {
	c.Domains = append(c.Domains, DomainConfig{
		Domain:           domain,
		CloudflareZoneID: zoneID,
		Aliases:          aliases,
		AddedAt:          time.Now().UTC().Format(time.RFC3339),
	})
}

func (c *Config) RemoveDomain(domain string) {
	for i, d := range c.Domains {
		if d.Domain == domain {
			c.Domains = append(c.Domains[:i], c.Domains[i+1:]...)
			return
		}
	}
}

func (d *DomainConfig) FindAlias(name string) *Alias {
	for i := range d.Aliases {
		if d.Aliases[i].Alias == name {
			return &d.Aliases[i]
		}
	}
	return nil
}

func (d *DomainConfig) AddAlias(name string, forwardTo []string) {
	d.Aliases = append(d.Aliases, Alias{
		Alias:     name,
		ForwardTo: forwardTo,
	})
}

func (d *DomainConfig) RemoveAlias(name string) {
	for i, a := range d.Aliases {
		if a.Alias == name {
			d.Aliases = append(d.Aliases[:i], d.Aliases[i+1:]...)
			return
		}
	}
}

func MaskAPIKey(key string) string {
	if len(key) <= 6 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// ApplySMTPDefaults fills in the smtp block that flows read from. Without a
// default_from, `mailctl flow run` fails with "no sender address" on a setup
// that otherwise completed cleanly. For Brevo the relay details are derivable
// from credentials already in the config, so they are filled in too rather
// than left for the user to discover.
func (c *Config) ApplySMTPDefaults(defaultFrom string) {
	needsBrevoRelay := c.SendingProvider() == ProviderBrevo &&
		c.BrevoSMTPLogin != "" && c.BrevoSMTPKey != ""

	if defaultFrom == "" && !needsBrevoRelay {
		return
	}

	if c.SMTP == nil {
		c.SMTP = &SMTPConfig{}
	}
	if defaultFrom != "" {
		c.SMTP.DefaultFrom = defaultFrom
	}
	if needsBrevoRelay {
		if c.SMTP.Host == "" {
			c.SMTP.Host = "smtp-relay.brevo.com"
		}
		if c.SMTP.Port == 0 {
			c.SMTP.Port = 587
		}
		if c.SMTP.User == "" {
			c.SMTP.User = c.BrevoSMTPLogin
		}
		if c.SMTP.Pass == "" {
			c.SMTP.Pass = c.BrevoSMTPKey
		}
	}
}
