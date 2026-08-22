# mailctl

CLI tool for managing custom domain email. Uses [Cloudflare Email Routing](https://developers.cloudflare.com/email-routing/) for receiving and either [Resend](https://resend.com) or [Brevo](https://brevo.com) for sending.

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
- Adds the domain to your sending provider (Resend or Brevo)
- Creates SPF/DKIM DNS records
- Publishes a starter DMARC policy so mail is not filtered as spam
- Authenticates the domain for sending
- Creates sender addresses (Brevo)
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
mailctl check yourdomain.com              # Deep health check (routing, DNS, sending provider)
mailctl doctor                            # Check the Cloudflare token has every permission
mailctl gmail yourdomain.com              # Steps to send from this domain in Gmail
mailctl sync --dry-run                    # Show where config disagrees with live routing
mailctl sync                              # Adopt live Cloudflare routing into config
mailctl register yourdomain.com           # Register an existing domain with the sending provider
mailctl serve                             # Local web panel
mailctl logs                              # Recently sent email + delivery status
mailctl logs -d yourdomain.com -s bounced # Filter the send log
mailctl alias add yourdomain.com billing  # Add an alias
mailctl alias list yourdomain.com         # List aliases
mailctl alias catchall yourdomain.com on  # Enable catch-all forwarding
mailctl remove yourdomain.com --dry-run   # Show what teardown would delete
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

# Sending provider: "resend" (default) or "brevo"
provider: "resend"

# Resend (when provider: resend)
resend_api_key: "re_..."

# Brevo (when provider: brevo)
brevo_api_key: "xkeysib-..."
brevo_smtp_key: "xsmtpsib-..."
brevo_smtp_login: "xxx@smtp-brevo.com"

default_forward_to: "you@gmail.com"

# Managed by mailctl add/remove — don't edit by hand
domains:
  - domain: yourdomain.com
    cloudflare_zone_id: "..."
    resend_domain_id: "..."   # only present when provider: resend
    managed_dns_record_ids:   # records mailctl created — teardown deletes only these
      - "abc123..."
    aliases:
      - alias: hello
        forward_to: [you@gmail.com]

# For sending emails from flows. default_from is required for flows on Resend;
# mailctl init asks for it. The host/port/user/pass fields are Brevo-only.
smtp:
  host: "smtp-relay.brevo.com"
  port: 587
  user: "xxx@smtp-brevo.com"
  pass: "xsmtpsib-..."
  default_from: "hello@yourdomain.com"
```

## Sending providers

mailctl can send through **Resend** (default) or **Brevo**. Pick one with the
`provider` field in config (or during `mailctl init`).

| | Resend | Brevo |
|---|---|---|
| Config | `resend_api_key` | `brevo_api_key`, `brevo_smtp_key`, `brevo_smtp_login` |
| Domain auth | by domain ID (stored as `resend_domain_id`) | by domain name |
| Senders | implicit — any address on a verified domain | explicit sender per alias |
| Flow `email.send` | HTTP API | SMTP |
| Gmail Send-As SMTP | `smtp.resend.com` (user `resend`, password = API key) | `smtp-relay.brevo.com` |

Switching providers only affects **sending** — Cloudflare receiving is
identical either way. Re-run `mailctl add` for a domain after switching so it's
registered with the new provider.

A config written before Resend support — no `provider` field, Brevo credentials
present, no `resend_api_key` — keeps using Brevo. Set `provider: resend` (or
re-run `mailctl init`) to switch.

## Deliverability

Correct SPF and DKIM prove a message is authentic, but on their own they do not
tell a receiver what to do when a message fails those checks. Without a
published DMARC policy many providers fall back to their own heuristics, which
is the usual reason mail from a properly authenticated domain still lands in
spam.

`mailctl add` publishes a starter policy at `_dmarc.<domain>`:

```
v=DMARC1; p=none; rua=mailto:<your forward-to address>
```

`p=none` is deliberate. It asks receivers to report on failures without acting
on them, which is what Resend recommends adopting first — starting at
`quarantine` or `reject` can silently blackhole legitimate mail from services
you forgot were sending as your domain.

**If the domain already has a DMARC record, mailctl leaves it exactly as it
is.** An existing policy is assumed deliberate.

Once the aggregate reports arriving at your `rua` address look clean — usually
a couple of weeks — tighten the policy by hand in Cloudflare:

```
v=DMARC1; p=quarantine; pct=100; rua=mailto:you@example.com
```

## Send log

```bash
mailctl logs                               # last 20 sent emails
mailctl logs -n 100                        # more history
mailctl logs -d yourdomain.com             # only mail sent from this domain
mailctl logs -s bounced                    # only bounces
mailctl logs --json                        # machine-readable
```

Each entry shows its last delivery event — `delivered`, `bounced`,
`complained`, `opened`, `clicked`, `delivery_delayed` — colour-coded so bounces
and complaints stand out, since those are what damage sending reputation.

**Outbound only.** This reads Resend's send log, so it covers mail your domains
*sent*. Mail *received* at your domains is forwarded by Cloudflare Email
Routing, which exposes no delivery-history API — check the destination inbox for
those. The command requires `provider: resend`; Brevo has no equivalent log.

## Teardown

`mailctl remove` deletes only the DNS records mailctl created. Every record it
adds is recorded by Cloudflare record ID under `managed_dns_record_ids`, and
teardown deletes exactly that set — so a Google Workspace or Mailchimp DKIM key
on the same zone is never touched.

```bash
mailctl remove yourdomain.com --dry-run   # list what would go, change nothing
mailctl remove yourdomain.com
```

Teardown also disables the catch-all rule. That rule lives at its own Cloudflare
endpoint and never appears in the routing-rules list, so without an explicit
step every address on the domain would keep forwarding to the old destination
after the domain looked removed.

**Domains added before record tracking existed have nothing recorded, and
teardown will skip DNS entirely rather than guess from record names.** Delete
their SPF/DKIM records by hand, or re-run `mailctl remove` then `mailctl add` to
get a tracked setup.

## Prerequisites

- **Go 1.22+** — [install Go](https://go.dev/dl/)
- **A domain on Cloudflare** — DNS must be managed by Cloudflare
- **A sending account** — a [Resend](https://resend.com) account with an API key **or** a [Brevo](https://brevo.com) account (free tier works, needed for SMTP)

### Cloudflare API Token

Create one at [dash.cloudflare.com/profile/api-tokens](https://dash.cloudflare.com/profile/api-tokens) with these permissions:

| Permission | Access |
|---|---|
| Zone > DNS | Edit |
| Zone > Email Routing Rules | Edit |
| Zone > Zone Settings | Edit |
| Account > Email Routing Addresses | Edit |

Email Routing Addresses is an **Account**-level permission — it does not appear
in the Zone dropdown. `mailctl add` creates the destination address for you, so
Read is not enough. Zone Settings covers the routing enable/status endpoints;
without it `add` and `check` still work but report those steps as warnings.

`mailctl init` probes the token against every endpoint it needs and tells you
exactly which permission is missing before you set up a domain.

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

On Brevo, check the `smtp` section in `~/.mailctl.yaml` — the `user` and `pass` fields use your Brevo SMTP credentials (different from the API key). On Resend, flows send over the HTTP API instead, so make sure `resend_api_key` is set and the sender domain is verified.

## License

MIT
