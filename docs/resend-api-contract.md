# Resend API contract

Researched 2026-08-21 against primary sources only: `resend.com/docs`, the official API reference, and the official `resend/resend-go` and `resend/resend-node` SDK sources on GitHub. No blog posts or third-party write-ups were used.

Where the hosted docs and the SDK source disagree, both are recorded and the disagreement is called out.

Base URL: `https://api.resend.com`. Auth: `Authorization: Bearer re_xxxxxxxxx`.

---

## 1. `POST /domains` — create domain

**Verified against:** <https://resend.com/docs/api-reference/domains/create-domain> and <https://github.com/resend/resend-go/blob/main/domains.go>

### Request body

`name` is the only required field. `region` **does** exist and is optional.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `name` | string | yes | The domain, e.g. `example.com` |
| `region` | string | no | Default `us-east-1`. One of `us-east-1`, `eu-west-1`, `sa-east-1`, `ap-northeast-1` |
| `custom_return_path` | string | no | Default `send` |
| `open_tracking` | boolean | no | |
| `click_tracking` | boolean | no | |
| `tracking_subdomain` | string | no | |
| `tls` | string | no | Default `opportunistic`; also `enforced` |
| `capabilities` | object | no | `{ "sending": "enabled"\|"disabled", "receiving": "enabled"\|"disabled" }` |

So `{"name": "example.com"}` is a valid, complete request body. The region defaults server-side.

The Go SDK's `CreateDomainRequest` confirms the same shape, with everything except `Name` carrying `omitempty`:

```go
type CreateDomainRequest struct {
	Name              string              `json:"name"`
	Region            string              `json:"region,omitempty"`
	CustomReturnPath  string              `json:"custom_return_path,omitempty"`
	TrackingSubdomain string              `json:"tracking_subdomain,omitempty"`
	OpenTracking      *bool               `json:"open_tracking,omitempty"`
	ClickTracking     *bool               `json:"click_tracking,omitempty"`
	Capabilities      *DomainCapabilities `json:"capabilities,omitempty"`
}
```

### Response — yes, `records` is present at creation time

This is the critical question and the answer is **yes**. The creation response carries the full DNS record set, so there is no need to follow up with a `GET` just to learn what to publish.

```json
{
  "id": "4dd369bc-aa82-4ff3-97de-514ae3000ee0",
  "name": "example.com",
  "created_at": "2026-03-28 17:12:02.059593+00",
  "status": "not_started",
  "open_tracking": true,
  "click_tracking": false,
  "tracking_subdomain": "links",
  "capabilities": { "sending": "enabled", "receiving": "disabled" },
  "records": [
    {
      "record": "SPF",
      "name": "send",
      "type": "MX",
      "ttl": "Auto",
      "status": "not_started",
      "value": "feedback-smtp.us-east-1.amazonses.com",
      "priority": 10
    }
  ],
  "region": "us-east-1"
}
```

### Record fields

**Verified against:** <https://github.com/resend/resend-node/blob/main/src/domains/interfaces/domain.ts>

Every record has `record`, `name`, `type`, `ttl`, `status`, `value`. `priority` is present only on MX records — it is optional in both SDKs.

- `record` — the label/purpose: `SPF`, `DKIM`, `Tracking`, `TrackingCAA`, `Receiving`
- `type` — the actual DNS type: `MX`, `TXT`, `CNAME`, `CAA`
- `ttl` — a **string**, and the documented value is the literal `"Auto"`, not a number. Do not model this as an int.
- `priority` — the Go SDK types this as `json.Number` with `omitempty`, the Node SDK as optional `number`.

Note the Go SDK's `CreateDomainResponse` uses `"createdAt"` (camelCase) while `Domain` uses `"created_at"`. mailctl does not read this field, so it is not a live issue, but it is a known inconsistency in the SDK.

Additional optional per-record fields appearing in the Node SDK types: `routing_policy`, `proxy_status` (`enable` | `disable`).

---

## 2. `GET /domains/{id}` — retrieve domain

**Verified against:** <https://resend.com/docs/api-reference/domains/get-domain>

Yes — the response includes `records` with a current per-record `status`. This is the endpoint to poll while waiting for DNS propagation.

```json
{
  "object": "domain",
  "id": "d91cd9bd-1176-453e-8fc1-35364d380206",
  "name": "example.com",
  "status": "not_started",
  "created_at": "2026-04-26 20:21:26.347412+00",
  "region": "us-east-1",
  "open_tracking": true,
  "click_tracking": false,
  "tracking_subdomain": "links",
  "capabilities": { "sending": "enabled", "receiving": "disabled" },
  "records": [
    {
      "record": "SPF",
      "name": "send",
      "type": "MX",
      "ttl": "Auto",
      "status": "not_started",
      "value": "feedback-smtp.us-east-1.amazonses.com",
      "priority": 10
    },
    {
      "record": "SPF",
      "name": "send",
      "value": "\"v=spf1 include:amazonses.com ~all\"",
      "type": "TXT",
      "ttl": "Auto",
      "status": "not_started"
    },
    {
      "record": "DKIM",
      "name": "resend._domainkey",
      "value": "p=MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDsc4Lh8xilsngyKEgN2S84...",
      "type": "TXT",
      "status": "not_started",
      "ttl": "Auto"
    },
    {
      "record": "Tracking",
      "name": "links.example.com",
      "type": "CNAME",
      "value": "links1.resend-dns.com",
      "ttl": "Auto",
      "status": "not_started"
    }
  ]
}
```

The single-domain response carries `object: "domain"` at the root; the response body is the domain itself, not wrapped in `data`.

---

## 3. `POST /domains/{id}/verify` — verify domain

**Verified against:** <https://resend.com/docs/api-reference/domains/verify-domain>

- Method `POST`, path `/domains/{domain_id}/verify`
- **No request body.**
- Response is a minimal acknowledgement — it does **not** return the verified status:

```json
{
  "object": "domain",
  "id": "d91cd9bd-1176-453e-8fc1-35364d380206"
}
```

Verification is **asynchronous**. The call kicks off a check, temporarily marks the domain `pending`, and emits `domain.updated` webhook events as it progresses. A `200` here means "check started", not "domain is verified". To learn the outcome you must poll `GET /domains/{id}` or consume the webhook.

The Go SDK returns `(bool, error)` for verify rather than a struct, consistent with the response carrying no useful payload.

---

## 4. `GET /domains` — list domains

**Verified against:** <https://resend.com/docs/api-reference/domains/list-domains> and <https://github.com/resend/resend-go/blob/main/domains.go>

Yes — wrapped in `data`. There are also `object` and `has_more` keys at the root.

```json
{
  "object": "list",
  "has_more": false,
  "data": [
    {
      "id": "d91cd9bd-1176-453e-8fc1-35364d380206",
      "name": "example.com",
      "status": "not_started",
      "created_at": "2026-04-26 20:21:26.347412+00",
      "region": "us-east-1",
      "open_tracking": true,
      "click_tracking": false,
      "capabilities": { "sending": "enabled", "receiving": "disabled" }
    }
  ]
}
```

```go
type ListDomainsResponse struct {
	Object  string   `json:"object"`
	Data    []Domain `json:"data"`
	HasMore bool     `json:"has_more"`
}
```

**The list items do not include `records`.** Only `GET /domains/{id}` and `POST /domains` return the DNS record set. Any code that looks up a domain by name via the list endpoint and then expects to read records off the result will get an empty slice.

`has_more` implies pagination exists. Code that ignores it will silently miss domains once an account exceeds one page.

---

## 5. `DELETE /domains/{id}` — delete domain

**Verified against:** <https://resend.com/docs/api-reference/domains/delete-domain>

```bash
curl -X DELETE 'https://api.resend.com/domains/d91cd9bd-1176-453e-8fc1-35364d380206' \
     -H 'Authorization: Bearer re_xxxxxxxxx'
```

```json
{
  "object": "domain",
  "id": "d91cd9bd-1176-453e-8fc1-35364d380206",
  "deleted": true
}
```

---

## 6. `POST /emails` — send email

**Verified against:** <https://resend.com/docs/api-reference/emails/send-email>

### Body parameters

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `from` | string | yes | `Name <email@example.com>` supported |
| `to` | string \| string[] | yes | Max 50 recipients |
| `subject` | string | yes | |
| `html` | string | at least one of | |
| `text` | string | at least one of | If omitted, plain text is auto-generated from the HTML |
| `react` | ReactNode | at least one of | Node SDK only |
| `template` | object | at least one of | `{ id, variables }` |
| `cc`, `bcc` | string \| string[] | no | |
| `reply_to` | string \| string[] | no | snake_case on the wire |
| `scheduled_at` | string | no | Natural language or ISO 8601 |
| `headers` | object | no | |
| `tags` | array | no | |
| `topic_id` | string | no | |
| `attachments` | array | no | Max 40 MB total per email |

An optional `Idempotency-Key` request header prevents duplicate sends; it should be unique per request, expires after 24 hours, max 256 characters.

### Attachments — field names

**Verified against:** <https://resend.com/docs/api-reference/emails/send-email>, <https://github.com/resend/resend-node/blob/main/src/common/utils/parse-email-to-api-options.ts>, <https://github.com/resend/resend-go/blob/main/emails.go>

The wire format is **snake_case**: `content_type`, not `contentType`.

| Field | Notes |
| --- | --- |
| `filename` | Name of the attached file |
| `content` | The file content — see encoding below |
| `path` | Alternative to `content`: a URL where the file is hosted |
| `content_type` | MIME type; derived from `filename` if omitted |
| `content_id` | For inline images referenced as `<img src="cid:...">` |

`contentType` is the *Node SDK's* public TypeScript surface only. The SDK explicitly maps it to snake_case before it hits the API:

```ts
function parseAttachments(attachments) {
  return attachments?.map((attachment) => ({
    content: attachment.content,
    filename: attachment.filename,
    path: attachment.path,
    content_type: attachment.contentType,
    content_id: attachment.contentId,
  }));
}
```

The Go SDK's custom `MarshalJSON` independently confirms the same wire key:

```go
func (a *Attachment) MarshalJSON() ([]byte, error) {
	na := struct {
		Content         []int  `json:"content,omitempty"`
		Filename        string `json:"filename,omitempty"`
		Path            string `json:"path,omitempty"`
		ContentType     string `json:"content_type,omitempty"`
		ContentId       string `json:"content_id,omitempty"`
		InlineContentId string `json:"inline_content_id,omitempty"`
	}{ ... }
	return json.Marshal(na)
}
```

Two independent official SDKs both emit `content_type`. That is settled.

### Attachment `content` encoding — the two SDKs disagree

This is the one place where primary sources genuinely conflict, so both are recorded.

- **The docs** describe `content` as "buffer | string", with the string form being **Base64**.
- **The Node SDK** passes `content` through untouched, so a JS `string` serialises as a JSON string (Base64) and a `Buffer` serialises via `Buffer`'s own JSON form. The Base64 string path is a documented, supported wire form.
- **The Go SDK** converts `[]byte` into `[]int` — a JSON **array of byte values** — via `BytesToIntArray`, not a Base64 string.

Read together: the API accepts **both** a Base64 string and a numeric byte array for `content`. The Base64 string is the form the docs describe and the form the Node SDK produces, so it is the safer choice for a hand-rolled client. mailctl's Base64 string approach is consistent with the documentation and with the Node SDK; it is not what the Go SDK does.

Not yet verified by live request: I did not send a real email to confirm the API accepts a Base64 string from a non-SDK client. The claim rests on the docs plus Node SDK behaviour, which is strong but not an executed test.

---

## 7. Is `text` or `html` required? What about `text: ""`?

**Verified against:** <https://resend.com/docs/api-reference/emails/send-email> and <https://github.com/resend/resend-node/blob/main/src/emails/interfaces/create-email-options.interface.ts>

**Yes, at least one is required.** The docs state you must provide at least one of `html`, `text`, `react`, or `template`. The Node SDK enforces this at the type level with a `RequireAtLeastOne` helper over an `EmailRenderOptions` interface containing exactly `react`, `html`, and `text` — so the constraint is structural, not advisory.

**On `text: ""`** — the docs do not state what happens with an empty string, and I did not send a live request to find out. This is **unverified**, and it matters because mailctl deliberately relies on it.

The reasonable expectation is that `""` fails validation rather than satisfying the requirement. Resend's error codes cover this shape of failure (<https://resend.com/docs/api-reference/errors>):

- `missing_required_field` — 422, "The request body is missing one or more required fields."
- `validation_error` — 400, "An error was found with one or more fields in the request."

Sending `text: ""` to satisfy "at least one of text/html" is betting on an undocumented behaviour. An empty-bodied email is not a meaningful thing to send in the first place, so the better fix is to reject it client-side rather than to probe what the server tolerates.

---

## 8. Domain status values

**Verified against:** <https://github.com/resend/resend-go/blob/main/domains.go> and <https://github.com/resend/resend-node/blob/main/src/domains/interfaces/domain.ts>

The hosted API reference does not enumerate these — its examples only ever show `not_started`. Both official SDKs define the full set identically, which makes them the authoritative source here.

**Domain status** (six values):

```ts
export type DomainStatus =
  | 'pending'
  | 'verified'
  | 'failed'
  | 'not_started'
  | 'partially_verified'
  | 'partially_failed';
```

**Per-record status** (five values — a different set):

```ts
export type DomainRecordStatus =
  | 'pending'
  | 'verified'
  | 'failed'
  | 'temporary_failure'
  | 'not_started';
```

Two things worth internalising:

1. **Yes, the verified state is literally the lowercase string `"verified"`.** Both SDKs agree.
2. **The two enums are not the same.** `temporary_failure` occurs only at record level; `partially_verified` and `partially_failed` only at domain level. Code that reuses one status helper for both will mishandle cases.

`temporary_failure` is worth special handling: it signals a transient condition that may resolve on retry, so treating it as terminal failure will produce false negatives during DNS propagation.

---

## 9. DNS record `name` values — host-relative or FQDN?

**Verified against:** <https://resend.com/docs/api-reference/domains/get-domain>

**Mixed. This is a trap.** Resend returns some names host-relative and at least one fully qualified, in the same `records` array.

From the documented example for `example.com`:

| `record` | `name` | Form |
| --- | --- | --- |
| SPF (MX) | `send` | host-relative |
| SPF (TXT) | `send` | host-relative |
| DKIM | `resend._domainkey` | host-relative |
| Tracking | `links.example.com` | **fully qualified** |

The SPF and DKIM names are relative to the domain — `send` means `send.example.com`, `resend._domainkey` means `resend._domainkey.example.com`. But the Tracking CNAME comes back as `links.example.com`, already including the apex.

The tracking name is built from the `tracking_subdomain` setting (`links` in the example), so its exact value varies per domain configuration.

Practical consequence: naively appending `.{domain}` to every returned `name` produces `links.example.com.example.com` for the tracking record. Any code that constructs FQDNs from these values must first check whether the name already ends with the domain. mailctl currently only creates SPF/DKIM-shaped records, so this has not bitten yet — but it will the moment tracking records are handled.

---

## 10. SMTP settings

**Verified against:** <https://resend.com/docs/send-with-smtp>

| Setting | Value |
| --- | --- |
| Host | `smtp.resend.com` |
| Ports | `25`, `465`, `587`, `2465`, `2587` |
| Username | the literal string `resend` |
| Password | your Resend API key (`re_...`) |

The docs give the username as `resend` and the password as `YOUR_API_KEY`. So yes — the username is a fixed literal, not the account email and not the API key. The API key goes in the password field.

Port 587 is supported. The `2465` / `2587` alternatives exist for networks where the standard ports are blocked, which is a cloud-provider concern and not relevant to Gmail Send-As.

---

## 11. Gmail "Send mail as" constraints

**Verified against:** <https://support.google.com/mail/answer/22370>

Gmail's Send-As supports:

- **TLS on port 25 or 587**
- **SSL on port 465**
- Port 25 unsecured (Google explicitly discourages this)

Google requires that the third-party provider "supports SSL or TLS with a valid certificate".

**Resend and Gmail are compatible.** Port 587 with TLS is supported on both sides, so the intended configuration works:

| Gmail field | Value |
| --- | --- |
| SMTP Server | `smtp.resend.com` |
| Port | `587` |
| Username | `resend` |
| Password | Resend API key |
| Security | TLS |

Port 465 with SSL is a valid fallback if 587 is blocked. Ports `2465` and `2587` are **not** in Gmail's supported list — do not offer them as Gmail fallbacks even though Resend accepts them.

**One thing to flag, unrelated to correctness:** Google's own page states that from **January 2027**, Gmail will stop supporting Send-As for third-party email addresses such as `@yahoo.com` or `@outlook.com`. Google says this "does not affect Google Workspace or other Gmail addresses you own". Whether a custom domain routed through Resend counts as an address the user "owns" is not spelled out on that page. Given mailctl's whole purpose is Gmail Send-As on custom domains, this deadline is worth tracking — it is not actionable today and the wording may well exclude this case, but it is a dependency with a published end date.

---

## Discrepancies to check in mailctl

Assumptions in `/Users/sisle/mailctl/internal/resend/client.go` measured against the verified contract above.

### Correct — no change needed

| Assumption | Verdict |
| --- | --- |
| Sends `{"name": domain}` to `POST /domains` | **Correct.** `name` is the only required field; `region` is optional and defaults to `us-east-1` server-side. |
| Reads `records` from the `POST /domains` response | **Correct.** The creation response does include the full `records` array. |
| Treats status `"verified"` as authenticated | **Correct on the string.** The literal value is `"verified"`. See the caveats below on case and on the states it ignores. |
| Attachments as `{"filename", "content", "content_type"}` | **Correct.** `content_type` is confirmed snake_case by both official SDKs. Base64 string `content` matches the docs and the Node SDK. |
| List response is `{"data": [...]}` | **Correct**, though it ignores `has_more` — see below. |
| SMTP username `resend`, port 587 | **Correct.** Both are documented, and 587/TLS is in Gmail's supported set. |

### Issues found

**1. `body["text"] = ""` relies on undocumented behaviour — highest risk**

`client.go`:

```go
// Resend requires at least one of text/html.
if p.Text == "" && p.HTML == "" {
	body["text"] = ""
}
```

The comment is right that one of them is required, but the workaround assumes an empty string satisfies that requirement. Nothing in the docs supports this, and the expected outcome is a 422 `missing_required_field` or 400 `validation_error`. I could not confirm either way without sending a live request.

The failure mode is bad: the send fails at the API with a validation error that surfaces as a generic `resend API error (status 422)`, rather than as a clear local "refusing to send an empty email". Suggest failing fast client-side instead:

```go
if p.Text == "" && p.HTML == "" {
	return fmt.Errorf("%w: email must have text or html body", ErrInvalidMessage)
}
```

**2. `Authenticated()` collapses four non-verified states into "not ready"**

```go
func (d *Domain) Authenticated() bool {
	return strings.EqualFold(d.Status, "verified")
}
```

Two separate points:

- **The case-insensitive compare is harmless but misleading.** Resend returns lowercase `"verified"`; `EqualFold` implies uncertainty that does not exist. Not a bug.
- **The real gap is the states it does not distinguish.** `partially_verified`, `partially_failed`, and `failed` all return `false`, identically to `pending` and `not_started`. So a domain that has hard-failed verification is indistinguishable from one still propagating, and a `check` loop will wait forever on a domain that will never verify. Worth surfacing `failed` and `partially_failed` as terminal so the user is told to fix their DNS rather than left watching a spinner.

**3. `DNSRecord.Priority` is `int` but Resend sends `json.Number`, and `TTL` is a string**

```go
Priority int    `json:"priority"`
TTL      string `json:"ttl"`
```

`TTL` as `string` is correct — the API returns the literal `"Auto"`.

`Priority` as a plain `int` is the risk. The official Go SDK types this field as `json.Number` specifically because the API is not guaranteed to send it as a bare integer; if it ever arrives quoted (`"10"`), `encoding/json` will fail the entire `Domain` unmarshal with a type error, taking the whole response down over one optional field. Matching the SDK (`json.Number`) or using `*int` would be more robust. Also note `priority` is absent on non-MX records, so it silently zero-values there — fine as long as callers only read it for MX.

**4. `FindDomainByName` returns domains with no `records`**

```go
func (c *Client) FindDomainByName(name string) (*Domain, error) {
	domains, err := c.ListDomains()
	...
}
```

The list endpoint does not return `records` — only `POST /domains` and `GET /domains/{id}` do. So any `*Domain` from this function always has `Records == nil`. If a caller looks a domain up by name and then reads `.Records`, it gets an empty slice with no error, which reads as "this domain needs no DNS records" rather than "records were not fetched". Callers needing records must follow up with `GetDomain(d.ID)`.

Worth confirming how `cmd/check.go` and `internal/tui/` consume this — I traced the client but did not audit every call site.

**5. `ListDomains` ignores `has_more`**

```go
type listDomainsResponse struct {
	Data []Domain `json:"data"`
}
```

`has_more` is dropped, so pagination is not handled. On an account with enough domains to paginate, `ListDomains` silently returns a partial list and `FindDomainByName` reports "not found" for a domain that exists. Low impact at current scale, but it fails silently and wrongly rather than erroring.

**6. `VerifyDomain` success does not mean verified**

```go
func (c *Client) VerifyDomain(id string) error {
	_, _, err := c.do("POST", "/domains/"+id+"/verify", nil)
	return err
}
```

The signature is accurate and the empty body is correct. The hazard is at the call sites: verification is asynchronous, the response contains only `object` and `id`, and the domain is moved to `pending`. A `nil` error means "check started", not "domain verified". Any caller treating a successful `VerifyDomain` as confirmation is wrong and must poll `GetDomain` (or consume the `domain.updated` webhook) for the outcome.

**7. Record `name` values are not uniformly host-relative**

`DNSRecord.Name` holds whatever Resend sends, and that is inconsistent: `send` and `resend._domainkey` are relative, while the tracking CNAME arrives as `links.example.com`, already fully qualified. Any code appending `.{domain}` unconditionally will emit `links.example.com.example.com`.

Not currently triggered, since mailctl handles SPF/DKIM-shaped records. It becomes a live bug as soon as tracking records are in scope. The DNS-publishing path in `internal/tui/aliases.go` is where I would check this before adding tracking support.

### Not verified

- The behaviour of `text: ""` against the live API — needs one real request against a test key to settle item 1.
- Whether the API accepts a Base64 string `content` from a non-SDK client. The docs and the Node SDK both say yes; the Go SDK sends a byte array instead. mailctl follows the documented form.
- Call sites of `FindDomainByName` and `VerifyDomain` — I read `internal/resend/client.go` and grepped for SMTP constants, but did not audit every consumer in `cmd/` and `internal/tui/`.
