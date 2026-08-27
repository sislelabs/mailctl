package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/store"
)

// testServer builds a panel over an in-memory config, so handler tests touch
// no filesystem and make no API calls.
func testServer(t *testing.T) http.Handler {
	t.Helper()

	cfg := &internal.Config{
		Provider:         internal.ProviderResend,
		DefaultForwardTo: "you@example.com",
	}
	cfg.AddDomain("example.com", "zone1", []internal.Alias{
		{Alias: "hello", ForwardTo: []string{"you@example.com"}},
	})

	srv, err := NewServer(&store.Memory{Config: cfg})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv.Handler()
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestOverviewRenders(t *testing.T) {
	rec := get(t, testServer(t), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"example.com", "hello@", "mailctl"} {
		if !strings.Contains(body, want) {
			t.Errorf("overview missing %q", want)
		}
	}
	// The audit is loaded lazily; blocking the page on several API round trips
	// would make the panel feel broken on a slow network.
	if !strings.Contains(body, `hx-get="/audit"`) {
		t.Error("overview should defer the audit over htmx")
	}
}

func TestDomainPageRenders(t *testing.T) {
	rec := get(t, testServer(t), "/domains/example.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "example.com") {
		t.Error("domain page missing the domain")
	}
	if !strings.Contains(body, "not registered") {
		t.Error("a domain with no Resend ID should say so")
	}
	// Addresses are read live from Cloudflare, so the page ships without them
	// and htmx fills them in. Rendering the cached config values instead would
	// show a destination that may no longer be where mail goes.
	if !strings.Contains(body, `hx-get="/domains/example.com/aliases"`) {
		t.Error("domain page should defer the alias list over htmx")
	}
}

func TestAliasListDegradesWithoutCloudflare(t *testing.T) {
	// The test config carries no usable token. The fragment must still render
	// and say so, rather than erroring the whole page.
	rec := get(t, testServer(t), "/domains/example.com/aliases")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Could not read live routing") {
		t.Errorf("expected a degraded message, got:\n%s", rec.Body.String())
	}
}

func TestUnknownDomainIs404(t *testing.T) {
	if rec := get(t, testServer(t), "/domains/nope.com"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestAddAliasRejectsBadInputWithoutCallingCloudflare(t *testing.T) {
	h := testServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/domains/example.com/aliases",
		strings.NewReader("alias=foo@bar"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)

	// The handler renders the error into the swapped fragment rather than
	// returning a bare error status, since htmx swaps the response body in.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not a valid alias") {
		t.Errorf("expected a validation message, got:\n%s", rec.Body.String())
	}
}

func TestDuplicateAliasIsRejected(t *testing.T) {
	h := testServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/domains/example.com/aliases",
		strings.NewReader("alias=hello"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("expected a duplicate message, got:\n%s", rec.Body.String())
	}
}

func TestStaticAssetIsServed(t *testing.T) {
	// htmx is vendored rather than pulled from a CDN so the binary is
	// self-contained and the panel works offline.
	rec := get(t, testServer(t), "/static/htmx.min.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.Len() < 1000 {
		t.Errorf("htmx looks truncated: %d bytes", rec.Body.Len())
	}
}

func post(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)
	return rec
}

func TestAddDomainRejectsBadInputBeforeStartingAJob(t *testing.T) {
	rec := post(t, testServer(t), "/domains", "domain=notadomain&aliases=hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "is not a domain name") {
		t.Errorf("expected validation message, got:\n%s", body)
	}
	// A rejected form must come back as the form, not as a progress view that
	// would fail on its first step.
	if strings.Contains(body, "/jobs/") {
		t.Error("no job should have been started")
	}
}

func TestAddDomainRejectsAnAlreadyConfiguredDomain(t *testing.T) {
	rec := post(t, testServer(t), "/domains", "domain=example.com&aliases=hello")
	if !strings.Contains(rec.Body.String(), "already configured") {
		t.Errorf("expected a duplicate message, got:\n%s", rec.Body.String())
	}
}

func TestUnknownJobIs404(t *testing.T) {
	if rec := get(t, testServer(t), "/jobs/deadbeef"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestRemoveConfirmShowsScope(t *testing.T) {
	rec := get(t, testServer(t), "/domains/example.com/remove")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	// The domain in the fixture has no tracked records, so the prompt must say
	// DNS is left alone rather than implying a broader sweep.
	if !strings.Contains(body, "none are tracked") {
		t.Errorf("expected the DNS scope to be spelled out, got:\n%s", body)
	}
	if !strings.Contains(body, "type example.com to confirm") {
		t.Error("expected a typed confirmation prompt")
	}
}

func TestRemoveRequiresTheExactDomainName(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/domains/example.com", strings.NewReader("confirm=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	testServer(t).ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "Type the domain name exactly") {
		t.Errorf("expected a refusal, got:\n%s", body)
	}
	if strings.Contains(body, "/jobs/") {
		t.Error("a teardown must not start without an exact confirmation")
	}
}

func TestRemoveConfirmForSendingDomainOmitsRouting(t *testing.T) {
	cfg := &internal.Config{Provider: internal.ProviderResend, DefaultForwardTo: "you@example.com"}
	cfg.AddSendingDomain("info.example.com", "zone1", "example.com")

	srv, err := NewServer(&store.Memory{Config: cfg})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	rec := get(t, srv.Handler(), "/domains/info.example.com/remove")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()

	// The catch-all belongs to example.com, which serves other mail. Promising
	// to disable it here would be describing damage to a different domain.
	for _, forbidden := range []string{"Catch-all forwarding", "routing rules for every address"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("sending-domain teardown must not mention %q", forbidden)
		}
	}
	if !strings.Contains(body, "Routing and catch-all on example.com are not touched") {
		t.Errorf("expected an explicit note that the zone is untouched, got:\n%s", body)
	}
	if !strings.Contains(body, "receives nothing") {
		t.Error("expected the sending-domain wording")
	}
}

func sendingServer(t *testing.T) http.Handler {
	t.Helper()
	cfg := &internal.Config{Provider: internal.ProviderResend, DefaultForwardTo: "you@example.com"}
	cfg.AddSendingDomain("info.example.com", "zone1", "example.com")
	srv, err := NewServer(&store.Memory{Config: cfg})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv.Handler()
}

func TestSendingDomainPageHasNoAliasUI(t *testing.T) {
	rec := get(t, sendingServer(t), "/domains/info.example.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()

	// A sending domain has no addresses; offering an alias editor would invite
	// creating routing rules on a zone that serves unrelated mail.
	if strings.Contains(body, "/aliases") {
		t.Error("sending domain page must not load the alias editor")
	}
	if !strings.Contains(body, "sending only") {
		t.Error("expected the kind to be labelled")
	}
	if !strings.Contains(body, "/dns") {
		t.Error("expected the sending DNS view")
	}
}

func TestAliasRoutesRefuseSendingDomains(t *testing.T) {
	h := sendingServer(t)

	// Hiding the UI is not a control. Every mutating alias route must refuse.
	cases := []struct{ method, path, body string }{
		{"GET", "/domains/info.example.com/aliases", ""},
		{"POST", "/domains/info.example.com/aliases", "alias=hello"},
		{"DELETE", "/domains/info.example.com/aliases/hello", ""},
		{"POST", "/domains/info.example.com/aliases/hello/forward", "forward_to=x@y.com"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s = %d, want 400", c.method, c.path, rec.Code)
		}
	}
}

func TestMailboxDomainStillHasAliasUI(t *testing.T) {
	rec := get(t, testServer(t), "/domains/example.com")
	if !strings.Contains(rec.Body.String(), "/domains/example.com/aliases") {
		t.Error("mailbox domain lost its alias editor")
	}
}

func TestOverviewSeparatesKinds(t *testing.T) {
	rec := get(t, sendingServer(t), "/")
	body := rec.Body.String()
	if !strings.Contains(body, "Mailbox domains") || !strings.Contains(body, "Sending domains") {
		t.Error("overview should list the two kinds separately")
	}
	// The sending domain must appear under its own heading, not among mailboxes.
	mailboxIdx := strings.Index(body, "Mailbox domains")
	sendingIdx := strings.Index(body, "Sending domains")
	domainIdx := strings.Index(body, "info.example.com")
	if !(domainIdx > sendingIdx && sendingIdx > mailboxIdx) {
		t.Errorf("sending domain listed in the wrong section (mailbox=%d sending=%d domain=%d)",
			mailboxIdx, sendingIdx, domainIdx)
	}
}

func TestGmailFragmentDoesNotLeakTheSecret(t *testing.T) {
	cfg := &internal.Config{
		Provider:         internal.ProviderResend,
		ResendAPIKey:     "re_supersecretvalue",
		DefaultForwardTo: "you@example.com",
	}
	cfg.AddDomain("example.com", "zone1", []internal.Alias{
		{Alias: "hello", ForwardTo: []string{"you@example.com"}},
	})
	srv, err := NewServer(&store.Memory{Config: cfg})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	rec := get(t, srv.Handler(), "/domains/example.com/gmail")
	body := rec.Body.String()

	// The panel is local, but a rendered credential is one screenshot away
	// from disclosure and these pages get shared. It must be fetched, not
	// merely hidden with markup.
	if strings.Contains(body, "re_supersecretvalue") {
		t.Error("the SMTP password must not be in the default fragment")
	}
	if !strings.Contains(body, "reveal") {
		t.Error("expected a reveal control")
	}

	// And it must be reachable when actually asked for.
	rec = get(t, srv.Handler(), "/domains/example.com/gmail/password")
	if !strings.Contains(rec.Body.String(), "re_supersecretvalue") {
		t.Error("the reveal endpoint should return the password")
	}
}

func TestGmailRefusedForSendingDomains(t *testing.T) {
	// Gmail confirms ownership by emailing a code to the address, so a domain
	// that cannot receive can never complete the flow.
	rec := get(t, sendingServer(t), "/domains/info.example.com/gmail")
	if !strings.Contains(rec.Body.String(), "cannot receive Gmail") {
		t.Errorf("expected an explanation, got:\n%s", rec.Body.String())
	}
}

func TestOverviewDefersCatchAll(t *testing.T) {
	rec := get(t, testServer(t), "/")
	body := rec.Body.String()

	// The catch-all is a live read at its own Cloudflare endpoint. Rendering
	// config instead would show the first alias's destination, which is a
	// different value that only happens to agree.
	if !strings.Contains(body, `hx-get="/domains/example.com/catchall"`) {
		t.Error("overview should load the catch-all per row")
	}
	if !strings.Contains(body, "Catch-all delivers to") {
		t.Error("expected the column to be labelled for what it shows")
	}
}

func TestBreadcrumbOnlyOnDomainPages(t *testing.T) {
	if strings.Contains(get(t, testServer(t), "/").Body.String(), `class="crumb`) {
		t.Error("the root page should have no breadcrumb")
	}
	body := get(t, testServer(t), "/domains/example.com").Body.String()
	if !strings.Contains(body, `class="crumb mono">example.com`) {
		t.Error("domain page should name itself in the header")
	}
	// The brand is the way back, so a separate in-page link is redundant.
	if strings.Contains(body, "all domains</a>") {
		t.Error("the stray back link should be gone")
	}
}

func TestDomainSectionsCollapse(t *testing.T) {
	body := get(t, testServer(t), "/domains/example.com").Body.String()

	// Addresses are the reason to open a mailbox domain, so they stay in view;
	// everything under them folds away.
	if strings.Contains(body, "<summary>Addresses") {
		t.Error("Addresses should not be collapsible")
	}
	for _, section := range []string{"Send from Gmail", "Sending DNS", "Details", "Danger zone"} {
		if !strings.Contains(body, "<summary>"+section+"</summary>") {
			t.Errorf("%q should be a collapsible section", section)
		}
	}
	// Nothing on a mailbox domain opens by default.
	if strings.Contains(body, `<details class="section" open>`) {
		t.Error("mailbox sections should start collapsed")
	}
}

func TestSendingDomainOpensItsDNS(t *testing.T) {
	body := get(t, sendingServer(t), "/domains/info.example.com").Body.String()

	// A sending domain has no address list, so its DNS is the primary content
	// and starting it collapsed would leave the page looking empty.
	if !strings.Contains(body, `<details class="section" open>`) {
		t.Error("sending DNS should start open for a sending domain")
	}
	if strings.Contains(body, "Send from Gmail") {
		t.Error("a sending domain cannot be added to Gmail")
	}
}
