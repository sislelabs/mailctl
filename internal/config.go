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
	// Provider selects the sending provider: "brevo" (default) or "resend".
	Provider         string `yaml:"provider,omitempty"`
	BrevoAPIKey      string `yaml:"brevo_api_key,omitempty"`
	BrevoSMTPKey     string `yaml:"brevo_smtp_key,omitempty"`
	BrevoSMTPLogin   string `yaml:"brevo_smtp_login,omitempty"`
	ResendAPIKey     string `yaml:"resend_api_key,omitempty"`
	DefaultForwardTo string `yaml:"default_forward_to"`
	Domains          []DomainConfig `yaml:"domains,omitempty"`
	SMTP             *SMTPConfig    `yaml:"smtp,omitempty"`
}

// SendingProvider returns the configured provider, defaulting to Brevo so
// that existing configs written before Resend support keep working.
func (c *Config) SendingProvider() string {
	if c.Provider == ProviderResend {
		return ProviderResend
	}
	return ProviderBrevo
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
