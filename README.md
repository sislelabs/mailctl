# mailctl

CLI tool for managing custom domain email. Uses [Cloudflare Email Routing](https://developers.cloudflare.com/email-routing/) for receiving and [Brevo](https://brevo.com) for sending.

Set up professional email for any domain in one command — no Google Workspace, no Fastmail, no monthly fees.

![mailctl demo](assets/demo.gif)

## What it does

```
mailctl add yourdomain.com -a hello,support
```

That single command:
- Finds your Cloudflare zone
- Enables email routing with MX records
- Creates forwarding rules for each alias
- Enables catch-all forwarding
- Adds the domain to Brevo
- Creates SPF/DKIM DNS records
- Authenticates the domain for sending
- Creates sender addresses
- Prints Gmail Send-As setup instructions

## Install

```bash
go install github.com/sislelabs/mailctl@latest
```

Or build from source:

```bash
git clone https://github.com/sislelabs/mailctl.git
cd mailctl
go build .
```

## Quick Start

```bash
# Interactive setup — configures API keys
mailctl init

# Set up email for a domain
mailctl add yourdomain.com -a hello,support

# Launch the TUI dashboard
mailctl
```

## Commands

```bash
mailctl                                   # TUI dashboard
mailctl add yourdomain.com -a hello       # Full email setup
mailctl list                              # List domains with live status
mailctl check yourdomain.com              # Deep health check (routing, DNS, Brevo)
mailctl alias add yourdomain.com billing  # Add an alias
mailctl alias list yourdomain.com         # List aliases
mailctl alias catchall yourdomain.com on  # Enable catch-all forwarding
mailctl remove yourdomain.com             # Tear down everything
```

## TUI Dashboard

Run `mailctl` with no arguments for an interactive dashboard:

![mailctl TUI](assets/tui.gif)

- Browse domains with live status
- Inspect domain health checks
- Manage aliases inline
- Run flows with `f`

## Flows

Flows are composable YAML workflows. Drop a `.yaml` file in `~/.mailctl/flows/` and it's automatically discovered.

```bash
mailctl flow list                         # See available flows
mailctl flow run <name> [args] [--flags]  # Run a flow
mailctl flow new                          # Scaffold a new flow
```

Example flow:

```yaml
name: "welcome:send"
description: "Send welcome email to a new customer"
args:
  - name: email
    required: true
steps:
  - step: email.send
    args:
      to: "{{.args.email}}"
      subject: "Welcome!"
      body: "Thanks for signing up."
```

Available steps:
- `print` — display styled output
- `exit` — stop flow execution
- `confirm` — type-to-confirm prompt
- `prompt` — interactive input wizard
- `config.load` — load mailctl config
- `email.send` — send email with optional attachments

Flow control: `if`/`else` conditionals, `for_each` iteration.

## Config

Stored at `~/.mailctl.yaml` with `0600` permissions.

```yaml
cloudflare_api_token: "cfut_..."
cloudflare_account_id: "abc123..."

brevo_api_key: "xkeysib-..."
brevo_smtp_key: "xsmtpsib-..."
brevo_smtp_login: "xxx@smtp-brevo.com"

default_forward_to: "you@gmail.com"

# Managed by mailctl add/remove — don't edit by hand
domains:
  - domain: yourdomain.com
    cloudflare_zone_id: "..."
    aliases:
      - alias: hello
        forward_to: [you@gmail.com]

# For sending emails from flows
smtp:
  host: "smtp-relay.brevo.com"
  port: 587
  user: "xxx@smtp-brevo.com"
  pass: "xsmtpsib-..."
  default_from: "hello@yourdomain.com"
```

## Prerequisites

- **Go 1.22+** — [install Go](https://go.dev/dl/)
- **A domain on Cloudflare** — DNS must be managed by Cloudflare
- **A Brevo account** — free tier works, needed for SMTP sending

### Cloudflare API Token

Create one at [dash.cloudflare.com/profile/api-tokens](https://dash.cloudflare.com/profile/api-tokens) with these permissions:

| Permission | Access |
|---|---|
| Zone > DNS | Edit |
| Zone > Email Routing Rules | Edit |
| Zone > Email Routing Addresses | Edit |
| Account > Email Routing Addresses | Read |

### Gmail Send-As

After `mailctl add`, you can send from your custom domain in Gmail:

1. Open [Gmail Accounts settings](https://mail.google.com/mail/#settings/accounts)
2. Click "Add another email address"
3. Use the SMTP settings printed by `mailctl add`

## Troubleshooting

**Emails not arriving after `mailctl add`**

MX record propagation can take up to 15 minutes. Check with:
```bash
dig yourdomain.com MX +short
mailctl check yourdomain.com
```

**"Authentication error (code 10000)" from Cloudflare**

Your API token is missing permissions. See the table above.

**SMTP errors when sending from flows**

Check the `smtp` section in `~/.mailctl.yaml`. The `user` and `pass` fields use your Brevo SMTP credentials (different from the API key).

## License

MIT
