package mailsetup

import (
	"fmt"

	"github.com/sislelabs/mailctl/internal"
)

// GmailSendAs holds everything needed to add a domain address to Gmail's
// "Send mail as", so mail can be sent from it without leaving Gmail.
type GmailSendAs struct {
	// Provider is the display name of the sending provider.
	Provider string
	Host     string
	Port     int
	Username string
	// Password is the secret Gmail wants. Callers decide whether to show it.
	Password string
	// PasswordHint describes the secret when it is not being shown.
	PasswordHint string
	// Addresses are the domain addresses that can be added, read live so a
	// rule added outside mailctl is offered too.
	Addresses []GmailAddress
	// AddressesError explains why the address list is empty, when reading it
	// failed. The steps and SMTP settings are still correct without it.
	AddressesError string
}

// GmailAddress is one address and where its mail currently lands, which is
// where Gmail's confirmation code will arrive.
type GmailAddress struct {
	Address    string
	DeliversTo []string
}

// GmailSendAsFor builds the Gmail setup details for a domain.
//
// A sending domain has no addresses to add: Gmail's flow confirms ownership by
// emailing a code to the address, and a domain that cannot receive can never
// complete it.
func GmailSendAsFor(cfg *internal.Config, d *internal.DomainConfig) (*GmailSendAs, error) {
	if d.IsSending() {
		return nil, fmt.Errorf("%s is a sending domain — it cannot receive Gmail's confirmation code, so it cannot be added to Send mail as", d.Domain)
	}

	out := &GmailSendAs{Provider: ProviderLabel(cfg), Port: 587}

	switch cfg.SendingProvider() {
	case internal.ProviderResend:
		if cfg.ResendAPIKey == "" {
			return nil, fmt.Errorf("no resend_api_key in config — run 'mailctl init'")
		}
		out.Host = "smtp.resend.com"
		out.Username = "resend"
		out.Password = cfg.ResendAPIKey
		out.PasswordHint = "your Resend API key"
	default:
		if cfg.BrevoSMTPLogin == "" || cfg.BrevoSMTPKey == "" {
			return nil, fmt.Errorf("no Brevo SMTP credentials in config — run 'mailctl init'")
		}
		out.Host = "smtp-relay.brevo.com"
		out.Username = cfg.BrevoSMTPLogin
		out.Password = cfg.BrevoSMTPKey
		out.PasswordHint = "your Brevo SMTP key"
	}

	// Read addresses live: config is a cache, and an address added in the
	// Cloudflare dashboard is just as usable from Gmail. Failing to read them
	// is not fatal — the steps and SMTP settings are what people come for.
	views, err := ListAliases(cfg, d)
	if err != nil {
		out.AddressesError = err.Error()
		return out, nil
	}
	for _, v := range views {
		out.Addresses = append(out.Addresses, GmailAddress{
			Address:    v.Address,
			DeliversTo: v.ForwardTo,
		})
	}

	return out, nil
}

// MaskedPassword returns the password with its middle hidden.
func (g *GmailSendAs) MaskedPassword() string {
	return internal.MaskAPIKey(g.Password)
}
